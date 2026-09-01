// Copyright (c) 2026 TomiWebPro <TomiWebPro@gmail.com>
// SPDX-License-Identifier: BSD-3-Clause

package xray

import (
	"context"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gofrs/uuid/v5"
	utls "github.com/refraction-networking/utls"
)

// WriteVLESSRequest writes the VLESS client handshake header to w.
// The header is: version (0x00) || uuid (16 bytes) || addons_length (0x00).
func WriteVLESSRequest(w io.Writer, uuidStr string) error {
	u, err := uuid.FromString(uuidStr)
	if err != nil {
		return fmt.Errorf("invalid VLESS UUID %q: %w", uuidStr, err)
	}
	b := u.Bytes()
	header := make([]byte, 0, 1+vlessUUIDLen+1)
	header = append(header, vlessVersion)
	header = append(header, b...)
	header = append(header, 0)
	if _, err := w.Write(header); err != nil {
		return fmt.Errorf("writing VLESS header: %w", err)
	}
	return nil
}

// DialVLESS dials the VLESS endpoint described by cfg, performs the
// VLESS handshake and returns the authenticated stream. The caller is
// responsible for closing the returned connection.
//
// Security handling:
//   - "none": plain TCP
//   - "tls", "xtls": TLS-wrapped TCP (InsecureSkipVerify for stealth,
//     SNI set to cfg.Address), ClientHello shaped with uTLS.
//   - "reality", "reality_xtls": TLS with Reality-style decoy handshake.
//     The ClientHello is shaped with uTLS (e.g. chrome) and the SNI is set
//     to the decoy destination (Reality.Dest) so the TLS stream is
//     indistinguishable from a normal browser connection to that site. The
//     server certificate is not verified (InsecureSkipVerify) because the
//     client validates the server via its Reality public key instead.
//
// The VLESS header (version + UUID + addons) is written and the server's
// version ack (single byte 0x00) is verified before returning.
func DialVLESS(ctx context.Context, cfg *VLESSConfig) (net.Conn, error) {
	if cfg == nil {
		return nil, fmt.Errorf("nil VLESS config")
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	addr := net.JoinHostPort(cfg.Address, strconv.Itoa(cfg.Port))
	// Use timeout from config if set, otherwise 10s.
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 10 * time.Second
	}
	// Prefer context deadline if tighter.
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining < timeout && remaining > 0 {
			timeout = remaining
		}
	}
	dialer := &net.Dialer{Timeout: timeout}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("dialling VLESS %s: %w", addr, err)
	}
	// If security requires TLS, wrap before VLESS header.
	sec := cfg.Security
	if sec == "reality" {
		sec = "reality_xtls"
	}
	if sec == "" {
		sec = "reality_xtls"
	}

	// sni resolves the TLS SNI to present. For reality_xtls the SNI must be
	// the decoy destination host, never the real server address — presenting
	// the real address as SNI while the certificate claims the decoy is a
	// classic (and fatal) Reality giveaway.
	sni := cfg.Address
	if sec == "reality_xtls" && cfg.Dest != "" {
		if host, _, herr := net.SplitHostPort(cfg.Dest); herr == nil {
			sni = host
		} else {
			sni = cfg.Dest
		}
		// Dest may be an IP:port for local testing (e.g. 127.0.0.1:xxxxx) where
		// SNI must be a real decoy name. IP SNIs are stripped by Go/utls
		// (no SNI extension) and always fail Reality verification.
		if net.ParseIP(sni) != nil {
			sni = "www.cloudflare.com"
		}
	}

	switch sec {
	case "tls", "xtls":
		uconf := &utls.Config{
			ServerName:         sni,
			InsecureSkipVerify: true,
			MinVersion:         tls.VersionTLS12,
			NextProtos:         []string{"h2", "http/1.1"},
		}
		helloID := fpToClientHelloID(cfg.FP)
		uconn := utls.UClient(conn, uconf, helloID)
		_ = uconn.SetDeadline(time.Now().Add(timeout))
		if err := uconn.HandshakeContext(ctx); err != nil {
			conn.Close()
			return nil, fmt.Errorf("%s handshake for VLESS %s: %w", sec, addr, err)
		}
		_ = uconn.SetDeadline(time.Time{})
		conn = uconn
	case "reality_xtls":
		// True Reality: utls + Reality auth (shortId + public key) via vendored client.
		// Fail-closed when PublicKey is missing — plain utls with InsecureSkipVerify
		// and no HMAC check would leak UUID to a MITM with any cert for SNI.
		if cfg.PublicKey == "" {
			conn.Close()
			return nil, fmt.Errorf("reality_xtls requires pbk (public key) — refusing to downgrade to plain uTLS (would leak UUID to MITM); got empty PublicKey for %s", addr)
		}
		pubBytes, err := hex.DecodeString(cfg.PublicKey)
		if err != nil || len(pubBytes) != 32 {
			conn.Close()
			return nil, fmt.Errorf("reality_xtls: invalid public key %q: %w", cfg.PublicKey, err)
		}
		var shortIdBytes []byte
		if cfg.ShortID != "" {
			b, err := hex.DecodeString(cfg.ShortID)
			if err != nil {
				conn.Close()
				return nil, fmt.Errorf("reality_xtls: invalid shortId %q: %w", cfg.ShortID, err)
			}
			shortIdBytes = b
		}
		rcfg := &RealityClientConfig{
			Show:        false,
			Fingerprint: cfg.FP,
			ServerName:  sni,
			PublicKey:   pubBytes,
			ShortId:     shortIdBytes,
			SpiderX:     cfg.SpiderX,
		}
		if rcfg.SpiderX == "" {
			rcfg.SpiderX = "/"
		}
		if rcfg.Fingerprint == "" {
			rcfg.Fingerprint = "chrome"
		}
		_ = conn.SetDeadline(time.Now().Add(timeout))
		rConn, err := RealityUClient(conn, rcfg, ctx)
		if err != nil {
			conn.Close()
			return nil, fmt.Errorf("reality_xtls handshake for VLESS %s: %w", addr, err)
		}
		_ = rConn.SetDeadline(time.Time{})
		conn = rConn
	case "none":
		// plain TCP, no TLS
	default:
		conn.Close()
		return nil, fmt.Errorf("unsupported VLESS security %q", cfg.Security)
	}

	// Write VLESS header.
	if err := WriteVLESSRequest(conn, cfg.ID); err != nil {
		conn.Close()
		return nil, err
	}
	// Await server ack (single version byte).
	if cfg.Timeout > 0 {
		_ = conn.SetReadDeadline(time.Now().Add(cfg.Timeout))
	} else {
		_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	}
	var ack [1]byte
	if _, err := io.ReadFull(conn, ack[:]); err != nil {
		conn.Close()
		return nil, fmt.Errorf("reading VLESS ack from %s: %w", addr, err)
	}
	_ = conn.SetReadDeadline(time.Time{})
	if ack[0] != vlessVersion {
		conn.Close()
		return nil, fmt.Errorf("unexpected VLESS version %d from %s", ack[0], addr)
	}
	return conn, nil
}

// fpToClientHelloID maps a uTLS fingerprint name to a utls ClientHelloID.
// Unknown/empty values default to a recent Chrome ClientHello, which is the
// most common browser fingerprint observed on the wire.
func fpToClientHelloID(fp string) utls.ClientHelloID {
	switch strings.ToLower(fp) {
	case "firefox":
		return utls.HelloFirefox_Auto
	case "safari":
		return utls.HelloSafari_Auto
	case "ios", "ios_auto":
		return utls.HelloIOS_Auto
	case "randomized", "random":
		return utls.HelloRandomized
	case "golang", "hellogolang":
		return utls.HelloGolang
	case "chrome_120", "hellochrome_120":
		return utls.HelloChrome_120
	case "chrome", "":
		return utls.HelloChrome_Auto
	default:
		return utls.HelloChrome_Auto
	}
}

// ParseVLESSURI parses a vless:// URI into a VLESSConfig.
// Expected form: vless://<uuid>@<host>:<port>?security=<mode>&fp=<fingerprint>&type=tcp&flow=xtls-rprx-vision[&dest=<decoy>&pbk=<pubkey>&sid=<shortid>]
// uuid, host and port are required; security defaults to reality_xtls when
// empty and "reality" is normalised. dest/pbk/sid carry the Reality decoy,
// public key and short id so the client can validate the server.
func ParseVLESSURI(uri string) (*VLESSConfig, error) {
	if uri == "" {
		return nil, fmt.Errorf("empty VLESS URI")
	}
	// Minimal manual parse to avoid net/url overhead for the custom scheme.
	// We accept vless://uuid@host:port?query
	const prefix = "vless://"
	if len(uri) < len(prefix) || uri[:len(prefix)] != prefix {
		return nil, fmt.Errorf("invalid VLESS URI %q: missing vless:// prefix", uri)
	}
	rest := uri[len(prefix):]
	// Split uuid and host:port+query
	at := -1
	for i, c := range rest {
		if c == '@' {
			at = i
			break
		}
	}
	if at == -1 {
		return nil, fmt.Errorf("invalid VLESS URI %q: missing @", uri)
	}
	id := rest[:at]
	if id == "" {
		return nil, fmt.Errorf("invalid VLESS URI %q: empty UUID", uri)
	}
	hostPortQuery := rest[at+1:]
	// Separate query
	qIdx := -1
	for i, c := range hostPortQuery {
		if c == '?' {
			qIdx = i
			break
		}
	}
	var hostPort, query string
	if qIdx == -1 {
		hostPort = hostPortQuery
		query = ""
	} else {
		hostPort = hostPortQuery[:qIdx]
		query = hostPortQuery[qIdx+1:]
	}
	host, portStr, err := net.SplitHostPort(hostPort)
	if err != nil {
		return nil, fmt.Errorf("invalid VLESS URI %q: %w", uri, err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return nil, fmt.Errorf("invalid VLESS URI %q: bad port %q", uri, portStr)
	}
	// Parse all query parameters (security, fp, dest, pbk, sid, ...).
	params := map[string]string{}
	if query != "" {
		start := 0
		for i := 0; i <= len(query); i++ {
			if i == len(query) || query[i] == '&' {
				pair := query[start:i]
				if eq := strings.IndexByte(pair, '='); eq >= 0 {
					params[pair[:eq]] = pair[eq+1:]
				}
				start = i + 1
			}
		}
	}
	security := params["security"]
	if security == "" {
		security = "reality_xtls"
	}
	if security == "reality" {
		security = "reality_xtls"
	}
	destEsc := params["dest"]
	if destEsc != "" {
		if u, err := url.QueryUnescape(destEsc); err == nil {
			destEsc = u
		}
	}
	spxEsc := params["spx"]
	if spxEsc != "" {
		if u, err := url.QueryUnescape(spxEsc); err == nil {
			spxEsc = u
		}
	}
	cfg := &VLESSConfig{
		ID:        id,
		Network:   "tcp",
		Address:   host,
		Port:      port,
		Security:  security,
		Dest:      destEsc,
		FP:        params["fp"],
		PublicKey: params["pbk"],
		ShortID:   params["sid"],
		SpiderX:   spxEsc,
		Timeout:   30 * time.Second,
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}
