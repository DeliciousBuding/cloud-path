#!/usr/bin/env python3
"""Generate the standalone repository for the reference Application plugin.

Source of truth: ``examples/scheduled-compartment`` in this monorepo plus the
public Go SDK boundary it imports. The generator writes a complete, independent
repository tree (own ``go.mod``, README, LICENSE, .gitignore, CI, release
workflow, manifest and manifest validator) into a **local output directory**.

Hard boundaries enforced by this script:

* It never pushes, never creates a remote repository, never touches
  ``examples/`` and never writes outside the output directory.
* Generated Go code must not import ``<core>/internal/...`` or any non-SDK core
  path; such an import is a fatal error, not a warning.
* Generated text is scanned for private material (local absolute paths, home
  directories, credentials, operator/course identifiers). A hit is fatal.
* Every documentation rewrite is an asserted exact-match replacement: if the
  upstream text drifts, generation fails instead of silently producing a wrong
  README.

SDK dependency strategy (deliberate trade-off, also documented in the generated
README):

* Default output uses the **published module path**::

      require github.com/DeliciousBuding/cloud-path v0.1.0

  This is the shippable form: anyone can build the plugin repo from a clean
  checkout with no local assumptions, and ``go mod tidy`` fills ``go.sum``.
  It requires the core repository to be public and tagged.

* ``--core-path <dir>`` additionally writes a ``replace`` directive pointing at
  a local core checkout. That makes the generated tree buildable **today**,
  before the core tag exists, which is how this script verifies itself. A tree
  generated with ``--core-path`` is marked with ``LOCAL_REPLACE.txt`` and must
  not be published as-is: the replace line has to be dropped first.

Usage:
    python deploy/split/split_app_plugin.py --self-test
    python deploy/split/split_app_plugin.py --core-path . --force
    python deploy/split/split_app_plugin.py --out /tmp/plugin-repo --core-version v0.1.0
"""
from __future__ import annotations

import argparse
import os
import pathlib
import re
import shutil
import subprocess
import sys
import tempfile

REPO_ROOT = pathlib.Path(__file__).resolve().parents[2]

CORE_MODULE = "github.com/DeliciousBuding/cloud-path"
PLUGIN_MODULE = "github.com/DeliciousBuding/cloud-path-app-scheduled-compartment"
PLUGIN_REPO_SLUG = "cloud-path-app-scheduled-compartment"
SOURCE_DIR = REPO_ROOT / "examples" / "scheduled-compartment"
ENTRYPOINT_PKG = "cmd/cloud-path-app-scheduled-compartment"
ENTRYPOINT_BIN = "cloud-path-app-scheduled-compartment"
SDK_PREFIX = CORE_MODULE + "/sdk/"
TEMPLATE_SCRIPTS = REPO_ROOT / "templates" / "go-plugin" / "application" / "scripts"

# Files copied verbatim from the incubation directory.
VERBATIM_FILES = ("plugin.yaml", "requirements.yaml")

# Private material that must never reach a public plugin repository.
DENY_PATTERNS: tuple[tuple[str, re.Pattern[str]], ...] = (
    ("windows-abs-path", re.compile(r"\b[A-Za-z]:\\(?:Users|Code)\\", re.IGNORECASE)),
    ("unix-home-path", re.compile(r"(?<![A-Za-z0-9])/(?:home|Users)/[^/\s]+/")),
    ("private-key", re.compile(r"-----BEGIN [A-Z ]+ PRIVATE KEY-----")),
    ("github-token", re.compile(r"\bgh[pousr]_[A-Za-z0-9]{20,}\b")),
    ("openai-style-key", re.compile(r"\bsk-[A-Za-z0-9_-]{20,}\b")),
    ("tenant-token", re.compile(r"\bcp_[A-Za-z0-9_-]{20,}\b")),
    ("operator-domain", re.compile(r"vectorcontrol", re.IGNORECASE)),
    ("course-board", re.compile(r"\b(?:IAP15|STC-B|STCB)\b")),
    ("course-platform", re.compile(r"学习通|课程|作业|学号")),
)

# Applied to Go sources only: a plugin repository must not even mention a core
# internal package path. The bundled manifest validator *detects* such imports,
# so it is exempt from the text scan and covered by the Go-only rule instead.
GO_ONLY_DENY: tuple[tuple[str, re.Pattern[str]], ...] = (
    ("core-internal-import", re.compile(re.escape(CORE_MODULE + "/internal/"))),
)

# Files whose text is exempt from a given deny rule, with the reason.
DENY_EXEMPT: dict[str, tuple[str, ...]] = {
    "scripts/validate_manifest.py": ("core-internal-import",),
}


class SplitError(RuntimeError):
    """Fatal generation error (never a warning)."""


def read_text(path: pathlib.Path) -> str:
    return path.read_text(encoding="utf-8")


def write_text(path: pathlib.Path, text: str) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(text, encoding="utf-8", newline="\n")


def core_go_version() -> str:
    """Read the Go language version from the core go.mod (never hard-coded)."""
    mod = read_text(REPO_ROOT / "go.mod")
    m = re.search(r"^go\s+([0-9]+(?:\.[0-9]+)*)\s*$", mod, re.MULTILINE)
    if not m:
        raise SplitError("could not read the go directive from go.mod")
    return m.group(1)


def rewrite_go_source(text: str, rel: str) -> str:
    """Rewrite core-module import paths for the standalone repository."""
    if CORE_MODULE + "/internal/" in text:
        raise SplitError(f"{rel}: imports a core internal package; the plugin must only use the public SDK")
    # The incubation package itself becomes the plugin module root.
    text = text.replace(f'"{CORE_MODULE}/examples/scheduled-compartment"', f'"{PLUGIN_MODULE}"')
    text = text.replace(f'"{CORE_MODULE}/examples/scheduled-compartment/', f'"{PLUGIN_MODULE}/')
    # Any remaining core import must be under the public SDK prefix.
    for m in re.finditer(r'"(' + re.escape(CORE_MODULE) + r'/[^"]+)"', text):
        path = m.group(1)
        if path == PLUGIN_MODULE or path.startswith(PLUGIN_MODULE + "/"):
            continue
        if not path.startswith(SDK_PREFIX):
            raise SplitError(f"{rel}: imports non-SDK core path {path}")
    return text


README_REWRITES: tuple[tuple[str, str], ...] = (
    (
        """Inside the CloudPath monorepo the same code lives under
`examples/scheduled-compartment`; prefix the package patterns with that
directory, for example `go build ./examples/scheduled-compartment/...`.""",
        """This repository is generated from the CloudPath core monorepo
(`github.com/DeliciousBuding/cloud-path`, incubation path
`examples/scheduled-compartment`) by `deploy/split/split_app_plugin.py`. The
package layout here is the standalone one: the application library lives at the
module root and the entrypoint under `cmd/`.""",
    ),
    (
        """Inside the monorepo, the repository-level gates additionally cover the whole
tree:

```bash
go vet ./...
python scripts/fmtcheck.py
# Full binary → Host E2E (monorepo-only harness)
go test ./testing/plugin-harness -run TestScheduledCompartmentBinaryHostE2E -count=1
```""",
        """Repository-level gates in this checkout:

```bash
gofmt -l .                                  # expected: no output
python scripts/validate_manifest.py --self-test
python scripts/validate_manifest.py plugin.yaml --dir .
```

The full binary-to-Host end-to-end harness lives in the CloudPath core
repository (`testing/plugin-harness`) because it needs the Core Plugin Host;
it is not duplicated here.""",
    ),
    (
        """- **Go 1.26.3 or newer** (the checkout's `go.mod` declares `go 1.26.3`).""",
        """- **Go** — the exact language version is declared in this repository's
  `go.mod` (kept in sync with the core monorepo by the generator).""",
    ),
)


def render_readme(upstream: str) -> str:
    text = upstream
    for old, new in README_REWRITES:
        if old not in text:
            raise SplitError(f"README rewrite anchor no longer matches upstream text:\n---\n{old[:120]}\n---")
        text = text.replace(old, new, 1)
    header = (
        f"# {PLUGIN_REPO_SLUG}\n\n"
        "> Standalone repository for the CloudPath reference **Application** plugin\n"
        "> `scheduled-compartment`. Generated from the CloudPath core monorepo by\n"
        "> `deploy/split/split_app_plugin.py`; edit the upstream source, then regenerate.\n\n"
        "## Module and SDK dependency\n\n"
        f"- Module path: `{PLUGIN_MODULE}`\n"
        f"- Depends only on the public CloudPath SDK (`{CORE_MODULE}/sdk/...`) — no core\n"
        "  `internal/` package is imported, and the generator fails hard if one appears.\n"
        f"- `go.mod` requires the published core module (`require {CORE_MODULE} <version>`).\n"
        "  That is the shippable form: a clean checkout builds with no local assumptions\n"
        "  once the core tag exists.\n"
        "- For local development against an unpublished core checkout, add a temporary\n"
        f"  `replace {CORE_MODULE} => ../cloud-path` (the generator can write it with\n"
        "  `--core-path`, in which case it also drops a `LOCAL_REPLACE.txt` marker).\n"
        "  **Never publish a tree that still carries that replace line.**\n\n"
        "---\n\n"
    )
    # The upstream README starts with its own H1; drop that first line so the
    # generated file has exactly one title.
    lines = text.splitlines()
    if lines and lines[0].startswith("# "):
        lines = lines[1:]
        while lines and not lines[0].strip():
            lines = lines[1:]
    return header + "\n".join(lines).rstrip() + "\n"


def replace_target(out_dir: pathlib.Path, core_path: str) -> str:
    """Express the local core checkout as a relative path when possible.

    A relative replace keeps local absolute paths (user names, drive letters)
    out of the generated go.mod, which the private-material scan forbids. When
    the two trees are on different drives/roots a relative path does not exist,
    so the absolute path is used and the tree is clearly marked as local-only.
    """
    try:
        rel = os.path.relpath(core_path, str(out_dir))
    except ValueError:
        return core_path.replace(os.sep, "/")
    return rel.replace(os.sep, "/")


def render_go_mod(go_version: str, core_version: str, core_path: str | None,
                  out_dir: pathlib.Path | None = None) -> str:
    lines = [
        f"module {PLUGIN_MODULE}",
        "",
        f"go {go_version}",
        "",
        f"require {CORE_MODULE} {core_version}",
        "",
    ]
    if core_path:
        target = replace_target(out_dir or pathlib.Path("."), core_path)
        lines += [
            "// LOCAL DEVELOPMENT ONLY - remove before publishing this repository.",
            "// The published form resolves the SDK from the tagged core module above.",
            f"replace {CORE_MODULE} => {target}",
            "",
        ]
    return "\n".join(lines)


GITIGNORE = """# Build artifacts
/dist/
/bin/
*.exe
*.test
*.out

# Local runtime state created when the plugin is exercised by a Host
/data/
/plugins.d/
plugins.lock
*.log

# Local private layer / editor noise
.local/
.env
.env.*
.idea/
.vscode/
.DS_Store
"""


def render_ci(go_version: str) -> str:
    return f"""name: ci

on:
  push:
  pull_request:
  workflow_dispatch:

permissions:
  contents: read

jobs:
  test:
    name: build / vet / test / gofmt / manifest
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - uses: actions/setup-go@v5
        with:
          go-version: '{go_version}'
          cache: true

      - uses: actions/setup-python@v5
        with:
          python-version: '3.12'

      - name: Build
        run: go build ./...

      - name: Vet
        run: go vet ./...

      - name: Test
        run: go test ./... -count=1

      - name: gofmt gate
        run: test -z "$(gofmt -l .)"

      - name: No core internal imports
        run: |
          if grep -R "{CORE_MODULE}/internal" --include="*.go" .; then
            echo "plugin must only depend on the public SDK"; exit 1
          fi

      - name: Manifest validator self-test
        run: python scripts/validate_manifest.py --self-test

      - name: Manifest gate
        run: python scripts/validate_manifest.py plugin.yaml --dir .
"""


def render_release(go_version: str) -> str:
    return f"""name: release

on:
  push:
    tags:
      - "v*"

permissions:
  contents: write

jobs:
  build:
    name: build (${{{{ matrix.os }}}}/${{{{ matrix.arch }}}})
    strategy:
      fail-fast: false
      matrix:
        include:
          - {{ os: linux, arch: amd64 }}
          - {{ os: linux, arch: arm64 }}
          - {{ os: windows, arch: amd64 }}
          - {{ os: windows, arch: arm64 }}
          - {{ os: darwin, arch: amd64 }}
          - {{ os: darwin, arch: arm64 }}
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - uses: actions/setup-go@v5
        with:
          go-version: '{go_version}'
          cache: true

      - name: Build entrypoint
        env:
          GOOS: ${{{{ matrix.os }}}}
          GOARCH: ${{{{ matrix.arch }}}}
          CGO_ENABLED: "0"
        run: |
          set -euo pipefail
          ext=""
          if [ "${{{{ matrix.os }}}}" = "windows" ]; then ext=".exe"; fi
          name="{PLUGIN_REPO_SLUG}_${{{{ github.ref_name }}}}_${{{{ matrix.os }}}}_${{{{ matrix.arch }}}}${{ext}}"
          mkdir -p dist
          go build -trimpath -ldflags "-s -w" -o "dist/$name" ./{ENTRYPOINT_PKG}
          echo "artifact=$name" >> "$GITHUB_ENV"

      - name: Upload artifact
        uses: actions/upload-artifact@v4
        with:
          name: ${{{{ matrix.os }}}}-${{{{ matrix.arch }}}}
          path: dist

  release:
    name: publish release
    needs: build
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - name: Collect assets
        uses: actions/download-artifact@v4
        with:
          path: release-assets
          merge-multiple: true

      - name: Checksums
        run: |
          set -euo pipefail
          cd release-assets
          sha256sum * > checksums.txt
          cp ../plugin.yaml plugin.yaml
          ls -l

      - name: Create release
        uses: softprops/action-gh-release@v2
        with:
          draft: false
          fail_on_unmatched_files: true
          files: |
            release-assets/*
"""


EXPECTED_FILES = (
    "go.mod",
    "README.md",
    "LICENSE",
    ".gitignore",
    "plugin.yaml",
    "requirements.yaml",
    "config.go",
    "service.go",
    "service_test.go",
    "manifest_test.go",
    f"{ENTRYPOINT_PKG}/main.go",
    "scripts/validate_manifest.py",
    ".github/workflows/ci.yml",
    ".github/workflows/release.yml",
)


def resolve_out(out: str) -> pathlib.Path:
    """Resolve and fence the output directory."""
    candidate = pathlib.Path(out).expanduser()
    resolved = candidate.resolve() if candidate.is_absolute() else (pathlib.Path.cwd() / candidate).resolve()
    tmp_root = pathlib.Path(tempfile.gettempdir()).resolve()
    allowed = False
    for root in (REPO_ROOT, tmp_root):
        try:
            resolved.relative_to(root)
            allowed = True
            break
        except ValueError:
            continue
    if not allowed:
        raise SplitError(f"--out must be inside the repository ({REPO_ROOT}) or the system temp dir ({tmp_root})")
    if resolved == REPO_ROOT:
        raise SplitError("--out must not be the repository root")
    if resolved.name == ".git" or (resolved / ".git").exists() or resolved == REPO_ROOT / ".git":
        raise SplitError(f"--out points at a git directory or checkout: {resolved}")
    for part in resolved.parts:
        if part == ".git":
            raise SplitError(f"--out must not live inside a .git directory: {resolved}")
    return resolved


def generate(out_dir: pathlib.Path, core_version: str, core_path: str | None, force: bool) -> list[pathlib.Path]:
    if not SOURCE_DIR.is_dir():
        raise SplitError(f"incubation source not found: {SOURCE_DIR}")
    if out_dir.exists():
        if not force:
            raise SplitError(f"output directory already exists (pass --force to replace): {out_dir}")
        shutil.rmtree(out_dir)
    out_dir.mkdir(parents=True, exist_ok=True)

    written: list[pathlib.Path] = []
    go_version = core_go_version()

    # 1. Go sources, with import rewriting (fatal on internal/ or non-SDK imports).
    for src in sorted(SOURCE_DIR.rglob("*.go")):
        rel = src.relative_to(SOURCE_DIR).as_posix()
        dst = out_dir / rel
        write_text(dst, rewrite_go_source(read_text(src), rel))
        written.append(dst)

    # 2. Manifest files verbatim (the drift tests pin all three copies together).
    for name in VERBATIM_FILES:
        src = SOURCE_DIR / name
        if not src.is_file():
            raise SplitError(f"missing manifest source: {src}")
        dst = out_dir / name
        write_text(dst, read_text(src))
        written.append(dst)

    # 3. README: upstream text plus asserted rewrites and a generated header.
    readme_src = SOURCE_DIR / "README.md"
    if not readme_src.is_file():
        raise SplitError(f"missing README source: {readme_src}")
    dst = out_dir / "README.md"
    write_text(dst, render_readme(read_text(readme_src)))
    written.append(dst)

    # 4. Module files.
    dst = out_dir / "go.mod"
    write_text(dst, render_go_mod(go_version, core_version, core_path, out_dir))
    written.append(dst)

    license_src = REPO_ROOT / "LICENSE"
    if not license_src.is_file():
        raise SplitError("core LICENSE not found; the plugin repository needs the same license text")
    dst = out_dir / "LICENSE"
    write_text(dst, read_text(license_src))
    written.append(dst)

    dst = out_dir / ".gitignore"
    write_text(dst, GITIGNORE)
    written.append(dst)

    # 5. Manifest validator reused from the official Go plugin template.
    validator = TEMPLATE_SCRIPTS / "validate_manifest.py"
    if not validator.is_file():
        raise SplitError(f"template manifest validator not found: {validator}")
    dst = out_dir / "scripts" / "validate_manifest.py"
    write_text(dst, read_text(validator))
    written.append(dst)

    # 6. CI + release workflows.
    dst = out_dir / ".github" / "workflows" / "ci.yml"
    write_text(dst, render_ci(go_version))
    written.append(dst)
    dst = out_dir / ".github" / "workflows" / "release.yml"
    write_text(dst, render_release(go_version))
    written.append(dst)

    # 7. Local-replace marker (only when a local core checkout was wired in).
    if core_path:
        dst = out_dir / "LOCAL_REPLACE.txt"
        write_text(dst,
                   "This tree was generated with --core-path, so go.mod carries a local `replace`\n"
                   "directive. Delete this file and the replace line before publishing.\n")
        written.append(dst)

    return written


def local_replace_uses_absolute_path(out_dir: pathlib.Path) -> bool:
    """True when go.mod replaces the core module with an absolute local path."""
    try:
        text = read_text(out_dir / "go.mod")
    except OSError:
        return False
    m = re.search(rf"^replace {re.escape(CORE_MODULE)} => (.+)$", text, re.MULTILINE)
    if not m:
        return False
    target = m.group(1).strip()
    return target.startswith("/") or bool(re.match(r"^[A-Za-z]:[\\/]", target))


def audit_tree(out_dir: pathlib.Path, core_path: str | None) -> list[str]:
    """Post-generation invariants. Returns a list of problems (empty = clean)."""
    problems: list[str] = []

    for rel in EXPECTED_FILES:
        if not (out_dir / rel).is_file():
            problems.append(f"missing expected file: {rel}")
    if problems:
        return problems

    if (out_dir / "LOCAL_REPLACE.txt").is_file() and not core_path:
        problems.append("LOCAL_REPLACE.txt exists but no --core-path was given")
    if core_path and not (out_dir / "LOCAL_REPLACE.txt").is_file():
        problems.append("--core-path was given but LOCAL_REPLACE.txt is missing")

    go_mod = read_text(out_dir / "go.mod")
    if not go_mod.startswith(f"module {PLUGIN_MODULE}\n"):
        problems.append("go.mod module line is not the plugin module path")
    if f"go {core_go_version()}" not in go_mod:
        problems.append("go.mod go directive does not match the core go.mod")
    if local_replace_uses_absolute_path(out_dir) and not core_path:
        problems.append("go.mod carries an absolute local replace path without LOCAL_REPLACE.txt")
    if core_path and f"replace {CORE_MODULE} =>" not in go_mod:
        problems.append("go.mod is missing the requested local replace directive")
    if not core_path and "replace " in go_mod:
        problems.append("go.mod unexpectedly contains a replace directive")

    text_files = [p for p in sorted(out_dir.rglob("*")) if p.is_file()]
    for path in text_files:
        rel = path.relative_to(out_dir).as_posix()
        try:
            text = read_text(path)
        except UnicodeDecodeError:
            problems.append(f"{rel}: not valid UTF-8 text")
            continue
        # A local-only tree (marked by LOCAL_REPLACE.txt, never published) may
        # carry an absolute local replace path when the two checkouts are on
        # different drives; that single file is exempt from the path rules.
        local_only = rel == "go.mod" and (out_dir / "LOCAL_REPLACE.txt").is_file()
        for rule, pattern in DENY_PATTERNS:
            if local_only and rule in ("windows-abs-path", "unix-home-path"):
                continue
            for line_no, line in enumerate(text.splitlines(), 1):
                if pattern.search(line):
                    problems.append(f"{rel}:{line_no}: private material [{rule}]")
        if rel.endswith(".go"):
            exempt = DENY_EXEMPT.get(rel, ())
            for rule, pattern in GO_ONLY_DENY:
                if rule in exempt:
                    continue
                for line_no, line in enumerate(text.splitlines(), 1):
                    if pattern.search(line):
                        problems.append(f"{rel}:{line_no}: private material [{rule}]")
        if rel.endswith(".go"):
            for m in re.finditer(r'^\s*(?:_ )?"([^"]+)"', text, re.MULTILINE):
                imp = m.group(1)
                if imp.startswith(PLUGIN_MODULE):
                    continue
                if imp.startswith(CORE_MODULE):
                    if not imp.startswith(SDK_PREFIX):
                        problems.append(f"{rel}: imports non-SDK core path {imp}")
                    continue
                if "." in imp.split("/")[0]:
                    problems.append(f"{rel}: unexpected third-party dependency {imp}")

    manifest = read_text(out_dir / "plugin.yaml")
    if f"entrypoint: {ENTRYPOINT_BIN}" not in manifest:
        problems.append("plugin.yaml entrypoint does not match the generated binary name")
    if not (out_dir / ENTRYPOINT_PKG / "main.go").is_file():
        problems.append(f"entrypoint package missing: {ENTRYPOINT_PKG}")
    return problems


RESOLUTION_MARKERS = (
    "no required module provides package",
    "missing go.sum entry",
    "module lookup disabled",
    "cannot find module",
    "unrecognized import path",
    "no matching versions",
    "unknown revision",
    "dial tcp",
    "i/o timeout",
    "connection refused",
    "not found",
    "gone",
    "verifying module",
    "GOPROXY",
)


def run_go(out_dir: pathlib.Path, args: list[str]) -> tuple[int, str]:
    proc = subprocess.run(["go", *args], cwd=str(out_dir), capture_output=True, text=True, errors="replace")
    return proc.returncode, ((proc.stdout or "") + (proc.stderr or "")).strip()


def classify_go_failure(output: str) -> str:
    low = output.lower()
    if any(marker in low for marker in RESOLUTION_MARKERS):
        return "unresolved-dependency"
    return "compile-error"


def try_build(out_dir: pathlib.Path) -> tuple[str, str]:
    """Return (status, detail); status is ok|unresolved-dependency|compile-error|no-go."""
    import shutil as _shutil

    if _shutil.which("go") is None:
        return "no-go", "go toolchain not on PATH"
    rc, out = run_go(out_dir, ["build", "./..."])
    if rc == 0:
        return "ok", out
    return classify_go_failure(out), out


def self_test() -> int:
    errors: list[str] = []
    notes: list[str] = []
    tmp = pathlib.Path(tempfile.mkdtemp(prefix="cpsplit-"))
    try:
        # 1. Default generation (published module path, no local replace).
        out = tmp / "plugin"
        generate(out, "v0.1.0", None, force=True)
        problems = audit_tree(out, None)
        if problems:
            errors.extend(f"default tree: {p}" for p in problems)
        readme = read_text(out / "README.md")
        if "prefix the package patterns" in readme:
            errors.append("README still carries the monorepo-only build instructions")
        if "testing/plugin-harness" in readme and "core\nrepository" not in readme:
            errors.append("README still points at the monorepo harness as a local command")
        if not readme.startswith(f"# {PLUGIN_REPO_SLUG}"):
            errors.append("generated README does not start with the plugin repository title")
        go_mod = read_text(out / "go.mod")
        if f"require {CORE_MODULE} v0.1.0" not in go_mod:
            errors.append("go.mod does not require the published core module")
        mains = [p for p in out.rglob("*.go") if "package main" in read_text(p)]
        if len(mains) != 1:
            errors.append(f"expected exactly one main package, got {len(mains)}")

        # 2. Import rewriting rejects core internal packages and non-SDK paths.
        for bad, why in (
            (f'import "{CORE_MODULE}/internal/store"', "internal"),
            (f'import "{CORE_MODULE}/testing/plugin-harness"', "non-SDK"),
        ):
            try:
                rewrite_go_source(f"package x\n\n{bad}\n", "bad.go")
                errors.append(f"rewrite_go_source accepted a {why} import")
            except SplitError:
                pass

        # 3. README rewrite anchors are asserted, not best-effort.
        try:
            render_readme("# Title\n\nno anchors here at all\n")
            errors.append("render_readme accepted upstream text without the expected anchors")
        except SplitError:
            pass

        # 4. Private-material scanner really fires on a tampered tree.
        tampered = tmp / "tampered"
        generate(tampered, "v0.1.0", None, force=True)
        with (tampered / "README.md").open("a", encoding="utf-8") as fh:
            fh.write("\nsee " + "D:" + "\\Code\\secret\\notes.md and " + "cp_" + "A" * 24 + "\n")
        found = audit_tree(tampered, None)
        if not any("windows-abs-path" in f for f in found):
            errors.append("audit_tree did not catch a windows absolute path")
        if not any("tenant-token" in f for f in found):
            errors.append("audit_tree did not catch a tenant token literal")
        with (tampered / "service.go").open("a", encoding="utf-8") as fh:
            fh.write('\n// import "' + CORE_MODULE + '/internal/store"\n')
        found = audit_tree(tampered, None)
        if not any("private material [core-internal-import]" in f for f in found):
            errors.append("audit_tree did not catch a core internal import reference")

        # 5. Output fencing.
        for bad_out, why in (
            (str(REPO_ROOT), "repository root"),
            (str(REPO_ROOT / ".git"), "git dir"),
        ):
            try:
                resolve_out(bad_out)
                errors.append(f"resolve_out accepted the {why}")
            except SplitError:
                pass
        outside = pathlib.Path(tempfile.gettempdir()).parent / "definitely-outside-cpsplit"
        try:
            resolve_out(str(outside))
            notes.append(f"resolve_out accepted {outside} (allowed only if inside temp/repo)")
        except SplitError:
            pass

        # 6. Local-replace generation + real build (the strongest available check).
        if shutil.which("go") is None:
            notes.append("go toolchain not on PATH: skipped the local-replace build check")
        else:
            local = tmp / "plugin-local"
            generate(local, "v0.1.0", str(REPO_ROOT), force=True)
            problems = audit_tree(local, str(REPO_ROOT))
            if problems:
                errors.extend(f"local tree: {p}" for p in problems)
            status, detail = try_build(local)
            if status != "ok":
                errors.append(f"generated tree did not build with --core-path ({status}): {detail[:400]}")
            else:
                rc, out_text = run_go(local, ["vet", "./..."])
                if rc != 0:
                    errors.append(f"go vet failed in the generated tree: {out_text[:400]}")
                rc, out_text = run_go(local, ["test", "./...", "-count=1"])
                if rc != 0:
                    errors.append(f"go test failed in the generated tree: {out_text[:400]}")
                else:
                    notes.append("generated tree: go build + go vet + go test all PASS (local replace)")
    finally:
        shutil.rmtree(tmp, ignore_errors=True)

    for note in notes:
        print("self-test note: " + note)
    if errors:
        for e in errors:
            print("self-test: " + e)
        print("split self-test FAILED")
        return 1
    print("split self-test PASS (tree/module/import-rewrite/private-material/fencing/build)")
    return 0


def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--self-test", action="store_true")
    parser.add_argument("--out", default=str(pathlib.Path("dist") / "split" / PLUGIN_REPO_SLUG),
                        help="output directory (default: dist/split/<repo slug>, gitignored)")
    parser.add_argument("--core-version", default="v0.1.0",
                        help="core module version to require in the generated go.mod")
    parser.add_argument("--core-path", default="",
                        help="local core checkout to wire in with a replace directive (verification only)")
    parser.add_argument("--force", action="store_true", help="replace an existing output directory")
    parser.add_argument("--no-build", action="store_true", help="skip the post-generation go build attempt")
    parser.add_argument("--require-build", action="store_true",
                        help="exit non-zero unless the generated tree builds")
    args = parser.parse_args(argv)

    if args.self_test:
        return self_test()

    try:
        out_dir = resolve_out(args.out)
        core_path = str(pathlib.Path(args.core_path).expanduser().resolve()) if args.core_path else None
        written = generate(out_dir, args.core_version, core_path, args.force)
    except SplitError as exc:
        print(f"split FAILED: {exc}")
        return 1

    print(f"generated {len(written)} file(s) into {out_dir}")
    for path in written:
        print("  " + path.relative_to(out_dir).as_posix())

    problems = audit_tree(out_dir, core_path)
    if problems:
        print("post-generation audit FAILED:")
        for p in problems:
            print("  " + p)
        return 1
    print("post-generation audit PASS (tree, module path, import rewrite, private-material scan)")

    if not args.no_build:
        status, detail = try_build(out_dir)
        if status == "ok":
            print("go build ./... PASS in the generated tree")
        elif status == "no-go":
            print(f"go build SKIPPED ({detail})")
        elif status == "unresolved-dependency":
            print("go build SKIPPED: the core module is not resolvable from this machine yet.")
            print("  Re-run with --core-path <local core checkout> to build against a local replace,")
            print("  or wait until the core repository is public and tagged "
                  f"{args.core_version}. Detail: {detail[:200]}")
        else:
            print("go build FAILED in the generated tree:")
            print(detail[:2000])
            return 1
        if args.require_build and status != "ok":
            print("--require-build was given but the build status is " + status)
            return 1

    print("")
    print("Next steps are manual and deliberately NOT performed by this script")
    print("(it never pushes, never creates a remote repository, never touches examples/):")
    print(f"  cd {out_dir}")
    if core_path:
        print("  # first: drop the local replace line from go.mod and delete LOCAL_REPLACE.txt")
    print("  git init -b main && git add -A && git commit -m 'feat: split standalone plugin repo'")
    print(f"  gh repo create DeliciousBuding/{PLUGIN_REPO_SLUG} --private --source . --remote origin")
    print("  git push -u origin main")
    print("  # first release: git tag v0.1.0 && git push origin v0.1.0 (triggers release.yml)")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
