# 设备协议契约

最后更新：2026-09-09

CloudPath 的公共设备模型是 **Device / Entity / Capability / Observation / Event / Command**。
设备线协议、串口帧、厂商字段和板级容错由对应 Driver Plugin 拥有；Core 只处理平台模型和
版本化消息。具体设备的协议入口见文末参考表。

## 概念

| 概念 | 含义 | 责任边界 |
|---|---|---|
| Device | 一台物理或虚拟设备 | Driver 发现并提供稳定 `device_id` / `external_id` |
| Entity | Device 下可独立观察或控制的逻辑单元 | Driver 声明稳定 `entity_id`、名称和类别 |
| Capability | Entity 能做什么的版本化声明 | Driver 声明 Properties、Events、Actions 与 UI hints |
| Observation | 当前状态的类型化采样值 | 保留 `observed_at`、`received_at`、质量和 sequence；不从旧 `raw` 猜实体 |
| Event | 不可覆盖的时间点事实 | Type 属于 Capability 或 Application 命名空间；设备级事件可省略 `entity_id` |
| Command | 对声明动作的一次请求 | 命令白名单来自 Driver/Capability 的 action 声明；参数由 Core 做传输边界校验 |

约束：

- **协议不枚举具体业务事件**：`Event.type` 对 Core 是不透明字符串。标准事件由 Capability
  或 Application 命名空间声明，旧适配器发送的标签继续兼容；Core 不维护
  `BOOT` / `REMIND` / `TAKEN` 等设备专用枚举。
- **状态幂等**：读取状态的轮询不得改变设备状态。轮询命令名由适配器显式声明，Core 只按
  白名单下发。
- **命令白名单**：只有 Driver 的 `ActionDescriptor` / 适配器 `SupportedCommands()` 声明过的动作
  能被下发；Server 拒绝白名单外命令，前端命令面板消费同一份声明。
- **Raw 只作兼容和诊断**：`State.raw` 可以承载旧设备字段，但不是跨 Driver 的语义契约。
  主路径使用 Descriptor / Entity / Capability / Observation；未知字段原样保留，不得推断业务含义。
- **时间与质量**：设备时钟不可信时，Driver/Edge 必须保留真实采样时间并标明质量；Core 不从
  时间字符串或旧字段伪造 Observation。

---

## RPC 与 Schema 契约层次

- `proto/cloudpath/v1/*.proto` 是 Driver/Application Protocol v1 的语言无关文本，不是 protoc 输入。当前可执行实现是 Go SDK 的手写 struct + JSON codec；`scripts/check_contract.py` 同时校验 proto 字段/服务方法与 Go SDK，防止两套文本各自漂移。
- `spec/plugin-manifest.schema.json` 是 Manifest 的运行时 schema，发布二进制通过 `pluginschema.go` 内嵌；`spec/descriptor.schema.json` 与 `spec/capability.schema.json` 是设备/能力公开 schema，`sdk/go/model` 是它们的 Go 校验实现。schema ↔ Go 字段集合由同一门禁守。
- 不在冻结 schema 中的 TS 旧字段（Capability action `command`/`primary`、Event `title`/`description`）只是 WebUI 兼容读取，不进入 Go/Edge 生产转换；新 Driver 不应依赖。嵌套 `Entity.observations` 省略 `entity_id` 是上下文省略，独立 Observation 仍可携带它。

| 协议面 | 当前接入状态 |
|---|---|
| Driver `Initialize` / `Describe` / `ConfigureInstance` / `OpenDevice` / `CloseDevice` / `Watch` / `Execute` / `Health` / `Shutdown` | 当前 Edge Plugin Host / external driver 路径已接入 |
| Driver `Discover` / `DiscoveryEvent` | SDK 与 conformance harness 已定义；当前生产 Edge 宿主不调用，属未来/可选发现层 |
| `WatchRequest.resume_from_sequence`、`max_buffered`、`InitializeResponse.replay_supported` | 协议保留；当前 Edge 宿主不请求/不依赖 replay，不能当成已交付能力 |
| `CommandProgress` / `Diagnostic` | 协议保留；当前 external driver 消费路径不处理，属未来/诊断扩展 |
| Application `Initialize` / `Describe` / `ConfigureInstance` / `ValidateBinding` / `HandleEvents` / `RunJob` / `Health` / `Shutdown` | 当前 AppHost/appruntime 已接入；`RunJob` 服务手动/调度任务 |
| Application `HandleRequest` | SDK/appruntime 路径存在；当前没有公开 `/api/plugins/...` 路由，属未来层 |
| `ApplicationDescriptor.declarative_only` | 字段保留；当前没有独立声明式执行器，Application 仍需 process `entrypoint` |
| `SendNotification` effect | 协议识别；Core 无通知通道，fail-closed 返回 `not_implemented` |

`InitializeRequest` 的 `handshake_cookie` / `node_id` / `host_info` 为协议保留字段；当前 Host 只发送 launch/protocol/runtime 字段，业务不得依赖这些字段存在。

---

## 平台 WebSocket：插件控制面

插件控制面复用 edge ↔ server 的版本 1 信封，新增四类向后兼容消息。旧实现遇到未知消息只记录并忽略，不因新增类型断开连接。完整权威划分见 [Plugin Control Plane Synchronization](architecture/control-plane-sync.md)。

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
- 每条文档按 `spec/capability.schema.json` 的字段/枚举语义校验（Go 侧由 `sdk/go/model.Validate` 实现，parity 由 `scripts/check_contract.py` 守），非法文档单条跳过；声明者形状/规模超限
  （>64 声明者或单声明者 >256 条）则整批拒绝并保留旧文档（fail-closed）；
- 同一 Capability ID 同时来自 Server 进程内适配器与 Edge 上报时，**以进程内为准**：
  平台契约不被插件改写；
- 不向浏览器广播：前端消费路径是 `GET /api/capabilities` 与 `/api/descriptors` 的随行
  `capabilities` 字段，保持单一事实源。
- Action 的 `destructive` 与 `confirmation` 必须与 `inputSchema` 一同跨传输保留。
  Driver RPC `ActionDescriptor` 的 `title`、`description`、`destructive`、`confirmation` 为可选字段，
  对应 proto 字段号 4–7；字段号 1–3 及旧 JSON 形状保持不变。Edge 把这些字段原样转换为
  Capability Action，`input_schema_json` 转换为 `inputSchema`，再由 `capabilities` 消息传到 Server。
  旧 Driver 缺省这些字段时仍兼容，不由 Edge 补造标题或危险性。
  破坏性标记触发交互确认，确认文案原样展示；二者不是命令权限或硬件安全校验的替代。

## Reference: 设备侧协议归属

平台契约到 `capabilities` / `descriptor` / `state` / `event` / `command` 为止。**具体设备的线协议
（串口帧、字节序、时序、板级怪癖）不属于本仓库**，由对应 Driver Plugin 仓库拥有：

| 设备 | Driver 仓库 |
|---|---|
| STC-B（IAP15F2K61S2） | [`cloud-path-driver-stcb`](https://github.com/DeliciousBuding/cloud-path-driver-stcb)（STC-B Device Protocol v1） |

这样划分的直接后果：新增一种硬件不需要改 Core，也不需要改本文件；Core 只认
Device / Entity / Capability / Observation / Event / Command 六个概念。写新 Driver 的步骤见
[How to build a CloudPath Driver](architecture/how-to-build-driver.md)。

## 应用观测输入（Core 0.2.15 起）

`state.observations` 是可选的实体观测数组，每项为 `{entity_id, observations: {property: Observation}}`。
Edge 保留真实 `observed_at`、质量和序号，单独补 `received_at`；数值不变也能报告新的采样。
Core 不从旧 `raw` 字段推断实体，未声明/未绑定/跨租户实体不进入应用。已绑定能力的每条观测
通过 `CapabilityEvent` 投递，`event_type=cloudpath.dev/event/property-observed@1`，`payload_json` 为
Observation 对象。它是采样事实，不是用户按键事件、业务结局或执行器 ACK。

`JobDescriptor.manual_only` 为兼容扩展（默认 false）。true 的 job 不进入隐式分钟循环，
仅由已鉴权的显式应用操作请求调用；旧宿主不识别此约束，因此使用者必须声明最低 Core 0.2.15。
输入与操作的完整边界见 [设计](design.md#应用输入与操作契约) 和 [HTTP API](api.md#551-应用手动操作operator)。
