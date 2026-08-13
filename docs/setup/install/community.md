# Community packages

Several Linux distributions and community members provide packages for stscale. Those packages may be used instead of
the [official releases](official.md) provided by the stscale maintainers. Such packages offer improved integration
for their targeted operating system and usually:

- setup a dedicated local user account to run stscale
- provide a default configuration
- install stscale as system service

!!! warning "Community packages might be outdated"

    The packages mentioned on this page might be outdated or unmaintained. Use the [official releases](official.md) to
    get the current stable version or to [test pre-releases](main.md).

    [![Packaging status](https://repology.org/badge/vertical-allrepos/stscale.svg)](https://repology.org/project/stscale/versions)

## Arch Linux

Arch Linux offers a package for stscale, install via:

```shell
pacman -S stscale
```

## Fedora, RHEL, CentOS

A third-party repository for various RPM based distributions is available at:
<https://copr.fedorainfracloud.org/coprs/jonathanspw/stscale/>. The site provides detailed setup and installation
instructions.

## Nix, NixOS

A Nix package is available as: `stscale`. See the [NixOS package site for installation
details](https://search.nixos.org/packages?show=stscale).

## Gentoo

```shell
emerge --ask net-vpn/stscale
```

Gentoo specific documentation is available [here](https://wiki.gentoo.org/wiki/User:Maffblaster/Drafts/StealthScale).

## OpenBSD

StealthScale is available in ports. The port installs stscale as system service with `rc.d` and provides usage
instructions upon installation.

```shell
pkg_add stscale
```
