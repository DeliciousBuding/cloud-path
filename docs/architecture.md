# CloudPath 架构总览

最后更新：2026-09-09

> 状态：**当前实现与目标态分列**。外部 Driver Host、Registry、Application Runtime 与多租户隔离
> 已实现；Connector/Transform 运行时、集中 Secret Store、分布式配额和跨 Server 部署仍是目标态。
> 当前接口以 [design.md](design.md)、`spec/` 和代码为准；目标态见 §11。
>
> 插件控制面期望态/实际态同步见 [control-plane-sync.md](architecture/control-plane-sync.md)；租户保留期、配额与插件秘密边界见 [tenant-security-policy.md](architecture/tenant-security-policy.md)。

## 1. 产品与仓库命名

| 对象 | 规范名称 |
|---|---|
| 产品/品牌 | **CloudPath** |
| 核心公开仓库 | `cloud-path` |
| Go module（迁移后） | `github.com/DeliciousBuding/cloud-path` |
| CLI / 二进制前缀 | `cloudpath`、`cloudpath-server`、`cloudpath-edge` |
| 规范命名空间 | `cloudpath.dev/*` |
| 插件仓库前缀 | `cloud-path-driver-*` / `cloud-path-app-*` / `cloud-path-connector-*` |
| 插件发现 Topic | `cloudpath-plugin` |

仓库 slug 使用 `cloud-path`，面向用户的产品名统一写 `CloudPath`；机器标识和二进制不插入连字符，避免命令、包名和协议字段不必要地变化。

## 2. 定位

CloudPath 是一个以 **Device / Entity / Capability** 为核心的通用 IoT 平台，而不是某块开发板的上位机。

- Driver Plugin 把厂商硬件和协议映射为标准能力。
- Application Plugin 把能力组合成具体业务。
- Connector Plugin 把 CloudPath 与外部平台、通知系统或数据后端连接（运行时仍属目标态）。
- WebUI 默认按 Descriptor / Capability 声明渲染设备字段与操作，不写死具体设备；业务页面 Schema 仍属目标态。

第一个板卡和“定时分格提醒”只作为 reference driver / reference application；两者必须可以独立替换。

## 3. 设计原则

1. **核心零设备认知**：移除全部设备插件后，Core 仍可编译、启动和管理插件。
2. **业务零硬件依赖**：Application Plugin 依赖 Capability，不依赖某个 Driver ID。
3. **接口按类型拆分**：Driver、Application、Connector 使用各自的最小协议，不做一个万能接口。
4. **描述优先**：配置、状态、命令和页面优先使用声明式 Schema；任意代码扩展是后备能力。
5. **进程隔离**：可执行第三方插件在独立进程运行，不能直接链接进 Core 地址空间。
6. **最小权限**：插件声明串口、网络、文件、秘密和系统能力；安装时展示并确认权限差异。
7. **开放发现、验证安装**：Topic 可被任何仓库声明，因此只用于发现；执行前必须验证 Manifest、Release、摘要与来源。
8. **版本分离**：插件版本、Manifest 版本、RPC 协议版本和 Capability 版本分别演进。
9. **渐进兼容**：当前内置 Adapter 作为迁移桥，不成为永久公开插件 API。
10. **可测试替换**：所有 Capability 和插件协议均提供 mock 与 conformance test。

## 4. 系统分层

```text
┌─────────────────────────────────────────────────────────┐
│ CloudPath Experience Plane                              │
│ Core WebUI · Schema Renderer · Dashboard · Plugin UX    │
└──────────────────────────┬──────────────────────────────┘
                           │ REST / WebSocket
┌──────────────────────────▼──────────────────────────────┐
│ CloudPath Control Plane                                 │
│ Tenant · Auth · Registry · Application Host · Commands  │
│ Automation · Audit · Plugin Catalog                     │
└──────────────────────────┬──────────────────────────────┘
                           │ State / Event / Command Bus
┌──────────────────────────▼──────────────────────────────┐
│ CloudPath Data Plane                                    │
│ Current State · Event Log · Telemetry · Plugin Storage  │
└──────────────────────────┬──────────────────────────────┘
                           │ Edge WebSocket
┌──────────────────────────▼──────────────────────────────┐
│ CloudPath Edge Plane                                    │
│ Driver Host · Discovery · Offline Buffer · Local Policy │
└──────────────┬───────────────────────────┬──────────────┘
               │ versioned RPC             │ versioned RPC
      ┌────────▼────────┐          ┌────────▼────────┐
      │ Driver Plugin A │          │ Driver Plugin B │
      └─────────────────┘          └─────────────────┘
```

### Core 拥有

- 租户、用户、角色和审计边界
- Plugin Definition / Installation / Instance 生命周期
- Device / Entity / Capability 注册表
- Observation / Event / Command 标准模型
- 当前状态、历史事件、命令状态和插件数据备份入口
- Edge 会话、离线状态、命令路由和幂等语义
- Schema Renderer 和权限确认 UI

### Core 不拥有

- 厂商帧格式、串口特殊时序和板级容错
- 行业专用字段、业务流程和业务页面
- 第三方系统的私有 API 细节
- 插件自己的内部算法和可替换实现

## 5. 插件类型

| 类型 | 默认宿主 | 负责 | 不负责 |
|---|---|---|---|
| Driver | Edge | 设备发现、连接、协议解析、能力映射、设备动作 | 业务流程、租户 UI |
| Application | Server | 业务对象、绑定、规则、任务、仪表盘、领域 API | 直接访问串口或 Core DB |
| Connector（运行时目标态） | Edge 或 Server | MQTT/Webhook/外部平台/通知/数据出口 | 定义核心设备模型 |
| Transform（后期） | Server/Edge 沙箱 | 无状态映射、过滤、聚合、规则函数 | 长连接和任意系统访问 |

UI 贡献不是独立可执行插件类型。当前 WebUI 由 Descriptor/Capability schema 驱动通用设备视图、能力动作与命令表单；任意第三方页面或 JavaScript 延后到具备独立 Origin、沙箱与细粒度 API token 以后。

## 6. 核心领域模型

```text
PluginDefinition        可安装的软件与声明
  └─ PluginInstallation 某节点上已验证的版本
       └─ PluginInstance 某租户/节点的一份配置与运行实例

DriverInstance
  └─ Device
       └─ Entity
            └─ Capability
                 ├─ Property / Observation
                 ├─ Event
                 └─ Action / Command

ApplicationInstance
  └─ Binding：把应用所需 Capability 绑定到实际 Entity
```

详细语义见 [architecture/capability-model.md](architecture/capability-model.md)。

## 7. 插件生命周期

```text
DISCOVERED → INSPECTED → DOWNLOADED → VERIFIED → INSTALLED
                                              ↓
                                      CONFIGURED → STARTING
                                              ↓
                              HEALTHY ↔ DEGRADED ↔ RESTARTING
                                              ↓
                                    DISABLED → UNINSTALLED
```

安装软件、创建实例、启用实例是三件独立操作：下载插件不会自动执行；安装后也不会未经配置访问硬件或网络。

## 8. GitHub 发现

CloudPath 使用双通道发现：

1. **开放通道**：GitHub 仓库 Topic `cloudpath-plugin`。
2. **精选通道**：官方维护的 Registry，记录经过审查的插件与发布者策略。

Topic 只是候选集合，不是信任证明。CLI 搜到仓库后还必须检查根 Manifest、兼容范围、GitHub Release、资产摘要和可选构建证明。详细流程见 [architecture/github-ecosystem.md](architecture/github-ecosystem.md)。
仓库组合、插件命名、拆仓条件与公开边界见 [architecture/repository-strategy.md](architecture/repository-strategy.md)。

## 9. 插件运行时

- Driver 和有 Backend 的 Application 使用独立子进程；Connector 运行时仍是目标态。
- 子进程通过 stdout 完成一次握手，再通过本地 versioned RPC 通信（当前为长度前缀 JSON 帧，transport 可替换）。
- Transport 不写死：当前 Windows 使用 loopback TCP，Linux/macOS 使用 Unix socket，测试使用内存传输；named pipe 等可后续扩展。
- 一个插件进程可托管多个 Plugin Instance 和多台设备，而不是一设备一进程。
- Host 负责健康检查、日志、崩溃检测、指数退避、资源统计和优雅退出。

详细说明见 [architecture/plugin-system.md](architecture/plugin-system.md)。

## 10. 当前实现

| 能力 | 当前状态 | 边界 |
|---|---|---|
| 设备扩展 | 外部 Driver Plugin + Driver Protocol v1，由 Edge Plugin Host 运行；内置 `demo` 仅作无硬件参考 | 同一外部 Driver 的多实例多设备映射与串口注入已实现；命令/事件按 `(device_id, entity_id)` 匹配；维护者已完成受控三块 STC-B 验证，其他硬件仍需按设备 ACK 和事件复核 |
| 状态模型 | Descriptor / Entity / Capability + typed Observation；`State.Raw` 仅保留兼容与诊断 | 旧 raw 读面继续可用，不伪装成 typed 语义 |
| UI | Descriptor / Capability 驱动设备视图、能力动作与命令表单 | 任意第三方 React bundle 注入仍是非目标 |
| STC-B | 已拆为独立 Driver Plugin [`cloud-path-driver-stcb`](https://github.com/DeliciousBuding/cloud-path-driver-stcb)，Core 生产二进制不再内置 STC-B | 发布版本以插件仓库 tag 为准 |
| 业务应用 | Server AppHost + `internal/appruntime` + Application Protocol v1，支持 Capability 绑定、领域记录、任务与手动操作 | 参考应用已拆为独立仓库；旧 bootstrap 仅作历史参考 |
| Connector / 通知 | Connector Manifest 贡献可安装与披露；运行时未实现，`SendNotification` effect fail-closed | 没有 Connector 进程宿主或通知投递通道；不得按现状使用 |
| 插件发现与安装 | GitHub Topic 开放发现 + Registry CLI（search/inspect/install/enable/disable/update/remove/host），校验 Manifest、digest、兼容范围并写 `plugins.lock` | Registry 是信任增强通道，不替代摘要与权限校验 |
| 多租户 | 账号/RBAC、tenant token、审计、设备和插件实例按 `tenant_id` 隔离；浏览器 WS 快照与 fan-out 按租户过滤 | 单 Server 部署；分布式全局配额与跨 Server 调度未实现 |

## 11. 目标态与后续工作

以下能力不是当前实现，不能按现状使用：

- **Connector / Transform Runtime**：Connector 已有 Manifest 声明，运行时待实现；Transform/WASM 仍在设计阶段。
- **强隔离与集中秘密**：当前是受用户授权的本地进程插件与 Edge 本地 secret provider；中心 KMS/Vault、远程 secret 分发、自动轮换尚未实现。
- **横向扩展**：多 Server 全局配额、分布式 limiter、跨节点调度与一致性尚未实现。
- **协议与数据扩展**：MQTT/Modbus 等接入网关、远程 OTA 编排、时序聚合和业务分析不在当前基线。
- **第三方 UI 扩展**：任意 React bundle 不进入主页面；插件通过声明式 UI contribution 注册业务导航与页面，复杂页面使用独立 Origin、sandboxed iframe 与细粒度 API token。契约见 [plugin-ui.md](architecture/plugin-ui.md)。

## 12. 决策记录

- [ADR-0001：能力中心的多类型插件模型](architecture/adr/0001-capability-centered-plugins.md)
- [ADR-0002：GitHub Topic + Registry 混合发现](architecture/adr/0002-github-plugin-discovery.md)


## 13. 非目标

- v1 不支持任意第三方 React bundle 注入主页面。
- v1 不承诺不受信任插件的强 OS 沙箱；初期定位为“经用户授权的本地代码”，但仍执行进程隔离与权限披露。
- v1 不做中心化付费插件商店。
- v1 不把 MQTT、Modbus 或某个厂商协议提升为核心模型。
- 不为了未来可能性提前实现全部 Connector/Transform 类型；先锁定接口，再用第二个真实插件验证抽象。
