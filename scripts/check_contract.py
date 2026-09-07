#!/usr/bin/env python3
"""契约漂移门禁：Go `internal/api/types.go` ↔ TS `webui/src/lib/types.ts`。

`docs/design.md` 硬约束 4 要求 DTO 三处同步（Go 结构体 / TS 接口 / 文档）。文档那一半靠人守，
代码这一半靠本门禁：两侧**同名**类型的 JSON 字段集合必须一致，否则非零退出。

为什么值得一个门禁：`EventData` 曾在 Go 侧是 `{type, entity_id?, label?}`、TS 侧是 `{type, label?}`，
前端还照 TS 的形状重建实时事件载荷，于是同一条事件在实时列表里是 `{"label":""}`、在历史里是
`{"type":…,"entity_id":…}`。三个文件单独读都自洽，横向比对才看得出漂移。

已知边界（有意为之，不做过度设计）：

* 只比对两侧同名的类型。Go-only（`HelloData`、`CommandData` 等 edge↔server 内部消息）与
  TS-only（视图模型、请求入参）只在 `--list` 里列出，不判失败。
* TS 侧 `extends` 会被展开成平面字段集再比对（Go 结构体是平面的），父接口不在本文件时
  该类型跳过比对并显式告警——看不见的字段不能拿来判漂移。
* 类型改名会逃过本门禁：改名请在同一提交里改两侧，必要时用 `--list` 人工核对。
* 可选性（Go `omitempty` ↔ TS `?`）只报告不判失败：omitempty 说的是「零值是否省略编码」，
  与 TS 的「键可以不存在」并不严格等价，强行对齐会制造假门禁。

用法：

    python scripts/check_contract.py             # 门禁：字段集合漂移即非零退出
    python scripts/check_contract.py --list      # 打印匹配/未匹配类型供人工核对
    python scripts/check_contract.py --self-test # 解析器红队自检
"""
from __future__ import annotations

import argparse
import pathlib
import re
import sys

# Windows CI 控制台默认 cp1252：直接 print 中文会 UnicodeEncodeError 崩掉门禁
# （崩溃会掩盖真正的比对结果），统一切到 UTF-8 并对不可编码字符降级替换。
for _stream in (sys.stdout, sys.stderr):
    if hasattr(_stream, "reconfigure"):
        _stream.reconfigure(encoding="utf-8", errors="replace")

ROOT = pathlib.Path(__file__).resolve().parent.parent
GO_TYPES = ROOT / "internal" / "api" / "types.go"
TS_TYPES = ROOT / "webui" / "src" / "lib" / "types.ts"

OPENERS = "{[("
CLOSERS = "}])"

GO_STRUCT_RE = re.compile(r"^type\s+(\w+)\s+struct\s*\{", re.M)
GO_JSON_TAG_RE = re.compile(r'json:"([^"]*)"')
GO_FIELD_NAME_RE = re.compile(r"^([A-Z]\w*)\b")
TS_DECL_RE = re.compile(r"^export\s+(?:interface|type)\s+(\w+)", re.M)
TS_EXTENDS_RE = re.compile(r"\bextends\s+([A-Za-z_$][\w$]*(?:\s*,\s*[A-Za-z_$][\w$]*)*)")
TS_MEMBER_RE = re.compile(r"^(?:readonly\s+)?([A-Za-z_$][\w$]*)\s*(\?)?\s*:")

Fields = dict[str, bool]


def strip_comments(src: str) -> str:
    """去掉块注释与行注释：注释里的花括号会破坏配对，中文注释里的 `{` 尤其容易踩。

    契约声明文件里没有含 `//` 的原始字符串字面量，按行剥行注释是安全的。
    """
    src = re.sub(r"/\*.*?\*/", "", src, flags=re.S)
    return "\n".join(re.sub(r"//.*$", "", line) for line in src.split("\n"))


def slice_block(src: str, open_idx: int) -> str:
    """从 `open_idx`（指向开括号）起按括号配对切出块内容，不含最外层括号。"""
    depth = 0
    for i in range(open_idx, len(src)):
        ch = src[i]
        if ch in OPENERS:
            depth += 1
        elif ch in CLOSERS:
            depth -= 1
            if depth == 0:
                return src[open_idx + 1 : i]
    raise ValueError("unbalanced brackets at offset %d" % open_idx)


def _depth_after(line: str, depth: int) -> int:
    for ch in line:
        if ch in OPENERS:
            depth += 1
        elif ch in CLOSERS:
            depth -= 1
    return depth


def _go_fields(body: str) -> Fields:
    """结构体 body → {json 字段名: 是否 omitempty}。

    嵌套（匿名结构体）内部的字段属于内层类型，不计入外层契约；但内层收尾行上的
    tag 属于外层字段，靠「行末深度归零」把它捞回来。
    """
    fields: Fields = {}
    depth = 0
    pending: str | None = None
    for line in body.split("\n"):
        stripped = line.strip()
        before = depth
        depth = _depth_after(stripped, depth)
        if not stripped:
            continue
        if before > 0 and depth > 0:
            continue  # 内层字段属于内层类型，不外泄成外层契约
        if before == 0:
            name = GO_FIELD_NAME_RE.match(stripped)
            pending = name.group(1) if name else None
        tag = GO_JSON_TAG_RE.search(stripped)
        if tag:
            spec = tag.group(1)
            if spec and spec != "-":
                parts = spec.split(",")
                fields[parts[0]] = "omitempty" in parts[1:]
            pending = None
        elif pending and depth == 0:
            fields[pending] = False  # 无 tag 的导出字段按 Go 名序列化
            pending = None
    return fields


def go_structs(src: str) -> dict[str, Fields]:
    """解析 Go 契约源，返回 {结构体名: 字段表}（无字段的空结构体不返回）。"""
    out: dict[str, Fields] = {}
    for m in GO_STRUCT_RE.finditer(src):
        fields = _go_fields(slice_block(src, m.end() - 1))
        if fields:
            out[m.group(1)] = fields
    return out


def _ts_members(body: str) -> Fields:
    """类型体 → {成员名: 是否可选}。

    成员分隔只认 `;` 与换行（本仓风格），不认逗号：`Record<string, { a: number }>` 这类
    泛型参数里的逗号会把成员切碎。索引签名 `[k: string]: unknown` 不以标识符开头，自然跳过。
    """
    members: Fields = {}
    depth = 0
    seg: list[str] = []

    def flush() -> None:
        text = "".join(seg).strip()
        seg.clear()
        if not text:
            return
        m = TS_MEMBER_RE.match(text)
        if m:
            members[m.group(1)] = bool(m.group(2))

    for ch in body:
        if ch in OPENERS:
            depth += 1
        elif ch in CLOSERS:
            depth -= 1
        if depth == 0 and ch in ";\n":
            flush()
        else:
            seg.append(ch)
    flush()
    return members


def ts_types(src: str) -> tuple[dict[str, Fields], dict[str, list[str]], list[str]]:
    """解析 TS 契约源 → ({类型名: 自有成员}, {类型名: extends 父名}, 跳过的非对象声明)。

    只认对象形状：`export type Tone = 'a' | 'b'`、`Record<string, unknown>`、
    `typeof X[keyof typeof X]` 一律跳过（它们没有可比对的字段集合）。
    """
    own: dict[str, Fields] = {}
    parents: dict[str, list[str]] = {}
    skipped: list[str] = []
    for m in TS_DECL_RE.finditer(src):
        name = m.group(1)
        nxt = src.find("\nexport ", m.end())
        window = src[m.end() : len(src) if nxt == -1 else nxt]
        brace = window.find("{")
        semi = window.find(";")
        pipe = window.find("|")
        if brace == -1 or (semi != -1 and semi < brace) or (pipe != -1 and pipe < brace):
            skipped.append(name)
            continue
        head = window[:brace]
        ext = TS_EXTENDS_RE.search(head)
        if ext:
            parents[name] = [p.strip() for p in ext.group(1).split(",") if p.strip()]
        members = _ts_members(slice_block(src, m.end() + brace))
        if members:
            own[name] = members
        elif name not in parents:
            skipped.append(name)
    return own, parents, skipped


def flatten_ts(own: dict[str, Fields], parents: dict[str, list[str]]) -> tuple[dict[str, Fields], list[str]]:
    """把 `extends` 展开成平面字段集（自有成员覆盖父接口）；返回 (平面表, 父接口缺失的类型名)。"""
    flat: dict[str, Fields] = {}
    unresolved: list[str] = []

    def resolve(name: str, seen: set[str]) -> Fields | None:
        if name in flat:
            return flat[name]
        if name in seen:  # 循环继承：按已收集到的部分收敛，不死循环
            return {}
        if name not in own:
            return None
        seen = seen | {name}
        merged: Fields = {}
        for parent in parents.get(name, ()):
            inherited = resolve(parent, seen)
            if inherited is None:
                return None
            merged.update(inherited)
        merged.update(own[name])
        flat[name] = merged
        return merged

    for name in own:
        if resolve(name, set()) is None:
            unresolved.append(name)
    for name in unresolved:
        flat.pop(name, None)
    return flat, sorted(unresolved)


def compare(go: dict[str, Fields], ts: dict[str, Fields]) -> list[tuple[str, list[str], list[str]]]:
    """同名类型的字段集合差异：[(类型名, Go 有 TS 缺, TS 有 Go 缺)]。"""
    drift = []
    for name in sorted(set(go) & set(ts)):
        missing = sorted(set(go[name]) - set(ts[name]))
        extra = sorted(set(ts[name]) - set(go[name]))
        if missing or extra:
            drift.append((name, missing, extra))
    return drift


def optionality(go: dict[str, Fields], ts: dict[str, Fields]) -> list[tuple[str, list[str], list[str]]]:
    """共有字段的可选性差异：[(类型名, Go omitempty 而 TS 必填, Go 必填而 TS 可选)]。"""
    notes = []
    for name in sorted(set(go) & set(ts)):
        shared = set(go[name]) & set(ts[name])
        loose = sorted(k for k in shared if go[name][k] and not ts[name][k])
        tight = sorted(k for k in shared if not go[name][k] and ts[name][k])
        if loose or tight:
            notes.append((name, loose, tight))
    return notes


FIXTURE_GO = """package api

// Envelope 是统一信封。
type Envelope struct {
	V      int    `json:"v"`
	Type   string `json:"type"`
	Device string `json:"device,omitempty"`
	Data   []byte `json:"data"`
}

type EventData struct {
	Type     string `json:"type"`
	EntityID string `json:"entity_id,omitempty"`
	Label    string `json:"label,omitempty"`
}

// Derived 在 TS 侧被拆成 Base + extends：平面化后必须对得上
type Derived struct {
	A string `json:"a"`
	B int    `json:"b,omitempty"`
	C bool   `json:"c"`
}

// 注释里的花括号 { 不应破坏配对
type Ignored struct {
	Secret string `json:"-"`
	Raw    string
	Nested struct {
		Inner string `json:"inner"`
	} `json:"nested"`
}
"""

FIXTURE_TS = """export interface Envelope { v: number; type: string; device?: string; data?: unknown }

export interface EventData {
  type: string
  entity_id?: string
}

export type Tone = 'idle' | 'warn'

export type Raw = Record<string, unknown>

export interface Base { a: string; b?: number }

export interface Derived extends Base { c: boolean }

export interface Orphan extends MissingParent { d: string }

export interface Ignored {
  raw: string
  nested: { inner: string }
  map: Record<string, { a: number }>
  [k: string]: unknown
}
"""


def self_test() -> int:
    """解析器红队自检：门禁自己坏了必须比契约漂移更早被发现。"""
    errors: list[str] = []
    go = go_structs(strip_comments(FIXTURE_GO))
    own, parents, skipped = ts_types(strip_comments(FIXTURE_TS))
    ts, unresolved = flatten_ts(own, parents)

    expect_go = {
        "Envelope": {"v": False, "type": False, "device": True, "data": False},
        "EventData": {"type": False, "entity_id": True, "label": True},
        "Derived": {"a": False, "b": True, "c": False},
        # Secret 被 json:"-" 排除；Raw 无 tag 按 Go 名；Inner 属于内层，不外泄
        "Ignored": {"Raw": False, "nested": False},
    }
    expect_ts = {
        # 单行 interface 用 `;` 分隔成员：只按行解析的实现会漏掉 device/data
        "Envelope": {"v": False, "type": False, "device": True, "data": True},
        "EventData": {"type": False, "entity_id": True},
        "Base": {"a": False, "b": True},
        # extends 平面化：父接口成员并入，自有成员覆盖同名父成员
        "Derived": {"a": False, "b": True, "c": False},
        # 嵌套对象与 Record<> 里的逗号不得切碎成员；索引签名不是成员
        "Ignored": {"raw": False, "nested": False, "map": False},
    }
    if go != expect_go:
        errors.append("Go 解析不符：%r" % (go,))
    if ts != expect_ts:
        errors.append("TS 解析不符：%r" % (ts,))
    for alias in ("Tone", "Raw"):
        if alias not in skipped:
            errors.append("非对象类型别名 %s 未被跳过：%r" % (alias, skipped))
    if unresolved != ["Orphan"]:
        errors.append("父接口缺失的类型未被隔离：%r" % (unresolved,))

    expect_drift = [("EventData", ["label"], []), ("Ignored", ["Raw"], ["map", "raw"])]
    if compare(go, ts) != expect_drift:
        errors.append("漂移判定不符：%r" % (compare(go, ts),))
    if optionality(go, ts) != [("Envelope", [], ["data"])]:
        errors.append("可选性报告不符：%r" % (optionality(go, ts),))
    # 字段集合一致、只有可选性差异时不得误报漂移
    if compare({"A": {"x": False}}, {"A": {"x": True}}) != []:
        errors.append("可选性差异被误判成字段漂移")

    if errors:
        for e in errors:
            print("self-test failed: " + e)
        return 1
    print("contract self-test PASS（6 组红队断言）")
    return 0


def _load() -> tuple[dict[str, Fields], dict[str, Fields], list[str], list[str]]:
    for path in (GO_TYPES, TS_TYPES):
        if not path.is_file():
            raise SystemExit("缺少契约源文件：%s" % path.relative_to(ROOT))
    go = go_structs(strip_comments(GO_TYPES.read_text(encoding="utf-8")))
    own, parents, skipped = ts_types(strip_comments(TS_TYPES.read_text(encoding="utf-8")))
    ts, unresolved = flatten_ts(own, parents)
    return go, ts, skipped, unresolved


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(
        description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter
    )
    parser.add_argument("--self-test", action="store_true", help="只跑解析器红队自检")
    parser.add_argument("--list", action="store_true", help="打印匹配情况供人工核对")
    args = parser.parse_args(argv)

    if args.self_test:
        return self_test()

    go, ts, skipped, unresolved = _load()
    matched = sorted(set(go) & set(ts))
    go_only = sorted(set(go) - set(ts))
    ts_only = sorted(set(ts) - set(go))

    if args.list:
        print("同名类型（%d）：%s" % (len(matched), ", ".join(matched) or "无"))
        print("Go-only（%d）：%s" % (len(go_only), ", ".join(go_only) or "无"))
        print("TS-only（%d）：%s" % (len(ts_only), ", ".join(ts_only) or "无"))
        print("TS 非对象声明（%d）：%s" % (len(skipped), ", ".join(skipped) or "无"))

    exit_code = 0
    for name in unresolved:
        print("跳过比对：%s 的 extends 父接口不在 %s 内，看不见的字段不拿来判漂移"
              % (name, TS_TYPES.relative_to(ROOT).as_posix()))

    drift = compare(go, ts)
    if drift:
        print(
            "契约漂移 %d 处（%s ↔ %s）："
            % (len(drift), GO_TYPES.relative_to(ROOT).as_posix(), TS_TYPES.relative_to(ROOT).as_posix())
        )
        for name, missing, extra in drift:
            print("  " + name)
            if missing:
                print("    Go 有、TS 缺 : " + ", ".join(missing))
            if extra:
                print("    TS 有、Go 缺 : " + ", ".join(extra))
        print("修复：两侧字段集合必须一致（docs/design.md 硬约束 4）。")
        return 1

    notes = optionality(go, ts)
    if notes:
        print("可选性差异（不判失败，动这些字段时顺手核对）：")
        for name, loose, tight in notes:
            parts = []
            if loose:
                parts.append("Go omitempty / TS 必填: " + ", ".join(loose))
            if tight:
                parts.append("Go 必填 / TS 可选: " + ", ".join(tight))
            print("  %s — %s" % (name, "；".join(parts)))

    print(
        "contract check PASS（%d 个同名类型字段一致；Go-only %d，TS-only %d）"
        % (len(matched), len(go_only), len(ts_only))
    )
    return exit_code


if __name__ == "__main__":
    sys.exit(main())