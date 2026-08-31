---
title: "Harden WebUI exposure and auth"
labels: ["feature", "webui", "stealth"]
priority: medium
assignee: ""
created: 2026-08-28T00:00:00Z
---

**Goal:** WebUI (`/web`, `/admin`, `hscontrol/webui/`) must not be a product-level fingerprint or an unauthenticated entry.

**Context:** `hscontrol/webui/webui.go:213` and `hscontrol/assets/` were scrubbed of `StealthScale` branding (`hscontrol/templates/general.go:12`), but `/web` is still an app-shell on `listen_addr` and `metrics_listen_addr`. No auth gate for the embedded UI beyond the existing `api` key; `hscontrol/app.go:247` `securityHeaders` skips control paths.

**Tasks:**
- Decide and document `webui` exposure: (a) bind to `metrics_listen_addr` only, (b) require `api` key/OIDC, or (c) gate behind `enforce_control` (only over VLESS).
- Add auth check for `/web`/`/admin` (reuse `hscontrol/api` or `oidc.go:12` `register_confirm` flow) and remove anonymous enumeration.
- Update `docs/usage/webui.md` and `config-example.yaml` with the chosen `webui.listen_addr`/`enabled` flag.

**Acceptance:**
- `curl http://<listen_addr>/web` without auth returns `401`/`403` when hardening is enabled.
- `packaging/systemd/stealthscale.service` and `docs/stealthscale/install.md` reflect the new binding.
