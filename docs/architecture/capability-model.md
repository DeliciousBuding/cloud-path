# Capability 与领域模型

最后更新：2026-09-03

> 本文定义 CloudPath 的设备无关模型。它是 Driver 与 Application 解耦的核心契约。

## 1. 为什么不能长期依赖 `State.Raw`

自由 JSON 适合原型和取证，但无法稳定支持：跨 Driver 应用复用、通用图表与单位换算、配置/数据迁移、命令参数校验、Capability 依赖匹配和跨设备搜索。因此保留 Raw 作为兼容与诊断面，主路径改为 typed Entity/Capability。

## 2. 对象定义

### Device

一台物理或虚拟设备，由 Driver 发现和维护。

```text
device_id              Core 内稳定 ID
external_id            Driver 范围内不可变 ID
plugin_instance_id     来源 Driver Instance
tenant_id               数据隔离范围
edge_id                 接入节点
manufacturer/model      可选描述
status                  online/offline/unavailable/degraded
```

`device_id` 不使用串口名；COM3 或 `/dev/ttyUSB0` 会变化，只能作为当前连接属性。

### Entity

Device 下可独立观察、控制、命名和授权的逻辑单元。例如一个温湿度传感器可产生温度、湿度和电池三个 Entity。

```text
entity_id
unique_key              Driver 范围内不可由用户修改
name
category                sensor/actuator/diagnostic/config
capabilities[]
```

### Capability

描述 Entity “能做什么”，由稳定 ID 和独立版本标识：

```text
cloudpath.dev/capability/temperature@1
cloudpath.dev/capability/contact@1
cloudpath.dev/capability/alarm@1
cloudpath.dev/capability/display-text@1
cloudpath.dev/capability/clock@1
cloudpath.dev/capability/schedule@1
```

第三方 Capability 使用发布者命名空间，例如 `io.github.example/capability/air-quality-index@1`。Capability 包含 Properties、Events、Actions 和 UI Hints，但 UI Hints 不是语义真相。

## 3. Capability Schema 示例

```yaml
apiVersion: capabilities.cloudpath.dev/v1alpha1
kind: Capability
metadata:
  id: cloudpath.dev/capability/temperature
  version: 1
  title: Temperature
spec:
  properties:
    value:
      type: number
      unit: Cel
      access: read
      quality: [good, uncertain, bad, unavailable]
  events:
    threshold-crossed:
      payloadSchema:
        type: object
        required: [value, threshold, direction]
        properties:
          value: { type: number }
          threshold: { type: number }
          direction: { enum: [rising, falling] }
  actions:
    calibrate:
      inputSchema:
        type: object
        required: [offset]
        properties:
          offset: { type: number, minimum: -20, maximum: 20 }
  presentation:
    primaryProperty: value
    defaultWidget: gauge
```

Schema 使用 JSON Schema 可表达的子集；单位使用统一代码表，不能由插件随意写本地化文本作为机器单位。

## 4. 数据三分法

### Observation

可形成当前状态的观测值：

```json
{
  "entity_id": "entity-123",
  "capability": "cloudpath.dev/capability/temperature@1",
  "property": "value",
  "value": 24.7,
  "observed_at": "2026-09-03T08:00:00Z",
  "received_at": "2026-09-03T08:00:01Z",
  "quality": "good",
  "sequence": 42
}
```

`observed_at` 可来自设备；`received_at` 必须由可信的 Edge/Core 生成。设备时钟不可信时，以 received time 为准并标注质量。

### Event

不可覆盖的时间点事实，例如 `opened`、`alarm-fired`、`device-booted`。Event Type 属于 Capability 或 Application 命名空间，不能继续维护一个全平台写死的业务事件枚举。

### Command

有生命周期、幂等键和超时语义的动作请求：

```text
CREATED → DISPATCHED → ACCEPTED → RUNNING
                              └→ SUCCEEDED / FAILED / TIMED_OUT / CANCELLED
```

至少携带：`command_id`、`idempotency_key`、`entity_id`、`action`、`args`、`deadline`、`actor`。

## 5. Application Binding

Application 不引用 Driver，而声明 Requirement：

```yaml
requirements:
  - id: reminder-output
    capability: cloudpath.dev/capability/alarm@1
    cardinality: one
  - id: compartments
    capability: cloudpath.dev/capability/contact@1
    cardinality: one-or-more
    minItems: 3
  - id: local-display
    capability: cloudpath.dev/capability/display-text@1
    cardinality: zero-or-one
```

安装 Application Instance 时，CloudPath 提供绑定向导：

```text
Requirement             Candidate Entity
reminder-output    →    stcb-001/alarm
compartments[0]    →    stcb-001/compartment-1
compartments[1]    →    stcb-001/compartment-2
compartments[2]    →    stcb-001/compartment-3
local-display      →    stcb-001/display
```

绑定保存稳定 `entity_id`；端口、Edge 重连和 Driver 重启不应改变绑定。

## 6. Reference Application

公开仓库中的参考业务名使用 **Scheduled Compartment**，仓库建议：

```text
cloud-path-app-scheduled-compartment
```

它表达“按计划管理若干分格并记录开合/完成事件”，药品、零件、样品、耗材都可以成为部署配置，避免 Core 和公开协议绑定单一行业。

它依赖 Capability，而不是：

```yaml
required_driver: stcb   # 禁止
```

## 7. Driver 映射职责

Driver 必须把厂商字段转换为稳定模型：

```text
厂商串口 S:...
  → Device stcb-001
  → Entity clock / alarm / compartment-1..N
  → Observation / Event
```

设备专属容错、乱码抢救、慢速命令、RTC 缺陷都留在 Driver 内，不泄漏到 Capability 或 Application。

## 8. 兼容与迁移

当前 `State.Raw` 的迁移分四步：

1. Adapter 同时发布 Raw 与 Descriptor。
2. Core 从 Descriptor 创建 Entity/Capability 注册表。
3. Edge 同时发送旧 State 与新 Observation/Event；前端优先新模型。
4. 外部 Driver 全量使用新协议；Raw 只保留在诊断字段。

Capability v1 一旦发布，不原地改变字段语义；破坏性变化发布 `@2`，Application 可声明兼容范围或提供迁移器。

## 9. 不变量

- 用户可改显示名，不可改 Driver 提供的 `unique_key`。
- 相同物理设备重连后恢复原 Device/Entity ID。
- Application 数据不能写入 Driver 私有状态。
- Driver 不能直接操作 Application 存储。
- UI label 可本地化；机器 ID、Capability ID、Event Type 不本地化。
- 未知 Capability 仍可保存和转发，只是 Core UI 回落为通用 JSON/表格视图。

## 10. 展示层（WebUI 呈现契约）

WebUI 的默认视图是「人先看值，机器细节按需」：展示名 / 当前值 / 单位 / 状态 / 新鲜度进默认视图；
entity_id、capability URI、property key、raw JSON 只进能力 Inspector 与诊断页。

文本三层归属：

| 层 | 例子 | 归属 |
|---|---|---|
| 平台 UI 词 | 概览 / 保存 / 事件 / 载荷 | WebUI 自身（薄词典，暂不引 i18n 框架） |
| 声明展示元数据 | 温度 / 蜂鸣器 / 按下 | Descriptor / Capability declaration 的 title/description |
| 机器身份 | `temperature`、`cloudpath.dev/capability/...` | 永远 canonical，不翻译 |

展示名解析优先级（`webui/src/lib/descriptor.ts`）：
声明 localized title → 平台通用词汇（PROPERTY_LABEL / GENERIC_NOUN / CMD_LABEL / EVENT_VERB 等）
→ humanize → canonical machine name。
locale 匹配规则：声明 title 含 CJK 时优先采用；纯英文 title 让位平台通用词汇，
避免中文界面漏出英文机器术语（driver 声明改中文才是根修，见 Dev Agent 移交清单）。

resolver 均为纯函数并有确定性单测（fallback 顺序、脏数据降级）；UI 不写设备特例。
