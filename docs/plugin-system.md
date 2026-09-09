# 插件系统（Plugin System）

CloudPath 是**设备无关**的插件化平台：核心（`internal/*`）不识别任何具体硬件，
一切设备语义都由**插件**承载。本页是插件开发的入口文档。

## 1. 三类插件

| 类型 | 运行位置 | 职责 |
|------|----------|------|
| **Driver** | Edge | 设备发现、连接、协议解析、能力映射、设备动作；“如何接一台设备”的插件 |
| **Application** | Server | 业务对象、Capability 绑定、规则、任务、领域 API |
| **Connector** | Edge 或 Server | MQTT/Webhook/外部平台/通知/数据出口 |

## 2. Driver 插件（核心）

想让任何设备接入 CloudPath，只需写一个 Driver：

- 用公开 Go SDK：`sdk/go/driverkit`（驱动器骨架）、`sdk/go/model`（语义模型）、
  `sdk/go/rpc` / `sdk/go/transport`（进程通信）、`sdk/go/pluginmain`（入口）。
- 实现三件事：设备**发现**、**连接与协议解析**（串口/网络）、把设备状态/事件映射为平台**模型**，
  并声明**命令白名单**。
- 参考实现：`cloud-path-driver-stcb`（STC-B 学习板，独立仓库）——"如何写一个 Driver Plugin"的官方范例，含黄金样本单测。

## 3. 契约（Manifest）

插件通过仓库根 `plugin.yaml`（Manifest）声明自身。当前 schema 的顶层字段是
`apiVersion`、`kind`、`id`、`version`、`protocol`、`entrypoint`，并可声明
`compatibility`、`permissions`、`capabilities`、`requirements`、`contributes`；完整字段以
[`spec/plugin-manifest.schema.json`](../spec/plugin-manifest.schema.json) 为准。

- `kind ∈ Driver|Application|Connector`；不再使用 `CloudPathPlugin` 或 `metadata` 包装。
- 能力与命令白名单来自 `Describe` / 适配器声明，Manifest 只声明安装、权限、兼容和贡献身份。
- 本地 secret 只写名称，运行期明文解析，仓库绝不落 secret。

命令合法性以**适配器 `SupportedCommands()`** 为唯一事实源；server 与前端不另建清单。

## 4. 生命周期

发现 → 安装（Manifest 校验）→ 运行（Edge/Server 加载）→ 调度/重启 → 卸载。
插件可拆为**独立仓库**（进程内参考实现 → 独立 go.mod + 进程入口），契约在拆仓前冻结。

## 5. 拆仓为独立插件仓库

STC-B Driver 已拆为独立仓库 `cloud-path-driver-stcb`（进程内参考形态已移除），只依赖公开 SDK，不 import `internal/**`。更细的插件架构：
- `docs/architecture/how-to-build-driver.md`（新增 Driver 的完整操作入口）
- `docs/architecture/adr/0001-capability-centered-plugins.md`
- `docs/architecture/adr/0002-github-plugin-discovery.md`

## 参考

- `cloud-path-driver-stcb`：官方 STC-B Driver + 契约（`plugin.yaml`）
- `docs/protocol.md`：消息信封 / DTO 契约
- `docs/design.md`：技术 SSOT
