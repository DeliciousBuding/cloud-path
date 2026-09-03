#!/usr/bin/env python3
"""Structural gate for GitHub Actions workflow files.

GitHub only tells you a workflow is broken after you push it, so this script
asserts the invariants that matter *before* the push, using only the standard
library. When PyYAML happens to be importable it is used for a real parse and
the structural checks run against the parsed document; otherwise the same
invariants are checked with a line-based scan (degraded mode, reported as such).

Checked invariants:

* no TAB indentation (YAML forbids it);
* a top-level ``name`` and a trigger block (``on:``);
* a top-level ``permissions:`` block (least privilege by default);
* at least one job, and every job declares ``runs-on`` plus ``steps``/``uses``;
* every ``uses:`` action is pinned to a major-version tag (``@vN``), never a
  floating branch such as ``@main``;
* no plaintext credential-looking assignments;
* ``ci.yml``: ubuntu + windows matrix, race detector only on Linux, gofmt gate,
  frozen pnpm install, typecheck/test/build, public audit and link check;
* ``release.yml``: triggered by ``v*`` tags, builds linux/arm64 (mandatory for
  the native arm64 production host), produces checksums and publishes a release.

Usage:
    python scripts/check_workflows.py                 # .github/workflows
    python scripts/check_workflows.py path/to/a.yml   # explicit files
    python scripts/check_workflows.py --self-test
"""
from __future__ import annotations

import argparse
import pathlib
import re
import sys
import tempfile
from dataclasses import dataclass

try:  # optional: real YAML parse when available
    import yaml  # type: ignore

    HAVE_YAML = True
except Exception:  # pragma: no cover - depends on the machine
    yaml = None  # type: ignore
    HAVE_YAML = False

REPO_ROOT = pathlib.Path(__file__).resolve().parent.parent

USES_RE = re.compile(r"^\s*-\s*uses:\s*(\S+)")
RUNS_ON_RE = re.compile(r"^\s*runs-on:")
JOB_KEY_RE = re.compile(r"^\s{2}([A-Za-z0-9_-]+):\s*$")
TRIGGER_RE = re.compile(r"^(on|true):\s*$|^(on|true):\s*\S")
SECRET_ASSIGN_RE = re.compile(
    r"(?i)^\s*(?:[A-Za-z0-9_.-]*(?:token|password|secret|api[_-]?key)[A-Za-z0-9_.-]*)\s*:\s*['\"]?[A-Za-z0-9_\-]{12,}"
)
ALLOWED_SECRET_VALUE_RE = re.compile(r"\$\{\{\s*secrets\.")


@dataclass(frozen=True)
class Problem:
    path: str
    message: str


def basic_line_checks(path: pathlib.Path, text: str) -> list[Problem]:
    problems: list[Problem] = []
    name = path.name
    lines = text.splitlines()

    for i, line in enumerate(lines, 1):
        if "\t" in line.split("#", 1)[0]:
            problems.append(Problem(name, f"line {i}: TAB indentation is invalid YAML"))
        m = USES_RE.match(line)
        if m:
            ref = m.group(1).strip().strip("'\"")
            if "@" not in ref:
                problems.append(Problem(name, f"line {i}: action is not version-pinned: {ref}"))
            else:
                pin = ref.rsplit("@", 1)[1]
                if not re.match(r"^v\d+(\.\d+)*$", pin):
                    problems.append(Problem(name, f"line {i}: action pinned to a floating ref: {ref}"))
        if SECRET_ASSIGN_RE.match(line) and not ALLOWED_SECRET_VALUE_RE.search(line):
            problems.append(Problem(name, f"line {i}: looks like a plaintext credential assignment"))

    if not re.search(r"^name:\s*\S", text, re.MULTILINE):
        problems.append(Problem(name, "missing top-level name:"))
    if not any(TRIGGER_RE.match(line) for line in lines):
        problems.append(Problem(name, "missing trigger block (on:)"))
    if not re.search(r"^permissions:", text, re.MULTILINE):
        problems.append(Problem(name, "missing top-level permissions: (least privilege)"))
    if not re.search(r"^jobs:\s*$", text, re.MULTILINE):
        problems.append(Problem(name, "missing jobs: block"))
    if not any(RUNS_ON_RE.match(line) for line in lines):
        problems.append(Problem(name, "no job declares runs-on:"))
    if not re.search(r"^\s+steps:\s*$", text, re.MULTILINE) and "uses:" not in text:
        problems.append(Problem(name, "no job declares steps: or uses:"))
    return problems


def yaml_checks(path: pathlib.Path, text: str) -> list[Problem]:
    """Real-parse checks; only run when PyYAML is importable."""
    problems: list[Problem] = []
    name = path.name
    try:
        data = yaml.safe_load(text)
    except Exception as exc:  # noqa: BLE001 - any parse error is a finding
        return [Problem(name, f"YAML parse error: {exc}")]
    if not isinstance(data, dict):
        return [Problem(name, "document root is not a mapping")]

    # YAML 1.1 turns a bare `on:` key into boolean True.
    triggers = data.get("on", data.get(True))
    if not triggers:
        problems.append(Problem(name, "no trigger block parsed (on:)"))
    jobs = data.get("jobs")
    if not isinstance(jobs, dict) or not jobs:
        problems.append(Problem(name, "jobs must be a non-empty mapping"))
        return problems
    for job_id, job in jobs.items():
        if not isinstance(job, dict):
            problems.append(Problem(name, f"job {job_id} is not a mapping"))
            continue
        if not job.get("runs-on") and not job.get("uses"):
            problems.append(Problem(name, f"job {job_id} has no runs-on"))
        steps = job.get("steps")
        if steps is not None and (not isinstance(steps, list) or not steps):
            problems.append(Problem(name, f"job {job_id} has an empty steps list"))
        for step in steps or []:
            if not isinstance(step, dict):
                problems.append(Problem(name, f"job {job_id} has a non-mapping step"))
                continue
            if not step.get("run") and not step.get("uses"):
                problems.append(Problem(name, f"job {job_id} has a step with neither run nor uses"))
    return problems


CI_REQUIRED = (
    "ubuntu-latest",
    "windows-latest",
    "go build ./...",
    "go vet ./...",
    "go test ./... -count=1",
    "-race",
    "fmtcheck.py",
    "pnpm install --frozen-lockfile",
    "tsc --noEmit",
    "pnpm test -- --run",
    "pnpm build",
    "public_audit.py",
    "check_links.py",
    "setup-go",
    "setup-node",
    "pnpm/action-setup",
)

RELEASE_REQUIRED = (
    "tags:",
    "v*",
    "build_matrix.py",
    "--verify-only",
    "checksums",
    "action-gh-release",
    "contents: write",
    "linux",
    "arm64",
    "amd64",
    "windows",
    "darwin",
)


# A plugin repository has no WebUI and none of the core hygiene scripts, so its
# workflows are held to a lighter (but still real) contract. Applying the core
# expectations to them would be a false positive.
PLUGIN_CI_REQUIRED = (
    "ubuntu-latest",
    "go build ./...",
    "go vet ./...",
    "go test ./... -count=1",
    "gofmt -l .",
    "validate_manifest.py",
    "setup-go",
)

PLUGIN_RELEASE_REQUIRED = (
    "tags:",
    "v*",
    "sha256sum",
    "checksums.txt",
    "action-gh-release",
    "contents: write",
    "arm64",
)


def detect_profile(path: pathlib.Path) -> str:
    """core = this repository's own workflows; plugin = generated plugin repos."""
    try:
        resolved = path.resolve()
        resolved.relative_to((REPO_ROOT / ".github" / "workflows").resolve())
        return "core"
    except ValueError:
        return "plugin"


def race_is_linux_only(text: str) -> bool:
    """The race detector needs cgo; assert every -race step is Linux-only.

    A step qualifies when it carries an explicit ``if:`` guard naming ubuntu, or
    when the enclosing job pins ``runs-on: ubuntu-latest`` literally (a matrix
    expression such as ``${{ matrix.os }}`` does not qualify).
    """
    lines = text.splitlines()
    hits = [i for i, line in enumerate(lines) if "-race" in line and not line.strip().startswith("#")]
    if not hits:
        return False
    for idx in hits:
        # Walk up to the start of this step (the nearest "- " item at any indent).
        step_start = idx
        for j in range(idx, max(-1, idx - 15), -1):
            if lines[j].lstrip().startswith("- "):
                step_start = j
                break
        step_block = "\n".join(lines[step_start:idx + 1])
        if re.search(r"(?i)if:.*ubuntu", step_block):
            continue
        # No step-level guard: the job itself must be pinned to ubuntu-latest.
        runs_on = None
        for j in range(step_start, -1, -1):
            m = re.match(r"\s*runs-on:\s*(.+?)\s*$", lines[j])
            if m:
                runs_on = m.group(1)
                break
        if runs_on is not None and runs_on.strip().strip("'\"") == "ubuntu-latest":
            continue
        return False
    return True


def file_specific_checks(path: pathlib.Path, text: str, profile: str = "core") -> list[Problem]:
    problems: list[Problem] = []
    name = path.name
    lowered = name.lower()
    ci_required = CI_REQUIRED if profile == "core" else PLUGIN_CI_REQUIRED
    release_required = RELEASE_REQUIRED if profile == "core" else PLUGIN_RELEASE_REQUIRED

    if "ci" in lowered:
        for needle in ci_required:
            if needle not in text:
                problems.append(Problem(name, f"[{profile}] CI workflow is missing the required element: {needle!r}"))
        if profile == "core" and not race_is_linux_only(text):
            problems.append(Problem(name, "-race must be present and guarded to the Linux leg only (needs cgo)"))
    if "release" in lowered:
        for needle in release_required:
            if needle not in text:
                problems.append(Problem(name, f"[{profile}] release workflow is missing the required element: {needle!r}"))
        has_linux_arm64 = ("linux/arm64" in text) or ("os: linux" in text and "arch: arm64" in text)
        if not has_linux_arm64:
            problems.append(Problem(name, "release matrix must include the linux+arm64 combination (native arm64 production host)"))
    return problems


def check_path(path: pathlib.Path, profile: str | None = None) -> list[Problem]:
    try:
        text = path.read_text(encoding="utf-8")
    except OSError as exc:
        return [Problem(path.name, f"unreadable: {exc}")]
    resolved_profile = profile or detect_profile(path)
    problems = basic_line_checks(path, text)
    if HAVE_YAML:
        problems.extend(yaml_checks(path, text))
    problems.extend(file_specific_checks(path, text, resolved_profile))
    return problems


GOOD_CI = """name: CI

on:
  push:
    branches: [main]

permissions:
  contents: read

jobs:
  go:
    runs-on: ${{ matrix.os }}
    strategy:
      matrix:
        os: [ubuntu-latest, windows-latest]
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
      - run: go build ./...
      - run: go vet ./...
      - run: go test ./... -count=1
      - name: Race (Linux only)
        if: matrix.os == 'ubuntu-latest'
        run: go test ./... -race -count=1
      - run: python scripts/fmtcheck.py
      - uses: pnpm/action-setup@v4
      - uses: actions/setup-node@v4
      - run: pnpm install --frozen-lockfile
      - run: pnpm exec tsc --noEmit
      - run: pnpm test -- --run
      - run: pnpm build
      - run: python scripts/public_audit.py
      - run: python scripts/check_links.py
"""


def self_test() -> int:
    errors: list[str] = []
    tmp = pathlib.Path(tempfile.mkdtemp(prefix="cpwf-"))
    try:
        good = tmp / "ci.yml"
        good.write_text(GOOD_CI, encoding="utf-8")
        for p in check_path(good, "core"):
            errors.append(f"good CI workflow flagged: {p.message}")

        cases: dict[str, tuple[str, str]] = {
            "tab indent": ("\t- run: echo hi\n", "TAB indentation"),
            "floating action ref": ("      - uses: actions/checkout@main\n", "floating ref"),
            "unpinned action": ("      - uses: actions/checkout\n", "not version-pinned"),
            "plaintext secret": ("      api_token: abcdef0123456789abcdef\n", "plaintext credential"),
            "missing windows leg": ("", "windows-latest"),
        }
        for label, (inject, expect) in cases.items():
            bad = tmp / "ci-bad.yml"
            if label == "missing windows leg":
                bad.write_text(GOOD_CI.replace(", windows-latest", ""), encoding="utf-8")
            else:
                bad.write_text(GOOD_CI + inject, encoding="utf-8")
            found = check_path(bad, "core")
            if not any(expect in p.message for p in found):
                errors.append(f"{label}: expected a finding containing {expect!r}, got {[p.message for p in found]}")

        # secrets.* references must NOT be flagged.
        ok_secret = tmp / "ci-secret.yml"
        ok_secret.write_text(GOOD_CI + "      api_token: ${{ secrets.API_TOKEN }}\n", encoding="utf-8")
        for p in check_path(ok_secret, "core"):
            if "credential" in p.message:
                errors.append("a ${{ secrets.* }} reference was flagged as a plaintext credential")

        # Race guard: moving -race outside the Linux-only guard must be caught.
        unguarded = tmp / "ci-race.yml"
        unguarded.write_text(GOOD_CI.replace("        if: matrix.os == 'ubuntu-latest'\n", ""), encoding="utf-8")
        if not any("guarded to the Linux leg" in p.message for p in check_path(unguarded, "core")):
            errors.append("an unguarded -race step was not caught")

        # Release invariants.
        good_release = tmp / "release.yml"
        good_release.write_text(
            "name: release\n\non:\n  push:\n    tags:\n      - \"v*\"\n\npermissions:\n  contents: write\n\n"
            "jobs:\n  build:\n    runs-on: ubuntu-latest\n    steps:\n      - uses: actions/checkout@v4\n"
            "      - run: python scripts/build_matrix.py --platforms linux/arm64,linux/amd64,windows/amd64,"
            "windows/arm64,darwin/amd64,darwin/arm64 --out dist\n"
            "  publish:\n    runs-on: ubuntu-latest\n    steps:\n      - uses: actions/download-artifact@v4\n"
            "      - run: python scripts/build_matrix.py --verify-only --out dist\n"
            "      - run: cat dist/checksums.txt\n      - uses: softprops/action-gh-release@v2\n",
            encoding="utf-8")
        for p in check_path(good_release, "core"):
            errors.append(f"good release workflow flagged: {p.message}")

        bad_release = tmp / "release-noarm.yml"
        bad_release.write_text(good_release.read_text(encoding="utf-8").replace("linux/arm64,", "")
                               .replace("--verify-only", "--no-verify"), encoding="utf-8")
        found = [p.message for p in check_path(bad_release, "core")]
        if not any("linux+arm64" in m for m in found):
            errors.append(f"release workflow without arm64 was not caught: {found}")
        if not any("--verify-only" in m for m in found):
            errors.append(f"release workflow without the verify gate was not caught: {found}")

        if HAVE_YAML:
            broken = tmp / "ci-broken.yml"
            broken.write_text("name: x\non: push\npermissions:\n  contents: read\njobs:\n  a:\n   runs-on: ubuntu-latest\n  steps: []\n", encoding="utf-8")
            if not any("parse error" in p.message or "not a mapping" in p.message or "steps" in p.message
                       for p in check_path(broken, "core")):
                errors.append("malformed YAML structure was not caught in PyYAML mode")
        # Plugin profile: the generated plugin repo workflow must pass its own
        # (lighter) contract and must NOT be judged by core-only expectations.
        plugin_ci = tmp / "plugin-ci.yml"
        plugin_ci.write_text(
            "name: ci\n\non:\n  push:\n\npermissions:\n  contents: read\n\n"
            "jobs:\n  test:\n    runs-on: ubuntu-latest\n    steps:\n"
            "      - uses: actions/checkout@v4\n      - uses: actions/setup-go@v5\n"
            "      - run: go build ./...\n      - run: go vet ./...\n"
            "      - run: go test ./... -count=1\n"
            "      - run: test -z \"$(gofmt -l .)\"\n"
            "      - run: python scripts/validate_manifest.py plugin.yaml --dir .\n",
            encoding="utf-8")
        for p in check_path(plugin_ci, "plugin"):
            errors.append(f"plugin CI workflow flagged under the plugin profile: {p.message}")
        for p in check_path(plugin_ci, "core"):
            if "windows-latest" not in p.message:
                break
        else:
            errors.append("the core profile did not notice a missing windows leg")

    finally:
        import shutil

        shutil.rmtree(tmp, ignore_errors=True)

    mode = "PyYAML parse + line scan" if HAVE_YAML else "line scan only (PyYAML not importable)"
    if errors:
        for e in errors:
            print("self-test: " + e)
        print(f"workflow check self-test FAILED ({mode})")
        return 1
    print(f"workflow check self-test PASS ({mode})")
    return 0


def collect(paths: list[str]) -> list[pathlib.Path]:
    if not paths:
        wf_dir = REPO_ROOT / ".github" / "workflows"
        if not wf_dir.is_dir():
            return []
        return sorted(list(wf_dir.glob("*.yml")) + list(wf_dir.glob("*.yaml")))
    found: list[pathlib.Path] = []
    for raw in paths:
        p = pathlib.Path(raw)
        if p.is_dir():
            found.extend(sorted(list(p.glob("*.yml")) + list(p.glob("*.yaml"))))
        elif p.is_file():
            found.append(p)
        else:
            print(f"path not found: {raw}", file=sys.stderr)
            raise SystemExit(2)
    return found


def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--self-test", action="store_true")
    parser.add_argument("--profile", choices=("core", "plugin", "auto"), default="auto",
                        help="expectation profile; auto = core for .github/workflows, plugin elsewhere")
    parser.add_argument("paths", nargs="*", help="workflow files or directories (default: .github/workflows)")
    args = parser.parse_args(argv)

    if args.self_test:
        return self_test()

    files = collect(args.paths)
    if not files:
        print("workflow check FAILED: no workflow files found")
        return 1
    mode = "PyYAML parse + line scan" if HAVE_YAML else "line scan only (PyYAML not importable; structural checks degraded)"
    profile = None if args.profile == "auto" else args.profile
    all_problems: list[Problem] = []
    for f in files:
        problems = check_path(f, profile)
        all_problems.extend(problems)
        effective = profile or detect_profile(f)
        print(f"  {'FAIL' if problems else 'OK  '} {f} [{effective}]")
        for p in problems:
            print(f"       - {p.message}")
    if all_problems:
        print(f"workflow check FAILED ({len(all_problems)} problem(s), mode: {mode})")
        return 1
    print(f"workflow check PASS ({len(files)} file(s), mode: {mode})")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
