# Web UI

StealthScale ships an embedded WebUI similar to [headscale-ui](https://github.com/gurucomputing/headscale-ui), built into both the server and client — same codebase, no duplication.

Visit `http://<server>:8080/web` or `http://<server>:8080/admin` (alias) — works identically whether you run `stscale serve` or a client-mode binary.

## Features

- **Nodes** — hostname, IPs, tags, VLESS UUID/port; per-node actions: expire (set an RFC3339 timestamp or `never`), rename, tag, and approve advertised routes — each write is broadcast to connected clients immediately
- **Users** — names, emails, providers; create users
- **PreAuthKeys** — keys, reuse/ephemeral flags, expiry; create, expire and delete keys
- **API Keys** — list (masked prefixes only), create (raw key shown once), expire and delete
- **Auth** — inspect pending authentication requests by auth ID; approve and reject SSH-check sessions (registration sessions are completed via the node's auth URL, not by approve/reject)
- **Policy** — current ACL/HuJSON policy; validate a draft policy against the live users and nodes without applying it
- **DERP** — DERP map with `stealth_satisfied`, `shouldIncludeDERP` and `runtime` flags (fail-closed when stealth unsatisfied); the map can be edited and applied at runtime (`POST /web/api/derp`) — no `derp.yaml` edit or restart needed; an applied map is broadcast to connected clients immediately and takes precedence over the periodic auto-update until the server restarts
- **VLESS** — per-node `vless://` URI, UUID, port, Reality dest; endpoint settings (address, port, security variant) can be applied from the UI at runtime (`POST /web/api/vless/{id}`) — the node's VLESS listener rebinds to the overridden endpoint (in-flight connections keep their deadline); an applied override can be cleared from the UI (`POST {"clear": true}`) — the listener rebinds back to the config-derived endpoint
- **Election** — coordinator election status; switch between auto (any node may become coordinator) and manual (fixed primary) mode, and set the primary node in manual mode (`POST /web/api/election`)
- **Health** — machine API health, DB ping, stealth status

Dark theme matches the scheduler WebUI (`--bg:#0b0e14;--panel:#11151f;--line:#232a3b;--acc:#5b8cff`).

## Hardening and exposure

By default the WebUI is served on the public `listen_addr` (`/web` and `/admin`) and requires no extra auth when `xray.stealth.enforce_control` is `false`. For production stealth, set:

```yaml
xray:
  stealth:
    enforce: true
    enforce_control: true  # hide /ts2021 and gate WebUI behind auth
```

When `enforce` and `enforce_control` are both `true` (the default), the WebUI at `/web` and `/admin` requires an `Authorization: Bearer <api-key>` or `X-API-Key` header; unauthenticated `curl http://<listen_addr>/web` returns `401`. This prevents anonymous enumeration and product fingerprinting. For stronger isolation, firewall the public `listen_addr` and expose the WebUI only on `metrics_listen_addr` (internal network) or behind a Reality-enabled reverse proxy.

`packaging/systemd/stealthscale.service` ships with the same `config-example.yaml` defaults; `docs/stealthscale/install.md` covers the hardening.

## API

When hardening is disabled, all endpoints serve JSON from the live `state.State` stores and require no extra auth when accessed via the embedded UI (API-key auth is enforced at the control-plane API layer). When hardening is enabled, every `/web/api/*` and `/admin/api/*` request (and the frontend shell itself) requires the API-key header above.

```
GET    /web/api/nodes        # list nodes
DELETE /web/api/nodes/{id}   # delete node (delegates to `state.DeleteNode(NodeView)`; its change is broadcast to connected clients — falls back to stub in tests)
POST   /web/api/nodes/{id}/expire   # set node expiry {expiry: "<RFC3339>"|"never"} → `state.SetNodeExpiry`
POST   /web/api/nodes/{id}/rename   # rename node {name} → `state.RenameNode`
POST   /web/api/nodes/{id}/tags     # set node tags {tags: ["tag:..."]} → `state.SetNodeTags`
POST   /web/api/nodes/{id}/routes   # set advertised routes {routes: ["10.0.0.0/24"]} → `state.SetApprovedRoutes`; an empty list clears all approved routes (same as `stscale nodes approve-routes -r ""` and the v1 API)
GET    /web/api/users        # list users
POST   /web/api/users        # create user {name, email} (delegates to `state.CreateUser`; its change is broadcast to connected clients)
GET    /web/api/preauthkeys  # list pre-auth keys
POST   /web/api/preauthkeys  # create pre-auth key {userID, reusable, ephemeral, aclTags, expiry} (delegates to `state.CreatePreAuthKey`)
POST   /web/api/preauthkeys/{id}/expire  # expire pre-auth key → `state.ExpirePreAuthKey`
DELETE /web/api/preauthkeys/{id}         # delete pre-auth key → `state.DeletePreAuthKey`
GET    /web/api/apikeys      # list API keys (masked prefixes only — never key material or hashes)
POST   /web/api/apikeys      # create API key {expiry} → raw key returned exactly once → `state.CreateAPIKey`
POST   /web/api/apikeys/{id}/expire  # expire API key → `state.ExpireAPIKey`
DELETE /web/api/apikeys/{id}         # destroy API key → `state.DestroyAPIKey`
GET    /web/api/auth/{id}    # pending auth session status → `state.GetAuthCacheEntry`
POST   /web/api/auth/{id}/approve   # approve pending SSH-check session (`FinishAuth` with an accepting verdict); registration sessions → 409
POST   /web/api/auth/{id}/reject    # reject pending SSH-check session (`FinishAuth` with a rejecting verdict); registration sessions → 409
GET    /web/api/policy       # current policy
PUT    /web/api/policy       # set policy {policy: "HuJSON string"} (also POST)
POST   /web/api/policy/check # validate a draft policy against live users and nodes without persisting it
GET    /web/api/derp         # DERP map + stealth_satisfied flag + runtime flag (whether the live map is operator-applied)
POST   /web/api/derp         # apply a full DERP map at runtime {"derpMap": {...}} (bare DERPMap object also accepted) → `state.SetDERPMapRuntime`; broadcast to connected clients immediately and takes precedence over the `derp.auto_update_enabled` refresh (which skips while an operator-applied map is in effect); reported with stealth_satisfied/shouldIncludeDERP so the fail-closed gate is visible
GET    /web/api/vless/{id}   # VLESS URI for node {id} (reflects an applied per-node endpoint override)
POST   /web/api/vless/{id}   # apply node VLESS endpoint settings {address, port, security} at runtime — validated against the transport rules (security: none|tls|xtls|reality_xtls, "reality" alias accepted), stored via `state.SetNodeVLESS` and enforced by rebinding the node's VLESS listener; a security variant the server has no credentials for fails closed on the transport; {"clear": true} (no endpoint fields — the two are mutually exclusive) removes the override and rebinds the listener to the config-derived endpoint
GET    /web/api/election     # coordinator election snapshot
POST   /web/api/election     # coordinator election: {"mode": "auto"|"manual"} switches the mode (→ `state.SetElectionMode`), {"node_id": N} sets the fixed primary (→ `state.SetElectionPrimary`); both may be combined; a POST without "mode" only sets/clears the primary (legacy form); switching to auto clears the fixed primary
GET    /web/api/health       # health + stealth status
```

All write endpoints are available under both `/web/api/*` and `/admin/api/*` (alias). They validate JSON and, when the corresponding write method is implemented, delegate to `state.State` (e.g. `CreateUser`, `CreatePreAuthKey`, `SetPolicy`, `DeleteNode`, `SetNodeExpiry`, `RenameNode`, `SetNodeTags`, `SetApprovedRoutes`, `SetDERPMapRuntime`, `SetElectionMode`, `SetElectionPrimary`, `ExpirePreAuthKey`, `DeletePreAuthKey`, `CreateAPIKey`, `ExpireAPIKey`, `DestroyAPIKey`, and the auth-session verdicts) and forward the returned `change.Change` to connected clients (see the propagation notes below). Otherwise they return stub success for coverage. Holepunching via DERP/STUN is exempt from the VLESS requirement and is only offered when stealth is satisfied.

## Serving

The UI is registered via `hscontrol/webui.Register(mux, cfg, state, onChange)` in `hscontrol/app.go:createRouter` — the `onChange` sink is `(*StealthScale).Change`, so every write the UI applies is broadcast to connected clients — and served by `hscontrol/webui.Handler` (Go `embed.FS` + file server). Both `stscale serve` and any client mode mount the same handler, so there is no difference in code.

## Reverse proxy and TLS

Terminate TLS at your reverse proxy and forward `/web` and `/` to the StealthScale server. See [Reverse proxy](../ref/integration/reverse-proxy.md) and [TLS](../ref/tls.md).

> **Management parity:** node expire/rename/tag/approve-routes, pre-auth key expire/delete, API-key CRUD, auth approve/reject (SSH-check sessions — registration sessions complete via the node's auth URL), policy `check`, runtime DERP map apply, and VLESS endpoint settings apply and clear are all reachable from the Web UI (see the API list above; implemented in `hscontrol/webui/webui.go`, tabs in `frontend/index.html`). WebUI stores the API key in `localStorage` and sends `Authorization: Bearer` (see `frontend/app.js:ensureAPIKey`). Hardened default requires `enforce && enforce_control` and validates via `ValidateAPIKey` (not presence only).

> **Propagation notes:** every WebUI write is live end-to-end: node delete (`DELETE /nodes/{id}`), expiry, rename, tags and approved-routes updates (`POST /nodes/{id}/{expire,rename,tags,routes}`) and user creation (`POST /users`) forward the `change.Change` their `state.State` call returns to the mapper batcher, so connected clients receive the write on their live long-poll within the batch delay — no reconnect, no poll restart — exactly like the identical CLI and API v1/v2/gRPC operations (a node delete also tears down the deleted node's batcher connection through its `NodeRemoved` work item). A DERP map applied via `POST /web/api/derp` is live in the control-plane state immediately and is broadcast to connected clients right away (a `DERPMap` change). It takes precedence over configuration: while an operator-applied map is in effect, the periodic `derp.auto_update_enabled` refresh (default every 3h) skips its `derp.yaml`-derived map instead of reverting the operator's one — the applied map stays in effect until the server restarts. When `derp.server.enabled` and `derp.automatically_add_embedded_derp_region` are set, the embedded region is kept in applied maps. GET and POST responses report `runtime` (whether the live map is operator-applied). The stealth fail-closed gate is unaffected: when stealth is unsatisfied the map is stored but withheld from clients. Per-node VLESS endpoint settings are applied at runtime via `state.State.SetNodeVLESS`: the override is stored per node, the read paths (`GET /web/api/vless/{id}`, the node listing) report the overridden endpoint, and the node's VLESS listener is rebound to the overridden address/port — in-flight connections keep their deadline. A `POST {"clear": true}` clears the override (`SetNodeVLESS` with `nil`) and rebinds the listener to the config-derived endpoint — the path back to the derived endpoint without a restart. A per-node security variant the server has no credentials for (e.g. a `tls` override on a `reality_xtls` server) fails closed on the transport rather than silently downgrading.
