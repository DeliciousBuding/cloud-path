# WebUI 界面设计规范

最后更新：2026-09-09

> 本文是 WebUI 呈现、排版、布局和交互的单一事实源；用户侧名称见
> [name-lexicon.md](../docs/architecture/name-lexicon.md)，路由、数据获取与安全边界见
> [docs/design.md](../docs/design.md#前端react-router-7)，Capability 与领域语义见
> [capability-model.md](../docs/architecture/capability-model.md)。

WebUI 的默认视图是「人先看值，机器细节按需」：展示名 / 当前值 / 单位 / 状态 / 新鲜度进默认视图；
entity_id、capability URI、property key、raw JSON 只进能力 Inspector 与诊断页。

文本三层归属：

| 层 | 例子 | 归属 |
|---|---|---|
| 平台 UI 词 | 概览 / 网关 / 设备 / 状态 / 事件 / 操作 / 应用 | `webui/src/i18n/`（zh-CN / en-US，组件只通过 `t()` 读取） |
| 声明展示元数据 | 温度 / 蜂鸣器 / 按下 | Descriptor / Capability declaration 的 title/description |
| 机器身份 | `temperature`、`cloudpath.dev/capability/...` | 永远 canonical，不翻译 |

展示名解析优先级（`webui/src/lib/descriptor.ts`）：
声明 localized title → 平台本地化词典 → humanize → canonical machine name。
locale 匹配规则：优先精确 locale 和语言基础 locale；中文界面不回落英文声明，英文界面也不回落中文声明，
缺少当前语言声明时使用平台本地化词典，再保留机器名。插件应通过 `i18n` map 提供多语言标题，见
[docs/architecture/i18n.md](../docs/architecture/i18n.md)。

resolver 均为纯函数并有确定性单测（fallback 顺序、脏数据降级）；UI 不写设备特例。

新鲜度呈现：页头与列表行的「更新于 / 连接于」用相对时间（绝对时间进 title 悬停查看），
事实表与诊断页保留绝对时间。

长历史按天分组（EventFeed `dayGrouped` 与运行记录页操作历史同一视觉语言）：组头承载日期
（今天 / 昨天 / 月日），行内只显示时刻，完整时间进 title；避免「组头 + 每行完整日期」双重冗余。

跨设备列表的呈现约定：设备列表 2xl「关键读数」列至多两条声明主观测（与概览 KPI 同一
`metricTiles` 推导），能力全量事实面只在详情「能力」tab；运行记录页/概览的操作展示名经
`cmdMeta(cmd, actions?, idx?)` 解析——无单设备操作集时用 `commandDecl`（eventDecl 的操作侧
对称件）查 catalog 声明，再回落平台词典 / humanize。

状态三态约定：详情类页面在数据未到手时必须区分「加载中（骨架）/ 404（未注册空态）/
其它失败（可重试错误态）」。`isNotFound()`（webui/src/lib/api.ts）是 404 语义的唯一判定；
把 404 渲染成「加载失败」会让用户去查 server，而真相只是设备没接入。

值类型感知呈现（`SummaryValue.kind`，由 widget 推导）：布尔主值（开关/触发与否）渲染为状态胶囊
而非大字号——半屏大的「否」是排版错误；胶囊语义色只接 warn/bad，其余保持中性。单位经展示层单一
出口 `unitLabel()` 人话化（s→秒、Cel→°C），科学单位（V/lux/%）原样；诊断页 raw JSON 不受影响。

计数/频次图必须零基线（TrendChart `zeroBase`）：负轴或悬空基线会夸大差异，属数据可视化谎言；
窄高 sparkline 模式（`hideY`）连同横向网格省去，峰值由标题说人话；X 轴刻度粒度由调用方传入
（分桶图传时分，秒对 ≥1 分钟的桶是噪音）。

设备概览首屏三段：KPI 行 → [最近事件 2/3 | 设备状况 1/3] → 操作历史通栏置底（限 8 条 + 控制页
出口）。操作历史放右栏会拉到 20 行、把左栏踢出一大片空洞。

运行实例状态词汇覆盖两套后端事实源：edge pluginhost.State（大写 STOPPED/HEALTHY…）与 server
AppHost 的 appruntime.InstanceState（小写 running/stopping/failed…，internal/appruntime/types.go）；
observed.detail 的已知机器标记（server-apphost）经 `hostDetailLabel()` 人话化；未知值原样呈现，不猜。

视觉纪律（Vercel design.md 反模式审查）：无限循环动效（脉冲光环/闪动）与玻璃拟态一律移除（default to stillness / 拒 glass）；
普通元数据（适配器/串口/网关 ID）不用胶囊，降级为 mono 文本，只有状态保留胶囊；数值/时间列右对齐且表头同对齐；
时间戳与机器标识用 Geist Mono（只标识本身进 mono，整句不进）；散文不用破折号。胶囊只承载状态/语义：分类、计数、权限 scope、操作集来源等普通元数据用纯文本（高权限 scope 用语义色文字而非胶囊）。

### 设备操作与运行实例

操作及危险性只来自 Descriptor / Capability 或适配器白名单，不由前端猜测。Capability action 的
`title` / `description` / `destructive` / `confirmation` 是声明元数据；往返与安全边界见
[protocol.md](../docs/protocol.md#capabilities)，UI 不按操作名补造危险性。

参数优先以声明 schema 生成字段或“设置方式”表单，不注入默认值；只有无法安全展开的嵌套对象、
其他组合结构或未知约束才保留高级 JSON 编辑。平铺标量、数组、对象数组和根级 `oneOf` 在能无损
映射时使用表单，JSON 与表单切换不得丢弃字段。前端校验 JSON 语法、已支持的
required/type/enum、数值/字符串/数组边界以及 `oneOf` / `anyOf` / `allOf`，并遵守 64 UTF-8
字节及换行/NUL 传输门禁。未知约束参与组合匹配时使用未知态，以“部分参数由设备端确认”提示；
设备端仍是最终裁决者。保留用户输入，不静默剥离、截断或压缩 JSON；ack 失败缺 detail 时用可读兜底。

操作面板及独立按钮都检查当前身份：viewer、加载中、未登录或无效身份无写表单；保留显式开放模式
和合法服务身份 `id=0` 的既有设备操作接口。更换设备、账号、租户、角色或声明会卸载参数与确认状态，
旧请求/回执不能污染新身份。危险动作保留声明确认，发送后沿用 POST → WS ACK → 历史刷新或超时反馈。
确认框覆盖整个视口、限制背景滚动和键盘焦点；默认聚焦取消，关闭后恢复触发位置。长内容在对话框内
滚动，不把确认按钮挤到屏外。参数错误优先使用声明字段名及中文类型；宽屏控制区给表单更多宽度，
危险快捷动作不抢占首位。

Application Plane 的展示入口位于运行实例详情。应用目录声明 `kind=application`，或实例的
`edge_id=server` 时均可进入；目录为空不影响服务端应用。`server` 是中心服务的应用宿主，不是离线
网关，不链接到虚构的网关页。隔离方式沿用插件展示词汇：`shared` 为共享进程，`per-instance` 为
实例独立进程。

控制请求原样使用投影的 `v.id`（可能为裸标识，也可能带网关前缀）；records / bindings / jobs
三个只读请求始终使用 `desired.instance_id`。viewer 可读三个分区，但不能新建、编辑、启停、重新
下发或删除实例；切换为只读角色会关闭列表页已打开的创建或编辑表单。应用读面跟随 `authReady`：
`open`（L0/认证探针不可用）可读但强制只读，账号模式仍要求合法 `tenant_id>0`；服务令牌的
`user.id=0` 是合法身份。写操作继续只允许已登录的 operator/admin。插件业务导航、`/apps/:route`
路由和自定义 iframe bridge 的契约见 `webui/src/components/plugin-ui/README.md`。

普通视图展示可辨认的结构化字段、公开 Descriptor / Capability 名称与本地化时间。已知通用字段沿用
公共词汇；无展示声明的字段保留原字段名，不能用“数据项 1”掩盖含义。标题/名称、状态类字段与已填
内容优先，空值和其它字段可在结构化视图继续展开；数组保持原始顺序与空值位置。未知业务枚举保留
应用原值，不维护业务专用词典或推断状态颜色。明确带时区且有效的 RFC3339 字符串按当前时区显示，
同时保留原值；不猜无时区文本或普通数字。记录分类/标识、任务标识、时间表达式与完整 JSON 仍在
技术详情中。实例配置默认折叠，不以 `app_config` 原文作为应用界面。绑定实体缺少公开名称或局部标识
有歧义时，不猜设备归属；实体与能力展示名相同时不重复堆叠。记录为主内容，绑定与调度作为紧凑的
运行上下文。

运行期绑定与声明任务由 `running` 投影说明；应用停止后这两类清空，历史记录和持久计划仍可查看。
持久计划的启用/取消、下次时间、最近调度、错过策略单独呈现，调度时间不代表执行成功。计划时间按其
timezone 显示，复杂时间规则保留到技术详情，不猜执行周期。

REST 仍是数据事实源：查询缓存按租户、用户、裸实例标识隔离；记录分页每页 20 条，分类筛选失败后
仍可清除或重试。初读、空列表、权限拒绝与读取失败分别展示，失败不伪装为空。切换实例/身份会复位
局部筛选与分页并取消旧请求；旧响应不能覆盖新数据或使新会话退出。单例 WebSocket 的 `domain_record`
仅触发对应实例查询失效，不累积第二份记录库；相邻实例通知通过直接订阅逐个消费，同批通知合并补读。
每次连接建立/重建都重新读取 REST，覆盖断线窗口；在线时也每 10 秒读取绑定与任务，以感知没有独立
推送的启停和调度变化。控制投影变化会立即补读，运行事实不由 `desired.enabled` 乐观推断。

实时状态分组视图 = 紧凑瓦片矩阵（StateTile：2/3/4 列随宽度）：单标量不拉通栏行（标签↔值
扫视距离是可读性成本）；布尔走胶囊、机器串（无 CJK/足够长/id 字符集）在默认视图以 mono 降级呈现、
完整值进 title，表格视图不降级；次要观测折进 details。

中文字体纪律：中文走自托管 Noto Sans SC 可变子集（OFL；GB2312 一级字表 ∪ 界面词汇 ∪ 中文标点，
unicode-range 只接管 CJK，拉丁/数字仍走 Geist；可复现构建见 webui/scripts/build-cjk-subset.py）。
负字距是拉丁刻度：全仓止于 CJK 安全值 -0.01em（`tracking-tight` 禁用）；mono 等宽面恒零字距（负字距破坏等宽网格）；中文正文保持 0。

### 10.9 排版刻度与字重（2026-09-05 增补）

- 字重阶梯三档：regular 400（降级/单位）/ medium 500（正文与标签）/ semibold 600（标题与值），标题不超 600（Vercel 纪律）。浅色正文基底 450（CJK 光学平价，可变轴真实实例）；**暗色整条阶梯等差上移 50**（450→500 / 500→550 / 600→650）——纯黑 + 灰度抗锯齿削约半档笔画，是 Noto Sans SC 暗底发飘的根因；补偿只平移不改变相对差与层级。
- 字号刻度 {11,12,13,14,15,18,20,22,24,26,28,30}，全部由 token 管理：11=mono 微文本（`text-micro`）；12=元数据（`text-meta`）；13=密排正文（`text-compact`）；14=正文/导航（`text-body`）；15=面板标题（`text-lead`）；18=紧凑统计值（`text-stat`）；20=小节标题（`text-section-sm`）；22=异常页标题（`text-section`）；24=详情 hero（`text-hero`）；26=认证页标题（`text-title`）；28=页标题（`text-page-title`）；30=指标 display（`text-display`，配 `.metric`）。组件禁止 `text-[Npx]`。
- `.metric` 是拉丁负字距唯一出口：等宽数字 + `-0.02em`（Geist 数字刻度）；CJK 文本负字距止于 -0.01em（全角字面会挤），tracking-tight/tighter 灭绝。
- 中文标点 `palt` 比例宽度全局启用（body），收紧全角逗号/句号而不碰汉字字面；数字恒 `tnum`。标题 `text-wrap: balance`、散文 `pretty`。
- 字号下限：非 mono 文字 ≥12px；mono 微文本（标识/raw JSON/版本号/SVG 轴刻度）11px；10px 全仓灭绝。阅读字号（键值行/按钮/侧栏导航）14px。移动端输入框（≤639px）提升到 16px，避免 iOS Safari 聚焦自动放大。
- 字符串型 KPI（版本号等）用 mono medium 渲染，不用 sans semibold：字符串不是量级。
- 散文行宽 ≤62ch：全宽长行是布局失败（About 等通栏段落加 max-w）。

### 10.10 形状与结构

- 圆角只有三种语义：tile 8px（`rounded-tile`，卡内子面/内联 note/控件）、card 12px（`rounded-card`，.card/浮层 Toast/品牌 logo）、pill 999px（`rounded-pill`，状态点/胶囊/圆形按钮）；`rounded-sm/md/lg/xl/2xl` 不再作为业务类使用。
- 列表在 Panel 内用 divider rows（`divide-y`），不套子卡（嵌套卡片是硬反模式）；Admin 用户/令牌行已行化。
- 表格体单元格 `vertical-align: baseline`（对齐行首基线；多行表头才底对齐）。
- 长 ledger（运行记录页事件记录/操作记录）本地滚动（max-h + overflow-y-auto），页面保持一屏可读；天分组头 sticky 于滚动容器顶，跨天查找不迷路。
- 空态/错误态不用装饰性图标瓷砖（彩色圆底）：plain 语义色图标即可。

### 10.11 页面组合

- 概览是摘要不是 ledger：fleet 首屏只露 8 行＋「查看全部」出口（完整查找去 /devices）；同一计数/状态在一屏内只出现一处（页头不重复关注面板的计数）；关注行计数已在人话标题里，行首只留语义色点。
- 概览主体是单列 field（KPI → 关注 → fleet 通栏表 → 事件）：live 数据下任何双列 split 都会被两栏高差踢出画布空洞（空洞只随数据搬家，不会消失），reflow 成单列结构消灭；fleet 表格占有全证据宽（Vercel：tables own the full evidence width）。关注为 0 时不摆空面板，一行 quiet 语义文本「暂无异常」。
- StatTile 的 sub 行恒预留（min-h）：peer 瓦片共享 label→value→detail 内部行，高度结构一致、不互撑。
- 概览「需要处理」只列实时故障，恢复后自动消失；历史失败操作单独成组并跳到运行记录处理。失败明细的单一证据家是运行记录页（同屏同一答案不复述第二处）；右列不再被明细 ledger 撑高。
- 设备概览组合 = KPI → 事实横条（设备状况 KV 多列通栏，回答「健康吗」）→ 双 ledger 并排（最近事件 | 操作历史，互为 peer 等高互不牵制）；列表型内容不进窄轨。
- 面积图平涂 `fillOpacity 0.1`：装饰性渐变/渐变淡出是硬反模式，渐变只允许作为有标注的连续数据标尺。
- 用户可见文案不用破折号「——」接续句子（改逗号/句号）；代码注释不受此限。

### 10.12 组件库与复用边界

- 四层职责固定：token（`webui/src/index.css` 的 `@theme`）→ primitive（`webui/src/components/ui.tsx`）→ 领域组件（`webui/src/components/**`）→ 页面组合（`webui/src/pages/**`）。页面负责数据与组合，不复制控件行为。
- 可复用控件必须进入 primitive 或领域组件层，并带行为测试；不要在两个页面各写一份按钮、下拉、弹层或键盘逻辑。
- 页面不得直接写 `<select>`；统一走 `Select` primitive。原生 `<button>` / `<input>` 只允许用于语义明确的专用控件（如 checkbox、range、file），且仍须消费 token 类。
- primitive 负责交互与无障碍，领域组件负责业务语义，页面负责数据与布局；跨层直写原生控件或裸样式视为设计系统漂移。
- `scripts/check_design_tokens.py` 同时守 token 与 `<select>` 边界；新增 primitive 行为必须有组件测试。

### 10.13 设计 token（2026-09-09 收口）


- SSOT 是 `webui/src/index.css` 的 `@theme`。组件只消费语义 token，不写裸色值、任意 px 字号、裸圆角、裸 z-index、裸动效时长。
- 颜色：`--color-*`（canvas/surface/ink/accent/ok/warn/bad/idle）；语义色只表达状态，不作为装饰。
- 字号：`--text-*`（micro/meta/compact/body/lead/stat/section-sm/section/hero/title/page-title/display）；12/14 分别落在 meta/body。
- 形状：`--radius-tile` / `--radius-card` / `--radius-pill`；控件高度：`--spacing-control-sm` / `--spacing-control` / `--spacing-touch`。
- 层级：`--z-local` / `--z-sticky` / `--z-nav` / `--z-overlay`；动效：`--motion-fast` / `--motion-base` / `--motion-slow` / `--motion-shimmer`，统一 `--ease-standard`。
- 焦点：`--focus-ring-color` / `--focus-ring-width` / `--focus-ring-offset` / `--focus-halo` / `--focus-halo-bad`；键盘焦点必须可见，输入框错误态用红色 halo。
- 状态：`--opacity-disabled`；骨架屏 1.6s 微光，`prefers-reduced-motion` 下关闭。
- 下拉框统一走 `Select` 原语：触发器与弹层都在 DOM 中，使用 combobox/listbox ARIA、键盘导航、外点关闭和 token 化圆角/阴影；视觉隐藏的原生 `select` 只保留表单与读屏语义，不再依赖 `appearance: base-select` 或浏览器原生弹层。筛选器用 `pill`，紧凑表单用 `compact`。
- 输入框和 Select 的高度只走 `--spacing-control-sm` / `--spacing-control` / `--spacing-touch`，禁用态统一 `--opacity-disabled`；组件不再自己叠 `min-h-11` / `sm:min-h-0`。
- `theme-color` 随手动浅/深主题更新；Firefox 使用 token 色滚动条，WebKit 使用同一 token 色。
- CI 由 `scripts/check_design_tokens.py` 守门，禁止任意 px 字号、裸圆角、裸 z-index、`min-h-11`、`transition-all` 和业务源码裸色值。
- Vercel 官方 `design.md` 是报告网站品牌指南，不是 CloudPath 的 token 来源。只吸收其判断原则，不引入 `vbg-*` 类名、色值或圆角；本地参考文件在 `.local/design/vercel-design.md`，不提交仓库。
