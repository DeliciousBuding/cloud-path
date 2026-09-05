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

新鲜度呈现：页头与列表行的「更新于 / 连接于」用相对时间（绝对时间进 title 悬停查看），
事实表与诊断页保留绝对时间。

长历史按天分组（EventFeed `dayGrouped` 与活动页命令历史同一视觉语言）：组头承载日期
（今天 / 昨天 / 月日），行内只显示时刻，完整时间进 title；避免「组头 + 每行完整日期」双重冗余。

跨设备列表的呈现约定：设备列表 2xl「关键读数」列至多两条声明主观测（与概览 KPI 同一
`metricTiles` 推导），能力全量事实面只在详情「能力」tab；活动页/概览的命令展示名经
`cmdMeta(cmd, actions?, idx?)` 解析——无单设备命令集时用 `commandDecl`（eventDecl 的命令侧
对称件）查 catalog 声明，再回落平台词典 / humanize。

状态三态契约：详情类页面在数据未到手时必须区分「加载中（骨架）/ 404（未注册空态）/
其它失败（可重试错误态）」。`isNotFound()`（webui/src/lib/api.ts）是 404 语义的唯一判定；
把 404 渲染成「加载失败」会让用户去查 server，而真相只是设备没接入。

值类型感知呈现（`SummaryValue.kind`，由 widget 推导）：布尔主值（开关/触发与否）渲染为状态胶囊
而非大字号——半屏大的「否」是排版错误；胶囊语义色只接 warn/bad，其余保持中性。单位经展示层单一
出口 `unitLabel()` 人话化（s→秒、Cel→°C），科学单位（V/lux/%）原样；诊断页 raw JSON 不受影响。

计数/频次图必须零基线（TrendChart `zeroBase`）：负轴或悬空基线会夸大差异，属数据可视化谎言；
窄高 sparkline 模式（`hideY`）连同横向网格省去，峰值由标题说人话；X 轴刻度粒度由调用方传入
（分桶图传时分，秒对 ≥1 分钟的桶是噪音）。

设备概览首屏三段：KPI 行 → [最近活动 2/3 | 设备状况 1/3] → 命令历史通栏置底（限 8 条 + 控制页
出口）。命令历史放右栏会拉到 20 行、把左栏踢出一大片空洞。

插件实例状态词汇覆盖两套后端事实源：edge pluginhost.State（大写 STOPPED/HEALTHY…）与 server
AppHost 的 appruntime.InstanceState（小写 running/stopping/failed…，internal/appruntime/types.go）；
observed.detail 的已知机器标记（server-apphost）经 `hostDetailLabel()` 人话化；未知值原样呈现，不猜。

视觉纪律（Vercel design.md 反模式审查）：无限循环动效（脉冲光环/闪动）与玻璃拟态一律移除（default to stillness / 拒 glass）；
普通元数据（适配器/串口/节点 ID）不用胶囊，降级为 mono 文本，只有状态保留胶囊；数值/时间列右对齐且表头同对齐；
时间戳与机器标识用 Geist Mono（只标识本身进 mono，整句不进）；散文不用破折号。胶囊只承载状态/语义：分类、计数、权限 scope、命令集来源等普通元数据用纯文本（高权限 scope 用语义色文字而非胶囊）。

命令参数校验契约（Vercel field 纪律）：保留用户原文，不静默剥离/截断；超长/控制字符在框下显式报错并禁用下发
（argsError）；单行 input 的换行净化由 HTML spec 负责，前端不重复做。label 与长度上限说明在框外。参数逐字下发（前端不做静默二次截断，后端门禁兜底拒收）；ack 失败缺 detail 用人话兜底，不露机器状态串。

实时状态分组视图 = 紧凑瓦片矩阵（StateTile：2/3/4 列随宽度）：单标量不拉通栏行（标签↔值
扫视距离是可读性成本）；布尔走胶囊、机器串（无 CJK/足够长/id 字符集）在默认视图以 mono 降级呈现、
完整值进 title，表格视图不降级；次要观测折进 details。

中文字体纪律：中文走自托管 Noto Sans SC 可变子集（OFL；GB2312 一级字表 ∪ 界面词汇 ∪ 中文标点，
unicode-range 只接管 CJK，拉丁/数字仍走 Geist；可复现构建见 webui/scripts/build-cjk-subset.py）。
负字距是拉丁刻度：全仓止于 CJK 安全值 -0.01em（`tracking-tight` 禁用，design-system 守卫）；mono 等宽面恒零字距（负字距破坏等宽网格）；中文正文保持 0。

### 10.9 排版刻度与字重（2026-09-05 增补）

- 字重阶梯三档：regular 400（降级/单位）/ medium 500（正文与标签）/ semibold 600（标题与值），标题不超 600（Vercel 纪律）。浅色正文基底 450（CJK 光学平价，可变轴真实实例）；**暗色整条阶梯等差上移 50**（450→500 / 500→550 / 600→650）——纯黑 + 灰度抗锯齿削约半档笔画，是 Noto Sans SC 暗底发飘的根因；补偿只平移不改变相对差与层级。
- 字号刻度 {10,11,12,13,14,15,22,24,26,28,30}：30=指标 display（KPI/观测大数字，`.metric`）；28=页标题；26=认证页标题；24=详情 hero 与槽内短值；22=异常页标题；15=面板标题；14=正文/导航；13=密排正文；12=元数据；11=mono 机器文本下限；10 灭绝。
- `.metric` 是拉丁负字距唯一出口：等宽数字 + `-0.02em`（Geist 数字刻度）；CJK 文本负字距止于 -0.01em（全角字面会挤），tracking-tight/tighter 灭绝。
- 中文标点 `palt` 比例宽度全局启用（body），收紧全角逗号/句号而不碰汉字字面；数字恒 `tnum`。标题 `text-wrap: balance`、散文 `pretty`。
- 字号下限：非 mono 文字 ≥12px；mono 微文本（标识/raw JSON/版本号/SVG 轴刻度）11px；10px 全仓灭绝。阅读字号（键值行/按钮/侧栏导航）14px。均由 design-system 守卫执行。
- 字符串型 KPI（版本号等）用 mono medium 渲染，不用 sans semibold：字符串不是量级。
- 散文行宽 ≤62ch：全宽长行是布局失败（About 等通栏段落加 max-w）。

### 10.10 形状与结构

- 圆角刻度只有两档：tile 8px（卡内子面/内联 note/控件）与 card 12px（.card/浮层 Toast/品牌 logo）；rounded-2xl+ 灭绝（守卫）。
- 列表在 Panel 内用 divider rows（`divide-y`），不套子卡（嵌套卡片是硬反模式）；Admin 用户/令牌行已行化。
- 表格体单元格 `vertical-align: baseline`（对齐行首基线；多行表头才底对齐）。
- 长 ledger（活动页事件流/命令历史）本地滚动（max-h + overflow-y-auto），页面保持一屏可读；天分组头 sticky 于滚动容器顶，跨天查找不迷路。
- 空态/错误态不用装饰性图标瓷砖（彩色圆底）：plain 语义色图标即可。

### 10.11 页面组合

- 概览是摘要不是 ledger：fleet 首屏只露 8 行＋「查看全部」出口（完整查找去 /devices）；同一计数/状态在一屏内只出现一处（页头不重复关注面板的计数）；关注行计数已在人话标题里，行首只留语义色点。
- 概览主体是单列 field（KPI → 关注 → fleet 通栏表 → 事件）：live 数据下任何双列 split 都会被两栏高差踢出画布空洞（空洞只随数据搬家，不会消失），reflow 成单列结构消灭；fleet 表格占有全证据宽（Vercel：tables own the full evidence width）。关注为 0 时不摆空面板，一行 quiet 语义文本「暂无异常」。
- StatTile 的 sub 行恒预留（min-h）：peer 瓦片共享 label→value→detail 内部行，高度结构一致、不互撑。
- 概览「需要关注」只给聚合主行 + 去向链接：失败命令明细的单一证据家是活动页（同屏同一答案不复述第二处）；右列不再被明细 ledger 撑高。
- 设备概览组合 = KPI → 事实横条（设备状况 KV 多列通栏，回答「健康吗」）→ 双 ledger 并排（最近活动 | 命令历史，互为 peer 等高互不牵制）；列表型内容不进窄轨。
- 面积图平涂 `fillOpacity 0.1`：装饰性渐变/渐变淡出是硬反模式，渐变只允许作为有标注的连续数据标尺（守卫）。
- 用户可见文案不用破折号「——」接续句子（改逗号/句号）；代码注释不受此限。
