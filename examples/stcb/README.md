# examples/stcb — STC-B 参考适配器

Cloudpath 的第一个官方 reference device：STC-B 学习板（IAP15F2K61S2，UART 9600 8N1）。
它同时是"如何写一个适配器"的范例：协议解析、状态语义、命令通道、热插拔与单测都在这里，
核心（`internal/*`）不认识这块板子。

## 文件

| 文件 | 职责 |
|---|---|
| `stcb.go` | `device.Adapter` 实现：串口打开、RX 循环、命令写入、`Snapshot()` 状态输出 |
| `parser.go` | 协议解析（转储行 / 事件行）+ 语义标签 + 漂移计算，纯函数无 I/O |
| `parser_test.go` | 黄金样本单测（真实板测捕获行，含损坏分隔符、噪声前缀、越界值） |

## 上报的状态字段

`Snapshot().Raw` 是设备自定义语义，原样透传到前端与 SQLite：

| 键 | 类型 | 含义 |
|---|---|---|
| `state` | int | 状态机：0 待机 / 1 提醒中 / 2 逾期 |
| `state_label` | string | 状态中文标签 |
| `hour` `min` | int | 设备钟（时/分） |
| `clock` | string | `"HH:MM"` |
| `slots` | array | 三个槽位 `{index, code, label}`，code：0 待确认 / 1 已确认 / 2 逾期 |
| `drift_min` | number | 设备钟与参考时间（Asia/Shanghai）的偏差分钟数 |
| `dump_raw` | string | 原始转储行（取证/排障） |

在线判定：端口未死 **且** 30 秒内收到过转储。

## 命令

| 命令 | 线上写入 | 说明 |
|---|---|---|
| `dump` | `S` | 请求一次状态转储 |
| `trigger` | `R` | 触发提醒（联调用） |
| `open` | `O` | 模拟一次确认动作 |
| `sync` | `T` + `HHMM` | 对时。**逐字节 50ms 慢发**（固件命令缓冲只有 1 字节，快发会丢） |
| `isp` | `D` | 延迟 5 秒软复位进入 ISP 烧录模式（设备随后离线） |
| `raw` | `args` 原样 | 高级：直接写串口（server 侧限制长度 ≤64、不含换行/NUL） |

命令集由 `SupportedCommands()` 声明，server 拒绝白名单外的命令，前端命令面板自动跟随。

## 板级限制（适配时必须知道）

这些是硬件/固件事实，不是 Cloudpath 的 bug，适配器与上层调度都按此设计：

- RTC 的 hour 寄存器可能不可靠 → 固件用软件 hour 补偿；上电后必须对时。
- 长时间断电后 RTC 会被重置 → edge 在设备打开时立即 `sync` + `dump`，并周期对时。
- 响铃（蜂鸣）与串口 TX 同拍会互相干扰 → 解析器容忍损坏行（`S` 后允许出现替换字符），
  并从噪声前缀里"抢救"合法转储。
- 设备钟只有时/分两位 → 漂移精度天然 ±1 分钟。

## 换一块板子要改什么

只改这个目录：解析规则（`parser.go`）、命令映射（`Send`）、状态字段（`Snapshot`）。
若字段名沿用 `clock/drift_min/slots/state`，前端无需任何改动即可展示。
