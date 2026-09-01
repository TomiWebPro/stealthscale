# Development builds (StealthScale)

!!! warning

    Development builds are from `main` and are **not versioned releases**. They track `xray.security:reality_xtls` via `github.com/xtls/reality` + `utls` and `xray.stealth.enforce:true` (fail-closed DERP). May contain breaking changes — test only. The authoritative setup is [StealthScale Install & deploy](../../stealthscale/install.md).

Each push to `main` produces container images and cross-compiled binaries (`stscale` unified binary: `stscale serve` and `stscale up` same binary, `CGO_ENABLED=0`). Docker images are enabled for alpha.4 via `kos` (distroless).


## Container images (multi-arch amd64/arm64, distroless — enabled for alpha.4)

- `ghcr.io/tomiwebpro/stealthscale:<VERSION>` via goreleaser `kos` (`main-<sha>`, `v0.0.1-alpha.4`, `unstable`, `sha-*`, `latest`/`stable` for prod)
- `ghcr.io/tomiwebpro/stealthscale:main-<sha>` for dev (canonical)
- `docker.io/stealthscale/stealthscale:main-<sha>` (legacy)

Latest tag: [GitHub Releases](https://github.com/tomiwebpro/stealthscale/releases) or [GHCR package page](https://github.com/tomiwebpro/stealthscale/pkgs/container/stealthscale).

```shell
docker run \
  --name stscale \
  --detach \
  --read-only \
  --tmpfs /var/run/stealthscale \
  --volume "$(pwd)/config:/etc/stealthscale:ro" \
  --volume "$(pwd)/lib:/var/lib/stealthscale" \
  --publish 127.0.0.1:8080:8080 \
  --publish 127.0.0.1:9090:9090 \
  --publish 10001-10100:10001-10100 \
  --publish 3478:3478/udp \
  ghcr.io/tomiwebpro/stealthscale:main-<sha> \
  serve
```

Expose the VLESS range (`10001`–`10100`) and STUN if DERP enabled. Without it, `stscale nodes vless <id>` URIs are unreachable. For `postgres`, set `xray.secret` (`openssl rand -hex 32`) explicitly.

## Binaries (via nightly.link)

| OS | Arch | Download |
|---|---|---|
| Linux | amd64 | [stscale-linux-amd64](https://nightly.link/tomiwebpro/stealthscale/workflows/container-main/main/stscale-linux-amd64.zip) |
| Linux | arm64 | [stscale-linux-arm64](https://nightly.link/tomiwebpro/stealthscale/workflows/container-main/main/stscale-linux-arm64.zip) |
| macOS | amd64 | [stscale-darwin-amd64](https://nightly.link/tomiwebpro/stealthscale/workflows/container-main/main/stscale-darwin-amd64.zip) |
| macOS | arm64 | [stscale-darwin-arm64](https://nightly.link/tomiwebpro/stealthscale/workflows/container-main/main/stscale-darwin-arm64.zip) |

After download, verify VLESS identity:

```shell
./stscale --help
./stscale configtest
./stscale nodes vless 1  # should be stable when xray.secret / .xray_secret stable
curl http://127.0.0.1:8080/web | head  # embedded WebUI
curl http://127.0.0.1:8080/health
```

See [Container](container.md) for full container setup and [StealthScale clients](../../stealthscale/clients.md) for `stscale up --coordinator --vless-uri`.
