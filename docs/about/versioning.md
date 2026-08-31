# Versioning

This page is the **source of truth** for StealthScale version strings. Future agents, release automation, and humans must follow it exactly — especially when deciding whether a binary is `isDev` (must not write `database_versions`) or enforces the one-minor upgrade path.

`GetVersionInfo().Version` comes from `debug.ReadBuildInfo.Main.Version` (the goreleaser tag or Go pseudo-version). `hscontrol/db/versioncheck.go:parseVersion` strips prerelease and build metadata (`-xxx`, `+yyy`) and parses `MAJOR.MINOR.PATCH`. `isDev` decides if the version is a development build that skips the DB version check.

## The four channels

| Channel | Git tag / version string | Example | `isDev` | Writes `database_versions`? | Goreleaser `prerelease` | Docker tag | APT repo | Upgrade guard |
|---|---|---|---|---|---|---|---|
| **dev** (local) | none — `make build` | `dev` or `v0.0.1-5-g944d522-dirty` | **yes** | no (preserves stored version) | n/a (snapshot `-next`) | none | none | skipped |
| **nightly** | `vX.Y.Z-nightly.YYYYMMDD` or snapshot `vX.Y.Z-next` / pseudo `v0.0.0-YYYYMMDDHHMMSS-abc123` | `v0.0.1-nightly.20260831+g944d522` <br>snapshot: `v0.0.1-next` | **yes** | **no** | yes (if tagged) / n/a (snapshot) | `ghcr.io/tomiwebpro/stealthscale:nightly` <br>`unstable` | `unstable` | skipped |
| **alpha / beta / rc** | `vX.Y.Z-alpha.N` `vX.Y.Z-beta.N` `vX.Y.Z-rc.N` | `v0.0.1-alpha.1` <br>`v0.0.1-rc.1` | **no** | **yes** (`X.Y.Z` core) | yes (`prerelease: auto`) | `ghcr.io/...:alpha`, `sha-...` | `unstable` | enforces `MINOR` of core (strips prerelease) |
| **production** | `vX.Y.Z` | `v0.0.1` `v1.0.0` | **no** | **yes** | no | `ghcr.io/...:stable`, `latest`, `v0.0.1`, `sha-...` | `stable` | enforces one-minor |

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
main  ──► nightly (daily 02:00 UTC, schedule) ──► alpha tag ──► rc/beta ──► prod tag
         snapshot -next (on every push)        v0.0.1-alpha.1   v0.0.1-rc.1  v0.0.1
```

### Nightly

- **Trigger:** GitHub Actions `schedule: cron '0 2 * * *'` on `main` plus `push` to `main` snapshot. No git tag; `goreleaser build --snapshot` produces `v<latest-tag>-next` (or nightly workflow creates tag `vNEXT-nightly.YYYYMMDD` and runs `goreleaser release --clean` with `prerelease:true`).
- **Version examples:** `v0.31.0-nightly.20260831`, `v0.31.0-nightly.20260831+g54add2b`, or local pseudo `v0.0.0-20260831120000-54add2b123456`
- **Artifacts:** Linux `stscale` binary, `stscale_*_linux_*.deb`, `ghcr.io/tomiwebpro/stealthscale:nightly` / `unstable` / `sha-…`
- **DB:** `isDev=true` → never writes `database_versions`, never blocks upgrade. Safe to run against a copy of prod DB for testing.
- **Purpose:** First Linux binary testing, integration, stealth verification. **Do not use as production control plane.**

### Alpha / Beta / RC

- **Trigger:** manual `git tag v0.31.0-alpha.1 && git push origin v0.31.0-alpha.1` (alpha), `-beta.1`, `-rc.1`.
- **Goreleaser:** `prerelease: auto` marks it as GitHub prerelease. Changelog auto-grouped, `stable: false` docker tags (`stable` not pushed).
- **DB:** `isDev=false` → writes `database_versions` as core `0.31.0` after migrations. Upgrade from previous stable `0.30.x` to alpha is `minorDiff==1` → allowed; alpha to `0.31.0` stable is `minorDiff==0` → allowed. Downgrade alpha→stable not blocked (same core), but `0.31.0`→`0.30.x` is blocked.
- **Purpose:** Test the migration path and breaking changes with real users on `unstable` repo.

### Production

- **Trigger:** `git tag v0.31.0 && git push origin v0.31.0` (no prerelease suffix).
- **Goreleaser:** `prerelease: auto` → `false`, `draft: true` (then undrafted manually), `latest`/`stable` docker tags, `stable` APT.
- **DB:** `isDev=false` → stores exact `v0.31.0`, enforces one-minor path: `0.29.x→0.30.0` allowed, `0.28→0.30` blocked with message to upgrade to `0.29.x` first.
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

### Nightly (automated)

```bash
# No tag needed — CI does on schedule:
# .github/workflows/nightly.yml → goreleaser build --snapshot  → upload to :nightly
# Manual nightly tag (if you want a GitHub prerelease):
git tag v0.31.0-nightly.20260831
git push origin v0.31.0-nightly.20260831  # goreleaser → prerelease, ghcr :nightly
```

### Alpha

```bash
# Bump next minor, create prerelease tag
git tag v0.31.0-alpha.1
git push origin v0.31.0-alpha.1
# goreleaser → GitHub prerelease, ghcr :alpha, apt unstable
# Test upgrade: restore 0.30.0 dump, run alpha binary, check migrations
```

### Production

```bash
# 1. Update CHANGELOG.md: ## 0.31.0 (2026-09-01) with breaking notes
# 2. Update docs/about/versioning.md if convention changes
# 3. Commit, then tag
git tag v0.31.0
git push origin v0.31.0
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
