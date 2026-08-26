#!/usr/bin/env python3
"""
Closer — moves finished in-progress issues to closed/ when marker present.
Called every 10m by stealthscale-closer (hy3).
"""
import shutil
from pathlib import Path
ROOT = Path(__file__).resolve().parents[1]
INPROG = ROOT / "issues/in-progress"
CLOSED = ROOT / "issues/closed"

def is_done(p: Path):
    txt = p.read_text()
    return "<!-- status: done -->" in txt or "<!-- status:done -->" in txt or "status: done" in txt.lower()[-500:]

def main():
    CLOSED.mkdir(parents=True, exist_ok=True)
    for p in sorted(INPROG.glob("*.md")):
        if p.name.lower() == "readme.md":
            continue
        if is_done(p):
            dest = CLOSED / p.name
            shutil.move(str(p), str(dest))
            print(f"Closed {p.name} -> closed/")
        else:
            print(f"Still in-progress: {p.name}")

if __name__ == "__main__":
    main()
