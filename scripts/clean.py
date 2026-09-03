#!/usr/bin/env python3
"""清理构建产物与本地运行数据（跨平台，替代 rm -rf / Remove-Item 差异）。

删除目标固定为仓库内的构建产物目录，绝不接受外部路径参数：
  bin/          编译出的二进制
  data/         本地 SQLite 运行数据
  webui/dist/   前端构建产物

用法：python scripts/clean.py [--dry-run]
"""
import pathlib
import shutil
import sys

TARGETS = ("bin", "data", "webui/dist")


def main() -> int:
    dry = "--dry-run" in sys.argv[1:]
    root = pathlib.Path(__file__).resolve().parent.parent
    for rel in TARGETS:
        target = (root / rel).resolve()
        # 安全栅栏：目标必须严格位于仓库根内
        if root not in target.parents:
            print(f"跳过（不在仓库内）: {target}", file=sys.stderr)
            continue
        if not target.exists():
            print(f"不存在: {rel}")
            continue
        if dry:
            print(f"[dry-run] 将删除: {rel}")
            continue
        shutil.rmtree(target, ignore_errors=True)
        print(f"已删除: {rel}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
