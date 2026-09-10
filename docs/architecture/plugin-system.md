# Plugin System 设计与运行时

最后更新：2026-09-10

> 状态：Manifest、SDK 与进程运行时以当前代码、`spec/` 和
> [how-to-build-driver.md](how-to-build-driver.md) 为准。本文说明多类型插件边界、控制面同步与故障语义；
> 明确标注“目标态”的内容不得当成当前能力。

## 1. 统一平台，多类插件

单插件仓库的根 `plugin.yaml`、或 monorepo catalog 中 `<path>/plugin.yaml` 是安装与信任的机器可读声明。Manifest 的 `kind` 决定贡献类型：

| kind | Protocol | 默认宿主 | 当前状态 |
|---|---|---|---|
| `Driver` | Driver Protocol v1 | Edge Plugin Host | 已实现 |
| `Application` | Application Protocol v1 | Server AppHost | 已实现 |
| `Connector` | Manifest contribution only | Edge 或 Server，由贡献声明决定 | 安装声明可校验，运行时待实现 |

UI 贡献不是独立可执行插件类型，也不使用 `views` Manifest 字段；Core 通过
Descriptor/Capability schema 渲染通用界面。Transform/WASM 属于后续目标态，不在当前 `kind` 枚举中。

禁止把所有插件塞进一个 `DoEverything` RPC。

## 2. Manifest v1alpha1

单插件仓库使用根 `plugin.yaml`；monorepo 的根 `plugins.yaml` 只负责选择条目，Manifest 固定在
`<path>/plugin.yaml`。顶层字段必须符合
[`spec/plugin-manifest.schema.json`](../../spec/plugin-manifest.schema.json)；`kind` 只能是
`Driver`、`Application` 或 `Connector`。

```yaml
apiVersion: plugins.cloudpath.dev/v1alpha1
kind: Driver
id: io.github.example.cloud-path-driver-example
version: 0.1.0
protocol: 1
entrypoint: cloudpath-driver-example
compatibility:
  core: ">=0.2.0 <0.4.0"
permissions:
  hardware: [serial]
  network: []
  filesystem: []
  secrets: []
capabilities:
  - io.github.example/capability/example@1
contributes:
  drivers:
    - id: example-driver
      title: Example Driver
      discovery: manual
```

`contributes` 是可选块；Driver、Application、Connector 分别使用
`contributes.drivers`、`contributes.applications`、`contributes.connectors`。每个贡献的 `id`
必须稳定且与插件 `id` 不同。`permissions` 的 `hardware` / `network` / `filesystem` / `secrets`
都是字符串数组，不是对象。字段全集与约束以 schema 为准。

新增 Driver 的复制模板、实现、测试和发布流程见
[How to Build a New CloudPath Driver](how-to-build-driver.md)。

### 不可混用的版本

- `version`：插件发布包 SemVer。
- `apiVersion`：Manifest 结构版本。
- `protocol`：进程 RPC 协议版本（当前为正整数 `1`）。
- `compatibility.core`：Core 产品兼容范围。
- Capability 尾部版本：数据语义版本。
- 配置 Schema 自己的 `schemaVersion`。

## 3. 运行时边界

### Driver Plugin

- 一进程可管理多个 Driver Instance 和多台设备。
- 只从 Host 获得批准后的端口/资源，不扫描未授权范围。
- 设备发现、连接、解析和命令执行在插件内。
- 所有上报经 Host 校验尺寸、类型、时间和速率后进入 Core。

### Application Plugin

当前可执行路径是 **process Application**：业务逻辑运行在 Server AppHost 子进程中，通过
Application Protocol v1 调用 Core。SDK descriptor 仍保留 `declarative_only` 字段，但当前没有
独立的“仅声明式、无进程”执行器；需要安装的 Application 仍必须具备 manifest `entrypoint`。
纯声明式执行属于目标态，不得把它当成现成运行时。

Application Backend 不直接打开 Core SQLite，也不获得全局管理员令牌。协议预留的插件 HTTP
子路由命名空间是 `/api/plugins/{plugin_id}/instances/{instance_id}/...`；当前公开 Server 路由面
仍以 [api.md](../api.md) 为准，不表示任意插件子路由已经开放。请求上下文由 Core 注入 tenant、actor、
instance 和 scope。数据写入插件 namespaced store 或插件专属数据目录，纳入备份清单。

### Connector Plugin

Connector 目前只有 Manifest 贡献声明，没有进程运行时。合法形状是：

```yaml
contributes:
  connectors:
    - id: mqtt-export
      direction: outbound
      host: server
    - id: local-modbus-gateway
      direction: inbound
      host: edge
```

安装/目录/权限披露可以识别该声明；启动 Connector 会被 host 以 `connector plugin kind is not
supported` fail-closed。Transport 生命周期、Mapping Schema 和通知投递属于目标态。

## 4. 进程启动与握手

当前实现使用进程启动握手和本地 RPC 传输；transport 可替换，不把 named pipe 写死为协议的一部分。流程：

```text
Host 生成 launch_id + 随机一次性 cookie
  → 启动子进程并通过 env/handle 传入
  → Plugin 在 stdout 输出唯一 handshake 行
  → Host 验证 cookie、插件 ID、协议版本和地址
  → 建立本地 RPC 传输（当前为长度前缀 JSON 帧）
  → 调用 Initialize / Describe / Health
```

握手示意：

```text
CP1|<plugin-id>|driver=1|tcp|127.0.0.1:49172|grpc|<launch-id>|<proof>
```

- Windows 初期默认 loopback TCP 随机端口，后续可增加 named pipe transport。
- Linux/macOS 优先 Unix socket。
- 测试使用 `bufconn` 或同等内存传输。
- 握手第 6 字段固定为 `grpc`，这是兼容标记；当前 SDK RPC 实际使用长度前缀 JSON 帧。
- Cookie/proof 是误启动防护，不等同安全沙箱；传输仍需限制在本机并绑定 launch identity。

## 5. Driver Protocol v1

```protobuf
service DriverService {
  rpc Initialize(InitializeRequest) returns (InitializeResponse);
  rpc Describe(DescribeRequest) returns (DriverDescriptor);
  rpc ConfigureInstance(ConfigureInstanceRequest) returns (ConfigureInstanceResponse);
  rpc Discover(DiscoverRequest) returns (stream DiscoveryEvent);
  rpc OpenDevice(OpenDeviceRequest) returns (OpenDeviceResponse);
  rpc CloseDevice(CloseDeviceRequest) returns (CloseDeviceResponse);
  rpc Watch(WatchRequest) returns (stream DriverMessage);
  rpc Execute(ExecuteRequest) returns (ExecuteResponse);
  rpc Health(HealthRequest) returns (HealthResponse);
  rpc Shutdown(ShutdownRequest) returns (ShutdownResponse);
}
```

`DriverMessage` 使用 oneof：

```text
DeviceUpsert
EntityUpsert
Observation
Event
CommandProgress
Diagnostic
```

设计约束：

- 每条消息有 `plugin_instance_id`、sequence 和 schema version。
- Core 对同 instance/device 的 sequence 去重。
- Stream 断开后插件/Edge 能从最后确认 sequence 续传；无能力时明确声明 `replay=false`。
- `Execute` 接收幂等键和 deadline；插件必须回报接受/拒绝，长任务通过 `CommandProgress` 更新。
- 单消息、每秒消息数、日志速率和排队长度均有限制。

## 6. Application Protocol v1

```protobuf
service ApplicationService {
  rpc Initialize(InitializeRequest) returns (InitializeResponse);
  rpc Describe(DescribeRequest) returns (ApplicationDescriptor);
  rpc ConfigureInstance(ConfigureInstanceRequest) returns (ConfigureInstanceResponse);
  rpc ValidateBinding(ValidateBindingRequest) returns (ValidateBindingResponse);
  rpc HandleEvents(stream ApplicationEvent) returns (stream ApplicationEffect);
  rpc HandleRequest(PluginHTTPRequest) returns (PluginHTTPResponse);
  rpc RunJob(RunJobRequest) returns (RunJobResponse);
  rpc Health(HealthRequest) returns (HealthResponse);
  rpc Shutdown(ShutdownRequest) returns (ShutdownResponse);
}
```

`ApplicationEffect` 只能表达 Core 允许的操作，例如创建领域记录、请求命令和计划任务；不能返回任意 SQL 或系统命令。
协议仍识别 `SendNotification`，但 Core 当前没有通知通道，执行该 effect 会 fail-closed 返回 `not_implemented`，
不得写成已实现。

## 7. Schema-driven UI（当前为 Descriptor/Capability 子集）

后续任意页面 Schema 的组件集合由 Core 维护（目标态）：

```text
metric / gauge / status / badge / chart / timeline
entity-list / table / form / command / markdown / json
```

当前已实现 Descriptor/Capability 驱动的设备视图、能力动作与命令表单；插件提供任意页面层级、查询绑定和布局属于后续目标态。

插件不能提供内联脚本、任意 HTML、远程 JS URL、全局 CSS 覆盖或直接读取 cookie/localStorage。需要自定义 UI 时，后续采用独立 Origin 的 sandboxed iframe 和 scoped `postMessage` SDK；不直接采用共享 React runtime 的动态 Module Federation。

## 8. 权限模型

| 权限 | 示例 |
|---|---|
| Hardware | serial、usb、gpio、bluetooth、can |
| Network | outbound host/port、listen local、LAN discovery |
| Filesystem | plugin-data、用户选择目录、只读文件 |
| Secrets | 按名称请求 secret handle，不返回其他 secret |
| Core scopes | read entities、emit observations、execute actions、manage schedules |
| UI scopes | navigation、device view、dashboard template |

权限变化规则：

- patch 升级新增权限：暂停自动升级，要求用户确认。
- 禁止权限扩大而版本不变。
- 禁用插件立即撤销新的 API 调用和资源租约。
- 卸载默认保留数据并允许单独 purge；purge 是独立高风险操作。

## 9. Supervisor

进程状态：

```text
STOPPED / STARTING / HEALTHY / DEGRADED / CRASHED / BACKOFF / DISABLED
```

Supervisor 负责 handshake timeout、RPC Health、stdout/stderr 结构化收集和敏感信息过滤、crash loop 检测、带 jitter 的指数退避、最大重启预算、优雅关闭和 orphan cleanup。Windows 使用 Job Object、Linux 使用 process group；同时记录 CPU、内存、句柄、消息率和重启次数。

## 10. 安装、实例和进程

```text
PluginInstallation（节点上某版本）
  ├─ PluginInstance tenant-a/config-1
  ├─ PluginInstance tenant-a/config-2
  └─ PluginInstance tenant-b/config-3
```

默认一个安装版本启动一个共享进程，服务多个实例；需要更强隔离时可选择 `isolation: per-instance`。同一节点可并存两个版本用于滚动迁移，但同一 Instance 同时只绑定一个版本。

### 实例身份

实例的完整身份是 `(tenant_id, edge_id, instance_id)`，与 store 主键一致：instance id 只在租户内唯一，两个租户各自创建同名实例完全合法。因此任何运行态映射都必须带租户——进程面的实例记录、协议面的运行记录与开窗去重键、Application Runtime 的实例表都是如此。按裸 instance id 建键会让后写入的租户覆盖先写入的：被覆盖的实例静默不运行，而 revision 比较跨租户串味，看起来像“实例自己挂了”。

Server 侧 AppHost 只收敛 `edge_id` 为伪 edge `server` 的期望态行。真实 Edge 的行由该 Edge 自己收敛；Server 再应用一遍就会在本地多跑一份属于 Edge 的实例。这条部署边界与「真实 Edge 不会收到 server 侧实例的期望态」对称，且进程面与协议面共用同一次过滤，不各自判断。

### 实例重配置与会话恢复

Host 收敛既有实例时，以完整定义比较版本、插件、隔离方式及配置；先验证候选会话与配置，再替换运行绑定。配置通过租户限定实例 ID 的 `ConfigureInstance` RPC 下发，不使用共享进程环境变量承载各实例的配置。`ConfigPath` / 保留键 `path` 仍只作本地引用，不传给插件，也不在此处引入文件读取或合并规则。

Supervisor 自动重启后，Manager 已应用的实例配置会在新会话对外可用、状态变为 `HEALTHY` 之前恢复。恢复失败按既有崩溃预算重试，不能让未配置的新进程冒充成功；共享进程只恢复仍启用的实例，并保留显式清空配置的语义。裸 `CreateInstance` / `Start` 仍只负责传输启动；设备绑定与 Application 的结构化配置继续由各自上层协议调用方负责，不由 Manager 猜测或展开。

切换运行绑定不是跨进程、实例文件和 revision 缓存的事务。旧进程退出超时会保留待清理记录，重复请求必须先完成退出确认；实例文件保存失败不回滚已运行的新进程，而是在相同 desired 重试时补齐持久化。失败期间不推进完整 applied revision；详见[控制面故障恢复](control-plane-sync.md#8-故障与恢复)。

### 客户端寻址

map 遍历顺序不是路由规则。插件级查询 `DriverClient(pluginID)` / `ApplicationClient(pluginID)` 只服务尚未持有实例身份的既有调用方：当该插件所有已启用绑定都落在同一个进程上时返回该进程的会话客户端，否则以 `ErrAmbiguousInstance` 失败关闭，不静默挑选一个版本。共享进程内的多个实例仍然无歧义；并存两个版本时必须改用实例级查询。

`DriverClientForInstance(tenant, id)` / `ApplicationClientForInstance(tenant, id)` 解析唯一一条已启用的租户/实例绑定，不向其他版本、进程或租户回落；实例被禁用时返回 `ErrInstanceNotFound`，而不是交给同插件的兄弟实例。两类客户端都绑定当前会话，进程重启后必须重新解析。

上层协议按收敛定义解析进程，不按插件 ID 匹配。AppHost 在把客户端交给 Application Runtime 之前，先核对该实例的进程快照处于启用态，且插件 ID 与版本等于本次期望值；进程面尚未收敛到期望版本时拒绝下发，避免把新版本的 `Initialize` / `ConfigureInstance` 送进陈旧进程。同理，进程面 apply 未成功的实例不会在协议面被晋升为运行实例。

## 11. 数据和升级（目标态）

- Core 数据迁移与插件数据迁移分开。
- 插件升级先调用 `PlanMigration`，展示可回滚性、停机和数据副本大小。
- 更新：下载 → 验证 → side-by-side 启动 → 配置校验 → 状态导入 → 健康门 → 切流 → 保留旧版回滚窗口。
- 自动更新默认只允许“不新增权限、兼容当前协议、非 prerelease”的 patch 版本。
- `plugins.lock` 固定精确版本、资产 digest、来源仓库和验证结果。

## 12. SDK 与一致性测试

核心仓库提供：

```text
sdk/go                  Go helper/client/server
proto/                  语言无关 Protocol v1 文本
spec/                   Manifest/Capability JSON Schema
testing/plugin-harness  conformance runner + mock Core
```

每个 Driver 必须通过 handshake/版本协商、Descriptor 稳定性、配置迁移、重连/重复/乱序/背压、命令幂等/超时/取消、崩溃退出、权限越界以及多实例/多设备测试。Core CI 还要以 mock 插件执行黑盒 wire test，避免同一 SDK 的 client/server 自测掩盖协议错误。
