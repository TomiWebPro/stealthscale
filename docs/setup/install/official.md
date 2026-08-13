# Official releases

Official releases for stscale are available as binaries for various platforms and DEB packages for Debian and Ubuntu.
Both are available on the [GitHub releases page](https://github.com/tomiwebpro/stealthscale/releases).

## Using packages for Debian/Ubuntu (recommended)

It is recommended to use our DEB packages to install stscale on a Debian based system as those packages configure a
local user to run stscale, provide a default configuration and ship with a systemd service file. Supported
distributions are Ubuntu 22.04 or newer, Debian 12 or newer.

1. Download the [latest stscale package](https://github.com/tomiwebpro/stealthscale/releases/latest) for your platform (`.deb` for Ubuntu and Debian).

    ```shell
    STSCALE_VERSION="" # See above URL for latest version, e.g. "X.Y.Z" (NOTE: do not add the "v" prefix!)
    STSCALE_ARCH="" # Your system architecture, e.g. "amd64"
    wget --output-document=stscale.deb \
     "https://github.com/tomiwebpro/stealthscale/releases/download/v${STSCALE_VERSION}/stealthscale_${STSCALE_VERSION}_linux_${STSCALE_ARCH}.deb"
    ```

1. Install stscale:

    ```shell
    sudo apt install ./stscale.deb
    ```

1. [Configure stscale by editing the configuration file](../../ref/configuration.md). An up-to date example
   configuration file is also available in `/usr/share/doc/stealthscale/examples/config-example.yaml`:

    ```shell
    sudo nano /etc/stealthscale/config.yaml
    ```

1. Restart stscale to pick up configuration changes:

    ```shell
    sudo systemctl restart stscale
    ```

1. Verify that stscale is running as intended:

    ```shell
    sudo systemctl status stscale
    ```

Continue on the [getting started page](../../usage/getting-started.md) to register your first machine.

## Using standalone binaries (advanced)

!!! warning "Advanced"

    This installation method is considered advanced as one needs to take care of the local user and the systemd
    service themselves. If possible, use the [DEB packages](#using-packages-for-debianubuntu-recommended) or a
    [community package](community.md) instead.

This section describes the installation of stscale according to the [Requirements and
assumptions](../requirements.md#assumptions). StealthScale is run by a dedicated local user and the service itself is
managed by systemd.

1. Download the latest [`stscale` binary from GitHub's release page](https://github.com/tomiwebpro/stealthscale/releases):

    ```shell
    sudo wget --output-document=/usr/bin/stscale \
    https://github.com/tomiwebpro/stealthscale/releases/download/v<STSCALE VERSION>/stealthscale_<STSCALE VERSION>_linux_<ARCH>
    ```

1. Make `stscale` executable:

    ```shell
    sudo chmod +x /usr/bin/stscale
    ```

1. Add a dedicated local user to run stscale:

    ```shell
    sudo useradd \
     --create-home \
     --home-dir /var/lib/stealthscale/ \
     --system \
     --user-group \
     --shell /usr/sbin/nologin \
     stscale
    ```

1. Download the example configuration for your chosen version and save it as: `/etc/stealthscale/config.yaml`. Adjust the
   configuration to suit your local environment. See [Configuration](../../ref/configuration.md) for details.

    ```shell
    sudo mkdir -p /etc/stealthscale
    sudo nano /etc/stealthscale/config.yaml
    ```

1. Copy [stscale's systemd service file](https://github.com/tomiwebpro/stealthscale/blob/main/packaging/systemd/stscale.service)
   to `/etc/systemd/system/stscale.service` and adjust it to suit your local setup. The following parameters likely need
   to be modified: `ExecStart`, `WorkingDirectory`, `ReadWritePaths`.

1. In `/etc/stealthscale/config.yaml`, override the default `stscale` unix socket with a path that is writable by the
   `stscale` user or group:

    ```yaml title="config.yaml"
    unix_socket: /var/run/stealthscale/stscale.sock
    ```

1. Reload systemd to load the new configuration file:

    ```shell
    systemctl daemon-reload
    ```

1. Enable and start the new stscale service:

    ```shell
    systemctl enable --now stscale
    ```

1. Verify that stscale is running as intended:

    ```shell
    systemctl status stscale
    ```

Continue on the [getting started page](../../usage/getting-started.md) to register your first machine.
