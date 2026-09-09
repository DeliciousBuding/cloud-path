# Plugin UI Contribution 设计

最后更新：2026-09-09

> 状态：本文定义 CloudPath 插件 UI 的正式契约。`ui` 是 Manifest 的声明式贡献，
> 不是任意 React bundle 注入。Core 负责校验、路由、权限和渲染；插件只声明页面
> 结构与数据意图。

## 1. 目标

1. 安装并启用 Application 插件后，主导航自动出现该应用的业务入口。
2. 每个业务应用拥有稳定、可深链接的独立路由，例如 `/apps/pillbox`。
3. 通用 UI 由 Core 的白名单组件渲染，插件不需要把业务实现塞进 Core。
4. 复杂页面有受控的自定义扩展路径，但不允许任意脚本进入主 SPA。
5. Driver 插件可以扩展设备详情页，但不进入业务主导航。
6. UI 贡献必须受租户、RBAC、实例状态、Core 兼容性和 Manifest 校验约束。
7. UI 不复制业务事实源：状态来自实例投影，动作来自 job descriptor，历史来自领域记录。

## 2. 非目标

- 不允许插件向主 WebUI 注入任意 React、JavaScript、HTML、远程脚本或全局 CSS。
- 不允许插件直接读取浏览器 cookie、localStorage、主页面 DOM 或 Core 内部 API。
- 不新增 `/api/pillbox/*`、`/api/music/*` 等业务特例 API。
- 不把插件本地安装路径、secret、entrypoint 或任意文件路径暴露给浏览器。
- 不把 Connector 当作已有 UI 运行时；Connector UI 仍为未实现。

## 3. 贡献位置

Application 在 `contributes.applications[]` 上声明 `ui`：

```yaml
contributes:
  applications:
    - id: scheduled-compartment
      title: 取药提醒
      ui:
        apiVersion: 1
        navigation:
          title: 药盒提醒
          i18n:
            zh-CN: 药盒提醒
            en-US: Pillbox reminders
          icon: pill
          order: 30
          route: pillbox
          visibility: instance-enabled
        pages:
          - id: home
            title: 药盒提醒
            i18n:
              zh-CN: 药盒提醒
              en-US: Pillbox reminders
            sections:
              - type: status
              - type: metrics
              - type: actions
                source: manual-jobs
              - type: records
                recordType: window
                presentation: timeline
              - type: form
                source: config
```

Driver 在 `contributes.drivers[]` 上声明 `ui.device`，用于扩展设备详情页：

```yaml
contributes:
  drivers:
    - id: stcb
      title: STC-B Driver
      ui:
        apiVersion: 1
        device:
          sections:
            - type: status
            - type: actions
              source: device-actions
            - type: diagnostics
              source: diagnostics
```

Connector 暂不接受 `ui`；manifest 校验直接拒绝，避免把未实现的 UI 运行时伪装成可用。

## 4. 路由与导航

### Application

- `ui.navigation.route` 必须是稳定 slug：`^[a-z0-9][a-z0-9-]{0,62}$`。
- `navigation.title` / `pages[].title` / `section.title` 可选 `i18n` map；旧字段是默认值，机器 `route` / `id` 永不翻译。Page 用 `<locale>.description`，section 还用 `<locale>.emptyText`；字段用 `fields[].i18n`（`<locale>` 与 `<locale>.description`），枚举展示值用 `fields[].valuesI18n[<raw>][<locale>]`。用户可见文案原则见 [i18n.md](i18n.md)。
- Core 生成 `/apps/{route}`；后续页面为 `/apps/{route}/{pageID}`。
- 旧的业务路径只做重定向，不进入新契约。
- 导航项只在以下条件全部满足时出现：
  1. 插件安装且 `verified=true`；
  2. 插件 kind 为 `Application`；
  3. `ui.navigation` 合法；
  4. 当前租户至少有一个可见实例；
  5. 至少一个实例 `enabled=true`；
  6. 当前用户拥有读权限。
- `visibility`：
  - `instance-enabled`：默认，只有启用实例才显示；
  - `always`：安装后显示，页面内说明尚未启用。
- 同一 `route` 冲突时安装/目录投影失败关闭，不静默覆盖。
- 多个实例时，页面顶部提供实例选择；URL 可带 `?instance=<instance_id>`。

### Driver

- Driver 不生成主导航。
- `ui.device.sections` 追加到设备详情页的“高级”或独立设备扩展区。
- 设备页面始终以 Descriptor/Capability 为事实源；UI 不能伪造观测值。

## 5. 页面结构

每个 Application page 由白名单 section 组成。`page.description` 用一句话说明业务用途；`section.title`、`section.description`、`section.emptyText` 用于把通用渲染器变成可读的业务界面。当前契约支持：

| type | 用途 | 数据源 |
|---|---|---|
| `status` | 实例状态、版本、drift/stale、最近更新时间 | instance |
| `metrics` | 从状态或领域记录提取的指标卡 | instance / records |
| `form` | 配置表单；由 schema 驱动，禁止裸 JSON 作为主界面 | config |
| `actions` | 手动 job 按钮；自动 job 不渲染成按钮 | jobs |
| `records` | 领域记录列表 | records |
| `timeline` | 领域记录时间线 | records |
| `table` | 通用表格 | records / bindings |
| `schedule` | 计划/窗口展示 | jobs / records |
| `chart` | 时序趋势 | records |
| `markdown` | 受限 Markdown 说明，禁止 HTML | inline |
| `custom` | 受控自定义页面，见 §7 | custom |

`fields` 用于声明主视图真正要展示的字段。字段可以带 `label`、`unit`、`precision`、`format`（`text` / `time` / `number` / `percent` / `duration`）、`values`（机器值到用户文案的映射）和 `hideWhenEmpty`；未声明的字段不会进入指标卡或记录主视图，完整原始数据仍在“更多信息”中。

配置表单字段类型为 `string` / `number` / `integer` / `boolean` / `select` / `textarea` / `array`。`array` 必须声明 `itemFields`（仅允许标量类型，可嵌套一层），并可声明 `minItems` / `maxItems`；数组内容由 Core 结构化编辑，禁止要求用户手写 JSON。`unit` 可以是字面单位，也可以指向记录中的字段路径（例如 `temperature.unit`），渲染器会优先解析路径。

通用 section 示例：

```yaml
sections:
  - type: status
  - type: metrics
    source: instance
  - type: actions
    source: manual-jobs
  - type: records
    recordType: window
    presentation: timeline
  - type: form
    source: config
```

### 配置表单

`form` section 读取插件声明的配置 schema。配置 schema 必须是：

- 安装时校验过的 JSON Schema；
- 只允许 JSON 基础类型、`enum`、`minimum/maximum`、`pattern`、`required`、`itemFields`、`minItems/maxItems`；
- 不包含本地路径、secret、远程引用或 `$ref` 到网络；
- 页面默认渲染表单，原始 JSON 只放“高级参数”。

如果插件没有提供安全 schema，页面显示“配置表单不可用”，并提供高级 JSON 入口；不能空白或静默忽略配置。

## 6. 数据与动作契约

页面只消费 Core 已有公开读面：

```text
GET /api/plugin-instances/{id}
GET /api/plugin-instances/{id}/bindings
GET /api/plugin-instances/{id}/jobs
GET /api/plugin-instances/{id}/records
POST /api/plugin-instances/{id}/jobs/{job}/run
PATCH /api/plugin-instances/{id}
```

约束：

- `actions` 只渲染 `manual_only=true` 的 job；自动 job 不显示为按钮。
- `records` 只读；UI 不能直接写业务表。
- 写操作必须经过现有 RBAC、CSRF、幂等键和审计。
- UI 显示“已受理”不等于设备已执行；设备 ACK 和领域记录才是后验事实。
- 任何失败都使用稳定错误码，不解析后端自然语言。

## 7. 自定义页面

自定义页面采用**受控 iframe**，不进入主 SPA：

```yaml
- type: custom
  entry: ui/index.html
  scopes: [instance.read, jobs.run, records.read]
```

规则：

- `entry` 只能是插件包内相对路径，禁止 `..`、绝对路径和远程 URL。
- iframe 使用 `sandbox="allow-scripts"`，不授予 `allow-same-origin`。
- iframe 不能直接读主页面 cookie/localStorage，也不能直接调用 Core API。
- iframe 通过 `postMessage` 与 Core 的 `PluginUIBridge` 通信。
- Core 按 `scopes` 代理白名单 API，并对 tenant、instance、RBAC 再校验。
- 自定义页面失败时显示错误边界和返回业务页入口，不拖垮主 WebUI。
- 没有 `entry` 或资产不可用时 fail-closed，显示“自定义界面不可用”。

### 自定义资产端点

Core 提供：

```text
GET /api/plugin-ui/assets/{pluginID}/{version}/{path:.*}
```

- 只服务中心 AppHost 本地安装、且 manifest 已声明至少一个 `custom` section 的 Application
  插件资产；
- `path` 只能位于插件包 `ui/` 子树，拒绝绝对路径、`..`、反斜杠、URL、symlink 逃逸；
- 只允许 HTML/CSS/JS/JSON/SVG/图片/字体等固定 MIME 白名单，单文件上限 5 MiB；
- 版本必须与请求插件目录的 lock/manifest 一致；
- 账号模式必须认证；L0 open 模式允许匿名读取，但仍按空租户做 catalog 可见性校验；
- 响应固定 `Cache-Control: no-store`、`X-Frame-Options: SAMEORIGIN` 和仅允许同源静态
  资源的 CSP，`connect-src 'none'`；
- 未安装、未声明 custom entry、版本不匹配、跨租户不可见或本地包不存在（例如 Edge-only）
  一律 `404`；错误响应不包含本机路径。

自定义 UI 是逃生通道，不是默认路径。药盒、呼叫、环境、音乐、告警优先用声明式 section 实现。

## 8. 权限与安全

- Manifest `ui` 是公开元数据，不得包含 secret、本机路径或内部主机名。
- 安装时校验 `apiVersion`、route、section type、source、scope 和大小上限。
- 未识别字段按当前兼容策略忽略；未知 section type fail-closed，不渲染为 HTML。
- 插件停用、卸载、版本不兼容或无权限时，导航和路由一起消失。
- UI 贡献不能绕过现有 API 鉴权；iframe scoped API 只是二次收窄，不是替代鉴权。
- 所有 UI 文案使用人话；机器 ID、digest、raw JSON 只出现在高级详情。

## 9. 版本与兼容

- `ui.apiVersion: 1` 是 UI 贡献结构版本。
- 插件 `version` 是发布版本；`compatibility.core` 继续约束 Core 版本。
- Core 只渲染自己认识的 `ui.apiVersion` 和 section type。
- UI schema 变化必须保持旧 route 稳定；新增页面用新 page id。
- 卸载或停用插件不删除领域记录；页面进入“未启用”状态。

## 10. 当前插件映射

| 插件 | route | 首页重点 | 主要动作 | 主要记录 |
|---|---|---|---|---|
| scheduled-compartment | `/apps/pillbox` | 下一次提醒、确认状态、计划 | start-reminder、confirm-window | window |
| hall-pillbox | `/apps/hall-pillbox` | 开盖确认、磁场/开窗状态 | start-window、confirm-window | window |
| button-indicator | `/apps/service-desk` | 当前呼叫、确认队列 | request、acknowledge | request |
| environment-guard | `/apps/environment` | 温度、光照、阈值状态 | arm/disarm、阈值配置 | alert |
| music-player | `/apps/music` | 曲目、播放状态、进度 | play、stop | playback |
| sensor-alert | `/apps/sensor-alert` | 当前告警、阈值、静默状态 | arm/disarm、静默配置 | alert |
| stcb-driver | 无导航 | 设备详情扩展：诊断、原始端口、校准 | 设备动作由 Capability 决定 | 无业务记录 |

## 11. 实施边界

必须实现：

- Manifest UI schema 与校验；
- 插件目录 API 的安全 UI 投影；
- WebUI 动态导航、`/apps/:route` 路由和通用 Application Console；
- 六个 Application 的 `ui` 声明与页面配置；
- Driver 设备详情 UI section 投影；
- 自定义 iframe bridge 的契约和 fail-closed 行为；
- 单元/集成测试、文档和契约检查。

不做：

- 任意第三方 React/JS bundle；
- 业务特例 Core API；
- Connector UI 运行时；
- 把设备原始 JSON 直接作为普通用户主界面。
