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
| `capabilities` | edge → server | 上报本 Edge 全部适配器自述的 Capability 文档（全量覆盖） |

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

### `capabilities`

外部 Driver Plugin 的能力文档（Capability 标题、Property 单位/读写、Event 声明、Action
`inputSchema`）只存在于 Edge 侧的插件进程里，而 `GET /api/capabilities` 与前端 Schema 驱动
UI 都跑在 Server 侧。本消息就是这条通道；没有它，装了新 Driver 的设备在 WebUI 上只有裸
观测值、没有命令面板。

- 载荷是 `sources[]`，每个元素为 `{source, capabilities[]}`；`source` 是声明者（外部 Driver 的
  driver id，或进程内适配器名）；
- **全量覆盖语义**：一次上报即本 Edge 当前全部声明者，Server 整体替换该 Edge 的文档集。
  没有增量/删除消息，因此插件停用或卸载后不会在 catalog 里留下幽灵能力；
- 文档随连接生命周期存在：Edge 断线即清理，重连必须重报；
- 每条文档按 `spec/capability.schema.json` 校验，非法文档单条跳过；声明者形状/规模超限
  （>64 声明者或单声明者 >256 条）则整批拒绝并保留旧文档（fail-closed）；
- 同一 Capability ID 同时来自 Server 进程内适配器与 Edge 上报时，**以进程内为准**：
  平台契约不被插件改写；
- 不向浏览器广播：前端消费路径是 `GET /api/capabilities` 与 `/api/descriptors` 的随行
  `capabilities` 字段，保持单一事实源。
## Reference: 设备侧协议归属

平台契约到 `capabilities` / `descriptor` / `state` / `event` / `command` 为止。**具体设备的线协议
（串口帧、字节序、时序、板级怪癖）不属于本仓库**，由对应 Driver Plugin 仓库拥有：

| 设备 | Driver 仓库 |
|---|---|
| STC-B（IAP15F2K61S2） | [`cloud-path-driver-stcb`](https://github.com/DeliciousBuding/cloud-path-driver-stcb)（STC-B Device Protocol v1） |

这样划分的直接后果：新增一种硬件不需要改 Core，也不需要改本文件；Core 只认
Device / Entity / Capability / Observation / Event / Command 六个概念。写新 Driver 的步骤见
[How to build a CloudPath Driver](architecture/how-to-build-driver.md)。
