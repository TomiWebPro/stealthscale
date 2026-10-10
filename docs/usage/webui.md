# Web UI

StealthScale ships an embedded WebUI similar to [headscale-ui](https://github.com/gurucomputing/headscale-ui), built into both the server and client — same codebase, no duplication.

Visit `http://<server>:8080/web` or `http://<server>:8080/admin` (alias) — works identically whether you run `stscale serve` or a client-mode binary.

## Features

- **Nodes** — hostname, IPs, tags, VLESS UUID/port; per-node actions: expire (set an RFC3339 timestamp or `never`), rename, tag, and approve advertised routes
- **Users** — names, emails, providers; create users
- **PreAuthKeys** — keys, reuse/ephemeral flags, expiry; create, expire and delete keys
- **API Keys** — list (masked prefixes only), create (raw key shown once), expire and delete
- **Auth** — inspect pending authentication requests by auth ID; approve and reject SSH-check sessions (registration sessions are completed via the node's auth URL, not by approve/reject)
- **Policy** — current ACL/HuJSON policy; validate a draft policy against the live users and nodes without applying it
- **DERP** — DERP map with `stealth_satisfied` and `shouldIncludeDERP` flags (fail-closed when stealth unsatisfied); the map can be edited and applied at runtime (`POST /web/api/derp`) — no `derp.yaml` edit or restart needed
- **VLESS** — per-node `vless://` URI, UUID, port, Reality dest; endpoint settings (address, port, security variant) can be submitted from the UI (`POST /web/api/vless/{id}`)
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
DELETE /web/api/nodes/{id}   # delete node (delegates to `state.DeleteNode(NodeView)`, falls back to stub in tests)
POST   /web/api/nodes/{id}/expire   # set node expiry {expiry: "<RFC3339>"|"never"} → `state.SetNodeExpiry`
POST   /web/api/nodes/{id}/rename   # rename node {name} → `state.RenameNode`
POST   /web/api/nodes/{id}/tags     # set node tags {tags: ["tag:..."]} → `state.SetNodeTags`
POST   /web/api/nodes/{id}/routes   # set advertised routes {routes: ["10.0.0.0/24"]} → `state.SetApprovedRoutes`; an empty list clears all approved routes (same as `stscale nodes approve-routes -r ""` and the v1 API)
GET    /web/api/users        # list users
POST   /web/api/users        # create user {name, email} (delegates to `state.CreateUser` with Change)
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
GET    /web/api/derp         # DERP map + stealth_satisfied flag
POST   /web/api/derp         # apply a full DERP map at runtime {"derpMap": {...}} (bare DERPMap object also accepted) → `state.SetDERPMap`; reported with stealth_satisfied/shouldIncludeDERP so the fail-closed gate is visible
GET    /web/api/vless/{id}   # VLESS URI for node {id}
POST   /web/api/vless/{id}   # set node VLESS endpoint settings {address, port, security} — validated against the transport rules (security: none|tls|xtls|reality_xtls, "reality" alias accepted); delegates to `state.SetNodeVLESS` when the runtime per-node override mechanism is available
GET    /web/api/election     # coordinator election snapshot
POST   /web/api/election     # coordinator election: {"mode": "auto"|"manual"} switches the mode (→ `state.SetElectionMode`), {"node_id": N} sets the fixed primary (→ `state.SetElectionPrimary`); both may be combined; a POST without "mode" only sets/clears the primary (legacy form); switching to auto clears the fixed primary
GET    /web/api/health       # health + stealth status
```

All write endpoints are available under both `/web/api/*` and `/admin/api/*` (alias). They validate JSON and, when the corresponding write method is implemented, delegate to `state.State` (e.g. `CreateUser`, `CreatePreAuthKey`, `SetPolicy`, `DeleteNode`, `SetNodeExpiry`, `RenameNode`, `SetNodeTags`, `SetApprovedRoutes`, `SetDERPMap`, `SetElectionMode`, `SetElectionPrimary`, `ExpirePreAuthKey`, `DeletePreAuthKey`, `CreateAPIKey`, `ExpireAPIKey`, `DestroyAPIKey`, and the auth-session verdicts). Otherwise they return stub success for coverage. Holepunching via DERP/STUN is exempt from the VLESS requirement and is only offered when stealth is satisfied.

## Serving

The UI is registered via `hscontrol/webui.Register(mux, cfg, state)` in `hscontrol/app.go:createRouter` and served by `hscontrol/webui.Handler` (Go `embed.FS` + file server). Both `stscale serve` and any client mode mount the same handler, so there is no difference in code.

## Reverse proxy and TLS

Terminate TLS at your reverse proxy and forward `/web` and `/` to the StealthScale server. See [Reverse proxy](../ref/integration/reverse-proxy.md) and [TLS](../ref/tls.md).


> **Management parity:** node expire/rename/tag/approve-routes, pre-auth key expire/delete, API-key CRUD, auth approve/reject (SSH-check sessions — registration sessions complete via the node's auth URL), policy `check`, runtime DERP map apply, and VLESS endpoint settings submission are all reachable from the Web UI (see the API list above; implemented in `hscontrol/webui/webui.go`, tabs in `frontend/index.html`). WebUI stores the API key in `localStorage` and sends `Authorization: Bearer` (see `frontend/app.js:ensureAPIKey`). Hardened default requires `enforce && enforce_control` and validates via `ValidateAPIKey` (not presence only).

> **Propagation notes:** a DERP map applied via `POST /web/api/derp` is live in the control-plane state immediately and is sent to clients with their next map response that carries the DERP map (initial map on connect, a change broadcast, or the periodic `derp.auto_update_enabled` refresh, default every 3h — which re-applies the `derp.yaml`-derived map and overrides a WebUI-applied one until a broadcast hook lands). The stealth fail-closed gate is unaffected: when stealth is unsatisfied the map is stored but withheld from clients. Per-node VLESS endpoint settings are validated and delegated to `state.State.SetNodeVLESS` once the runtime per-node override mechanism lands (tracked as the follow-up work item); until then the endpoint reports the validated settings and the derived endpoint.
