# Build from source

!!! warning "Community documentation"

    This page is not actively maintained by the stscale authors and is
    written by community members. It is _not_ verified by stscale developers.

    **It might be outdated and it might miss necessary steps**.

StealthScale can be built from source using the latest version of [Go](https://golang.org) and [Buf](https://buf.build)
(Protobuf generator). See the [Contributing section in the GitHub
README](https://github.com/tomiwebpro/stealthscale#contributing) for more information.

## OpenBSD

### Install from source

```shell
# Install prerequisites
pkg_add go git

git clone https://github.com/tomiwebpro/stealthscale.git

cd stscale

# optionally checkout a release
# option a. you can find official release at https://github.com/tomiwebpro/stealthscale/releases/latest
# option b. get latest tag, this may be a beta release
latestTag=$(git describe --tags `git rev-list --tags --max-count=1`)

git checkout $latestTag

go build -ldflags="-s -w -X github.com/tomiwebpro/stealthscale/hscontrol/types.Version=$latestTag" -X github.com/tomiwebpro/stealthscale/hscontrol/types.GitCommitHash=HASH" github.com/tomiwebpro/stealthscale

# make it executable
chmod a+x stscale

# copy it to /usr/local/sbin
cp stscale /usr/local/sbin
```

### Install from source via cross compile

```shell
# Install prerequisites
# 1. go v1.20+: stscale newer than 0.21 needs go 1.20+ to compile
# 2. gmake: Makefile in the stscale repo is written in GNU make syntax

git clone https://github.com/tomiwebpro/stealthscale.git

cd stscale

# optionally checkout a release
# option a. you can find official release at https://github.com/tomiwebpro/stealthscale/releases/latest
# option b. get latest tag, this may be a beta release
latestTag=$(git describe --tags `git rev-list --tags --max-count=1`)

git checkout $latestTag

make build GOOS=openbsd

# copy stscale to openbsd machine and put it in /usr/local/sbin
```
