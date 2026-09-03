#!/usr/bin/env python3
"""Rename a CloudPath plugin template into a real plugin.

This is the single step a developer runs after copying a template (driver/ or
application/) into a new plugin repository. It rewrites the plugin id, the Go
module path, the binary name, the title and the capability literal across file
contents, and renames the well-known cmd/<binary> directory.

It is stdlib-only and never invokes git or the GitHub CLI.

Usage:
    python rename.py --dir <template-dir> \
        --plugin-id io.github.acme.cloud-path-driver-foo \
        --module github.com/acme/cloud-path-driver-foo \
        --binary cloudpath-driver-foo \
        --title "ACME Foo Driver"

    # run the built-in smoke test
    python rename.py --self-test
"""
import argparse
import os
import re
import shutil
import sys
import tempfile

# Literal defaults that are rewritten. The template ships these exact strings.
DEFAULTS = {
    "driver": {
        "module": "github.com/DeliciousBuding/cloud-path-plugin-template-go/driver",
        "binary": "cloudpath-driver-template",
        "id": "io.github.deliciousbuding.cloud-path-driver-template",
        "title": "Driver Template",
        "capability": "cloudpath.dev/capability/drivertemplate@1",
    },
    "application": {
        "module": "github.com/DeliciousBuding/cloud-path-plugin-template-go/application",
        "binary": "cloudpath-app-template",
        "id": "io.github.deliciousbuding.cloud-path-app-template",
        "title": "Application Template",
        "capability": "cloudpath.dev/capability/drivertemplate@1",
    },
}

MODULE_RE = re.compile(r"^[A-Za-z0-9_.\-]+(?:/[A-Za-z0-9_.\-]+)+$")
IDENT_RE = re.compile(r"^[A-Za-z0-9._\-]+$")
PLUGIN_ID_RE = re.compile(r"^[a-z0-9._\-]+(?:\.[a-z0-9._\-]+)+$")


def fail(msg, code=2):
    print("rename.py: error: " + msg, file=sys.stderr)
    sys.exit(code)


def validate(field, value):
    if value is None or (isinstance(value, str) and not value.strip()):
        fail("empty value for --" + field)
    if ".." in value:
        fail("path-traversal value rejected for --" + field + ": " + value)
    if value.startswith("/") or value.startswith("\\"):
        fail("absolute path rejected for --" + field + ": " + value)
    if "\x00" in value:
        fail("NUL byte rejected for --" + field)
    if field == "module":
        if not MODULE_RE.match(value):
            fail("invalid Go module path for --module: " + value)
    elif field == "binary":
        if not IDENT_RE.match(value):
            fail("invalid binary name for --binary: " + value)
    elif field == "plugin_id":
        if not PLUGIN_ID_RE.match(value):
            fail("invalid plugin id for --plugin-id: " + value)
    return value


def detect_kind(dirpath):
    for path in (dirpath, os.path.join(dirpath, "driver"), os.path.join(dirpath, "application")):
        manifest = os.path.join(path, "plugin.yaml")
        if os.path.isfile(manifest):
            with open(manifest, "r", encoding="utf-8", errors="replace") as f:
                text = f.read()
            if "kind: Driver" in text:
                return "driver"
            if "kind: Application" in text:
                return "application"
    # Fall back to directory name heuristics.
    name = os.path.basename(os.path.abspath(dirpath))
    if "application" in name:
        return "application"
    return "driver"


def replacements(kind, args):
    d = DEFAULTS[kind]
    caps = args.capability if args.capability else d["capability"]
    table = {
        d["module"]: args.module,
        d["binary"]: args.binary,
        d["id"]: args.plugin_id,
        d["title"]: args.title,
        d["capability"]: caps,
    }
    if args.description:
        table[description_placeholder(kind)] = args.description
    return table


def description_placeholder(kind):
    # A couple of stable English descriptions used in the template READMEs.
    if kind == "driver":
        return "Driver for the template simulated device"
    return "Application that reacts to a single bound capability"


def is_text(path):
    suffixes = (".go", ".md", ".yaml", ".yml", ".json", ".mod", ".sum", ".txt", ".toml")
    if path.endswith(suffixes):
        return True
    # Skip obvious binaries.
    with open(path, "rb") as f:
        return b"\x00" not in f.read(8192)


def apply_rename(root, table):
    # Rename path components whose basename equals a template default binary.
    for dirpath, dirnames, filenames in os.walk(root, topdown=False):
        for old in list(dirnames) + list(filenames):
            if old in table:
                new = table[old]
                src = os.path.join(dirpath, old)
                dst = os.path.join(dirpath, new)
                if not os.path.exists(dst):
                    os.rename(src, dst)

    # Rewrite file contents.
    for dirpath, dirnames, filenames in os.walk(root):
        for name in filenames:
            path = os.path.join(dirpath, name)
            if not is_text(path):
                continue
            try:
                with open(path, "r", encoding="utf-8") as f:
                    text = f.read()
            except UnicodeDecodeError:
                continue
            original = text
            for old, new in table.items():
                text = text.replace(old, new)
            if text != original:
                with open(path, "w", encoding="utf-8", newline="") as f:
                    f.write(text)


def rebuild_go_mod(root, module):
    gomod = os.path.join(root, "go.mod")
    if not os.path.isfile(gomod):
        return
    with open(gomod, "r", encoding="utf-8") as f:
        lines = f.readlines()
    out = []
    for line in lines:
        if line.startswith("module "):
            out.append("module " + module + "\n")
        else:
            out.append(line)
    with open(gomod, "w", encoding="utf-8", newline="") as f:
        f.writelines(out)


def resolve_template_dir(dirspec):
    # dirspec may point at the template root or at a parent containing driver/application.
    if os.path.isfile(os.path.join(dirspec, "plugin.yaml")):
        return dirspec
    for c in ("driver", "application"):
        p = os.path.join(dirspec, c)
        if os.path.isfile(os.path.join(p, "plugin.yaml")):
            return p
    return dirspec


def self_test():
    # Locate a template fixture relative to this script (either the parent dir
    # when the script lives in a template's scripts/, or a sibling template).
    script_dir = os.path.dirname(os.path.abspath(__file__))
    template_root = resolve_template_dir(os.path.dirname(script_dir))

    tmp = tempfile.mkdtemp(prefix="cprename-")
    try:
        fixture = os.path.join(tmp, "tmpl")
        shutil.copytree(template_root, fixture, ignore=shutil.ignore_patterns("*.exe", "*.hex", "*.lib"))

        kind = detect_kind(fixture)
        args = argparse.Namespace(
            plugin_id="io.github.acme.cloud-path-driver-foo",
            module="github.com/acme/cloud-path-driver-foo",
            binary="cloudpath-driver-foo",
            title="ACME Foo Driver",
            capability="cloudpath.dev/capability/acmefoo@1",
            description=None,
        )
        apply_rename(fixture, replacements(kind, args))
        rebuild_go_mod(fixture, args.module)

        manifest = os.path.join(fixture, "plugin.yaml")
        with open(manifest, "r", encoding="utf-8") as f:
            assert args.plugin_id in f.read(), "plugin id was not rewritten"

        # go.mod module line must be updated.
        gomod = os.path.join(fixture, "go.mod")
        if os.path.isfile(gomod):
            with open(gomod, "r", encoding="utf-8") as f:
                assert "module " + args.module in f.read(), "go.mod module line not updated"

        # A Go source file import must reflect the new module path.
        go_import_found = False
        for dirpath, _dirnames, filenames in os.walk(fixture):
            for name in filenames:
                if name.endswith(".go"):
                    with open(os.path.join(dirpath, name), "r", encoding="utf-8") as f:
                        if args.module in f.read():
                            go_import_found = True
        assert go_import_found, "Go import path was not rewritten"

        # The cmd/<binary> directory must have been renamed.
        cmd_binary = os.path.join(fixture, "cmd", args.binary)
        assert os.path.isdir(cmd_binary), "cmd/%s directory missing" % args.binary

        # Rejection checks.
        for field_value in (
            ("--plugin-id", "../escape"),
            ("--plugin-id", ""),
            ("--module", "../escape"),
            ("--binary", "../../x"),
        ):
            try:
                ns = argparse.Namespace(
                    plugin_id=args.plugin_id, module=args.module, binary=args.binary,
                    title=args.title, capability=args.capability, description=None,
                )
                setattr(ns, field_value[0][2:].replace("-", "_"), field_value[1])
                validate(field_value[0][2:].replace("-", "_"), getattr(ns, field_value[0][2:].replace("-", "_")))
                raise AssertionError("value %r was not rejected" % (field_value,))
            except SystemExit:
                pass
            except AssertionError as e:
                raise

        print("rename.py: self-test OK")
        return 0
    finally:
        shutil.rmtree(tmp, ignore_errors=True)


def main():
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--dir", default=None, help="template directory (default: script parent)")
    ap.add_argument("--plugin-id", default=None)
    ap.add_argument("--module", default=None)
    ap.add_argument("--binary", default=None)
    ap.add_argument("--title", default=None)
    ap.add_argument("--capability", default=None)
    ap.add_argument("--description", default=None)
    ap.add_argument("--self-test", action="store_true")
    args = ap.parse_args()

    if args.self_test:
        return self_test()

    for field in ("plugin_id", "module", "binary", "title"):
        if getattr(args, field) is None:
            fail("missing --" + field.replace("_", "-"))
        setattr(args, field, validate(field, getattr(args, field)))

    root = args.dir or os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
    root = resolve_template_dir(os.path.abspath(root))
    kind = detect_kind(root)

    table = replacements(kind, args)
    apply_rename(root, table)
    rebuild_go_mod(root, args.module)
    print("rename.py: renamed template in " + root)
    return 0


if __name__ == "__main__":
    sys.exit(main())
