# ADR-0001：能力中心的多契约插件模型

状态：已接受
日期：2026-09-03

## 背景

最初把 CloudPath 设计成「装很多硬件 Adapter 的上位机」，药盒业务与 STC-B 板强耦合。
这会导致：新增业务要改核心、换硬件要改业务、前端页面按设备字段写死。

## 决定

采用 **Device / Entity / Capability** 为核心，插件按契约分三型：

- **Driver Plugin**（跑在 Edge）：设备发现、连接、协议解析、能力映射、设备动作。
- **Application Plugin**（跑在 Server）：业务对象、绑定、规则、任务、仪表盘、领域 API。
- **Connector Plugin**（Edge 或 Server）：MQTT / Webhook / 外部平台 / 通知 / 数据出口。

核心不变量：

- Core 不认识任何具体设备/行业；业务不依赖 Driver ID，只声明 Capability Requirement。
- 当前 `State.Raw map[string]any` 保留为兼容/诊断面，主路径迁移到 typed Entity/Capability。
- UI 默认由插件声明的 Schema 渲染，不写死设备字段。

## 备选方案

- 全部设备都是 Driver Plugin：无法表达「业务应用与设备解耦」。
- 单一万能插件接口：契约过度泛化，难以演进与做 conformance test。

## 后果

- 正面：硬件、业务、连接三方独立演进，第三方可扩展。
- 代价：Capability 注册表、Schema 渲染、绑定向导、版本协商都是必须投入的基础设施。

详见 [../capability-model.md](../capability-model.md) 与 [../plugin-system.md](../plugin-system.md)。