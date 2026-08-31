---
title: "Persist xray.secret for postgres deployments"
labels: ["bug", "vless"]
priority: high
assignee: ""
created: 2026-08-28T00:00:00Z
---

**Goal:** `xray.secret` / Reality keypair / `ShortId` must be stable across restarts even when `database.type: postgres` (no local `db.sqlite` dir).

**Context:** `hscontrol/types/config.go:277` `InitIdentity(stateDir)` persists `xray.secret` to `filepath.Dir(db.sqlite)/.xray_secret` (`loadOrCreateSecret:332`). For `postgres` `stateDir==""` it generates an ephemeral 32-byte hex each restart (`loadOrCreateSecret:333`), changing `NodeUUID`/`NodePort` (`hscontrol/xray/vless.go:152`) and `Reality` `PrivateKey`/`ShortId`, breaking existing `vless://` URIs (see `cmd/stealthscale/cli/nodes.go:318` `ResolveXRayIdentity`).

**Tasks:**
- Persist `xray.secret` for `postgres`: (a) require `xray.secret` in `config.yaml` when `database.type: postgres` (fail fast in `validateServerConfig:770`), or (b) store it in the DB (`db` migration, new table `xray_secrets`) and load on startup.
- Document in `config-example.yaml:91` and `docs/ref/configuration.md` that `xray.secret` is mandatory for postgres/HA.
- Add test that two successive `LoadServerConfig` with same `postgres` DSN and no `xray.secret` either fails or returns the same `PublicKey`/`ShortId` (depending on chosen fix).

**Acceptance:**
- Restarting a `postgres`-backed coordinator does not change `stscale nodes vless <id>` output.
- `go test ./hscontrol/types -run TestInitIdentity` passes for both `sqlite` and `postgres` paths.
