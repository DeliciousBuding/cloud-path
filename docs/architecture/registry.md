# Plugin Registry 与 CLI

最后更新：2026-09-09

> Registry 索引、CLI 与供应链验证的实现规则。发现通道与信任链见
> [github-ecosystem.md](github-ecosystem.md)。

## 1. Registry Manifest（索引项）

Registry 记录已审查插件，字段固定：

| 字段 | 类型 | 说明 |
|---|---|---|
| `id` | string | 插件 ID（命名空间，如 `io.github.<owner>.<name>`） |
| `version` | string | semver |
| `kind` | string | Driver / Application / Connector |
| `source` | string | 仓库 URL（如 `https://github.com/owner/repo`） |
| `digest` | string | 资产 sha256 |
| `verifiedPublisher` | string | 已验证发布者 |
| `protocol` | int | RPC 协议版本 |
| `compatibility` | string | 兼容 Core 版本范围 |

锁文件 `plugins.lock` 记录：`id`、精确 `version`、资产 `digest`、`source`、验证结果。

## 2. CLI 子命令面（`cloudpath plugin`）

```
cloudpath plugin search <query>      # GitHub topic cloudpath-plugin 搜索
cloudpath plugin inspect <id|url>    # 读根 plugin.yaml + Release/摘要，输出验证结论
cloudpath plugin install <id|url>    # 下载 → 校验 digest → 落 plugins.d/ + 写 plugins.lock
# 匿名 GitHub API 限额 60 次/小时：设置 GITHUB_TOKEN（或 GH_TOKEN）走认证额度，
# 否则 discover/install 可能返回 ERR_RATE_LIMITED。令牌只从环境读取，不写入 lock/日志。
cloudpath plugin enable <id>         # 写入启用期望态
cloudpath plugin disable <id>        # 写入停用期望态
cloudpath plugin update <id>         # 更新固定版本/摘要与 desired（迁移计划仍属目标态）
cloudpath plugin remove <id>         # 卸载（默认保留数据，purge 另设）
```

## 3. 验证链（install 前强制，任一失败即拒绝）

1. 根 `plugin.yaml` 通过 `spec/plugin-manifest.schema.json` 校验（发布二进制内嵌同一份 schema，
   `-schema PATH` 可覆盖；`install` 输出会打印 schema 来源 `file:<path>` 或 `embedded`）；
2. `compatibility.core` 包含当前 Core 版本；
3. 存在 GitHub Release 与资产摘要；
4. sha256 匹配（可选 `gh attestation verify`）；
5. 权限披露展示并确认；
6. 写 `plugins.lock`。

## 4. 当前范围

- CLI 提供 `search` / `inspect` / `install` / `enable` / `disable` / `update` / `remove` / `host`；安装与更新执行 Manifest、兼容范围、Release 资产和摘要校验。
- `host` 只支持 Driver 与 Application 进程；Connector 可安装和披露，但启动会 fail-closed，直到 Connector 运行时落地。
- GitHub 搜索走 `gh` 或 GitHub REST（topic `cloudpath-plugin`）；不把 topic 当信任。
