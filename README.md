<div align="center">

# CloudPath · 云径

**云原生、插件驱动的互联物联网控制平台（Cloud-Native IoT Control Plane）**：
以「中心控制面 + 边缘代理」的边云协同一体化架构，将任意设备接入云端、实时可视化并远程控制；
设备无关、边缘自治，新设备 = 一个 Driver 插件。

[![Go](https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&logoColor=white)](https://go.dev/)
[![React](https://img.shields.io/badge/React-19-61DAFB?logo=react&logoColor=black)](https://react.dev/)
[![SQLite](https://img.shields.io/badge/SQLite-WAL-003B57?logo=sqlite&logoColor=white)](https://sqlite.org/)
[![License](https://img.shields.io/badge/license-MIT-blue)](LICENSE)

</div>

---

## 这是什么

CloudPath 把「插上一台设备 → 上云看到它 → 远程控制它」做成**可开箱即用的云原生基础设施**，
而非某块开发板的上位机。

- **云原生 · 边云协同**：中心控制面（Server）是 **期望态 / 租户 / 审计的唯一权威**；边缘代理（Edge）是
  **观测态的唯一权威**并保存最后成功 applied 快照。边缘自治：断网继续运行，重连仅应用最终快照、不回放中间副作用。
- **设备无关 · 插件驱动**：核心（`internal/*`）不识别任何具体硬件；新设备 = 一个 Driver 插件。
- **分布式 Hub-Spoke**：多边缘节点 + 单中心控制面，天然中心-边缘拓扑，可横向扩展。
- **设备身份** = `(tenant_id, edge_id, device_id)`；线上传输键 `<edge_id>/<device_id>`。
- **全链路实时**：edge → server → 浏览器全程 WebSocket；REST 只承担历史查询与管理操作。
- **单二进制 · 零 CGO**：WebUI `go:embed` 进 server；SQLite 用 `modernc.org/sqlite`，交叉编译 Linux/arm64 无需工具链。

```text
        ┌──────────────────────────────────────────────────────────┐
        │  Experience Plane                                        │
        │  WebUI（概览/设备/事件/边缘/系统/管理）· Schema 渲染        │
        └───────────────▲──────────────────────────────────────────┘
                        │ REST + WebSocket（/ws）
        ┌───────────────┴──────────────────────────────────────────┐
        │  Control Plane —— cloudpath-server（单二进制 + SQLite）   │
        │  desired 权威 · 租户/RBAC · 令牌 · 审计 · 限流 · 保留期     │
        │  插件目录/实例期望态 · 命令下发与回执结算                    │
        └───────────────▲──────────────────────────────────────────┘
                        │ WebSocket（/ws/edge）：state / event 上行，command 下行
        ┌───────────────┴──────────────────────────────────────────┐
        │  Edge Plane —— cloudpath-edge（每台电脑/站点一个）          │
        │  observed 权威 · 设备监督与退避重开 · 离线事件缓冲           │
        │  Driver Host（外部插件进程）· 本地 secret 明文解析           │
        └───────────────▲──────────────────────────────────────────┘
                        │ 串口 / 本地总线
                 设备（参考 Driver：stcb；或外部 Driver 插件）
```

## 三类插件与运行位置

| 类型 | 默认宿主 | 负责 | 不负责 |
|---|---|---|---|
| **Driver** | Edge | 设备发现、连接、协议解析、能力映射、设备动作 | 业务流程、租户 UI |
| **Application** | Server | 业务对象、绑定、规则、任务、领域 API | 直接访问串口或 Core 数据库 |
| **Connector** | Edge 或 Server | MQTT / Webhook / 外部平台 / 通知 / 数据出口 | 定义核心设备模型 |

UI 贡献不是独立的可执行插件类型：插件通过 Manifest 提交声明式导航、表单与页面 Schema。

当前仓库内的插件形态（孵化状态，逐条如实）：

| 位置 | 形态 | 说明 |
|---|---|---|
| [examples/stcb/](examples/stcb/README.md) | **进程内参考 Driver** | 通过 blank import 编译进 edge/server；`plugin.yaml` 已冻结未来独立仓的机器契约 |
| [examples/scheduled-compartment/](examples/scheduled-compartment/README.md) | **进程式参考 Application** | 由 Plugin Host 以独立进程拉起，只依赖公开 SDK；可用 [deploy/split/](deploy/split/README.md) 生成独立仓 |
| [templates/go-plugin/](templates/go-plugin/README.md) | 官方 Go 插件模板 | driver / application 两套，带 CI、Release 与 manifest 校验器 |

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

- **有真实串口设备**：在 `edge.yaml` 里填 `port`（Windows `COM3`、Linux `/dev/ttyUSB0`、
  macOS `/dev/cu.usbserial-*`）与 `adapter: stcb`。
- **没有硬件**：用内置参考演示适配器 `adapter: demo`（无需串口）。设备会真实上线并持续
  上报模拟状态（tick/uptime/level），命令 `ping/set/dump/noop` 真实执行并返回结果，
  零硬件即可验证「Edge 接入 / 多机接入 / 命令闭环 / 断线重连」全链路；demo 与 `stcb` 真板
  设备可挂在同一个 Edge 上共存。（`adapter: stcb` 而串口不存在时设备保持 offline，
  Edge 按 1→2→4→8…→30s 退避重试拔插自愈。）

### 3. 打开管理台

浏览器访问 <http://127.0.0.1:8080>：

- `/setup`：首装向导（在服务器本机/回环创建首个管理员账号；完成后转登录页）。
- `/login`：登录页。**账号密码登录**（会话 cookie）为主路径，实时通道 `/ws` 跟随登录态；
  也接受「访问令牌」（`CLOUDPATH_TOKEN` 或租户令牌）作为兜底——令牌会话只有 REST、没有实时推送
  （浏览器 WebSocket 无法携带 Authorization header），UI 会诚实显示「实时通道已断开」并定时刷新数据。
- 登录后：概览 `/`、设备 `/devices`、设备详情 `/devices/<edge>/<device>`、活动 `/activity`
  （旧 `/events` 路由自动跳转）、插件 `/plugins`、实例详情 `/plugins/<id>`、边缘节点 `/edges`、
  边缘详情 `/edges/<edge>`、设置 `/settings`；`role=admin` 另有管理页 `/admin`（用户、令牌、一次性令牌明文面板）。

### 4. 看设备、下发命令、看事件

设备卡片出现后，在详情页的命令面板点按钮（白名单来自适配器：
`stcb` 为 `sync / dump / trigger / open / isp / raw`，`demo` 为 `ping / set / dump / noop`），或用 API：

```bash
curl -fsS -X POST http://127.0.0.1:8080/api/devices/<edge_id>/<device_id>/commands \
  -H 'Content-Type: application/json' --data '{"cmd":"sync","args":""}'
# 返回 {"id":1,...,"status":"sent"}；设备离线时回执为 failed / "device offline"

curl -fsS "http://127.0.0.1:8080/api/events?limit=10"     # 事件流（新→旧）
curl -fsS http://127.0.0.1:8080/api/edges                 # 边缘节点在线状态
```

命令闭环是 `pending → sent → ok|failed`，90 秒未回执由后台 sweeper 标 `timeout`；
前端按 `command_id` 结算回执。事件与终态命令默认保留 30 天。

### 界面

苹果极简风管理台：浅色/深色双主题、移动端自适应、尊重系统「减少动效」偏好。

界面截图不在仓库内维护：历史验证截图含本机串口号与内部 edge id，按公开边界只保留在私有验证目录；
正式产品截图随每次 Release 的发布说明提供。

---

## 公网部署（systemd + nginx，不用容器）

完整逐条 SOP 见 [deploy/README.md](deploy/README.md)。要点：

1. **先断言产物架构，再投递**。生产主机是原生 `aarch64` 且没有 qemu/binfmt 回退，
   拿错架构必然 `exec format error`。门禁是硬失败，不是警告：

   ```bash
   task build:linux-arm64      # 交叉编译 + 自动断言 linux/arm64 + embed_ui
   python scripts/assert_arch.py --expect-os linux --expect-arch arm64 \
     --expect-tags embed_ui bin/cloudpath-server_linux_arm64
   ```

2. **systemd 单元**：[deploy/systemd/cloudpath-server.service](deploy/systemd/cloudpath-server.service)
   —— 专用非 root 账号、`Restart=always`、资源上限、沙箱加固、
   `ReadWritePaths` 指向 SQLite 持久目录；机密走 0600 的
   [systemd/cloudpath-server.env.example](deploy/systemd/cloudpath-server.env.example)（模板只有变量名）。
3. **nginx 反代（HTTPS + WSS）**：[deploy/nginx/cloudpath.vectorcontrol.tech.conf](deploy/nginx/cloudpath.vectorcontrol.tech.conf)
   —— `/ws` 与 `/ws/edge` 单独 location、`Upgrade`/`Connection` 升级头、
   `proxy_read_timeout 3600s`（60s 会掐断健康长连接）、CDN 真实客户端 IP、
   请求体上限与 gzip；HSTS 由反代补，应用自身的安全头（含 CSP）不被覆盖。
4. **鉴权归 CloudPath 自己**：产品自带登录、RBAC 与多租户，因此反代层是 public 的，
   不要再挂一层 auth_request/basic auth。
5. **先首装账号，再放开公网入口**：`POST /api/auth/setup` 的放行判据是真实 TCP 对端是否回环，
   同机反代转发过来的请求对端就是回环——所以必须在启用 nginx 站点之前，从主机本机完成首装。
6. 容器/compose 形态仍可用（[deploy/docker-compose.yml](deploy/docker-compose.yml)），
   但注意**宿主架构必须与镜像架构一致**，否则同样是 `exec format error`。

健康检查 `GET /healthz`；日志走 journald（JSON 结构化）；备份与保留期策略见
[deploy/README.md](deploy/README.md) §8。

---

## 多台电脑接入同一个 Server（把 Edge 分发给别人）

这是 CloudPath 的常规用法：**一个 Server，任意多台电脑各自跑 Edge**，
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
4. 在 `/edges` 与 `/devices` 确认对方上线；按 `<edge_id>/<device_id>` 独立下发命令。

### 使用者侧（自己的电脑，不改任何代码）

完整指引见 [deploy/edge/README.md](deploy/edge/README.md)（含各平台一句话安装、
开机自启、常见问题）。最短路径：

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
4. 断网/合盖/拔线都不用管：Edge 自带指数退避重连，离线事件进有界缓冲、重连后回放。

### 隔离与互不影响的实测事实

- 设备全局键是 `<edge_id>/<device_id>`；两台 Edge 各带多台设备时，命令按设备键路由，不串线。
- `edge` scope 的令牌**只能**连 `/ws/edge`；用它请求 REST（如 `/api/devices`）会得到 `403`。
- 一台 Edge 掉线不会踢掉另一台的连接，也不会改写对方的在线状态。
- 跨租户互相不可见：设备/事件/命令/插件实例都按 `tenant_id` 隔离。
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
| `-retention-days` | `CLOUDPATH_RETENTION_DAYS` | 30 | 事件/终态命令保留天数 |
| `-cmd-rate` | `CLOUDPATH_CMD_RATE` | 20 | 单设备每分钟命令上限，超限 429 |
| `-login-rate` | `CLOUDPATH_LOGIN_RATE` | 5 | 单 IP 每分钟登录尝试上限，超限 429 + Retry-After |
| `-session-days` | `CLOUDPATH_SESSION_DAYS` | 7 | 会话有效期（天） |
| `-setup-token` | `CLOUDPATH_SETUP_TOKEN` | 空 | 一次性首装令牌（非回环来源 setup 必带） |
| `-trusted-proxies` | `CLOUDPATH_TRUSTED_PROXIES` | 空 | 可信反代 CIDR；只有命中才采信 X-Forwarded-*（防伪造） |
| `-log-level` / `-log-format` | `CLOUDPATH_LOG` / `CLOUDPATH_LOG_FORMAT` | `info` / `text` | 日志级别 / text|json |

### edge（`edge.yaml`，本地私有不入库；母版 [edge.example.yaml](edge.example.yaml)）

| 字段 | 必填 | 默认 | 说明 |
|---|---|---|---|
| `server` | 是 | — | Edge 接入端点，末尾 `/ws/edge`；公网用 `wss://` |
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
| `plugin_host.state_dir` | 否 | `data/plugin-state` | 插件实例 desired-state 目录 |
| `plugin_host.tenant` | 否 | `default` | 外部 driver 实例租户 |
| `plugin_host.required` | 否 | false | true=host 失败则 edge 启动失败 |
| `plugin_host.lock` | 否 | `<root>/plugins.lock` | 锁文件（固定版本/digest/来源） |
| `plugin_host.close_timeout_s` | 否 | 10 | 优雅关闭 deadline |

> `plugin_host` 当前会按 desired-state 监督外部 driver 进程，但外部 driver 的
> handshake/descriptor/observation **尚未桥接进 edge 数据流**（只上报 unsupported，
> 不会伪装成在线设备）；内置 adapter 与外部 driver ID 冲突会拒绝启动。

### CLI（`cloudpath`）：插件 Registry 控制面

```bash
cloudpath plugin search <关键词>          # GitHub Topic 开放通道 + Registry 精选
cloudpath plugin inspect <id>             # manifest/兼容范围/摘要/权限披露
cloudpath plugin install <repo-or-id> --digest sha256:<hex> --yes
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

**Secret 边界（v0.1 的关键设计）**：

- **Server 只见 `secret://<name>` handle**，配置与审计里出现的都是 handle，不是值；
- **明文只存在于目标 Edge 本地**：由本地 provider 按 `<root>/<tenant>/<instance>/<name>`
  解析（[internal/secrethandle](internal/secrethandle/secrethandle.go)），不缓存、不落日志、
  不跨租户/实例读取；handle 名严格校验，无法编码路径或平台技巧；
- 插件必须在 manifest `permissions.secrets` 里**显式声明**才能解析对应 handle，未声明即 fail-closed；
- v0.1 **不做中心 Secret Store**：Server 不保存、不转发任何明文。

**输入与资源防护**：命令白名单由适配器声明；`args` ≤64 字节且不含换行/NUL；
`edge_id` 形状校验；设备归属校验（edge 只能上报自己注册过的键）；请求体
`MaxBytesReader(4096)`；WS 读上限（edge 64KB / 浏览器 4KB）；SPA 路径穿越防护；
命令与登录限流（429 + `Retry-After`）。

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
| `task test:web` / `test:templates` | 前端 frozen install 全链路 / Go 插件模板全链路 |
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
├── internal/       api（REST/WS 契约）· auth · server · edge · store(SQLite) · device
│                   pluginhost · plugincontrol · edgedriverhost · plugincatalog
│                   application · appruntime · registry · secrethandle · audit
│                   tenantpolicy · model · logx
├── sdk/go/         公开 Go SDK：cloudpath/v1/{application,driver,status} · driverkit
│                   model · pluginmain · pluginruntime · rpc · transport
├── proto/ spec/    versioned 协议与 manifest JSON Schema
├── examples/       stcb（参考 Driver）· scheduled-compartment（参考 Application）
├── templates/      go-plugin/{driver,application} 官方模板
├── testing/        plugin-harness（二进制→Host E2E）· plugin-fixtures
├── webui/          React 19 SPA（构建产物被 server 内嵌）
├── deploy/         systemd · nginx · edge 分发 · split 拆仓生成器
├── scripts/        门禁脚本（Python 3 stdlib only）
├── docs/           设计与契约 SSOT
└── firmware/       设备侧协议参考说明（不含厂商固件/库）
```

### 发布

打 `v*` tag 即触发 [.github/workflows/release.yml](.github/workflows/release.yml)：
6 平台矩阵构建（server 带 `-tags embed_ui`）→ release job 合并资产后用
`scripts/build_matrix.py --verify-only` 复验（含强制 linux/arm64 与 `embed_ui`）
并生成唯一 `checksums.txt` → 创建 GitHub Release。
产物命名与校验方式见 [CHANGELOG.md](CHANGELOG.md)。

---

## 当前真实能力 vs 尚未实现

> 目标态不冒充当前态。下表以基线代码与本机实测为准；不确定的一律指向 [docs/](docs/)。

**已实现（可依赖）**

- 单二进制 server（内嵌 WebUI）+ edge + 插件 CLI；全链路 WebSocket 实时链路；
  命令闭环（pending→sent→ok/failed/timeout）与事件/命令 SQLite 持久化、保留期清理。
- 账号模式：setup/login/logout/me、会话 cookie、RBAC（admin/operator/viewer）、
  用户管理、租户令牌（scopes `read|write|admin|edge`，明文一次）；审计日志
  （actor/tenant/action/outcome/request_id/remote_ip）。
- 设备监督（拔插退避重开）、离线事件有界缓冲与重连回放、断线指数退避重连、
  重启后从 SQLite 水合（一律先标离线，等 edge 重新上报）。
- 参考 Driver `stcb`（进程内 blank import，命令白名单 `sync/dump/trigger/open/isp/raw`）与内置
  参考演示适配器 `demo`（无硬件，`ping/set/dump/noop`，server/edge 双端同源注册，`/api/adapters` 与白名单同一事实源）。
- 参考 Application `scheduled-compartment`（进程式插件，Plugin Host 拉起，只依赖公开 SDK）
  与 [deploy/split/](deploy/split/README.md) 独立仓生成器。
- 外部 Driver Plugin Host：desired-state + `plugins.lock` 监督插件进程；
  Registry CLI 的 search/inspect/install/enable/disable/update/remove/host 与信任锚校验。
- 插件控制面全链路：Server desired 权威（写面 REST + 9 个稳定错误码 + RBAC/配额/审计/WS
  同链路推送）→ Edge reconcile（单调 revision、幂等 ack、离线跑 last-applied、重连只收敛最终
  快照）→ observed 上报投影（脱敏）→ UI desired/observed 双栏诚实呈现（drift/stale/last-ack）。
- `GET /api/overview` 聚合读面与 WebUI Overview/Activity/Plugins 产品信息架构；
  账号密码登录（会话 cookie，实时通道跟随登录态）与 390px 窄屏视觉守卫测试。
- `secret://<name>` handle 边界（本地 provider、租户/实例隔离、未声明 fail-closed；明文 secret
  永不进 Server DB / WS / 审计 / 日志 / UI）。
- WebUI：概览/设备/详情/活动/插件（目录·实例·desired/observed）/边缘/设置 + 管理页
  （用户、令牌、一次性令牌明文面板），浅色/深色双主题与 390px 窄屏收口。
- 发布工程：全平台构建矩阵、架构断言门禁、CI（Linux+Windows）、Release + checksums、
  systemd/nginx 部署物料与 SOP。

**尚未实现 / 目标态（不要当现状使用）**

- **外部 Driver 的 handshake/descriptor/observation 桥接进 Edge 数据流**：Plugin Host 能监督
  外部插件进程，但外部 Driver 的设备数据流尚未桥接（上报 unsupported）；进程内参考 Driver
  `stcb`/`demo` 不受影响。
- **令牌会话的实时通道**：用租户服务令牌登录的 WebUI 只有 REST，无 `/ws` 实时推送
  （浏览器 WebSocket 无法携带自定义 header）；账号密码会话功能完整。v0.1 接受此限制，UI 诚实呈现。
- **中心 Secret Store**：v0.1 明确不做（见上）；secret 一律 `secret://<name>` handle + Edge 本地
  provider 解析，未来可替换 Vault/KMS 而不改 desired 协议。
- **插件独立仓 Release**：药盒 Application 插件已拆入独立仓
  [`cloud-path-app-scheduled-compartment`](https://github.com/DeliciousBuding/cloud-path-app-scheduled-compartment)
  （[deploy/split/](deploy/split/README.md) 生成器生成）；独立 Release 资产发布待执行。
  STC-B Driver 与 Registry 客户端按当前决策**不拆仓**，留在主仓。
- MQTT/Modbus 等协议接入、远程 OTA 编排、时序聚合与业务分析（P2–P4 规划，见
  [docs/architecture.md](docs/architecture.md)）。

---

## 文档地图

| 文档 | 内容 |
|---|---|
| [docs/design.md](docs/design.md) | 技术 SSOT：技术栈、进程模型、存储、前端、安全、测试 |
| [docs/architecture.md](docs/architecture.md) | 架构总览与「当前实现 vs 目标态」 |
| [docs/protocol.md](docs/protocol.md) | Edge ↔ Server 线上协议契约（信封、消息、DTO） |
| [docs/api.md](docs/api.md) | REST/WS 契约、鉴权三级模型、限流与安全头 |
| [docs/security.md](docs/security.md) | 安全与运维基线（L0/L1/L2、令牌、检查表） |
| [docs/deploy.md](docs/deploy.md) | 部署指南（本地/容器/反代） |
| [docs/architecture/plugin-system.md](docs/architecture/plugin-system.md) | 插件运行时与协议契约 |
| [docs/architecture/capability-model.md](docs/architecture/capability-model.md) | Device/Entity/Capability 模型 |
| [docs/architecture/control-plane-sync.md](docs/architecture/control-plane-sync.md) | 声明式快照 + 单调 revision 同步语义 |
| [docs/architecture/tenant-security-policy.md](docs/architecture/tenant-security-policy.md) | 租户配额、保留期与 secret 边界 |
| [docs/architecture/repository-strategy.md](docs/architecture/repository-strategy.md) | 仓库组合、命名、拆仓门与公开边界 |
| [deploy/README.md](deploy/README.md) | 公网落地 SOP（systemd + nginx + WSS） |
| [deploy/edge/README.md](deploy/edge/README.md) | 客户端分发与 `edge.yaml` 填写指引 |

## 许可

MIT © Cloudpath Authors — 见 [LICENSE](LICENSE)。
