# StealthScale production Dockerfile — single binary `stscale` with VLESS+Reality.
# This is the canonical image for manual `docker build` (goreleaser `kos` uses ko+distroless).
# The image is intentionally close to Dockerfile.integration but without the delve debugger
# and with a production-oriented runtime (non-root `stealthscale` user, volume declarations,
# healthcheck, and the full VLESS port range).

FROM docker.io/golang:1.26.5-trixie AS builder
ARG VERSION=dev
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags "-X main.version=${VERSION}" -o /out/stscale ./cmd/stealthscale

FROM debian:trixie-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
        ca-certificates curl iproute2 iptables sqlite3 \
    && apt-get clean && rm -rf /var/lib/apt/lists/* \
    && groupadd -r stealthscale && useradd -r -g stealthscale -d /var/lib/stealthscale -m stealthscale \
    && mkdir -p /var/run/stealthscale /var/lib/stealthscale /etc/stealthscale \
    && chown -R stealthscale:stealthscale /var/run/stealthscale /var/lib/stealthscale \
    && chmod 0750 /var/run/stealthscale /var/lib/stealthscale

COPY --from=builder /out/stscale /usr/local/bin/stscale
RUN ln -sf /usr/local/bin/stscale /usr/local/bin/stealthscale

# Persisted state: sqlite DB, noise key, derp key, and .xray_secret (Reality identity).
# Mount /var/lib/stealthscale as a volume so the per-node VLESS UUID/ports stay stable.
VOLUME ["/var/lib/stealthscale", "/etc/stealthscale"]

EXPOSE 8080/tcp 9090/tcp 3478/udp 10001-10100/tcp

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 CMD ["/usr/local/bin/stscale", "health"]

ENTRYPOINT ["/usr/local/bin/stscale"]
CMD ["serve"]
