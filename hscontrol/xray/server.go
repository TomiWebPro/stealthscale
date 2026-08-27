// Copyright (c) 2026 TomiWebPro <TomiWebPro@gmail.com>
// SPDX-License-Identifier: BSD-3-Clause
//
// XTLS-Reality server via github.com/xtls/reality (MPL-2.0, Copyright (c) 2023 RPRX)
// and uTLS via github.com/refraction-networking/utls (BSD-3-Clause).
// See LICENSE (Third-Party Licenses).

package xray

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	reality "github.com/xtls/reality"
	utls "github.com/refraction-networking/utls"
	"github.com/rs/zerolog/log"

	"github.com/tomiwebpro/stealthscale/hscontrol/types"
)

// bufferedConn is a net.Conn whose reads go through a bufio.Reader, so
// bytes that were buffered while parsing the VLESS header are not lost.
type bufferedConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) {
	return c.r.Read(p)
}

// Server serves VLESS connections for individual nodes. Each node gets its
// own listener on a deterministic port, authenticated by a deterministic
// UUID derived from the node ID. Once a connection passes the VLESS header
// check, the raw stream is handed to the handler, which runs the Tailscale
// noise protocol over it.
type Server struct {
	cfg           *types.XRayConfig
	handler       func(net.Conn)
	tlsConfig     *utls.Config
	realityConfig *reality.Config

	mu        sync.Mutex
	listeners map[types.NodeID]*nodeListener
}

type nodeListener struct {
	nodeID types.NodeID
	port   int
	ln     net.Listener
	cancel context.CancelFunc
	done   chan struct{}
}

// NewServer creates a VLESS server. handler is invoked with the
// authenticated payload connection (after the VLESS header has been
// consumed) and is expected to serve it until it closes.
//
// Security modes:
//   - none: plain VLESS
//   - tls/xtls: TLS-wrapped VLESS (requires cert_file/key_file)
//   - reality_xtls: VLESS+Reality via XTLS+uTLS (default, stealth). Reality
//     steals the dest site's TLS handshake; uTLS shapes ClientHello. The real
//     Reality implementation (github.com/xtls/reality, MPL-2.0) is used on the
//     server and a lightweight vendored client (reality_client.go) on the
//     client side.
func NewServer(cfg *types.XRayConfig, handler func(net.Conn)) (*Server, error) {
	s := &Server{
		cfg:       cfg,
		handler:   handler,
		listeners: make(map[types.NodeID]*nodeListener),
	}

	// Normalize alias
	security := cfg.Security
	if security == "reality" {
		security = "reality_xtls"
	}

	switch security {
	case "tls", "xtls":
		cert, err := utls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("loading xray TLS credentials: %w", err)
		}
		s.tlsConfig = &utls.Config{
			Certificates: []utls.Certificate{cert},
			MinVersion:   tls.VersionTLS12,
		}
		applyUTLSFingerprint(s.tlsConfig, cfg.UTLSFingerprint)
	case "reality_xtls":
		// If operator explicitly supplied a TLS cert, honour it as a plain TLS
		// fallback (old behaviour). Otherwise configure true Reality.
		if cfg.CertFile != "" && cfg.KeyFile != "" {
			cert, err := utls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
			if err != nil {
				return nil, fmt.Errorf("loading xray TLS credentials for reality_xtls: %w", err)
			}
			s.tlsConfig = &utls.Config{
				Certificates: []utls.Certificate{cert},
				MinVersion:   tls.VersionTLS12,
			}
			applyUTLSFingerprint(s.tlsConfig, cfg.UTLSFingerprint)
			log.Warn().Msg("xray: reality_xtls with explicit cert_file/key_file — using TLS fallback; for true Reality unset cert_file")
		} else {
			rc, err := buildRealityConfig(cfg)
			if err != nil {
				return nil, fmt.Errorf("building reality config: %w", err)
			}
			s.realityConfig = rc
			// Prime the global post-handshake lens cache so the first client
			// handshake doesn't block waiting for dest probing. This dials the
			// dest in the background using utls (same as xray's NewListener).
			go reality.DetectPostHandshakeRecordsLens(rc)
			fp := cfg.UTLSFingerprint
			if fp == "" {
				fp = "chrome"
			}
			log.Info().
				Str("security", "reality_xtls").
				Str("utls", fp).
				Str("reality_dest", rc.Dest).
				Strs("server_names", cfg.Reality.ServerNames).
				Str("short_id", cfg.Reality.ShortID).
				Msg("xray: reality_xtls stealth transport enabled (xtls/reality)")
		}
	case "none", "":
		// no TLS
	default:
		return nil, fmt.Errorf("unsupported xray security mode %q", cfg.Security)
	}

	return s, nil
}

func buildRealityConfig(cfg *types.XRayConfig) (*reality.Config, error) {
	dest := cfg.Reality.Dest
	if dest == "" {
		dest = "www.cloudflare.com:443"
	}
	// Normalise dest to host:port
	if !strings.Contains(dest, ":") {
		dest += ":443"
	}
	if strings.Contains(dest, "://") {
		if h, err := parseDestHost(dest); err == nil {
			dest = h
			if !strings.Contains(dest, ":") {
				dest += ":443"
			}
		}
	}
	privHex := cfg.Reality.PrivateKey
	var privBytes []byte
	if privHex == "" {
		// No key derived (e.g. test without InitIdentity). Generate ephemeral
		// so the transport still works; production always has a persistent key
		// via XRayConfig.InitIdentity.
		privBytes = make([]byte, 32)
		if _, err := rand.Read(privBytes); err != nil {
			return nil, fmt.Errorf("generating ephemeral reality key: %w", err)
		}
	} else {
		b, err := hex.DecodeString(privHex)
		if err != nil {
			return nil, fmt.Errorf("invalid reality private key hex: %w", err)
		}
		if len(b) != 32 {
			return nil, fmt.Errorf("reality private key must be 32 bytes, got %d", len(b))
		}
		privBytes = b
	}
	serverNames := make(map[string]bool)
	for _, sn := range cfg.Reality.ServerNames {
		sn = strings.TrimSpace(sn)
		if sn != "" {
			serverNames[sn] = true
		}
	}
	if len(serverNames) == 0 {
		// Fallback decoys — both cloudflare and microsoft as requested.
		for _, sn := range []string{"www.cloudflare.com", "www.microsoft.com", "cloudflare.com", "microsoft.com"} {
			serverNames[sn] = true
		}
	}
	shortIds := make(map[[8]byte]bool)
	ids := cfg.Reality.ShortIDs
	if len(ids) == 0 && cfg.Reality.ShortID != "" {
		ids = []string{cfg.Reality.ShortID}
	}
	for _, sidHex := range ids {
		sidHex = strings.TrimSpace(sidHex)
		if sidHex == "" {
			// Empty shortId is allowed in xray when config.ShortIds contains "".
			// That maps to [8]byte{} (all zeros). Include it.
			var zero [8]byte
			shortIds[zero] = true
			continue
		}
		b, err := hex.DecodeString(sidHex)
		if err != nil {
			// Skip malformed entries but log
			log.Warn().Str("short_id", sidHex).Msg("xray: skipping invalid reality short_id")
			continue
		}
		var arr [8]byte
		copy(arr[:], b)
		shortIds[arr] = true
	}
	if len(shortIds) == 0 {
		// At least allow the derived shortId
		if cfg.Reality.ShortID != "" {
			if b, err := hex.DecodeString(cfg.Reality.ShortID); err == nil {
				var arr [8]byte
				copy(arr[:], b)
				shortIds[arr] = true
			}
		}
	}

	show := false
	rc := &reality.Config{
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, network, address)
		},
		Type:        "tcp",
		Dest:        dest,
		ServerNames: serverNames,
		PrivateKey:  privBytes,
		ShortIds:    shortIds,
		Show:        show,
	}
	return rc, nil
}

func applyUTLSFingerprint(cfg *utls.Config, fp string) {
	switch strings.ToLower(fp) {
	case "chrome", "":
		cfg.CipherSuites = []uint16{
			tls.TLS_AES_128_GCM_SHA256,
			tls.TLS_AES_256_GCM_SHA384,
			tls.TLS_CHACHA20_POLY1305_SHA256,
			tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256,
			tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256,
		}
		cfg.CurvePreferences = []utls.CurveID{utls.X25519, utls.CurveP256, utls.CurveP384}
	case "firefox":
		cfg.CipherSuites = []uint16{
			tls.TLS_AES_128_GCM_SHA256,
			tls.TLS_CHACHA20_POLY1305_SHA256,
			tls.TLS_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256,
			tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256,
		}
		cfg.CurvePreferences = []utls.CurveID{utls.X25519, utls.CurveP256, utls.CurveP384, utls.CurveP521}
	case "safari":
		cfg.CipherSuites = []uint16{
			tls.TLS_AES_128_GCM_SHA256,
			tls.TLS_AES_256_GCM_SHA384,
			tls.TLS_CHACHA20_POLY1305_SHA256,
			tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
		}
		cfg.CurvePreferences = []utls.CurveID{utls.X25519, utls.CurveP256}
	case "randomized":
		// Randomized mimics Chrome but shuffles order for anti-fingerprinting.
		cfg.CipherSuites = []uint16{
			tls.TLS_CHACHA20_POLY1305_SHA256,
			tls.TLS_AES_128_GCM_SHA256,
			tls.TLS_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256,
		}
		cfg.CurvePreferences = []utls.CurveID{utls.X25519, utls.CurveP256}
	default:
		// Unknown fingerprint defaults to Chrome.
		cfg.CipherSuites = []uint16{
			tls.TLS_AES_128_GCM_SHA256,
			tls.TLS_AES_256_GCM_SHA384,
			tls.TLS_CHACHA20_POLY1305_SHA256,
		}
		cfg.CurvePreferences = []utls.CurveID{utls.X25519, utls.CurveP256}
	}
}

func parseDestHost(dest string) (string, error) {
	// Accept dest as host:port or URL. If it contains ://, parse as URL.
	if strings.Contains(dest, "://") {
		// Simple extract host between // and next /
		rest := dest
		if idx := strings.Index(rest, "://"); idx != -1 {
			rest = rest[idx+3:]
		}
		if slash := strings.Index(rest, "/"); slash != -1 {
			rest = rest[:slash]
		}
		return rest, nil
	}
	return dest, nil
}

// NodeConfig returns the VLESS endpoint configuration for a node,
// independent of whether a listener is currently running.
func (s *Server) NodeConfig(nodeID types.NodeID) *VLESSConfig {
	sec := s.cfg.Security
	if sec == "" {
		sec = "reality_xtls"
	}
	if sec == "reality" {
		sec = "reality_xtls"
	}
	return &VLESSConfig{
		ID:        NodeUUID(nodeID, s.cfg.Secret),
		Network:   "tcp",
		Address:   s.cfg.ListenAddr,
		Port:      NodePort(nodeID, s.cfg.Secret, s.cfg.BaseListenPort, s.cfg.MaxListenPort),
		Security:  sec,
		Timeout:   s.cfg.Timeout,
		Dest:      s.cfg.Reality.Dest,
		FP:        s.cfg.UTLSFingerprint,
		PublicKey: s.cfg.Reality.PublicKey,
		ShortID:   s.cfg.Reality.ShortID,
		SpiderX:   s.cfg.Reality.SpiderX,
	}
}

// EnsureNodeListener starts a listener for the node if one is not already
// running, and returns the node's VLESS endpoint configuration.
func (s *Server) EnsureNodeListener(ctx context.Context, nodeID types.NodeID) (*VLESSConfig, error) {
	config := s.NodeConfig(nodeID)

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.listeners[nodeID]; ok {
		return config, nil
	}

	ctx, cancel := context.WithCancel(ctx)
	ln, err := net.Listen("tcp", net.JoinHostPort(config.Address, fmt.Sprintf("%d", config.Port)))
	if err != nil {
		cancel()
		return nil, fmt.Errorf("listening for node %d on port %d: %w", nodeID, config.Port, err)
	}

	nl := &nodeListener{
		nodeID: nodeID,
		port:   config.Port,
		ln:     ln,
		cancel: cancel,
		done:   make(chan struct{}),
	}
	s.listeners[nodeID] = nl

	go s.acceptLoop(ctx, nl)

	return config, nil
}

// Shutdown closes all per-node listeners and waits for the accept loops to
// exit.
func (s *Server) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	listeners := make([]*nodeListener, 0, len(s.listeners))
	for _, nl := range s.listeners {
		listeners = append(listeners, nl)
	}
	s.listeners = make(map[types.NodeID]*nodeListener)
	s.mu.Unlock()

	for _, nl := range listeners {
		nl.cancel()
		_ = nl.ln.Close()
	}

	done := make(chan struct{})
	go func() {
		for _, nl := range listeners {
			<-nl.done
		}
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Server) acceptLoop(ctx context.Context, nl *nodeListener) {
	defer close(nl.done)

	for {
		conn, err := nl.ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return
			default:
				log.Error().Err(err).Uint64("node_id", uint64(nl.nodeID)).Msg("xray: accept error")
				continue
			}
		}

		go s.handleConn(ctx, nl.nodeID, conn)
	}
}

func (s *Server) handleConn(ctx context.Context, nodeID types.NodeID, conn net.Conn) {
	defer conn.Close()

	security := s.cfg.Security
	if security == "reality" {
		security = "reality_xtls"
	}
	// Reality path uses xtls/reality which steals the dest handshake;
	// plain tls/xtls path uses utls.
	if s.realityConfig != nil && security == "reality_xtls" {
		rConn, err := reality.Server(ctx, conn, s.realityConfig)
		if err != nil {
			log.Error().Err(err).Uint64("node_id", uint64(nodeID)).Msg("xray: Reality handshake failed")
			return
		}
		conn = rConn
	} else if s.tlsConfig != nil {
		if security == "reality_xtls" {
			log.Debug().
				Uint64("node_id", uint64(nodeID)).
				Str("utls", s.cfg.UTLSFingerprint).
				Str("reality_dest", s.cfg.Reality.Dest).
				Msg("xray: reality_xtls fallback TLS handshake")
		}
		tlsConn := utls.Server(conn, s.tlsConfig)
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			log.Error().Err(err).Uint64("node_id", uint64(nodeID)).Msg("xray: TLS handshake failed")
			return
		}
		conn = tlsConn
	}

	deadline := time.Now().Add(s.cfg.Timeout)
	if err := conn.SetDeadline(deadline); err != nil {
		return
	}

	clientUUID, rest, err := ParseVLESSRequest(conn)
	if err != nil {
		log.Warn().Err(err).Str("remote_addr", conn.RemoteAddr().String()).Msg("xray: malformed VLESS header")
		return
	}

	// The header has been consumed; the payload stream may now run
	// indefinitely.
	_ = conn.SetDeadline(time.Time{})

	expected := NodeUUID(nodeID, s.cfg.Secret)
	if clientUUID != expected {
		log.Warn().
			Uint64("node_id", uint64(nodeID)).
			Str("got_uuid", clientUUID).
			Str("want_uuid", expected).
			Msg("xray: rejecting connection: UUID mismatch")
		return
	}

	// VLESS requires the server to confirm the session with a single
	// protocol-version byte before any payload is exchanged.
	if _, err := conn.Write([]byte{vlessVersion}); err != nil {
		log.Error().
			Err(err).
			Uint64("node_id", uint64(nodeID)).
			Msg("xray: writing VLESS version response")
		return
	}

	s.handler(&bufferedConn{Conn: conn, r: rest})
}
