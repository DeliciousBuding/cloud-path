<div align="center">

# CloudPath · 云径

**中心服务 + 网关的物联网控制平台（IoT Control Plane）**：
设备通过网关（Edge）接入，浏览器查看状态并远程控制。平台不识别具体硬件，
新设备由 Driver 插件提供。

[![Go](https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&logoColor=white)](https://go.dev/)
[![React](https://img.shields.io/badge/React-19-61DAFB?logo=react&logoColor=black)](https://react.dev/)
[![SQLite](https://img.shields.io/badge/SQLite-WAL-003B57?logo=sqlite&logoColor=white)](https://sqlite.org/)
[![License](https://img.shields.io/badge/license-MIT-blue)](LICENSE)
[![CI](https://github.com/DeliciousBuding/cloud-path/actions/workflows/ci.yml/badge.svg)](https://github.com/DeliciousBuding/cloud-path/actions)

</div>

---

## 这是什么

CloudPath 把「接入设备 → 查看状态 → 远程控制」做成一套可本地运行、也可部署到公网的系统，
不是某块开发板的专用上位机。

- **中心服务与网关**：中心服务（Server）是 **期望态 / 租户 / 审计的唯一权威**；网关（Edge）是
  **观测态的唯一权威**并保存最后成功 applied 快照。网关自治：断网继续运行，重连仅应用最终快照、不回放中间副作用。
- **设备无关 · 插件驱动**：核心（`internal/*`）不识别任何具体硬件；新设备 = 一个 Driver 插件。
- **中心-网关拓扑**：多网关 + 单中心服务；当前为单 Server 部署，
  多 Server 横向扩展仍属目标态。
- **设备身份** = `(tenant_id, edge_id, device_id)`；线上传输键 `<edge_id>/<device_id>`。
- **实时链路**：账号会话下 edge → server → 浏览器走 WebSocket；REST 承担历史查询与管理操作。
  租户令牌会话只有 REST，无浏览器实时通道（见下文边界）。
- **单二进制与零 CGO**：WebUI `go:embed` 进 server；SQLite 用 `modernc.org/sqlite`，交叉编译 Linux/arm64 无需工具链。

```text
        ┌──────────────────────────────────────────────────────────┐
        │  Experience Plane                                        │
        │  WebUI（概览/设备/网关/运行记录/应用与插件/设置/管理）· Schema 渲染 │
        └───────────────▲──────────────────────────────────────────┘
                        │ REST + WebSocket（/ws）
        ┌───────────────┴──────────────────────────────────────────┐
        │  Control Plane —— cloudpath-server（单二进制 + SQLite）   │
        │  desired 权威 · 租户/RBAC · 令牌 · 审计 · 限流 · 保留期     │
        │  插件目录/运行实例期望态 · 操作下发与回执结算                    │
        └───────────────▲──────────────────────────────────────────┘
                        │ WebSocket（/ws/edge）：state / event 上行，command 下行
        ┌───────────────┴──────────────────────────────────────────┐
        │  Gateway Plane（Edge）—— cloudpath-edge（每台电脑/站点一个） │
        │  observed 权威 · 设备监督与退避重开 · 离线事件缓冲           │
        │  Driver Host（外部插件进程）· 本地 secret 明文解析           │
        └───────────────▲──────────────────────────────────────────┘
                        │ 串口 / 本地总线
                 设备（参考 Driver：stcb；或外部 Driver 插件）
```

## 三类插件与运行位置

| 类型 | 默认宿主 | 负责 | 不负责 |
|---|---|---|---|
| **Driver** | 网关（Edge） | 设备发现、连接、协议解析、能力映射、设备动作 | 业务流程、租户 UI |
| **Application** | Server | 业务对象、绑定、规则、任务、领域 API | 直接访问串口或 Core 数据库 |
| **Connector** | 网关（Edge）或中心服务（Server；运行时目标态） | MQTT / Webhook / 外部平台 / 通知 / 数据出口 | 定义核心设备模型 |

UI 贡献不是独立的可执行插件类型：当前由 Descriptor/Capability schema 驱动通用设备视图与操作表单；任意页面 Schema 属于目标态。

插件源码入口与 Core 参考材料：

| 位置 | 形态 | 说明 |
|---|---|---|
| [cloud-path-driver-stcb](https://github.com/DeliciousBuding/cloud-path-driver-stcb) | **独立 Driver Plugin** | STC-B 参考驱动；经 GitHub discover/install 由 Plugin Host 运行 |
| [cloud-path-app-scheduled-compartment](https://github.com/DeliciousBuding/cloud-path-app-scheduled-compartment) | **独立 Application Plugin** | 定时隔间应用的现役源码、配置与发布说明 |
| [cloud-path-app-button-indicator](https://github.com/DeliciousBuding/cloud-path-app-button-indicator) | **独立 Application Plugin** | 按键指示应用的现役源码、配置与发布说明 |
| [cloud-path-app-environment-guard](https://github.com/DeliciousBuding/cloud-path-app-environment-guard) | **独立 Application Plugin** | 环境监护应用的现役源码、配置与发布说明 |
| [examples/scheduled-compartment/](examples/scheduled-compartment/README.md) | **Core 参考 / 历史 bootstrap** | 只依赖公开 SDK 的参考快照；[deploy/split/](deploy/split/README.md) 不用于更新现役应用 |
| [templates/go-plugin/](templates/go-plugin/README.md) | 官方 Go 插件模板 | 新插件的起步材料，带 CI、Release 与 manifest 校验器；不是已有应用的更新源 |

**现役应用的源码事实源是各自独立仓库。** 修复与升级在对应仓库进行，不要从 Core 示例或
split/scaffold 重新生成并覆盖已独立演进的应用。可安装版本、资产与摘要以各仓 Release 为准，
源码合并不等于已发布。

Core >= v0.2.15 的类型化 `property-observed`、手动任务与显式 `app_bindings` 接口见
[应用输入与操作接口](docs/design.md#应用输入与操作契约)；应用依赖范围以各自 `plugin.yaml` 和 `go.mod` 为准。

能力模型、控制面同步语义与租户安全边界见
[docs/architecture/capability-model.md](docs/architecture/capability-model.md)、
[docs/architecture/control-plane-sync.md](docs/architecture/control-plane-sync.md)、
[docs/architecture/tenant-security-policy.md](docs/architecture/tenant-security-policy.md)。

---

## 快速开始（本机）

前置：**Go 1.26+**、**Node 20+**（CI 用 24）、**pnpm**（版本以
[webui/package.json](webui/package.json) 的 `packageManager` 为准）；
[task](https://taskfile.dev/) 可选（`go install github.com/go-task/task/v3/cmd/task@latest`）。

```bash
git clone https://github.com/DeliciousBuding/cloud-path.git
cd cloud-path

task setup        # go mod download + webui pnpm install
task build        # 前端构建 → go:embed → bin/cloudpath-server(.exe) + bin/cloudpath-edge(.exe)
```

### 1. 起中心服务

```bash
./bin/cloudpath-server            # Windows: .\bin\cloudpath-server.exe
# 默认 127.0.0.1:8080，数据库 data/cloudpath.db，前端已内嵌
curl -fsS http://127.0.0.1:8080/healthz
```

本地默认是 L0 单机形态：不设令牌时，读接口开放、**写操作只允许回环来源**。
要体验账号模式（推荐，也是公网形态的前提），在**同一台机器**上首装管理员：

```bash
curl -fsS -X POST http://127.0.0.1:8080/api/auth/setup \
  -H 'Content-Type: application/json' \
  --data '{"username":"admin","password":"<换成强密码>"}'
```

首装后服务立即进入账号模式：除 `/healthz`、静态资源与 `/api/auth/*` 外，
全部 `/api/*` 与 `/ws` 都需要凭据；重复 setup 返回 `409 already set up`。

### 2. 接一台设备

```bash
cp edge.example.yaml edge.yaml    # edge.yaml 是本地私有配置，不入库
./bin/cloudpath-edge              # Windows: .\bin\cloudpath-edge.exe
```

- **没有硬件**：直接用母版里的内置 `adapter: demo`（无需串口）。设备会真实上线并持续
  上报进程内状态，操作真实执行并返回结果，可验证网关接入、多机接入、操作状态和断线重连。
- **有真实串口设备**：先安装并启用对应 Driver Plugin，再在 `edge.yaml` 启用
  `plugin_host`，填写 `port`（Windows `COM3`、Linux `/dev/ttyUSB0`、macOS
  `/dev/cu.usbserial-*`）与 `adapter: stcb`。串口不存在时设备保持 offline，网关按
  1→2→4→8…→30s 退避重试拔插自愈；demo 与外部 Driver 设备可挂在同一个网关上共存。

### 3. 打开管理台

浏览器访问 <http://127.0.0.1:8080>：

- `/setup`：首装向导（在服务器本机/回环创建首个管理员账号；完成后转登录页）。
- `/login`：登录页。**账号密码登录**（会话 cookie）为主路径，实时通道 `/ws` 跟随登录态；
  也接受「访问令牌」（`CLOUDPATH_TOKEN` 或租户令牌）作为兜底——令牌会话只有 REST、没有实时推送
  （浏览器 WebSocket 无法携带 Authorization header），UI 会诚实显示「实时通道已断开」并定时刷新数据。
- 登录后：概览 `/`、设备 `/devices`、设备详情 `/devices/<edge>/<device>`、运行记录 `/activity`
  （旧 `/events` 路由自动跳转）、应用与插件 `/plugins`、实例详情 `/plugins/<id>`、网关 `/edges`、
  网关详情 `/edges/<edge>`、设置 `/settings`；`role=admin` 另有 `/admin`（成员、权限和访问令牌）。

### 4. 看设备、下发操作、看事件

设备卡片出现后，在详情页的操作面板点按钮（白名单来自适配器：
`demo` 为 `ping / set / dump / noop`；外部 Driver（如 `stcb`）的操作面由该 Driver 的 Capability actions 提供），或用 API：

```bash
curl -fsS -X POST http://127.0.0.1:8080/api/devices/<edge_id>/<device_id>/commands \
  -H 'Content-Type: application/json' --data '{"cmd":"sync","args":""}'
# 返回 200 {"id":1,...,"status":"sent"}；网关离线返回 409，设备离线由后续回执标 failed

curl -fsS "http://127.0.0.1:8080/api/events?limit=10"     # 事件流（新→旧）
curl -fsS http://127.0.0.1:8080/api/edges                 # 网关在线状态
```

操作状态是 `pending → sent → ok|failed|timeout`；90 秒未回执由后台 sweeper 标为 `timeout`，前端按 `command_id` 结算回执。事件与终态操作默认保留 30 天。

### 界面

苹果极简风管理台：浅色/深色双主题、移动端自适应、尊重系统「减少动效」偏好。

界面截图不在仓库内维护：验证截图可能含本机串口号与设备标识，按公开边界不入库。

---

## 公网部署（systemd + nginx，不用容器）

完整逐条 SOP 见 [deploy/README.md](deploy/README.md)。要点：

1. **先断言产物架构，再投递**。发布矩阵必须包含 `linux/arm64`，拿错架构会直接失败。
   门禁是硬失败，不是警告：

   ```bash
   task build:linux-arm64      # 交叉编译 + 自动断言 linux/arm64 + embed_ui
   python scripts/assert_arch.py --expect-os linux --expect-arch arm64 \
     --expect-tags embed_ui bin/cloudpath-server_linux_arm64
   ```

2. **systemd 单元**：[deploy/systemd/cloudpath-server.service](deploy/systemd/cloudpath-server.service)
   —— 专用非 root 账号、`Restart=always`、资源上限、沙箱加固、
   `ReadWritePaths` 指向 SQLite 持久目录；机密走 0600 的
   [systemd/cloudpath-server.env.example](deploy/systemd/cloudpath-server.env.example)（模板只有变量名）。
3. **nginx 反代（HTTPS + WSS）**：[deploy/nginx.conf](deploy/nginx.conf)
   —— `/ws` 与 `/ws/edge` 单独 location、`Upgrade`/`Connection` 升级头、
   `proxy_read_timeout 3600s`（60s 会掐断健康长连接）、CDN 真实客户端 IP、
   请求体上限与 gzip；HSTS 由反代补，应用自身的安全头（含 CSP）不被覆盖。
4. **鉴权归 CloudPath 自己**：产品自带登录、RBAC 与多租户，因此反代层是 public 的，
   不要再挂一层 auth_request/basic auth。
5. **先首装账号，再放开公网入口**：`POST /api/auth/setup` 的放行判据是真实 TCP 对端是否回环，
   同机反代转发过来的请求对端就是回环——所以必须在启用 nginx 站点之前，从主机本机完成首装。
6. 容器/compose 形态仍可用（[deploy/compose/](deploy/compose/README.md)），
   但注意**宿主架构必须与镜像架构一致**，否则同样是 `exec format error`。

健康检查 `GET /healthz`；日志走 journald（JSON 结构化）；备份与保留期策略见
[deploy/README.md](deploy/README.md) §8。

---

## 多台电脑接入同一个 Server（把网关分发给别人）

这是 CloudPath 的常规用法：**一个 Server，任意多台电脑各自跑网关**，
每台电脑的设备互相隔离、独立控制。

### 管理员侧（一次性，每台接入电脑一份凭据）

1. 完成首装并登录（`role=admin`）。
2. 为每台电脑创建**租户令牌**，scope 取 `edge`：

   ```bash
   curl -fsS -X POST http://127.0.0.1:8080/api/tokens \
     -H 'Content-Type: application/json' \
     --data '{"name":"classmate-pc-01","scopes":["edge"]}'
   # 明文只在本次响应返回一次（cp_ 开头）；库里只存 SHA-256
   ```

   也可以在 WebUI `/admin` 页面创建。一人一令牌、一台电脑一个 `edge_id`。
3. 把三样东西交给对方：`wss://<域名>/ws/edge`、令牌、约定好的 `edge_id`。
4. 在 `/edges` 与 `/devices` 确认对方上线；按 `<edge_id>/<device_id>` 独立下发操作。

### 使用者侧（自己的电脑，不改任何代码）

完整指引见 [deploy/edge/README.md](deploy/edge/README.md)（含各平台一句话安装、
手动启动、常见问题）。最短路径：

1. 从 [GitHub Release](https://github.com/DeliciousBuding/cloud-path/releases) 下载
   自己平台的 `cloudpath-edge_<version>_<os>_<arch>[.exe]`，并按 `checksums.txt` 校验。
   六个平台组合（linux/windows/darwin × amd64/arm64）都有产物。
2. 复制仓库根的 [edge.example.yaml](edge.example.yaml) 为 `edge.yaml`，填：

   ```yaml
   server: wss://<域名>/ws/edge      # 公网必须 wss://
   token: ${CLOUDPATH_TOKEN}         # 用环境变量注入，明文不落盘
   edge_id: <管理员分配的 ID>
   devices:
     - id: board-1
       adapter: stcb
       port: <本机串口>
       baud: 9600
   ```

3. 设 `CLOUDPATH_TOKEN` 后运行二进制；看到 `connected to server` 即接入成功。
4. 断网/合盖/拔线都不用管：网关自带指数退避重连，离线事件进有界缓冲、重连后回放。

### 隔离与互不影响的实测事实

- 设备全局键是 `<edge_id>/<device_id>`；两台网关各带多台设备时，操作按设备键路由，不串线。
- `edge` scope 的令牌**只能**连 `/ws/edge`；用它请求 REST（如 `/api/devices`）会得到 `403`。
- 一台网关掉线不会踢掉另一台的连接，也不会改写对方的在线状态。
- 跨租户互相不可见：设备/事件/操作/运行实例都按 `tenant_id` 隔离。
- 使用者若用租户令牌登录 WebUI 只有 REST（无 `/ws` 实时推送，页面定时刷新并诚实提示）；
  需要完整实时通道就为其创建账号，走账号密码登录。

---

## 配置参考

### server（flag 与环境变量等价，flag 优先）

| flag | env | 默认 | 说明 |
|---|---|---|---|
| `-addr` | `CLOUDPATH_ADDR` | `127.0.0.1:8080` | 监听地址；公网部署保持回环，由反代对外 |
| `-db` | `CLOUDPATH_DB` | `data/cloudpath.db` | SQLite 路径（WAL） |
| `-token` | `CLOUDPATH_TOKEN` | 空 | 共享服务令牌（legacy，等价 default 租户 admin）；空=无鉴权（仅 L0） |
| `-webui` | `CLOUDPATH_WEBUI` | 空 | 开发模式前端静态目录（优先于内嵌产物） |
| `-allowed-origins` | `CLOUDPATH_ALLOWED_ORIGINS` | 空 | 浏览器 WS Origin 白名单（host 模式，逗号分隔）；空=开发策略并告警 |
| `-require-auth` | `CLOUDPATH_REQUIRE_AUTH` | false | 无用户时也强制读/写鉴权（L2 公网） |
| `-retention-days` | `CLOUDPATH_RETENTION_DAYS` | 30 | 事件/终态操作保留天数 |
| `-cmd-rate` | `CLOUDPATH_CMD_RATE` | 20 | 单设备每分钟操作上限，超限 429 |
| `-login-rate` | `CLOUDPATH_LOGIN_RATE` | 5 | 单 IP 每分钟登录尝试上限，超限 429 + Retry-After |
| `-session-days` | `CLOUDPATH_SESSION_DAYS` | 7 | 会话有效期（天） |
| `-setup-token` | `CLOUDPATH_SETUP_TOKEN` | 空 | 一次性首装令牌（非回环来源 setup 必带） |
| `-trusted-proxies` | `CLOUDPATH_TRUSTED_PROXIES` | 空 | 可信反代 CIDR；只有命中才采信 X-Forwarded-*（防伪造） |
| `-log-level` / `-log-format` | `CLOUDPATH_LOG` / `CLOUDPATH_LOG_FORMAT` | `info` / `text` | 日志级别 / text|json |

### edge（`edge.yaml`，本地私有不入库；母版 [edge.example.yaml](edge.example.yaml)）

| 字段 | 必填 | 默认 | 说明 |
|---|---|---|---|
| `server` | 是 | — | 网关（Edge）接入端点，末尾 `/ws/edge`；公网用 `wss://` |
| `token` | 服务端启用鉴权时必填 | — | 支持 `${ENV}` 展开 |
| `edge_id` | 否 | 主机名（点号归一为 `-`） | 字母数字 `-_`，1–64，全局唯一 |
| `poll_interval_s` | 否 | 5 | 状态转储轮询周期 |
| `sync_interval_s` | 否 | 600 | 周期对时 |
| `report_interval_s` | 否 | 30 | 状态心跳兜底（无变化也定期上报） |
| `devices[].id` | 是 | — | edge 内唯一；全局键 `<edge_id>/<id>` |
| `devices[].adapter` | 是 | — | 适配器名（`GET /api/adapters`） |
| `devices[].name` | 否 | — | 展示名 |
| `devices[].port` | 是 | — | 串口路径 |
| `devices[].baud` | 否 | 9600 | 波特率 |
| `plugin_host.enabled` | 否 | false | 外部 Driver 插件宿主开关 |
| `plugin_host.root` | 否 | `plugins.d` | 插件安装根目录（支持 `${ENV}`） |
| `plugin_host.state_dir` | 否 | `data/plugin-state` | 运行实例 desired-state 目录 |
| `plugin_host.tenant` | 否 | `default` | 外部 driver 实例租户 |
| `plugin_host.required` | 否 | false | true=host 失败则 edge 启动失败 |
| `plugin_host.lock` | 否 | `<root>/plugins.lock` | 锁文件（固定版本/digest/来源） |
| `plugin_host.close_timeout_s` | 否 | 10 | 优雅关闭 deadline |

> `plugin_host` 会按 desired-state 监督外部 driver 进程，并把 driver ID 桥接成
> 网关的 device.Adapter（Driver Protocol v1）：打开/状态/操作均走 DriverClient；
> 内置 adapter 与外部 driver ID 冲突会拒绝启动。外部 driver 实例的 `port` 等配置
> 由 Server desired state（运行实例配置）注入，网关自身不伪造设备在线。

### CLI（`cloudpath`）：插件 Registry 控制面

```bash
cloudpath plugin search <关键词>          # GitHub Topic 开放通道 + Registry 精选
cloudpath plugin inspect <id>             # manifest/兼容范围/摘要/权限披露
cloudpath plugin install <repo-or-id> --digest sha256:<hex> --yes
# 发布的二进制内嵌 manifest schema：干净机器上无需仓库 checkout，
# -schema PATH 只用于覆盖；install 输出会打印 schema 来源（file:/embedded）
cloudpath plugin enable|disable|update|remove <id>
cloudpath plugin host                     # 把 desired-state 变成受监督的插件进程
```

安装前强制验证 Manifest、兼容范围、Release 资产与摘要；`plugins.lock` 记录版本、
digest、来源与验证结果。Topic `cloudpath-plugin` 只是候选集合，不是信任证明。

---

## 安全边界

**暴露分级**（详见 [docs/security.md](docs/security.md)）：L0 单机（默认回环绑定、无令牌）→
L1 内网/反代（`-allowed-origins` + 建议令牌 + TLS）→ L2 公网（令牌或账号模式 +
`-require-auth` + TLS + 限流 + 安全头）。**不要把 L0 配置直接放到公网。**

**两种凭据模式**：

- 共享服务令牌 `-token`（legacy，等价 default 租户 admin）；
- 账号模式：`setup → login`（会话 cookie `cp_session`，HttpOnly / SameSite=Lax / TLS 下 Secure）
  + RBAC（`admin > operator > viewer`）+ **租户令牌**（`cp_` 前缀，scope 为
  `read|write|admin|edge` 的非空子集；明文只返回一次，库中只存 SHA-256 与短前缀）。

**Secret 边界（当前关键设计）**：

- **Server 只见 `secret://<name>` handle**，配置与审计里出现的都是 handle，不是值；
- **明文只存在于目标网关本地**：由本地 provider 按 `<root>/<tenant>/<instance>/<name>`
  解析（[internal/secrethandle](internal/secrethandle/secrethandle.go)），不缓存、不落日志、
  不跨租户/实例读取；handle 名严格校验，无法编码路径或平台技巧；
- 插件必须在 manifest `permissions.secrets` 里**显式声明**才能解析对应 handle，未声明即 fail-closed；
- 当前**不做中心 Secret Store**：Server 不保存、不转发任何明文。

**输入与资源防护**：操作白名单由适配器声明；`args` ≤64 字节且不含换行/NUL；
`edge_id` 形状校验；设备归属校验（edge 只能上报自己注册过的键）；请求体
`MaxBytesReader(4096)`；WS 读上限（edge 64KB / 浏览器 4KB）；SPA 路径穿越防护；
操作与登录限流（429 + `Retry-After`）。

**响应头**：`X-Content-Type-Options: nosniff`、`X-Frame-Options: DENY`、
`Referrer-Policy: no-referrer`、`Permissions-Policy`、带内联脚本 hash 的 CSP；
`/` 为 `no-cache`、`/assets/*` 为 `public, max-age=31536000, immutable`。

**仓库边界**：`edge.yaml`、`.env`、`*.local.json`、运行数据与构建产物一律不入库；
公开仓库不出现真实令牌、私钥、内网主机/IP、本机绝对路径与课程/学校资产。
发布前由 [scripts/public_audit.py](scripts/public_audit.py) 与
[scripts/check_links.py](scripts/check_links.py) 门禁（CI 同跑）。

---

## 开发

### task 命令表

| 命令 | 作用 |
|---|---|
| `task setup` | 首次拉取后安装依赖（Go + 前端） |
| `task build` | 前端构建 → 内嵌 → `bin/` 双二进制 |
| `task build:linux-arm64` | 生产物料：Linux arm64 server + 架构断言（硬失败） |
| `task build:matrix` | 全平台矩阵（server/edge/cli × 6 平台）→ `dist/` + `checksums.txt` |
| `task verify:arch` | 不重新构建，复跑 `dist/` 产物断言 + checksums 校验 |
| `task release:artifacts` | 矩阵构建 → 架构断言 → 公开审计 → 链接/workflow 检查 |
| `task dev:server` / `dev:edge` / `dev:web` | 开发模式（:8080 / edge / Vite :5173） |
| `task test` | Go 单测 + 前端 typecheck/组件测试 |
| `task test:race` | 带竞态检测（需 cgo，Linux/CI 用） |
| `task test:web` / `test:templates` | 前端 frozen install 完整流程 / Go 插件模板完整流程 |
| `task vet` / `lint` | go vet + gofmt 门禁 / 全量静态检查 |
| `task audit:public` / `check:links` / `check:workflows` | 公开边界 / Markdown 链接 / workflow 结构门禁 |
| `task selftest:scripts` | 发布脚本自测（架构断言 + 构建矩阵） |
| `task run` / `run:edge` | 生产模式本地起 server / edge |
| `task clean` | 清理 `bin/`、`data/`、`webui/dist/` |
| `task verify` | 聚合门禁（vet/test/web/templates/audit/links/workflows/脚本自测） |

### 测试

```bash
go test ./... -count=1
go test ./... -race -count=1          # Linux / CI
task test:web                          # pnpm frozen + tsc --noEmit + vitest + build
task test:templates                    # 模板 build/vet/test + manifest + rename 门禁
go test ./testing/plugin-harness -run TestScheduledCompartmentBinaryHostE2E -count=1
task verify                            # 发布前聚合门禁
```

### 目录结构

```text
cloud-path/
├── cmd/            cloudpath（插件 CLI）· cloudpath-server · cloudpath-edge
├── internal/       api（REST/WS 类型）· auth · server · edge · store(SQLite) · device
│                   pluginhost · plugincontrol · edgedriverhost · plugincatalog
│                   application · appruntime · registry · secrethandle · audit
│                   tenantpolicy · model · logx
├── sdk/go/         公开 Go SDK：cloudpath/v1/{application,driver,status} · driverkit
│                   model · pluginmain · pluginruntime · rpc · transport
├── proto/ spec/    versioned 协议与 manifest JSON Schema
├── examples/       demo（参考 Driver）· scheduled-compartment（参考 Application）
├── templates/      go-plugin/{driver,application} 官方模板
├── testing/        plugin-harness（二进制→Host E2E）· plugin-fixtures
├── webui/          React 19 SPA（构建产物被 server 内嵌）
├── deploy/         systemd · nginx · edge 分发 · split 拆仓生成器
├── scripts/        门禁脚本（Python 3 stdlib only）
├── docs/           设计与协议说明
└── firmware/       设备侧协议参考说明（不含厂商固件/库）
```

### 发布

打 `v*` tag 即触发 [.github/workflows/release.yml](.github/workflows/release.yml)：
6 平台矩阵构建（server 带 `-tags embed_ui`）→ release job 合并资产后用
`scripts/build_matrix.py --verify-only` 复验（含强制 linux/arm64 与 `embed_ui`）
并生成唯一 `checksums.txt` → 创建 GitHub Release。
产物命名与校验方式见 [CHANGELOG.md](CHANGELOG.md)。

---

## 当前状态与边界

> 未实现的能力不写成现状。完整的当前实现与目标态见 [docs/architecture.md](docs/architecture.md)。

当前实现包括单二进制 Server（内嵌 WebUI）、网关（Edge）、插件 CLI、账号/RBAC/多租户、
设备监督与离线缓冲、外部 Driver Plugin Host、Application Runtime、操作与事件持久化、
Registry CLI、WebUI，以及发布和部署物料。代码和测试能证明软件行为，不等于真板验证。

**真板证据边界**

- 多实例多设备映射和串口注入已有协议级回归测试，但尚未完成同一外部 Driver 驱动多块真板、
  覆盖拔插、重连和操作 ACK 的现场 E2E。
- 现有 STC-B 单板链路可用于回归；新结论必须附真板日志、操作 ACK 和设备事件。
- 操作和事件路由目前假设 `entity_id` 全局唯一，`(device_key, entity_id)` 尚未贯穿绑定与路由。
  多板链路在协议和真板证据补齐前不算已验证。

**尚未实现**

Connector/通知运行时、Transform/WASM、MQTT/Modbus 接入、远程 OTA、时序聚合、中心 KMS/Vault、
分布式配额和多 Server 仍未实现。租户令牌会话只有 REST，没有浏览器实时通道。
完整列表见 [docs/architecture.md](docs/architecture.md) §11。

---

## 文档地图

| 文档 | 内容 |
|---|---|
| [docs/design.md](docs/design.md) | 技术设计：技术栈、进程模型、存储、行为边界、安全、测试 |
| [webui/DESIGN.md](webui/DESIGN.md) | WebUI 呈现、排版、布局与交互设计 |
| [docs/architecture.md](docs/architecture.md) | 架构总览与「当前实现 vs 目标态」 |
| [docs/protocol.md](docs/protocol.md) | 网关（Edge）↔ 中心服务（Server）线上协议（信封、消息、DTO） |
| [docs/api.md](docs/api.md) | REST/WS API、鉴权三级模型、限流与安全头 |
| [docs/security.md](docs/security.md) | 安全与运维基线（L0/L1/L2、令牌、检查表） |
| [docs/deploy.md](docs/deploy.md) | 部署指南（本地/容器/反代） |
| [docs/architecture/plugin-system.md](docs/architecture/plugin-system.md) | 插件运行时与协议 |
| [docs/architecture/github-ecosystem.md](docs/architecture/github-ecosystem.md) | GitHub Topic 发现与信任链 |
| [docs/architecture/registry.md](docs/architecture/registry.md) | Registry 索引与 `cloudpath plugin` CLI |
| [docs/architecture/how-to-build-driver.md](docs/architecture/how-to-build-driver.md) | 新增 Driver 的操作入口 |
| [docs/architecture/capability-model.md](docs/architecture/capability-model.md) | Device/Entity/Capability 模型 |
| [docs/architecture/name-lexicon.md](docs/architecture/name-lexicon.md) | 用户侧中文名称与机器标识映射 |
| [docs/architecture/control-plane-sync.md](docs/architecture/control-plane-sync.md) | 声明式快照 + 单调 revision 同步语义 |
| [docs/architecture/tenant-security-policy.md](docs/architecture/tenant-security-policy.md) | 租户配额、保留期与 secret 边界 |
| [docs/architecture/repository-strategy.md](docs/architecture/repository-strategy.md) | 仓库组合、命名、拆仓条件与公开边界 |
| [docs/architecture/adr/0001-capability-centered-plugins.md](docs/architecture/adr/0001-capability-centered-plugins.md) | 能力中心插件模型决策 |
| [docs/architecture/adr/0002-github-plugin-discovery.md](docs/architecture/adr/0002-github-plugin-discovery.md) | GitHub 发现与 Registry 信任决策 |
| [deploy/README.md](deploy/README.md) | 公网落地 SOP（systemd + nginx + WSS） |
| [deploy/edge/README.md](deploy/edge/README.md) | 客户端分发与 `edge.yaml` 填写指引 |
| [deploy/compose/README.md](deploy/compose/README.md) | Docker Compose 本地与公网形态 |
| [deploy/split/README.md](deploy/split/README.md) | 参考 Application 拆仓与历史 bootstrap 说明 |
| [templates/go-plugin/README.md](templates/go-plugin/README.md) | 新 Driver/Application 插件模板 |

## 许可

MIT © CloudPath Authors — 见 [LICENSE](LICENSE)。
