# Plugin Registry 与 CLI

最后更新：2026-09-10

> Registry 索引、CLI 与供应链验证的实现规则。发现通道与信任链见
> [github-ecosystem.md](github-ecosystem.md)。

## 1. Registry Manifest（索引项）

Registry 记录已审查插件，字段如下：

| 字段 | 类型 | 说明 |
|---|---|---|
| `id` | string | 插件 ID（命名空间，如 `io.github.<owner>.<name>`） |
| `version` | string | semver |
| `kind` | string | Driver / Application / Connector |
| `source` | string | 仓库 URL（如 `https://github.com/owner/repo`） |
| `digest` | string | 资产 sha256 |
| `tag` | string（可选） | 精确 Release tag；提供时必须与解析结果一致 |
| `pluginPath` | string（可选） | monorepo 内仓库相对路径；提供时必须与解析结果一致 |
| `verifiedPublisher` | string | 已验证发布者 |
| `protocol` | int | RPC 协议版本 |
| `compatibility` | string | 兼容 Core 版本范围 |

`source` 始终是 repo URL，不因为 monorepo 条目而变成子目录 URL。缺少 `tag`/`pluginPath` 的旧
Registry 记录继续有效；一旦提供，`ValidateRegistryBinding` 会同时校验二者。

锁文件 `plugins.lock` 记录：`id`、精确 `version`、资产 `digest`、`source`、验证结果，以及新安装
的精确 `tag`；monorepo 条目同时记录 `pluginPath`。lock 格式版本仍为 `1`，旧文件中缺少两个可选
字段时按空值读取。

## 2. 仓库与 Catalog

单插件仓库：

```text
plugin.yaml
```

Plugin monorepo：

```text
plugins.yaml
<path>/plugin.yaml
```

`plugins.yaml` 使用 [`spec/plugin-catalog.schema.json`](../../spec/plugin-catalog.schema.json)：

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

约束：

- `tagPrefix` 必须严格等于 `path`；
- selector 大小写敏感，精确接受 `slug`、`id` 或 `path`；
- `archived: true` 的条目不进入 search、inspect、install 候选；
- monorepo Release tag 必须是 `<path>/v<manifest.version>`；安装时列出匹配前缀的 Release，
  选取最高 semver 后仍断言精确 tag，不一致即 fail-closed；
- 单插件仓库继续使用根 `plugin.yaml` 与 `/releases/latest`，行为不变。

## 3. CLI 子命令面（`cloudpath plugin`）

```text
cloudpath plugin search <query>       # GitHub topic + 受限展开 monorepo catalog
cloudpath plugin inspect <source>     # 单插件：根 plugin.yaml
cloudpath plugin inspect <source> --plugin <slug|id|path>
cloudpath plugin install <source>     # 单插件；monorepo 必须带 --plugin
cloudpath plugin update <id> [--source URL] [--plugin SELECTOR] [--allow-source-change]
cloudpath plugin enable <id>          # 写入启用期望态
cloudpath plugin disable <id>         # 写入停用期望态
cloudpath plugin remove <id>          # 卸载（默认保留数据，purge 另设）
# 匿名 GitHub API 限额 60 次/小时：设置 GITHUB_TOKEN（或 GH_TOKEN）走认证额度，
# 否则 discover/install 可能返回 ERR_RATE_LIMITED。令牌只从环境读取，不写 lock/日志。
```

更新默认继续使用 lock 中的 `source`。迁移到另一个 source（例如旧单插件仓库到 monorepo）必须
同时给出 `--source` 与 `--allow-source-change`；该开关只允许 source 变化，不能把 verified 安装
降级为 unreviewed TOFU，也不放宽 digest/permission/publisher 校验。monorepo 更新默认用 lock 的
`pluginPath`（缺失时用 plugin id）作为 selector，也可用 `--plugin` 覆盖。

## 4. 验证链（install 前强制，任一失败即拒绝）

1. 解析单插件或 catalog source；catalog 必须有精确、非 archived 的 `--plugin` 选择；
2. 读取根 `plugin.yaml` 或 `<path>/plugin.yaml`，通过 `spec/plugin-manifest.schema.json`
   （二进制内嵌同一份 schema，`-schema PATH` 可覆盖；install 会打印 schema 来源）；
3. `compatibility.core` 包含当前 Core 版本，protocol/kind 合法；
4. 解析并固定精确 Release tag；单插件 `/releases/latest`，monorepo 按 `<path>/v` 选择并核对
   manifest version；
5. 选择资产并校验 sha256（可选 attestation）；catalog 的 `asset` 只是首选资产名；
6. 权限披露展示并确认；
7. 写 `plugins.lock`，记录精确 tag 与 monorepo `pluginPath`。

## 5. 当前范围

- CLI 提供 `search` / `inspect` / `install` / `enable` / `disable` / `update` / `remove` / `host`。
- `search` 对每个 topic 命中做受限 catalog 探测；单个 catalog 失败保留 topic 回退并输出安全
  warning，不使整个搜索失败；结果总数与网络调用都有上限。
- `host` 只支持 Driver 与 Application 进程；Connector 可安装和披露，但启动 fail-closed，直到
  Connector 运行时落地。
- GitHub 搜索走 GitHub REST（topic `cloudpath-plugin`）；topic 与 catalog 都不是信任证明。
