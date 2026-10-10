// Copyright (c) 2026 TomiWebPro <TomiWebPro@gmail.com>
// SPDX-License-Identifier: BSD-3-Clause

package server

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"tailscale.com/tailcfg"
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
