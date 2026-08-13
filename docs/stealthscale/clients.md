# Connecting a patched client

StealthScale does **not** work with stock Tailscale clients. The data path
was changed from WireGuard to VLESS, so the client must be modified to dial
the node's VLESS endpoint and speak the Tailscale noise protocol over the
authenticated stream.

This page assumes you already have a built, patched `tailscaled`/`tailscale`
binary. If you are building one, read the
[client modification guide](../client-modification.md) first.

## What the client needs

Every node must be told three things before it can talk to the server:

1. The **control server URL** (`--login-server`).
1. Its **node UUID** — the VLESS authentication identity.
1. Its **VLESS endpoint** — address and port (and TLS mode if enabled).

The UUID and port are derived from the node's ID and never change. You fetch
them on the server with:

```shell
stscale nodes vless <node-id>
```

Which prints a table (use `-o json` for machine-readable output):

```
Field    | Value
ID       | 9f4d4f6c-d1e2-4a3b-9c8d-7a6b5c4d3e2f
Address  | 10.0.0.5
Port     | 10042
Security | none
URI      | vless://9f4d4f6c-d1e2-4a3b-9c8d-7a6b5c4d3e2f@10.0.0.5:10042?security=none
```

Give the client the `URI` (or the `id`/`address`/`port`/`security` fields
individually, depending on how the patch reads its configuration).

!!! note "Listeners start on registration"

    `stscale nodes vless <node-id>` computes the endpoint deterministically
    for **any** node ID, but the server only runs a VLESS listener for a node
    once that node has registered. Register the node first, then hand it its
    endpoint (e.g. `stscale nodes list` to confirm it exists).

## Registering a node

With a pre-auth key from the server
([create one first](install.md#register-users-and-preauth-keys)):

```shell
tailscale up \
  --login-server https://ctl.example.com \
  --authkey <pre-auth-key> \
  --vless-uri 'vless://<uuid>@<addr>:<port>?security=none'
```

!!! note

    The exact flag for the VLESS endpoint depends on the client patch. It
    may instead read from a config file or environment variable — see the
    modification guide and your build's `tailscale up --help`.

The client connects to its VLESS endpoint, authenticates with its UUID,
and registers through the machine API exactly as a normal Tailscale client
would — only the transport differs. Subsequent logins (e.g. after a reboot)
are stateless on the client side: it reconnects to the same endpoint with
the same UUID.

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
