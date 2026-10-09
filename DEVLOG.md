# DEVLOG

Append-only log of automated agent work on this repo. One entry per agent
run, **newest last**. Never edit or reorder older entries — append only.

Format:

```
## <YYYY-MM-DD HH:MM UTC> — <job name>
- what was done / found / committed (with short hashes) / issues filed or closed
```

## 2026-10-10 00:50 UTC — ss-worker

- picked up P0 issue `202610082015-go127-json-format-tag-sshaction` (Go 1.27
  encoding/json rejects `format:nano` tag in `tailcfg.SSHAction`)
- applied Route A: pinned `GOTOOLCHAIN=go1.26.7` in Makefile build/test targets
- refactored `SSHActionHandler` (noise.go) to marshal JSON into a buffer before
  writing HTTP headers — now returns 500 on encode failure instead of 200 empty body
- moved issue to closed/
- commit: `0f4a8fe`
- remaining open issues: `202610092238-network-emulation-gaps.md` (P2),
  `202610092238-webui-coordinator-election.md` (P2)

## 2026-10-10 01:10 UTC — ss-worker

- picked up P2 issue `202610092238-network-emulation-gaps`
- created `integration/dockertestutil/network_emulation.go`:
  - `NetworkEmulation` struct + `Apply()`/`ResetAll()` for tc/netem rules
    (latency, jitter, drop, duplication, corruption)
  - `Partition(pool, network, targets, timeout)` for route-level split-brain
  - `FlakyDERP(pool, network, container, latency, lossPct)` constructor helper
  - `findContainerInterface()` parses `ip -4 addr show` output, handles veth
    notation (`eth0@if74`) and bare containers
  - `buildNetemFilter()` generates tc netem argument strings from
    `NetworkConditions`
- added unit tests in `network_emulation_test.go` covering interface detection
  across Docker/Kernel formats and all netem filter combinations
- no changes needed to `Dockerfile.integration` — `tc` is already available
  via the `iproute2` package installed on line 30
- moved issue to closed/
