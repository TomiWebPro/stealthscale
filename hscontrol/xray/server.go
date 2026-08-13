// Copyright (c) 2026 TomiWebPro <TomiWebPro@gmail.com>
// SPDX-License-Identifier: BSD-3-Clause

package xray

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"net"
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
func NewServer(cfg *types.XRayConfig, handler func(net.Conn)) (*Server, error) {
	s := &Server{
		cfg:       cfg,
		handler:   handler,
		listeners: make(map[types.NodeID]*nodeListener),
	}

	if cfg.Security == "tls" || cfg.Security == "xtls" {
		cert, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("loading xray TLS credentials: %w", err)
		}
		s.tlsConfig = &tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS12,
		}
	}

	return s, nil
}

// NodeConfig returns the VLESS endpoint configuration for a node,
// independent of whether a listener is currently running.
func (s *Server) NodeConfig(nodeID types.NodeID) *VLESSConfig {
	return &VLESSConfig{
		ID:       NodeUUID(nodeID),
		Network:  "tcp",
		Address:  s.cfg.ListenAddr,
		Port:     NodePort(nodeID, s.cfg.BaseListenPort, s.cfg.MaxListenPort),
		Security: s.cfg.Security,
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

	if s.tlsConfig != nil {
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
