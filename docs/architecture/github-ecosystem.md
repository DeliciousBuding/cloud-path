# GitHub 插件生态与信任链

最后更新：2026-09-03

> 本文定义 CloudPath 插件如何通过 GitHub 被发现、验证与分发。配套决策见
> [adr/0002-github-plugin-discovery.md](adr/0002-github-plugin-discovery.md)。

## 1. 命名与发现契约

| 维度 | 约定 |
|---|---|
| 插件仓库前缀 | `cloud-path-driver-*` / `cloud-path-app-*` / `cloud-path-connector-*` |
| 发现 Topic | `cloudpath-plugin` |
| 根 Manifest | 仓库根 `plugin.yaml`（结构见 [plugin-system.md](plugin-system.md)） |
| 分发载体 | GitHub Release 资产（二进制/归档 + checksums） |
| 规范命名空间 | `cloudpath.dev/*`；第三方用发布者命名空间（如 `io.github.<owner>/capability/...@1`） |

Topic `cloudpath-plugin` 只是**候选集合**，不是信任证明：任何仓库都能给自己打这个 topic。

## 2. 双通道发现

1. **开放通道**：`gh search repos --topic cloudpath-plugin`，`cloudpath plugin search` 直接消费。
2. **精选通道**：官方 Registry 维护已审查插件与发布者策略，`cloudpath plugin inspect` 优先展示。

Registry 记录：plugin id、版本、来源仓库、资产 digest、`verifiedPublisher`、兼容范围、协议版本。
锁文件 `plugins.lock` 固定精确版本、资产 digest、来源仓库与验证结果。

## 3. 验证流程（发现之后、执行之前）

对候选仓库按顺序验证，任何一步失败即拒绝安装：

1. 根 Manifest 存在且 schema 合法；
2. 声明的兼容范围包含当前 Core 版本；
3. 存在 GitHub Release 与资产摘要；
4. 摘要匹配（sha256），可选 `gh attestation verify` / 构建证明；
5. 权限披露在安装时展示并确认；
6. `plugins.lock` 记录版本、digest、来源与验证结果。

## 4. 信任分级

| 级别 | 含义 |
|---|---|
| 未审查 | topic 命中但 Registry 未收录；安装时明确标注 |
| 已审查 | Registry 收录，发布者/摘要已验证 |
| 官方 | 维护者发布，`verifiedPublisher` 匹配 |

## 5. 参考先例

- `home-assistant-custom-component`：topic 发现 + manifest 声明，社区量大、质量靠维护者把关。
- `grafana-plugin`：官方 Registry + 签名，插件市场闭环。
- `krew`（k8s 插件）：索引仓库 + manifest + checksums，CLI 安装。
- `gh-extension`：GitHub 仓库 + `gh extension install`。

CloudPath 取折中：**topic 发现 + 可选 Registry 精选 + 强制摘要校验**——开放、低门槛，但不以 topic 为信任。

## 6. 非目标

- v1 不做中心化付费插件商店。
- v1 不承诺不受信任插件的强 OS 沙箱；先做进程隔离 + 权限披露。