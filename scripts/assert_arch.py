#!/usr/bin/env python3
"""Assert that a built binary really targets the expected OS/arch.

Why this exists: the production host is a native ``arm64`` (aarch64) Linux
machine with no qemu/binfmt fallback, so shipping an ``amd64`` artifact fails at
exec time with ``exec format error``. This gate turns that class of accident
into a hard, automated failure instead of a human eyeball check.

Detection is done twice, independently:

1. Container header parsing (stdlib only, no toolchain required):
   ELF ``e_machine``, PE COFF ``Machine``, Mach-O ``cputype``.
2. ``go version -m <bin>`` build settings (``GOOS`` / ``GOARCH`` / ``-tags``)
   when a Go toolchain is available.

If both sources are available they must agree; any disagreement, any missing
file, or any expectation mismatch exits non-zero. Warnings never substitute for
a failure.

Usage:
    python scripts/assert_arch.py --expect-os linux --expect-arch arm64 bin/cloudpath-server_linux_arm64
    python scripts/assert_arch.py --expect-os linux --expect-arch arm64 --expect-tags embed_ui bin/server
    python scripts/assert_arch.py --self-test
"""
from __future__ import annotations

import argparse
import os
import pathlib
import shutil
import struct
import subprocess
import sys
import tempfile
from dataclasses import dataclass

# ELF e_machine values (EM_*).
ELF_MACHINES = {
    3: ("linux", "386"),
    40: ("linux", "arm"),
    62: ("linux", "amd64"),
    183: ("linux", "arm64"),
    243: ("linux", "riscv64"),
}

# PE COFF Machine values.
PE_MACHINES = {
    0x014C: ("windows", "386"),
    0x8664: ("windows", "amd64"),
    0xAA64: ("windows", "arm64"),
}

# Mach-O cputype values (64-bit flag included).
MACHO_CPUTYPES = {
    0x01000007: ("darwin", "amd64"),
    0x0100000C: ("darwin", "arm64"),
}


@dataclass(frozen=True)
class Probe:
    """What one detection source says about a binary."""

    source: str
    goos: str | None
    goarch: str | None
    tags: tuple[str, ...]


@dataclass(frozen=True)
class Failure:
    path: str
    reason: str


def _probe_header(data: bytes) -> Probe | None:
    """Parse the executable container header. Returns None when unrecognized."""
    if data[:4] == b"\x7fELF":
        if len(data) < 20:
            return None
        endian = "<" if data[5] == 1 else ">"
        machine = struct.unpack_from(endian + "H", data, 18)[0]
        pair = ELF_MACHINES.get(machine)
        if pair is None:
            return Probe("elf", None, None, ())
        return Probe("elf", pair[0], pair[1], ())
    if data[:2] == b"MZ":
        if len(data) < 0x40:
            return None
        pe_off = struct.unpack_from("<I", data, 0x3C)[0]
        if data[pe_off:pe_off + 4] != b"PE\x00\x00":
            return None
        machine = struct.unpack_from("<H", data, pe_off + 4)[0]
        pair = PE_MACHINES.get(machine)
        if pair is None:
            return Probe("pe", "windows", None, ())
        return Probe("pe", pair[0], pair[1], ())
    magic = data[:4]
    if magic in (b"\xcf\xfa\xed\xfe", b"\xfe\xed\xfa\xcf", b"\xce\xfa\xed\xfe", b"\xfe\xed\xfa\xce"):
        if len(data) < 8:
            return None
        little = magic in (b"\xcf\xfa\xed\xfe", b"\xce\xfa\xed\xfe")
        cputype = struct.unpack_from("<I" if little else ">I", data, 4)[0]
        pair = MACHO_CPUTYPES.get(cputype)
        if pair is None:
            return Probe("macho", "darwin", None, ())
        return Probe("macho", pair[0], pair[1], ())
    return None


def _probe_go(path: str) -> Probe | None:
    """Read GOOS/GOARCH/-tags from ``go version -m`` when Go is installed."""
    if shutil.which("go") is None:
        return None
    proc = subprocess.run(["go", "version", "-m", path], capture_output=True, text=True, errors="replace")
    if proc.returncode != 0:
        return None
    goos = goarch = None
    tags: tuple[str, ...] = ()
    for line in proc.stdout.splitlines():
        # ``go version -m`` indents every record with a leading TAB, so drop
        # empty fields before indexing: ["build", "GOARCH=arm64"].
        parts = [p for p in line.split("\t") if p.strip()]
        if len(parts) < 2:
            continue
        key, value = parts[0].strip(), parts[-1].strip()
        if key == "build" and "=" in value:
            name, _, rest = value.partition("=")
            if name == "GOOS":
                goos = rest
            elif name == "GOARCH":
                goarch = rest
            elif name == "-tags":
                tags = tuple(t for t in rest.split(",") if t)
    if goos is None and goarch is None and not tags:
        return None
    return Probe("go", goos, goarch, tags)


def probe_binary(path: str) -> tuple[Probe | None, Probe | None, Failure | None]:
    """Return (header_probe, go_probe, fatal_failure) for one file."""
    p = pathlib.Path(path)
    if not p.is_file():
        return None, None, Failure(path, "file not found")
    try:
        with p.open("rb") as fh:
            head = fh.read(4096)
    except OSError as exc:
        return None, None, Failure(path, f"unreadable: {exc}")
    header = _probe_header(head)
    if header is None:
        return None, None, Failure(path, "not a recognized ELF/PE/Mach-O executable")
    return header, _probe_go(path), None


def check(path: str, expect_os: str, expect_arch: str, expect_tags: tuple[str, ...]) -> tuple[list[Failure], str]:
    """Validate one binary. Returns (failures, human summary line)."""
    header, goprobe, fatal = probe_binary(path)
    if fatal is not None:
        return [fatal], ""
    failures: list[Failure] = []
    assert header is not None
    observed_os, observed_arch = header.goos, header.goarch

    if goprobe is not None:
        # Cross-check: the two independent sources must agree.
        if goprobe.goos and header.goos and goprobe.goos != header.goos:
            failures.append(Failure(path, f"header says os={header.goos} but go build settings say GOOS={goprobe.goos}"))
        if goprobe.goarch and header.goarch and goprobe.goarch != header.goarch:
            failures.append(Failure(path, f"header says arch={header.goarch} but go build settings say GOARCH={goprobe.goarch}"))
        observed_os = goprobe.goos or header.goos
        observed_arch = goprobe.goarch or header.goarch

    if observed_os is None:
        failures.append(Failure(path, "could not determine target OS"))
    elif observed_os != expect_os:
        failures.append(Failure(path, f"target OS is {observed_os}, expected {expect_os}"))
    if observed_arch is None:
        failures.append(Failure(path, "could not determine target architecture"))
    elif observed_arch != expect_arch:
        failures.append(Failure(path, f"target arch is {observed_arch}, expected {expect_arch}"))

    if expect_tags:
        if goprobe is None:
            failures.append(Failure(path, "--expect-tags requires a Go toolchain (go version -m) but none was usable"))
        else:
            missing = [t for t in expect_tags if t not in goprobe.tags]
            if missing:
                failures.append(Failure(path, f"missing build tags {','.join(missing)} (has: {','.join(goprobe.tags) or 'none'})"))

    src = "header+go" if goprobe is not None else f"header({header.source})"
    summary = f"{path}: os={observed_os} arch={observed_arch} tags={','.join(goprobe.tags) if goprobe else 'n/a'} [{src}]"
    return failures, summary


def _write_fake(kind: str, path: str) -> None:
    """Write a minimal, header-only artifact for self-test detection checks."""
    if kind == "elf-arm64":
        data = b"\x7fELF" + bytes([2, 1, 1, 0]) + b"\x00" * 8 + struct.pack("<HHI", 2, 183, 0)
    elif kind == "elf-amd64":
        data = b"\x7fELF" + bytes([2, 1, 1, 0]) + b"\x00" * 8 + struct.pack("<HHI", 2, 62, 0)
    elif kind == "pe-amd64":
        pe_off = 0x80
        data = b"MZ" + b"\x00" * (0x3C - 2) + struct.pack("<I", pe_off)
        data += b"\x00" * (pe_off - len(data)) + b"PE\x00\x00" + struct.pack("<H", 0x8664) + b"\x00" * 64
    elif kind == "pe-arm64":
        pe_off = 0x80
        data = b"MZ" + b"\x00" * (0x3C - 2) + struct.pack("<I", pe_off)
        data += b"\x00" * (pe_off - len(data)) + b"PE\x00\x00" + struct.pack("<H", 0xAA64) + b"\x00" * 64
    elif kind == "macho-arm64":
        data = b"\xcf\xfa\xed\xfe" + struct.pack("<I", 0x0100000C) + b"\x00" * 64
    else:
        raise ValueError(kind)
    with open(path, "wb") as fh:
        fh.write(data)


def self_test() -> int:
    cases = {
        "elf-arm64": ("linux", "arm64"),
        "elf-amd64": ("linux", "amd64"),
        "pe-amd64": ("windows", "amd64"),
        "pe-arm64": ("windows", "arm64"),
        "macho-arm64": ("darwin", "arm64"),
    }
    errors: list[str] = []
    tmp = tempfile.mkdtemp(prefix="cparch-")
    try:
        for kind, (want_os, want_arch) in cases.items():
            path = os.path.join(tmp, kind)
            _write_fake(kind, path)
            header, _, fatal = probe_binary(path)
            if fatal is not None:
                errors.append(f"{kind}: unexpected fatal {fatal.reason}")
                continue
            assert header is not None
            if (header.goos, header.goarch) != (want_os, want_arch):
                errors.append(f"{kind}: detected {header.goos}/{header.goarch}, want {want_os}/{want_arch}")
            ok, _ = check(path, want_os, want_arch, ())
            if ok:
                errors.append(f"{kind}: matching expectation reported failures")
            bad, _ = check(path, "plan9", "s390x", ())
            if not bad:
                errors.append(f"{kind}: mismatching expectation was not caught")

        junk = os.path.join(tmp, "junk")
        with open(junk, "wb") as fh:
            fh.write(b"this is not an executable at all\n")
        failures, _ = check(junk, "linux", "arm64", ())
        if not failures:
            errors.append("junk file: not recognized as a failure")

        missing = os.path.join(tmp, "does-not-exist")
        failures, _ = check(missing, "linux", "arm64", ())
        if not failures:
            errors.append("missing file: not recognized as a failure")
    finally:
        shutil.rmtree(tmp, ignore_errors=True)

    if errors:
        for e in errors:
            print("self-test: " + e)
        print("assert_arch self-test FAILED")
        return 1
    print(f"assert_arch self-test PASS ({len(cases)} container formats + junk/missing cases)")
    return 0


def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--self-test", action="store_true", help="run built-in detection self-test")
    parser.add_argument("--expect-os", help="required target GOOS, e.g. linux / windows / darwin")
    parser.add_argument("--expect-arch", help="required target GOARCH, e.g. arm64 / amd64")
    parser.add_argument("--expect-tags", default="", help="comma-separated build tags that must be present (needs Go toolchain)")
    parser.add_argument("paths", nargs="*", help="binaries to assert")
    args = parser.parse_args(argv)

    if args.self_test:
        return self_test()
    if not args.paths:
        parser.error("no binaries given (or use --self-test)")
    if not args.expect_os or not args.expect_arch:
        parser.error("--expect-os and --expect-arch are required")

    expect_tags = tuple(t.strip() for t in args.expect_tags.split(",") if t.strip())
    all_failures: list[Failure] = []
    for path in args.paths:
        failures, summary = check(path, args.expect_os, args.expect_arch, expect_tags)
        if summary:
            print("  OK  " + summary if not failures else "  ??  " + summary)
        all_failures.extend(failures)

    if all_failures:
        print(f"arch assertion FAILED ({len(all_failures)} problem(s)):")
        for f in all_failures:
            print(f"  {f.path}: {f.reason}")
        return 1
    print(f"arch assertion PASS ({len(args.paths)} binary(ies), expect {args.expect_os}/{args.expect_arch}"
          + (f", tags {','.join(expect_tags)}" if expect_tags else "") + ")")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
