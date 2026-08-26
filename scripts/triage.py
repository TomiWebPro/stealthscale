#!/usr/bin/env python3
"""
StealthScale Triage — cost-saving hy3-style classifier for local issues.
Scans issues/open/*.md, decides hy3 vs spark routing based on labels/title/body heuristics,
moves to in-progress, and spawns one-shot scheduler worker jobs.

Usage: python3 scripts/triage.py
Called by scheduler job stealthscale-triage (every 5m) with hy3 model.
For pure non-LLM fast path, this script already implements the routing deterministically.
If scheduler prompt invokes an LLM, this script can be called as a tool.
"""
import re
import subprocess
import shutil
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
OPEN = ROOT / "issues/open"
INPROG = ROOT / "issues/in-progress"
SCHED = Path.home() / "projects/opencode-scheduler/scheduler.py"

NON_CODE_LABELS = {"docs","documentation","question","chore","mkdocs","readme"}
CODE_LABELS = {"bug","feature","stealth","vless","derp","webui","xray","policy","auth","api","hscontrol"}

def parse_frontmatter(p: Path):
    text = p.read_text()
    m = re.match(r'^---\s*\n(.*?)\n---\s*\n(.*)', text, re.DOTALL)
    if not m:
        return {}, text
    fm_raw, body = m.group(1), m.group(2)
    fm = {}
    # naive parse
    for line in fm_raw.splitlines():
        if ":" in line:
            k,v = line.split(":",1)
            fm[k.strip()] = v.strip()
    # labels special
    labels = fm.get("labels","")
    # extract list like ["docs"] or docs
    lbls = []
    if "[" in labels:
        lbls = [x.strip().strip('"').strip("'").lower() for x in labels.strip("[]").split(",") if x.strip()]
    else:
        lbls = [labels.strip().lower()] if labels else []
    fm["_labels"] = [l for l in lbls if l]
    fm["_body"] = body
    return fm, text

def classify(p: Path):
    fm,_ = parse_frontmatter(p)
    labels = set(fm.get("_labels",[]))
    title = fm.get("title","").lower()
    body = fm.get("_body","").lower()
    # non-code signals
    if labels & NON_CODE_LABELS:
        # if also code labels, code wins
        if labels & CODE_LABELS:
            return "spark", "both label sets, code takes precedence"
        return "hy3", f"labels {labels} -> non-code"
    if any(k in title for k in ["docs","documentation","readme","mkdocs"]):
        if not any(k in body for k in ["hscontrol","vless","derp","xray","go","webui"]):
            return "hy3", "title suggests docs only"
    if labels & CODE_LABELS:
        return "spark", f"labels {labels} -> code"
    if any(kw in body for kw in ["hscontrol","xray","vless","derp","webui","go test","config.go","mapper"]):
        return "spark", "body mentions code areas"
    # default hy3 for question/chore else spark for unknown feature/bug
    if "feature" in labels or "bug" in labels:
        return "spark", "default code label"
    return "hy3", "default non-code"

def ensure_inprog():
    INPROG.mkdir(parents=True, exist_ok=True)

def spawn_worker(issue_path: Path, route: str):
    slug = issue_path.stem
    job_name = f"issue-{slug}"
    # choose model/agent
    if route == "hy3":
        model = "opencode/hy3-free"
        agent = "stealth-docs"
        prompt_template = (f"Read ~/projects/stealthscale/prompts/README.md and ~/projects/stealthscale/prompts/docs-product.md and implement docs for localized issue {slug} at ~/projects/stealthscale/issues/in-progress/{slug}.md using hy3 (non-code, cost saving). Follow prompts/docs-product.md acceptance: mkdocs, README, docs/ref/xray-vless.md, docs/usage/webui.md. Also read prompts/triage-guide.md for routing context. Work dir ~/projects/stealthscale, commit locally (git commit), append '<!-- status: done -->' when finished. DO NOT PUSH.")
    else:
        model = "opencode/muse-spark-1.2-contributor-free"
        agent = "stealth-builder"
        if "webui" in slug.lower():
            agent = "stealth-webui"
        prompt_template = (f"Read ~/projects/stealthscale/prompts/README.md and issue ~/projects/stealthscale/issues/in-progress/{slug}.md. Then read the relevant prompt(s) in ~/projects/stealthscale/prompts/: vless-reality-xtls.md (VLESS+Reality_XTLS default), derp-stealth-fallback.md (DERP gated by stealth), unified-server-client.md (single codebase), webui-headscale.md (headscale-ui style embedded), tests-config.md (tests & config). Choose the prompt(s) matching the issue labels/title (e.g. vless->vless-reality-xtls, webui->webui-headscale, docs->docs-product). Implement the required code/tests/configs as described in that prompt, with acceptance criteria. Run go test for affected packages and make build, commit locally (git commit), append '<!-- status: done -->' when finished. Work dir ~/projects/stealthscale, DO NOT PUSH (daily-push will push at 02:00 UTC). Use muse-spark intelligent.")
    # Check if job already exists
    try:
        out = subprocess.run([str(SCHED), "list"], capture_output=True, text=True, timeout=10)
        if job_name in out.stdout:
            print(f"worker {job_name} already exists, skipping spawn")
            return
    except Exception as e:
        print(f"list check failed: {e}")
    cmd = [
        str(SCHED), "add",
        "--name", job_name,
        "--prompt", prompt_template,
        "--dir", str(ROOT),
        "--once",
        "--agent", agent,
        "--model", model,
        "--auto",
        "--timeout", "1200"
    ]
    print(f"Spawning worker: {' '.join(cmd)}")
    res = subprocess.run(cmd, capture_output=True, text=True, timeout=30)
    print(res.stdout)
    print(res.stderr, flush=True)

def main():
    ensure_inprog()
    open_files = sorted(OPEN.glob("*.md"))
    # ignore README.md
    open_files = [p for p in open_files if p.name.lower() != "readme.md"]
    if not open_files:
        print("No open issues")
        return
    for p in open_files:
        route, reason = classify(p)
        print(f"ISSUE: {p.name} -> ROUTE: {route} REASON: {reason}")
        dest = INPROG / p.name
        try:
            shutil.move(str(p), str(dest))
            print(f"Moved {p.name} to in-progress")
        except Exception as e:
            print(f"move failed {p.name}: {e}")
            continue
        spawn_worker(dest, route)

if __name__ == "__main__":
    main()
