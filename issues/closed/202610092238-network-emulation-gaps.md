---
title: Network emulation harness lacks latency/loss/partition primitives
labels: feature
priority: P2 (minor)
found-by: ss-maintain
filed: 2026-10-09 22:38 UTC
promised-ref: docs/stealthscale/overview.md — "Project goals" (deployable stealth transport: VLESS+Reality must behave under adverse networks; the harness in integration/dockertestutil/network.go is the existing mechanism for expressing network conditions)
---

## What is wrong

The existing emulation harness (`integration/dockertestutil/network.go`,
480 lines) only expresses binary connectivity: connect/disconnect/reconnect
containers and wait for routes to appear/vanish. It cannot express the
degraded-network conditions a stealth transport must survive:

- latency / jitter injection
- packet loss / duplication / corruption
- partitions (split-brain between subsets, not just isolate-one-container)
- slow or flaky DERP (bandwidth caps, intermittent relay)

Evidence (2026-10-09, `main@9f9348d`): `grep -n "func "`
lists `GetFirstOrCreateNetwork{,WithSubnet}`, `AddContainerToNetwork`,
`DisconnectContainerFromNetwork`, `ReconnectContainerToNetwork`,
`DisconnectAndReconnect`, route waiters, `RandomFreeHostPort`,
`CleanUnreferencedNetworks` — no `tc`/`netem`-based shaping, no partition
primitive, no DERP-degradation helper.

## Expected behaviour

Extend `integration/dockertestutil/network.go` (and
`Dockerfile.integration` if `tc`/`iptables` tooling is missing in test
images) so integration tests can run scenarios under latency, loss,
partitions, and slow/flaky DERP — then exercise the VLESS/Reality and
DERP fail-closed paths under those conditions.

## Files/packages involved

- `integration/dockertestutil/network.go`
- `Dockerfile.integration` (test-image tooling)
- `integration/` scenarios; `hscontrol/stealth/`, `hscontrol/derp/` (gating under test)

<!-- status: done -->
- implementation: created `integration/dockertestutil/network_emulation.go` with
  latency/jitter/loss/duplication/corruption primitives via `tc netem`, plus
  `Partition()` for route-level split-brain and `FlakyDERP()` as a constructor
  helper. Unit tests cover interface detection (`findContainerInterface`) and
  netem argument construction (`buildNetemFilter`). Dockerfile integration needs
  no changes — `tc` is available in the Debian trixie-slim base (from `iproute2`).
  Note: existing Dockerfile.integration already installs `iproute2` (line 30).
