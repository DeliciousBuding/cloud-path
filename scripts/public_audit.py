#!/usr/bin/env python3
"""Audit tracked files before publishing the repository.

The audit is intentionally conservative and reports only path, line number and
rule name—never the matched value. It uses ``git ls-files`` so ignored local
state and worktrees are outside its scope.
"""
from __future__ import annotations

import argparse
import pathlib
import re
import subprocess
import sys
from dataclasses import dataclass


@dataclass(frozen=True)
class Finding:
    path: str
    line: int | None
    rule: str


CONTENT_RULES: tuple[tuple[str, re.Pattern[str]], ...] = (
    ("private-key", re.compile(r"-----BEGIN [A-Z ]+ PRIVATE KEY-----")),
    ("openai-style-key", re.compile(r"\bsk-[A-Za-z0-9_-]{20,}\b")),
    ("github-token", re.compile(r"\bgh[pousr]_[A-Za-z0-9]{20,}\b")),
    ("aws-access-key", re.compile(r"\bAKIA[0-9A-Z]{16}\b")),
    ("slack-token", re.compile(r"\bxox[baprs]-[A-Za-z0-9-]{20,}\b")),
    ("local-windows-path", re.compile(r"\b[A-Za-z]:\\(?:Users|Code)\\", re.IGNORECASE)),
    ("local-home-path", re.compile(r"(?<![A-Za-z0-9])/(?:home|Users)/[^/\s]+/")),
)

FORBIDDEN_SUFFIXES = {
    ".db", ".db-shm", ".db-wal", ".hex", ".obj", ".lib",
    ".doc", ".docx", ".ppt", ".pptx",
}

ALLOWED_SPECIAL_FILES = {
    "deploy/config.example.env",
}


def tracked_files() -> list[str]:
    proc = subprocess.run(
        ["git", "ls-files", "-z"], capture_output=True, check=False
    )
    if proc.returncode != 0:
        raise RuntimeError(proc.stderr.decode("utf-8", "replace").strip())
    return [part.decode("utf-8", "surrogateescape") for part in proc.stdout.split(b"\0") if part]


def audit_paths(paths: list[str]) -> list[Finding]:
    findings: list[Finding] = []
    for raw in paths:
        normalized = raw.replace("\\", "/")
        lower = normalized.lower()
        path = pathlib.Path(raw)

        if lower == ".local" or lower.startswith(".local/"):
            findings.append(Finding(normalized, None, "tracked-private-layer"))
        if lower.endswith(("/edge.yaml",)) or lower == "edge.yaml":
            findings.append(Finding(normalized, None, "tracked-live-config"))
        if path.suffix.lower() in FORBIDDEN_SUFFIXES:
            findings.append(Finding(normalized, None, "forbidden-binary-or-runtime-asset"))
        if lower.endswith(".env") and normalized not in ALLOWED_SPECIAL_FILES and ".example." not in lower:
            findings.append(Finding(normalized, None, "tracked-environment-file"))

        if not path.is_file():
            continue
        try:
            data = path.read_bytes()
        except OSError:
            findings.append(Finding(normalized, None, "unreadable-tracked-file"))
            continue
        if b"\x00" in data:
            continue
        text = data.decode("utf-8", "replace")
        for line_no, line in enumerate(text.splitlines(), 1):
            for rule, pattern in CONTENT_RULES:
                if pattern.search(line):
                    findings.append(Finding(normalized, line_no, rule))
    return findings


def self_test() -> int:
    safe = ["README.md", "deploy/config.example.env"]
    if audit_paths(safe):
        print("self-test failed: safe repository files triggered findings")
        return 1
    samples = {
        "private-key": "FIXTURE_PEM_REMOVED_FROM_HISTORY",
        "openai-style-key": "FIXTURE_SK_REMOVED_FROM_HISTORY",
        "github-token": "FIXTURE_GHP_REMOVED_FROM_HISTORY",
        "aws-access-key": "FIXTURE_AKIA_REMOVED_FROM_HISTORY",
        "slack-token": "FIXTURE_SLACK_REMOVED_FROM_HISTORY",
        "local-windows-path": r"D:\Code\private\file.txt",
        "local-home-path": "/home/example/private/file.txt",
    }
    for rule, sample in samples.items():
        if not dict(CONTENT_RULES)[rule].search(sample):
            print(f"self-test failed: {rule} did not trigger")
            return 1
    print(f"public audit self-test PASS ({len(samples)} red-team cases)")
    return 0


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--self-test", action="store_true")
    args = parser.parse_args()
    if args.self_test:
        return self_test()

    findings = audit_paths(tracked_files())
    if findings:
        print("public audit FAILED (matched values are intentionally redacted):")
        for finding in findings:
            location = f":{finding.line}" if finding.line is not None else ""
            print(f"  {finding.path}{location} [{finding.rule}]")
        return 1
    print("public audit PASS")
    return 0


if __name__ == "__main__":
    sys.exit(main())
