// Copyright (c) 2026 TomiWebPro <TomiWebPro@gmail.com>
// SPDX-License-Identifier: BSD-3-Clause

package xray

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	cryptorand "crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"testing"
	"time"

	"github.com/gofrs/uuid/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	reality "github.com/xtls/reality"

	"github.com/tomiwebpro/stealthscale/hscontrol/types"
)

func primeRealityLens(key string) {
	reality.GlobalPostHandshakeRecordsLens.Store(key, []int{})
	reality.GlobalMaxCSSMsgCount.Store(key, 1)
}

func TestNodeUUIDDeterministic(t *testing.T) {
	id := types.NodeID(42)

	first := NodeUUID(id, "")
	second := NodeUUID(id, "")

	assert.Equal(t, first, second, "UUID must be stable across calls")
	assert.NotEqual(t, NodeUUID(types.NodeID(43), ""), first, "different nodes must get different UUIDs")

	parsed, err := uuid.FromString(first)
	require.NoError(t, err, "UUID must be a valid UUID")
	assert.Equal(t, uuid.V5, parsed.Version())
}

func TestNodePortDeterministic(t *testing.T) {
	minPort, maxPort := 10001, 10100
	id := types.NodeID(7)

	first := NodePort(id, "", minPort, maxPort)
	second := NodePort(id, "", minPort, maxPort)

	assert.Equal(t, first, second, "port must be stable across calls")
	assert.GreaterOrEqual(t, first, minPort)
	assert.LessOrEqual(t, first, maxPort)

	ports := make(map[int]bool)
	for i := 1; i <= 50; i++ {
		p := NodePort(types.NodeID(i), "", minPort, maxPort)
		assert.GreaterOrEqual(t, p, minPort)
		assert.LessOrEqual(t, p, maxPort)
		ports[p] = true
	}
	assert.Greater(t, len(ports), 1, "port derivation must spread across the range")
}

func TestNodePortDegenerateRange(t *testing.T) {
	assert.Equal(t, 443, NodePort(types.NodeID(1), "", 443, 443))
}

func vlessHeader(t *testing.T, nodeID types.NodeID) []byte {
	t.Helper()

	u, err := uuid.FromString(NodeUUID(nodeID, ""))
	require.NoError(t, err)

	header := make([]byte, 1+vlessUUIDLen+vlessAddonsLenLen)
	header[0] = vlessVersion
	copy(header[1:1+vlessUUIDLen], u.Bytes())
	header[1+vlessUUIDLen] = 0 // no addons

	return header
}

func TestParseVLESSRequest(t *testing.T) {
	nodeID := types.NodeID(99)
	header := vlessHeader(t, nodeID)

	payload := []byte("payload-after-header")
	stream := bytes.NewReader(append(header, payload...))

	clientUUID, rest, err := ParseVLESSRequest(stream)
	require.NoError(t, err)
	assert.Equal(t, NodeUUID(nodeID, ""), clientUUID)

	got, err := io.ReadAll(rest)
	require.NoError(t, err)
	assert.Equal(t, payload, got)
}

func TestParseVLESSRequestRejectsBadVersion(t *testing.T) {
	nodeID := types.NodeID(99)
	u, err := uuid.FromString(NodeUUID(nodeID, ""))
	require.NoError(t, err)

	stream := bytes.NewReader(append([]byte{1}, append(u.Bytes(), 0)...))

	_, _, err = ParseVLESSRequest(stream)
	assert.Error(t, err)
}

func TestParseVLESSRequestSkipsAddons(t *testing.T) {
	nodeID := types.NodeID(99)
	u, err := uuid.FromString(NodeUUID(nodeID, ""))
	require.NoError(t, err)

	// addon: command (tcp) + port + addrType (domain) + len + domain
	addon := make([]byte, 0, 16)
	var cmd [2]byte
	binary.BigEndian.PutUint16(cmd[:], vlessCommandTCP)
	addon = append(addon, cmd[:]...)
	var port [2]byte
	binary.BigEndian.PutUint16(port[:], 443)
	addon = append(addon, port[:]...)
	addon = append(addon, 0x02) // domain
	addon = append(addon, 9)    // length
	addon = append(addon, []byte("example.com")...)
	var pathLen [2]byte
	addon = append(addon, pathLen[:]...)

	stream := bytes.NewReader([]byte{0})
	data := make([]byte, 0, 1+vlessUUIDLen+1+len(addon)+len("rest-of-stream"))
	data = append(data, 0)
	data = append(data, u.Bytes()...)
	data = append(data, byte(len(addon)))
	data = append(data, addon...)
	data = append(data, []byte("rest-of-stream")...)
	stream = bytes.NewReader(data)

	clientUUID, rest, err := ParseVLESSRequest(stream)
	require.NoError(t, err)
	assert.Equal(t, NodeUUID(nodeID, ""), clientUUID)

	got, err := io.ReadAll(rest)
	require.NoError(t, err)
	assert.Equal(t, []byte("rest-of-stream"), got)
}

func testXRayConfig() types.XRayConfig {
	return types.XRayConfig{
		Enabled:        true,
		ListenAddr:     "127.0.0.1",
		BaseListenPort: 20001,
		MaxListenPort:  20010,
		Security:       "none",
		Timeout:        time.Second,
	}
}

func TestServerAcceptsValidNode(t *testing.T) {
	cfg := testXRayConfig()
	nodeID := types.NodeID(1001)

	handlerCalls := make(chan string, 1)
	server, err := NewServer(&cfg, func(conn net.Conn) {
		payload, err := io.ReadAll(conn)
		require.NoError(t, err)
		handlerCalls <- string(payload)
	})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer server.Shutdown(context.Background())

	config, err := server.EnsureNodeListener(ctx, nodeID)
	require.NoError(t, err)

	conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", cfg.ListenAddr, config.Port), time.Second)
	require.NoError(t, err)
	defer conn.Close()

	_, err = conn.Write(vlessHeader(t, nodeID))
	require.NoError(t, err)

	// The server must confirm the session with the VLESS version byte
	// before any payload flows.
	var ack [1]byte
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(2*time.Second)))
	_, err = io.ReadFull(conn, ack[:])
	require.NoError(t, err, "server should ack the VLESS handshake")
	assert.Equal(t, uint8(vlessVersion), ack[0], "server should reply with the VLESS version byte")

	_, err = conn.Write([]byte("hello-vless"))
	require.NoError(t, err)

	// Half-close so the handler's ReadAll sees EOF after the payload.
	tcpConn, ok := conn.(*net.TCPConn)
	require.True(t, ok)
	require.NoError(t, tcpConn.CloseWrite())

	select {
	case got := <-handlerCalls:
		assert.Equal(t, "hello-vless", got)
	case <-time.After(2 * time.Second):
		t.Fatal("handler was never invoked")
	}
}

func TestServerRejectsWrongUUID(t *testing.T) {
	cfg := testXRayConfig()
	nodeID := types.NodeID(1002)
	otherNode := types.NodeID(1003)

	handlerCalls := make(chan struct{}, 1)
	server, err := NewServer(&cfg, func(conn net.Conn) {
		handlerCalls <- struct{}{}
	})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer server.Shutdown(context.Background())

	config, err := server.EnsureNodeListener(ctx, nodeID)
	require.NoError(t, err)

	conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", cfg.ListenAddr, config.Port), time.Second)
	require.NoError(t, err)
	defer conn.Close()

	// Correct header shape, wrong UUID (belongs to another node).
	_, err = conn.Write(vlessHeader(t, otherNode))
	require.NoError(t, err)

	// The server must close the connection without invoking the handler.
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, err = conn.Read(make([]byte, 1))
	assert.Error(t, err, "connection should be closed after UUID mismatch")

	select {
	case <-handlerCalls:
		t.Fatal("handler must not be called for an invalid UUID")
	case <-time.After(500 * time.Millisecond):
	}
}

func TestServerEnsureListenerIdempotent(t *testing.T) {
	cfg := testXRayConfig()
	server, err := NewServer(&cfg, func(conn net.Conn) {})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer server.Shutdown(context.Background())

	_, err = server.EnsureNodeListener(ctx, types.NodeID(1))
	require.NoError(t, err)
	_, err = server.EnsureNodeListener(ctx, types.NodeID(1))
	require.NoError(t, err, "second EnsureNodeListener must be a no-op")
}

func TestServerShutdown(t *testing.T) {
	cfg := testXRayConfig()
	server, err := NewServer(&cfg, func(conn net.Conn) {})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_, err = server.EnsureNodeListener(ctx, types.NodeID(2))
	require.NoError(t, err)

	done := make(chan error, 1)
	go func() {
		done <- server.Shutdown(context.Background())
	}()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown did not complete")
	}
}

// startLocalRealityDest starts a minimal TLS 1.3 dest server that Reality
// steals its handshake from. It presents a self-signed cert for both decoys
// so SNI verification is not a factor. Returns the address (host:port) and a
// shutdown func.
func startLocalRealityDest(t *testing.T) (string, func()) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), cryptorand.Reader)
	require.NoError(t, err)
	serial, err := cryptorand.Int(cryptorand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	require.NoError(t, err)
	template := x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "www.cloudflare.com"},
		DNSNames:     []string{"www.cloudflare.com", "www.microsoft.com", "cloudflare.com", "microsoft.com"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(cryptorand.Reader, &template, &template, &priv.PublicKey, priv)
	require.NoError(t, err)
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(priv)
	require.NoError(t, err)
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	require.NoError(t, err)

	tlsCfg := &tls.Config{Certificates: []tls.Certificate{cert}}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", tlsCfg)
	require.NoError(t, err)
	done := make(chan struct{})
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				select {
				case <-done:
					return
				default:
					continue
				}
			}
			go func(c net.Conn) {
				defer c.Close()
				// Keep the connection open long enough for Reality to probe it.
				// The TLS handshake is driven by the peer's ClientHello; we just
				// wait and discard.
				_ = c.SetDeadline(time.Now().Add(10 * time.Second))
				_, _ = io.Copy(io.Discard, c)
			}(conn)
		}
	}()
	addr := ln.Addr().String()
	return addr, func() {
		close(done)
		ln.Close()
	}
}

// TestServerRealityTLSRoundTrip exercises the full Reality path: a server
// using xtls/reality that steals a local dest's TLS handshake, and a client
// using the vendored RealityUClient (utls + Reality auth). The handshake must
// succeed and VLESS payload must round-trip, proving the transport is not
// silently regressed to the old self-signed decoy.
func TestServerRealityTLSRoundTrip(t *testing.T) {
	destAddr, closeDest := startLocalRealityDest(t)
	defer closeDest()

	cfg := testXRayConfig()
	cfg.Security = "reality_xtls"
	cfg.UTLSFingerprint = "chrome"
	cfg.Reality.Dest = destAddr
	cfg.Reality.ServerNames = []string{"www.cloudflare.com", "www.microsoft.com", "cloudflare.com", "microsoft.com", "127.0.0.1"}
	// Derive stable Reality keypair/shortId from an ephemeral secret.
	// Use a temp dir so InitIdentity persists correctly.
	tmpDir := t.TempDir()
	require.NoError(t, cfg.InitIdentity(tmpDir))

	// Prime the global post-handshake lens so the server doesn't block
	// waiting for DetectPostHandshakeRecordsLens (which would fail against
	// our self-signed local dest because it verifies the cert). An empty
	// slice means "no post-handshake records to mimic".
	for _, sni := range cfg.Reality.ServerNames {
		for alpn := 0; alpn < 3; alpn++ {
			key := destAddr + " " + sni + " " + fmt.Sprintf("%d", alpn)
			// Import is via the reality package's globals — we set them here
			// to avoid the 5s polling loop in reality.Server.
			primeRealityLens(key)
		}
	}

	nodeID := types.NodeID(2001)

	handlerCalls := make(chan string, 1)
	server, err := NewServer(&cfg, func(conn net.Conn) {
		payload, _ := io.ReadAll(conn)
		if len(payload) > 0 {
			handlerCalls <- string(payload)
		}
	})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer server.Shutdown(context.Background())

	config, err := server.EnsureNodeListener(ctx, nodeID)
	require.NoError(t, err)

	clientCfg := &VLESSConfig{
		ID:        NodeUUID(nodeID, cfg.Secret),
		Network:   "tcp",
		Address:   cfg.ListenAddr,
		Port:      config.Port,
		Security:  "reality_xtls",
		Timeout:   5 * time.Second,
		Dest:      destAddr,
		FP:        "chrome",
		PublicKey: cfg.Reality.PublicKey,
		ShortID:   cfg.Reality.ShortID,
		SpiderX:   "/",
	}

	conn, err := DialVLESS(context.Background(), clientCfg)
	require.NoError(t, err, "reality_xtls handshake must succeed with RealityUClient")
	defer conn.Close()

	_, err = conn.Write([]byte("stealth-payload"))
	require.NoError(t, err)

	if cw, ok := conn.(interface{ CloseWrite() error }); ok {
		_ = cw.CloseWrite()
	}

	select {
	case got := <-handlerCalls:
		assert.Equal(t, "stealth-payload", got)
	case <-time.After(8 * time.Second):
		t.Fatal("handler was never invoked over reality_xtls (Reality handshake likely failed)")
	}
}

func TestBufferedConnPreservesBufferedBytes(t *testing.T) {
	raw := bytes.NewReader([]byte("buffered-payload"))
	br := bufio.NewReader(raw)

	// Simulate: bufio already consumed everything; the wrapper must still
	// yield it via Read.
	conn := &bufferedConn{
		Conn: net.Conn(new(nopConn)),
		r:    br,
	}

	got, err := io.ReadAll(conn)
	require.NoError(t, err)
	assert.Equal(t, []byte("buffered-payload"), got)
}

// nopConn is a net.Conn that does nothing; used to test bufferedConn in
// isolation.
type nopConn struct{}

func (n *nopConn) Read(_ []byte) (int, error)    { return 0, io.EOF }
func (n *nopConn) Write(_ []byte) (int, error)   { return 0, io.EOF }
func (n *nopConn) Close() error                  { return nil }
func (n *nopConn) LocalAddr() net.Addr           { return dummyAddr("local") }
func (n *nopConn) RemoteAddr() net.Addr          { return dummyAddr("remote") }
func (n *nopConn) SetDeadline(_ time.Time) error { return nil }
func (n *nopConn) SetReadDeadline(_ time.Time) error {
	return nil
}
func (n *nopConn) SetWriteDeadline(_ time.Time) error { return nil }

type dummyAddr string

func (a dummyAddr) Network() string { return "test" }
func (a dummyAddr) String() string  { return string(a) }
