// Copyright (c) 2026 TomiWebPro <TomiWebPro@gmail.com>
// SPDX-License-Identifier: BSD-3-Clause

package server

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tomiwebpro/stealthscale/hscontrol/types"
	"tailscale.com/tailcfg"
	"tailscale.com/types/key"
)

// TestDERPBootstrapDNSHandlerSkipsUnresolvableNodes pins the DNS
// failure mode of the bootstrap-DNS endpoint: a DERP node whose
// hostname fails to resolve (NXDOMAIN, offline resolver, lookup
// timeout) is omitted from the response while the resolvable
// nodes are still served, and the handler still answers 200 —
// one broken hostname must not take the whole bootstrap map
// down (the handler is mounted at /derpmap/bootstrap-dns from
// app.go, so its behaviour is a live failure surface).
//
// "localhost." resolves from /etc/hosts, so the happy path is
// deterministic even with no network. The ".invalid" TLD is
// reserved by RFC 6761 and never resolves, so the failure path
// is deterministic too.
func TestDERPBootstrapDNSHandlerSkipsUnresolvableNodes(t *testing.T) {
	t.Parallel()

	derpMap := &tailcfg.DERPMap{ //nolint:exhaustruct_v5 // test fixture: only Regions matters
		Regions: map[int]*tailcfg.DERPRegion{
			1: { //nolint:exhaustruct_v5 // test fixture: only RegionID/Nodes matter
				RegionID:   1,
				RegionCode: "lcl",
				RegionName: "Local",
				Nodes: []*tailcfg.DERPNode{
					{Name: "1a", RegionID: 1, HostName: "localhost."}, //nolint:exhaustruct_v5 // test fixture: only Name/HostName matter
				},
			},
			2: { //nolint:exhaustruct_v5 // test fixture: only RegionID/Nodes matter
				RegionID:   2,
				RegionCode: "bad",
				RegionName: "Unresolvable",
				Nodes: []*tailcfg.DERPNode{
					{Name: "2a", RegionID: 2, HostName: "no-such-host.invalid."}, //nolint:exhaustruct_v5 // test fixture: only Name/HostName matter
				},
			},
		},
	}

	handler := DERPBootstrapDNSHandler(derpMap.View())

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/derpmap/bootstrap-dns", nil)
	handler(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var entries map[string][]net.IP
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&entries))

	require.Contains(t, entries, "localhost.")
	require.NotEmpty(t, entries["localhost."], "resolvable node must be served")
	require.NotContains(
		t,
		entries,
		"no-such-host.invalid.",
		"unresolvable node must be skipped, not served",
	)
}

// TestDERPBootstrapDNSHandlerAllUnresolvableStillServes pins the
// total-DNS-outage failure mode: when every DERP hostname fails
// to resolve, the endpoint still answers 200 with an empty map
// (clients fall back) rather than failing the request — the
// lookup error is logged and skipped per node, never propagated.
func TestDERPBootstrapDNSHandlerAllUnresolvableStillServes(t *testing.T) {
	t.Parallel()

	derpMap := &tailcfg.DERPMap{ //nolint:exhaustruct_v5 // test fixture: only Regions matters
		Regions: map[int]*tailcfg.DERPRegion{
			900: { //nolint:exhaustruct_v5 // test fixture: only RegionID/Nodes matter
				RegionID:   900,
				RegionCode: "bad",
				RegionName: "Unresolvable",
				Nodes: []*tailcfg.DERPNode{
					{Name: "9a", RegionID: 900, HostName: "a.invalid."}, //nolint:exhaustruct_v5 // test fixture: only Name/HostName matter
					{Name: "9b", RegionID: 900, HostName: "b.invalid."}, //nolint:exhaustruct_v5 // test fixture: only Name/HostName matter
				},
			},
		},
	}

	handler := DERPBootstrapDNSHandler(derpMap.View())

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/derpmap/bootstrap-dns", nil)
	handler(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var entries map[string][]net.IP
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&entries))

	require.Empty(t, entries, "total DNS outage must yield an empty map, not a 5xx")
}

// TestGenerateRegion pins the embedded DERP region generation:
// the region every served DERP map carries is built here from
// the operator's derp config, and GenerateRegion is the one
// place a malformed value silently drops the embedded region
// from the map (appendEmbeddedDERPRegion logs the error and
// returns). The port defaults (443/80), the explicit-port and
// IPv6-literal forms, and every fail-closed error path are
// pinned: an unparseable server URL, an out-of-range port
// (which survives url.Parse but fails strconv.Atoi), and a
// malformed STUN address.
func TestGenerateRegion(t *testing.T) {
	t.Parallel()

	const (
		regionID   = 900
		regionCode = "lcl"
		regionName = "Local"
	)

	newServer := func(t *testing.T, serverURL, stunAddr string) *DERPServer {
		t.Helper()

		d, err := NewDERPServer(
			serverURL,
			key.NewNode(),
			&types.DERPConfig{ //nolint:exhaustruct_v5 // test fixture: only the region fields matter
				ServerRegionID:   regionID,
				ServerRegionCode: regionCode,
				ServerRegionName: regionName,
				IPv4:             "10.0.0.1",
				IPv6:             "fd7a:115c:a1e0::1",
				STUNAddr:         stunAddr,
			},
		)
		require.NoError(t, err)

		return d
	}

	t.Run("explicit port and STUN port", func(t *testing.T) {
		t.Parallel()

		region, err := newServer(
			t,
			"https://derp.example.com:8443",
			"0.0.0.0:3478",
		).GenerateRegion()
		require.NoError(t, err)

		require.Equal(t, regionID, region.RegionID)
		require.Equal(t, regionCode, region.RegionCode)
		require.Equal(t, regionName, region.RegionName)

		require.Len(t, region.Nodes, 1)

		node := region.Nodes[0]
		require.Equal(t, strconv.Itoa(regionID), node.Name)
		require.Equal(t, regionID, node.RegionID)
		require.Equal(t, "derp.example.com", node.HostName)
		require.Equal(t, 8443, node.DERPPort)
		require.Equal(t, 3478, node.STUNPort)
		require.Equal(t, "10.0.0.1", node.IPv4)
		require.Equal(t, "fd7a:115c:a1e0::1", node.IPv6)
	})

	t.Run("https without port defaults to 443", func(t *testing.T) {
		t.Parallel()

		region, err := newServer(
			t,
			"https://derp.example.com",
			"0.0.0.0:3478",
		).GenerateRegion()
		require.NoError(t, err)

		require.Equal(t, 443, region.Nodes[0].DERPPort)
	})

	t.Run("http without port defaults to 80", func(t *testing.T) {
		t.Parallel()

		region, err := newServer(
			t,
			"http://derp.example.com",
			"0.0.0.0:3478",
		).GenerateRegion()
		require.NoError(t, err)

		require.Equal(t, 80, region.Nodes[0].DERPPort)
	})

	t.Run("ipv6 literal host keeps its port", func(t *testing.T) {
		t.Parallel()

		region, err := newServer(
			t,
			"https://[::1]:8443",
			"0.0.0.0:3478",
		).GenerateRegion()
		require.NoError(t, err)

		require.Equal(t, "::1", region.Nodes[0].HostName)
		require.Equal(t, 8443, region.Nodes[0].DERPPort)
	})

	t.Run("out-of-range port fails closed", func(t *testing.T) {
		t.Parallel()

		// url.Parse accepts any all-digit port, so only
		// strconv.Atoi catches a value outside int range —
		// the region must not be served with a garbage port.
		_, err := newServer(
			t,
			"https://derp.example.com:99999999999999999999",
			"0.0.0.0:3478",
		).GenerateRegion()
		require.Error(t, err)
	})

	t.Run("unparseable server URL fails closed", func(t *testing.T) {
		t.Parallel()

		_, err := newServer(t, "://missing", "0.0.0.0:3478").GenerateRegion()
		require.Error(t, err)
	})

	t.Run("STUN address without port fails closed", func(t *testing.T) {
		t.Parallel()

		_, err := newServer(t, "https://derp.example.com:443", "0.0.0.0").GenerateRegion()
		require.Error(t, err)
	})

	t.Run("STUN address with non-numeric port fails closed", func(t *testing.T) {
		t.Parallel()

		_, err := newServer(t, "https://derp.example.com:443", "0.0.0.0:stun").GenerateRegion()
		require.Error(t, err)
	})
}

// TestDERPProbeHandlerMethods pins the latency-probe endpoint's
// method gate and its deliberate CORS posture: js/wasm clients
// hit it to measure DERP latency, so GET and HEAD must answer
// 200 while any other method is rejected with 405 and the
// explanatory body — and no Access-Control-Allow-Origin header
// is ever set (the wildcard was removed to close a cross-origin
// latency side channel, so a regression that re-adds it must
// not go unnoticed).
func TestDERPProbeHandlerMethods(t *testing.T) {
	t.Parallel()

	for _, method := range []string{http.MethodGet, http.MethodHead} {
		t.Run(method, func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()
			req := httptest.NewRequestWithContext(t.Context(), method, "/derp-probe", nil)

			DERPProbeHandler(rec, req)

			require.Equal(t, http.StatusOK, rec.Code)
			require.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"))
		})
	}

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()
			req := httptest.NewRequestWithContext(t.Context(), method, "/derp-probe", nil)

			DERPProbeHandler(rec, req)

			require.Equal(t, http.StatusMethodNotAllowed, rec.Code)
			require.Equal(t, "bogus probe method", rec.Body.String())
			require.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"))
		})
	}
}

// TestDERPHandlerRejectsNonDERPUpgrade pins the DERP endpoint's
// upgrade gate: /derp speaks the DERP protocol over a websocket
// or raw-TCP upgrade, so a request with no Upgrade header (a
// plain HTTP GET) or a foreign upgrade value must be rejected
// with 426 and the explanatory body — never silently accepted.
func TestDERPHandlerRejectsNonDERPUpgrade(t *testing.T) {
	t.Parallel()

	d, err := NewDERPServer(
		"https://derp.example.com:443",
		key.NewNode(),
		&types.DERPConfig{ //nolint:exhaustruct_v5 // test fixture: only STUNAddr is read
			STUNAddr: "0.0.0.0:3478",
		},
	)
	require.NoError(t, err)

	for _, upgrade := range []string{"", "h2c", "websocket-beta"} {
		t.Run("upgrade="+strconv.Quote(upgrade), func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/derp", nil)
			req.Header.Set("Upgrade", upgrade)

			d.DERPHandler(rec, req)

			require.Equal(t, http.StatusUpgradeRequired, rec.Code)
			require.Equal(t, "DERP requires connection upgrade", rec.Body.String())
		})
	}
}

// errVerifyRejected is the static error the failing verify
// handler returns (err113: no dynamic errors in tests).
var errVerifyRejected = errors.New("verify rejected")

// TestDERPVerifyTransportRoundTrip pins the client-verify
// transport's failure mode: when ServerVerifyClients is on,
// every DERP client connection is verified through this
// transport, so a failing verify handler must propagate the
// error (fail-closed — the connection is not verified) rather
// than fabricate a 200, and a succeeding handler's body must
// become the response body.
func TestDERPVerifyTransportRoundTrip(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodGet,
		DerpVerifyScheme+"://verify",
		nil,
	)

	t.Run("handler body becomes the response", func(t *testing.T) {
		t.Parallel()

		transport := NewDERPVerifyTransport(func(_ *http.Request, w io.Writer) error {
			_, err := w.Write([]byte(`{"ok":true}`))

			return err
		})

		resp, err := transport.RoundTrip(req)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, resp.StatusCode)

		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.JSONEq(t, `{"ok":true}`, string(body))
	})

	t.Run("handler error fails closed", func(t *testing.T) {
		t.Parallel()

		transport := NewDERPVerifyTransport(func(_ *http.Request, _ io.Writer) error {
			return errVerifyRejected
		})

		resp, err := transport.RoundTrip(req)
		if resp != nil {
			defer resp.Body.Close()
		}

		require.ErrorIs(t, err, errVerifyRejected)
		require.Nil(t, resp)
	})
}
