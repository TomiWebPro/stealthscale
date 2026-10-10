# DEVLOG

Append-only log of automated agent work on this repo. One entry per agent
run, **newest last**. Never edit or reorder older entries — append only.

Format:

```
## <YYYY-MM-DD HH:MM UTC> — <job name>
- what was done / found / committed (with short hashes) / issues filed or closed
```

## 2026-10-10 02:30 UTC — stealth-builder

- picked up P2 issue `202610100145-node-expiry-not-in-mapresponse` (bug: SetNodeExpiry not forcing immediate MapResponse with Expired flag)
- investigated by tracing code path through State.SetNodeExpiry → updatePolicyManagerNodes → batcher.AddWork → mapper.generateMapResponse
- verified behavior with focused tests: TestNodeExpirySelfSeesExpired (single node self-expiry), TestNodeExpirySelfAndPeerSimultaneously (all three clients converge simultaneously)
- confirmed: the current implementation IS correct — PeerChange from SetNodeExpiry sets OriginNode, so the origin node receives a selfMapResponse that includes WithSelfNode() which evaluates IsExpired() from the updated NodeStore snapshot; peers see the expired flag in PeersChanged since listPeers reads from the same updated NodeStore
- added regression tests: TestNodeExpirySelfSeesExpired and TestNodeExpirySelfAndPeerSimultaneously to hscontrol/servertest/expiry_test.go (consolidated into single test file)
- no code changes needed — issue resolved without modifications
- moved issue to closed/

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

## 2026-10-10 00:10 UTC — ss-maintain

- concurrency guard passed (no lock held)
- build: `go build ./...` green with GOTOOLCHAIN=go1.26.7
- tests: ran `go test -short ./hscontrol/... ./cmd/...` — all pass
- added: hscontrol/servertest/expiry_test.go — `TestNodeExpiryPropagatesToPeers`: failure-point test verifying that when a node is expired via SetNodeExpiry, connected peers see it as expired in their netmaps, and reactivates after clearing expiry. Uses servertest harness (3 clients), runs in ~7s. Complements existing expiry persistence tests by covering end-to-end MapResponse propagation.
- bug hunting: reviewed auth_test.go BUG markers — both `TestGitHubIssue2830_NodeRestartWithUsedPreAuthKey` and `TestDeletedPreAuthKeyNotRecreatedOnNodeUpdate` now PASS (bugs were previously fixed but stale comments remain). No new trivial fixes identified.
- issues filed: none (existing open issues still pending: node-expiry-not-in-mapresponse P2, vless-uri-copy-paste P2)
- issues closed: none
- commit: `cacd51e`
