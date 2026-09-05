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

## 版本状态

| 版本 | 状态 | 说明 |
|---|---|---|
| `v0.1.0` | 已发布（2026-09-04） | 首个公开版本；release workflow 自动产出 18 二进制 + checksums.txt |
| `v0.2.3` | 已发布（2026-09-05） | AppHost 接线完成（Server 侧 Application Plugin Host + app_domain_records schema v9）；外部 Driver capability 迟到重报；Scheduled Compartment 迁移通用 Capability；真板七阶段 E2E 全绿（Reference Rig） |
| `v0.2.4` | 已发布（2026-09-05） | Edge applier 修复：实例状态文件收敛后才持久化——失败 apply 不再把不可满足的版本写进 replay 状态（2026-09-05 生产 Edge 无法自举事故的根因），重启照常回放最后可满足配置 |
| `v0.2.5` | 已发布（2026-09-05） | appruntime 修复：domain-record effect 去重键内容化——upsert 恢复真语义（此前同一记录的后续更新全被幂等去重吞掉，真板实测 reminder_state 恒空）；真板 E2E 增加提醒命令失败路径（freq=9→固件 badarg→RequestCompleted(failed)→应用落痕）5/5 fault 案全绿 |
| `v0.2.6` | 已发布（2026-09-05） | AppHost 修复×2（jp1 生产实测 box-prod failed 90 分钟）：共享插件进程停一个实例不再连带杀兄弟（新增 StopInstanceStreamOnly，Shutdown RPC 只留给最后实例）；reconcile 自愈——desired 未变但实际态失活的实例按 stop+start 重建会话 |
| `v0.2.7` | 已发布（2026-09-05） | D1 Application Data Plane：`/api/plugin-instances/{id}/records|bindings|jobs` 通用读面（分页/过滤/租户隔离）+ WS `domain_record` 实时投影（created/updated）；契约 docs/api.md §5.5 |
| `dev` | 本地 | `task build` / `task build:matrix` 的未打标产物（`git describe` 兜底） |
