# cloud-path-driver-stcb（孵化目录 `examples/stcb/`）

STC-B（IAP15F2K61S2，UART 9600 8N1）参考 Driver，CloudPath 的第一个官方 reference device。
它同时是「如何写一个适配器」的范例：串口协议解析、状态语义、命令通道、热插拔与单测都在本目录，
CloudPath 核心（`internal/*`）不认识这块板子。

## 孵化状态（先读这一段）

当前阶段本目录是**进程内参考适配器**：`cloudpath-edge` / `cloudpath-server` 通过 blank import
把本包编译进二进制（见 `cmd/*/main.go`），本目录没有独立 `go.mod`，也没有独立进程入口。

`plugin.yaml` 已冻结拆仓后的机器契约（独立仓库 `cloud-path-driver-stcb` 的根 manifest）。
拆仓时才会生成进程入口二进制并实现 Driver Protocol v1；在那之前请把本目录当作
「只依赖公开 SDK 的参考实现 + 冻结契约」，而不是可安装的独立插件。

## 依赖

| 依赖 | 说明 |
|---|---|
| Go ≥ 1.26 | 跟随核心仓库 `go.mod`（`go 1.26.3`） |
| `sdk/go/driverkit`、`sdk/go/model` | CloudPath 公开 Go SDK |
| `go.bug.st/serial` | 串口打开（第三方） |

红线：本目录**不 import 任何 `github.com/DeliciousBuding/cloud-path/internal/**`**。
由两处门禁持续锁定：

```text
$ grep -R "internal/" examples/stcb/ --include="*.go"   # 零命中
$ go test ./sdk/go/driverkit/...                        # TestSTCBHasNoInternalImports
```

## 构建

本目录是核心 module 内的包，没有独立 go.mod（拆仓时生成）。在仓库根执行：

```bash
go build ./...                 # 全仓
go build ./examples/stcb/...   # 只编译本目录
```

## 配置

适配器通过 `driverkit.Config` 打开设备（`ID`/`Name`/`Port`/`Baud`）。下面是**占位符示例**，
不含任何真实端口/主机/账号：

```go
cfg := driverkit.Config{
    ID:   "edge-example",   // 设备稳定标识（由 edge/server 配置注入）
    Name: "示例节点",
    Port: "COM_PORT",       // Windows 示例；Linux/macOS 用 /dev/ttyUSB0 一类设备路径
    Baud: 9600,             // 缺省 9600；协议要求 8N1
}
```

- 在线判定：端口未死且 30 秒内收到过转储。
- 设备身份（DeviceID/ExternalID）来自 `cfg.ID`，本目录不硬编码任何设备/主机。

## 串口协议速览

转储行：`S:<state><hour 2位><min 2位><三槽各1位>`，共 8 位十进制半字节。完整契约见
[`docs/protocol.md`](../../docs/protocol.md)（拆仓后改为指向核心仓库公开文档的外部链接）。

| 命令 | 线上写入 | 说明 |
|---|---|---|
| `dump` | `S` | 请求一次状态转储 |
| `trigger` | `R` | 触发提醒（联调用） |
| `open` | `O` | 模拟一次确认动作 |
| `sync` | `T` + `HHMM` | 对时。逐字节 50ms 慢发（固件命令缓冲只有 1 字节，快发会丢） |
| `isp` | `D` | 延迟 5 秒软复位进入 ISP 烧录模式（设备随后离线） |
| `raw` | `args` 原样 | 高级：直接写串口（server 侧限制长度 ≤64、不含换行/NUL） |

命令集由 `SupportedCommands()` 声明；server 拒绝白名单外的命令。

## 上报的状态字段

`Snapshot().Raw` 是设备自定义语义，原样透传：

| 键 | 类型 | 含义 |
|---|---|---|
| `state` | int | 状态机：0 待机 / 1 提醒中 / 2 逾期 |
| `state_label` | string | 状态中文标签 |
| `hour` `min` | int | 设备钟（时/分） |
| `clock` | string | `"HH:MM"` |
| `slots` | array | 三个槽位 `{index, code, label}`，code：0 待确认 / 1 已确认 / 2 逾期 |
| `drift_min` | number | 设备钟与参考时间（Asia/Shanghai）的偏差分钟数 |
| `dump_raw` | string | 原始转储行（取证/排障） |

## 板级限制（适配时必须知道）

这些是硬件/固件事实，不是 CloudPath 的 bug：

- RTC 的 hour 寄存器可能不可靠 → 固件用软件 hour 补偿；上电后必须对时。
- 长时间断电后 RTC 会被重置 → edge 在设备打开时立即 `sync` + `dump`，并周期对时。
- 响铃（蜂鸣）与串口 TX 同拍会互相干扰 → 解析器容忍损坏行（`S` 后允许出现替换字符），
  并从噪声前缀里抢救合法转储。
- 设备钟只有时/分两位 → 漂移精度天然 ±1 分钟。

## 测试与验证

在仓库根执行：

```bash
go test ./examples/stcb/... -count=1
go vet ./examples/stcb/...
```

测试锁定：

- 协议解析黄金样本（含损坏分隔符、噪声前缀、越界值拒绝）；
- Descriptor/Capability 契约：每个 Entity 声明的 Capability 都能在 catalog 解析；
- `plugin.yaml` 冻结契约：必填字段、capability 声明与代码 catalog 一致、
  贡献 ID 与适配器名一致（`manifest_test.go`）。

## 卸载 / 移除

- **孵化阶段**：删除 `cmd/cloudpath-edge` 与 `cmd/cloudpath-server` 的 `main.go` 中
  `_ "github.com/DeliciousBuding/cloud-path/examples/stcb"` 的 blank import，再删除
  `examples/stcb/`。拆仓时核心仓库只保留安装示例与 Registry 指针，不再编译期引用本包。
- **独立发布后**：`cloudpath plugin remove io.github.deliciousbuding.cloud-path-driver-stcb`。

## 机器身份契约（不要随意改）

以下 ID 一经发布即为稳定契约；破坏性语义变化升 `@2`，patch 版本不得静默扩大权限：

| 对象 | 值 |
|---|---|
| plugin id | `io.github.deliciousbuding.cloud-path-driver-stcb` |
| version / protocol | `0.1.0` / `1` |
| entrypoint（拆仓后二进制） | `cloudpath-driver-stcb` |
| driver contribution id | `stcb` |
| capability | `cloudpath.dev/capability/clock@1`、`cloudpath.dev/capability/alarm@1`、`cloudpath.dev/capability/contact@1` |
| entity | `clock`、`alarm`、`compartment-1..3` |

`plugin.yaml` 与代码常量的交叉锁定见 `manifest_test.go`。

## 换一块板子要改什么

只改本目录：解析规则（`parser.go`）、命令映射（`Send`）、状态字段（`Snapshot`）。
若字段名沿用 `clock/drift_min/slots/state`，前端无需任何改动即可展示。
