# 插件 UI 运行时

最后更新：2026-09-09

本目录实现 `docs/architecture/plugin-ui.md` 的前端运行时。插件只声明导航、页面和
白名单 section；Core 负责路由、权限、数据读取和渲染。任意 React/JavaScript bundle、
远程脚本、HTML 和全局 CSS 都不进入主 SPA。

## 导航与路由

Application 插件通过 `contributes.applications[].ui` 声明：

```yaml
ui:
  apiVersion: 1
  navigation:
    title: 药盒提醒
    route: pillbox
    icon: pill
    order: 30
    visibility: instance-enabled
  pages:
    - id: home
      title: 药盒提醒
      sections:
        - type: status
        - type: metrics
          source: records
          recordType: window
        - type: actions
          source: manual-jobs
        - type: records
          recordType: window
          presentation: timeline
        - type: form
          source: config
```

运行时规则：

- `route` 是稳定 slug，页面地址为 `/apps/{route}`；子页面为
  `/apps/{route}/{pageId}`。
- 只有已验证的 Application 插件且当前可见实例满足 `visibility` 时才进入主导航。
- Driver/Connector 不进入业务主导航；Driver 的 `ui.device` 只用于设备详情扩展。
- 同一 route 冲突时全部 fail-closed，页面显示“应用入口冲突”。
- 未知 route、未安装、未验证、无实例、停用、无页面和未知 pageId 都有独立可读状态。
- 旧的 `/pillbox` 重定向到 `/apps/pillbox`；带设备参数的旧入口仍保留设备详情兼容跳转。

## 通用 section

插件页面可以用 `description` 说明用途，用 section 的 `title` / `description` / `emptyText` 组织内容；`fields` 声明指标卡和记录主视图要展示的字段，可带 `label`、`unit`、`precision`、`format`、`values`、`hideWhenEmpty`。未声明字段不会占据主视图，原始数据只在“更多信息”中展开。

Navigation、page、section 与配置字段都按当前语言解析 manifest 的 `i18n` / `valuesI18n`；缺失翻译回落到 manifest 原文，机器 `route` / `id` 永不翻译。

`ApplicationSections.tsx` 只渲染白名单类型：

| type | 行为 |
|---|---|
| `status` | 实例期望态/实际态、同步状态 |
| `metrics` | 按 `recordType` 过滤记录后取最新一条，字段由声明或数据推导 |
| `actions` | 只显示 `manual_only=true` 的 job，复用现有幂等键与 RBAC |
| `records` / `timeline` | 严格按 `recordType` 过滤，支持 list/timeline/table/cards |
| `table` | records 或 bindings 表格 |
| `schedule` | 已保存计划，不把“已排定”显示成“已执行” |
| `chart` | 从声明记录类型中生成轻量数值趋势 |
| `form` | `fields` 或安全 JSON Schema 驱动；`app_config.*` 按字段类型写回 JSON，原始 JSON 仅在高级详情 |
| `markdown` | 纯文本行渲染，不执行 HTML |
| `diagnostics` | 实例、绑定、任务和配置的技术详情 |
| `custom` | 见下方 sandbox bridge |

Core 不维护药盒、音乐、呼叫等行业类型映射。未知 section 在归一化阶段丢弃；
运行到未知类型时显示可读错误，而不是执行内容。

## 自定义 iframe bridge

`custom.entry` 只能是插件包内相对路径。运行时请求：

```text
GET /api/plugin-ui/assets/{pluginID}/{canonicalVersion}/{path}
```

`canonicalVersion` 优先取 `/api/plugins` 的 `catalog.version`，目录缺席时才回落到
`instance.desired.version`。iframe 固定：

```html
<iframe sandbox="allow-scripts" referrerpolicy="no-referrer">
```

不授予 `allow-same-origin`，因此 iframe 没有主页面 cookie/localStorage/DOM 访问权。
加载完成后 Core 发送：

```ts
{ type: 'cloudpath:init', apiVersion: 1, instance: { id, plugin_id }, scopes }
```

iframe 通过 `postMessage` 发送：

```ts
{ type: 'cloudpath:request', id, method, params }
```

Core 只接受白名单方法，并再次检查 section 声明的 scope：

| method | scope | 说明 |
|---|---|---|
| `instance.get` | `instance.read` | 单实例投影 |
| `bindings.list` | `bindings.read` | 设备绑定 |
| `records.list` | `records.read` | 记录，limit 最大 100 |
| `jobs.list` | `jobs.read` | 任务与计划 |
| `jobs.run` | `jobs.run` | 仅 `manual_only=true`，保留幂等键 |
| `config.update` | `config.write` | 配置写面，仍受 API RBAC |

响应为 `{ type: 'cloudpath:response', id, ok, data?, error? }`。未知 method、未知 scope、
非法参数和上游错误都 fail-closed，不把 Core 原始错误文本或 secret 传进 iframe。

## 开放访问与写权限

`authStatus='open'`（L0 或认证探针不可用）允许读取 Application Console 和动态路由，
但 `ApplicationPage` 对 open 模式强制只读；`appActionScope` 仍只允许已登录的
operator/admin 执行 job。账号模式下仍要求合法 `tenant_id>0`，API 的 401 继续由全局
鉴权收敛处理。

## 验证

```bash
cd webui
pnpm typecheck
pnpm test -- --run
pnpm build
```
