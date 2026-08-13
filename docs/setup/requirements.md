# Requirements

StealthScale should just work as long as the following requirements are met:

- A server with a public IP address for stscale. A dual-stack setup with a public IPv4 and a public IPv6 address is
  recommended.
- StealthScale is served via HTTPS on port 443[^1] and [may use additional ports](#ports-in-use).
- A reasonably modern Linux or BSD based operating system.
- A dedicated local user account to run stscale.
- A little bit of command line knowledge to configure and operate stscale.

## Ports in use

The ports in use vary with the intended scenario and enabled features. Some of the listed ports may be changed via the
[configuration file](../ref/configuration.md) but we recommend to stick with the default values.

- tcp/80
    - Expose publicly: yes
    - HTTP, used by Let's Encrypt to verify ownership via the HTTP-01 challenge.
    - Only required if the built-in Let's Encrypt client with the HTTP-01 challenge is used. See [TLS](../ref/tls.md) for
      details.
- tcp/443
    - Expose publicly: yes
    - HTTPS, required to make StealthScale available to Tailscale clients[^1]
    - Required if the [embedded DERP server](../ref/derp.md) is enabled
- udp/3478
    - Expose publicly: yes
    - STUN, required if the [embedded DERP server](../ref/derp.md) is enabled
- tcp/9090
    - Expose publicly: no
    - [Metrics and debug endpoint](../ref/debug.md#metrics-and-debug-endpoint)

## Assumptions

The stscale documentation and the provided examples are written with a few assumptions in mind:

- StealthScale is running as system service via a dedicated local user `stscale`.
- The [configuration](../ref/configuration.md) is loaded from `/etc/stealthscale/config.yaml`.
- SQLite is used as database.
- The data directory for stscale (used for private keys, policy, SQLite database, …) is located in `/var/lib/stealthscale`.
- URLs and values that need to be replaced by the user are either denoted as `<VALUE_TO_CHANGE>` or use placeholder
  values such as `stscale.example.com`.

Please adjust to your local environment accordingly.

\[^1\]: The Tailscale client assumes HTTPS on port 443 in certain situations. Serving stscale either via HTTP or via
HTTPS on a port other than 443 is possible but sticking with HTTPS on port 443 is strongly recommended for
production setups. See [issue 2164](https://github.com/tomiwebpro/stealthscale/issues/2164) for more information.
