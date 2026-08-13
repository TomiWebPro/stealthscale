# XRay/VLESS transport

This is the reference for the VLESS transport added by StealthScale. It
replaces WireGuard as the transport for the Tailscale control protocol.

## Configuration

See [Install & deploy](../stealthscale/install.md) for a full walkthrough. The
relevant `config.yaml` section:

```yaml
xray:
  enabled: false        # master switch for the VLESS listeners
  listen_addr: 0.0.0.0  # address the per-node listeners bind to
  listen_port: 10001    # first port in the per-node range
  max_listen_port: 10100 # last port in the per-node range
  security: none        # "none", "tls" or "xtls"
  cert_file: ""         # required when security is "tls"/"xtls"
  key_file: ""          # required when security is "tls"/"xtls"
  timeout: 30s          # VLESS header read timeout
```

### Validation rules

- `listen_port` must be greater than zero; `max_listen_port` must be greater
  than or equal to `listen_port` (a degenerate range collapses to a single
  port).
- `security` accepts only `none`, `tls`, or `xtls`.
- For `tls`/`xtls`, both `cert_file` and `key_file` must be set.
- `timeout` defaults to `30s` when omitted.

## Per-node endpoints

Every registered node gets a dedicated listener. Both the UUID and the port
are **deterministic functions of the node ID**, so they never change across
restarts.

### UUID

```
UUID = UUIDv5(namespace = "6ba7b810-9dad-11d1-80b4-00c04fd430c8",
              name     = "stealthscale:<node-id>")
```

### Port

```
port = listen_port + sha256("stealthscale-port:<node-id>")[0:8] % (max_listen_port - listen_port + 1)
```

### Endpoint format

The CLI prints the endpoint for a node:

```shell
stscale nodes vless <node-id>
```

```
Field    | Value
ID       | 9f4d4f6c-d1e2-4a3b-9c8d-7a6b5c4d3e2f
Address  | 10.0.0.5
Port     | 10042
Security | none
URI      | vless://9f4d4f6c-d1e2-4a3b-9c8d-7a6b5c4d3e2f@10.0.0.5:10042?security=none
```

Use `-o json` (or `--output json`) to get the machine-readable form:

```json
{
    "address": "10.0.0.5",
    "id": "9f4d4f6c-d1e2-4a3b-9c8d-7a6b5c4d3e2f",
    "port": 10042,
    "security": "none",
    "uri": "vless://9f4d4f6c-d1e2-4a3b-9c8d-7a6b5c4d3e2f@10.0.0.5:10042?security=none"
}
```

The URI follows the standard VLESS URI form:
`vless://<uuid>@<address>:<port>?security=<security>`.

## Wire protocol

### VLESS header (client → server)

After the TCP (or TLS/XTLS) connection is established, the client sends:

| Field         | Length   | Value                                       |
| ------------- | -------- | ------------------------------------------- |
| Version       | 1 byte   | `0x00`                                      |
| UUID          | 16 bytes | The node's deterministic UUID (binary form) |
| Addons length | 1 byte   | `0x00` (no addons)                          |
| Addons        | n bytes  | Optional; parsed and discarded              |

### Version response (server → client)

If the UUID matches the node the listener belongs to, the server replies
with a single byte: the VLESS version (`0x00`). On mismatch or a malformed
header, the server closes the connection **without** sending the version
byte.

### Application payload

After the VLESS handshake, the raw stream is the Tailscale control protocol:

1. **Noise handshake** (`controlbase.Server`/`Client`) — the standard TS2021
   noise key agreement.
1. **HTTP/2 machine API** over the noise connection — registration, map/poll,
   SSH actions (`/machine/register`, `/machine/map`, `/machine/ssh/...`).

The server consumes the VLESS header, then treats the stream as a normal
noise connection. No VLESS framing is applied to the payload itself.

## Security modes

| Mode   | Behaviour                                                                                                                                                |
| ------ | -------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `none` | Plain VLESS over TCP. The connection itself is not TLS-wrapped; stealth relies on the noise handshake and the traffic being unrecognisable as Tailscale. |
| `tls`  | The VLESS handshake runs inside a TLS connection (`tls.Server`). Combined with a real certificate, the stream is indistinguishable from HTTPS.           |
| `xtls` | XTLS-style TLS wrapping (accepted for compatibility with Xray clients; behaves like `tls` in the current implementation).                                |

## Operational notes

- **Firewall**: open the whole `listen_port` … `max_listen_port` TCP range,
  since per-node ports are assigned from it and clients connect to exactly
  one port each.
- **Listeners lifecycle**: listeners are started for all existing nodes at
  server startup and for newly registered nodes at registration time
  (idempotent — starting an already-running listener is a no-op). They are
  shut down with the server.
- **Concurrency**: each accepted connection is handled in its own goroutine;
  the noise handshake and machine API are served until the connection
  closes.

## Implementation

- `hscontrol/xray/` — VLESS protocol (`vless.go`) and the per-node listener
  server (`server.go`).
- `hscontrol/xray_server.go` — wiring into the `StealthScale` server: boots
  listeners at startup and ensures a listener exists when a node registers.
- `hscontrol/noise.go` — `serveNoise`/`noiseRouter`, shared by both the
  legacy `/ts2021` path and the VLESS path.
- `cmd/stealthscale/cli/nodes.go` — the `nodes vless` command.
