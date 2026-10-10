// Copyright (c) 2026 TomiWebPro <TomiWebPro@gmail.com>
// SPDX-License-Identifier: BSD-3-Clause

package servertest_test

import (
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tomiwebpro/stealthscale/hscontrol/servertest"
	"github.com/tomiwebpro/stealthscale/hscontrol/types/change"
	"tailscale.com/tailcfg"
	"tailscale.com/types/dnstype"
	"tailscale.com/types/netmap"
)

// dnsRecordNames returns the names of the MagicDNS extra
// records carried by a netmap's DNS config.
func dnsRecordNames(nm *netmap.NetworkMap) []string {
	if nm == nil {
		return nil
	}

	names := make([]string, 0, len(nm.DNS.ExtraRecords))
	for _, r := range nm.DNS.ExtraRecords {
		names = append(names, r.Name)
	}

	return names
}

// TestDNSExtraRecordsChangePropagatesDuringActivePoll is a
// failure-point test for a DNS failure mode: an operator edits
// the MagicDNS extra-records file while nodes hold active
// long-polls. The edit must reach their netmaps over the
// existing poll — otherwise clients keep resolving stale DNS
// records until they happen to reconnect.
//
// It exercises the exact production path (app.go
// scheduledTasks: ExtraRecordsMan.UpdateCh → cfg.SetExtraRecords
// → Change(change.ExtraRecords())) through the batcher's
// broadcast path into the connected client's incremental
// MapResponse, and pins that the resolver list itself is not
// clobbered by the update.
func TestDNSExtraRecordsChangePropagatesDuringActivePoll(t *testing.T) {
	t.Parallel()

	srv := servertest.NewServer(t)

	// Seed the DNS config before any client polls: one DoH
	// resolver and one extra record. Set pre-connect so the
	// write cannot race the mapper's per-build reads; later
	// edits go through the lock-guarded SetExtraRecords.
	//nolint:exhaustruct_v5 // partial literal: only the fields under test
	srv.Cfg.TailcfgDNSConfig = &tailcfg.DNSConfig{
		Resolvers: []*dnstype.Resolver{
			//nolint:exhaustruct_v5 // partial literal: only Addr matters
			{Addr: "https://dns.example.test/dns-query"},
		},
		ExtraRecords: []tailcfg.DNSRecord{
			//nolint:exhaustruct_v5 // partial literal: only Name/Value matter
			{Name: "old.dns.example.test", Value: "10.9.9.9"},
		},
	}

	user := srv.CreateUser(t, "dns-user")
	client := servertest.NewClient(t, srv, "dns-client", servertest.WithUser(user))

	// The initial (full) netmap must carry the DNS config and
	// the seeded extra record.
	client.WaitForCondition(t, "initial DNS config", 10*time.Second, func(nm *netmap.NetworkMap) bool {
		return nm != nil &&
			len(nm.DNS.Resolvers) == 1 &&
			nm.DNS.Resolvers[0].Addr == "https://dns.example.test/dns-query" &&
			slices.Contains(dnsRecordNames(nm), "old.dns.example.test")
	})

	updatesBefore := client.UpdateCount()

	// Operator edits the extra records — the same delivery
	// path the file watcher uses (app.go extraRecordsUpdate).
	//nolint:exhaustruct_v5 // partial literal: only Name/Value matter
	srv.Cfg.SetExtraRecords([]tailcfg.DNSRecord{
		{Name: "new.dns.example.test", Value: "10.9.9.10"},
	})
	srv.App.Change(change.ExtraRecords())

	// The connected client must receive the edit over its
	// active long-poll: the new record present, the stale one
	// gone, the resolver list untouched.
	client.WaitForCondition(t, "updated extra records", 10*time.Second, func(nm *netmap.NetworkMap) bool {
		return nm != nil &&
			len(nm.DNS.Resolvers) == 1 &&
			nm.DNS.Resolvers[0].Addr == "https://dns.example.test/dns-query" &&
			slices.Contains(dnsRecordNames(nm), "new.dns.example.test") &&
			!slices.Contains(dnsRecordNames(nm), "old.dns.example.test")
	})

	require.Greater(t, client.UpdateCount(), updatesBefore,
		"DNS change must arrive as a pushed update over the active long-poll, not via reconnect")
}
