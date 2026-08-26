#!/usr/bin/env python3
"""
Docs check — ensures product documentation is present and fresh.
Called daily at 03:00 by stealthscale-docs-check (hy3).
If docs missing/stale, creates a local issue in issues/open/.
"""
from pathlib import Path
import datetime

ROOT = Path(__file__).resolve().parents[1]
OPEN = ROOT / "issues/open"

checks = [
    (ROOT / "docs/stealthscale/overview.md", "overview exists"),
    (ROOT / "docs/stealthscale/install.md", "install docs"),
    (ROOT / "docs/usage/webui.md", "webui usage"),
    (ROOT / "README.md", "readme"),
    (ROOT / "hscontrol/webui", "webui package"),
]

def main():
    missing = []
    for path, desc in checks:
        if not path.exists():
            missing.append(f"{path.relative_to(ROOT)} ({desc}) missing")
    if missing:
        slug = f"docs-gap-{datetime.datetime.now().strftime('%Y%m%d')}"
        dest = OPEN / f"{slug}.md"
        if dest.exists():
            print(f"Docs gap issue already exists {dest}")
            return
        content = f"""---
title: "Documentation gap detected {datetime.datetime.now().date()}"
labels: ["docs"]
priority: medium
created: {datetime.datetime.now().isoformat()}Z
---

Automated docs check found missing docs:

"""
        for m in missing:
            content += f"- {m}\n"
        content += "\nPlease create these docs with hy3.\n"
        dest.write_text(content)
        print(f"Created docs gap issue {dest.name}")
    else:
        print("All docs checks passed")

if __name__ == "__main__":
    main()
