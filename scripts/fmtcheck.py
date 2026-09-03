#!/usr/bin/env python3
"""gofmt 门禁：列出未格式化文件并以非零码退出。

跨平台替代 `gofmt -l . | grep .` 这类 shell 管道技巧（Windows 上没有 grep/findstr 差异问题）。
gofmt 递归会进入 .worktrees/（开发产物 checkout），结果里过滤掉这些目录与 node_modules/dist。
用法：python scripts/fmtcheck.py
"""
import subprocess
import sys

# Windows CI 控制台默认 cp1252：直接 print 中文会 UnicodeEncodeError 崩掉门禁
# （崩溃会掩盖真正的 gofmt 结果），统一切到 UTF-8 并对不可编码字符降级替换。
for _stream in (sys.stdout, sys.stderr):
    if hasattr(_stream, "reconfigure"):
        _stream.reconfigure(encoding="utf-8", errors="replace")

EXCLUDE = (".worktrees", "node_modules", "dist", ".git")


def main() -> int:
    proc = subprocess.run(
        ["gofmt", "-l", "."],
        capture_output=True,
        text=True,
        errors="replace",
    )
    if proc.returncode != 0:
        sys.stderr.write((proc.stderr or "").strip() + "\n")
        return proc.returncode
    bad = [
        line.strip()
        for line in proc.stdout.splitlines()
        if line.strip() and not any(x in line for x in EXCLUDE)
    ]
    if bad:
        print("gofmt 未格式化文件：")
        for f in bad:
            print("  " + f)
        print("修复：gofmt -w .")
        return 1
    print("gofmt 干净")
    return 0


if __name__ == "__main__":
    sys.exit(main())