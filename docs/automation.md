# Automation Agentic System

StealthScale development is automated via **opencode-scheduler** + **localized file issues** + **multi-agent routing**. This document explains how to use it — for humans and for agents that bootstrap.

## TL;DR for Operators

```bash
# Issues are local markdown files
ls issues/open/ issues/in-progress/ issues/closed/

# Open an issue: create a markdown file in open/
cat > issues/open/fix-derp-stealth.md <<'ISSUE'
---
title: "DERP fallback must respect stealth"
labels: ["bug", "stealth"]
priority: high
---
Ensure DERP fallback is only used when stealth checks pass...
ISSUE

# Automation does the rest:
# 1. Triage (every 5m, hy3) classifies and spawns worker
# 2. Worker (spark for code, hy3 for docs) implements, commits locally, marks <!-- status: done -->
# 3. Closer (every 10m) moves done to closed/
# 4. Daily push (02:00 UTC) pushes aggregated commits once per day

# Inspect scheduler
python3 ~/projects/opencode-scheduler/scheduler.py list
python3 ~/projects/opencode-scheduler/scheduler.py runs -v
python3 ~/projects/opencode-scheduler/scheduler.py journal --tail 20

# WebUI at http://127.0.0.1:8788 (scheduler) and http://<stealthscale>:8080/web (stealthscale)
```

## Architecture

```
issues/open/*.md
       |
       v
stealthscale-triage (every 5m, hy3-free, stealth-triage agent)
  reads prompts/triage-guide.md + prompts/README.md
  -> ROUTE: hy3 (docs) or spark (code)
  -> moves to issues/in-progress/
  -> scheduler.py add --name issue-<slug> --once --agent <stealth-docs|stealth-builder|stealth-webui> --model <hy3|spark>
       |
       v
Worker job (one-shot)
  prompt cites prompts/<relevant>.md (e.g. vless-reality-xtls.md, webui-headscale.md, docs-product.md)
  -> edits code or docs, runs go test / make test, commits locally (git commit), appends <!-- status: done -->
       |
       v
stealthscale-closer (every 10m, hy3)
  moves <!-- status: done --> to issues/closed/
       |
       v
stealthscale-daily-push (0 2 * * *, hy3)
  git push origin main once per day (if origin/main..HEAD non-empty)
```

## Localised Issue Folders

See `issues/README.md` for full spec. Short version:

| Folder | Purpose |
|--------|---------|
| `issues/open/` | New issues — filename is slug, frontmatter has title/labels/priority |
| `issues/in-progress/` | Assigned, being worked |
| `issues/closed/` | Done, with `<!-- status: done -->` marker |
| `issues/triage/` | Optional staging |

Frontmatter:
```yaml
---
title: "Default to VLESS+Reality_XTLS"
labels: ["feature", "stealth", "vless"]
priority: high
created: 2026-08-27T00:00:00Z
---
Body...
```

Labels decide routing: `docs|chore|question` → hy3, `bug|feature|stealth|vless|derp|webui` → spark. See `prompts/triage-guide.md`.

## Multi-Agent System

Agents know how to get things going — see `~/.config/opencode/agents/` and `.opencode/agents/`:

| Agent | Model | Mode | When |
|-------|-------|------|------|
| `stealth-triage` | `opencode/hy3-free` | primary | Classifies issues, runs triage/closer/daily-push |
| `stealth-docs` | `opencode/hy3-free` | primary | Docs, mkdocs, README (cost saving) |
| `stealth-builder` | `opencode/muse-spark-1.2-contributor-free` | primary | Code: hscontrol, xray, derp, config, tests |
| `stealth-webui` | `opencode/muse-spark-1.2-contributor-free` | primary | WebUI headscale-ui style |
| `stealth-orchestrator` | `opencode/muse-spark-1.2-contributor-free` | primary | Knows all bootstrap (scheduler daemon, skills, issues) |

For anything **not code editing** (docs, triage, closer, daily-push) → **hy3** (cheap). For **issue → code editing** → **muse-spark** (intelligent).

## Prompts Catalog

Instead of coding directly, orchestrator guides coders via prompts in `prompts/`:

- `prompts/vless-reality-xtls.md` — default transport
- `prompts/derp-stealth-fallback.md` — DERP gated by stealth
- `prompts/unified-server-client.md` — single codebase
- `prompts/webui-headscale.md` — headscale-ui style WebUI
- `prompts/tests-config.md` — tests + config
- `prompts/docs-product.md` — product docs
- `prompts/triage-guide.md`, `prompts/daily-push.md` — automation chores

Each prompt is **self-contained** (no prior context), lists files, acceptance criteria, and test commands (`go test`, `make build`, `make test`, `prek run`).

## Scheduler as Global Skill

Skill: `~/.config/opencode/skills/scheduling/SKILL.md` (global, name `scheduling`). Use:

```bash
python3 ~/opencode-scheduler/scheduler.py list
python3 ~/opencode-scheduler/scheduler.py add --name my-task --prompt "..." --every 30m --auto --model opencode/hy3-free
python3 ~/opencode-scheduler/scheduler.py run-now stealthscale-triage
python3 ~/opencode-scheduler/scheduler.py runs -v
```

Daemon autostarts at boot: `~/.config/systemd/user/opencode-scheduler.service` + `opencode-scheduler-webui.service` via `loginctl enable-linger tomi` and `WantedBy=default.target`. Symlink `~/opencode-scheduler → ~/projects/opencode-scheduler` and `~/bin/ocsched` on PATH.

## Git & Push Policy

- Local commits: workers `git commit` locally without pushing (agent prompt says `DO NOT PUSH`).
- Daily push: `scripts/daily_push.py` at 02:00 UTC does `git log origin/main..HEAD` and `git push origin main` if ahead. That's **once per day**.
- Manual push: `gh` and `git` credential helper via `~/.git-credentials` with a stored PAT (see ~/.git-credentials) and user `TomiWebPro <TomiWebPro@gmail.com>`. Passwordless sudo enabled.

Check: `git status`, `git log --oneline origin/main..HEAD`, `systemctl --user status opencode-scheduler`.

## Product: Server & Client Unified, VLESS+Reality_XTLS, DERP Stealth, WebUI

- **No difference server/client:** single `hscontrol/xray` package, `XRayConfig` shared, `hscontrol/webui` embedded both sides (see `prompts/unified-server-client.md`).
- **Default transport:** `xray.security=reality_xtls` (VLESS+Reality via XTLS+uTLS, `utls_fingerprint: chrome`), not `none`. See `prompts/vless-reality-xtls.md`, `config-example.yaml`.
- **DERP fallback:** `hscontrol/stealth/stealth.go` `Checker.FilterDERPMap()` — only include DERP if stealth satisfied, else fail-closed (empty DERPMap). See `prompts/derp-stealth-fallback.md`.
- **WebUI:** `hscontrol/webui/` embedded frontend, dark theme like scheduler, tabs Nodes/Users/Keys/Policy/DERP/VLESS/Health, APIs `/web/api/*`, served at `/web` and `/admin` both server and client (see `prompts/webui-headscale.md` and `docs/usage/webui.md`).

## Tests, Configurations, Rest Components

Coders are guided to code via `prompts/tests-config.md`:

```bash
go test ./hscontrol/xray -v
go test ./hscontrol/stealth -v
go test ./hscontrol/webui -v
go test ./hscontrol/types -run TestXRay -v
make test
make build
make lint
prek run --all-files
```

Configs: `config-example.yaml` (`xray.enabled:true`, `reality_xtls`), `hscontrol/types/config.go`, `mkdocs.yml` nav, `docs/ref/xray-vless.md`, `docs/stealthscale/install.md`.

## How to Bootstrap (for orchestrator agent)

If a new machine or session asks "how to get things going":

1. Verify git: `git --version; git config --global user.name "TomiWebPro"`
2. Verify scheduler daemon: `systemctl --user status opencode-scheduler` else `systemctl --user start opencode-scheduler && systemctl --user enable opencode-scheduler` (linger already).
3. Verify webui: `curl -s http://127.0.0.1:8788/api/jobs`
4. Verify skill: `ls ~/.config/opencode/skills/scheduling/SKILL.md` and `cat ~/opencode-scheduler/SKILL.md`
5. Verify agents: `ls ~/.config/opencode/agents/ && cat ~/.config/opencode/opencode.jsonc`
6. Verify issues: `ls issues/open/`
7. List jobs: `python3 ~/projects/opencode-scheduler/scheduler.py list`
8. Trigger triage now: `python3 ~/projects/opencode-scheduler/scheduler.py run-now stealthscale-triage` (if not disabled).

All that is in `~/.config/opencode/agents/stealth-orchestrator.md`.

## Cost Model

- Hy3 for triage, docs, closer, daily-push, docs-check — cheap, fast.
- Spark for xray, derp, webui, unified, tests — intelligent, thorough.

Do not use spark for pure docs; do not use hy3 for code.

## Logs & Monitoring

- Scheduler DB: `~/projects/opencode-scheduler/scheduler.db` (`sqlite3 scheduler.db 'select * from runs order by id desc limit 10;'`)
- Logs: `~/projects/opencode-scheduler/logs/activity.jsonl` and `logs/journal.md` and `logs/<job>-<ts>.log`
- Journal: `python3 ~/projects/opencode-scheduler/scheduler.py journal`
- Docker-ish: `ps aux | grep scheduler`, `systemctl --user status opencode-scheduler`

## Next Steps

Pick an `issues/open/*.md` and follow its prompt file — e.g. `prompts/webui-headscale.md` for the WebUI issue. The scheduler will auto-spawn workers, but you can also manually run:

```bash
opencode run "Read prompts/webui-headscale.md and implement it for issue example-webui-headscale at issues/in-progress/example-webui-headscale.md" --dir ~/projects/stealthscale --agent stealth-webui -m opencode/muse-spark-1.2-contributor-free --auto
```

Then `git commit` locally, mark done, let closer archive, and daily-push will push at 02:00 UTC.
