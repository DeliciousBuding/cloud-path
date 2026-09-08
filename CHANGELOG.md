# Changelog

本文件记录**发布工程**层面的变更：版本、发布产物、命名规范与部署物料。
产品能力的实现事实以 [docs/](docs/) 下各文档与代码为准；本文件不复述功能清单，
也不把尚未合并/尚未验证的目标态写成已发布内容。

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
- **`linux/arm64` 是硬性要求**：生产主机为原生 arm64 且无模拟回退，`--verify-only`
  会因缺少该产物直接失败。
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
- `deploy/nginx/cloudpath.vectorcontrol.tech.conf`：可安装的公网站点示例（443 + WSS 升级头 +
  长 `proxy_read_timeout` + CDN 真实 IP + 请求体上限 + gzip），不覆盖应用自身安全头。
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

主题：**插件实例重配置与身份收口（P1）**。真机升级暴露「PATCH 返回成功、applied revision 前进，旧进程却继续运行」；
排查发现另有三处同源缺陷（实例身份在某个平面上被部分折叠），在原质量范围内一并收口，未新增架构。

### 契约变更（读面不再对用户说谎）

- `GET /api/stats`：`auth_enabled: bool` → `auth_mode ∈ account|token|open`，报告 server **实际执行**的鉴权形态。
  此前账号模式被系统页显示为「未启用（本机模式）」。字段在 [docs/api.md](docs/api.md) §2.2，档位定义见 §1。
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

- `.gitattributes` 补 `*.yml text eol=lf`（此前只写 `*.yaml`，12 个 `.yml` 落在规则外），两批共 11 个文件行尾归一化，
  消除跨 worktree 的幻影 diff；零语义改动由 `yaml.safe_load` 前后比对证明。
- 消除 `TestOverviewTenantIsolation` 约 5% 的闪断（tenant-b 事件断言前补一次等待）；修复后 120 连跑 0 失败。
- 18 二进制 + `checksums.txt` 与多架构 GHCR 镜像已发布；`linux/arm64` 镜像来源提交与 tag 一致。
- 发布源码同树 CI 13/13（含 Linux race、Windows、六平台构建与产物校验），Go 27 包全绿、`go vet` / `gofmt` 干净，
  WebUI 36 文件 676/676 与 `tsc` / 构建通过，契约漂移（44 个同名类型）/ 公开审计 / 链接三门禁 PASS。

### 验证边界（不外推）

- 重配置、重启后配置恢复、退出失败与跨租户隔离已有**真实子进程**回归，并以变异测试证明回归有效
  （`instanceKey` 退回裸 id、去掉 edge 过滤，均先红后绿）。
- 「不靠 Edge 重启」的**真机热更新后验在部署之后**进行，本节不代表其已通过；此前用受控 Edge 重启恢复版本一致，
  不作为补丁验收证据。
- 无第二种硬件，设备侧证据仍来自既有 STC-B 单链路；未接步进电机实物，不宣称电机物理动作完成。
- 生产浏览器 UI 验收需登录后进行，本轮只有测试、`tsc`/构建与真实 API/硬件证据，不冒充新 UI 已在浏览器验收。

## v0.2.15 — 应用输入与操作契约

- 新增可选实体观测上报、显式应用绑定及手动作业声明/HTTP调用，详见
  [设计契约](docs/design.md#应用输入与操作契约) 与 [应用操作 API](docs/api.md#551-应用手动操作operator)。
- 管理台按运行中插件声明生成操作表单，区分受理结果与设备回执；旧 raw 状态与自动任务保持兼容。
- 依赖观测事件或 manual_only 语义的应用要求 Core >=0.2.15，不可安装到会忽略该标记的旧宿主。
- 发布资产命名、六平台矩阵与校验和约定不变。测试/CI与部署/真板验收分别出证据，本节不代表现场验收通过。

## 版本状态

| 版本 | 状态 | 说明 |
|---|---|---|
| `v0.1.0` | 已发布（2026-09-04） | 首个公开版本；release workflow 自动产出 18 二进制 + checksums.txt |
| `v0.2.3` | 已发布（2026-09-05） | AppHost 接线完成（Server 侧 Application Plugin Host + app_domain_records schema v9）；外部 Driver capability 迟到重报；Scheduled Compartment 迁移通用 Capability；真板七阶段 E2E 全绿（Reference Rig） |
| `v0.2.4` | 已发布（2026-09-05） | Edge applier 修复：实例状态文件收敛后才持久化——失败 apply 不再把不可满足的版本写进 replay 状态（2026-09-05 生产 Edge 无法自举事故的根因），重启照常回放最后可满足配置 |
| `v0.2.5` | 已发布（2026-09-05） | appruntime 修复：domain-record effect 去重键内容化——upsert 恢复真语义（此前同一记录的后续更新全被幂等去重吞掉，真板实测 reminder_state 恒空）；真板 E2E 增加提醒命令失败路径（freq=9→固件 badarg→RequestCompleted(failed)→应用落痕）5/5 fault 案全绿 |
| `v0.2.6` | 已发布（2026-09-05） | AppHost 修复×2（jp1 生产实测 box-prod failed 90 分钟）：共享插件进程停一个实例不再连带杀兄弟（新增 StopInstanceStreamOnly，Shutdown RPC 只留给最后实例）；reconcile 自愈——desired 未变但实际态失活的实例按 stop+start 重建会话 |
| `v0.2.7` | 已发布（2026-09-05） | D1 Application Data Plane：`/api/plugin-instances/{id}/records|bindings|jobs` 通用读面（分页/过滤/租户隔离）+ WS `domain_record` 实时投影（created/updated）；契约 docs/api.md §5.5 |
| `v0.2.8` | 已发布（2026-09-05） | D2 Durable Scheduler：schema v10 `scheduled_jobs` + 5 字段 cron 解析器 + claim-then-dispatch 调度循环（重启零重复、missed-run policy skip/run_once、停机不漂移节奏）；`schedule_job`/`cancel_job` 从簿记变真 primitive |
| `v0.2.9` | 已发布（2026-09-05） | appruntime 修复：事件流开启即派发初始 `InstanceLifecycle` 事件——RunJob/RunRequest 早于任何设备事件到达时，应用侧 effect writer 尚未注册，其产生的 effect 此前被静默丢弃（button-indicator bootstrap 实测抓出） |
| `v0.2.10` | 已发布（2026-09-05） | appruntime 修复：实例停机/启动失败即移除记录——此前 failed 记录永远占位，AppHost reconcile 的进程内自愈每轮撞 `ErrInstanceExists`（D3 真板实测：自愈实际只在 server 整体重启时生效）；Stop→Start 重建成为受测契约 |
| `v0.2.11` | 已发布（2026-09-05） | 绑定确定性修复（D3 真板实测根因）：Edge descriptor 实体按 EntityID 排序、Server appCandidates 按 (device, entity) 排序——此前 map 随机迭代让 Binder first-match 每次绑到不同实体（button-indicator 重启后绑到 key2，用户按 K1 全部静默丢弃），且 descriptor 指纹每拍抖动导致整份 descriptor 每 poll 周期重发；AppHost 事件路由增加 dispatch/unrouted 观测日志（静默丢弃盲区） |
| `v0.2.12` | 已发布（2026-09-06） | Application Plane Web 三读面与实时/重连恢复；身份及只读权限隔离；中心服务宿主展示与概览活跃统计修复；容器 AppHost 持久化路径统一。发布源码经 500 项前端测试、Go tests/vet、CI 和真实浏览器隔离联调验证；本次浏览器设备输入为合成设备，不代表新增真板验收 |
| `v0.2.13` | 已发布（2026-09-07） | Driver 动作 RPC 透传可选标题、说明与危险确认元数据；`oneOf`/`anyOf`/`allOf` 三态校验；Host 完成认证连接计数后再发布 `ready`。18 二进制 + checksums.txt 与容器镜像已发布，linux/arm64 镜像来源提交与 tag 一致。发布源码同树 CI 13/13（含 Linux race、Windows、六平台），WebUI 36 文件 658/658 与构建通过；浏览器验证限于既有隔离联调，不代表新增真板验收或 Edge/Driver 更新后验完成 |
| `v0.2.14` | 已发布（2026-09-07） | 插件实例重配置与身份收口（P1）：既有实例变更不再被静默忽略、重启后在 `HEALTHY` 前恢复已应用配置、退出失败不冒充成功、实例身份统一带租户键并如实标记 drift；另修 `auth_mode`、未路由 404、首装会话三处会说谎的读面。详见 [§v0.2.14](#v0214--2026-09-07)。**验证边界**：真机「不靠 Edge 重启」的热更新后验在部署之后进行，本行不代表其已通过 |
| `dev` | 本地 | `task build` / `task build:matrix` 的未打标产物（`git describe` 兜底） |
