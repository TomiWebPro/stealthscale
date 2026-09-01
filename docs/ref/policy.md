# Policy

StealthScale implements a large portion of Tailscale's [policy
features](https://tailscale.com/docs/features/tailnet-policy-file), most notably access control based on
[ACLs](https://tailscale.com/docs/features/access-control/acls) and
[Grants](https://tailscale.com/docs/features/access-control/grants) or [Tailscale
SSH](https://tailscale.com/docs/features/tailscale-ssh). See [limitations](#limitations) to learn about missing features
and notable implementation differences between StealthScale and Tailscale.

StealthScale uses the same [huJSON](https://github.com/tailscale/hujson) based file format as Tailscale. By default, no
policy is loaded which means that StealthScale allows all traffic between nodes. To start using a policy file[^1], specify
its path in the `policy.path` key in the [configuration file](configuration.md).

StealthScale needs to be reloaded to pick up changes to the policy file. Either reload StealthScale via its systemd service
(`sudo systemctl reload stscale`) or by sending a SIGHUP signal (`sudo kill -HUP $(pidof stscale)`) to the main
process. StealthScale logs the result of policy processing after each reload.

Please have a look at Tailscale's policy related documentation to learn more:

- [Tailscale policy file](https://tailscale.com/docs/features/tailnet-policy-file): A description of supported sections
  within the policy file along with links to syntax references for each section.
- [ACLs](https://tailscale.com/docs/features/access-control/acls): How to configure access control using ACLs.
- [Grants](https://tailscale.com/docs/features/access-control/grants): Introduction to Grants with links to [syntax
  reference](https://tailscale.com/docs/reference/syntax/grants),
  [examples](https://tailscale.com/docs/reference/examples/grants) and a [migration guide from ACLs to
  Grants](https://tailscale.com/docs/reference/migrate-acls-grants).

## Getting started

StealthScale supports both [ACLs](https://tailscale.com/docs/features/access-control/acls) and
[Grants](https://tailscale.com/docs/features/access-control/grants) to write an access control policy. We recommend the
use of Grants since ACLs are considered legacy and will not receive new features by Tailscale.

### Allow All

If you define a policy file but completely omit the `"acls"` or `"grants"` section, StealthScale will default to an [allow
all](https://tailscale.com/docs/reference/examples/acls#allow-all-default-acl) policy. This means all devices connected
to your tailnet will be able to communicate freely with each other.

```json title="policy.json"
{}
```

### Deny All

To [prevent all communication within your tailnet](https://tailscale.com/docs/reference/examples/acls#deny-all), you can
include an empty array for the `"grants"` section in your policy file.

```json title="policy.json"
{
  "grants": []
}
```

### More examples

- See our documentation on [subnet routers](routes.md#subnet-router) and [exit nodes](routes.md#exit-node) to learn how
  to restrict their use or how to automatically approve them.
- The Tailscale documentation provides a large collection of configuration examples:
    - [ACL examples](https://tailscale.com/docs/reference/examples/acls)
    - [Grants examples](https://tailscale.com/docs/reference/examples/grants)
    - [SSH configuration](https://tailscale.com/docs/features/tailscale-ssh#configure-tailscale-ssh)
    - [Define a tag](https://tailscale.com/docs/features/tags#define-a-tag)

______________________________________________________________________

## Limitations

- [Device postures](https://tailscale.com/docs/features/device-posture) and the related sections such as `postures` or
  `srcPosture` aren't supported (see `hscontrol/policy/v2/types.go:2711` posture tracking).
- [IP sets](https://tailscale.com/docs/features/tailnet-policy-file/ip-sets) aren't supported.
- A subset of [Autogroups](#autogroups) are available. Missing: `autogroup:admin` and `autogroup:owner` (SaaS supports them; StealthScale returns `invalid autogroup` with tracking issue reference — see `feature-autogroups-admin-owner`). Workaround: use `group:admin` and migrate owner-gated grants manually.
- [Funnel / Serve](https://tailscale.com/docs/features/funnel) (`ServeConfig`, `NodeAttrFunnel`) not supported — `hscontrol/policy/v2/types.go:102` rejects `funnel` (`issues/2527`). See `feature-funnel-serve`.
- `nodeAttrs` `ipPool` allocator not implemented — `hscontrol/policy/v2/types.go:2718` returns `ErrNodeAttrIPPoolUnsupported` (`issues/2912`). Use `prefixes.v4/.v6` allocation instead (`feature-ippool-allocator`).
- Network flow logs (`tailcfg.NetworkFlowLog`) not implemented — see `feature-network-flow-logs`.
- OIDC groups in policy (`allowedGroups` filter only) — cannot be used in `src`/`dst`; see `feature-oidc-groups-in-policy`.
- IP sets, device posture, and policy posture not supported — see `feature-policy-posture-ipsets`.
- CLI alias `acl` now mirrors `policy` (`stscale acl get` works for Headscale migration — `feature-cli-parity-gaps`).

## Autogroups

StealthScale supports several [Autogroups](https://tailscale.com/docs/reference/targets-and-selectors#autogroups) that
automatically include users, destinations, or devices with specific properties. Autogroups provide a convenient way to
write policy rules without manually listing individual users or devices.

### [`autogroup:internet`](https://tailscale.com/docs/reference/targets-and-selectors#autogroupinternet)

Allows access to the internet through [exit nodes](routes.md#exit-node). Can only be used in policy destinations.

```json title="policy.json"
{
  "grants": [
    {
      "src": ["alice@"],
      "dst": ["autogroup:internet"],
      "ip": ["*"]
    }
  ]
}
```

### [`autogroup:member`](https://tailscale.com/docs/reference/targets-and-selectors#autogrouprole)

Includes all [personal (untagged) devices](registration.md/#identity-model).

```json title="policy.json"
{
  "grants": [
    {
      "src": ["autogroup:member"],
      "dst": ["tag:prod-app-servers"],
      "ip": ["80,443"]
    }
  ]
}
```

### [`autogroup:tagged`](https://tailscale.com/docs/reference/targets-and-selectors#autogrouptagged)

Includes all devices that [have at least one tag](registration.md/#identity-model).

```json title="policy.json"
{
  "grants": [
    {
      "src": ["autogroup:tagged"],
      "dst": ["tag:monitoring"],
      "ip": ["9090"]
    }
  ]
}
```

### [`autogroup:self`](https://tailscale.com/docs/reference/targets-and-selectors#autogroupself)

Includes devices where the same user is authenticated on both the source and destination. Does not include tagged
devices. Can only be used in policy destinations.

```json title="policy.json"
{
  "grants": [
    {
      "src": ["autogroup:member"],
      "dst": ["autogroup:self"],
      "ip": ["*"]
    }
  ]
}
```

!!! warning "The current implementation of `autogroup:self` is inefficient"

    Using `autogroup:self` may cause performance degradation on the StealthScale coordinator server in large deployments,
    as filter rules must be compiled per-node rather than globally and the current implementation is not very efficient.

    If you experience performance issues, consider using more specific policy rules or limiting the use of
    `autogroup:self`.

    ```json title="policy.json"
    {
      "grants": [
        // The following rules allow internal users to communicate with their
        // own nodes in case autogroup:self is causing performance issues.
        {
          "src": ["boss@"],
          "dst": ["boss@"],
          "ip": ["*"]
        },
        {
          "src": ["dev1@"],
          "dst": ["dev1@"],
          "ip": ["*"]
        },
        {
          "src": ["intern1@"],
          "dst": ["intern1@"],
          "ip": ["*"]
        }
      ]
    }
    ```

### [`autogroup:nonroot`](https://tailscale.com/docs/reference/targets-and-selectors#other-built-in-targets)

Used in Tailscale SSH rules to allow access to any user except root. Can only be used in the `users` field of SSH rules.

```json title="policy.json"
{
  "ssh": [
    {
      "action": "accept",
      "src": ["autogroup:member"],
      "dst": ["autogroup:self"],
      "users": ["autogroup:nonroot"]
    }
  ]
}
```

### [`autogroup:danger-all`](https://tailscale.com/docs/reference/targets-and-selectors#autogroupdanger-all)

This autogroup resolves to all IP addresses (`0.0.0.0/0` and `::/0`) which also includes all IP addresses outside the
standard Tailscale IP ranges. This autogroup can only be used as source.

## Node Attributes

[Node attributes](https://tailscale.com/docs/reference/syntax/policy-file#node-attributes) allow for device-specific
configuration and attributes. At least the following node attributes are currently supported by StealthScale\[^2\]:

- `drive:access`, `drive:share`: [Taildrive support](https://tailscale.com/docs/features/taildrive).
- `nextdns:<profile>`, `nextdns:no-device-info`: [NextDNS integration](https://tailscale.com/docs/integrations/nextdns).
  Be sure to set NextDNS as global resolver in the [configuration](configuration.md).
- `magicdns-aaaa`: Respond to AAAA queries on the local [MagicDNS](https://tailscale.com/docs/features/magicdns)
  resolver at 100.100.100.100.
- `disable-ipv4`: Selectively disable IPv4 for specfic nodes. This is may be useful to workaround [CGNat
  conflicts](https://tailscale.com/docs/reference/troubleshooting/network-configuration/cgnat-conflicts).
- `randomize-client-port`: Allocate a [random port for WireGuard
  traffic](https://tailscale.com/docs/reference/syntax/policy-file#randomizeclientport) instead of the static default
  port 41641.
- `disable-captive-portal-detection`: [Disable automatic captive portal
  detection](https://tailscale.com/docs/integrations/captive-portals#disable-captive-portal-detection).

```json title="policy.json"
{
  "nodeAttrs": [
    {
      // Enable MagicDNS AAAA records for all nodes
      "target": ["*"]
      "attr": ["magicdns-aaaa"]
    }
  ]
}
```

## Network-wide policy options

The following options are applied for the entire tailnet. Consider [node attributes](#node-attributes) for a more
fine-grained configuration instead.

- `randomizeClientPort`: Allocate a [random port for WireGuard
  traffic](https://tailscale.com/docs/reference/syntax/policy-file#randomizeclientport) instead of the static default
  port 41641.

```json title="policy.json"
{
  // Use a random WireGuard port for the entire tailnet
  "randomizeClientPort": true
}
```

\[^1\]: StealthScale also allows to store the policy in the database. This is typically only required in case a [web
interface](integration/web-ui.md) is used.

\[^2\]: Other key-only node attributes can be used as well. Find them in the client source code with `grep -E '^\s+NodeAttr\w+' tailcfg/tailcfg.go` or by using [GitHub code search (requires
login)](https://github.com/search?q=repo%3Atailscale%2Ftailscale%20language%3Ago%20path%3Atailcfg%2Ftailcfg.go%20symbol%3A%2FNodeAttr%5Cw%2B%2F&type=code).
