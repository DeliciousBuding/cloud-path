# GitHub 插件发现与信任链

最后更新：2026-09-10

> 本文定义 CloudPath 插件如何通过 GitHub 被发现、验证与分发。配套决策见
> [adr/0002-github-plugin-discovery.md](adr/0002-github-plugin-discovery.md)。

## 1. 仓库形状与发现规则

CloudPath 同时支持两种兼容的插件仓库形状：

| 形状 | 根文件 | Manifest | Release tag | 选择方式 |
|---|---|---|---|---|
| 单插件仓库 | `plugin.yaml` | 根 `plugin.yaml` | `v<manifest.version>` | 直接安装仓库，无需 `--plugin` |
| Plugin monorepo | `plugins.yaml` | `<path>/plugin.yaml` | `<path>/v<manifest.version>` | 必须用 `--plugin <slug\|id\|path>` 精确选择 |

| 维度 | 约定 |
|---|---|
| 发现 Topic | `cloudpath-plugin` |
| Catalog API | `plugins.cloudpath.dev/v1alpha1` / `PluginCatalog` |
| Catalog schema | [`spec/plugin-catalog.schema.json`](../../spec/plugin-catalog.schema.json) |
| `tagPrefix` | 必须严格等于条目的 `path` |
| 分发载体 | GitHub Release 资产（二进制/归档 + checksums） |
| 规范命名空间 | `cloudpath.dev/*`；第三方用发布者命名空间（如 `io.github.<owner>/capability/...@1`） |

monorepo 的根目录只有 `plugins.yaml`，每个条目的 manifest 位于 `<path>/plugin.yaml`。核心、Server、
Edge 与 WebUI 只把 monorepo repo 当成普通 source；插件路径、tag 命名空间和发布节奏仍由 catalog
与各自 manifest 决定。

Topic `cloudpath-plugin` 只是**候选集合**，不是信任证明：任何仓库都能给自己打这个 topic。

```yaml
apiVersion: plugins.cloudpath.dev/v1alpha1
kind: PluginCatalog
plugins:
  - id: io.github.deliciousbuding.cloud-path-app-scheduled-compartment
    slug: scheduled-compartment
    kind: Application
    path: apps/scheduled-compartment
    tagPrefix: apps/scheduled-compartment
    asset: cloud-path-app-scheduled-compartment
    archived: false
```

`archived: true` 的条目不得出现在 search、inspect、install 候选中；`asset` 只是可选首选
Release 资产名，不改变 digest/permission/source 校验。 selector 只做大小写敏感的精确匹配，
依次接受 slug、id、path。

## 2. 双通道发现

1. **开放通道**：GitHub Topic `cloudpath-plugin`。`cloudpath plugin search` 先取 topic 命中，
   再在受限数量内展开各仓库的 `plugins.yaml`；同一 repo 的每个非 archived 条目成为独立候选。
   单个 catalog 缺失按普通单插件仓库或 topic 候选处理；无效 catalog / 网络失败只产生安全
   warning，不使整个搜索崩溃。
2. **精选通道**：官方 Registry 维护已审查插件与发布者策略。Registry 记录 plugin id、版本、
   来源仓库、资产 digest、`verifiedPublisher`、兼容范围与协议版本；monorepo 条目可额外固定
   `tag` 与 `pluginPath`，提供时必须与解析结果完全一致。

Registry 不记录 `latest` 作为可执行事实：单插件仓库固定精确版本与 digest，monorepo 还固定
精确 tag 与插件 path。

## 3. 验证流程（发现之后、执行之前）

任何一步失败即拒绝安装：

1. 按 source 解析仓库形状：单插件读根 `plugin.yaml`；monorepo 必须提供 `--plugin`，selector
   必须命中一个非 archived 条目；
2. Manifest 位于根或 `<path>/plugin.yaml`，且通过 `spec/plugin-manifest.schema.json`；
3. `compatibility.core` 包含当前 Core 版本，协议与 kind 合法；
4. Release 解析：
   - 单插件仓库使用 `/releases/latest`；
   - monorepo 不依赖 `/releases/latest`，而是列出 Release，按 `<tagPrefix>/v` 前缀过滤并选
     最高 semver；随后必须断言 `tag == <tagPrefix>/v<manifest.version>`，否则 fail-closed；
5. 选择资产并校验 sha256；可选 `gh attestation verify` / 构建证明；
6. 权限披露在安装时展示并确认；
7. `plugins.lock` 记录 `id`、精确 `version`、`digest`、`source`、验证结果，以及新安装的精确
   `tag`（和 monorepo 的 `pluginPath`）。

`--allow-unreviewed` 只允许首个安装采用未审查 TOFU；更新不会把 verified 安装降级为
unreviewed TOFU。跨 source（例如旧单插件仓库迁移到 monorepo）必须显式
`--allow-source-change`，但该开关不放宽 verified/digest/permission 门禁。

## 4. 信任分级

| 级别 | 含义 |
|---|---|
| 未审查 | topic 命中或 catalog 条目，但 Registry 未收录；安装时明确标注 |
| 已审查 | Registry 收录，发布者/摘要已验证；可进一步绑定 tag/pluginPath |
| 官方 | 维护者发布，`verifiedPublisher` 匹配 |

## 5. 当前范围与非目标

- 当前支持单插件仓库和 `plugins.yaml` monorepo 的发现/选择/安装/更新；
- 当前不提供中心化付费商店，也不以 topic 或 catalog 代替 digest/权限验证；
- v1 不承诺不受信任插件的强 OS 沙箱；先做进程隔离 + 权限披露。
