# Running `stscale` in a container (StealthScale)

!!! warning "StealthScale defaults — keep VLESS+Reality"

    The authoritative guide is [StealthScale Install & deploy](../../stealthscale/install.md). The container must expose the **per-node VLESS range** `xray.listen_port` … `max_listen_port` (default `10001`–`10100`, `xray.security:reality_xtls`, `xray.secret` auto to `.xray_secret`). Without it, `stscale nodes vless <id>` will print a port that is not reachable. The embedded Web UI is at `/web` and `/admin` on `listen_addr` (hardened by default: `enforce_control:true` → `401` without API key).

A container runtime (Docker/Podman) is required. Images are at:

- Docker Hub: `docker.io/stealthscale/stealthscale:<VERSION>` (legacy name)
- GitHub Container Registry (canonical): `ghcr.io/tomiwebpro/stealthscale:<VERSION>` — use this.

## Configure and run

1. Create host directories for config and DB (which will also hold `.xray_secret`):

    ```shell
    mkdir -p ./stscale/{config,lib}
    cd ./stscale
    ```

1. Download `config-example.yaml` for your version as `$(pwd)/config/config.yaml` and edit `server_url`, keep `xray.enabled:true` / `reality_xtls`, set `xray.secret` explicitly for postgres, and ensure `listen_addr`/`xray.listen_addr` are reachable. See [Configuration](../../ref/configuration.md) and [XRay/VLESS reference](../../ref/xray-vless.md).

1. Start (exposing both control plane and VLESS range):

    ```shell
    docker run \
      --name stscale \
      --detach \
      --read-only \
      --tmpfs /var/run/stealthscale \
      --volume "$(pwd)/config:/etc/stealthscale:ro" \
      --volume "$(pwd)/lib:/var/lib/stealthscale" \
      --publish 8080:8080 \
      --publish 9090:9090 \
      --publish 10001-10100:10001-10100 \
      --publish 3478:3478/udp \
      ghcr.io/tomiwebpro/stealthscale:<VERSION> \
      serve
    ```

    Or `docker-compose.yaml`:

    ```yaml
    services:
      stscale:
        image: ghcr.io/tomiwebpro/stealthscale:<VERSION>
        restart: unless-stopped
        read_only: true
        tmpfs: ["/var/run/stealthscale"]
        ports:
          - "8080:8080"
          - "9090:9090"
          - "10001-10100:10001-10100"
          - "3478:3478/udp"
        volumes:
          - ./config:/etc/stealthscale:ro
          - ./lib:/var/lib/stealthscale
        command: serve
        healthcheck: { test: ["CMD", "stscale", "health"] }
    ```

1. Verify:

    ```shell
    docker logs --follow stscale
    curl http://127.0.0.1:8080/health
    curl http://127.0.0.1:8080/web | head
    ```

For debug variant use `:<VERSION>-debug` (distroless vs busybox, `ls /ko-app`).

Continue at [StealthScale clients](../../stealthscale/clients.md) (`stscale up --coordinator --vless-uri 'vless://...?security=reality_xtls&pbk=...&sid=...&dest=...'`). See [StealthScale Install & deploy](../../stealthscale/install.md) for backup of `.xray_secret` and threat model notes.
