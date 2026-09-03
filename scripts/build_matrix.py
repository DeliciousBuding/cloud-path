#!/usr/bin/env python3
"""Reproducible release build matrix for CloudPath artifacts.

Builds every shipped binary for every supported platform, then asserts each
artifact really has the target OS/arch (see ``scripts/assert_arch.py``) and
writes ``checksums.txt``. Assertion failure is fatal: a mis-targeted artifact
never reaches a release or a production host.

Artifact naming (also documented in CHANGELOG.md and deploy/README.md):

    cloudpath-server_<version>_<os>_<arch>[.exe]
    cloudpath-edge_<version>_<os>_<arch>[.exe]
    cloudpath_<version>_<os>_<arch>[.exe]

The server embeds the WebUI, so ``webui/dist`` must exist before this script
runs (``pnpm -C webui build``); the script fails hard instead of silently
producing an API-only server.

Usage:
    python scripts/build_matrix.py --version v0.1.0 --out dist
    python scripts/build_matrix.py --version dev --out dist --only server --platforms linux/arm64
    python scripts/build_matrix.py --verify-only --out dist    # re-assert existing artifacts
    python scripts/build_matrix.py --self-test
"""
from __future__ import annotations

import argparse
import hashlib
import os
import pathlib
import re
import shutil
import subprocess
import sys

REPO_ROOT = pathlib.Path(__file__).resolve().parent.parent
SCRIPTS_DIR = REPO_ROOT / "scripts"

# Full support matrix. Every OS ships both amd64 and arm64 because edge clients
# run on the users' own machines (Windows/macOS/Linux laptops).
PLATFORMS: tuple[tuple[str, str], ...] = (
    ("linux", "arm64"),
    ("linux", "amd64"),
    ("windows", "amd64"),
    ("windows", "arm64"),
    ("darwin", "amd64"),
    ("darwin", "arm64"),
)

# Binary name -> Go package. The server needs the embed_ui build tag.
TARGETS: dict[str, tuple[str, bool]] = {
    "server": ("./cmd/cloudpath-server", True),
    "edge": ("./cmd/cloudpath-edge", False),
    "cli": ("./cmd/cloudpath", False),
}

BINARY_BASENAME = {"server": "cloudpath-server", "edge": "cloudpath-edge", "cli": "cloudpath"}

BINARY_BASENAME_INVERSE = {v: k for k, v in BINARY_BASENAME.items()}

NAME_RE = re.compile(r"^(cloudpath-server|cloudpath-edge|cloudpath)_.+_(linux|windows|darwin)_(amd64|arm64)(\.exe)?$")


def artifact_name(target: str, version: str, goos: str, goarch: str) -> str:
    # Sanitize hard: the version string ends up in a file name, so it must not be
    # able to carry path separators, ".." traversal or leading/trailing dots.
    safe_version = re.sub(r"[^A-Za-z0-9._+-]", "-", version.strip() or "dev") or "dev"
    safe_version = re.sub(r"\.{2,}", ".", safe_version).strip(".-") or "dev"
    ext = ".exe" if goos == "windows" else ""
    return f"{BINARY_BASENAME[target]}_{safe_version}_{goos}_{goarch}{ext}"


def sha256_file(path: pathlib.Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as fh:
        for chunk in iter(lambda: fh.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def build_one(target: str, version: str, goos: str, goarch: str, out_dir: pathlib.Path,
              embed_tags: bool, quiet: bool) -> pathlib.Path:
    pkg, needs_ui = TARGETS[target]
    name = artifact_name(target, version, goos, goarch)
    out_path = out_dir / name
    tags = ["embed_ui"] if (needs_ui and embed_tags) else []
    cmd = ["go", "build"]
    if tags:
        cmd += ["-tags", ",".join(tags)]
    cmd += ["-trimpath", "-ldflags", f"-s -w -X main.version={version}", "-o", str(out_path), pkg]
    env = dict(os.environ)
    env.update({"GOOS": goos, "GOARCH": goarch, "CGO_ENABLED": "0"})
    if not quiet:
        print(f"  build {name}")
    proc = subprocess.run(cmd, cwd=str(REPO_ROOT), env=env, capture_output=True, text=True, errors="replace")
    if proc.returncode != 0:
        sys.stderr.write((proc.stderr or proc.stdout or "").strip() + "\n")
        raise SystemExit(f"build failed: {name} (exit {proc.returncode})")
    return out_path


def assert_artifacts(artifacts: list[tuple[pathlib.Path, str, str]], embed_tags: bool) -> None:
    """Hard-fail when any artifact does not match its intended OS/arch."""
    sys.path.insert(0, str(SCRIPTS_DIR))
    import assert_arch  # noqa: PLC0415  (sibling script, stdlib only)

    failures: list[str] = []
    for path, goos, goarch in artifacts:
        expect_tags = ("embed_ui",) if (embed_tags and "cloudpath-server" in path.name) else ()
        found, _summary = assert_arch.check(str(path), goos, goarch, expect_tags)
        for f in found:
            failures.append(f"{f.path}: {f.reason}")
        if not found:
            print(f"  assert OK  {path.name} -> {goos}/{goarch}" + (f" tags={','.join(expect_tags)}" if expect_tags else ""))
    if failures:
        print("artifact architecture assertion FAILED:")
        for line in failures:
            print("  " + line)
        raise SystemExit(1)


def write_checksums(out_dir: pathlib.Path, artifacts: list[pathlib.Path]) -> pathlib.Path:
    path = out_dir / "checksums.txt"
    lines = [f"{sha256_file(p)}  {p.name}" for p in sorted(artifacts, key=lambda x: x.name)]
    path.write_text("\n".join(lines) + "\n", encoding="utf-8", newline="\n")
    return path


def require_webui_dist() -> None:
    index = REPO_ROOT / "webui" / "dist" / "index.html"
    if not index.is_file():
        raise SystemExit(
            "webui/dist/index.html not found.\n"
            "The server embeds the WebUI, so build the frontend first:\n"
            "    pnpm -C webui install --frozen-lockfile && pnpm -C webui build\n"
            "or run: task build:matrix (which builds the WebUI for you)."
        )


def parse_artifact_name(name: str) -> tuple[str, str, str] | None:
    """Recover (version, goos, goarch) from a documented artifact file name."""
    stem = name[:-4] if name.endswith(".exe") else name
    m = re.match(r"^(cloudpath-server|cloudpath-edge|cloudpath)_(.+)_(linux|windows|darwin)_(amd64|arm64)$", stem)
    if not m:
        return None
    base, version, goos, goarch = m.groups()
    if BINARY_BASENAME_INVERSE.get(base) is None:
        return None
    if name.endswith(".exe") and goos != "windows":
        return None
    return version, goos, goarch


def verify_only(out_dir: pathlib.Path, require_checksums: bool) -> int:
    """Assert every artifact already present in ``out_dir``; write checksums.txt.

    Also enforces the release invariant that a Linux arm64 server exists, since
    the production host is native arm64 with no emulation fallback.
    """
    if not out_dir.is_dir():
        print(f"verify-only FAILED: {out_dir} does not exist")
        return 1
    artifacts: list[tuple[pathlib.Path, str, str]] = []
    unnamed: list[str] = []
    for path in sorted(out_dir.iterdir()):
        if not path.is_file() or path.name == "checksums.txt":
            continue
        parsed = parse_artifact_name(path.name)
        if parsed is None:
            unnamed.append(path.name)
            continue
        _version, goos, goarch = parsed
        artifacts.append((path, goos, goarch))

    if not artifacts:
        print(f"verify-only FAILED: no recognized artifacts in {out_dir}")
        return 1
    if unnamed:
        print("verify-only FAILED: files do not match the documented naming convention:")
        for name in unnamed:
            print("  " + name)
        return 1

    assert_artifacts(artifacts, True)

    have = {(g, a) for _p, g, a in artifacts}
    if ("linux", "arm64") not in have:
        print("verify-only FAILED: linux/arm64 artifacts are mandatory (native arm64 production host)")
        return 1
    if not any(p.name.startswith("cloudpath-server_") and g == "linux" and a == "arm64" for p, g, a in artifacts):
        print("verify-only FAILED: cloudpath-server linux/arm64 artifact is mandatory")
        return 1

    checksums = out_dir / "checksums.txt"
    if checksums.is_file():
        expected = {line.split("  ", 1)[1]: line.split("  ", 1)[0]
                    for line in checksums.read_text(encoding="utf-8").splitlines() if "  " in line}
        bad = [name for name, digest in expected.items()
               if not (out_dir / name).is_file() or sha256_file(out_dir / name) != digest]
        stale = sorted({p.name for p, _, _ in artifacts} - set(expected))
        if bad or stale:
            print("verify-only FAILED: checksums.txt does not match the artifacts")
            for name in bad:
                print(f"  mismatch or missing: {name}")
            for name in stale:
                print(f"  not listed: {name}")
            return 1
        print(f"checksums verified: {len(expected)} entries")
    elif require_checksums:
        cp = write_checksums(out_dir, [p for p, _, _ in artifacts])
        print(f"checksums written: {cp.name} ({len(artifacts)} entries)")

    print(f"verify-only PASS ({len(artifacts)} artifact(s), linux/arm64 present)")
    return 0


def self_test() -> int:
    errors: list[str] = []

    # 1. Every OS must ship arm64 (the production host is native arm64).
    for goos in ("linux", "windows", "darwin"):
        arches = {a for o, a in PLATFORMS if o == goos}
        if "arm64" not in arches:
            errors.append(f"platform matrix missing arm64 for {goos}")
        if "amd64" not in arches:
            errors.append(f"platform matrix missing amd64 for {goos}")

    # 2. All three binaries are in the matrix.
    for target in ("server", "edge", "cli"):
        if target not in TARGETS:
            errors.append(f"target missing from matrix: {target}")

    # 3. Naming convention round-trips and matches the documented pattern.
    for target in TARGETS:
        for goos, goarch in PLATFORMS:
            name = artifact_name(target, "v0.1.0", goos, goarch)
            if not NAME_RE.match(name):
                errors.append(f"artifact name does not match convention: {name}")
    if artifact_name("server", "v0.1.0", "windows", "arm64") != "cloudpath-server_v0.1.0_windows_arm64.exe":
        errors.append("windows artifact must end with .exe")
    if artifact_name("edge", "v0.1.0", "linux", "arm64") != "cloudpath-edge_v0.1.0_linux_arm64":
        errors.append("linux artifact must not end with .exe")
    if ".." in artifact_name("server", "a/../b", "linux", "arm64"):
        errors.append("version must be sanitized against path traversal")

    # 4. The WebUI precondition really triggers (run from a temp copy of the tree).
    import tempfile
    import types

    tmp = pathlib.Path(tempfile.mkdtemp(prefix="cpmatrix-"))
    try:
        fake_root = tmp / "repo"
        (fake_root / "webui").mkdir(parents=True)
        saved = dict(globals())
        try:
            globals()["REPO_ROOT"] = fake_root
            try:
                require_webui_dist()
                errors.append("require_webui_dist did not fail when webui/dist is missing")
            except SystemExit:
                pass
            (fake_root / "webui" / "dist").mkdir(parents=True)
            (fake_root / "webui" / "dist" / "index.html").write_text("<html></html>", encoding="utf-8")
            require_webui_dist()  # must not raise now
        finally:
            globals().update({k: v for k, v in saved.items() if k in ("REPO_ROOT",)})
    finally:
        shutil.rmtree(tmp, ignore_errors=True)

    # 5. checksums.txt format is the sha256sum(1) two-space convention.
    tmp2 = pathlib.Path(tempfile.mkdtemp(prefix="cpmatrix-sum-"))
    try:
        f = tmp2 / "a.bin"
        f.write_bytes(b"hello")
        cp = write_checksums(tmp2, [f])
        content = cp.read_text(encoding="utf-8")
        if not re.match(r"^[0-9a-f]{64}  a\.bin\n$", content):
            errors.append(f"checksums.txt has unexpected format: {content!r}")
    finally:
        shutil.rmtree(tmp2, ignore_errors=True)

    del types
    if errors:
        for e in errors:
            print("self-test: " + e)
        print("build_matrix self-test FAILED")
        return 1
    print(f"build_matrix self-test PASS ({len(PLATFORMS)} platforms x {len(TARGETS)} binaries, naming + preconditions)")
    return 0


def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--self-test", action="store_true")
    parser.add_argument("--version", default="dev", help="version stamped into -ldflags and file names")
    parser.add_argument("--out", default="dist", help="output directory (gitignored)")
    parser.add_argument("--only", default="", help="comma-separated subset of: server,edge,cli")
    parser.add_argument("--platforms", default="", help="comma-separated subset like linux/arm64,darwin/arm64")
    parser.add_argument("--no-checksums", action="store_true")
    parser.add_argument("--no-ui-embed", action="store_true", help="build server without embed_ui (API-only; not for release)")
    parser.add_argument("--quiet", action="store_true")
    parser.add_argument("--verify-only", action="store_true",
                        help="do not build; assert artifacts already present in --out")
    args = parser.parse_args(argv)

    if args.self_test:
        return self_test()

    if args.verify_only:
        out = (REPO_ROOT / args.out).resolve()
        if REPO_ROOT not in out.parents:
            raise SystemExit(f"--out must stay inside the repository: {out}")
        return verify_only(out, not args.no_checksums)

    if shutil.which("go") is None:
        raise SystemExit("go toolchain not found on PATH")

    targets = [t.strip() for t in args.only.split(",") if t.strip()] or list(TARGETS)
    for t in targets:
        if t not in TARGETS:
            raise SystemExit(f"unknown --only target {t!r} (choose from {','.join(TARGETS)})")

    if args.platforms:
        wanted = []
        for item in args.platforms.split(","):
            item = item.strip()
            if not item:
                continue
            if "/" not in item:
                raise SystemExit(f"--platforms entries must look like linux/arm64, got {item!r}")
            goos, goarch = item.split("/", 1)
            if (goos, goarch) not in PLATFORMS:
                raise SystemExit(f"unsupported platform {item} (supported: {', '.join(f'{o}/{a}' for o, a in PLATFORMS)})")
            wanted.append((goos, goarch))
        platforms = tuple(wanted)
    else:
        platforms = PLATFORMS

    if "server" in targets and not args.no_ui_embed:
        require_webui_dist()

    out_dir = (REPO_ROOT / args.out).resolve()
    if REPO_ROOT not in out_dir.parents:
        raise SystemExit(f"--out must stay inside the repository: {out_dir}")
    out_dir.mkdir(parents=True, exist_ok=True)

    built: list[tuple[pathlib.Path, str, str]] = []
    for target in targets:
        for goos, goarch in platforms:
            path = build_one(target, args.version, goos, goarch, out_dir, not args.no_ui_embed, args.quiet)
            built.append((path, goos, goarch))

    print(f"built {len(built)} artifact(s) into {out_dir}")
    assert_artifacts(built, not args.no_ui_embed)

    if not args.no_checksums:
        cp = write_checksums(out_dir, [p for p, _, _ in built])
        print(f"checksums: {cp.relative_to(REPO_ROOT)} ({len(built)} entries)")
    print("build matrix PASS")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
