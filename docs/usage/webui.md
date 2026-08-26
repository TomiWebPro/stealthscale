# Using a web UI with StealthScale

StealthScale itself does not ship a built-in web interface, but a number of
community projects provide one. This page covers the practical steps to run a
web UI in front of your `stscale` server. For the full list of available
projects, see the [Web UI reference](../ref/integration/web-ui.md).

## What a web UI needs

Every web UI is a client of the StealthScale control plane. To connect one
you need:

- **The control API base URL** — the same `server_url` your nodes use, e.g.
  `https://ctl.example.com`. The web UI talks to the management API, not the
  VLESS transport.
- **An API key** — create one with the CLI and hand it to the UI:

  ```shell
  stscale apikeys create --user <user-id>
  ```

  The printed key is what the UI uses to authenticate against the API. Treat
  it like a password.
- **Network reachability** — the UI must be able to reach `server_url` over
  HTTPS. In most deployments the UI runs behind the same reverse proxy that
  terminates TLS for the control server.

## Typical deployment

A common layout is to run the web UI as a small container (or static site)
behind your existing reverse proxy, pointing it at the control API and the
API key you created above. Because the UI only consumes the management API,
no VLESS ports need to be opened for it.

```
+----------+      HTTPS       +-------------------+      +------------------+
|  Browser | <--------------> |  Reverse proxy    | <--> |  stscale API     |
+----------+                  +-------------------+      +------------------+
        ^                                                          |
        |                       +-------------------+              |
        +---------------------- |  Web UI (static / | <------------+
                                 |  container)       |
                                 +-------------------+
```

## Reverse proxy and TLS

Terminate TLS for both the control API and the web UI at your reverse proxy
(recommended). See [Reverse proxy](../ref/integration/reverse-proxy.md) for
the general pattern, and [TLS](../ref/tls.md) for the server-side options.

## Choosing a UI

The [Web UI reference](../ref/integration/web-ui.md) lists community projects
ranging from full admin dashboards to minimal self-service device managers.
Pick one that matches your deployment model (single admin vs. self-service
users) and follow its own setup instructions, supplying the control API URL
and API key from the steps above.

!!! warning "Community contributions"

    The web UIs listed in the reference are maintained by community members,
    not by the StealthScale authors. Validate any third-party project before
    exposing it to the Internet, and ask in the
    [Discord server](https://discord.gg/c84AZQhmpx) "web-interfaces" channel
    for guidance.
