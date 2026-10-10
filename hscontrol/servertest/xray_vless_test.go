// Copyright (c) 2026 TomiWebPro <TomiWebPro@gmail.com>
// SPDX-License-Identifier: BSD-3-Clause

package servertest

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tomiwebpro/stealthscale/hscontrol/types"
	"github.com/tomiwebpro/stealthscale/hscontrol/webui"
	"github.com/tomiwebpro/stealthscale/hscontrol/xray"
	"golang.org/x/net/http2"
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

	uuidBytes, err := uuid.Parse(xray.NodeUUID(nodeID, ""))
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

	port := xray.NodePort(nodeID, "", xrayTestPortStart, xrayTestPortEnd)
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

	port := xray.NodePort(nv.ID(), "", xrayTestPortStart, xrayTestPortEnd)
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))

	// Dial node A's listener but present node B's UUID.
	conn, err := net.Dial("tcp", addr)
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close() })

	uuidBytes, err := uuid.Parse(xray.NodeUUID(other.ID(), ""))
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

	port := xray.NodePort(nodeID, "", xrayTestPortStart, xrayTestPortEnd)
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

// TestVLESSNodeEndpointOverrideRebindsListener pins the
// per-node VLESS endpoint override mechanism end-to-end
// (goal #4): a Web UI apply (POST /web/api/vless/{id} →
// state.SetNodeVLESS) rebinds the node's VLESS listener
// to the overridden endpoint, the read paths report the
// override, and an in-flight connection survives the
// rebind with its deadline intact. A Web UI clear
// (POST {"clear": true} → SetNodeVLESS with nil) is
// the path back: the listener rebinds to the derived
// endpoint and the read paths report it again.
func TestVLESSNodeEndpointOverrideRebindsListener(t *testing.T) {
	ts := NewServer(t, WithXRay("127.0.0.1", xrayTestPortStart, xrayTestPortEnd))

	user := ts.CreateUser(t, "vless-override")
	nv := ts.CreateRegisteredNode(t, user, "vless-override-node")
	nodeID := nv.ID()

	ts.StartXRay(t)

	derivedPort := xray.NodePort(nodeID, "", xrayTestPortStart, xrayTestPortEnd)
	derivedAddr := net.JoinHostPort("127.0.0.1", strconv.Itoa(derivedPort))

	// The node is served on its derived endpoint first;
	// keep this connection open across the rebind below.
	inFlight := dialVLESS(t, derivedAddr, nodeID)

	// Apply a per-node endpoint override through the Web
	// UI surface — the same delegation a Web UI client
	// uses. The change listeners fire synchronously, so
	// the listener is rebound before the apply returns.
	overridePort := derivedPort + 500

	h := webui.Handler(&types.Config{ //nolint:exhaustruct_v5 // test fixture: only the XRay section is under test
		XRay: types.XRayConfig{ //nolint:exhaustruct_v5 // test fixture: only fields under test
			Enabled:        true,
			ListenAddr:     "127.0.0.1",
			BaseListenPort: xrayTestPortStart,
			MaxListenPort:  xrayTestPortEnd,
			Timeout:        30 * time.Second,
		},
	}, ts.State())

	ui := httptest.NewServer(h)

	defer ui.Close()

	body := fmt.Sprintf(`{"address":"127.0.0.1","port":%d,"security":"none"}`, overridePort)

	applyReq, err := http.NewRequestWithContext(t.Context(), http.MethodPost, ui.URL+"/web/api/vless/"+nodeID.String(), strings.NewReader(body))
	require.NoError(t, err)

	applyReq.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(applyReq)
	require.NoError(t, err)

	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode, "applying a per-node endpoint override must succeed")

	// The override is stored in state and the read path
	// reports the overridden endpoint.
	require.NotNil(t, ts.State().NodeVLESS(nodeID))

	getReq, err := http.NewRequestWithContext(t.Context(), http.MethodGet, ui.URL+"/web/api/vless/"+nodeID.String(), nil)
	require.NoError(t, err)

	getResp, err := http.DefaultClient.Do(getReq)
	require.NoError(t, err)

	defer getResp.Body.Close()

	var got map[string]any

	require.NoError(t, json.NewDecoder(getResp.Body).Decode(&got))
	assert.Equal(t, "127.0.0.1", got["address"])
	assert.InDelta(t, overridePort, got["port"], 0)
	assert.Contains(t, got["uri"], fmt.Sprintf("127.0.0.1:%d", overridePort))
	assert.Contains(t, got["uri"], "security=none")

	// The listener moved to the overridden endpoint: it
	// serves the VLESS handshake...
	overrideAddr := net.JoinHostPort("127.0.0.1", strconv.Itoa(overridePort))

	rebound := dialVLESS(t, overrideAddr, nodeID)
	require.NotNil(t, rebound)

	// ...and the derived endpoint no longer accepts.
	derivedDialer := &net.Dialer{Timeout: 250 * time.Millisecond} //nolint:exhaustruct_v5 // test fixture: only fields under test

	_, err = derivedDialer.DialContext(t.Context(), "tcp", derivedAddr)
	require.Error(t, err, "the derived endpoint must stop accepting after the override rebinds the listener")

	// The in-flight connection from before the rebind
	// keeps its deadline: the server is still waiting
	// for the noise handshake on that stream, so a read
	// must time out rather than return EOF as a
	// torn-down stream would.
	require.NoError(t, inFlight.SetReadDeadline(time.Now().Add(300*time.Millisecond)))

	_, err = inFlight.Read(make([]byte, 1))

	var netErr net.Error

	require.ErrorAs(t, err, &netErr)
	assert.True(t, netErr.Timeout(), "in-flight connection must keep its deadline across the rebind")

	// Clear the override through the Web UI surface
	// itself (POST {"clear": true} → state.SetNodeVLESS
	// with nil): the listener must rebind back to the
	// config-derived endpoint. This is the operator
	// path back to the derived endpoint — without it
	// an applied override is stuck until a restart.
	clearReq, err := http.NewRequestWithContext(t.Context(), http.MethodPost, ui.URL+"/web/api/vless/"+nodeID.String(), strings.NewReader(`{"clear":true}`))
	require.NoError(t, err)

	clearReq.Header.Set("Content-Type", "application/json")

	clearResp, err := http.DefaultClient.Do(clearReq)
	require.NoError(t, err)

	clearResp.Body.Close()
	require.Equal(t, http.StatusOK, clearResp.StatusCode, "clearing a per-node endpoint override must succeed")

	// The override is gone from the store and the read
	// path reports the derived endpoint again.
	require.Nil(t, ts.State().NodeVLESS(nodeID))

	clearedGetReq, err := http.NewRequestWithContext(t.Context(), http.MethodGet, ui.URL+"/web/api/vless/"+nodeID.String(), nil)
	require.NoError(t, err)

	clearedGetResp, err := http.DefaultClient.Do(clearedGetReq)
	require.NoError(t, err)

	defer clearedGetResp.Body.Close()

	var cleared map[string]any

	require.NoError(t, json.NewDecoder(clearedGetResp.Body).Decode(&cleared))
	assert.InDelta(t, derivedPort, cleared["port"], 0, "a cleared override must report the derived endpoint again")
	assert.Contains(t, cleared["uri"], fmt.Sprintf("127.0.0.1:%d", derivedPort))

	// The derived endpoint accepts again...
	reDerived := dialVLESS(t, derivedAddr, nodeID)
	require.NotNil(t, reDerived)

	// ...and the overridden port no longer accepts.
	overrideDialer := &net.Dialer{Timeout: 250 * time.Millisecond} //nolint:exhaustruct_v5 // test fixture: only fields under test

	_, err = overrideDialer.DialContext(t.Context(), "tcp", overrideAddr)
	require.Error(t, err, "the overridden endpoint must stop accepting after the override is cleared")

	// The in-flight connection from before either rebind
	// keeps its deadline: neither the override nor the
	// clear tore the stream down.
	require.NoError(t, inFlight.SetReadDeadline(time.Now().Add(300*time.Millisecond)))

	_, err = inFlight.Read(make([]byte, 1))

	require.ErrorAs(t, err, &netErr)
	assert.True(t, netErr.Timeout(), "in-flight connection must keep its deadline across the override and the clear rebind")
}

// TestVLESSNodeOverrideConflictingPortFailsClosed pins the
// failure mode of a per-node VLESS endpoint override whose
// port collides with a live listener (here: another node's
// config-derived port). The apply must fail loudly — a 400
// whose error names the node and the conflicting port —
// instead of silently succeeding, and the conflicting port
// must stay owned by the node it belongs to: a connection
// presenting the overridden node's UUID to it is refused
// without the version ack, so a bad override can never
// make a node's handshake succeed on another node's
// listener.
func TestVLESSNodeOverrideConflictingPortFailsClosed(t *testing.T) {
	ts := NewServer(t, WithXRay("127.0.0.1", xrayTestPortStart, xrayTestPortEnd))

	user := ts.CreateUser(t, "vless-conflict")
	owner := ts.CreateRegisteredNode(t, user, "vless-conflict-owner")
	conflicting := ts.CreateRegisteredNode(t, user, "vless-conflict-node")
	ownerID := owner.ID()
	nodeID := conflicting.ID()

	ts.StartXRay(t)

	// The owner node's derived endpoint is live; it is the
	// port the conflicting override collides with.
	ownerPort := xray.NodePort(ownerID, "", xrayTestPortStart, xrayTestPortEnd)
	ownerAddr := net.JoinHostPort("127.0.0.1", strconv.Itoa(ownerPort))

	ownerConn := dialVLESS(t, ownerAddr, ownerID)
	require.NotNil(t, ownerConn, "the owner node's derived listener must be live before the conflicting apply")

	h := webui.Handler(&types.Config{ //nolint:exhaustruct_v5 // test fixture: only the XRay section is under test
		XRay: types.XRayConfig{ //nolint:exhaustruct_v5 // test fixture: only fields under test
			Enabled:        true,
			ListenAddr:     "127.0.0.1",
			BaseListenPort: xrayTestPortStart,
			MaxListenPort:  xrayTestPortEnd,
			Timeout:        30 * time.Second,
		},
	}, ts.State())

	ui := httptest.NewServer(h)

	defer ui.Close()

	// Apply an override that points the second node at the
	// first node's live port.
	body := fmt.Sprintf(`{"address":"127.0.0.1","port":%d,"security":"none"}`, ownerPort)

	applyReq, err := http.NewRequestWithContext(t.Context(), http.MethodPost, ui.URL+"/web/api/vless/"+nodeID.String(), strings.NewReader(body))
	require.NoError(t, err)

	applyReq.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(applyReq)
	require.NoError(t, err)

	defer resp.Body.Close()

	// The rebind onto an occupied port must fail the apply:
	// the operator is told which node and port collided.
	require.Equal(t, http.StatusBadRequest, resp.StatusCode, "an override colliding with a live listener must fail the apply")

	errBody, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Contains(t, string(errBody), "listening for node", "the 400 must carry the listener bind error")
	assert.Contains(t, string(errBody), strconv.Itoa(ownerPort), "the 400 must name the conflicting port")

	// The conflicting port stays owned by its node: a
	// connection presenting the second node's UUID to it is
	// refused without the version ack.
	conn, err := (&net.Dialer{Timeout: 10 * time.Second}).DialContext(t.Context(), "tcp", ownerAddr) //nolint:exhaustruct_v5 // test fixture: only the timeout is under test
	require.NoError(t, err)

	t.Cleanup(func() { conn.Close() })

	uuidBytes, err := uuid.Parse(xray.NodeUUID(nodeID, ""))
	require.NoError(t, err)

	header := make([]byte, 0, 18)
	header = append(header, 0)
	header = append(header, uuidBytes[:]...)
	header = append(header, 0)

	require.NoError(t, conn.SetDeadline(time.Now().Add(10*time.Second)))

	_, err = conn.Write(header)
	require.NoError(t, err)

	var ack [1]byte

	_, err = io.ReadFull(conn, ack[:])
	require.Error(t, err, "the conflicting node must not be acked on another node's listener")
}
