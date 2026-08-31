---
title: "Docs: Reality install, config and client-modification for dual decoys"
labels: ["docs"]
priority: medium
assignee: ""
created: 2026-08-28T00:00:00Z
---

**Goal:** Docs must reflect true Reality (not "uTLS-shaped decoy cert") and the dual-decoy setup.

**Context:** `config-example.yaml:129` now defaults to `dest: www.cloudflare.com:443`, `server_names: [www.cloudflare.com,www.microsoft.com,cloudflare.com,microsoft.com]`, `short_ids: []`. `hscontrol/types/config.go:214` and `hscontrol/xray/server.go:140` implement it, but `docs/ref/xray-vless.md`, `docs/stealthscale/overview.md:4`, `docs/stealthscale/install.md`, `docs/stealthscale/clients.md`, and `docs/client-modification.md` still describe the old `www.microsoft.com:443` single-decoy or "Reality-style decoy certificate".

**Tasks:**
- Update `docs/ref/xray-vless.md` to describe `xtls/reality` cert stealing (MPL-2.0, `steal dest handshake`) vs old self-signed, and the `server_names`/`short_ids` dual list.
- Update `docs/stealthscale/overview.md` and `README.md:4` ("VLESS + uTLS-shaped TLS with a decoy certificate (Reality-style)") to "VLESS+Reality (xtls/reality) + uTLS".
- Update `docs/stealthscale/install.md` with `xray.reality.dest`/`server_names`/`short_ids`/`spider_x` and the `xray.secret` persistence note for `postgres`.
- Update `docs/stealthscale/clients.md` + `docs/client-modification.md` with `vless://` URI fields `dest`/`pbk`/`sid`/`spx`/`fp` and `fp` mapping (`hscontrol/xray/client.go:212`).

**Acceptance:**
- `make lint` (`markdownlint`) passes; `mkdocs serve` renders the new Reality section without TODOs.
