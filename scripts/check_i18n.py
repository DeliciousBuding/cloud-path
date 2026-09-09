#!/usr/bin/env python3
"""Quality gate for CloudPath's frontend i18n resources.

The gate intentionally has no third-party dependencies: it runs in the same
Python-only jobs as the repository's other structural checks. It enforces three
things that ordinary TypeScript tests cannot protect by themselves:

* every locale exposes the same canonical key tree (i18next plural suffixes are
  treated as variants of one logical key);
* translations are not empty, do not drop interpolation placeholders, and do
  not leave an English value copied from Chinese (a "pseudo translation");
* new hard-coded CJK UI literals in ``webui/src`` are rejected relative to a
  git baseline. Existing migration debt is reported as a count, not a failure.

The hard-coded-CJK check is a ratchet, not a claim that the old strings are
good. As migration proceeds, remove the strings from source; the baseline then
naturally shrinks. An intentional non-UI exception must carry an inline
``i18n-ignore: reason`` comment on the same or previous line.

Usage:
    python scripts/check_i18n.py
    python scripts/check_i18n.py --base-ref main
    python scripts/check_i18n.py --all          # audit existing debt too
    python scripts/check_i18n.py --self-test
"""
from __future__ import annotations

import argparse
import dataclasses
import hashlib
import pathlib
import re
import subprocess
import sys
from collections.abc import Iterable, Iterator

REPO_ROOT = pathlib.Path(__file__).resolve().parent.parent
WEBUI_SRC = REPO_ROOT / "webui" / "src"
LOCALES_DIR = WEBUI_SRC / "i18n" / "locales"
DEFAULT_LOCALE = "zh-CN"
ENGLISH_LOCALE = "en-US"

CJK_RE = re.compile(r"[\u3400-\u4dbf\u4e00-\u9fff\uf900-\ufaff]")
PLURAL_SUFFIX_RE = re.compile(r"_(zero|one|two|few|many|other)$")
PLACEHOLDER_RE = re.compile(r"\{\{\s*[^{}]+\s*\}\}")
IDENT_RE = re.compile(r"[A-Za-z_$][A-Za-z0-9_$-]*")
SOURCE_SUFFIXES = {".ts", ".tsx", ".js", ".jsx"}

# These keys are deliberately identical or deliberately contain Chinese in the
# English bundle. Keep this list tiny and explain each entry next to the key.
IDENTICAL_TRANSLATION_ALLOWLIST = {
    "common.app.name": "product name",
    "common.actions.none": "punctuation-only empty value",
    "common.language.zhCN": "language self-name",
    "common.language.enUS": "language self-name",
}
ENGLISH_CJK_ALLOWLIST = {
    "common.language.zhCN": "language self-name is intentionally shown in Chinese",
}

# The i18n implementation and its resources are the place where CJK belongs.
# Tests and CSS are not shipped UI source. All other source files are scanned.
IGNORED_SOURCE_PREFIXES = (
    ("webui/src/i18n/locales/", "translation resource files"),
    ("webui/src/test/", "test harness"),
)
IGNORED_SOURCE_MARKERS = (
    ("/__tests__/", "test fixtures and assertions"),
    (".test.ts", "test file"),
    (".test.tsx", "test file"),
)


@dataclasses.dataclass(frozen=True)
class Finding:
    path: str
    line: int | None
    message: str

    def render(self) -> str:
        location = f":{self.line}" if self.line is not None else ""
        return f"{self.path}{location}: {self.message}"


@dataclasses.dataclass(frozen=True)
class CjkLiteral:
    line: int
    text: str

    @property
    def fingerprint(self) -> str:
        normalized = re.sub(r"\s+", " ", self.text).strip()
        return hashlib.sha256(normalized.encode("utf-8")).hexdigest()


class ParseError(ValueError):
    """Raised when a locale file is not the small object literal we support."""


class ObjectParser:
    """Small, deliberately strict parser for ``export default { ... }`` files.

    It supports strings, nested objects and arrays, comments, trailing commas and
    the TypeScript ``as const`` suffix. Translation resources should not need
    arbitrary TypeScript, so unsupported syntax fails loudly instead of being
    silently skipped.
    """

    def __init__(self, text: str, path: str) -> None:
        self.text = text
        self.path = path
        self.pos = 0

    def parse(self) -> object:
        match = re.search(r"\bexport\s+default\b", self.text)
        if not match:
            self._error("missing `export default`")
        self.pos = match.end()
        self._skip_trivia()
        value = self._parse_value()
        self._skip_trivia()
        if self.text.startswith("as", self.pos):
            self.pos += 2
            self._skip_trivia()
        return value

    def _parse_value(self) -> object:
        self._skip_trivia()
        ch = self._peek()
        if ch == "{":
            return self._parse_object()
        if ch == "[":
            return self._parse_array()
        if ch in {"'", '"', "`"}:
            return self._parse_string()
        self._error("expected a string, object or array value")

    def _parse_object(self) -> dict[str, object]:
        self._expect("{")
        result: dict[str, object] = {}
        while True:
            self._skip_trivia()
            if self._peek() == "}":
                self.pos += 1
                return result
            key = self._parse_key()
            self._skip_trivia()
            self._expect(":")
            value = self._parse_value()
            if key in result:
                self._error(f"duplicate key {key!r}")
            result[key] = value
            self._skip_trivia()
            if self._peek() == ",":
                self.pos += 1
                continue
            if self._peek() == "}":
                continue
            self._error("expected `,` or `}` in object")

    def _parse_array(self) -> list[object]:
        self._expect("[")
        result: list[object] = []
        while True:
            self._skip_trivia()
            if self._peek() == "]":
                self.pos += 1
                return result
            result.append(self._parse_value())
            self._skip_trivia()
            if self._peek() == ",":
                self.pos += 1
                continue
            if self._peek() == "]":
                continue
            self._error("expected `,` or `]` in array")

    def _parse_key(self) -> str:
        self._skip_trivia()
        if self._peek() in {"'", '"', "`"}:
            return self._parse_string()
        match = IDENT_RE.match(self.text, self.pos)
        if not match:
            self._error("expected an object key")
        self.pos = match.end()
        return match.group(0)

    def _parse_string(self) -> str:
        quote = self._peek()
        if quote not in {"'", '"', "`"}:
            self._error("expected a string")
        self.pos += 1
        out: list[str] = []
        while True:
            ch = self._peek()
            if ch is None:
                self._error("unterminated string")
            if ch == quote:
                self.pos += 1
                return "".join(out)
            if quote == "`" and ch == "$" and self._peek(1) == "{":
                self._error("template interpolation is not supported in translation resources")
            if ch != "\\":
                out.append(ch)
                self.pos += 1
                continue
            self.pos += 1
            esc = self._peek()
            if esc is None:
                self._error("unterminated escape")
            simple = {"n": "\n", "r": "\r", "t": "\t", "b": "\b", "f": "\f", "v": "\v", "0": "\0"}
            if esc in simple:
                out.append(simple[esc])
                self.pos += 1
            elif esc in {"\\", "'", '"', "`", "/"}:
                out.append(esc)
                self.pos += 1
            elif esc == "u":
                digits = self.text[self.pos + 1 : self.pos + 5]
                if not re.fullmatch(r"[0-9a-fA-F]{4}", digits):
                    self._error("invalid unicode escape")
                out.append(chr(int(digits, 16)))
                self.pos += 5
            elif esc == "x":
                digits = self.text[self.pos + 1 : self.pos + 3]
                if not re.fullmatch(r"[0-9a-fA-F]{2}", digits):
                    self._error("invalid hex escape")
                out.append(chr(int(digits, 16)))
                self.pos += 3
            else:
                out.append(esc)
                self.pos += 1

    def _skip_trivia(self) -> None:
        while self.pos < len(self.text):
            if self.text.startswith("//", self.pos):
                newline = self.text.find("\n", self.pos + 2)
                self.pos = len(self.text) if newline < 0 else newline + 1
                continue
            if self.text.startswith("/*", self.pos):
                end = self.text.find("*/", self.pos + 2)
                if end < 0:
                    self._error("unterminated block comment")
                self.pos = end + 2
                continue
            if self.text[self.pos].isspace():
                self.pos += 1
                continue
            break

    def _peek(self, offset: int = 0) -> str | None:
        index = self.pos + offset
        return self.text[index] if index < len(self.text) else None

    def _expect(self, expected: str) -> None:
        if self._peek() != expected:
            self._error(f"expected {expected!r}")
        self.pos += 1

    def _error(self, message: str) -> None:
        line = self.text.count("\n", 0, self.pos) + 1
        raise ParseError(f"{self.path}:{line}: {message}")


def parse_resource_file(path: pathlib.Path) -> dict[str, object]:
    text = path.read_text(encoding="utf-8")
    value = ObjectParser(text, path.as_posix()).parse()
    if not isinstance(value, dict):
        raise ParseError(f"{path.as_posix()}: resource root must be an object")
    return value


def flatten_tree(value: object, prefix: str = "") -> Iterator[tuple[str, str]]:
    if isinstance(value, str):
        yield prefix, value
        return
    if isinstance(value, dict):
        for key, child in value.items():
            child_prefix = f"{prefix}.{key}" if prefix else key
            yield from flatten_tree(child, child_prefix)
        return
    if isinstance(value, list):
        for index, child in enumerate(value):
            child_prefix = f"{prefix}[{index}]"
            yield from flatten_tree(child, child_prefix)
        return
    raise ParseError(f"unsupported resource value at {prefix or '<root>'}: {type(value).__name__}")


def canonical_key(key: str) -> str:
    head, separator, tail = key.rpartition(".")
    stripped = PLURAL_SUFFIX_RE.sub("", tail)
    if stripped != tail:
        return f"{head}.{stripped}" if separator else stripped
    return key


def placeholders(value: str) -> set[str]:
    return {match.group(0).replace(" ", "") for match in PLACEHOLDER_RE.finditer(value)}


def meaningful_text(value: str) -> str:
    return PLACEHOLDER_RE.sub("", value).strip()


def load_locales() -> dict[str, dict[str, dict[str, object]]]:
    if not LOCALES_DIR.is_dir():
        raise ParseError(f"locale directory not found: {LOCALES_DIR}")
    locales: dict[str, dict[str, dict[str, object]]] = {}
    for locale_dir in sorted(path for path in LOCALES_DIR.iterdir() if path.is_dir()):
        namespaces: dict[str, dict[str, object]] = {}
        for path in sorted(locale_dir.glob("*.ts")):
            if path.name == "index.ts":
                continue
            namespaces[path.stem] = parse_resource_file(path)
        if namespaces:
            locales[locale_dir.name] = namespaces
    return locales


def check_resources(locales: dict[str, dict[str, dict[str, object]]]) -> list[Finding]:
    findings: list[Finding] = []
    if DEFAULT_LOCALE not in locales:
        return [Finding("webui/src/i18n/locales", None, f"missing default locale {DEFAULT_LOCALE}")]

    flattened: dict[str, dict[str, str]] = {}
    for locale, namespaces in locales.items():
        values: dict[str, str] = {}
        for namespace, tree in namespaces.items():
            for key, value in flatten_tree(tree):
                full_key = f"{namespace}.{key}" if key else namespace
                values[full_key] = value
        flattened[locale] = values

    base = flattened[DEFAULT_LOCALE]
    base_canonical = {canonical_key(key): value for key, value in base.items()}
    for locale, values in flattened.items():
        if locale == DEFAULT_LOCALE:
            continue
        canonical_values: dict[str, str] = {}
        for key, value in values.items():
            canonical_values[canonical_key(key)] = value
        missing = sorted(set(base_canonical) - set(canonical_values))
        extra = sorted(set(canonical_values) - set(base_canonical))
        for key in missing:
            findings.append(Finding("webui/src/i18n/locales", None, f"{locale}: missing key {key}"))
        for key in extra:
            findings.append(Finding("webui/src/i18n/locales", None, f"{locale}: extra key {key}"))

        for key in sorted(set(base_canonical) & set(canonical_values)):
            value = canonical_values[key]
            if not value.strip():
                findings.append(Finding("webui/src/i18n/locales", None, f"{locale}: empty value for {key}"))
            if locale.startswith("en") and CJK_RE.search(value) and key not in ENGLISH_CJK_ALLOWLIST:
                findings.append(Finding("webui/src/i18n/locales", None, f"{locale}: CJK text in English translation {key}"))
            expected_placeholders = placeholders(base_canonical[key])
            actual_placeholders = placeholders(value)
            if expected_placeholders != actual_placeholders:
                findings.append(
                    Finding(
                        "webui/src/i18n/locales",
                        None,
                        f"{locale}: placeholder mismatch for {key}: "
                        f"{sorted(expected_placeholders)} != {sorted(actual_placeholders)}",
                    )
                )
            if (
                locale == ENGLISH_LOCALE
                and key not in IDENTICAL_TRANSLATION_ALLOWLIST
                and meaningful_text(value) == meaningful_text(base_canonical[key])
                and re.search(r"[A-Za-z\u3400-\u9fff]", meaningful_text(value))
            ):
                findings.append(Finding("webui/src/i18n/locales", None, f"{locale}: pseudo translation for {key}"))
    return findings


def scan_cjk_literals(text: str) -> list[CjkLiteral]:
    """Return CJK string/template literals and JSX text nodes from TS/JSX."""
    findings: list[CjkLiteral] = []
    index = 0
    line = 1
    length = len(text)

    def add(value: str, start_line: int) -> None:
        if CJK_RE.search(value):
            findings.append(CjkLiteral(start_line, value))

    while index < length:
        ch = text[index]
        if ch == "/" and index + 1 < length and text[index + 1] == "/":
            end = text.find("\n", index + 2)
            index = length if end < 0 else end
            continue
        if ch == "/" and index + 1 < length and text[index + 1] == "*":
            end = text.find("*/", index + 2)
            if end < 0:
                break
            line += text.count("\n", index, end + 2)
            index = end + 2
            continue
        if ch in {"'", '"', "`"}:
            quote = ch
            start_line = line
            index += 1
            out: list[str] = []
            while index < length:
                current = text[index]
                if current == quote:
                    index += 1
                    break
                if current == "\\":
                    index += 1
                    if index >= length:
                        break
                    escaped = text[index]
                    simple = {"n": "\n", "r": "\r", "t": "\t", "b": "\b", "f": "\f", "v": "\v", "0": "\0"}
                    if escaped in simple:
                        out.append(simple[escaped])
                    elif escaped == "u":
                        digits = text[index + 1 : index + 5]
                        if re.fullmatch(r"[0-9a-fA-F]{4}", digits):
                            out.append(chr(int(digits, 16)))
                            index += 4
                        else:
                            out.append(escaped)
                    elif escaped == "x":
                        digits = text[index + 1 : index + 3]
                        if re.fullmatch(r"[0-9a-fA-F]{2}", digits):
                            out.append(chr(int(digits, 16)))
                            index += 2
                        else:
                            out.append(escaped)
                    else:
                        out.append(escaped)
                    index += 1
                    continue
                if current == "\n":
                    line += 1
                out.append(current)
                index += 1
            add("".join(out), start_line)
            continue
        if CJK_RE.match(ch):
            start_line = line
            start = index
            while index < length and text[index] not in "<>{}\n;=,()[]":
                if text[index] == "/" and index + 1 < length and text[index + 1] in {"/", "*"}:
                    break
                if text[index] == "\n":
                    break
                index += 1
            add(text[start:index], start_line)
            continue
        if ch == "\n":
            line += 1
        index += 1
    return findings


def is_scanned_source(relative: str) -> bool:
    if not relative.startswith("webui/src/"):
        return False
    if pathlib.PurePosixPath(relative).suffix not in SOURCE_SUFFIXES:
        return False
    if any(relative.startswith(prefix) for prefix, _ in IGNORED_SOURCE_PREFIXES):
        return False
    if any(marker in relative for marker, _ in IGNORED_SOURCE_MARKERS):
        return False
    return True


def git(repo: pathlib.Path, *args: str) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        ["git", *args],
        cwd=repo,
        text=True,
        encoding="utf-8",
        errors="replace",
        capture_output=True,
        check=False,
    )


def choose_base_ref(repo: pathlib.Path, explicit: str | None) -> str | None:
    if explicit:
        return explicit
    candidates: list[str] = []
    github_base = os_environ("GITHUB_BASE_REF")
    if github_base:
        candidates.extend([f"origin/{github_base}", github_base])
    candidates.extend(["origin/main", "main", "HEAD^"])
    valid: list[tuple[int, str]] = []
    for ref in dict.fromkeys(candidates):
        verify = git(repo, "rev-parse", "--verify", "--quiet", ref)
        if verify.returncode != 0:
            continue
        distance = git(repo, "rev-list", "--count", f"{ref}..HEAD")
        try:
            count = int(distance.stdout.strip())
        except ValueError:
            count = 10**9
        valid.append((count, ref))
    if not valid:
        return None
    return min(valid, key=lambda item: (item[0], item[1]))[1]


def os_environ(name: str) -> str | None:
    # Kept behind a tiny helper so the self-test can run without importing os
    # into every rule function's namespace.
    import os

    return os.environ.get(name)


def show_base_file(repo: pathlib.Path, ref: str, relative: str) -> str | None:
    result = git(repo, "show", f"{ref}:{relative}")
    return result.stdout if result.returncode == 0 else None


def has_ignore_directive(text: str, line: int) -> bool:
    lines = text.splitlines()
    for index in range(max(0, line - 2), min(len(lines), line + 1)):
        if "i18n-ignore:" in lines[index]:
            return True
    return False


def check_hardcoded_cjk(repo: pathlib.Path, base_ref: str | None, audit_all: bool) -> tuple[list[Finding], int, str | None]:
    if base_ref is None and not audit_all:
        return [], 0, "cannot determine git baseline; pass --base-ref or use --all"

    current: list[tuple[str, str, CjkLiteral]] = []
    for path in sorted(WEBUI_SRC.rglob("*")):
        if not path.is_file():
            continue
        relative = path.relative_to(repo).as_posix()
        if not is_scanned_source(relative):
            continue
        text = path.read_text(encoding="utf-8")
        for literal in scan_cjk_literals(text):
            if has_ignore_directive(text, literal.line):
                continue
            current.append((relative, text, literal))

    legacy: set[tuple[str, str]] = set()
    if base_ref and not audit_all:
        # One `git show` per changed file, not per literal: the repository has
        # hundreds of existing CJK literals and a subprocess per literal would
        # make the gate unusably slow on Windows.
        base_cache: dict[str, str | None] = {}
        for relative in sorted({relative for relative, _, _ in current}):
            base_text = base_cache.setdefault(relative, show_base_file(repo, base_ref, relative))
            if base_text is None:
                continue
            for literal in scan_cjk_literals(base_text):
                legacy.add((relative, literal.fingerprint))

    findings: list[Finding] = []
    legacy_count = 0
    for relative, _, literal in current:
        if not audit_all and (relative, literal.fingerprint) in legacy:
            legacy_count += 1
            continue
        preview = re.sub(r"\s+", " ", literal.text).strip()
        if len(preview) > 80:
            preview = preview[:77] + "..."
        findings.append(Finding(relative, literal.line, f"hard-coded CJK literal: {preview!r}"))
    return findings, legacy_count, None


def self_test() -> int:
    errors: list[str] = []

    parsed = ObjectParser("export default { a: { b: 'x', c: '你好' }, d: ['y'] }", "sample.ts").parse()
    if parsed != {"a": {"b": "x", "c": "你好"}, "d": ["y"]}:
        errors.append("object parser")

    if canonical_key("common.time.minutesShort_one") != "common.time.minutesShort":
        errors.append("plural canonicalization")

    found = scan_cjk_literals("// 注释\nconst x = '中文'\n<div>你好</div>\n")
    if len(found) != 2 or {item.text for item in found} != {"中文", "你好"}:
        errors.append(f"cjk scanner: {found!r}")

    locale_sample = {
        "zh-CN": {"common": {"hello": "你好", "count": "{{count}} 条"}},
        "en-US": {"common": {"hello": "Hello", "count": "{{count}} items"}},
    }
    if check_resources(locale_sample):
        errors.append(f"resource checker: {check_resources(locale_sample)!r}")

    bad_sample = {
        "zh-CN": {"common": {"hello": "你好", "count": "{{count}} 条"}},
        "en-US": {"common": {"hello": "你好", "count": "items"}},
    }
    bad_messages = [finding.message for finding in check_resources(bad_sample)]
    if not any("CJK text" in message for message in bad_messages):
        errors.append("pseudo-translation detection")
    if not any("placeholder mismatch" in message for message in bad_messages):
        errors.append("placeholder detection")

    if errors:
        print("i18n check self-test FAILED:")
        for error in errors:
            print(f"  - {error}")
        return 1
    print("i18n check self-test PASS")
    return 0


def render_findings(title: str, findings: Iterable[Finding]) -> None:
    findings = list(findings)
    if not findings:
        return
    print(f"{title}:")
    for finding in findings[:80]:
        print(f"  - {finding.render()}")
    if len(findings) > 80:
        print(f"  ... {len(findings) - 80} more")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--base-ref", help="git revision used as the hard-coded-CJK baseline")
    parser.add_argument("--all", action="store_true", help="audit existing hard-coded CJK debt instead of ratcheting")
    parser.add_argument("--self-test", action="store_true", help="run internal parser/rule self-tests")
    args = parser.parse_args()

    if args.self_test:
        return self_test()

    try:
        locales = load_locales()
        resource_findings = check_resources(locales)
    except (OSError, ParseError) as exc:
        print(f"i18n check FAILED: {exc}")
        return 1

    base_ref = choose_base_ref(REPO_ROOT, args.base_ref)
    hardcoded_findings, legacy_count, hardcoded_error = check_hardcoded_cjk(REPO_ROOT, base_ref, args.all)

    all_findings = [*resource_findings, *hardcoded_findings]
    if all_findings or hardcoded_error:
        print("i18n check FAILED")
        render_findings("resource findings", resource_findings)
        render_findings("hard-coded CJK findings", hardcoded_findings)
        if hardcoded_error:
            print(f"  - {hardcoded_error}")
        return 1

    mode = "all" if args.all else f"baseline {base_ref}"
    print(f"i18n check PASS ({len(locales)} locales, hard-coded CJK: 0 new, {legacy_count} legacy allowed; {mode})")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())