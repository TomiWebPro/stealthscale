// Package main is a minimal standalone VLESS+Reality client that mimics the
// patched tailscale dial without forking tailscale. It dials a StealthScale
// node's VLESS endpoint via xray.DialVLESS (which does RealityUClient when
// security=reality_xtls), then runs the Noise handshake and a single
// RegisterRequest over HTTP/2 — the same flow as hscontrol/servertest/xray_vless_test.go.

package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/tomiwebpro/stealthscale/hscontrol/xray"
	"golang.org/x/net/http2"
	"tailscale.com/control/controlbase"
	"tailscale.com/tailcfg"
	"tailscale.com/types/key"
)

func main() {
	vlessURI := flag.String("vless-uri", "", "vless://<uuid>@<host>:<port>?security=reality_xtls&pbk=&sid=&spx=&fp=&dest= (from `stscale nodes vless <id>`)")
	coordinator := flag.String("coordinator", "", "coordinator URL to fetch /key (e.g. https://ctl.example.com)")
	authKey := flag.String("authkey", "", "pre-auth key")
	hostname := flag.String("hostname", "stealth-client", "hostname to register")
	insecure := flag.Bool("insecure", false, "skip TLS verification for /key")
	flag.Parse()
	if *vlessURI == "" {
		fmt.Fprintln(os.Stderr, "--vless-uri is required (get via `stscale nodes vless <id>` on coordinator)")
		os.Exit(1)
	}
	cfg, err := xray.ParseVLESSURI(*vlessURI)
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid --vless-uri: %v\n", err)
		os.Exit(1)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	fmt.Printf("dialling VLESS %s@%s:%d security=%s dest=%s fp=%s\n", cfg.ID[:8], cfg.Address, cfg.Port, cfg.Security, cfg.Dest, cfg.FP)
	conn, err := xray.DialVLESS(ctx, cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "VLESS dial failed: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()
	fmt.Println("VLESS handshake ok")

	if *coordinator == "" || *authKey == "" {
		fmt.Println("stealth verification complete (need --coordinator and --authkey to register)")
		return
	}

	// Fetch server's Noise key via /key (discovery, may be non-stealth)
	serverPub, err := fetchKey(ctx, *coordinator, *insecure)
	if err != nil {
		fmt.Fprintf(os.Stderr, "fetch /key: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("coordinator noise key: %s\n", serverPub.ShortString())

	machineKey := key.NewMachine()
	nodeKey := key.NewNode()

	noiseConn, err := controlbase.Client(ctx, conn, machineKey, serverPub, uint16(tailcfg.CurrentCapabilityVersion))
	if err != nil {
		fmt.Fprintf(os.Stderr, "noise handshake over VLESS: %v\n", err)
		os.Exit(1)
	}
	defer noiseConn.Close()
	fmt.Println("noise over VLESS ok")

	tr := &http2.Transport{AllowHTTP: true, DialTLSContext: func(context.Context, string, string, *tls.Config) (net.Conn, error) { return noiseConn, nil }}
	defer tr.CloseIdleConnections()
	regReq := tailcfg.RegisterRequest{Version: tailcfg.CurrentCapabilityVersion, NodeKey: nodeKey.Public(), Hostinfo: &tailcfg.Hostinfo{Hostname: *hostname}, Auth: &tailcfg.RegisterResponseAuth{AuthKey: *authKey}}
	body, _ := json.Marshal(regReq)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://control/machine/register", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := tr.RoundTrip(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "register: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	var regResp tailcfg.RegisterResponse
	if err := json.NewDecoder(resp.Body).Decode(&regResp); err != nil {
		fmt.Fprintf(os.Stderr, "decode: %v\n", err)
		os.Exit(1)
	}
	if regResp.Error != "" {
		fmt.Fprintf(os.Stderr, "register error: %s\n", regResp.Error)
		os.Exit(1)
	}
	fmt.Printf("registered: authorized=%v nodeKeyExpired=%v\n", regResp.MachineAuthorized, regResp.NodeKeyExpired)
}

func fetchKey(ctx context.Context, coordinator string, insecure bool) (key.MachinePublic, error) {
	var zero key.MachinePublic
	url := coordinator + "/key?v=" + fmt.Sprint(tailcfg.CurrentCapabilityVersion)
	client := &http.Client{Timeout: 10 * time.Second}
	if insecure {
		client.Transport = &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return zero, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return zero, err
	}
	defer resp.Body.Close()
	var r tailcfg.OverTLSPublicKeyResponse
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return zero, err
	}
	return r.PublicKey, nil
}
