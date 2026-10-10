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

## 2026-10-09 23:49 UTC — stealth-builder

- concurrency guard passed (lock ~19 min old); no open issues — cycle aborted (queue empty)

## 2026-10-10 01:45 UTC — ss-maintain

- build: `go build ./...` green with GOTOOLCHAIN=go1.26.7 (Go 1.27 toolchain has unrelated issues)
- tests: ran `go test -short ./hscontrol/... ./cmd/...` with pinned toolchain; initial run had stale cache causing webui build failures, clean cache resolved this
- fixed: cli/nodes.go — added missing `--address` flag to `nodes vless` subcommand (warning text referenced a non-existent flag), added flag + code path to override `xray.listen_addr`; all CLI and hscontrol tests now pass
- added: hscontrol/servertest/expiry_test.go — 2 fast unit tests covering node expiry persistence in NodeStore/DB (`TestNodeExpiryPersisted`, `TestNodeExpiryFutureDoesNotMarkExpired`)
- bug hunting: found that `SetNodeExpiry` doesn't force immediate MapResponse refresh with Expired flag — client netmaps stay active despite past expiry in store; filed as issue
- issues filed: `202610100145-vless-uri-copy-paste.md` (P2), `202610100145-node-expiry-not-in-mapresponse.md` (P2)
- issues closed: none
- commit: `54bf7f8`

## 2026-10-10 02:00 UTC — stealth-builder

- picked up P2 issue `202610100145-vless-uri-copy-paste` (CLI: vless URI manual copy-paste between coordinator and client)
- added `--one-line` flag to `stscale nodes vless <id>` — prints only the raw `vless://` URI (no table headers)
- updated error message in `stscale up` when `--vless-uri` missing — now shows one-command shell subshell example
- updated `up --help` Long description with both manual paste and one-command join patterns
- updated `docs/stealthscale/clients.md` — documented `--one-line`, added subshell pattern
- updated `CHANGELOG.md` with unreleased entry
- build: `go build ./...` green; tests: `go test -short ./cmd/stealthscale/cli/...` ok, `go test -short ./hscontrol/...` ok
- moved issue to closed/
