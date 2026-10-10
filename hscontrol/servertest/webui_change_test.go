// Copyright (c) 2026 TomiWebPro <TomiWebPro@gmail.com>
// SPDX-License-Identifier: BSD-3-Clause

package servertest

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"tailscale.com/tailcfg"
	"tailscale.com/types/netmap"

	"github.com/tomiwebpro/stealthscale/hscontrol/types"
	"github.com/tomiwebpro/stealthscale/hscontrol/webui"
)

// webUIWriteServer mounts the WebUI API against the
// harness's real *state.State with the harness's real
// change sink (h.Server.App.Change) — the production
// wiring from hscontrol/app.go:createRouter
// (webui.Register(r, h.cfg, h.state, h.Change)).
func webUIWriteServer(h *TestHarness) *httptest.Server {
	wh := webui.Handler(h.Server.Cfg, h.Server.State(), h.Server.App.Change)

	return httptest.NewServer(wh)
}

// webUIWrite issues a JSON request against the WebUI
// write API and returns the response status code.
func webUIWrite(tb testing.TB, ts *httptest.Server, method, path string, body any) int {
	tb.Helper()

	var rdr *bytes.Reader

	if body != nil {
		b, err := json.Marshal(body)
		require.NoError(tb, err)

		rdr = bytes.NewReader(b)
	} else {
		rdr = bytes.NewReader(nil)
	}

	req, err := http.NewRequestWithContext(
		context.Background(), method, ts.URL+path, rdr)
	require.NoError(tb, err)

	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(tb, err)

	defer resp.Body.Close()

	return resp.StatusCode
}

// quiesce blocks until no netmap update has arrived
// for the quiet window, so a later update can be
// attributed to the caller's action rather than to
// late convergence (endpoint and online patches
// still land after mesh completion).
func quiesce(tb testing.TB, c *TestClient, quiet time.Duration) {
	tb.Helper()

	for {
		select {
		case <-c.updates:
			// Drain a late convergence update and
			// restart the quiet window.
			continue
		case <-time.After(quiet):
			return
		}
	}
}

// TestWebUI_NodeDeleteChangeReachesConnectedClients
// pins the propagation promised by docs/usage/webui.md:
// a node deleted through DELETE /web/api/nodes/{id} is
// broadcast to connected clients on their live long-poll.
// The handler forwards the change state.State.DeleteNode
// returned to the change sink (the mapper batcher) — the
// same propagation the API v1/v2 and gRPC paths perform.
//
// Failure mode guarded: the write lands in the NodeStore
// and the database but the returned change is dropped (the
// write handlers used to discard it), so the deleted
// node's peers keep it in their netmaps and keep trying
// to reach it until they reconnect — the delete only
// surfaces by accident, when a poll session happens to
// be interrupted.
func TestWebUI_NodeDeleteChangeReachesConnectedClients(t *testing.T) {
	t.Parallel()

	h := NewHarness(t, 2)

	// Baseline: the two clients see each other as
	// peers before the delete.
	for _, tc := range h.Clients() {
		tc.WaitForPeers(t, 1, 10*time.Second)
	}

	victim := h.Client(1)
	victimID := types.NodeID(victim.Netmap().SelfNode.ID())

	ts := webUIWriteServer(h)
	defer ts.Close()

	code := webUIWrite(t, ts, http.MethodDelete,
		"/web/api/nodes/"+victimID.String(), nil)
	require.Equal(t, http.StatusOK, code)

	// The write is live in the control-plane state.
	_, ok := h.Server.State().GetNodeByID(victimID)
	require.False(t, ok, "the deleted node must be gone from state")

	// The surviving client drops the deleted peer on
	// its live long-poll — no reconnect, no poll
	// restart, no waiting for the next full map.
	survivor := h.Client(0)
	survivor.WaitForCondition(t, "deleted peer gone", 10*time.Second,
		func(nm *netmap.NetworkMap) bool {
			for _, p := range nm.Peers {
				hi := p.Hostinfo()
				if hi.Valid() && hi.Hostname() == victim.Name {
					return false
				}
			}

			return true
		})

	assert.Empty(t, survivor.Peers(),
		"no peers remain after the only peer is deleted")
}

// TestWebUI_NodeLifecycleWritesReachConnectedClients
// pins the same propagation for the node lifecycle
// writes (rename, expiry, approved routes): the change
// the state method returns reaches the surviving
// client's netmap on its live long-poll, like the
// identical CLI/API operations.
func TestWebUI_NodeLifecycleWritesReachConnectedClients(t *testing.T) {
	t.Parallel()

	h := NewHarness(t, 2)

	observer := h.Client(0)
	target := h.Client(1)

	for _, tc := range h.Clients() {
		tc.WaitForPeers(t, 1, 10*time.Second)
	}

	targetID := types.NodeID(target.Netmap().SelfNode.ID())
	targetPath := "/web/api/nodes/" + targetID.String()

	ts := webUIWriteServer(h)
	defer ts.Close()

	// Rename: the surviving client's netmap shows the
	// new display name without a reconnect.
	code := webUIWrite(t, ts, http.MethodPost, targetPath+"/rename",
		map[string]any{"name": "renamed-webui-node"})
	require.Equal(t, http.StatusOK, code)

	observer.WaitForCondition(t, "renamed peer visible", 10*time.Second,
		func(nm *netmap.NetworkMap) bool {
			for _, p := range nm.Peers {
				if p.Name() == "renamed-webui-node" {
					return true
				}
			}

			return false
		})

	// Expiry: the surviving client's netmap carries the
	// new key expiry.
	expiry := time.Now().Add(time.Hour).UTC().Truncate(time.Second)

	code = webUIWrite(t, ts, http.MethodPost, targetPath+"/expire",
		map[string]any{"expiry": expiry.Format(time.RFC3339)})
	require.Equal(t, http.StatusOK, code)

	observer.WaitForCondition(t, "expiry propagated to peer", 10*time.Second,
		func(nm *netmap.NetworkMap) bool {
			for _, p := range nm.Peers {
				if p.ID() == targetID.NodeID() {
					return p.KeyExpiry().Equal(expiry)
				}
			}

			return false
		})

	// Approved routes: the target first advertises the
	// route (an approval gates advertised routes), then
	// the WebUI approval reaches the observer's netmap.
	route := netip.MustParsePrefix("10.10.10.0/24")

	target.Direct().SetHostinfo(&tailcfg.Hostinfo{
		BackendLogID: "servertest-webui-routes",
		Hostname:     target.Name,
		RoutableIPs:  []netip.Prefix{route},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_ = target.Direct().SendUpdate(ctx)

	observer.WaitForCondition(t, "advertised route in hostinfo", 10*time.Second,
		func(nm *netmap.NetworkMap) bool {
			for _, p := range nm.Peers {
				hi := p.Hostinfo()
				if hi.Valid() && hi.Hostname() == target.Name {
					return hi.RoutableIPs().Len() > 0
				}
			}

			return false
		})

	code = webUIWrite(t, ts, http.MethodPost, targetPath+"/routes",
		map[string]any{"routes": []string{route.String()}})
	require.Equal(t, http.StatusOK, code)

	observer.WaitForCondition(t, "approved route in AllowedIPs", 15*time.Second,
		func(nm *netmap.NetworkMap) bool {
			for _, p := range nm.Peers {
				if p.ID() != targetID.NodeID() {
					continue
				}

				for i := range p.AllowedIPs().Len() {
					if p.AllowedIPs().At(i) == route {
						return true
					}
				}
			}

			return false
		})
}

// TestWebUI_UserCreateChangeReachesConnectedClients
// pins the propagation for user creation: the change
// state.State.CreateUser returned reaches connected
// clients as a netmap update on their live long-poll.
func TestWebUI_UserCreateChangeReachesConnectedClients(t *testing.T) {
	t.Parallel()

	h := NewHarness(t, 2)

	observer := h.Client(0)

	for _, tc := range h.Clients() {
		tc.WaitForPeers(t, 1, 10*time.Second)
	}

	// Let late convergence settle so the
	// post-create update is unambiguous.
	quiesce(t, observer, 5*time.Second)

	before := observer.UpdateCount()

	ts := webUIWriteServer(h)
	defer ts.Close()

	code := webUIWrite(t, ts, http.MethodPost, "/web/api/users",
		map[string]any{
			"name":  "webui-created-user",
			"email": "webui-created@example.com",
		})
	require.Equal(t, http.StatusCreated, code)

	// The created user is persisted.
	users, err := h.Server.State().ListAllUsers()
	require.NoError(t, err)

	found := false

	for _, u := range users {
		if u.Name == "webui-created-user" {
			found = true
		}
	}

	require.True(t, found, "the created user must be persisted")

	// ... and the change it produced reaches the
	// connected client as a netmap update.
	observer.WaitForCondition(t, "netmap update after user create", 10*time.Second,
		func(*netmap.NetworkMap) bool {
			return observer.UpdateCount() > before
		})
}
