#!/usr/bin/env python3
"""
Daily push — commits locally staged changes and pushes once per day.
Called daily at 02:00 UTC by stealthscale-daily-push (hy3).
Ensures only one push per day, aggregates local commits.
"""
import subprocess
from pathlib import Path
import datetime

ROOT = Path(__file__).resolve().parents[1]

def run(cmd, cwd=ROOT):
    res = subprocess.run(cmd, cwd=cwd, capture_output=True, text=True)
    return res

def main():
    print(f"[{datetime.datetime.now().isoformat()}] daily push check")
    # check git status
    st = run(["git","status","--porcelain"])
    # check if there's anything to commit (including untracked that should be committed? we follow typical flow)
    diff = run(["git","diff","--quiet"])
    # git diff --quiet returns 0 if no diff, 1 if diff
    # We want to see if there are changes to commit
    # Check staged + unstaged + untracked (but respect .gitignore)
    # First add all tracked changes, but not arbitrary large - we commit what workers committed
    log = run(["git","log","--oneline","origin/main..HEAD"]).stdout.strip()
    if log:
        print(f"Local commits ahead of origin/main:\n{log}")
    else:
        print("No local commits ahead of origin/main")
        # check if there are uncommitted changes we should commit
        status = run(["git","status","--porcelain"]).stdout.strip()
        if status:
            print(f"Uncommitted changes:\n{status}")
            # auto-commit as daily hygiene
            run(["git","add","-A"])
            commit = run(["git","commit","-m",f"chore: daily auto-commit {datetime.datetime.now().date()} [skip ci]"])
            print(commit.stdout)
            print(commit.stderr)
            log = run(["git","log","--oneline","origin/main..HEAD"]).stdout.strip()
        else:
            print("Nothing to commit, nothing to push — skipping")
            return
    # Now push if we have commits
    if not run(["git","log","--oneline","origin/main..HEAD"]).stdout.strip():
        print("No commits to push after hygiene")
        return
    # push
    print("Pushing to origin/main...")
    # ensure remote uses PAT (already via credential helper)
    push = run(["git","push","origin","main"])
    print(push.stdout)
    print(push.stderr)
    if push.returncode == 0:
        print("Push successful")
    else:
        print(f"Push failed code {push.returncode}")

if __name__ == "__main__":
    main()
