// Copyright (c) 2026 TomiWebPro <TomiWebPro@gmail.com>
// SPDX-License-Identifier: BSD-3-Clause

package xray

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"testing"
	"time"

	"github.com/gofrs/uuid/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tomiwebpro/stealthscale/hscontrol/types"
)

func TestNodeUUIDDeterministic(t *testing.T) {
	id := types.NodeID(42)

	first := NodeUUID(id)
	second := NodeUUID(id)

	assert.Equal(t, first, second, "UUID must be stable across calls")
	assert.NotEqual(t, NodeUUID(types.NodeID(43)), first, "different nodes must get different UUIDs")

	parsed, err := uuid.FromString(first)
	require.NoError(t, err, "UUID must be a valid UUID")
	assert.Equal(t, uuid.V5, parsed.Version())
}

func TestNodePortDeterministic(t *testing.T) {
	minPort, maxPort := 10001, 10100
	id := types.NodeID(7)

	first := NodePort(id, minPort, maxPort)
	second := NodePort(id, minPort, maxPort)

	assert.Equal(t, first, second, "port must be stable across calls")
	assert.GreaterOrEqual(t, first, minPort)
	assert.LessOrEqual(t, first, maxPort)

	ports := make(map[int]bool)
	for i := 1; i <= 50; i++ {
		p := NodePort(types.NodeID(i), minPort, maxPort)
		assert.GreaterOrEqual(t, p, minPort)
		assert.LessOrEqual(t, p, maxPort)
		ports[p] = true
	}
	assert.Greater(t, len(ports), 1, "port derivation must spread across the range")
}

func TestNodePortDegenerateRange(t *testing.T) {
	assert.Equal(t, 443, NodePort(types.NodeID(1), 443, 443))
}

func vlessHeader(t *testing.T, nodeID types.NodeID) []byte {
	t.Helper()

	u, err := uuid.FromString(NodeUUID(nodeID))
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
	assert.Equal(t, NodeUUID(nodeID), clientUUID)

	got, err := io.ReadAll(rest)
	require.NoError(t, err)
	assert.Equal(t, payload, got)
}

func TestParseVLESSRequestRejectsBadVersion(t *testing.T) {
	nodeID := types.NodeID(99)
	u, err := uuid.FromString(NodeUUID(nodeID))
	require.NoError(t, err)

	stream := bytes.NewReader(append([]byte{1}, append(u.Bytes(), 0)...))

	_, _, err = ParseVLESSRequest(stream)
	assert.Error(t, err)
}

func TestParseVLESSRequestSkipsAddons(t *testing.T) {
	nodeID := types.NodeID(99)
	u, err := uuid.FromString(NodeUUID(nodeID))
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
	assert.Equal(t, NodeUUID(nodeID), clientUUID)

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
