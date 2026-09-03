# 仓库组合、命名与公开边界

最后更新：2026-09-03

> 本文定义 CloudPath 的公开仓库组合、命名、插件孵化/拆仓策略和公开边界。插件发现与信任链见
> [github-ecosystem.md](github-ecosystem.md)，插件运行契约见 [plugin-system.md](plugin-system.md)。

## 1. 名称层级

| 对象 | 规范 |
|---|---|
| 产品/品牌 | **CloudPath**（中文：云径） |
| 核心 GitHub 仓库 | `DeliciousBuding/cloud-path` |
| Go module | `github.com/DeliciousBuding/cloud-path` |
| CLI/二进制 | `cloudpath`、`cloudpath-server`、`cloudpath-edge` |
| 机器命名空间 | `cloudpath.dev/*` |
| GitHub 发现 Topic | `cloudpath-plugin` |

规则：产品文案写 `CloudPath`；仓库 slug 用 `cloud-path-*`；命令、包名和协议字段用 `cloudpath`，不插连字符。

## 2. 官方仓库组合

| 仓库 | 职责 | 创建时机 |
|---|---|---|
| `cloud-path` | Core、Server、Edge、WebUI、公共 API/Schema、Go SDK、测试 harness | 当前主仓库 |
| `cloud-path-registry` | 精选插件索引、发布者策略、digest/attestation 元数据；不存二进制 | A7 稳定后 |
| `cloud-path-driver-stcb` | STC-B Driver Plugin；板级容错、串口协议、Capability 映射 | A5 拆仓 |
| `cloud-path-app-scheduled-compartment` | 完全硬件无关的参考 Application Plugin | A6 可运行后 |
| `cloud-path-plugin-template-go` | 官方 Go 插件模板、CI、Release、conformance 示例 | 第二个外部插件前 |

暂不创建 `cloud-path-docs`、`cloud-path-sdk-*` 等空仓库。文档、Go SDK、Schema、harness 在接口稳定前留在核心仓库，避免跨仓同步成本。

## 3. 插件仓库命名

官方插件：

```text
cloud-path-driver-<hardware-or-protocol>
cloud-path-app-<domain-neutral-application>
cloud-path-connector-<external-system>
```

示例：

```text
cloud-path-driver-stcb
cloud-path-driver-modbus
cloud-path-app-scheduled-compartment
cloud-path-connector-home-assistant
```

社区插件不强制仓库前缀，但必须：

1. 添加 Topic `cloudpath-plugin`；
2. 仓库根有 `plugin.yaml`；
3. plugin id 使用发布者命名空间；
4. Release 资产有摘要，安装前通过 Manifest/兼容性/digest 验证。

## 4. 核心仓库边界

核心仓库拥有：

- Device/Entity/Capability/Observation/Event/Command 通用模型；
- Edge、Server、WebUI、认证/租户、Plugin Host 与 Registry 客户端；
- versioned 协议、Schema、SDK、conformance harness；
- 尚处孵化期的 reference plugin（放 `examples/`，不作为永久运行形态）。

核心仓库不拥有：

- 厂商帧格式、串口时序、板级缺陷补偿；
- 行业专用流程、字段和业务页面；
- 第三方系统私有 API；
- 云服务内部拓扑、运营控制面和生产凭据。

依赖只能朝一个方向：

```text
Plugin → public SDK/Schema/Protocol → Core contracts
Core ─X→ 某个具体 Driver/Application
Application ─X→ Driver ID / 端口 / 厂商字段
```

## 5. 插件孵化与拆仓门

插件先在 `examples/<slug>/` 孵化；同时满足下列条件才拆成独立仓库：

1. Manifest/Descriptor 稳定，机器 ID 不再频繁变化；
2. 通过对应 conformance suite；
3. Core 移除该插件后仍可 build/test/start；
4. 有独立版本、Release、checksums 和升级兼容说明；
5. 不依赖核心 `internal/` 包，只依赖 public SDK/Schema；
6. README 能让陌生人独立安装、配置、验证和卸载。

拆仓使用 `git filter-repo`/subtree 保留相关历史；核心仓库删除编译期 import，仅保留安装示例和 Registry 指针。

## 6. 公开展示矩阵

| 内容 | 公开仓库 | 私有层/其他家 |
|---|---|---|
| Core/Edge/Server/WebUI、通用 SDK/Schema/协议 | ✅ MIT |
| 通用 reference Driver/Application | ✅，去行业/个人语义 |
| 无凭据的 Docker/反代/配置示例 | ✅ |
| 架构、公开 API、安全模型、威胁边界 | ✅ |
| 设备清单、串口号、真实 edge/site id、板测日志 | ❌ `.local/` |
| 路线图、未发布计划、内部验收证据 | ❌ `.local/` 或私有文档家 |
| 课程 BSP/课件/模板、厂商不可再分发 SDK | ❌ 不进任何公开仓库 |
| token/password/cookie/私钥/生产 URL/IP/内部昵称 | ❌ secret store；仓库只写 key 名和 example |
| 云服务运营控制面、计费、内部告警与拓扑 | 默认 ❌ 闭源增值 |

公开截图必须人工复查：账号、真实设备名、主机/IP、串口、事件正文、浏览器书签和桌面路径均不得出现。

## 7. 版本与发布

- 核心、插件、Manifest、RPC、Capability 分别版本化；不能把它们绑定成同一版本号。
- 核心使用 semver tag；官方插件独立 semver/tag/Release。
- Capability `@1` 发布后不原地改变语义；破坏性变化发布 `@2`。
- patch 版本不得静默扩大权限；权限新增需要显式确认。
- Registry 不记录 `latest` 作为可执行事实，必须固定版本和 digest。

## 8. Public 仓库发布门

首次创建或每次发布前至少通过：

1. `go build ./...`、`go vet ./...`、`go test ./... -count=1`；
2. WebUI typecheck/test/build；
3. Manifest/Schema/conformance 测试；
4. secret、个人信息、内部路径/主机与不可再分发资产扫描；
5. README/Docs 相对链接检查；
6. Release 资产摘要与来源验证；
7. 截图人工隐私复查。