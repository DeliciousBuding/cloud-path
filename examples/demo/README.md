# examples/demo —— CloudPath 参考演示设备适配器（reference demo device）

> **诚实性声明（红线）**：这不是真实硬件驱动。它不打开串口、不访问任何外设、
> 不生成随机假数。它上报的是**本进程内真实维护的状态**，命令会**真实改变**这些
> 进程内状态并产生事件。UI 与上报中它始终标注为 `reference-demo-device`。

## 为什么存在

验收场景要求「多台电脑上的 ≥3 台真实设备、实时状态、分别控制、断线重连」。
不是每个人手上都有 STC-B 板子；没有板子的电脑如果接不进来，整条链路就无法
被验证。`demo` 适配器让**零硬件**的机器也能跑通：

```text
Edge(demo 设备) → Server → WebUI：上线/实时状态/独立命令/事件/断线重连
```

它验证的是**平台链路**（多 Edge、多设备、路由不串线、重连全量重报），
不是物理设备行为。真实硬件验证仍然只认真板。

## 注册的适配器

| 项 | 值 |
|---|---|
| `Adapter.Name()` | `demo` |
| 命令白名单 | `ping`、`set`、`dump`、`noop` |
| 需要端口 | **否**（`PortRequired() == false`；`port` 字段被有意忽略） |
| Descriptor | 6 个 Entity（见下） |
| Capability | `counter@1`、`uptime@1`、`setpoint@1`、`toggle@1`、`diagnostics@1` |

命令白名单是唯一事实源：server 的命令校验与前端命令面板都读
`SupportedCommands()`，本目录不再另立命令表。

## 进程内真实状态

| 字段（`State.Raw`） | 含义 | 变化来源 |
|---|---|---|
| `kind` | 恒为 `reference-demo-device` | 常量（诚实标注） |
| `hardware` | 恒为 `none` | 常量（诚实标注） |
| `uptime_s` | 自 `Open` 起的真实运行秒数 | 时间 |
| `ticks` | 心跳计数 | 内部 tick 循环（默认 10s，`extra.tick_interval_s` 可调） |
| `commands` | 已接受命令计数 | 每条白名单命令 |
| `pings` | `ping` 命令计数 | `ping` |
| `level` | 可写回数值设定 | `set value=<整数>` |
| `enabled` | 可写回开关 | `set enabled=<true\|false>` |
| `tick_rate` | 当前心跳周期 | 配置 |

没有随机数：同样的命令序列产生同样的状态，`set` 写进去的值可以原样读回。

## 命令语义

| 命令 | 行为 | 事件 |
|---|---|---|
| `ping` | `pings++`、`commands++` | `probed` |
| `set` | 写回 `value` / `enabled`，`commands++` | 值真变化时 `setpoint-changed` / `toggled` |
| `dump` | `commands++`（状态本身始终实时可读） | 无 |
| `noop` | `commands++` | 无 |
| 其它 | 返回错误（fail-closed，绝不静默成功） | 无 |

`Open` 时产生一次 `device-booted`。

`set` 参数三种等价写法（前端按 action `inputSchema` 生成的种子是第 2 种）：

```text
value=42 enabled=true
{"value":42,"enabled":true}
42                      # 等价于 value=42
```

未知键、非整数 `value`、非布尔 `enabled` 一律报错，不静默忽略。

## Descriptor / Entity

| entity_id | 名称 | category | capability | 主观测 |
|---|---|---|---|---|
| `heartbeat` | 心跳 | sensor | `counter@1` | `value` = ticks |
| `switch` | 开关 | actuator | `toggle@1` | `state` = enabled |
| `uptime` | 运行时长 | diagnostic | `uptime@1` | `seconds` |
| `commands` | 命令计数 | diagnostic | `counter@1` | `value` |
| `diagnostics` | 诊断 | diagnostic | `diagnostics@1` | `status` = `reference-demo-device` |
| `level` | 设定值 | config | `setpoint@1` | `value` = level |

Capability `spec.actions` 的键**等于命令名**（`set` / `ping` / `dump` / `noop`），
因此 schema-driven UI 渲染出的是真实按钮与参数输入框，而不是 JSON 编辑器。
`diagnostics@1` 带 `presentation.headline=true`，设备卡片首屏主值即为
`reference demo device`——诚实标注在列表页就可见。

观测值刻意不写 `observed_at` / `received_at`：时间戳每拍都变会击穿 edge 的
Descriptor diff 抑制（与进程内参考适配器同一约定），`received_at` 由可信的
Edge/Core 生成。

## 最小配置（无硬件）

```yaml
server: wss://cloudpath.vectorcontrol.tech/ws/edge
token: ${CLOUDPATH_TOKEN}
edge_id: my-laptop

devices:
  - id: demo-1
    adapter: demo
    name: 参考设备 1
  - id: demo-2
    adapter: demo
    name: 参考设备 2
    extra:
      tick_interval_s: "5"
```

`port` / `baud` 可以省略（`demo` 不需要端口）。一台 Edge 可挂多台 demo 设备，
每台各自独立监督协程；配合多台电脑各自的 Edge，即可凑出跨机器的 ≥3 台设备。

## 接线要求（两个二进制都已注册）

适配器靠包 `init()` 注册进 `driverkit` 注册表，因此**消费它的二进制必须 blank import
本包**，两侧作用不同：

| 二进制 | import | 作用 | 现状 |
|---|---|---|---|
| `cmd/cloudpath-edge` | `_ ".../examples/demo"` | 真的打开设备、上报状态 | ✅ 已接线 |
| `cmd/cloudpath-server` | `_ ".../examples/demo"` | `GET /api/adapters` 命令白名单 + `GET /api/capabilities` catalog | ✅ 已接线 |

任一二进制缺这行 import，都会导致 `GET /api/adapters` / `GET /api/capabilities` /
`/api/descriptors` 少列 `demo`，前端命令按钮（Capability actions 与适配器白名单回落）
同时落空；`handlePostCommand` 的白名单校验也会整段跳过（server 侧 fail-open，仅由
Edge 侧 `dev.Send` 的命令白名单兜底）。保持两行 blank import 与 server 端注册表同源。

## 拆仓红线

本包只依赖 `sdk/go/driverkit` 与 `sdk/go/model`，**不 import 任何 `internal/*`**
（`demo_test.go` 的导入扫描锁定该红线）。
