#!/usr/bin/env python3
"""Validate a CloudPath plugin manifest and scan Go sources for internal imports.

This is a deliberate, stdlib-only parser for the *current* plugin.yaml shape.
It does not implement general YAML. The known-good manifest is a flat set of
top-level keys; the required fields are plain scalars and the optional fields
(compatibility/permissions/capabilities/requirements) hold indented mappings or
sequences. The parser enforces a safe closure on that shape and rejects, rather
than mis-parses, anything it does not understand.

Checks:
  1. Only the six required top-level scalars are validated for value; a nested
     block is simply ignored at the top level.
  2. Rejects duplicate top-level keys, tabs, empty required scalars, YAML
     multi-document markers, anchors/aliases/tags, and malformed top-level
     lines.
  3. Validates basic values: apiVersion, kind, protocol (and a light check on
     id/version/entrypoint).
  4. Fails if any Go file under --dir imports
     github.com/DeliciousBuding/cloud-path/internal/*.

The authoritative JSON Schema remains spec/plugin-manifest.schema.json in the
cloud-path core repo; this script is a self-contained install-time gate.

Usage:
    python validate_manifest.py [manifest] [--dir root]
    python validate_manifest.py --self-test
"""
import argparse
import os
import re
import shutil
import sys
import tempfile

REQUIRED = ("apiVersion", "kind", "id", "version", "protocol", "entrypoint")
VALID_API = ("plugins.cloudpath.dev/v1alpha1",)
VALID_KINDS = ("Driver", "Application", "Connector")
INTERNAL_PREFIX = "github.com/DeliciousBuding/cloud-path/internal/"

TAB = "\t"
TOP_KEY_RE = re.compile(r"^([A-Za-z0-9_./-]+):(.*)$")
DOC_RE = re.compile(r"^(---|\.\.\.)\s*$")
VERSION_RE = re.compile(r"^\d+(\.\d+)*([-+][0-9A-Za-z.-]+)?$")
PROTOCOL_RE = re.compile(r"^\d+$")
IMPORT_RE = re.compile(r'"(github\.com/DeliciousBuding/cloud-path/internal/[^"]+)"')


def unquote(value):
    value = value.strip()
    if len(value) >= 2 and value[0] == value[-1] and value[0] in ("'", '"'):
        return value[1:-1]
    return value


def strip_inline_comment(value):
    # A YAML comment starts with '#' preceded by whitespace.
    return value.split(" #", 1)[0]


def check_scalar(key, value, line, errors):
    if key == "apiVersion":
        if value not in VALID_API:
            errors.append("line %d: unsupported apiVersion %r" % (line, value))
    elif key == "kind":
        if value not in VALID_KINDS:
            errors.append("line %d: invalid kind %r (want one of %s)" % (line, value, ", ".join(VALID_KINDS)))
    elif key == "id":
        if value != value.strip() or " " in value or ".." in value or "/" in value or "\\" in value:
            errors.append("line %d: invalid plugin id %r" % (line, value))
    elif key == "version":
        if not VERSION_RE.match(value):
            errors.append("line %d: invalid version %r" % (line, value))
    elif key == "protocol":
        if not PROTOCOL_RE.match(value) or int(value) < 1:
            errors.append("line %d: invalid protocol %r (want a positive integer)" % (line, value))
    elif key == "entrypoint":
        if value != value.strip() or " " in value or ".." in value or "/" in value or "\\" in value:
            errors.append("line %d: invalid entrypoint %r" % (line, value))


def validate_manifest_text(text):
    """Return a list of validation errors (empty means OK)."""
    errors = []
    seen = {}  # top-level key -> line number
    for line_no, raw in enumerate(text.split("\n"), start=1):
        if TAB in raw:
            errors.append("line %d: tabs are not allowed in manifest" % line_no)
            continue
        stripped = raw.strip()
        if not stripped or stripped.startswith("#"):
            continue
        if DOC_RE.match(stripped):
            errors.append("line %d: YAML document markers are not allowed" % line_no)
            continue
        if stripped.startswith("%"):
            errors.append("line %d: YAML directives are not allowed" % line_no)
            continue
        if any(ch in stripped for ch in ("&", "*")) or stripped.startswith(("!", "!!")):
            errors.append("line %d: YAML anchors/aliases/tags are not allowed" % line_no)
            continue
        m = TOP_KEY_RE.match(raw)
        if not m:
            if raw and not raw[0].isspace():
                errors.append("line %d: malformed top-level line: %r" % (line_no, raw))
            continue
        key, value = m.group(1), m.group(2)
        if key in seen:
            errors.append("line %d: duplicate top-level key %r" % (line_no, key))
        seen[key] = line_no
        if key in REQUIRED:
            v = unquote(strip_inline_comment(value))
            if not v:
                errors.append("line %d: required field %r is empty" % (line_no, key))
            else:
                check_scalar(key, v, line_no, errors)

    for k in REQUIRED:
        if k not in seen:
            errors.append("missing required field: %s" % k)
    return errors


def scan_internal(root):
    hits = []
    for dirpath, _dirnames, filenames in os.walk(root):
        for name in filenames:
            if not name.endswith(".go"):
                continue
            path = os.path.join(dirpath, name)
            with open(path, "r", encoding="utf-8", errors="replace") as f:
                for line in f:
                    for m in IMPORT_RE.finditer(line):
                        hits.append("%s: %s" % (path, m.group(1)))
    return hits


def valid_manifest_text():
    return (
        "apiVersion: plugins.cloudpath.dev/v1alpha1\n"
        "kind: Driver\n"
        "id: io.github.acme.cloud-path-driver-demo\n"
        "version: 0.1.0\n"
        "protocol: 1\n"
        "entrypoint: cloudpath-driver-demo\n"
        "compatibility:\n"
        '  core: ">=0.2.0 <0.4.0"\n'
        "permissions:\n"
        "  hardware: []\n"
        "capabilities:\n"
        "  - cloudpath.dev/capability/demodriver@1\n"
    )


def self_test():
    base = valid_manifest_text()
    cases = [
        ("valid", base, 0),
        ("missing kind", base.replace("kind: Driver\n", ""), 1),
        ("duplicate id", base + "id: a.b\n", 1),
        ("illegal kind", base.replace("kind: Driver", "kind: Widget"), 1),
        ("empty version", base.replace("version: 0.1.0\n", "version:\n"), 1),
        ("tab", base.replace("kind: Driver", "kind:\tDriver"), 1),
        ("multi-document", "---\n" + base + "---\n", 1),
        ("anchor", base.replace("kind: Driver", "kind: Driver &a"), 1),
        ("invalid protocol", base.replace("protocol: 1", "protocol: abc"), 1),
    ]
    errors = []
    for name, text, want in cases:
        got = validate_manifest_text(text)
        ok = (len(got) == 0)
        if ok != (want == 0):
            errors.append("%s: expected exit %d, got %d (%s)" % (name, want, 0 if ok else 1, got))

    tmp = tempfile.mkdtemp(prefix="cpvalid-")
    try:
        with open(os.path.join(tmp, "bad.go"), "w", encoding="utf-8") as f:
            f.write('package bad\nimport _ "github.com/DeliciousBuding/cloud-path/internal/model"\n')
        hits = scan_internal(tmp)
        if not hits:
            errors.append("internal import scan should have found a hit")
    finally:
        shutil.rmtree(tmp, ignore_errors=True)

    if errors:
        for e in errors:
            print("self-test: " + e)
        print("self-test FAILED")
        return 1
    print("self-test OK")
    return 0


def main():
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("manifest", nargs="?", default="plugin.yaml")
    ap.add_argument("--dir", default=".")
    ap.add_argument("--self-test", action="store_true")
    args = ap.parse_args()

    if args.self_test:
        return self_test()

    ok = True
    try:
        with open(args.manifest, "r", encoding="utf-8") as f:
            text = f.read()
    except OSError as e:
        print("manifest: could not read %s: %s" % (args.manifest, e))
        return 1

    errs = validate_manifest_text(text)
    if errs:
        for e in errs:
            print("manifest: " + e)
        ok = False

    hits = scan_internal(args.dir)
    if hits:
        print("internal import found:")
        for h in hits:
            print("  " + h)
        ok = False

    if ok:
        print("plugin manifest OK; no internal imports")
        return 0
    return 1


if __name__ == "__main__":
    sys.exit(main())
