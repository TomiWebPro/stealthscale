# Running stscale in a container

!!! warning "Community documentation"

    This page is not actively maintained by the stscale authors and is
    written by community members. It is _not_ verified by stscale developers.

    **It might be outdated and it might miss necessary steps**.

A container runtime such as [Docker](https://www.docker.com) or [Podman](https://podman.io) is required. The container
image can be found on [Docker Hub](https://hub.docker.com/r/stscale/stscale) and [GitHub Container
Registry](https://github.com/tomiwebpro/stealthscale/pkgs/container/stscale). The container image URLs are:

- [Docker Hub](https://hub.docker.com/r/stscale/stscale): `docker.io/stealthscale/stealthscale:<VERSION>`
- [GitHub Container Registry](https://github.com/tomiwebpro/stealthscale/pkgs/container/stscale):
  `ghcr.io/tomiwebpro/stealthscale:<VERSION>`

## Configure and run stscale

1. Create a directory on the container host to store stscale's [configuration](../../ref/configuration.md) and the SQLite database:

    ```shell
    mkdir -p ./stscale/{config,lib}
    cd ./stscale
    ```

1. Download the example configuration for your chosen version and save it as: `$(pwd)/config/config.yaml`. Adjust the
   configuration to suit your local environment. See [Configuration](../../ref/configuration.md) for details.

1. Start stscale from within the previously created `./stscale` directory:

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
      docker.io/stealthscale/stealthscale:<VERSION> \
      serve
    ```

    Note: use `0.0.0.0:8080:8080` instead of `127.0.0.1:8080:8080` if you want to expose the container externally.

    This command mounts the local directories inside the container, forwards port 8080 and 9090 out of the container so
    the stscale instance becomes available and then detaches so stscale runs in the background.

    A similar configuration for `docker-compose`:

    ```yaml title="docker-compose.yaml"
    services:
      stscale:
        image: docker.io/stealthscale/stealthscale:<VERSION>
        restart: unless-stopped
        container_name: stscale
        read_only: true
        tmpfs:
          - /var/run/stealthscale
        ports:
          - "127.0.0.1:8080:8080"
          - "127.0.0.1:9090:9090"
        volumes:
          # Please set <STSCALE_PATH> to the absolute path
          # of the previously created stscale directory.
          - <STSCALE_PATH>/config:/etc/stealthscale:ro
          - <STSCALE_PATH>/lib:/var/lib/stealthscale
        command: serve
        healthcheck:
            test: ["CMD", "stscale", "health"]
    ```

1. Verify stscale is running:

    Follow the container logs:

    ```shell
    docker logs --follow stscale
    ```

    Verify running containers:

    ```shell
    docker ps
    ```

    Verify stscale is available:

    ```shell
    curl http://127.0.0.1:8080/health
    ```

Continue on the [getting started page](../../usage/getting-started.md) to register your first machine.

## Debugging stscale running in Docker

The StealthScale container image is based on a distroless image that does not contain a shell or any other debug tools. If you need to debug stscale running in the Docker container, you can use the `-debug` variant, for example `docker.io/stealthscale/stealthscale:x.x.x-debug`.

### Running the debug Docker container

To run the debug Docker container, use the exact same commands as above, but replace `docker.io/stealthscale/stealthscale:x.x.x` with `docker.io/stealthscale/stealthscale:x.x.x-debug` (`x.x.x` is the version of stscale). The two containers are compatible with each other, so you can alternate between them.

### Executing commands in the debug container

The default command in the debug container is to run `stscale`, which is located at `/ko-app/stscale` inside the container.

Additionally, the debug container includes a minimalist Busybox shell.

To launch a shell in the container, use:

```shell
docker run -it docker.io/stealthscale/stealthscale:x.x.x-debug sh
```

You can also execute commands directly, such as `ls /ko-app` in this example:

```shell
docker run docker.io/stealthscale/stealthscale:x.x.x-debug ls /ko-app
```

Using `docker exec -it` allows you to run commands in an existing container.
