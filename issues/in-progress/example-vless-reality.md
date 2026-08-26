---
title: "Default to VLESS+Reality_XTLS, DERP fallback only if stealth satisfied"
labels: ["feature", "stealth", "vless"]
priority: high
created: 2026-08-27T00:00:00Z
---

Implement server+client unified transport:
- Single codebase for both server and client sections
- Default security = `reality_xtls` (VLESS+Reality)
- DERP fallback must only activate after stealth check passes
- If stealth fails, do not fall back to plain DERP — fail closed.

Acceptance:
- config-example.yaml defaults to xray.security=reality_xtls
- types/config.go handles reality_xtls
- xray/server.go supports utls + reality
- derp/ routing checks stealth before fallback
