# Changelog

本文件记录发布与工程变更：版本、发布产物、命名规范与部署物料。
产品能力的实现事实以 [docs/](docs/) 下各文档与代码为准；本文件不复述功能清单，
也不把尚未发布或尚未验证的目标态写成已发布内容。

格式参考 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)，版本遵循
[SemVer](https://semver.org/lang/zh-CN/)。仓库 tag 形状为 `v*`，由
[.github/workflows/release.yml](.github/workflows/release.yml) 触发发布。

## 发布产物与命名规范（所有版本通用）

产物由 [scripts/build_matrix.py](scripts/build_matrix.py) 构建，命名固定为：

```text
cloudpath-server_<version>_<os>_<arch>[.exe]   # 中心服务（内嵌 WebUI，构建标签 embed_ui）
cloudpath-edge_<version>_<os>_<arch>[.exe]     # 边缘客户端（接入设备的那台电脑上运行）
cloudpath_<version>_<os>_<arch>[.exe]          # 插件管理 CLI
checksums.txt                                  # 全部资产的 sha256（sha256sum 两空格格式）
```

- `<version>` 与 git tag 一致（例如 `v0.1.0`）；版本串会被安全化，不能携带路径分隔符或 `..`。
- `<os>` ∈ `linux` | `windows` | `darwin`；`<arch>` ∈ `amd64` | `arm64`。六个平台组合全部产出，
  共 18 个二进制 + 1 个 `checksums.txt`。
- **`linux/arm64` 是硬性要求**：发布矩阵必须产出该目标，`--verify-only` 会因缺少该产物直接失败。
- 全部产物 `CGO_ENABLED=0`、`-trimpath`、`-ldflags "-s -w -X main.version=<version>"`。
- 每个产物构建后都会经 [scripts/assert_arch.py](scripts/assert_arch.py) 断言：容器头
  （ELF/PE/Mach-O）与 `go version -m` 构建设置双源交叉校验，server 额外断言 `embed_ui`
  标签存在；不一致即非零退出。

校验方式（使用者侧）：

```bash
sha256sum -c checksums.txt --ignore-missing          # Linux
shasum -a 256 -c checksums.txt --ignore-missing      # macOS
certutil -hashfile <文件> SHA256                      # Windows（逐项对照）
```

## v0.2.26 — 2026-09-09

- 应用中心：插件页面按业务字段渲染，支持字段标签、单位、精度、格式化、值映射与空值隐藏；指标、记录和时间线不再默认展示机器字段或原始 JSON。
- 应用中心：页面、分区、空态和导航文案改为人话，运行中的业务应用按独立路由进入，原始数据收进“更多信息”。
- 插件 UI：Manifest、Go 校验、TypeScript 类型与 schema 同步扩展，旧插件声明保持兼容。
- 稳定性：修复插件一致性用例在 Linux race 下的异步等待竞态。

## v0.2.25 — 2026-09-09

- 概览：实时故障与历史失败操作分组展示；实时状态恢复后自动消失，失败操作可标记已处理，原始记录保留。
- 运行记录：新增处理状态筛选、单条/批量标记已处理，并支持从概览直接跳到未处理失败项。
- 后端：schema v13 增加 `commands.handled_at`，新增处理状态查询与标记接口，原始记录不删除。

## v0.2.24 — 2026-09-09

### 发布工程

- 修正 `scripts/assert_arch.py` 索引中的 CRLF 行尾：Linux Release checkout 不再因 `.gitattributes` 的 LF 归一化而让 Go 把源码树判定为 dirty。
- Release 构建前新增干净工作树门禁，阻止 `+dirty` 二进制再次发布。
- 取代 `v0.2.23` 的 CI 二进制；该批资产仅因上述行尾问题被误标为 `+dirty`，运行时代码相同。

## v0.2.23 — 2026-09-09

- WebUI：建立 `zh-CN` / `en-US` 国际化，语言选择持久化；导航、页面、表单、错误态和插件生命周期文案全部走 i18n，硬编码中文清零。
- 插件契约：Manifest、Descriptor、Capability 与插件 UI 标题支持可选 `i18n` map，旧 `title` / `name` / `description` 保持兼容；后端错误改用稳定 `code` + `params`。
- 文案：面向普通用户重写状态、错误和操作提示，隐藏契约、实体、运行时、revision 等工程术语；设计 token 与 i18n 门禁同时纳入 CI。

- 插件 UI：Manifest 新增声明式 `ui` 契约与真实 schema 校验，支持 Application 导航/页面、Driver 设备扩展、受控 custom section；公开 API 与插件目录完成白名单投影，旧 manifest 无 `ui` 时完全兼容。
- 插件 UI：新增 `GET /api/plugin-ui/assets/{pluginID}/{version}/{path:.*}`，只服务已声明 custom entry 的本地 AppHost 插件 `ui/` 子树；账号模式强制认证，open 模式按空租户可见性校验，路径/symlink/MIME/大小均 fail-closed。
- 插件目录：同一 `plugin_id` 先按 semver 取最高版本，同版本 Edge 事实优先，避免旧 Edge 安装遮蔽新版 AppHost Application。
- 工程：全仓静态检查清零；`docs/api.md` 与 `docs/architecture/plugin-ui.md` 同步 UI 与资产端点契约。

## v0.2.22 — 2026-09-09

### 行为修复

- 修复设备详情页点击「概览」后立即回到「设备操作」的问题：概览现在显式写入 `tab=overview`，不再依赖“缺少参数”表达状态。

## v0.2.21 — 2026-09-09

- WebUI：收口窄屏导航与可访问名称，统一状态/空态/错误态，并重整运行记录、命令表单和页面视觉层级。
- WebUI：设备或网关离线时禁用操作与历史重试并说明原因；应用结果只显示人话摘要，原始 JSON 按需展开；补充移动端触控热区和准确的 demo 命令计数标签。
- 服务端：统一鉴权/RBAC 前置拒绝的稳定错误体；AppHost 实例启动后立即投影 observed，不再等待固定 30 秒轮询。
- 插件边界：强化外部 Driver 命令白名单、Capability/实例租户隔离与控制面写面的 fail-closed 行为。

## v0.1.0 — 2026-09-04

### 新增（发布工程）

- `scripts/assert_arch.py`：产物 OS/架构/构建标签断言门禁，硬失败，含 `--self-test`。
- `scripts/build_matrix.py`：全平台发布矩阵构建、产物命名、sha256 `checksums.txt`、
  `--verify-only` 复验模式（断言 + 校验和 + 强制 linux/arm64 server 存在）。
- `scripts/check_workflows.py`：GitHub Actions workflow 的 stdlib-only 结构自检
  （可用 PyYAML 时做真实解析，否则退化为结构扫描），含 `--self-test`。
- `.github/workflows/release.yml`：`v*` tag 触发的多平台发布 + checksums + GitHub Release。
- `.github/workflows/ci.yml`：ubuntu + windows 双平台矩阵，Linux 额外 `-race`，
  前端 frozen install / typecheck / test / build，公开边界与链接门禁，产物架构断言。
- `deploy/systemd/cloudpath-server.service`：非 root 专用账号、加固沙箱、资源上限、
  `ReadWritePaths` 持久化目录、`EnvironmentFile=-` 机密注入、`SystemCallArchitectures=native`。
- `deploy/systemd/cloudpath-server.env.example`：环境变量模板，**只有变量名没有值**。
- nginx 公网站点示例：443 + WSS 升级头 + 长 `proxy_read_timeout` + CDN 真实 IP +
  请求体上限 + gzip，不覆盖应用自身安全头。
- `deploy/README.md`：原生二进制 + systemd + nginx 的逐条落地 SOP（架构断言 → 传输 →
  建用户/目录 → 装 unit → 首装账号 → nginx → 健康检查 → 备份 → 升级 → 回滚 → 排查 → 清单）。
- `deploy/edge/README.md`：客户端分发指引（各平台一句话安装、`edge.yaml` 逐字段填写、
  开机自启、常见问题、卸载）。
- `deploy/split/`：把参考 Application 插件拆成独立 `go.mod` 仓库的可复现脚本与说明。
- 根 `README.md` 重写为“全新机器视角”的入口文档（架构、插件类型、本地快速开始、
  公网部署指针、多台电脑接入同一 Server、配置参考、安全边界、开发命令、当前能力与未实现项）。
- `Taskfile.yml` 新增 `build:linux-arm64`、`build:matrix`、`verify:arch`、
  `release:artifacts`、`selftest:scripts`。

### 变更

- `deploy/nginx.conf`：WebSocket 位置拆出 `/ws` 与 `/ws/edge`，`proxy_read_timeout`
  从 60s 提升到 3600s（60s 会掐断健康的长连接），升级头改用带前缀的 `map` 变量避免与同机
  其它站点的 `map` 冲突，并指向可直接安装的站点示例。
- `.gitignore`：忽略 Python 字节码缓存与拆仓脚本的临时输出。

## v0.2.14 — 2026-09-07

主题：**插件实例重配置与身份收口**。真机升级暴露「PATCH 返回成功、applied revision 前进，旧进程却继续运行」；
排查发现另有三处同源缺陷（实例身份在某个平面上被部分折叠），在原质量范围内一并收口，未新增架构。

### 接口变更（读面不再误导用户）

- `GET /api/stats`：`auth_enabled: bool` → `auth_mode ∈ account|token|open`，报告 server **实际执行**的鉴权形态。
  此前账号模式被设置页显示为「未启用（本机模式）」。字段在 [docs/api.md](docs/api.md) §2.2，档位定义见 §1。
- 路由表之外的 `/api/*`（含 `/api/auth/*` 下不存在的子路径）与缺失的 `/assets/*` 回 `404`，不再被 SPA 兜底成
  `200 + index.html`——此前一个不存在的 `DELETE` 端点也答 200。见 [docs/api.md](docs/api.md) §2.2。
- `PluginInstanceView.drift`：启用实例双方版本已知且不一致时如实标记，列表、详情与写响应同形，
  不被错误的 applied ACK 掩盖。见 [docs/api.md](docs/api.md) §5.3。
- 首装建号即交接会话；会话没有落地时不再谎报「用户名或密码错误」。

### 行为修复

- 既有实例的 version / pluginID / config / isolation 变更走可校验的重配置入口，不再被 `CreateInstance` 的
  `ErrInstanceExists` 路径静默忽略；幂等重放与保留键语义不变。
- Supervisor 自动重启后经 `prepareSession` 在状态转 `HEALTHY` **之前**恢复全部已应用配置；
  恢复失败消耗既有重启预算，不让未配置的新进程冒充成功。
- 插件退出失败不再被当成功；快照收敛可重试，失败期间不推进 applied revision。
- `DriverClient` / `ApplicationClient` 遇歧义即以 `ErrAmbiguousInstance` 失败关闭，另补按 `(tenant, instance)` 的
  实例级精确寻址；map 遍历顺序不再是事实上的路由规则。
- AppHost 运行记录、开窗去重键与 appruntime 实例表统一按 `(tenant, instance)` 建键，与 store 主键一致——
  两租户同名实例不再互相覆盖并静默饿死。`reconcile` 抽出 `serverHostedRows` 作为进程面与协议面共用的唯一 edge 过滤点。
- 设置向导第 3 步补「全鉴权会掐断已接入边缘」的可执行恢复步骤（仅在探到已有边缘/设备时出现）；
  事件载荷无增量信息时不再给「展开原始载荷」；`AppHostEdgeID` 取代生产代码里的裸 `"server"` 字面量。
- 实现语义与故障恢复边界见 [docs/architecture/plugin-system.md](docs/architecture/plugin-system.md)
  （实例身份／实例重配置与会话恢复／客户端寻址）与
  [docs/architecture/control-plane-sync.md](docs/architecture/control-plane-sync.md) §8。

### 发布工程

- `.gitattributes` 补 `*.yml text eol=lf`（此前只写 `*.yaml`），并统一 YAML 行尾，避免同一文件在不同检出环境中产生无意义 diff；零语义改动由 `yaml.safe_load` 前后比对证明。
- 消除 `TestOverviewTenantIsolation` 的偶发闪断（tenant-b 事件断言前补一次等待），恢复稳定回归。
- 18 二进制 + `checksums.txt` 与多架构 GHCR 镜像已发布；`linux/arm64` 镜像来源提交与 tag 一致。
- 发布源码在同一源码树的 CI 覆盖 Linux race、Windows 与六平台构建/产物校验；Go tests、`go vet` / `gofmt`、
  WebUI typecheck/test/build、类型一致性门禁、公开审计与链接检查均通过。

### 验证范围

- 重配置、重启后配置恢复、退出失败与跨租户隔离已有**真实子进程**回归，并以变异测试证明回归有效
  （`instanceKey` 退回裸 id、去掉 edge 过滤，均先红后绿）。
- 「不靠 Edge 重启」的**真机热更新验证在部署之后**进行，本节不代表其已通过；此前用受控 Edge 重启恢复版本一致，
  不作为补丁验证证据。
- 无第二种硬件，设备侧证据仍来自既有 STC-B 单链路；未接步进电机实物，不宣称电机物理动作完成。
- 生产浏览器 UI 验证需登录后进行；本版本只有测试、`tsc`/构建与真实 API/硬件证据，不声称新 UI 已在浏览器验证。

## v0.2.15 — 应用输入与操作接口

- 新增可选实体观测上报、显式应用绑定及手动作业声明/HTTP调用，详见
  [设计](docs/design.md#应用输入与操作契约) 与 [应用操作 API](docs/api.md#551-应用手动操作operator)。
- 管理台按运行中插件声明生成操作表单，区分受理结果与设备回执；旧 raw 状态与自动任务保持兼容。
- 依赖观测事件或 manual_only 语义的应用要求 Core >=0.2.15，不可安装到会忽略该标记的旧宿主。
- 发布资产命名、六平台矩阵与校验和约定不变。测试/CI 与部署/真板验证分别出证据，本节不代表现场验证通过。

## v0.2.18 — 2026-09-09

### 行为修复

- Edge 断线期间不再丢失待发命令，超时终态会补发通知；Server Ping 等待放宽以容忍 SQLite 抖动，
  同时避免 Edge 与 Server 重复 Ping 形成死锁。
- AppHost 加固命令终态与失败投影，插件拒绝、超时或传输失败不会被包装成成功。
- Capability 级执行器独占绑定改为 fail-closed：共享传感器仍可复用，执行器冲突在启动前拒绝；
  跨租户绑定互不影响，并移除不存在的 capability 字面量。

### 验证范围

- 本版本由 Go 单测、集成回归与涉及插件进程的真实子进程用例覆盖；不代表新增多板真机 E2E，真板证据仍按单板链路与
  `docs/architecture/how-to-build-driver.md` 的硬件验证要求分别出具。

## v0.2.19 — 2026-09-09

### 行为修复

- 删除插件实例时 `purge=true` 在同一事务内清除 observed、领域记录和定时任务；默认删除仍保留实例私有数据，
  审计与安装事实不变。

## v0.2.20 — 2026-09-09

### 新增 / 修复

- Server 将设备最后已知 Descriptor 持久化到 SQLite（schema v11）；重启后即使设备离线，也能水合描述符并
  恢复 Application 绑定。
- 增加 schema v11 迁移、Descriptor 持久化与重启恢复回归测试。

## 版本状态

| 版本 | 状态 | 说明 |
|---|---|---|
| `v0.1.0` | 已发布（2026-09-04） | 首个公开版本；release workflow 自动产出 18 二进制 + checksums.txt |
| `v0.2.3` | 已发布（2026-09-05） | AppHost 接线完成（Server 侧 Application Plugin Host + app_domain_records schema v9）；外部 Driver capability 迟到重报；Scheduled Compartment 迁移通用 Capability；参考设备真板 E2E 已通过（Reference Rig） |
| `v0.2.4` | 已发布（2026-09-05） | Edge applier 修复：实例状态文件收敛后才持久化——失败 apply 不再把不可满足的版本写进 replay 状态（2026-09-05 生产 Edge 无法自举事故的根因），重启照常回放最后可满足配置 |
| `v0.2.5` | 已发布（2026-09-05） | appruntime 修复：domain-record effect 去重键内容化——upsert 恢复真语义（此前同一记录的后续更新全被幂等去重吞掉，真板实测 reminder_state 恒空）；真板 E2E 增加提醒命令失败路径（freq=9→固件 badarg→RequestCompleted(failed)→应用落痕） |
| `v0.2.6` | 已发布（2026-09-05） | AppHost 修复×2（生产环境实测某应用实例持续失败）：共享插件进程停一个实例不再连带杀兄弟（新增 StopInstanceStreamOnly，Shutdown RPC 只留给最后实例）；reconcile 自愈——desired 未变但实际态失活的实例按 stop+start 重建会话 |
| `v0.2.7` | 已发布（2026-09-05） | Application Data Plane：`/api/plugin-instances/{id}/records|bindings|jobs` 通用读面（分页/过滤/租户隔离）+ WS `domain_record` 实时投影（created/updated）；接口见 docs/api.md §5.5 |
| `v0.2.8` | 已发布（2026-09-05） | Durable Scheduler：schema v10 `scheduled_jobs` + 5 字段 cron 解析器 + claim-then-dispatch 调度循环（重启零重复、missed-run policy skip/run_once、停机不漂移节奏）；`schedule_job`/`cancel_job` 从簿记变真 primitive |
| `v0.2.9` | 已发布（2026-09-05） | appruntime 修复：事件流开启即派发初始 `InstanceLifecycle` 事件——RunJob/RunRequest 早于任何设备事件到达时，应用侧 effect writer 尚未注册，其产生的 effect 此前被静默丢弃（button-indicator bootstrap 实测抓出） |
| `v0.2.10` | 已发布（2026-09-05） | appruntime 修复：实例停机/启动失败即移除记录——此前 failed 记录永远占位，AppHost reconcile 的进程内自愈每轮撞 `ErrInstanceExists`（真板实测：自愈实际只在 server 整体重启时生效）；Stop→Start 重建成为受测行为 |
| `v0.2.11` | 已发布（2026-09-05） | 绑定确定性修复（真板实测根因）：Edge descriptor 实体按 EntityID 排序、Server appCandidates 按 (device, entity) 排序——此前 map 随机迭代让 Binder first-match 每次绑到不同实体（button-indicator 重启后绑到 key2，用户按 K1 全部静默丢弃），且 descriptor 指纹每拍抖动导致整份 descriptor 每 poll 周期重发；AppHost 事件路由增加 dispatch/unrouted 观测日志（静默丢弃盲区） |
| `v0.2.12` | 已发布（2026-09-06） | Application Plane Web 三读面与实时/重连恢复；身份及只读权限隔离；中心服务宿主展示与概览活跃统计修复；容器 AppHost 持久化路径统一。发布源码经前端测试、Go tests/vet、CI 和真实浏览器隔离联调验证；本次浏览器设备输入为合成设备，不代表新增真板验证 |
| `v0.2.13` | 已发布（2026-09-07） | Driver 动作 RPC 透传可选标题、说明与危险确认元数据；`oneOf`/`anyOf`/`allOf` 三态校验；Host 完成认证连接计数后再发布 `ready`。18 二进制 + checksums.txt 与容器镜像已发布，linux/arm64 镜像来源提交与 tag 一致。发布源码在同一源码树的 CI（含 Linux race、Windows、六平台）与 WebUI typecheck/test/build 通过；浏览器验证限于既有隔离联调，不代表新增真板验证或 Edge/Driver 更新后验完成 |
| `v0.2.14` | 已发布（2026-09-07） | 插件实例重配置与身份收口：既有实例变更不再被静默忽略、重启后在 `HEALTHY` 前恢复已应用配置、退出失败不伪装成成功、实例身份统一带租户键并如实标记 drift；另修 `auth_mode`、未路由 404、首装会话三处会误导用户的读面。详见 [§v0.2.14](#v0214--2026-09-07)。**验证范围**：真机「不靠 Edge 重启」的热更新验证在部署之后进行，本行不代表其已通过 |
| `v0.2.15` | 已发布（2026-09-08） | 应用输入与操作接口：实体观测上报、显式绑定、手动作业声明/HTTP 调用；详见 [§v0.2.15](#v0215--应用输入与操作接口) |
| `v0.2.18` | 已发布（2026-09-09） | 控制面稳定性：命令断线/超时终态、Ping 死锁与 SQLite 抖动、执行器独占绑定、AppHost 失败投影；详见 [§v0.2.18](#v0218--2026-09-09) |
| `v0.2.19` | 已发布（2026-09-09） | `purge=true` 事务化清除插件实例私有数据；详见 [§v0.2.19](#v0219--2026-09-09) |
| `v0.2.20` | 已发布（2026-09-09） | 持久化设备最后已知 Descriptor（schema v11），离线重启后可水合并恢复 Application 绑定；详见 [§v0.2.20](#v0220--2026-09-09) |
| `v0.2.21` | 已发布（2026-09-09） | WebUI 易用性、响应式布局与文案收口；离线设备操作 fail-closed；应用结果与原始 JSON 分层展示；RBAC 稳定错误体与 AppHost observed 即时投影；全仓静态检查清零 |
| `v0.2.22` | 已发布（2026-09-09） | 修复设备详情「概览」标签点击后被默认「设备操作」弹回 |
| `v0.2.26` | 已发布（2026-09-09） | 应用中心按业务字段呈现插件页面，隐藏机器字段与原始 JSON；插件 UI 契约支持字段格式化、值映射与空值隐藏 |
| `v0.2.25` | 已发布（2026-09-09） | 概览分离实时故障与历史失败；失败操作可标记已处理，原记录保留 |
| `v0.2.24` | 已发布（2026-09-09） | 修复 Release 行尾脏标记并新增干净工作树门禁；取代 v0.2.23 的 `+dirty` CI 资产 |
| `v0.2.23` | 已发布（2026-09-09） | 插件 UI 声明式契约与受控资产端点；WebUI `zh-CN` / `en-US` 完整国际化、稳定错误码和设计 token 门禁 |
| `dev` | 本地 | `task build` / `task build:matrix` 的未打标产物（`git describe` 兜底） |

> 仓库没有 `v0.2.16` / `v0.2.17` tag；`v0.2.18` 覆盖 `v0.2.15` 之后累计的变更。当前 `main` 在
> 最新发布版本为 `v0.2.26`。
