#!/usr/bin/env python3
"""检查 Git 可见的非 RAG Markdown 本地文件链接（不校验锚点/远程 URL）。"""

from pathlib import Path
import re
import subprocess


def main() -> int:
    root = Path(__file__).resolve().parent.parent
    paths = subprocess.check_output(
        ["git", "ls-files", "-z", "--cached", "--others", "--exclude-standard", "*.md"],
        cwd=root,
    ).decode().split("\0")
    failures = []
    for name in sorted(set(paths)):
        if not name or name.startswith("internal/rag/"):
            continue
        source = root / name
        if not source.is_file():
            continue
        for link in re.findall(r"\]\(([^)]+)\)", source.read_text(encoding="utf-8")):
            target = link.strip().split("#", 1)[0]
            if not target or re.match(r"[a-zA-Z][a-zA-Z0-9+.-]*:", target):
                continue
            target = target.strip("<>")
            if not (source.parent / target).exists():
                failures.append(f"{name}: {target}")
    for failure in failures:
        print(failure)
    print(f"Local links: {len(failures)} missing targets")
    return int(bool(failures))


if __name__ == "__main__":
    raise SystemExit(main())
