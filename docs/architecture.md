# CloudPath 架构总览

最后更新：2026-09-03

> 状态：**目标架构已定，尚未全部实现**。本文是架构入口；当前 P1 实现事实仍以
> [design.md](design.md) 为准。插件契约落地期间必须同时标注“已实现”和“目标态”，不得把规划写成现状。

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
- Connector Plugin 把 CloudPath 与外部平台、通知系统或数据后端连接。
- WebUI 默认按插件声明的 Schema 渲染，不写死设备字段和业务页面。

第一个板卡和“定时分格提醒”只作为 reference driver / reference application；两者必须可以独立替换。

## 3. 第一性原则

1. **核心零设备认知**：移除全部设备插件后，Core 仍可编译、启动和管理插件。
2. **业务零硬件依赖**：Application Plugin 依赖 Capability，不依赖某个 Driver ID。
3. **契约多型而非万能接口**：Driver、Application、Connector 使用不同的最小协议。
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
               │ versioned gRPC            │ versioned gRPC
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
| Connector | Edge 或 Server | MQTT/Webhook/外部平台/通知/数据出口 | 定义核心设备模型 |
| Transform（后期） | Server/Edge 沙箱 | 无状态映射、过滤、聚合、规则函数 | 长连接和任意系统访问 |

UI 贡献不是独立可执行插件类型。v1 由上述插件通过 Manifest 提交声明式导航、表单、页面和组件 Schema；自定义 JavaScript 延后到具备独立 Origin、沙箱与细粒度 API token 以后。

## 6. 核心领域模型

```text
PluginDefinition        可安装的软件与契约
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

## 8. GitHub 生态

CloudPath 使用双通道发现：

1. **开放通道**：GitHub 仓库 Topic `cloudpath-plugin`。
2. **精选通道**：官方维护的 Registry，记录经过审查的插件与发布者策略。

Topic 只是候选集合，不是信任证明。CLI 搜到仓库后还必须检查根 Manifest、兼容范围、GitHub Release、资产摘要和可选构建证明。详细流程（GitHub 生态发现与验证）待规划成文。

## 9. 插件运行时

- Driver 和有 Backend 的 Application/Connector 使用独立子进程。
- 子进程通过 stdout 完成一次握手，再通过本地 versioned gRPC 通信。
- Transport 不写死：Windows 可用 loopback TCP 或 named pipe；Linux/macOS 优先 Unix socket；测试使用内存传输。
- 一个插件进程可托管多个 Plugin Instance 和多台设备，而不是一设备一进程。
- Host 负责健康检查、日志、崩溃检测、指数退避、资源统计和优雅退出。

详细契约见 [architecture/plugin-system.md](architecture/plugin-system.md)。

## 10. 当前实现与目标态

| 能力 | 当前 P1 | 目标 |
|---|---|---|
| 设备扩展 | Go `init()` + `device.Adapter` | 外部 Driver Plugin + versioned gRPC |
| 状态 | `State.Raw map[string]any` | Entity/Capability + typed Observation；Raw 仅兼容 |
| UI | 部分字段与命令外观硬编码 | Descriptor/Schema 全驱动 |
| STC-B | `examples/stcb` 编译进 Edge | 独立 `cloud-path-driver-stcb` 仓库 |
| 业务应用 | 无正式 Application Runtime | Capability Binding + App Plugin |
| 插件发现 | 无 | GitHub Topic + Registry |
| 安装可信度 | 无 | digest、来源验证、attestation、lockfile |
| 多租户 | 仅设计 | tenant-scoped plugin/device/data |

## 11. 决策记录与实施

ADR 与完整实施计划尚在规划中，成文后补入 `docs/architecture/adr/` 与 `docs/architecture/plan/`。

## 12. 非目标

- v1 不支持任意第三方 React bundle 注入主页面。
- v1 不承诺不受信任插件的强 OS 沙箱；初期定位为“经用户授权的本地代码”，但仍执行进程隔离与权限披露。
- v1 不做中心化付费插件商店。
- v1 不把 MQTT、Modbus 或某个厂商协议提升为核心模型。
- 不为了未来可能性提前实现全部 Connector/Transform 类型；先锁定契约，再用第二个真实插件验证抽象。
