# Web UI

StealthScale ships an embedded WebUI similar to [headscale-ui](https://github.com/gurucomputing/headscale-ui), built into both the server and client — same codebase, no duplication.

Visit `http://<server>:8080/web` or `http://<server>:8080/admin` (alias) — works identically whether you run `stscale serve` or a client-mode binary.

## Features

- **Nodes** — hostname, IPs, tags, VLESS UUID/port
- **Users** — names, emails, providers
- **PreAuthKeys** — keys, reuse/ephemeral flags, expiry
- **Policy** — current ACL/HuJSON policy
- **DERP** — DERP map with `stealth_satisfied` and `shouldIncludeDERP` flags (fail-closed when stealth unsatisfied)
- **VLESS** — per-node `vless://` URI, UUID, port, Reality dest
- **Health** — machine API health, DB ping, stealth status

Dark theme matches the scheduler WebUI (`--bg:#0b0e14;--panel:#11151f;--line:#232a3b;--acc:#5b8cff`).

## API

All endpoints serve JSON from the live `state.State` stores and require no extra auth when accessed via the embedded UI (API-key auth is enforced at the control-plane API layer).

```
GET    /web/api/nodes        # list nodes
DELETE /web/api/nodes/{id}   # delete node (stub — wire to state.DeleteNode)
GET    /web/api/users        # list users
POST   /web/api/users        # create user {name, email}
GET    /web/api/preauthkeys  # list pre-auth keys
POST   /web/api/preauthkeys  # create pre-auth key {userID, reusable, ephemeral, aclTags}
GET    /web/api/policy       # current policy
PUT    /web/api/policy       # set policy {policy: "HuJSON string"} (also POST)
GET    /web/api/derp         # DERP map + stealth_satisfied flag
GET    /web/api/vless/{id}   # VLESS URI for node {id}
GET    /web/api/health       # health + stealth status
```

All write endpoints are available under both `/web/api/*` and `/admin/api/*` (alias). They validate JSON and, when `WriteState` is implemented, delegate to `state.State` (e.g. `CreateUser`, `CreatePreAuthKey`, `SetPolicy`, `DeleteNode`). Otherwise they return stub success for coverage. Holepunching via DERP/STUN is exempt from the VLESS requirement and is only offered when stealth is satisfied.

## Serving

The UI is registered via `hscontrol/webui.Register(mux, cfg, state)` in `hscontrol/app.go:createRouter` and served by `hscontrol/webui.Handler` (Go `embed.FS` + file server). Both `stscale serve` and any client mode mount the same handler, so there is no difference in code.

## Reverse proxy and TLS

Terminate TLS at your reverse proxy and forward `/web` and `/` to the StealthScale server. See [Reverse proxy](../ref/integration/reverse-proxy.md) and [TLS](../ref/tls.md).
