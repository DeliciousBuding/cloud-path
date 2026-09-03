# Plugin System 目标设计

最后更新：2026-09-03

## 1. 统一平台，多个契约

`cloudpath.plugin.yaml` 是统一入口，但插件根据贡献类型实现不同协议：

| Contribution | Protocol | Host |
|---|---|---|
| `drivers` | Driver Protocol | Edge Plugin Host |
| `applications` | Application Protocol 或 declarative-only | Server Plugin Host |
| `connectors` | Connector Protocol | Edge/Server，由 Manifest 指定 |
| `views` | 声明式 View Schema | WebUI Schema Renderer |
| `transforms`（后期） | WASM Component/WIT | 受限 Runtime |

禁止把所有插件塞进一个 `DoEverything` RPC。

## 2. Manifest v1alpha1

```yaml
apiVersion: plugins.cloudpath.dev/v1alpha1
kind: CloudPathPlugin
metadata:
  id: io.github.deliciousbuding.cloud-path-driver-stcb
  name: STC-B Driver
  version: 0.1.0
  description: Driver for the STC-B reference board
  license: MIT
  repository: https://github.com/DeliciousBuding/cloud-path-driver-stcb
compatibility:
  core: ">=0.2.0 <0.4.0"
  protocols:
    driver: [1]
runtime:
  type: process
  entrypoints:
    windows-amd64: cloudpath-driver-stcb.exe
    linux-amd64: cloudpath-driver-stcb
    linux-arm64: cloudpath-driver-stcb
  handshakeTimeout: 5s
  shutdownTimeout: 5s
permissions:
  serial: { required: true }
  network: { outbound: [] }
  filesystem:
    read: []
    write: [plugin-data]
  secrets: []
contributes:
  drivers:
    - id: stcb
      configSchema: schemas/driver-config.json
      discovery: manual
      capabilityCatalog: schemas/capabilities.yaml
  views:
    - id: device-overview
      target: device
      schema: views/device-overview.yaml
```

### 不可混用的版本

- `metadata.version`：发布包 SemVer
- `apiVersion`：Manifest 结构版本
- `compatibility.core`：Core 产品兼容范围
- `compatibility.protocols.*`：RPC 协议版本集合
- Capability 尾部版本：数据语义版本
- 配置 Schema 自己的 `schemaVersion`

## 3. 运行时边界

### Driver Plugin

- 一进程可管理多个 Driver Instance 和多台设备。
- 只从 Host 获得批准后的端口/资源，不扫描未授权范围。
- 设备发现、连接、解析和命令执行在插件内。
- 所有上报经 Host 校验尺寸、类型、时间和速率后进入 Core。

### Application Plugin

Application 可为：

1. `declarative-only`：绑定、自动化、数据模型和页面均由 Schema 描述；优先使用。
2. `process`：复杂业务逻辑在 Server 子进程中运行，通过受限 App Protocol 调用 Core。

Application Backend 不直接打开 Core SQLite，不获得全局管理员令牌；API 只能挂在
`/api/plugins/{plugin_id}/instances/{instance_id}/...`。请求上下文由 Core 注入 tenant、actor、instance 和 scope。数据写入插件 namespaced store 或插件专属数据目录，纳入备份清单。

### Connector Plugin

Connector 明确声明方向和宿主：

```yaml
connectors:
  - id: mqtt-export
    direction: outbound
    host: server
  - id: local-modbus-gateway
    direction: inbound
    host: edge
```

Transport 生命周期与 Mapping Schema 分离；第一版可由同一插件同时提供，接口上不耦死。

## 4. 进程启动与握手

不把 named pipe 写死为协议的一部分。推荐流程：

```text
Host 生成 launch_id + 随机一次性 cookie
  → 启动子进程并通过 env/handle 传入
  → Plugin 在 stdout 输出唯一 handshake 行
  → Host 验证 cookie、插件 ID、协议版本和地址
  → 建立本地 gRPC
  → 调用 Initialize / Describe / Health
```

握手示意：

```text
CP1|driver=1|tcp|127.0.0.1:49172|grpc|<launch-id>|<proof>
```

- Windows 初期默认 loopback TCP 随机端口，后续可增加 named pipe transport。
- Linux/macOS 优先 Unix socket。
- 测试使用 `bufconn` 或同等内存传输。
- Cookie 是误启动防护，不等同安全沙箱；传输仍需限制在本机并绑定 launch identity。

## 5. Driver Protocol v1 草案

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

## 6. Application Protocol v1 草案

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

`ApplicationEffect` 只能表达 Core 允许的操作，例如创建领域记录、请求命令、计划任务和发送通知；不能返回任意 SQL 或系统命令。

## 7. Schema-driven UI

v1 允许的组件集合由 Core 维护：

```text
metric / gauge / status / badge / chart / timeline
entity-list / table / form / command / markdown / json
```

插件提供页面层级和布局、数据查询绑定、字段/单位/格式提示、命令表单 JSON Schema、条件可见性、空状态和本地化资源。

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

Supervisor 负责 handshake timeout、gRPC health、stdout/stderr 结构化收集和敏感信息过滤、crash loop 检测、带 jitter 的指数退避、最大重启预算、优雅关闭和 orphan cleanup。Windows 使用 Job Object、Linux 使用 process group；同时记录 CPU、内存、句柄、消息率和重启次数。

## 10. 安装、实例和进程

```text
PluginInstallation（节点上某版本）
  ├─ PluginInstance tenant-a/config-1
  ├─ PluginInstance tenant-a/config-2
  └─ PluginInstance tenant-b/config-3
```

默认一个安装版本启动一个共享进程，服务多个实例；需要更强隔离时可选择 `isolation: per-instance`。同一节点可并存两个版本用于滚动迁移，但同一 Instance 同时只绑定一个版本。

## 11. 数据和升级

- Core 数据迁移与插件数据迁移分开。
- 插件升级先调用 `PlanMigration`，展示可回滚性、停机和数据副本大小。
- 更新：下载 → 验证 → side-by-side 启动 → 配置校验 → 状态导入 → 健康门 → 切流 → 保留旧版回滚窗口。
- 自动更新默认只允许“不新增权限、兼容当前协议、非 prerelease”的 patch 版本。
- `plugins.lock` 固定精确版本、资产 digest、来源仓库和验证结果。

## 12. SDK 与一致性测试

核心仓库提供：

```text
sdk/go                  Go helper/client/server
proto/                  语言无关 protobuf
spec/                   Manifest/Capability JSON Schema
testing/plugin-harness  conformance runner + mock Core
```

每个 Driver 必须通过 handshake/版本协商、Descriptor 稳定性、配置迁移、重连/重复/乱序/背压、命令幂等/超时/取消、崩溃退出、权限越界以及多实例/多设备测试。Core CI 还要以 mock 插件执行黑盒 wire test，避免同一 SDK 的 client/server 自测掩盖契约错误。
