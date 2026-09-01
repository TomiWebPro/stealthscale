package cli

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pterm/pterm"
	"github.com/spf13/cobra"
	"github.com/tomiwebpro/stealthscale/hscontrol/xray"
	"golang.org/x/net/http2"
	"tailscale.com/control/controlbase"
	"tailscale.com/tailcfg"
	"tailscale.com/types/key"
)

func init() {
	upCmd.Flags().String("coordinator", "", "Coordinator URL (e.g. https://ctl.example.com)")
	upCmd.Flags().String("authkey", "", "Pre-auth key for registration")
	upCmd.Flags().String("vless-uri", "", "Stealth endpoint URI (vless://<uuid>@<addr>:<port>?security=...)")
	// Alias --endpoint for --vless-uri so --vless doesn't sound lame; VLESS is the
	// default transport anyway (xray.enabled=true, security=reality_xtls), no
	// --vless boolean needed. Holepunching via DERP/STUN is exempt.
	upCmd.Flags().String("endpoint", "", "Alias for --vless-uri")
	_ = upCmd.Flags().MarkHidden("endpoint")
	upCmd.Flags().String("hostname", "", "Hostname to register as (defaults to OS hostname)")
	upCmd.Flags().String("state-dir", "", "Directory to persist machine key (defaults to %ProgramData%\\stealthscale on Windows, ~/.stealthscale or /var/lib/stealthscale elsewhere)")
	upCmd.Flags().Bool("insecure", false, "Skip TLS verification for coordinator (useful for self-signed)")
	upCmd.Flags().Duration("timeout", 15*time.Second, "Dial timeout")
	rootCmd.AddCommand(upCmd)
}

var upCmd = &cobra.Command{
	Use:   "up",
	Short: "Join a StealthScale network as a node (unified client)",
	Long: `Join a StealthScale network as a node using the stealth transport.

Every device runs the same binary; 'up' is the client side of the unified
node. It dials the node's stealth endpoint (from --vless-uri / --endpoint),
performs the VLESS handshake and establishes the stealth transport. VLESS
+ Reality + uTLS is the default transport (no --vless flag needed;
xray.enabled=true, security=reality_xtls); holepunching via DERP/STUN is
exempt. Once two peers have identified each other, all further transport
is stealth per docs/stealthscale/overview.md#bootstrap-discovery-and-steady-state-transport.

Discovery vs stealth:
  Plain HTTPS GET /key is discovery (allowed non-stealth per overview.md#bootstrap);
  VLESS+Noise registration is stealth. DERP fail-closed is gated on stealth — when
  stealth_satisfied is false, DERP/STUN are suppressed (check via 'stscale health'
  or curl /web/api/derp). See docs/stealthscale/overview.md#bootstrap.

Examples:
  # Manual: fetch VLESS URI on coordinator, then paste to client
  stscale nodes vless 1  # on coordinator, prints vless://...
  stscale up --coordinator https://ctl.example.com --authkey <key> --vless-uri 'vless://<uuid>@<addr>:<port>?security=reality_xtls&pbk=...&sid=...&dest=...&fp=chrome'

  # Alias endpoint
  stscale up --coordinator https://ctl.example.com --authkey <key> --endpoint 'vless://...'

  # Check stealth/DERP status
  stscale health
  curl -s http://127.0.0.1:8080/web/api/derp | jq .stealth_satisfied
`,
	RunE: func(cmd *cobra.Command, args []string) error {
		coordinator, _ := cmd.Flags().GetString("coordinator")
		authKey, _ := cmd.Flags().GetString("authkey")
		vlessURI, _ := cmd.Flags().GetString("vless-uri")
		if vlessURI == "" {
			vlessURI, _ = cmd.Flags().GetString("endpoint")
		}
		hostname, _ := cmd.Flags().GetString("hostname")
		stateDir, _ := cmd.Flags().GetString("state-dir")
		insecure, _ := cmd.Flags().GetBool("insecure")
		timeout, _ := cmd.Flags().GetDuration("timeout")

		if vlessURI == "" {
			return fmt.Errorf("--vless-uri (or --endpoint) is required (get it via 'stscale nodes vless <node-id>' on the coordinator)")
		}
		cfg, err := xray.ParseVLESSURI(vlessURI)
		if err != nil {
			return fmt.Errorf("invalid --vless-uri: %w", err)
		}
		if coordinator != "" {
			if _, err := url.Parse(coordinator); err != nil {
				return fmt.Errorf("invalid --coordinator URL %q: %w", coordinator, err)
			}
		}
		if authKey == "" {
			pterm.Warning.Println("no --authkey provided; will attempt interactive registration if the server allows it")
		}
		if hostname == "" {
			if hn, err := os.Hostname(); err == nil {
				hostname = hn
				hostname = strings.Split(hostname, ".")[0]
			} else {
				hostname = "stealth-node"
			}
		}
		pterm.Info.Printf("registering as hostname %q\n", hostname)
		// Enforce VLESS as transport: all traffic except holepunching must be stealth.
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()

		pterm.Info.Printf("dialling VLESS %s@%s:%d (security=%s, dest=%s, fp=%s)...\n", cfg.ID[:8]+"...", cfg.Address, cfg.Port, cfg.Security, cfg.Dest, cfg.FP)
		conn, err := xray.DialVLESS(ctx, cfg)
		if err != nil {
			return fmt.Errorf("VLESS dial failed (stealth transport not satisfied): %w\n\nHints:\n- endpoint must be reachable (firewall: xray.listen_port..max_listen_port)\n- UUID/port derived from xray.secret — ensure coordinator's secret is stable\n- pbk/sid mismatch: verify 'stscale nodes vless <id>' URI matches client (check pbk, sid, dest, fp columns with --verbose)\n- if coordinator's xray.listen_addr is 0.0.0.0, URI contains 0.0.0.0 — override with public IP\n- check 'curl http://<coordinator>/web/api/derp | jq .stealth_satisfied' and 'stscale health'", err)
		}
		defer conn.Close()
		pterm.Success.Printf("VLESS handshake succeeded to %s:%d (security=%s, utls=%s)\n", cfg.Address, cfg.Port, cfg.Security, cfg.FP)

		if coordinator == "" || authKey == "" {
			pterm.Success.Println("stealth verification complete. Provide --coordinator and --authkey to register.")
			return nil
		}

		pterm.Info.Println("stealth transport verified; fetching coordinator noise key and performing noise handshake over VLESS...")

		// Load or generate persistent machine key so the node identity is stable.
		machineKey, err := loadOrCreateMachineKey(stateDir)
		if err != nil {
			return fmt.Errorf("machine key: %w", err)
		}
		nodeKey := key.NewNode()

		// Fetch server's Noise public key via coordinator's /key endpoint.
		// This is done over plain HTTPS (not VLESS) for discovery; the subsequent
		// registration is over VLESS+noise per project policy.
		serverPub, err := fetchServerNoiseKey(ctx, coordinator, insecure)
		if err != nil {
			return fmt.Errorf("fetching coordinator noise key from %s/key: %w", coordinator, err)
		}
		pterm.Info.Printf("coordinator noise key: %s...\n", serverPub.ShortString())

		// Perform Noise handshake over the VLESS stream (replaces WireGuard).
		// This is the same handshake stock Tailscale would do over raw TCP,
		// but now tunneled inside the authenticated VLESS stream.
		noiseConn, err := controlbase.Client(ctx, conn, machineKey, serverPub, uint16(tailcfg.CurrentCapabilityVersion))
		if err != nil {
			return fmt.Errorf("noise handshake over VLESS failed: %w", err)
		}
		defer noiseConn.Close()
		pterm.Success.Println("noise handshake over VLESS succeeded")

		// Now speak the Tailscale machine API over HTTP/2 on top of noise,
		// exactly as the patched Tailscale client would.
		tr := &http2.Transport{
			AllowHTTP: true,
			DialTLSContext: func(context.Context, string, string, *tls.Config) (net.Conn, error) {
				return noiseConn, nil
			},
		}
		defer tr.CloseIdleConnections()

		regReq := tailcfg.RegisterRequest{
			Version:  tailcfg.CurrentCapabilityVersion,
			NodeKey:  nodeKey.Public(),
			Hostinfo: &tailcfg.Hostinfo{Hostname: hostname},
			Auth:     &tailcfg.RegisterResponseAuth{AuthKey: authKey},
		}
		body, err := json.Marshal(regReq)
		if err != nil {
			return fmt.Errorf("marshaling register request: %w", err)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://control/machine/register", bytes.NewReader(body))
		if err != nil {
			return fmt.Errorf("creating register request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")

		pterm.Info.Println("sending RegisterRequest over VLESS+noise (HTTP/2)...")
		resp, err := tr.RoundTrip(req)
		if err != nil {
			return fmt.Errorf("register RoundTrip over VLESS+noise: %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			var errBody bytes.Buffer
			_, _ = errBody.ReadFrom(resp.Body)
			return fmt.Errorf("register failed: HTTP %d: %s", resp.StatusCode, errBody.String())
		}
		var regResp tailcfg.RegisterResponse
		if err := json.NewDecoder(resp.Body).Decode(&regResp); err != nil {
			return fmt.Errorf("decoding register response: %w", err)
		}
		if regResp.Error != "" {
			return fmt.Errorf("register error: %s", regResp.Error)
		}
		if !regResp.MachineAuthorized {
			pterm.Warning.Println("register response: machine not yet authorized (check authkey or approve via coordinator)")
		}
		if regResp.NodeKeyExpired {
			return fmt.Errorf("register response: node key expired")
		}
		pterm.Success.Printf("registered as %s (authorized=%v, nodeKey=%s)\n", hostname, regResp.MachineAuthorized, nodeKey.Public().ShortString())

		// Persist node key for future MapRequests (optional, for now just print).
		if stateDir != "" {
			_ = os.MkdirAll(stateDir, 0700)
			if text, err := nodeKey.MarshalText(); err == nil {
				_ = os.WriteFile(filepath.Join(stateDir, "node.key"), text, 0600)
			}
		}

		pterm.Success.Println("node is ready to serve as coordinator+client (unified). Run 'stscale serve' on this host to also coordinate.")
		return nil
	},
}

func loadOrCreateMachineKey(stateDir string) (key.MachinePrivate, error) {
	var zero key.MachinePrivate
	if stateDir == "" {
		if home, err := os.UserHomeDir(); err == nil {
			stateDir = filepath.Join(home, ".stealthscale")
		} else {
			// os.TempDir handles Windows (%TEMP%), Linux (/tmp) and darwin correctly.
			stateDir = filepath.Join(os.TempDir(), "stealthscale")
		}
	}
	if err := os.MkdirAll(stateDir, 0700); err != nil {
		return zero, err
	}
	keyPath := filepath.Join(stateDir, "machine.key")
	if data, err := os.ReadFile(keyPath); err == nil {
		var k key.MachinePrivate
		if err := k.UnmarshalText(bytes.TrimSpace(data)); err == nil {
			return k, nil
		}
	}
	k := key.NewMachine()
	text, err := k.MarshalText()
	if err != nil {
		return zero, err
	}
	if err := os.WriteFile(keyPath, text, 0600); err != nil {
		return zero, err
	}
	return k, nil
}

func fetchServerNoiseKey(ctx context.Context, coordinator string, insecure bool) (key.MachinePublic, error) {
	var zero key.MachinePublic
	u, err := url.Parse(coordinator)
	if err != nil {
		return zero, err
	}
	// Ensure we hit /key with capability version query
	keyURL := strings.TrimSuffix(coordinator, "/") + fmt.Sprintf("/key?v=%d", tailcfg.CurrentCapabilityVersion)
	if u.Scheme == "" {
		keyURL = "http://" + keyURL
	}
	httpClient := &http.Client{Timeout: 10 * time.Second}
	if insecure || u.Scheme == "http" {
		httpClient.Transport = &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, keyURL, nil)
	if err != nil {
		return zero, err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return zero, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return zero, fmt.Errorf("GET %s: HTTP %d", keyURL, resp.StatusCode)
	}
	var k tailcfg.OverTLSPublicKeyResponse
	if err := json.NewDecoder(resp.Body).Decode(&k); err != nil {
		return zero, fmt.Errorf("decoding /key response: %w", err)
	}
	if k.PublicKey.IsZero() {
		return zero, fmt.Errorf("server returned zero Noise public key")
	}
	return k.PublicKey, nil
}
