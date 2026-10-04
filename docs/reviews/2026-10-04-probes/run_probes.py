#!/usr/bin/env python3
"""通过 Go overlay 运行人工评审探针，不修改目标包文件。"""

import argparse
import json
import os
from pathlib import Path
import subprocess
import tempfile


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--mcp", action="store_true", help="只运行需要监听本机端口的 MCP 探针"
    )
    args = parser.parse_args()
    materials = Path(__file__).resolve().parent
    root = materials.parents[2]
    selections = (
        [("internal/mcp/einoadapter", "mcp_test.go.txt")]
        if args.mcp
        else [
            ("internal/sandbox", "sandbox_test.go.txt"),
            ("internal/runtime", "runtime_test.go.txt"),
            ("internal/tasks", "tasks_test.go.txt"),
        ]
    )
    replacements = {}
    for package, source in selections:
        virtual_file = root / package / "zz_review_probe_test.go"
        if virtual_file.exists():
            raise SystemExit(f"拒绝覆盖已有文件：{virtual_file}")
        replacements[str(virtual_file)] = str(materials / source)

    # 本次环境的 Go 从 PATH 选择工具链，避免继承不匹配的 GOROOT。
    env = os.environ.copy()
    env.pop("GOROOT", None)
    with tempfile.TemporaryDirectory(prefix="humbert-review-probes-") as temp:
        overlay = Path(temp) / "overlay.json"
        overlay.write_text(
            json.dumps({"Replace": replacements}, ensure_ascii=False),
            encoding="utf-8",
        )
        command = [
            "go", "test", f"-overlay={overlay}", "-count=1",
            "-run=^TestReview", "-v",
            *[f"./{package}" for package, _ in selections],
        ]
        print("运行历史人工探针；3d01afd 基线预期返回 1，当前代码以正式回归测试为准。", flush=True)
        return subprocess.run(command, cwd=root, env=env, check=False).returncode


if __name__ == "__main__":
    raise SystemExit(main())
