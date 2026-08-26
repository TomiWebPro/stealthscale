# Localised Issue Tracking

This directory implements **local file-based issue tracking** for StealthScale automation.
The scheduler daemon watches these folders and drives the agentic system.

## Folders

| Folder | Purpose |
|--------|---------|
| `issues/open/` | New issues — create a markdown file here to open an issue. Filename = issue slug. |
| `issues/in-progress/` | Issues being worked on (automation moves files here when assigned) |
| `issues/closed/` | Finished issues — automation moves files here when done. |
| `issues/triage/` | Unclassified issues awaiting triage decision (optional staging) |

## Issue File Format

Each issue is a markdown file with YAML frontmatter:

```markdown
---
title: "Fix VLESS reality handshake"
labels: ["bug", "stealth"]
priority: high
assignee: ""
created: 2026-08-27T00:00:00Z
---

Describe the issue here. The automation reads `labels`, `title`, and body to decide:
- `labels` containing `docs`, `question`, `chore` → non-code → handled by hy3 (cost saving)
- `labels` containing `bug`, `feature`, `stealth`, `vless`, `derp`, `webui` → code editing → handled by muse-spark
- No labels → triage agent decides.
```

## Automation Flow

```
open/ ---> triage (hy3, every 5m) ---> in-progress ---> closed/
               |                              |
               +--> hy3 (docs/chore)          +--> spark (code)
                    (direct to closed)             (via spark agent)
```

- **Triage scheduler job** (`stealthscale-triage`, every 5m, hy3): scans `open/`, classifies, moves to `in-progress/` and spawns appropriate worker job.
- **Worker jobs**: one-shot scheduler jobs named `issue-<slug>` that use the chosen model/agent.
- **Closer** (`stealthscale-closer`, every 10m): checks `in-progress/` for completed work (looks for `<!-- status: done -->` marker or successful run history) and moves to `closed/`.

## Manual Usage

```bash
# Open an issue
cat > issues/open/fix-derp-stealth.md <<'ISSUE'
---
title: "DERP fallback must respect stealth"
labels: ["bug", "stealth"]
priority: high
---
Ensure DERP fallback is only used when stealth checks pass...
ISSUE

# Check status
ls issues/open/ issues/in-progress/ issues/closed/

# Close manually
mv issues/open/my-issue.md issues/closed/my-issue.md
```

All scheduler jobs log to `~/projects/opencode-scheduler/logs/` and are visible via `scheduler.py runs` / webui at http://127.0.0.1:8788
