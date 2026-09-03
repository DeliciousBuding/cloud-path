# 设备协议契约

最后更新：2026-09-03

Cloudpath 用四个统一概念对接任何设备：`Command`（下发）、`State`（状态）、`Dump`（转储）、
`Event`（事件）。具体设备的线上协议由 `examples/<device>` 的适配器实现；本文记录概念契约与
首个参考设备（STC-B）的线上格式。

## 概念

| 概念 | 含义 | 谁负责 |
|---|---|---|
| Command | 上位机 → 设备的指令（对时 / 转储 / 触发 / 刷机 / 原始写入） | 适配器声明白名单，server 校验，edge 执行 |
| State | 设备状态快照（在线判定 + 自定义语义字段） | 适配器 `Snapshot()` 产出，核心原样透传 |
| Dump | 设备回传的状态转储行 | 适配器解析成 State |
| Event | 设备主动上报的事件标签 | 适配器归一化成平台事件类型 |

约束：

- **事件不带时间戳**：时间由 edge 打点（设备钟不可信），server 落库并广播。
- **状态幂等**：转储请求不得改变设备状态，上位机按固定周期轮询。
- **命令白名单**：只有适配器 `SupportedCommands()` 声明过的命令能被下发；
  server 拒绝白名单外的命令（400），前端命令面板也以同一份清单渲染。
- **状态字段自定义**：`State.Raw` 是 `map[string]any`，前端识别通用键
  （`clock` `hour` `min` `state` `state_label` `slots` `drift_min` `dump_raw`），
  未知键原样展示在「原始状态」面板。

## 平台事件类型

适配器把设备原始标签归一化成下列类型（前端展示标签见 `webui/src/lib/format.ts`）：

| 类型 | 含义 | 展示 |
|---|---|---|
| `BOOT` | 设备上电/复位 | 上电 |
| `REMIND` | 进入提醒状态 | 提醒 |
| `TAKEN` | 在窗口内完成确认动作 | 已确认 |
| `TAKEN-LATE` | 超过窗口后才确认 | 逾期确认 |
| `MISSED` | 窗口结束仍未确认 | 逾期未确认 |
| `SYNC-OK` | 对时成功 | 对时成功 |

新增设备可以复用这些类型，也可以在适配器里定义新类型：前端对未知类型回落显示原始名。

---

## Platform WebSocket: Plugin Control Plane

插件控制面复用 edge ↔ server 的版本 1 信封，新增三种向后兼容消息。旧实现遇到未知消息只记录并忽略，不因新增类型断开连接。完整权威划分见 [Plugin Control Plane Synchronization](architecture/control-plane-sync.md)。

| 消息 | 方向 | 作用 |
|---|---|---|
| `plugin_status` | edge → server | 上报安装物与实例实际态全量快照 |
| `plugin_desired` | server → edge | 下发该 tenant/edge 的期望态全量快照 |
| `plugin_ack` | edge → server | 确认或拒绝一个期望态 revision |

### `plugin_status`

关键字段：

- `boot_id`：Edge 进程启动标识；新进程使用新值；
- `sequence`：同一 `boot_id` 下单调递增，重复/倒序上报被忽略；
- `applied_revision`：Edge 最近完整应用成功的 Server revision；
- `installations`：只含 manifest、digest、trust、permission、contribution 等公开元数据；
- `instances`：只含 state/health/restart/metrics 等实际态。

payload 不含 tenant/edge 自报身份；Server 必须使用已经鉴权并绑定的 edge 连接身份。禁止上报本地路径、配置值、环境变量、token 或 secret 明文。

### `plugin_desired`

关键字段：

- `revision`：Server 为该 tenant/edge 分配的单调期望态版本；
- `snapshot_digest`：绑定规范化完整快照；
- `instances`：实例的 plugin/version/enabled/isolation/config。敏感配置只能是 `secret://<name>` handle。

Edge 拒绝旧 revision。相同 revision + 相同 digest 是幂等重放；相同 revision + 不同 digest 是协议冲突，必须 fail-closed。

### `plugin_ack`

`status` 只允许：

- `applied`：整个快照已经完整应用，可以推进 `applied_revision`；
- `rejected`：revision/digest/权限/租户等契约错误；
- `failed`：运行时应用失败，继续保持上一完整 revision。

`results` 可按实例返回状态和经过长度限制、路径/secret 脱敏的 detail。Server 不把 ack 成功混同插件健康；健康只来自后续 `plugin_status`。
## Reference: STC-B

STC-B（IAP15F2K61S2）学习板是第一个官方适配设备，实现见 [`examples/stcb`](../examples/stcb/README.md)。

### 物理层

| 项 | 值 |
|---|---|
| 接口 | UART（USB 转串口） |
| 波特率 | 9600 8N1 |
| 行尾 | `\n`（可能带 `\r`） |
| 编码 | ASCII（损坏字节可能出现 U+FFFD） |

### 命令

| 平台命令 | 线上写入 | 说明 |
|---|---|---|
| `dump` | `S` | 请求状态转储 |
| `trigger` | `R` | 触发一次提醒（联调用） |
| `open` | `O` | 模拟一次确认动作（开盖） |
| `sync` | `T` + 4 位 `HHMM` | 对时。**逐字节 50ms 慢发**：固件命令缓冲仅 1 字节，快发会丢字符 |
| `isp` | `D` | 延迟 5 秒软复位进入 ISP 烧录模式（随后离线，直到重新烧录） |
| `raw` | `args` 原样 | 高级通道：直接写串口（server 限制长度 ≤64、不含换行/NUL） |

### 转储格式

```
S:<state><hour><min><slot0><slot1><slot2>
```

- `S` 与 8 位数字之间允许出现损坏字符（响铃期 UART 受干扰，`:` 可能变成 U+FFFD）；
- 8 位均为十进制数字（BCD 风格），依次是：状态机、时、分、三个槽位；
- 语义：`state` 0 待机 / 1 提醒中 / 2 逾期；`slot` 0 待确认 / 1 已确认 / 2 逾期；
- 合法性校验：`hour ≤ 23`、`min ≤ 59`、`state ≤ 2`、`slot ≤ 2`，越界即视为损坏行丢弃；
- 解析用「搜索」而非「锚定」：可以从噪声前缀中抢救出合法转储（如 `O:21S:21213120`）。

示例：`S:01213120` → 待机、12:13、槽位 [已确认, 逾期, 待确认]。

### 事件行

固件按行输出事件标签，适配器归一化：

| 线上标签 | 归一化类型 |
|---|---|
| `REMIND` | `REMIND` |
| `TAKEN` | `TAKEN` |
| `TAKEN-LATE` / `LATE` | `TAKEN-LATE` |
| `MISSED` | `MISSED` |
| `BOOT` / 带任意厂商前缀的 `*-BOOT` | `BOOT` |
| `OK` | `SYNC-OK` |

匹配规则：先精确匹配，再包含匹配（容忍 `[RAW-NOEOL] REMIND` 这类噪声前后缀）；
长标签优先（`TAKEN-LATE` 不被 `TAKEN` 截胡）；以 `S` 开头的行按转储处理，不当事件。

### 板级限制（适配时必须知晓）

这些是硬件/固件事实，平台按此设计调度与解析：

- **RTC hour 寄存器不可靠**：固件用软件 hour 补偿；上位机必须周期对时。
- **长时间断电后 RTC 被重置**：edge 在设备打开时立即 `sync` + `dump`，随后按
  `sync_interval_s` 周期对时。
- **响铃与串口 TX 同拍互相干扰**：解析器容忍损坏行；edge 的轮询与对时不需要与设备动作同步。
- **设备钟只有时/分两位**：漂移精度天然 ±1 分钟，展示与告警阈值按此设定
  （|d| ≤ 1 优、≤ 5 良、更大差）。
- **命令缓冲仅 1 字节**：多字节命令必须逐字节慢发（见 `sync`）。
