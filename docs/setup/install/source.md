# Build from source (StealthScale)

!!! warning "Use the StealthScale guide for stealth defaults"

    The authoritative build + config is [StealthScale Install & deploy](../../stealthscale/install.md) (`make build` → `./stscale`, `reality_xtls` default, `xray.secret` handling). This page is kept for completeness but points there for VLESS identity, WebUI, and threat model. Requires **Go 1.26.1+** (per `go.mod`), not `1.20`. `nix develop` pins the toolchain with `buf` + `golangci-lint` + `prek`.

## Linux / generic

```shell
sudo apt install git  # or equivalent
git clone https://github.com/tomiwebpro/stealthscale.git
cd stealthscale
# optionally checkout a release tag
latestTag=$(git describe --tags $(git rev-list --tags --max-count=1))
git checkout $latestTag

# Nix dev shell (recommended: Go 1.26.1, buf, golangci-lint, prek)
nix develop
prek install
make dev      # fmt + lint + test + build
make build    # => ./stscale
./stscale --help
```

Or `go build` directly (reproduce goreleaser flags: `CGO_ENABLED=0`, `-mod=readonly`):

```shell
go build -ldflags="-s -w -X github.com/tomiwebpro/stealthscale/hscontrol/types.Version=$latestTag" -o stscale ./cmd/stealthscale
chmod +x stscale
sudo cp stscale /usr/local/bin/
```

Copy `config-example.yaml` to `/etc/stealthscale/config.yaml` and keep `xray.*` defaults (`enabled:true`, `security:reality_xtls`, `utls_fingerprint:chrome`, `stealth.enforce:true`, `enforce_control:true`, `reality.dest: www.cloudflare.com:443`). See [Configuration](../../ref/configuration.md) and [XRay/VLESS reference](../../ref/xray-vless.md).

## OpenBSD

```shell
pkg_add go git gmake
git clone https://github.com/tomiwebpro/stealthscale.git
cd stealthscale
latestTag=$(git describe --tags $(git rev-list --tags --max-count=1))
git checkout $latestTag

# native
go build -ldflags="-s -w -X github.com/tomiwebpro/stealthscale/hscontrol/types.Version=$latestTag" -o stscale ./cmd/stealthscale

# or cross-compile via make
gmake build GOOS=openbsd  # Makefile is GNU make
```

Copy to `/usr/local/sbin` and follow the systemd/service notes in [StealthScale Install & deploy](../../stealthscale/install.md) (adjust `ExecStart`, `WorkingDirectory`, `ReadWritePaths`, user `stscale` vs packaging's `coordination`).

## Verify stealth

```shell
go test ./hscontrol/xray -run TestReality -v
go test ./hscontrol/stealth -v
stscale nodes vless 1  # stable URI
curl http://127.0.0.1:8080/web/api/derp | jq .stealth_satisfied
```
