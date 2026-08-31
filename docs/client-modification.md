# VLESS Protocol (Reference)

**There is no separate client.** `stscale` is the single unified binary:
`stscale serve` (coordinator) and `stscale up` (node) both use
`hscontrol/xray` `DialVLESS` + `RealityUClient` (`cmd/stealthscale/cli/up.go`).
You do **not** need a patched `tailscale`.

This guide is **reference** — it explains the VLESS transport so you can
understand `stscale up` or, if you prefer, patch `tailscale` yourself.
A stock `tailscaled` dials with WireGuard; `stscale` replaces it with **VLESS
+ Reality + uTLS** to the node's `vless://` endpoint. The control plane
protocol is unchanged — only the transport changes.

## Protocol contract

The server implements, and the client must implement:

### 1. VLESS handshake

```
client → server:
    version        : uint8    = 0x00
    uuid           : [16]byte // node's deterministic UUID, binary form
    addons_length  : uint8    = 0x00

server → client (on success):
    version        : uint8    = 0x00
```

On UUID mismatch or a malformed header the server closes the connection
without replying. See [XRay/VLESS reference](ref/xray-vless.md).

### 2. Noise handshake

Run the standard Tailscale **controlbase** handshake over the authenticated
stream:

- **Server**: `controlbase.Server(ctx, conn, controlKey, nil)`
- **Client**: `controlbase.Client(ctx, conn, machineKey, controlPub, version)`

The server derives the machine identity from the noise peer key, exactly as
in the legacy path.

### 3. Machine API over HTTP/2

Serve (server) / drive (client) the machine API over HTTP/2 on top of the
noise connection:

- `POST /machine/register` — `tailcfg.RegisterRequest`
- `POST /machine/map` — streaming `tailcfg.MapRequest`/`MapResponse`
- `GET/POST /machine/ssh/...` — SSH actions

This is the same router the legacy `/ts2021` path uses; the VLESS path just
reaches it through a different listener.

## Where to patch in a Tailscale client

The key component is the dialing layer that currently uses
`controlhttp.Dialer` (or the TS2021 client), which upgrades the connection
via `controlhttp`/`controlbase`. Replace the network dial step:

1. **Dial** the node's VLESS endpoint (from the URI: `vless://<uuid>@<addr>:<port>?security=...`).
1. **Write the VLESS header** (version + UUID + addons length), then **read
   the version byte** from the server.
1. Return the resulting connection as the "dialed" connection to the noise
   handshake (`controlbase.Client`), so the rest of the client is untouched.

If you use `security=tls`, wrap the dialed connection in `tls.Client` before
sending the VLESS header.

### Minimal sketch

```go
conn, err := net.Dial("tcp", net.JoinHostPort(addr, strconv.Itoa(port)))
if err != nil {
    return nil, err
}

uuidBytes, _ := uuid.Parse(nodeUUID)
header := append([]byte{0}, uuidBytes[:]...) // version 0 + UUID
header = append(header, 0)                   // addons length
if _, err := conn.Write(header); err != nil {
    conn.Close()
    return nil, err
}

var ack [1]byte
if _, err := io.ReadFull(conn, ack[:]); err != nil {
    conn.Close()
    return nil, err // server refused (e.g. wrong UUID)
}
if ack[0] != 0 {
    conn.Close()
    return nil, fmt.Errorf("unexpected VLESS version %d", ack[0])
}

// hand `conn` to controlbase.Client / controlhttp as the underlying dial
```

## Configuration surface

The patch needs a way to receive the node's VLESS endpoint. Suggested
options, in order of preference:

1. A `--vless-uri` flag on `tailscale up`, parsed into UUID/addr/port/security.
1. A dedicated config file the patched binary reads at startup.
1. Environment variables (`TS_VLESS_URI`, `TS_VLESS_UUID`, ...).

The URI is produced by the server:

```shell
stscale nodes vless <node-id>
# vless://9f4d4f6c-...@10.0.0.5:10042?security=reality_xtls&fp=chrome&type=tcp&flow=xtls-rprx-vision&dest=www.cloudflare.com%3A443&pbk=<pubkey-hex>&sid=<shortId-hex>&spx=%2F
# for reality_xtls: dest (decoy), pbk (Reality public key hex), sid (shortId hex), spx (SpiderX), fp (uTLS fingerprint: chrome/firefox/safari/ios/randomized → hscontrol/xray/client.go:247)
# for none/tls/xtls: vless://<uuid>@<addr>:<port>?security=<mode>
```

## What must NOT change

- The noise key material and the machine registration flow.
- The `tailcfg` request/response types and capability versioning.
- The map/poll session semantics — the server's `PollNetMapHandler` is
  transport-agnostic.

## Testing against the server

The repository's test suite includes an end-to-end test that drives the
protocol with a raw VLESS client:
`hscontrol/servertest/xray_vless_test.go`. It covers:

- pre-auth-key registration through the full VLESS → noise → HTTP/2 stack;
- rejection of a connection presenting the wrong UUID;
- automatic creation of a VLESS listener when a node registers through the
  regular noise path.

Use it as the reference behaviour when developing your client patch.
