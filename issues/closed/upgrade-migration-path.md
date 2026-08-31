---
title: "Upgrade and migration path for existing Headscale deployments"
labels: ["feature", "docs"]
priority: medium
assignee: ""
created: 2026-08-28T00:00:00Z
---

**Goal:** Operators on plain Headscale/WireGuard or old `stealthscale` (self-signed Reality) can upgrade without re-registering all nodes.

**Context:** `hscontrol/types/config.go:277` `InitIdentity` now derives `PrivateKey`/`PublicKey`/`ShortId`/`ShortIDs` from `.xray_secret`; changing `xray.secret` or `xray.reality.dest` changes `NodeUUID`/`NodePort` (`hscontrol/xray/vless.go:152`) and breaks existing `vless://` URIs (see `cmd/stealthscale/cli/nodes.go:318` `ResolveXRayIdentity`). No migration doc exists.

**Tasks:**
- Document in `docs/stealthscale/install.md` and `CHANGELOG.md` the upgrade steps: (a) backup `db.sqlite` + `.xray_secret`, (b) keep `xray.secret`/`xray.reality.*` stable, (c) `stscale nodes vless <id>` to re-issue URIs if `server_url`/`dest` changes.
- Provide `stscale migrate xray-identity` (or similar) to re-derive or rotate `PrivateKey`/`ShortId` without changing `NodeUUID`/`NodePort` (or warn that rotation requires re-issuing).
- Test the path in `integration/` with an old `db.sqlite` volume.

**Acceptance:**
- `docs/stealthscale/install.md` has an "Upgrading from Headscale / old StealthScale" section.
- `CHANGELOG.md` entry for the Reality vendoring is marked as breaking if `xray.secret` changes.
