# StealthScale — Single Binary (Server + Client)

**There is no separate client.** `stscale` is the single unified binary:
`stscale serve` runs the coordinator, `stscale up` runs the node.
Both are `VLESS+Reality` (`hscontrol/xray` `DialVLESS` + `RealityUClient`).

This directory is **reference only** — it shows how a `tailscale`/`tailscaled`
fork *could* be patched to speak `VLESS+Reality` if you prefer `tailscale up`
flags. You do **not** need it: use `stscale up`.

The unified `stscale` binary (`cmd/stealthscale/cli/up.go` → `xray.DialVLESS` →
`controlbase.Client` over VLESS) is already VLESS+Reality capable and is built
for all platforms via `goreleaser` (`stscale` only). The `client/example` and
`client/patch` are examples, not a separate distribution.

## Fork pin

`go.mod:55` pins `tailscale.com v1.101.0-pre`. The patch is developed against
that tag; newer `tailscale.com` may need rebase.

```bash
git clone https://github.com/tailscale/tailscale --branch v1.101.0-pre --depth 1 /tmp/tailscale
cp -r client/patch/* /tmp/tailscale/
cd /tmp/tailscale
go mod edit -replace github.com/tomiwebpro/stealthscale=/path/to/stealthscale
```

## What the patch does

1. **Adds `VLESSConfig` handling** (`hscontrol/xray/vless.go:50` `VLESSConfig`,
   `ParseVLESSURI`, `NodeUUID`/`NodePort` derivation). The URI
   `vless://<uuid>@<host>:<port>?security=reality_xtls&pbk=&sid=&spx=&fp=&dest=`
   is parsed (see `hscontrol/xray/client.go:255`); `NodePort`/`NodeUUID` are
   `HMAC(secret,"node-port:<id>")` / `UUIDv5(HMAC(secret,"uuid-namespace")…)` when
   `xray.secret` is set, fallback `stealthscale:<id>` otherwise.

2. **Replaces the dial** in `tailscale.com/control/controlclient/direct.go`
   (or `control/controlhttp`) — where it currently does
   `net.Dial` → `controlbase.Client` — with:

   ```go
   cfg, _ := xray.ParseVLESSURI(vlessURI) // from --vless-uri flag or TS_VLESS_URI env
   conn, err := xray.DialVLESS(ctx, cfg)   // does RealityUClient when cfg.Security=="reality_xtls" && cfg.PublicKey!=""
   // then:
   noiseConn, err := controlbase.Client(ctx, conn, machineKey, controlPub, capVer)
   // then HTTP/2 over noiseConn to POST /machine/register and stream MapRequest
   ```

   `xray.DialVLESS` (`hscontrol/xray/client.go:57`) handles `none` (plain TCP),
   `tls`/`xtls` (utls), and `reality_xtls` (utls+`RealityUClient` with
   `ServerName` = decoy host from `cfg.Dest`, `PublicKey`/`ShortId`/`SpiderX`/`FP`).

3. **Adds flags** `--vless-uri` / `TS_VLESS_URI` and `--coordinator` (or reuses
   `--login-server`) so `tailscale up --login-server https://ctl.example.com --vless-uri 'vless://…' --authkey …` works.

See `patch/direct.go.diff` for the minimal diff and `example/` for a standalone
Go program that mimics the patched dial without forking tailscale.

## Building — Single Binary

```bash
# Single unified binary (server+client, VLESS+Reality)
make build                      # ./stscale
goreleaser build --snapshot --clean  # dist/stscale_*/* (stscale only)
./stscale up --coordinator https://ctl.example.com --authkey <key> --vless-uri 'vless://…'
./stscale nodes vless <id>      # prints vless:// URI (cmd/stealthscale/cli/nodes.go:318)
```

`goreleaser` builds **only `stscale`** (`.goreleaser.yml:17` `id: stealthscale`,
`binary: stscale`, `CGO_ENABLED=0`, targets `darwin_*`/`linux_*`/`windows_*`).
There is no `stclient` or `tailscale-stealth` artifact — `stscale` *is* the
client. `scripts/build-client.sh` and `client/example` remain as reference if
you still want a `tailscale` fork.

## Verification (single binary)

```bash
# Unit: Reality handshake (local dest, no internet)
go test ./hscontrol/xray -run TestServerRealityTLSRoundTrip -count=1 -v
# Servertest: full register over VLESS+Reality via stscale's DialVLESS (same code as stscale up)
go test ./hscontrol/servertest -run TestVLESSRealityE2E -count=1 -v
# Integration: Docker e2e with stscale as both server and client (no separate tailscale image)
go run ./cmd/hi doctor && go run ./cmd/hi run TestVLESSRealityE2E --postgres
# Manual: stscale as client
./stscale up --coordinator https://ctl.example.com --authkey <key> --vless-uri 'vless://…' --insecure
```

Stock `tailscale` without `stscale` cannot connect when
`xray.stealth.enforce_control:true` — `/ts2021` is gated and only VLESS+Reality (`hscontrol/app.go:247`) is served.
