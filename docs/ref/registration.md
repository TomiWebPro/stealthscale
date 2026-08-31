# Registration methods (StealthScale)

!!! warning "Stealth transport is mandatory by default — stock `tailscale up` alone will not connect"

    StealthScale defaults to `xray.enabled:true`, `xray.security:reality_xtls`, `xray.stealth.enforce:true`, `xray.stealth.enforce_control:true`. The plaintext `/ts2021` Noise endpoint is **not served** — only **VLESS+Reality** per-node listeners (`10001`–`10100` by default). A stock Tailscale client that dials `tailscale up --login-server <URL>` without `--vless-uri` will fail with `404` or timeout. Use the **unified `stscale` binary** (`stscale up --coordinator <URL> --authkey <key> --vless-uri 'vless://<uuid>@<addr>:<port>?security=reality_xtls&pbk=...&sid=...&dest=...&fp=chrome'` — fetch with `stscale nodes vless <id>`) or a patched Tailscale that imports `hscontrol/xray` (`DialVLESS` + `RealityUClient`). See [StealthScale clients](../stealthscale/clients.md), [XRay/VLESS reference](./xray-vless.md), and [Threat model](./threat-model.md). If you need stock-client compat, set `xray.stealth.enforce_control:false` (then fingerprintable).

StealthScale supports multiple ways to register a node. The preferred registration method depends on the identity of a node
and your use case. The identity model is inherited from Headscale/Tailscale.

## Identity model

Tailscale's identity model distinguishes between personal and tagged nodes:

- A personal node (or user-owned node) is owned by a human and typically refers to end-user devices such as laptops,
  workstations or mobile phones. End-user devices are managed by a single user.
- A tagged node (or service-based node or non-human node) provides services to the network. Common examples include web-
  and database servers. Those nodes are typically managed by a team of users. Some additional restrictions apply for
  tagged nodes, e.g. a tagged node is not allowed to [Tailscale SSH](https://tailscale.com/docs/features/tailscale-ssh)
  into a personal node.

StealthScale implements Tailscale's identity model and distinguishes between personal and tagged nodes where a personal
node is owned by a StealthScale user and a tagged node is owned by a tag. Tagged devices are grouped under the special user
`tagged-devices`.

## Registration methods

There are two main ways to register new nodes, [web authentication](#web-authentication) and [registration with a pre
authenticated key](#pre-authenticated-key). Both methods can be used to register personal and tagged nodes.

### Web authentication

Web authentication is the default method to register a new node. It's interactive, where the client initiates the
registration and the StealthScale administrator needs to approve the new node before it is allowed to join the network. A
node can be approved with:

- StealthScale CLI (described in this documentation)
- [StealthScale API](api.md)
- Or delegated to an identity provider via [OpenID Connect](oidc.md)

Web authentication relies on the presence of a StealthScale user. Use the `stscale users` command to create a new
user\[^1\]:

```console
stscale users create <USER>
```

=== "Personal devices — unified `stscale` (recommended, VLESS+Reality)"

    ```console
    # on coordinator: create user and pre-auth key
    stscale users create alice
    stscale preauthkeys create --user <USER_ID> --reusable
    # fetch this node's VLESS URI (port/pbk/sid derived from xray.secret)
    stscale nodes vless <NODE_ID>
    # on the new device (same stscale binary):
    stscale up --coordinator <YOUR_STSCALE_URL> --authkey <YOUR_AUTH_KEY> \
      --vless-uri 'vless://<uuid>@<addr>:<port>?security=reality_xtls&pbk=<pubkey>&sid=<sid>&dest=www.cloudflare.com%3A443&fp=chrome'
    ```

    For stock `tailscale up` you must patch the client to dial VLESS (see [Client modification](../client-modification.md)); otherwise the coordinator's `/ts2021` is not mounted (`enforce_control:true`). A browser window flow may still open if you set `enforce_control:false` (not stealth):

    ```console
    tailscale up --login-server <YOUR_STSCALE_URL>  # only when enforce_control:false
    stscale auth register --user <USER> --auth-id <AUTH_ID>
    ```

=== "Tagged devices — unified `stscale`"

    Your StealthScale user must be authorized for the tag in [`tagOwners`](https://tailscale.com/docs/reference/syntax/policy-file#tag-owners):

    ```json title="The user alice can register nodes tagged with tag:server"
    {
      "tagOwners": { "tag:server": ["alice@"] }
    }
    ```

    ```console
    stscale users create alice  # once
    stscale preauthkeys create --tags tag:server  # tags come from the key
    stscale nodes vless <NODE_ID>
    stscale up --coordinator <YOUR_STSCALE_URL> --authkey <YOUR_AUTH_KEY> \
      --vless-uri 'vless://<uuid>@<addr>:<port>?security=reality_xtls&pbk=...&sid=...&dest=...'
    ```

    Equivalent stock-client (only when `enforce_control:false`):

    ```console
    tailscale up --login-server <YOUR_STSCALE_URL> --advertise-tags tag:server
    stscale auth register --user <USER> --auth-id <AUTH_ID>
    ```

    Ownership transfers to `tagged-devices`; check `stscale nodes list`.

### Pre-authenticated key

Registration with a pre-authenticated key is non-interactive and best for automation. With stealth, the key is still required but the transport is VLESS.

=== "Personal devices — VLESS"

    ```console
    stscale users create <USER>
    stscale preauthkeys create --user <USER_ID>
    # -> <YOUR_AUTH_KEY> (default 1h, single use; add --reusable --expiration 24h as needed)
    stscale nodes vless <ID>  # get URI for this node
    stscale up --coordinator <YOUR_STSCALE_URL> --authkey <YOUR_AUTH_KEY> \
      --vless-uri 'vless://<uuid>@<addr>:<port>?security=reality_xtls&pbk=...&sid=...&dest=...&fp=chrome'
    ```

    Stock-client fallback (only when `enforce_control:false`):

    ```console
    tailscale up --login-server <YOUR_STSCALE_URL> --authkey <YOUR_AUTH_KEY>
    ```

=== "Tagged devices — VLESS"

    ```console
    stscale preauthkeys create --tags tag:<TAG>
    # -> <YOUR_AUTH_KEY> (tags baked into key)
    stscale nodes vless <ID>
    stscale up --coordinator <YOUR_STSCALE_URL> --authkey <YOUR_AUTH_KEY> \
      --vless-uri 'vless://<uuid>@<addr>:<port>?security=reality_xtls&pbk=...&sid=...&dest=...&fp=chrome'
    ```

    Stock fallback (only when `enforce_control:false`):

    ```console
    tailscale up --login-server <YOUR_STSCALE_URL> --authkey <YOUR_AUTH_KEY>
    ```

    Listed as `tagged-devices` in `stscale nodes list`.

\[^1\]: [Ensure that the StealthScale username does not end with `@`.](oidc.md#reference-a-user-in-the-policy)
