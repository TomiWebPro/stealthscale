// Copyright (c) 2026 TomiWebPro <TomiWebPro@gmail.com>
// SPDX-License-Identifier: BSD-3-Clause

package servertest

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/http2"

	"github.com/tomiwebpro/stealthscale/hscontrol/types"
	"github.com/tomiwebpro/stealthscale/hscontrol/xray"
	"tailscale.com/control/controlbase"
	"tailscale.com/tailcfg"
	"tailscale.com/types/key"
)

const (
	xrayTestPortStart = 41000
	xrayTestPortEnd   = 41999
)

// dialVLESS opens a TCP connection to a node's VLESS endpoint and performs
// the VLESS client handshake, exactly as a stealthscale-patched Tailscale
// client does before starting the noise handshake. It returns the
// authenticated stream.
func dialVLESS(tb testing.TB, addr string, nodeID types.NodeID) net.Conn {
	tb.Helper()

	conn, err := net.Dial("tcp", addr)
	require.NoError(tb, err)
	tb.Cleanup(func() { conn.Close() })

	uuidBytes, err := uuid.Parse(xray.NodeUUID(nodeID))
	require.NoError(tb, err)

	header := make([]byte, 0, 18)
	header = append(header, 0) // VLESS protocol version
	header = append(header, uuidBytes[:]...)
	header = append(header, 0) // addons length

	require.NoError(tb, conn.SetDeadline(time.Now().Add(10*time.Second)))

	_, err = conn.Write(header)
	require.NoError(tb, err)

	// The server answers the VLESS handshake with a single version byte.
	var ack [1]byte
	_, err = io.ReadFull(conn, ack[:])
	require.NoError(tb, err, "server should ack the VLESS handshake")
	require.Equal(tb, byte(0), ack[0], "server should reply with VLESS version 0")

	require.NoError(tb, conn.SetDeadline(time.Time{}))

	return conn
}

// registerViaVLESS sends a [tailcfg.RegisterRequest] through the raw noise
// client protocol — a controlbase handshake followed by an HTTP/2 machine
// API request — carried over a VLESS-authenticated stream. This mirrors the
// protocol a stealthscale-patched Tailscale client speaks.
func registerViaVLESS(
	tb testing.TB,
	conn net.Conn,
	machineKey key.MachinePrivate,
	serverPub key.MachinePublic,
	regReq tailcfg.RegisterRequest,
) tailcfg.RegisterResponse {
	tb.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	noiseConn, err := controlbase.Client(ctx, conn, machineKey, serverPub, 1)
	require.NoError(tb, err, "noise handshake over VLESS")

	tr := &http2.Transport{
		AllowHTTP: true,
		DialTLSContext: func(context.Context, string, string, *tls.Config) (net.Conn, error) {
			return noiseConn, nil
		},
	}
	defer tr.CloseIdleConnections()

	body, err := json.Marshal(regReq)
	require.NoError(tb, err)

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		"http://control/machine/register",
		bytes.NewReader(body),
	)
	require.NoError(tb, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := tr.RoundTrip(req)
	require.NoError(tb, err)
	defer resp.Body.Close()

	require.Equal(tb, http.StatusOK, resp.StatusCode)

	var regResp tailcfg.RegisterResponse
	require.NoError(tb, json.NewDecoder(resp.Body).Decode(&regResp))
	require.Empty(tb, regResp.Error, "register response should not carry an error")

	return regResp
}

// TestVLESSNoiseRegistrationE2E exercises the full stealthscale transport
// stack: a client dials a node's VLESS endpoint, authenticates with the
// node's deterministic UUID, runs the Tailscale noise handshake over the
// VLESS stream, and registers the machine through the HTTP/2 machine API.
//
// The node is pre-provisioned and bound to the client's machine key, the
// same flow an operator uses to hand a static VLESS URI to a client.
func TestVLESSNoiseRegistrationE2E(t *testing.T) {
	ts := NewServer(t, WithXRay("127.0.0.1", xrayTestPortStart, xrayTestPortEnd))

	user := ts.CreateUser(t, "vless-e2e")
	pakKey := ts.CreatePreAuthKey(t, types.UserID(user.ID))

	machineKey := key.NewMachine()
	nodeKey := key.NewNode()

	// Pre-provision the node, then bind it to the machine key and node key
	// the raw client will present over the noise handshake.
	nv := ts.CreateRegisteredNode(t, user, "vless-e2e-node")
	node := nv.AsStruct()
	node.MachineKey = machineKey.Public()
	node.NodeKey = nodeKey.Public()
	ts.State().PutNodeInStoreForTest(*node)
	nodeID := nv.ID()

	ts.StartXRay(t)

	port := xray.NodePort(nodeID, xrayTestPortStart, xrayTestPortEnd)
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))

	conn := dialVLESS(t, addr, nodeID)
	resp := registerViaVLESS(t, conn, machineKey, ts.App.NoisePublicKey(),
		tailcfg.RegisterRequest{
			Version: tailcfg.CurrentCapabilityVersion,
			NodeKey: nodeKey.Public(),
			Auth: &tailcfg.RegisterResponseAuth{
				AuthKey: pakKey,
			},
			Hostinfo: &tailcfg.Hostinfo{Hostname: "vless-e2e"},
		})

	require.True(t, resp.MachineAuthorized, "pre-auth key registration should be authorized")
	require.False(t, resp.NodeKeyExpired)

	// The re-registration must have bound to the pre-provisioned node rather
	// than minting a duplicate.
	nodes := ts.State().ListNodes()
	require.Equal(t, 1, nodes.Len(), "re-registration must not create a duplicate node")

	found, ok := ts.State().GetNodeByID(nodeID)
	require.True(t, ok)
	require.Equal(t, machineKey.Public(), found.MachineKey())
	require.Equal(t, nodeKey.Public(), found.NodeKey())
}

// TestVLESSRejectsWrongUUID verifies that a connection presenting a UUID
// that does not match the node the listener belongs to is refused: the
// server must not ack the VLESS handshake.
func TestVLESSRejectsWrongUUID(t *testing.T) {
	ts := NewServer(t, WithXRay("127.0.0.1", xrayTestPortStart, xrayTestPortEnd))

	user := ts.CreateUser(t, "vless-wrong-uuid")
	nv := ts.CreateRegisteredNode(t, user, "vless-node-a")
	other := ts.CreateRegisteredNode(t, user, "vless-node-b")
	ts.StartXRay(t)

	port := xray.NodePort(nv.ID(), xrayTestPortStart, xrayTestPortEnd)
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))

	// Dial node A's listener but present node B's UUID.
	conn, err := net.Dial("tcp", addr)
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close() })

	uuidBytes, err := uuid.Parse(xray.NodeUUID(other.ID()))
	require.NoError(t, err)

	header := make([]byte, 0, 18)
	header = append(header, 0)
	header = append(header, uuidBytes[:]...)
	header = append(header, 0)

	require.NoError(t, conn.SetDeadline(time.Now().Add(10*time.Second)))
	_, err = conn.Write(header)
	require.NoError(t, err)

	// The server must close the stream without sending the version ack.
	var ack [1]byte
	_, err = io.ReadFull(conn, ack[:])
	require.Error(t, err, "server must not ack a connection with the wrong UUID")
}

// TestVLESSListenerAutoCreatedForRegisteredNode verifies that a node
// registering through the regular noise path automatically gets its VLESS
// listener, bound to the node's deterministic port and UUID. A subsequent
// wake-up register (tailscaled restart) is then served over the VLESS
// transport.
func TestVLESSListenerAutoCreatedForRegisteredNode(t *testing.T) {
	ts := NewServer(t, WithXRay("127.0.0.1", xrayTestPortStart, xrayTestPortEnd))
	ts.StartXRay(t)

	client := NewClient(t, ts, "xray-hook-node")

	nodes := ts.State().ListNodes()
	require.Equal(t, 1, nodes.Len())
	nodeID := nodes.At(0).ID()

	port := xray.NodePort(nodeID, xrayTestPortStart, xrayTestPortEnd)
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))

	// The registration hook must have started a listener for the node even
	// though it registered through the plain noise path.
	conn := dialVLESS(t, addr, nodeID)

	// A wake-up register (no auth key, same machine key and node key) is
	// answered over the VLESS transport with the node's current state.
	resp := registerViaVLESS(t, conn, client.MachineKey(), ts.App.NoisePublicKey(),
		tailcfg.RegisterRequest{
			Version: tailcfg.CurrentCapabilityVersion,
			NodeKey: nodes.At(0).NodeKey(),
		})

	require.False(t, resp.NodeKeyExpired)
	require.True(t, resp.MachineAuthorized)
}
