# Connecting a node (same binary)

StealthScale's **unified binary** (`stscale`) is both client and coordinator.
Every device runs the same `stscale` binary; `stscale serve` starts the
coordinator, and `stscale up` joins an existing network. This is the
recommended path — no separate patched `tailscaled` is required.

> See the [project goals](overview.md#project-goals) and
> [bootstrap, discovery, and steady-state transport](overview.md#bootstrap-discovery-and-steady-state-transport)
> for the design rationale: discovery may be non-stealth, but **all
> post-discovery transport is VLESS stealth**.

If you prefer a patched Tailscale client (e.g. `tailscaled` with the VLESS
dial patch), that still works — see
[Legacy patched Tailscale client](#legacy-patched-tailscale-client) and the
[client modification guide](../client-modification.md).

!!! note "A stealth-capable client is required for the data plane"

    The unified `stscale` binary *is* the stealth-capable client: it uses `utls`
    to shape the ClientHello and speaks VLESS to the coordinator. A stock,
    unmodified Tailscale client (or anything that dials WireGuard) cannot use the
    data plane — it must be patched to import `hscontrol/xray`. `stscale up`
    performs a stealth transport check (validating the server's Reality public
    key) before joining.

## What the client needs

Every node must be told three things before it can talk to the coordinator:

1. The **coordinator URL** (`--coordinator`, e.g. `https://ctl.example.com`).
1. Its **node UUID** — the VLESS authentication identity.
1. Its **VLESS endpoint** — address and port (and TLS mode if enabled).

The UUID and port are derived from the node's ID and never change. You fetch
them on the coordinator with:

```shell
stscale nodes vless <node-id>
```

Which prints a table (use `-o json` for machine-readable output):

```
Field    | Value
ID       | 9f4d4f6c-d1e2-4a3b-9c8d-7a6b5c4d3e2f
Address  | 10.0.0.5
Port     | 10042
Security | reality_xtls
URI      | vless://9f4d4f6c-d1e2-4a3b-9c8d-7a6b5c4d3e2f@10.0.0.5:10042?security=reality_xtls&fp=chrome&type=tcp&flow=xtls-rprx-vision
```

Give the client the `URI` (or the `id`/`address`/`port`/`security` fields
individually).

!!! note "Listeners start on registration"

    `stscale nodes vless <node-id>` computes the endpoint deterministically
    for **any** node ID, but the coordinator only runs a VLESS listener for a node
    once that node has registered. Register the node first, then hand it its
    endpoint (e.g. `stscale nodes list` to confirm it exists).

## Registering a node — unified binary (recommended)

With a pre-auth key from the coordinator
([create one first](install.md#register-users-and-preauth-keys)):

```shell
stscale up \
  --coordinator https://ctl.example.com \
  --authkey <pre-auth-key> \
  --vless-uri 'vless://<uuid>@<addr>:<port>?security=reality_xtls'
# --endpoint is an alias for --vless-uri; no --vless flag needed — VLESS is
# the default transport (xray.enabled=true, security=reality_xtls), holepunching via DERP/STUN is exempt.
```

`stscale up` is the client side of the unified binary (`hscontrol/xray/client.go`
`DialVLESS`): it dials the node's stealth endpoint (`--vless-uri` / `--endpoint`), authenticates with its UUID,
and performs a stealth transport check — validating the server via its Reality
public key over uTLS-shaped TLS with a decoy certificate (`fp=chrome` by default).
Once two peers have identified each other, **all further transport is VLESS
stealth** — the control path runs inside the stealth stream rather than as a
plaintext port. Holepunching via DERP/STUN for NAT traversal is exempt and is
only used when stealth is satisfied (`xray.stealth.enforce: true`, fail-closed).

The coordinator is also a node: running `stscale serve` on any device makes it
both a node and a coordinator (on by default). Point another device at it with
`stscale up --coordinator ...` to join.

!!! note "Discovery vs stealth"

    Finding a coordinator for the first time may use whatever is necessary
    (including non-stealth discovery). But once two peers have exchanged
    identity, all communication is VLESS stealth per
    [overview.md](overview.md#bootstrap-discovery-and-steady-state-transport).

Subsequent logins (e.g. after a reboot) are stateless: the node reconnects to
the same endpoint with the same UUID (derived as
`UUIDv5("6ba7b810-9dad-11d1-80b4-00c04fd430c8", "stealthscale:<id>")`).

### Legacy patched Tailscale client

If you already have a patched `tailscaled`/`tailscale` binary (see
[client modification guide](../client-modification.md)), it also works:

```shell
tailscale up \
  --login-server https://ctl.example.com \
  --authkey <pre-auth-key> \
  --vless-uri 'vless://<uuid>@<addr>:<port>?security=reality_xtls'
```

The exact flag for the VLESS endpoint depends on the patch; it may read from a
config file or `TS_VLESS_URI`. The transport is the same (VLESS + noise +
HTTP/2), only the binary differs.

## Verifying connectivity

- On the server:

    ```shell
    stscale nodes list
    ```

    should show the node as online.

- The `tailscale status` on the client should report `Connected` to the
  control server, and show peers once a second node joins.

## Behavior notes

- **Static identity.** The node UUID/port are fixed for the lifetime of the
  node ID. Re-registering the same machine (restart, reinstall) reuses the
  same endpoint.
- **No WireGuard keys.** The client must not try to start `wgengine`; the
  patched build routes the control connection over VLESS instead.
- **Per-node ports.** Every node uses exactly one port in the configured
  range. Two nodes never share a port.
- **TLS mode.** If the server runs `xray.security: tls`, the client must be
  configured with `security=tls` and validate (or pin) the server
  certificate, just like any HTTPS client. With uTLS, the ClientHello is
  shaped to mimic a browser.

## Troubleshooting

| Symptom                                | Likely cause                                                                                                                   |
| -------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------ |
| Connection reset during `tailscale up` | Wrong UUID for this node, or the node ID does not match the listener (the server refuses mismatched UUIDs).                    |
| Connection timeout                     | The port range is not reachable: check the firewall/NAT and `xray.listen_port` … `xray.max_listen_port`.                       |
| TLS handshake failure                  | `xray.security: tls` is enabled but the client is configured `security=none` (or vice-versa), or the certificate is untrusted. |
| Registration succeeds but no peers     | Both nodes must be in the same user/policy and both must be online; check `stscale nodes list`.                                |
