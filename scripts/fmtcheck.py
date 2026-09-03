#!/usr/bin/env python3
"""gofmt 门禁：列出未格式化文件并以非零码退出。

跨平台替代 `gofmt -l . | grep .` 这类 shell 管道技巧（Windows 上没有 grep/findstr 差异问题）。
用法：python scripts/fmtcheck.py [目录...]
"""
import subprocess
import sys


def main() -> int:
    targets = sys.argv[1:] or ["."]
    proc = subprocess.run(
        ["gofmt", "-l", *targets],
        capture_output=True,
        text=True,
    )
    if proc.returncode != 0:
        sys.stderr.write(proc.stderr.strip() + "\n")
        return proc.returncode
    files = [line.strip() for line in proc.stdout.splitlines() if line.strip()]
    if files:
        print("gofmt 未格式化文件：")
        for f in files:
            print("  " + f)
        print("修复：gofmt -w .")
        return 1
    print("gofmt 干净")
    return 0


if __name__ == "__main__":
    sys.exit(main())
