---
title: "WebUI similar to headscale-ui built into server and client"
labels: ["feature", "webui"]
priority: high
created: 2026-08-27T00:00:00Z
---

Build a WebUI similar to https://github.com/gurucomputing/headscale-ui
- Embedded into StealthScale server (hscontrol) as static assets
- Also available in client section (no difference in code for both server and client)
- Should expose: nodes, users, preauthkeys, policy, DERP status, VLESS endpoints, machine api health
- Use Go embed + Go templates (or elem-go) like current assets
- Reuse scheduler's webui style as reference for dark theme.

Acceptance:
- `hscontrol/webui/` package with embedded frontend
- Served at /web or /admin
- API endpoints for management
