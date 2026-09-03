# 设备协议契约

Cloudpath 通过统一的协议概念对接设备：`Command`（下发）、`State`（状态）、`Dump`（转储）、
`Event`（事件）。具体设备的串口协议由 `examples/<device>` 的 Adapter 实现。

## 概念

| 概念 | 含义 |
|---|---|
| Command | 上位机 → 设备的指令（对时 / 转储 / 触发 / 刷机） |
| State | 设备状态快照（在线 / 时钟 / 业务状态） |
| Dump | 设备回传的状态转储，Adapter 解析成统一 State |
| Event | 设备主动上报的事件标签（上电 / 业务事件） |

## Reference: STC-B

STC-B（IAP15F2K61S2）是第一个官方适配设备，协议实现见 `examples/stcb`。

### 物理层

| 项 | 值 |
|---|---|
| 接口 | UART（USB 转串口） |
| 波特率 | 9600 8N1 |
| 行尾 | `\n`（CRLF） |
| 编码 | ASCII |

### 命令

| 命令 | 含义 |
|---|---|
| `S` | 状态转储 |
| `R` | 触发业务动作（如强制提醒） |
| `O` | 触发业务动作（如模拟开盖） |
| `T`+4 位 `HHMM` | 对时（逐字节慢发，设备命令缓冲仅 1 字节） |
| `D` | 延迟 5s 软复位进 ISP（远程刷机） |

### 转储

`S:<state><hour><min><slots>`，8 位十进制半字节：
状态 + 时钟（hour/min，BCD）+ 业务槽位。

### 事件

`PILLBOX-BOOT` / `REMIND` / `TAKEN` / `TAKEN-LATE` / `MISSED`（事件不带时间戳，由 edge 打点）。

### 板级限制（适配时必须知晓）

- RTC hour 寄存器可能不可靠（软件 hour 补偿），上电后须由 edge 周期 `T` 对时。
- 长时间断电后上电 RTC 会被重置，重新对时即可。
- 同拍蜂鸣与串口 TX 会互相干扰，安静期通信可靠。