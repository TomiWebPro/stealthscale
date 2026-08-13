# Development builds

!!! warning

    Development builds are created automatically from the latest `main` branch
    and are **not versioned releases**. They may contain incomplete features,
    breaking changes, or bugs. Use them for testing only.

Each push to `main` produces container images and cross-compiled binaries.
Container images are multi-arch (amd64, arm64) and use the same distroless
base image as official releases.

## Container images

Images are available from both Docker Hub and GitHub Container Registry, tagged
with the short commit hash of the build (e.g. `main-abc1234`):

- Docker Hub: `docker.io/stealthscale/stealthscale:main-<sha>`
- GitHub Container Registry: `ghcr.io/tomiwebpro/stealthscale:main-<sha>`

To find the latest available tag, check the
[GitHub Actions workflow](https://github.com/tomiwebpro/stealthscale/actions/workflows/container-main.yml)
or the [GitHub Container Registry package page](https://github.com/tomiwebpro/stealthscale/pkgs/container/stscale).

For example, to run a specific development build:

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
  --health-cmd "CMD stscale health" \
  docker.io/stealthscale/stealthscale:main-<sha> \
  serve
```

See [Running stscale in a container](container.md) for full container setup instructions.

## Binaries

Pre-built binaries from the latest successful build on `main` are available
via [nightly.link](https://nightly.link/tomiwebpro/stealthscale/workflows/container-main/main):

| OS    | Arch  | Download                                                                                                                    |
| ----- | ----- | --------------------------------------------------------------------------------------------------------------------------- |
| Linux | amd64 | [stscale-linux-amd64](https://nightly.link/tomiwebpro/stealthscale/workflows/container-main/main/stscale-linux-amd64.zip)   |
| Linux | arm64 | [stscale-linux-arm64](https://nightly.link/tomiwebpro/stealthscale/workflows/container-main/main/stscale-linux-arm64.zip)   |
| macOS | amd64 | [stscale-darwin-amd64](https://nightly.link/tomiwebpro/stealthscale/workflows/container-main/main/stscale-darwin-amd64.zip) |
| macOS | arm64 | [stscale-darwin-arm64](https://nightly.link/tomiwebpro/stealthscale/workflows/container-main/main/stscale-darwin-arm64.zip) |

After downloading and extracting the archive, make the binary executable and follow the
[standalone binary installation](official.md#using-standalone-binaries-advanced)
instructions for setting up the service.
