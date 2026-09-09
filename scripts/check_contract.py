#!/usr/bin/env python3
"""契约漂移门禁：Go DTO ↔ TS DTO、proto ↔ Go SDK、JSON Schema ↔ Go DTO。

现有 Go/TS HTTP DTO 门禁保持不变；新增两类可执行 parity：

* `proto/cloudpath/v1/*.proto` 的手写字段/服务方法必须与
  `sdk/go/cloudpath/v1/{driver,application,status}` 的 JSON wire 形状一致。
  oneof 在 Go 侧由 codec wire struct 承载，门禁按 wire struct 比对，不比较 `Union`。
* `spec/{plugin-manifest,descriptor,capability}.schema.json` 的对象字段集合必须与
  `internal/registry` / `sdk/go/model` 的 Go DTO 一致。TS 侧另有少量旧 UI 兼容字段，
  门禁用显式例外隔离，避免它们悄悄变成第二套契约。

已知边界（有意为之，不做过度设计）：

* proto 不是 protoc 输入，字段号本身不在 JSON wire 上；本门禁守字段名、字段集合和服务方法。
* JSON Schema 的 `required`/枚举/正则仍由现有 Go 校验器守；本门禁只守对象字段集合。
* 可选性差异只报告不判失败，避免把 `omitempty` 与 TS `?` 强行等同。

用法：

    python scripts/check_contract.py             # 全部门禁；漂移即非零退出
    python scripts/check_contract.py --list      # 打印匹配/未匹配类型供人工核对
    python scripts/check_contract.py --self-test # 解析器红队自检
"""
from __future__ import annotations

import argparse
import json
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
PROTO_DRIVER = ROOT / "proto" / "cloudpath" / "v1" / "driver.proto"
PROTO_APPLICATION = ROOT / "proto" / "cloudpath" / "v1" / "application.proto"
GO_DRIVER_TYPES = ROOT / "sdk" / "go" / "cloudpath" / "v1" / "driver" / "types.go"
GO_DRIVER_CODEC = ROOT / "sdk" / "go" / "cloudpath" / "v1" / "driver" / "codec.go"
GO_APPLICATION_TYPES = ROOT / "sdk" / "go" / "cloudpath" / "v1" / "application" / "types.go"
GO_APPLICATION_CODEC = ROOT / "sdk" / "go" / "cloudpath" / "v1" / "application" / "codec.go"
GO_STATUS = ROOT / "sdk" / "go" / "cloudpath" / "v1" / "status" / "status.go"
SCHEMA_MANIFEST = ROOT / "spec" / "plugin-manifest.schema.json"
SCHEMA_DESCRIPTOR = ROOT / "spec" / "descriptor.schema.json"
SCHEMA_CAPABILITY = ROOT / "spec" / "capability.schema.json"
GO_REGISTRY_MANIFEST = ROOT / "internal" / "registry" / "manifest.go"
GO_MODEL_DESCRIPTOR = ROOT / "sdk" / "go" / "model" / "descriptor.go"
GO_MODEL_DEVICE = ROOT / "sdk" / "go" / "model" / "device.go"
GO_MODEL_OBSERVATION = ROOT / "sdk" / "go" / "model" / "observation.go"
GO_MODEL_CAPABILITY = ROOT / "sdk" / "go" / "model" / "capability.go"

OPENERS = "{[("
CLOSERS = "}])"

GO_STRUCT_RE = re.compile(r"^type\s+(\w+)\s+struct\s*\{", re.M)
GO_JSON_TAG_RE = re.compile(r'json:"([^"]*)"')
GO_FIELD_NAME_RE = re.compile(r"^([A-Z]\w*)\b")
TS_DECL_RE = re.compile(r"^export\s+(?:interface|type)\s+(\w+)", re.M)
TS_EXTENDS_RE = re.compile(r"\bextends\s+([A-Za-z_$][\w$]*(?:\s*,\s*[A-Za-z_$][\w$]*)*)")
TS_MEMBER_RE = re.compile(r"^(?:readonly\s+)?([A-Za-z_$][\w$]*)\s*(\?)?\s*:")
PROTO_MESSAGE_RE = re.compile(r"\bmessage\s+(\w+)\s*\{", re.M)
PROTO_FIELD_RE = re.compile(
    r"\b(?:(repeated)\s+)?(?:map\s*<[^>]+>|[A-Za-z_][\w.]*)\s+([a-z_]\w*)\s*=\s*\d+\s*;"
)
PROTO_SERVICE_RE = re.compile(r"\bservice\s+(\w+)\s*\{", re.M)
PROTO_RPC_RE = re.compile(r"\brpc\s+(\w+)\s*\(")
GO_METHOD_RE = re.compile(r'\bMethod(\w+)\s*=\s*"[^"]+"')

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


def go_structs(src: str, *, include_empty: bool = False) -> dict[str, Fields]:
    """解析 Go 契约源，返回 {结构体名: 字段表}。

    默认跳过空结构体，保持既有 Go/TS 比对语义；proto parity 需要把
    `DescribeRequest`/`HealthRequest` 这类空消息也纳入映射，故显式开启。
    """
    out: dict[str, Fields] = {}
    for m in GO_STRUCT_RE.finditer(src):
        fields = _go_fields(slice_block(src, m.end() - 1))
        if fields or include_empty:
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




SCHEMA_GO_CONTRACTS = (
    (SCHEMA_MANIFEST, "plugin-manifest", GO_REGISTRY_MANIFEST, "Manifest", ()),
    (SCHEMA_MANIFEST, "plugin-manifest", GO_REGISTRY_MANIFEST, "Compatibility", ("properties", "compatibility")),
    (SCHEMA_MANIFEST, "plugin-manifest", GO_REGISTRY_MANIFEST, "Permissions", ("properties", "permissions")),
    (SCHEMA_MANIFEST, "plugin-manifest", GO_REGISTRY_MANIFEST, "Contributes", ("properties", "contributes")),
    (
        SCHEMA_MANIFEST,
        "plugin-manifest",
        GO_REGISTRY_MANIFEST,
        "DriverContribution",
        ("properties", "contributes", "properties", "drivers", "items"),
    ),
    (
        SCHEMA_MANIFEST,
        "plugin-manifest",
        GO_REGISTRY_MANIFEST,
        "ApplicationContribution",
        ("properties", "contributes", "properties", "applications", "items"),
    ),
    (
        SCHEMA_MANIFEST,
        "plugin-manifest",
        GO_REGISTRY_MANIFEST,
        "ConnectorContribution",
        ("properties", "contributes", "properties", "connectors", "items"),
    ),
    (SCHEMA_DESCRIPTOR, "descriptor", GO_MODEL_DESCRIPTOR, "Descriptor", ()),
    (SCHEMA_DESCRIPTOR, "descriptor", GO_MODEL_DEVICE, "Entity", ("definitions", "Entity")),
    (SCHEMA_DESCRIPTOR, "descriptor", GO_MODEL_OBSERVATION, "Observation", ("definitions", "Observation")),
    (SCHEMA_CAPABILITY, "capability", GO_MODEL_CAPABILITY, "Capability", ()),
    (SCHEMA_CAPABILITY, "capability", GO_MODEL_CAPABILITY, "CapabilityMetadata", ("properties", "metadata")),
    (SCHEMA_CAPABILITY, "capability", GO_MODEL_CAPABILITY, "CapabilitySpec", ("properties", "spec")),
    (SCHEMA_CAPABILITY, "capability", GO_MODEL_CAPABILITY, "Property", ("definitions", "Property")),
    (SCHEMA_CAPABILITY, "capability", GO_MODEL_CAPABILITY, "EventDecl", ("definitions", "EventDecl")),
    (SCHEMA_CAPABILITY, "capability", GO_MODEL_CAPABILITY, "ActionDecl", ("definitions", "ActionDecl")),
)

SCHEMA_TS_CONTRACTS = (
    (SCHEMA_DESCRIPTOR, "descriptor", "DeviceDescriptor", ()),
    (SCHEMA_DESCRIPTOR, "descriptor", "DescriptorEntity", ("definitions", "Entity")),
    (SCHEMA_DESCRIPTOR, "descriptor", "Observation", ("definitions", "Observation")),
    (SCHEMA_CAPABILITY, "capability", "CapabilityDoc", ()),
    (SCHEMA_CAPABILITY, "capability", "CapabilityMetadata", ("properties", "metadata")),
    (SCHEMA_CAPABILITY, "capability", "CapabilitySpec", ("properties", "spec")),
    (SCHEMA_CAPABILITY, "capability", "CapabilityProperty", ("definitions", "Property")),
    (SCHEMA_CAPABILITY, "capability", "CapabilityEventDecl", ("definitions", "EventDecl")),
    (SCHEMA_CAPABILITY, "capability", "CapabilityActionDecl", ("definitions", "ActionDecl")),
)

# TS 侧的历史兼容例外：显式列出，新增漂移仍会失败。
TS_SCHEMA_ALLOWED = {
    # 嵌套 Observation 的 entity_id 由外层 Entity 提供；TS 只消费嵌套形态。
    ("descriptor", "Observation"): ({"entity_id"}, set()),
    # 旧前端兼容读取，不进入 Go/Edge 生产转换，也不属于冻结 schema。
    ("capability", "CapabilityActionDecl"): (set(), {"command", "primary"}),
    # 旧前端展示提示，不进入 Go/Edge 生产转换，也不属于冻结 schema。
    ("capability", "CapabilityEventDecl"): (set(), {"title", "description"}),
}


def proto_messages(src: str) -> dict[str, Fields]:
    """proto 文本 → {message: 字段表}；oneof 内字段按其 wire 名计入消息。"""
    out: dict[str, Fields] = {}
    for m in PROTO_MESSAGE_RE.finditer(src):
        body = slice_block(src, m.end() - 1)
        fields: Fields = {}
        for fm in PROTO_FIELD_RE.finditer(body):
            fields[fm.group(2)] = bool(fm.group(1))
        out[m.group(1)] = fields
    return out


def proto_services(src: str) -> dict[str, list[str]]:
    """proto 文本 → {service: [rpc 方法名]}。"""
    out: dict[str, list[str]] = {}
    for m in PROTO_SERVICE_RE.finditer(src):
        body = slice_block(src, m.end() - 1)
        out[m.group(1)] = [rpc.group(1) for rpc in PROTO_RPC_RE.finditer(body)]
    return out


def go_methods(src: str) -> set[str]:
    """Go 类型文件里的 MethodXxx 常量 → {Xxx}。"""
    return set(GO_METHOD_RE.findall(src))


def schema_object_fields(doc: dict, path: tuple[str, ...]) -> Fields:
    """取 JSON Schema 指定对象节点的 properties 字段集合。"""
    cur: object = doc
    for key in path:
        if not isinstance(cur, dict) or key not in cur:
            raise ValueError("schema path missing: " + ".".join(path))
        cur = cur[key]
    if not isinstance(cur, dict):
        raise ValueError("schema node is not an object: " + ".".join(path))
    props = cur.get("properties")
    if not isinstance(props, dict):
        raise ValueError("schema node has no object properties: " + ".".join(path))
    return {str(name): False for name in props}


def _load_go_structs(path: pathlib.Path, *, include_empty: bool = True) -> dict[str, Fields]:
    return go_structs(strip_comments(path.read_text(encoding="utf-8")), include_empty=include_empty)


def proto_go_parity() -> tuple[list[tuple[str, str, list[str], list[str]]], int]:
    """手写 proto ↔ Go SDK wire structs 的字段/服务方法 parity。"""
    status = _load_go_structs(GO_STATUS)
    driver_types = _load_go_structs(GO_DRIVER_TYPES)
    driver_codec = _load_go_structs(GO_DRIVER_CODEC)
    app_types = _load_go_structs(GO_APPLICATION_TYPES)
    app_codec = _load_go_structs(GO_APPLICATION_CODEC)

    contracts = (
        (
            "driver.proto",
            PROTO_DRIVER,
            driver_types,
            {
                "Status": status.get("Status"),
                "DriverMessage": driver_codec.get("driverMessageWire"),
                "DiscoveryEvent": driver_codec.get("discoveryEventWire"),
                "Value": driver_codec.get("valueWire"),
            },
            GO_DRIVER_TYPES,
        ),
        (
            "application.proto",
            PROTO_APPLICATION,
            app_types,
            {
                "Status": status.get("Status"),
                "ApplicationEvent": app_codec.get("applicationEventWire"),
                "ApplicationEffect": app_codec.get("applicationEffectWire"),
            },
            GO_APPLICATION_TYPES,
        ),
    )

    drifts: list[tuple[str, str, list[str], list[str]]] = []
    message_count = 0
    for label, proto_path, go_structs_by_name, special, method_path in contracts:
        proto_src = strip_comments(proto_path.read_text(encoding="utf-8"))
        messages = proto_messages(proto_src)
        message_count += len(messages)
        for name, proto_fields in sorted(messages.items()):
            if name in special:
                go_fields = special[name]
            else:
                go_fields = go_structs_by_name.get(name)
            if go_fields is None:
                drifts.append((label, name, sorted(proto_fields), []))
                continue
            missing = sorted(set(proto_fields) - set(go_fields))
            extra = sorted(set(go_fields) - set(proto_fields))
            if missing or extra:
                drifts.append((label, name, missing, extra))

        declared = go_methods(method_path.read_text(encoding="utf-8"))
        for service, methods in sorted(proto_services(proto_src).items()):
            missing = sorted(set(methods) - declared)
            extra = sorted(declared - set(methods))
            if missing or extra:
                drifts.append((label, service, missing, extra))
    return drifts, message_count


def json_schema_go_parity() -> tuple[list[tuple[str, str, list[str], list[str]]], int]:
    """JSON Schema 对象字段 ↔ Go DTO 字段 parity。"""
    go_cache: dict[pathlib.Path, dict[str, Fields]] = {}
    drifts: list[tuple[str, str, list[str], list[str]]] = []
    for schema_path, label, go_path, go_name, path in SCHEMA_GO_CONTRACTS:
        doc = json.loads(schema_path.read_text(encoding="utf-8"))
        node = f"{label}:{'/'.join(path) or '$'}"
        try:
            schema_fields = schema_object_fields(doc, path)
        except ValueError as err:
            drifts.append((schema_path.name, node, [str(err)], []))
            continue
        if go_path not in go_cache:
            go_cache[go_path] = _load_go_structs(go_path)
        go_fields = go_cache[go_path].get(go_name)
        if go_fields is None:
            drifts.append((schema_path.name, node, sorted(schema_fields), []))
            continue
        missing = sorted(set(schema_fields) - set(go_fields))
        extra = sorted(set(go_fields) - set(schema_fields))
        if missing or extra:
            drifts.append((schema_path.name, node, missing, extra))
    return drifts, len(SCHEMA_GO_CONTRACTS)


def json_schema_ts_parity() -> tuple[
    list[tuple[str, str, list[str], list[str]]],
    list[tuple[str, str, list[str], list[str]]],
    int,
]:
    """JSON Schema 对象字段 ↔ TS 消费类型 parity；历史兼容字段单列。"""
    own, parents, _skipped = ts_types(strip_comments(TS_TYPES.read_text(encoding="utf-8")))
    ts, _unresolved = flatten_ts(own, parents)
    drifts: list[tuple[str, str, list[str], list[str]]] = []
    notes: list[tuple[str, str, list[str], list[str]]] = []
    for schema_path, label, ts_name, path in SCHEMA_TS_CONTRACTS:
        doc = json.loads(schema_path.read_text(encoding="utf-8"))
        try:
            schema_fields = schema_object_fields(doc, path)
        except ValueError as err:
            drifts.append((schema_path.name, ts_name, [str(err)], []))
            continue
        ts_fields = ts.get(ts_name)
        if ts_fields is None:
            drifts.append((schema_path.name, ts_name, sorted(schema_fields), []))
            continue
        raw_missing = set(schema_fields) - set(ts_fields)
        raw_extra = set(ts_fields) - set(schema_fields)
        allowed_missing, allowed_extra = TS_SCHEMA_ALLOWED.get((label, ts_name), (set(), set()))
        missing = sorted(raw_missing - allowed_missing)
        extra = sorted(raw_extra - allowed_extra)
        if missing or extra:
            drifts.append((schema_path.name, ts_name, missing, extra))
        noted_missing = sorted(raw_missing & allowed_missing)
        noted_extra = sorted(raw_extra & allowed_extra)
        if noted_missing or noted_extra:
            notes.append((schema_path.name, ts_name, noted_missing, noted_extra))
    return drifts, notes, len(SCHEMA_TS_CONTRACTS)


FIXTURE_GO = """package api

type Empty struct{}

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


FIXTURE_PROTO = """syntax = "proto3";
message Empty {}
message Sample {
  string id = 1;
  repeated string tags = 2;
  map<string, string> labels = 3;
  oneof body {
    Nested nested = 10;
  }
}
message Nested { double fraction = 1; string detail = 2; }
service SampleService {
  rpc Call(Sample) returns (Nested);
}
"""

FIXTURE_SCHEMA = """{
  "type": "object",
  "properties": {
    "id": {"type": "string"},
    "nested": {
      "type": "object",
      "properties": {"value": {"type": "number"}}
    }
  }
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

    proto = proto_messages(strip_comments(FIXTURE_PROTO))
    expect_proto = {
        "Empty": {},
        "Sample": {"id": False, "tags": True, "labels": False, "nested": False},
        "Nested": {"fraction": False, "detail": False},
    }
    if proto != expect_proto:
        errors.append("proto 解析不符：%r" % (proto,))
    if proto_services(strip_comments(FIXTURE_PROTO)) != {"SampleService": ["Call"]}:
        errors.append("proto service 解析不符：%r" % (proto_services(strip_comments(FIXTURE_PROTO)),))
    if go_structs(strip_comments(FIXTURE_GO), include_empty=True).get("Empty") != {}:
        errors.append("Go 空结构体未按 include_empty 保留")
    schema = json.loads(FIXTURE_SCHEMA)
    if schema_object_fields(schema, ()) != {"id": False, "nested": False}:
        errors.append("JSON Schema 顶层字段解析不符")
    if schema_object_fields(schema, ("properties", "nested")) != {"value": False}:
        errors.append("JSON Schema 嵌套字段解析不符")

    if errors:
        for e in errors:
            print("self-test failed: " + e)
        return 1
    print("contract self-test PASS（Go/TS/proto/schema 解析器红队断言）")
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
    proto_drifts, proto_count = proto_go_parity()
    schema_go_drifts, schema_go_count = json_schema_go_parity()
    schema_ts_drifts, schema_ts_notes, schema_ts_count = json_schema_ts_parity()

    if args.list:
        print("同名类型（%d）：%s" % (len(matched), ", ".join(matched) or "无"))
        print("Go-only（%d）：%s" % (len(go_only), ", ".join(go_only) or "无"))
        print("TS-only（%d）：%s" % (len(ts_only), ", ".join(ts_only) or "无"))
        print("TS 非对象声明（%d）：%s" % (len(skipped), ", ".join(skipped) or "无"))
        print("proto messages（%d）" % proto_count)
        print("JSON Schema ↔ Go 对象（%d）" % schema_go_count)
        print("JSON Schema ↔ TS 对象（%d）" % schema_ts_count)

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

    for source, name, missing, extra in schema_ts_notes:
        print("TS 兼容例外（不判失败）：%s / %s" % (source, name))
        if missing:
            print("  schema/Go 有、TS 省略 : " + ", ".join(missing))
        if extra:
            print("  TS 兼容读取、冻结契约没有 : " + ", ".join(extra))

    parity = proto_drifts + schema_go_drifts + schema_ts_drifts
    if parity:
        print("协议/schema 契约漂移 %d 处：" % len(parity))
        for source, name, missing, extra in parity:
            print("  %s / %s" % (source, name))
            if missing:
                print("    契约有、对侧缺 : " + ", ".join(missing))
            if extra:
                print("    对侧有、契约缺 : " + ", ".join(extra))
        return 1

    print(
        "contract check PASS（%d 个同名类型字段一致；Go-only %d，TS-only %d；"
        "proto %d messages；schema %d+%d objects）"
        % (len(matched), len(go_only), len(ts_only), proto_count, schema_go_count, schema_ts_count)
    )
    return exit_code


if __name__ == "__main__":
    sys.exit(main())