// Copyright (c) 2026 TomiWebPro <TomiWebPro@gmail.com>
// SPDX-License-Identifier: BSD-3-Clause

package xray

import (
	"bytes"
	"context"
	"net"
	"testing"
	"time"

	"github.com/gofrs/uuid/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteVLESSRequest(t *testing.T) {
	// Valid UUID round-trips via ParseVLESSRequest.
	u := uuid.Must(uuid.NewV4()).String()
	var buf bytes.Buffer
	require.NoError(t, WriteVLESSRequest(&buf, u))
	data := buf.Bytes()
	require.Len(t, data, 1+vlessUUIDLen+1)
	assert.Equal(t, byte(vlessVersion), data[0])
	assert.Equal(t, byte(0), data[len(data)-1])

	// Parse back.
	clientUUID, rest, err := ParseVLESSRequest(bytes.NewReader(data))
	require.NoError(t, err)
	assert.Equal(t, u, clientUUID)
	// rest should be empty (no payload)
	remaining, err := rest.ReadByte()
	assert.Error(t, err, "rest should be EOF")
	assert.Equal(t, byte(0), remaining)

	// Invalid UUID
	var buf2 bytes.Buffer
	assert.Error(t, WriteVLESSRequest(&buf2, "not-a-uuid"))
	assert.Error(t, WriteVLESSRequest(&buf2, ""))
}

func TestParseVLESSURI(t *testing.T) {
	// Valid reality_xtls URI
	u := uuid.Must(uuid.NewV4()).String()
	uri := "vless://" + u + "@10.0.0.1:10001?security=reality_xtls&fp=chrome&type=tcp&flow=xtls-rprx-vision"
	cfg, err := ParseVLESSURI(uri)
	require.NoError(t, err)
	assert.Equal(t, u, cfg.ID)
	assert.Equal(t, "10.0.0.1", cfg.Address)
	assert.Equal(t, 10001, cfg.Port)
	assert.Equal(t, "reality_xtls", cfg.Security)

	// none security keeps none
	uri2 := "vless://" + u + "@127.0.0.1:20001?security=none"
	cfg2, err := ParseVLESSURI(uri2)
	require.NoError(t, err)
	assert.Equal(t, "none", cfg2.Security)

	// reality alias normalised
	uri3 := "vless://" + u + "@127.0.0.1:20001?security=reality"
	cfg3, err := ParseVLESSURI(uri3)
	require.NoError(t, err)
	assert.Equal(t, "reality_xtls", cfg3.Security)

	// missing prefix
	_, err = ParseVLESSURI("http://" + u + "@127.0.0.1:10001")
	assert.Error(t, err)

	// missing @
	_, err = ParseVLESSURI("vless://no-at-sign")
	assert.Error(t, err)

	// bad port
	_, err = ParseVLESSURI("vless://" + u + "@127.0.0.1:notaport?security=none")
	assert.Error(t, err)

	// empty
	_, err = ParseVLESSURI("")
	assert.Error(t, err)

	// default security when query missing
	uri4 := "vless://" + u + "@127.0.0.1:10001"
	cfg4, err := ParseVLESSURI(uri4)
	require.NoError(t, err)
	assert.Equal(t, "reality_xtls", cfg4.Security)

	// round-trip via VLESSConfig.URI
	cfg5 := &VLESSConfig{ID: u, Address: "1.2.3.4", Port: 10001, Security: "none", Timeout: 30 * time.Second}
	require.NoError(t, cfg5.Validate())
	uri5 := cfg5.URI()
	parsed5, err := ParseVLESSURI(uri5)
	require.NoError(t, err)
	assert.Equal(t, cfg5.ID, parsed5.ID)
	assert.Equal(t, cfg5.Address, parsed5.Address)
	assert.Equal(t, cfg5.Port, parsed5.Port)
}

func TestDialVLESS_Plain(t *testing.T) {
	// Start a fake VLESS server that accepts one connection, validates header, acks.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()
	addr := ln.Addr().String()
	host, portStr, _ := net.SplitHostPort(addr)
	// Extract port
	var port int
	_, _ = net.LookupPort("tcp", portStr)
	// Actually parse portStr
	parsedPort := 0
	for _, c := range portStr {
		parsedPort = parsedPort*10 + int(c-'0')
	}
	port = parsedPort

	u := uuid.Must(uuid.NewV4()).String()
	cfg := &VLESSConfig{ID: u, Address: host, Port: port, Security: "none", Timeout: 2 * time.Second}

	// Server goroutine
	done := make(chan string, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			done <- "accept:" + err.Error()
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
		clientUUID, rest, err := ParseVLESSRequest(conn)
		if err != nil {
			done <- "parse:" + err.Error()
			return
		}
		// Ack
		if _, err := conn.Write([]byte{vlessVersion}); err != nil {
			done <- "write ack:" + err.Error()
			return
		}
		// Consume rest
		_ = rest
		done <- clientUUID
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := DialVLESS(ctx, cfg)
	require.NoError(t, err, "DialVLESS plain should succeed")
	defer conn.Close()
	// Verify server saw correct UUID
	select {
	case got := <-done:
		assert.Equal(t, u, got)
	case <-time.After(3 * time.Second):
		t.Fatal("server did not receive header")
	}
	// Connection should be usable (write payload)
	_, err = conn.Write([]byte("hello"))
	require.NoError(t, err)
}

func TestDialVLESS_InvalidConfig(t *testing.T) {
	ctx := context.Background()
	// nil config
	_, err := DialVLESS(ctx, nil)
	assert.Error(t, err)

	// invalid port
	u := uuid.Must(uuid.NewV4()).String()
	cfg := &VLESSConfig{ID: u, Address: "127.0.0.1", Port: 0, Security: "none", Timeout: time.Second}
	_, err = DialVLESS(ctx, cfg)
	assert.Error(t, err)

	// bad security
	cfg = &VLESSConfig{ID: u, Address: "127.0.0.1", Port: 10001, Security: "bogus", Timeout: time.Second}
	_, err = DialVLESS(ctx, cfg)
	assert.Error(t, err)
}

func TestDialVLESS_WrongUUIDRejected(t *testing.T) {
	// Server expects uuid A, client sends B -> server closes without ack, client should fail reading ack.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()
	addr := ln.Addr().String()
	host, portStr, _ := net.SplitHostPort(addr)
	port := 0
	for _, c := range portStr {
		port = port*10 + int(c-'0')
	}
	expectedUUID := uuid.Must(uuid.NewV4()).String()
	clientUUID := uuid.Must(uuid.NewV4()).String()
	require.NotEqual(t, expectedUUID, clientUUID)

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
		gotUUID, _, err := ParseVLESSRequest(conn)
		if err != nil {
			return
		}
		if gotUUID != expectedUUID {
			// Simulate server rejecting: close without ack
			return
		}
		_, _ = conn.Write([]byte{vlessVersion})
	}()

	cfg := &VLESSConfig{ID: clientUUID, Address: host, Port: port, Security: "none", Timeout: 2 * time.Second}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err = DialVLESS(ctx, cfg)
	assert.Error(t, err, "wrong UUID should be rejected (server closes without ack)")
}
