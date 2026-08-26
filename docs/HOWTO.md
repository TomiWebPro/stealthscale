# How to Use StealthScale (Product Guide)

This is the **good documentation** of how to use the product, for operators and for automation coders (hy3).

## 1. One-Command Bootstrap (for orchestrator agent)

```bash
# Scheduler daemon (autostarts at boot, but verify)
systemctl --user status opencode-scheduler
systemctl --user status opencode-scheduler-webui
curl -s http://127.0.0.1:8788/api/jobs | jq

# Global skill
ls ~/.config/opencode/skills/scheduling/SKILL.md
cat ~/opencode-scheduler/SKILL.md | head -20

# Issues
ls issues/open/ issues/in-progress/ issues/closed/
cat issues/README.md

# Agents
cat ~/.config/opencode/opencode.jsonc | jq .agent
ls ~/.config/opencode/agents/

# Prompts
ls prompts/

# Git push policy
git config --global user.name # TomiWebPro
git log --oneline origin/main..HEAD
python3 ~/projects/opencode-scheduler/scheduler.py list
```

If any check fails, see `docs/automation.md` bootstrap section.

## 2. Build & Config

See `docs/stealthscale/install.md` and `config-example.yaml` (defaults `reality_xtls`). No difference server/client.

## 3. WebUI

See `docs/usage/webui.md` — `http://ctl:8080/web` dark theme, headscale-ui style, embedded.

## 4. Automation

See `docs/automation.md` — localized issue open-close folders, hy3 vs spark routing, scheduler jobs.

## 5. Prompts for Coders

Instead of editing code directly, read `prompts/README.md` and follow the prompt file for your issue:

- VLESS Reality → `prompts/vless-reality-xtls.md` (spark)
- DERP Stealth → `prompts/derp-stealth-fallback.md` (spark)
- Unified → `prompts/unified-server-client.md` (spark)
- WebUI → `prompts/webui-headscale.md` (spark)
- Tests → `prompts/tests-config.md` (spark)
- Docs → `prompts/docs-product.md` (hy3)

All prompts end with `git commit` locally, `<!-- status: done -->`, DO NOT PUSH.

## 6. Daily Push

`scripts/daily_push.py` runs at 02:00 UTC via `stealthscale-daily-push` job. It commits any stray changes and `git push origin main` once. Check `logs/activity.jsonl` for `daily-push` events.

## 7. Development Workflow

See `AGENTS.md` and `docs/automation.md` for `make dev`, `make test`, `make build`, `prek`, `nix develop`.

## 8. Troubleshooting

- Scheduler not running: `systemctl --user restart opencode-scheduler`
- Issues not triaged: `python3 scripts/triage.py` manually, check `logs/activity.jsonl`
- WebUI not showing: `go test ./hscontrol/webui -v`, `curl /web/api/health`
- DERP not gating: `go test ./hscontrol/stealth -v`, `curl /web/api/derp | jq .stealth_satisfied`

## 9. Updating Docs

Docs are handled by hy3 (cheap). Create an `issues/open/docs-...md` with `labels: [docs]` and triage will spawn `stealth-docs` with `prompts/docs-product.md`.

## 10. Security

- PAT at `~/.git-credentials` (`https://TomiWebPro:<PAT>@github.com`), user `TomiWebPro`, passwordless sudo.
- Reality private keys in `config.yaml` `xray.reality.private_key` — never commit real keys.
- `tls_cert_path` vs `xray.cert_file` separate.

---
For full automation spec, read `docs/automation.md` and `prompts/README.md`. For headscale-ui style WebUI, read `docs/usage/webui.md` and `hscontrol/webui/frontend/index.html`.
