# Versioning

This page is the **source of truth** for StealthScale version strings. Future agents, release automation, and humans must follow it exactly — especially when deciding whether a binary is `isDev` (must not write `database_versions`) or enforces the one-minor upgrade path.

`GetVersionInfo().Version` comes from `debug.ReadBuildInfo.Main.Version` (the goreleaser tag or Go pseudo-version). `hscontrol/db/versioncheck.go:parseVersion` strips prerelease and build metadata (`-xxx`, `+yyy`) and parses `MAJOR.MINOR.PATCH`. `isDev` decides if the version is a development build that skips the DB version check.

## The three channels

| Channel | Git tag / version string | Example | `isDev` | Writes `database_versions`? | Goreleaser `prerelease` | Docker tag | APT repo | Upgrade guard |
|---|---|---|---|---|---|---|---|
| **dev** (local) | none — `make build` | `dev` or `v0.0.1-5-g944d522-dirty` | **yes** | no (preserves stored version) | n/a (snapshot `-next`) | none | none | skipped |
| **nightly** | `v0.0.1-nightly` (floating, moves daily) | `v0.0.1-nightly` → `v0.0.1-nightly+g944d522` <br>snapshot: `v0.0.1-next` | **yes** | **no** | yes (`prerelease: auto`) | `ghcr.io/tomiwebpro/stealthscale:nightly` <br>`unstable` | `unstable` | skipped |
| **alpha** | `v0.0.1-alpha` (floating, consolidates `v0.0.1-alpha.1..4`) | `v0.0.1-alpha` (was `alpha.1..4` @ df5849c..ac2cd9d) | **no** | **yes** (`0.0.1` core) | yes (`prerelease: auto`) | `ghcr.io/...:alpha`, `sha-...` | `unstable` | enforces `MINOR` of core |
| **stable** | `v0.0.1` (floating stable) | `v0.0.1` → `v0.0.1` <br>`v1.0.0` | **no** | **yes** | no | `ghcr.io/...:stable`, `latest`, `v0.0.1`, `sha-...` | `stable` | enforces one-minor |

> **Fresh start (2026-08-31):** StealthScale restarts versioning at `v0.0.1` — not a small Headscale fork. Headscale `0.29.x/0.30.0` history is archived in `CHANGELOG.md`. New installs start at `0.0.1`; upgrades from Headscale DBs are not supported without manual migration.

### What counts as `isDev`

`hscontrol/db/versioncheck.go:isDev` returns true for:

- `""`, `"dev"`, `"(devel)"`
- any string containing `dirty`, `-next`, `nightly`, `.nightly`, or `-g` (git-describe commits-ahead, e.g. `v0.30.0-5-g54add2b`)
- Go pseudo-versions `v0.0.0-20260831120000-abc123` (`pseudoVersionTime` match)

Everything else — `v0.30.0`, `v0.31.0-alpha.1`, `v0.31.0-rc.1` — is **not** dev and **does** participate in version tracking.

!!! warning "Stripping"
    `parseVersion` strips everything after `-` and `+` before comparing `MAJOR.MINOR.PATCH`. So `v0.31.0-nightly.20260831`, `v0.31.0-alpha.1`, and `v0.31.0` all parse as `0.31.0`. The upgrade gate therefore sees nightly and alpha as the *same* core version as their future stable. This is intentional: nightly never writes, alpha does.

## SemVer

We use **Semantic Versioning 2.0.0**: `vMAJOR.MINOR.PATCH[-PRERELEASE][+BUILD]`

- **MAJOR** `0` while pre-`1.0` (`0.0`, `0.1`). `1.0.0` will be first stable major; major bumps are breaking (see below).
- **MINOR** increments for features and may contain breaking changes while `MAJOR=0` (documented in `CHANGELOG.md` `### BREAKING`). Upgrade path enforces **one minor at a time** (`hscontrol/db/versioncheck.go:checkVersionUpgradePath`). First StealthScale release is `0.0.1`.
- **PATCH** increments for bug fixes, always allowed within same minor (`minorDiff==0`).
- **PRERELEASE** is `-alpha.N`, `-beta.N`, `-rc.N`, or `-nightly.YYYYMMDD` (nightly is dev-only despite containing it). Build metadata `+gSHA` may be appended by goreleaser.

## Branching to channels

```
main  ──► v0.0.1-nightly (daily 02:00 UTC) ──► v0.0.1-alpha (floating) ──► v0.0.1 (stable)
         snapshot -next (on every push)        (was alpha.1..4)          v0.1.0 next
```

> **Consolidation 2026-09-02:** `v0.0.1-alpha.1..4` squashed into single `v0.0.1-alpha` (see `git tag --list`). Channels are now `v0.0.1-nightly`/`v0.0.1-alpha`/`v0.0.1` as only three tags. Use `git tag -f v0.0.1-alpha && git push -f origin v0.0.1-alpha` to move alpha.

### Nightly

- **Trigger:** `git tag -f v0.0.1-nightly && git push -f origin v0.0.1-nightly` (floating, moves daily) or `schedule: cron '0 2 * * *'` on `main`; `push` to `main` snapshot `goreleaser build --snapshot` → `v<latest-tag>-next`.
- **Version examples:** `v0.0.1-nightly` / `v0.0.1-nightly+g944d522`, or local pseudo `v0.0.0-20260902120000-54add2b123456`
- **Artifacts:** Linux `stscale` binary, `stscale_*_linux_*.deb`, `ghcr.io/tomiwebpro/stealthscale:nightly` / `unstable` / `sha-…`
- **DB:** `isDev=true` → never writes `database_versions`, never blocks upgrade. Safe to run against a copy of prod DB for testing.
- **Purpose:** First Linux binary testing, integration, stealth verification. **Do not use as production control plane.**

### Alpha

- **Trigger:** `git tag -f v0.0.1-alpha && git push -f origin v0.0.1-alpha` (floating, consolidates former `v0.0.1-alpha.1..4`).
- **Goreleaser:** `prerelease: auto` → true, GitHub prerelease, `ghcr.io/...:alpha` / `unstable`, `sha-...`.
- **DB:** `isDev=false` → writes `database_versions` as `0.0.1` core.
- **Purpose:** Test the migration path and breaking changes with real users on `unstable` repo.

### Stable

- **Trigger:** `git tag -f v0.0.1 && git push -f origin v0.0.1` (+ `v0.1.0` for next minor). Release workflow triggers on `v*`.
- **Goreleaser:** `prerelease: auto` → `false`, `draft: true` (then undrafted), `latest`/`stable` docker tags, `stable` APT.
- **DB:** `isDev=false` → stores exact `v0.0.1`, enforces one-minor path.
- **CHANGELOG:** Must have `## X.Y.Z (YYYY-MM-DD)` entry with `### BREAKING` if needed (see `docs/setup/upgrade.md`).

### Dev / Snapshot (`-next`, `+gSHA`, `-dirty`)

- **Trigger:** `make build` (produces `dev` or `...-dirty` via `vcs.modified`), `goreleaser build --snapshot --clean` (produces `v0.30.0-next`), or `git describe` ahead of tag.
- **DB:** `isDev=true` → no write, no gate. Running `make build` against a staging DB will warn `database version check is skipped` and preserve stored version.

## Upgrade path and `database_versions`

`hscontrol/db/versioncheck.go:checkVersionUpgradePath` enforces:

- same `MAJOR` required (major bump not yet supported, will be after `1.0.0`)
- `minorDiff == 0` → patch upgrade, always allowed
- `minorDiff == 1` → one-minor upgrade, allowed
- `minorDiff > 1` → **blocked** (must upgrade one minor at a time)
- `minorDiff < 0` → **blocked** (no downgrade)

`isDev` versions **skip** the check and **do not** update `database_versions`. `snapshot.version_template: "{{ .Tag }}-next"` in `.goreleaser.yml:167` therefore never poisons the DB (fixed in `nightly-blocker-version-poisoning-dirty-next`).

## How to cut each release

### Nightly (floating)

```bash
git tag -f v0.0.1-nightly && git push -f origin v0.0.1-nightly  # moves nightly
# goreleaser → ghcr :nightly / :unstable / sha-... (prerelease, isDev=true)
```

### Alpha (floating, replaces alpha.1..4)

```bash
git tag -f v0.0.1-alpha && git push -f origin v0.0.1-alpha  # consolidates 4 tags
# goreleaser → GitHub prerelease, ghcr :alpha / :unstable, apt unstable
```

### Stable (floating)

```bash
# 1. Update CHANGELOG.md: ## 0.1.0 (2026-09-02) with breaking notes
# 2. Update docs/about/versioning.md if convention changes
# 3. Commit, then tag
git tag -f v0.0.1 && git push -f origin v0.0.1
git tag v0.1.0 && git push origin v0.1.0  # optional next minor
# goreleaser → stable release, ghcr :stable/:latest, apt stable
# 4. Verify stscale nodes vless 1 identical before/after
```

## For future agents

- **Never** change `parseVersion` stripping without updating `isDev` and this doc.
- **Never** add a new prerelease label (e.g. `-canary`) without adding it to `isDev` decision (is it dev or release?).
- **Never** reorder or delete migration IDs; versioning and migrations jointly enforce the upgrade path.
- **Always** update `docs/about/releases.md` and `CHANGELOG.md` header when adding a channel.
- **Always** keep `.goreleaser.yml:prerelease: auto` and `snapshot.version_template` aligned with this doc.
- When in doubt: nightly = dev, alpha/beta/rc = release prerelease, prod = release. If `isDev` is wrong, you will poison `database_versions` and brick the one-minor guard.

## References

- Implementation: `hscontrol/types/version.go` (`GetVersionInfo`), `hscontrol/db/versioncheck.go` (`isDev`, `parseVersion`, `checkVersionUpgradePath`, `setDatabaseVersion`), `hscontrol/db/db.go:runMigrations`, `.goreleaser.yml:8,167`
- Historical fix: `issues/closed/nightly-blocker-version-poisoning-dirty-next.md`
- Upgrade docs: `docs/setup/upgrade.md`
