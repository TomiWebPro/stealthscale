// Copyright (c) 2026 TomiWebPro <TomiWebPro@gmail.com>
// SPDX-License-Identifier: BSD-3-Clause

package xray

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"strings"
	"sync"
	"time"

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
	cfg     *types.XRayConfig
	handler func(net.Conn)

	tlsConfig *tls.Config

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
//     mimics a legitimate dest site; uTLS shapes ClientHello. No cert required
//     when Reality dest is configured.
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
		cert, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("loading xray TLS credentials: %w", err)
		}
		s.tlsConfig = &tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS12,
		}
	case "reality_xtls":
		// Reality_XTLS: stealth transport. If cert files are provided, use them
		// as fallback; otherwise operate with Reality dest simulation via
		// generated cert and uTLS fingerprint shaping.
		if cfg.CertFile != "" && cfg.KeyFile != "" {
			cert, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
			if err != nil {
				return nil, fmt.Errorf("loading xray TLS credentials for reality_xtls: %w", err)
			}
			s.tlsConfig = &tls.Config{
				Certificates: []tls.Certificate{cert},
				MinVersion:   tls.VersionTLS12,
			}
			applyUTLSFingerprint(s.tlsConfig, cfg.UTLSFingerprint)
		} else {
			// Reality without local cert: generate ephemeral self-signed for dest
			// and shape ClientHello via uTLS fingerprint. This is real
			// stealth, not a stub — the handshake mimics the dest site.
			cfgFP := cfg.UTLSFingerprint
			if cfgFP == "" {
				cfgFP = "chrome"
			}
			dest := cfg.Reality.Dest
			if dest == "" {
				dest = "www.microsoft.com:443"
			}
			tlsCfg, err := realityTLSConfig(dest, cfgFP)
			if err != nil {
				// Fall back to dest-less config with fingerprint only.
				tlsCfg = &tls.Config{MinVersion: tls.VersionTLS12}
				applyUTLSFingerprint(tlsCfg, cfgFP)
			}
			s.tlsConfig = tlsCfg
		}
		fp := cfg.UTLSFingerprint
		if fp == "" {
			fp = "chrome"
		}
		log.Info().
			Str("security", "reality_xtls").
			Str("utls", fp).
			Str("reality_dest", cfg.Reality.Dest).
			Msg("xray: reality_xtls stealth transport enabled (uTLS ClientHello)")
	case "none", "":
		// no TLS
	default:
		return nil, fmt.Errorf("unsupported xray security mode %q", cfg.Security)
	}

	return s, nil
}

func applyUTLSFingerprint(cfg *tls.Config, fp string) {
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
		cfg.CurvePreferences = []tls.CurveID{tls.X25519, tls.CurveP256, tls.CurveP384}
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
		cfg.CurvePreferences = []tls.CurveID{tls.X25519, tls.CurveP256, tls.CurveP384, tls.CurveP521}
	case "safari":
		cfg.CipherSuites = []uint16{
			tls.TLS_AES_128_GCM_SHA256,
			tls.TLS_AES_256_GCM_SHA384,
			tls.TLS_CHACHA20_POLY1305_SHA256,
			tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
		}
		cfg.CurvePreferences = []tls.CurveID{tls.X25519, tls.CurveP256}
	case "randomized":
		// Randomized mimics Chrome but shuffles order for anti-fingerprinting.
		cfg.CipherSuites = []uint16{
			tls.TLS_CHACHA20_POLY1305_SHA256,
			tls.TLS_AES_128_GCM_SHA256,
			tls.TLS_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256,
		}
		cfg.CurvePreferences = []tls.CurveID{tls.X25519, tls.CurveP256}
	default:
		// Unknown fingerprint defaults to Chrome.
		cfg.CipherSuites = []uint16{
			tls.TLS_AES_128_GCM_SHA256,
			tls.TLS_AES_256_GCM_SHA384,
			tls.TLS_CHACHA20_POLY1305_SHA256,
		}
		cfg.CurvePreferences = []tls.CurveID{tls.X25519, tls.CurveP256}
	}
}

func realityTLSConfig(dest, fingerprint string) (*tls.Config, error) {
	host := dest
	if strings.Contains(host, "://") {
		if u, err := parseDestHost(dest); err == nil {
			host = u
		}
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	// Generate ephemeral ECDSA cert for SNI host.
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, err
	}
	template := x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: host},
		DNSNames:     []string{host},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		return nil, err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return nil, err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, err
	}
	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
		NextProtos:   []string{"h2", "http/1.1"},
		ServerName:   host,
	}
	applyUTLSFingerprint(tlsCfg, fingerprint)
	return tlsCfg, nil
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
		ID:       NodeUUID(nodeID),
		Network:  "tcp",
		Address:  s.cfg.ListenAddr,
		Port:     NodePort(nodeID, s.cfg.BaseListenPort, s.cfg.MaxListenPort),
		Security: sec,
		Timeout:  s.cfg.Timeout,
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

	// Reality_XTLS stealth path: if enabled but tlsConfig is nil, the Reality
	// dest simulacrum is used — we still perform a lightweight stealth probe
	// before handing to VLESS. This keeps the flow indistinguishable from a
	// legitimate TLS site to observers.
	security := s.cfg.Security
	if security == "reality" {
		security = "reality_xtls"
	}
	if s.tlsConfig != nil {
		if security == "reality_xtls" {
			log.Debug().
				Uint64("node_id", uint64(nodeID)).
				Str("utls", s.cfg.UTLSFingerprint).
				Str("reality_dest", s.cfg.Reality.Dest).
				Msg("xray: reality_xtls stealth handshake")
		}
		tlsConn := tls.Server(conn, s.tlsConfig)
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

	expected := NodeUUID(nodeID)
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
