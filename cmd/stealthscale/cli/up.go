package cli

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/pterm/pterm"
	"github.com/spf13/cobra"
	"github.com/tomiwebpro/stealthscale/hscontrol/types"
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
	upCmd.Flags().String("node-id", "", "Node ID on the coordinator — fetch the stealth endpoint from its Web UI API (GET /web/api/vless/{id}) instead of copy/pasting --vless-uri")
	upCmd.Flags().String("api-key", "", "Coordinator API key sent as Bearer for the --node-id endpoint fetch; defaults to STEALTHSCALE_CLI_API_KEY. Required only when the coordinator enforces stealth control (xray.stealth.enforce_control)")
	upCmd.Flags().String("address", "", "Override the address in the VLESS endpoint (e.g. when the coordinator's xray.listen_addr is 0.0.0.0)")
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
node. It dials the node's stealth endpoint (from --vless-uri / --endpoint,
or fetched from the coordinator with --node-id),
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
  # One-command join — fetch the endpoint from the coordinator (no copy/paste)
  stscale up --coordinator https://ctl.example.com --authkey <key> --node-id 1

  # Same, when the coordinator enforces stealth control (API key required)
  stscale up --coordinator https://ctl.example.com --authkey <key> --api-key <api-key> --node-id 1

  # Coordinator listens on 0.0.0.0: override the non-dialable address
  stscale up --coordinator https://ctl.example.com --authkey <key> --node-id 1 --address 10.0.0.5

  # Fetch VLESS URI from coordinator, then paste into up
  stscale nodes vless 1           # on coordinator, prints table with URI column
  stscale up --coordinator https://ctl.example.com --authkey <key> --vless-uri 'vless://...'

  # One-command join (bash/zsh): fetch URI via subshell
  stscale up --coordinator https://ctl.example.com --authkey <key> \
    --vless-uri "$(stscale nodes vless 1 --one-line)"

  # Alias endpoint
  stscale up --coordinator https://ctl.example.com --authkey <key> --endpoint 'vless://...'

  # Interactive join without a pre-auth key: up prints a
  # registration URL, you approve it, and up polls until
  # the join completes:
  stscale up --coordinator https://ctl.example.com --node-id 1

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

		nodeIDStr, _ := cmd.Flags().GetString("node-id")
		apiKey, _ := cmd.Flags().GetString("api-key")
		addressOverride, _ := cmd.Flags().GetString("address")
		hostname, _ := cmd.Flags().GetString("hostname")
		stateDir, _ := cmd.Flags().GetString("state-dir")
		insecure, _ := cmd.Flags().GetBool("insecure")
		timeout, _ := cmd.Flags().GetDuration("timeout")

		// Enforce VLESS as transport: all traffic except holepunching must be stealth.
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()

		// One-command join: fetch the endpoint from the coordinator's
		// Web UI API instead of copy/pasting a ~250-char URI between
		// machines. An explicit --vless-uri/--endpoint still wins.
		if vlessURI == "" && nodeIDStr != "" {
			if coordinator == "" {
				return errNodeIDRequiresCoordinator
			}

			nodeID, err := types.ParseNodeID(nodeIDStr)
			if err != nil {
				return fmt.Errorf("invalid --node-id %q: %w", nodeIDStr, err)
			}

			if apiKey == "" {
				apiKey = os.Getenv("STEALTHSCALE_CLI_API_KEY")
			}

			pterm.Info.Printf("fetching VLESS endpoint for node %s from %s...\n", nodeIDStr, coordinator)

			vlessURI, err = fetchVLESSURI(ctx, coordinator, nodeID, apiKey, insecure)
			if err != nil {
				return err
			}

			pterm.Success.Println("fetched VLESS endpoint from coordinator")
		}
		if vlessURI == "" {
			return fmt.Errorf(
				"--vless-uri (or --endpoint) is required unless --node-id is given\n\n" +
					"One-command join (no copy/paste):\n" +
					"  stscale up --coordinator https://ctl.example.com --authkey <key> --node-id 1\n\n" +
					"Or get the URI from the coordinator:\n" +
					"  stscale nodes vless <node-id>   # prints table; use --one-line for raw URI only\n\n" +
					"One-command shell example:\n" +
					"  stscale up --coordinator https://ctl.example.com --authkey <key> \\\n" +
					"    --vless-uri \"$(stscale --config /etc/stealthscale/config.yaml nodes vless 1 --one-line)\"",
			)
		}
		cfg, err := xray.ParseVLESSURI(vlessURI)
		if err != nil {
			return fmt.Errorf("invalid --vless-uri: %w", err)
		}
		// The fetched (or pasted) URI carries the coordinator's
		// xray.listen_addr, which is often 0.0.0.0 and not dialable
		// from a client — override before dialing.
		if addressOverride != "" {
			cfg.Address = addressOverride
		}
		if coordinator != "" {
			if _, err := url.Parse(coordinator); err != nil {
				return fmt.Errorf("invalid --coordinator URL %q: %w", coordinator, err)
			}
		}
		if authKey == "" {
			pterm.Warning.Println("no --authkey provided; attempting interactive registration (approve the printed registration URL to join)")
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

		pterm.Info.Printf("dialling VLESS %s@%s:%d (security=%s, dest=%s, fp=%s)...\n", cfg.ID[:8]+"...", cfg.Address, cfg.Port, cfg.Security, cfg.Dest, cfg.FP)
		conn, err := xray.DialVLESS(ctx, cfg)
		if err != nil {
			return fmt.Errorf("VLESS dial failed (stealth transport not satisfied): %w\n\nHints:\n- endpoint must be reachable (firewall: xray.listen_port..max_listen_port)\n- UUID/port derived from xray.secret — ensure coordinator's secret is stable\n- pbk/sid mismatch: verify 'stscale nodes vless <id>' output (PBK/SID/Dest/FP columns) matches the client's URI\n- if the URI contains 0.0.0.0 (coordinator's xray.listen_addr), pass --address <public-IP-or-FQDN> or set xray.listen_addr on the coordinator\n- check 'curl http://<coordinator>/web/api/derp | jq .stealth_satisfied' and 'stscale health'", err)
		}
		defer conn.Close()
		pterm.Success.Printf("VLESS handshake succeeded to %s:%d (security=%s, utls=%s)\n", cfg.Address, cfg.Port, cfg.Security, cfg.FP)

		if coordinator == "" {
			pterm.Success.Println("stealth verification complete. Provide --coordinator to register.")
			return nil
		}

		pterm.Info.Println("stealth transport verified; fetching coordinator noise key and performing noise handshake over VLESS...")

		// Load or generate persistent machine and node keys so the
		// node identity is stable across runs. Reusing the node key
		// matters: the coordinator skips pre-auth-key validation when
		// an existing node re-registers with the same node key
		// (hscontrol/state HandleNodeFromPreAuthKey), so a restarted
		// node re-registers even when its one-shot pre-auth key was
		// already spent by the first run.
		machineKey, err := loadOrCreateMachineKey(stateDir)
		if err != nil {
			return fmt.Errorf("machine key: %w", err)
		}

		nodeKey, err := loadOrCreateNodeKey(stateDir)
		if err != nil {
			return fmt.Errorf("node key: %w", err)
		}

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

		// With no --authkey the request carries no Auth: the
		// coordinator answers with a registration URL
		// (interactive registration) instead of authorizing
		// immediately.
		regReq := tailcfg.RegisterRequest{
			Version:  tailcfg.CurrentCapabilityVersion,
			NodeKey:  nodeKey.Public(),
			Hostinfo: &tailcfg.Hostinfo{Hostname: hostname},
		}

		if authKey != "" {
			//nolint:exhaustruct_v5 // only the auth key is set on the registration
			regReq.Auth = &tailcfg.RegisterResponseAuth{AuthKey: authKey}
		}

		pterm.Info.Println("sending RegisterRequest over VLESS+noise (HTTP/2)...")

		regResp, err := registerOnce(ctx, tr, regReq)
		if err != nil {
			return err
		}

		if regResp.NodeKeyExpired {
			return errRegisterNodeKeyExpired
		}

		if !regResp.MachineAuthorized {
			// Interactive registration: the operator completes
			// the join at the printed registration URL, and
			// the followup polls deliver the approval verdict
			// back here.
			regResp, err = completeInteractiveRegistration(ctx, tr, regReq, regResp.AuthURL, interactiveRegistrationTimeout)
			if err != nil {
				return err
			}
		}
		pterm.Success.Printf("registered as %s (authorized=%v, nodeKey=%s)\n", hostname, regResp.MachineAuthorized, nodeKey.Public().ShortString())

		pterm.Success.Println("node is ready to serve as coordinator+client (unified). Run 'stscale serve' on this host to also coordinate.")
		return nil
	},
}

// interactiveRegistrationTimeout bounds how long `stscale up`
// waits for an interactive registration to be approved: the
// coordinator holds each followup poll open until the
// operator's verdict arrives, so this is an upper bound for
// a slow approval, not a busy loop.
const interactiveRegistrationTimeout = 10 * time.Minute

// interactivePollRetryDelay backs off between followup polls
// when the coordinator answers immediately (its own wait
// window expired, or it restarted the session) instead of
// holding the poll open.
const interactivePollRetryDelay = 500 * time.Millisecond

// registerOnce sends one RegisterRequest over the
// noise-backed HTTP/2 transport and decodes the
// coordinator's answer. It is the single request unit of
// both the pre-auth-key join and the interactive
// registration followup polls.
func registerOnce(
	ctx context.Context,
	tr http.RoundTripper,
	req tailcfg.RegisterRequest,
) (*tailcfg.RegisterResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshaling register request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://control/machine/register", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating register request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := tr.RoundTrip(httpReq)
	if err != nil {
		return nil, fmt.Errorf("register RoundTrip over VLESS+noise: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var errBody bytes.Buffer

		_, _ = errBody.ReadFrom(resp.Body)

		return nil, fmt.Errorf("%w: HTTP %d: %s", errRegisterFailed, resp.StatusCode, errBody.String())
	}

	var regResp tailcfg.RegisterResponse

	err = json.NewDecoder(resp.Body).Decode(&regResp)
	if err != nil {
		return nil, fmt.Errorf("decoding register response: %w", err)
	}

	if regResp.Error != "" {
		return nil, fmt.Errorf("%w: %s", errRegisterError, regResp.Error)
	}

	return &regResp, nil
}

// completeInteractiveRegistration performs the pending half
// of an interactive (no pre-auth key) registration: it
// prints the registration URL the operator must complete,
// then polls the coordinator's followup endpoint
// (RegisterRequest.Followup) until the operator's verdict
// turns the join into an authorized node. The coordinator
// restarts the session by answering a poll with a fresh
// AuthURL (rejection or expiry); the new URL is surfaced
// and polled in turn.
func completeInteractiveRegistration(
	ctx context.Context,
	tr http.RoundTripper,
	req tailcfg.RegisterRequest,
	authURL string,
	timeout time.Duration,
) (*tailcfg.RegisterResponse, error) {
	if authURL == "" {
		return nil, errRegisterNotAuthorized
	}

	pterm.Warning.Println(pendingRegistrationMessage(authURL))
	pterm.Info.Printf("waiting for the operator to approve the registration (up to %s)...\n", timeout)

	// The dial-timeout context is far too short for an
	// operator to approve the join in a browser; poll under
	// a fresh budget instead.
	pollCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	defer cancel()

	// The followup poll is a plain RegisterRequest carrying
	// the session's followup URL and no auth key: the
	// coordinator identifies the pending session by the auth
	// ID in the URL and the Noise session by the machine
	// key, so the node identity must stay on every poll.
	req.Followup = authURL
	req.Auth = nil

	var lastErr error

	for {
		resp, err := registerOnce(pollCtx, tr, req)
		if err != nil {
			// The coordinator's wait window expired (or the
			// connection dropped) before the verdict arrived:
			// expected while the operator has not completed
			// the registration URL yet. Retry within budget.
			lastErr = err
		} else if resp.NodeKeyExpired {
			return nil, errRegisterNodeKeyExpired
		} else if resp.MachineAuthorized {
			return resp, nil
		} else if resp.AuthURL == "" {
			return nil, errRegisterNotAuthorized
		} else if resp.AuthURL != authURL {
			// The coordinator restarted the session (the
			// registration was rejected or expired): keep
			// polling the fresh URL and surface it — the
			// operator must act on the new URL.
			authURL = resp.AuthURL

			req.Followup = authURL

			pterm.Warning.Println(pendingRegistrationMessage(authURL))
		}

		select {
		case <-pollCtx.Done():
			if lastErr != nil {
				return nil, fmt.Errorf("%w within %s: %w", errInteractiveRegistrationTimedOut, timeout, lastErr)
			}

			return nil, fmt.Errorf("%w within %s", errInteractiveRegistrationTimedOut, timeout)
		case <-time.After(interactivePollRetryDelay):
		}
	}
}

// pendingRegistrationMessage renders the warning shown when the
// coordinator has not authorized the registration yet. The
// response's AuthURL is the node's registration URL — it embeds
// the auth ID and is the only handle the operator has on the
// pending session (opening it renders the exact
// `stealthscale auth register --auth-id <id> --user <user>`
// command for the web provider, or the OIDC confirmation
// interstitial; the approve/reject endpoints deliberately refuse
// registration sessions), so it must be surfaced verbatim
// instead of a generic hint that sends the operator hunting for
// the auth ID.
func pendingRegistrationMessage(authURL string) string {
	if authURL == "" {
		return "register response: machine not yet authorized (check authkey or approve via coordinator)"
	}

	return "register response: machine not yet authorized. Complete the registration at: " +
		authURL
}

// resolveStateDir returns the directory holding the node's
// persistent identity keys: the --state-dir flag when given,
// ~/.stealthscale otherwise (%TEMP%\stealthscale on Windows
// when the home directory cannot be resolved).
func resolveStateDir(stateDir string) string {
	if stateDir != "" {
		return stateDir
	}

	home, err := os.UserHomeDir()
	if err == nil {
		return filepath.Join(home, ".stealthscale")
	}

	// os.TempDir handles Windows (%TEMP%), Linux (/tmp) and darwin correctly.
	return filepath.Join(os.TempDir(), "stealthscale")
}

func loadOrCreateMachineKey(stateDir string) (key.MachinePrivate, error) {
	var zero key.MachinePrivate

	stateDir = resolveStateDir(stateDir)

	mkdirErr := os.MkdirAll(stateDir, 0o700)
	if mkdirErr != nil {
		return zero, mkdirErr
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

	writeErr := os.WriteFile(keyPath, text, 0o600)
	if writeErr != nil {
		return zero, writeErr
	}
	return k, nil
}

// loadOrCreateNodeKey persists the node's NodePrivate key in
// stateDir/node.key, generating it on first run. Reusing the key
// across runs is what lets the coordinator's restart fast-path
// (HandleNodeFromPreAuthKey: an existing node re-registering with
// the same node key skips pre-auth-key validation) re-register a
// restarted node without a fresh valid key — a fresh key on every
// run would force the rotation path, which requires a valid
// (unspent) pre-auth key. Mirrors loadOrCreateMachineKey; both keys
// share the state dir so one --state-dir backs the whole identity.
func loadOrCreateNodeKey(stateDir string) (key.NodePrivate, error) {
	var zero key.NodePrivate

	stateDir = resolveStateDir(stateDir)

	mkdirErr := os.MkdirAll(stateDir, 0o700)
	if mkdirErr != nil {
		return zero, mkdirErr
	}

	keyPath := filepath.Join(stateDir, "node.key")

	data, readErr := os.ReadFile(keyPath)
	if readErr == nil {
		var k key.NodePrivate

		unmarshalErr := k.UnmarshalText(bytes.TrimSpace(data))
		if unmarshalErr == nil {
			return k, nil
		}
	}

	k := key.NewNode()

	text, err := k.MarshalText()
	if err != nil {
		return zero, err
	}

	writeErr := os.WriteFile(keyPath, text, 0o600)
	if writeErr != nil {
		return zero, writeErr
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

// vlessEndpointResponse is the JSON body served by the
// coordinator's GET /web/api/vless/{id}
// (hscontrol/webui/webui.go handleVLESS).
type vlessEndpointResponse struct {
	NodeID  string            `json:"nodeID"`
	UUID    string            `json:"uuid"`
	Port    int               `json:"port"`
	URI     string            `json:"uri"`
	Address string            `json:"address"`
	Config  *xray.VLESSConfig `json:"config"`
}

// Static errors for the --node-id endpoint fetch. The
// dynamic parts (URL, status code, actionable hint) are
// added by the caller via %w formatting (err113).
var (
	errNodeIDRequiresCoordinator = errors.New(
		"--node-id requires --coordinator: the VLESS endpoint is fetched from the coordinator's Web UI API",
	)
	errFetchVLESSStatus = errors.New("fetching VLESS endpoint")
	errNoVLESSURI       = errors.New("coordinator returned no VLESS URI")
)

// Static errors for the machine-API register flow, same
// pattern: the dynamic parts (status code, response body,
// budget) are added by the caller via %w formatting.
var (
	errRegisterFailed                  = errors.New("register failed")
	errRegisterError                   = errors.New("register error")
	errRegisterNodeKeyExpired          = errors.New("register response: node key expired")
	errRegisterNotAuthorized           = errors.New("register response: machine not authorized and no registration URL provided")
	errInteractiveRegistrationTimedOut = errors.New("interactive registration not completed")
)

// fetchVLESSURI fetches a node's VLESS endpoint URI from the
// coordinator's Web UI API (GET /web/api/vless/{id}), collapsing
// the join flow to one command per machine: neither the ~250-char
// URI nor the node ID has to be copied between coordinator and
// client.
//
// The URI carries server-secret-derived material (pbk, sid, dest)
// that the client cannot compute locally, so it must come from the
// coordinator. When stealth control enforcement is on
// (xray.stealth.enforce_control), the Web UI API requires an API
// key — pass it via --api-key or STEALTHSCALE_CLI_API_KEY.
func fetchVLESSURI(ctx context.Context, coordinator string, nodeID types.NodeID, apiKey string, insecure bool) (string, error) {
	u, err := url.Parse(coordinator)
	if err != nil {
		return "", fmt.Errorf("invalid --coordinator URL %q: %w", coordinator, err)
	}

	endpointURL := strings.TrimSuffix(coordinator, "/") + "/web/api/vless/" + strconv.FormatUint(uint64(nodeID), 10)
	if u.Scheme == "" {
		// The fetch may carry a Bearer API key, so default to HTTPS
		// for scheme-less coordinator URLs; pass http:// explicitly
		// for local plain-HTTP coordinators.
		endpointURL = "https://" + endpointURL
	}

	//nolint:exhaustruct_v5 // partial literal is deliberate, same as fetchServerNoiseKey
	httpClient := &http.Client{Timeout: 10 * time.Second}
	if insecure || u.Scheme == "http" {
		//nolint:exhaustruct_v5 // partial literal is deliberate, same as fetchServerNoiseKey
		httpClient.Transport = &http.Transport{
			//nolint:gosec // intentionally insecure for self-signed coordinators
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:exhaustruct_v5
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpointURL, nil)
	if err != nil {
		return "", err
	}

	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetching VLESS endpoint from %s: %w", endpointURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		hint := ""

		if resp.StatusCode == http.StatusUnauthorized {
			hint = "\n\nThe coordinator requires an API key for its Web UI API (xray.stealth.enforce_control is enabled). Create one with 'stscale apikeys create' and pass it via --api-key or STEALTHSCALE_CLI_API_KEY."
		}

		return "", fmt.Errorf("%w from %s: HTTP %d%s", errFetchVLESSStatus, endpointURL, resp.StatusCode, hint)
	}

	var ep vlessEndpointResponse

	decodeErr := json.NewDecoder(resp.Body).Decode(&ep)
	if decodeErr != nil {
		return "", fmt.Errorf("decoding VLESS endpoint response from %s: %w", endpointURL, decodeErr)
	}

	if ep.URI == "" {
		return "", fmt.Errorf("%w for node %d: %s", errNoVLESSURI, uint64(nodeID), endpointURL)
	}

	return ep.URI, nil
}
