#!/usr/bin/env python3
"""WebUI design-token drift gate.

The design system is only useful if arbitrary values cannot creep back in.
This gate scans production TS/TSX under webui/src and rejects the small set
of patterns that already have semantic tokens.

Usage:
    python scripts/check_design_tokens.py
    python scripts/check_design_tokens.py --self-test
"""
from __future__ import annotations

import argparse
import pathlib
import re
import sys

for _stream in (sys.stdout, sys.stderr):
    if hasattr(_stream, "reconfigure"):
        _stream.reconfigure(encoding="utf-8", errors="replace")

ROOT = pathlib.Path(__file__).resolve().parent.parent
SRC = ROOT / "webui" / "src"

RULES: list[tuple[re.Pattern[str], str]] = [
    (re.compile(r"text-\[[0-9]+px\]"), "arbitrary font size; use a text-* token"),
    (re.compile(r"rounded-(?:sm|md|lg|xl|2xl|3xl|full)\b"), "raw radius; use rounded-tile/card/pill"),
    (re.compile(r"\bz-(?:\[[^\]]+\]|[0-9]+)\b"), "raw z-index; use z-local/sticky/nav/overlay"),
    (re.compile(r"\bmin-h-11\b"), "raw touch height; use min-h-touch"),
    (re.compile(r"\btransition-all\b"), "transition-all; name the property"),
    (re.compile(r"#[0-9a-fA-F]{3,8}\b|\brgba?\(|\bhsla?\("), "raw color; use a color token"),
    (re.compile(r"<select\b"), "raw select; use the Select primitive"),
    (re.compile(r"<input\b"), "raw input; use Input/Checkbox/Radio primitive"),
    (re.compile(r"<textarea\b"), "raw textarea; use Textarea primitive"),
]


def scan_text(text: str, *, allow_primitive_tags: bool = False) -> list[str]:
    hits: list[str] = []
    for line_no, line in enumerate(text.splitlines(), 1):
        for pattern, message in RULES:
            if allow_primitive_tags and message in {
                "raw select; use the Select primitive",
                "raw input; use Input/Checkbox/Radio primitive",
                "raw textarea; use Textarea primitive",
            }:
                continue
            match = pattern.search(line)
            if match:
                hits.append(f"line {line_no}: {match.group(0)} ({message})")
    return hits


def production_files() -> list[pathlib.Path]:
    return sorted(
        path
        for path in SRC.rglob("*")
        if path.suffix in {".ts", ".tsx"} and "__tests__" not in path.parts
    )


def self_test() -> int:
    bad = "\n".join(
        [
            'className="text-[13px]"',
            'className="rounded-lg"',
            'className="z-50"',
            'className="min-h-11"',
            'className="transition-all"',
            'style={{ color: "#fff" }}',
            '<select aria-label="raw" />',
            '<input aria-label="raw" />',
            '<textarea aria-label="raw" />',
        ]
    )
    good = 'className="text-meta rounded-tile z-overlay transition-colors"'
    failures: list[str] = []
    if len(scan_text(bad)) != 9:
        failures.append("forbidden sample did not produce nine hits")
    if scan_text(good):
        failures.append("semantic sample produced a hit")
    if failures:
        for failure in failures:
            print(f"SELF-TEST FAIL: {failure}", file=sys.stderr)
        return 1
    print("SELF-TEST OK")
    return 0


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--self-test", action="store_true")
    args = parser.parse_args()
    if args.self_test:
        return self_test()

    failures: list[str] = []
    files = production_files()
    primitive = ROOT / "webui" / "src" / "components" / "ui.tsx"
    for path in files:
        allow_primitive_tags = path == primitive
        for hit in scan_text(path.read_text(encoding="utf-8"), allow_primitive_tags=allow_primitive_tags):
            failures.append(f"{path.relative_to(ROOT)}:{hit}")

    if failures:
        print("DESIGN TOKEN GATE: FAIL", file=sys.stderr)
        for failure in failures:
            print(f"  {failure}", file=sys.stderr)
        return 1

    print(f"DESIGN TOKEN GATE: OK ({len(files)} production files)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
