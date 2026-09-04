# Plugin Registry 与 CLI 契约（A7）

最后更新：2026-09-03

> A7（Registry + CLI + 供应链）的实现契约。发现通道与信任链见 `docs/architecture/github-ecosystem.md`。

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
cloudpath plugin enable <id>         # 启动实例
cloudpath plugin disable <id>        # 停用实例
cloudpath plugin update <id>         # 升级（走 PlanMigration）
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

## 4. 首批范围

- 实现 `search` / `inspect` / `install`（本地安装 + digest 校验）为硬目标；`enable/disable/update/remove` 先落命令与错误码，实例运行时接 A4 Plugin Host。
- GitHub 搜索走 `gh` 或 GitHub REST（topic `cloudpath-plugin`）；不把 topic 当信任。