# Cloudpath 技术设计（P1 实现版）

本文是 Cloudpath 的技术 SSOT：技术栈、进程模型、目录、契约、存储、前端、安全、测试与里程碑。
设备侧协议契约见 [protocol.md](protocol.md)；面向使用者的说明见根 [README.md](../README.md)。

> 状态：P1（M0–M4）已实现并通过测试与真机验证。文末「实现偏差记录」列出与最初设计稿的差异。

## 目标 / 非目标

**P1 目标**：插上一台串口设备 → edge 解析状态 → WebSocket 实时上报 server → 管理台看到设备卡片
（时钟/漂移/业务状态/事件流），可下发命令（对时/转储/触发），事件与命令持久化到 SQLite，
单二进制部署（webui 内嵌 server）。

**非目标（P1 不做）**：公网多租户与用户体系（P2）、MQTT 接入（P2）、远程 OTA 编排（P3）、
时序聚合与业务分析（P4）。架构为它们留扩展点（见文末）。

## 技术栈

| 层 | 选型 | 理由 |
|---|---|---|
| 语言 | Go 1.26 | 单二进制、跨平台交叉编译（server 可直接上 VPS/arm64） |
| HTTP 路由 | chi v5 | 轻量惯用，中间件生态好 |
| WebSocket | coder/websocket | context 原生、活跃维护；edge↔server 与 server↔浏览器共用 |
| 存储 | SQLite（modernc.org/sqlite） | 纯 Go 零 CGO，交叉编译无负担；WAL + busy_timeout |
| 日志 | stdlib `log/slog` | 结构化日志，零依赖，支持 text/json |
| 串口 | go.bug.st/serial | 跨平台、无 CGO |
| 前端 | React 19 + TypeScript + Vite 6 | 现代 SPA 标准栈，`strict` + `noUnusedLocals` |
| 路由 | React Router 7 | 声明式 SPA 路由 + 路由级懒加载 |
| 样式 | Tailwind CSS 4 + 自建设计系统 | CSS 变量主题（浅/深）、手写原语组件，无组件库依赖 |
| 数据层 | TanStack Query 5（REST）+ zustand 5（WS 实时态） | 查询缓存与实时推送分离 |
| 图表 | recharts | 漂移趋势（按需加载 chunk） |
| 图标 | lucide-react | 线性图标，随 `currentColor` |
| 包管理 | pnpm | 快、磁盘友好 |
| 任务编排 | Taskfile.yml（go-task） | 跨平台 dev/build/test/lint 一键 |

## 进程模型与数据流

```
设备(串口) ──> cloudpath-edge(每站点一个)
                  │  适配器解析协议 → 状态/事件
                  │  设备监督协程（拔插退避重开）+ 轮询/对时调度
                  │  WS 长连接（上报 state/event，接收 command，断线指数退避重连 + 事件缓冲）
                  ▼
             cloudpath-server(中心，单二进制)
                  │  ├─ WS hub：edge 连接池 + 浏览器订阅 fan-out
                  │  ├─ REST API：设备/事件/命令/边缘/适配器/统计
                  │  ├─ SQLite：devices / device_state / events / commands
                  │  ├─ 后台维护：命令超时 sweeper + 保留期清理
                  │  └─ embed：webui/dist 静态资源（SPA fallback）
                  ▼
             React 管理台（浏览器，WS 实时 + REST 历史）
```

要点：**全链路 WebSocket**（edge→server→浏览器），状态变化秒级到达面板；REST 只承担历史查询与
管理操作。命令走 server→edge 的 WS 下行，带 ack 回执落库，前端按 `command_id` 结算。

## 目录结构

```
cloudpath/
├── go.mod                      # module github.com/DeliciousBuding/cloud-path
├── Taskfile.yml                # setup/build/dev/test/lint/run/clean
├── cmd/
│   ├── cloudpath-server/main.go
│   └── cloudpath-edge/main.go
├── internal/
│   ├── api/                    # REST/WS 共享类型（信封、消息、DTO）——两侧唯一契约
│   ├── device/                 # Device/Adapter 接口 + 注册表（设备无关核心）
│   ├── edge/                   # 运行时：设备监督、轮询/对时、WS 客户端、离线事件缓冲
│   ├── server/                 # chi 路由、WS hub、命令下发与限流、保留期清理、webui embed
│   ├── store/                  # SQLite：schema 迁移(user_version)、devices/state/events/commands
│   └── logx/                   # slog 初始化
├── examples/
│   └── stcb/                   # 参考适配器：STC-B 学习板（协议 + 解析器 + 黄金样本单测）
├── webui/                      # React SPA（Vite），构建产物被 server 内嵌
│   └── src/{pages,components,hooks,store,lib}
├── firmware/                   # 设备侧协议参考说明（不含任何厂商固件/库）
├── scripts/                    # 跨平台开发脚本（gofmt 门禁、清理），Python 3 stdlib
└── docs/                       # design.md(本文) + protocol.md
```

## 设备抽象（核心不变量）

```go
// internal/device
type State struct {
    Online    bool
    Raw       map[string]any // 适配器自定义语义，原样透传到前端与存储
    UpdatedAt time.Time
}

type Device interface {
    ID() string
    Snapshot() State                                  // 必须并发安全
    Send(ctx context.Context, c Command) error        // 命令执行
    Done() <-chan struct{}                            // 端口致命错误（拔线）通知
    Close() error
}

type Adapter interface {                              // examples/stcb 实现
    Name() string
    SupportedCommands() []string                      // 命令白名单（server 据此拒绝未知命令）
    Open(ctx context.Context, cfg Config, onEvent func(Event)) (Device, error)
}
// 注册表：适配器以 init() 注册，edge 按配置名实例化，server 用它校验白名单
// → 新增设备不改核心，前端命令面板自动跟随 GET /api/adapters
```

约定：`Raw` 里的通用键（`clock` `hour` `min` `state` `state_label` `slots` `drift_min`
`dump_raw`）由前端直接展示；未知键原样进「原始状态」面板。核心与前端都不对具体设备做分支判断。

## API 契约

### WS 消息信封（edge↔server、server↔浏览器统一）

```json
{ "v": 1, "type": "hello|snapshot|state|event|command|command_ack|edge_up|edge_down",
  "device": "lab-1/demo-1", "ts": 1788408968, "data": { } }
```

| type | 方向 | data | 说明 |
|---|---|---|---|
| `hello` | edge→server | `{edge_id, token, version, devices:[{id,adapter,name,port}]}` | 首帧，鉴权 + 注册 |
| `snapshot` | server→浏览器 | `{devices[], edges[]}` | 浏览器连接首帧全量快照 |
| `state` | edge→server→浏览器 | `{online, raw, updated_at}` | 状态快照（diff 抑制 + 心跳兜底） |
| `event` | edge→server→浏览器 | `{type, label?}` | 设备事件，落库 `events` |
| `command` | server→edge | `{command_id, cmd, args}` | 浏览器经 REST 触发 |
| `command_ack` | edge→server→浏览器 | `{command_id, status, detail}` | 更新 `commands` 并广播 |
| `edge_up` / `edge_down` | server→浏览器 | `{edge_id, devices[], version}` | 边缘节点上下线 |

浏览器连接 `/ws` 订阅全量 fan-out（P1 单租户，不做按设备订阅过滤）。协议版本不匹配（`v`）的消息
被丢弃并告警。

### REST

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/healthz` | 版本、运行时长、在线设备/边缘计数（存活探针） |
| GET | `/api/devices` | 设备列表 + 最新状态（内存态） |
| GET | `/api/devices/{edgeID}/{deviceID}` | 单设备详情；设备键含 `/`，故用两段路径参数 |
| POST | `/api/devices/{edgeID}/{deviceID}/commands` | 下发命令 `{cmd,args}` → 建 `commands` 行 → WS 推给 edge |
| GET | `/api/events?device=&since=&limit=` | 事件历史（新→旧，limit 默认 100 上限 1000） |
| GET | `/api/commands?device=&status=&limit=` | 命令与 ack 状态 |
| GET | `/api/edges` | 边缘节点：在线连接 + 曾接入的离线节点（按设备表反推） |
| GET | `/api/adapters` | 已注册适配器与命令白名单（前端命令面板事实源） |
| GET | `/api/stats` | 事件/命令/设备计数、最早事件、保留期、schema 版本、鉴权状态 |
| GET | `/ws` | 浏览器实时订阅（可选 `?token=`） |
| GET | `/ws/edge` | 边缘接入 |
| GET | `/*` | 内嵌前端（SPA fallback；含路径穿越防护） |

错误体统一 `{"error":"…"}`；状态码语义：400 参数/白名单、401 令牌、404 设备不存在、
409 edge 离线、429 命令限流、503 存储不可用或 edge 队列满。

## SQLite Schema（`PRAGMA user_version` 逐级迁移）

```sql
-- v1（schema.sql）
devices(id PK, edge_id, adapter, name, port, meta, first_seen, last_seen)
device_state(device_id PK → devices.id, state JSON, online, updated_at)
events(id PK AUTOINCREMENT, device_id, ts, type, payload JSON)
commands(id PK AUTOINCREMENT, device_id, cmd, args, status, created_at, acked_at, result)
INDEX idx_events_device_ts(device_id, ts), idx_commands_status(status, created_at)

-- v2（schema_v2.sql）：补齐检索索引
INDEX idx_events_ts(ts)                          -- ?since= 与保留期清理
INDEX idx_commands_device(device_id, created_at) -- 设备详情页命令历史
```

迁移是有序表（`internal/store/store.go` 的 `migrations`）：新增版本追加一项，**永不修改已发布项**。
连接池上限 4 + WAL + `busy_timeout(5000)`，避免 `database is locked`。

## 并发与稳定性不变量

改 `internal/server`、`internal/edge` 前先读这一节，这些是被测试锁住的设计：

1. **锁内不做磁盘 I/O**：`s.mu` 只保护内存态；落库在锁内收集数据、锁外 `persistXxx`。
   广播不会被写库拖慢，写库失败也不影响实时链路。
2. **Store 可为 nil**（API-only 模式）：所有落库路径先判空，端点返回空集合而非 panic。
3. **慢消费者不阻塞**：浏览器/edge 的发送都是带缓冲 chan + `select default` 丢帧；
   状态可从 REST 补，事件在 edge 侧有离线缓冲。
4. **半开连接收敛**：双向 Ping 保活（30s），Ping 失败即 `cancel()` 会话 ctx，
   读循环退出触发清理与重连。
5. **重连挤占语义**：同 `edge_id` 新连接上线时旧连接被 cancel；旧连接的清理回调发现
   自己已不是注册连接（`was_current=false`）就**不把设备标离线**。
6. **edge 自愈**：串口打开失败指数退避（1s→30s）；拔线 3 秒防抖后重开；
   设备打开后立即 `sync` + `dump`（掉电后 RTC 需要重新对时）。
7. **断线不丢事件**：离线期间事件进有界缓冲（512 条，超限丢最旧），重连后回放；
   状态消息幂等，重连即强制补报一次（`onServerOnline`）。
8. **命令闭环**：`pending → sent → ok|failed`；90 秒未回执由 sweeper 标 `timeout`；
   前端按钮跟踪 `command_id` 的 ack，另有 15 秒超时兜底提示。
9. **重启不空白**：server 启动从 SQLite 水合设备与最后状态，一律标离线，等 edge 重新上报。
10. **输入收口**：命令白名单（适配器声明）、参数长度 ≤64 且不含换行/NUL、
    `edge_id` 形状校验（字母数字 `-_`，1–64）、设备归属校验（edge 只能上报自己注册过的键）、
    请求体 `MaxBytesReader(4096)`、WS 读上限（edge 64KB / 浏览器 4KB）、SPA 路径穿越防护。

## 安全边界

- server 默认只绑 `127.0.0.1`；公网部署必须置于自有反代 + TLS 之后。
- `CLOUDPATH_TOKEN` 一启用：edge hello 校验、浏览器写操作 Bearer 校验、浏览器 WS `?token=` 校验。
  令牌只经环境变量/配置注入（`edge.yaml` 支持 `${ENV}` 展开），不入库。
- WS Origin 策略：`CLOUDPATH_ALLOWED_ORIGINS` 显式清单（公网形态）；留空 = 开发策略
  （请求自身 host 始终放行 + `localhost:*`/`127.0.0.1:*`/`[::1]:*`），启动时告警提示收紧。
  非浏览器客户端不带 Origin，不受影响。
- 命令限流：单设备默认 20 次/分钟，超出 429（防跑飞的 UI/脚本刷串口）。
- 数据保留：事件与终态命令默认保留 30 天，后台每小时清理（在途命令不清）。
- 设备清单/串口路径/令牌全部本地配置注入（`edge.yaml`、`.local/` 均 gitignored）。

## 前端（React Router 7）

| 路由 | 页面 | 内容 |
|---|---|---|
| `/` | 概览 | 统计卡（在线设备/边缘/今日事件/服务运行）、设备卡栅格、实时事件面板 |
| `/devices` | 设备 | 全部设备卡片（WS 快照优先，REST 轮询兜底） |
| `/devices/:edgeId/:deviceId` | 设备详情 | 大时钟 + 漂移/时/分、命令面板（白名单驱动 + raw 输入）、槽位、漂移趋势、事件时间线、命令历史、原始状态 |
| `/events` | 事件 | 设备/类型筛选、WS 实时 + REST 历史合并去重、上限提示 |
| `/edges` | 边缘节点 | 在线/离线节点、版本、最后在线、所辖设备跳转 |
| `/settings` | 系统 | 服务状态、实时连接、令牌、存储统计、适配器清单、关于 |

设计系统（`webui/src/index.css`）：CSS 变量主题（浅色 `#f5f5f7` 画布 / 深色纯黑画布 +
iOS 系统语义色），`.dark` class 切换 + 首帧前内联脚本防闪白，毛玻璃侧栏/顶栏、卡片阴影层级、
`tabular-nums` 数字等宽、骨架屏微光、`prefers-reduced-motion` 降级、`:focus-visible` 焦点环。

状态管理：zustand 持有 WS 实时快照（设备 map + 事件环形缓冲 300 条 + 会话级漂移历史 240 点 +
ack map）；TanStack Query 管 REST（设备/事件/命令/统计）。`store/ws.ts` 是单例连接，
自带指数退避重连（1→15s + 抖动）与令牌变更重连。

数据获取约定：页面**同时**消费实时层与 REST（`useDevices`/`useEdges` 合并两者），
因此实时通道断开时面板仍可用；事件流用 `mergeEvents` 按 `设备+时间+类型` 去重合并。

开发态：Vite dev server（:5173）代理 `/api` `/ws` `/healthz` 到 :8080；
生产态：`vite build` → `webui/dist` → `go:embed`（构建标签 `embed_ui`，未启用时有 stub 兜底，
server 退化为 API-only 并返回可读提示）。

## 配置与环境

**server**（flag 与环境变量等价，flag 优先）：

| flag | env | 默认 |
|---|---|---|
| `-addr` | `CLOUDPATH_ADDR` | `127.0.0.1:8080` |
| `-db` | `CLOUDPATH_DB` | `data/cloudpath.db` |
| `-token` | `CLOUDPATH_TOKEN` | 空（无鉴权） |
| `-webui` | `CLOUDPATH_WEBUI` | 空（用内嵌产物） |
| `-allowed-origins` | `CLOUDPATH_ALLOWED_ORIGINS` | 空（开发策略） |
| `-retention-days` | `CLOUDPATH_RETENTION_DAYS` | `30` |
| `-cmd-rate` | `CLOUDPATH_CMD_RATE` | `20` |
| `-log-level` / `-log-format` | `CLOUDPATH_LOG` / `CLOUDPATH_LOG_FORMAT` | `info` / `text` |

**edge**（`edge.yaml`，本地私有不入库；仓库带 `edge.example.yaml`）：

```yaml
server: ws://127.0.0.1:8080/ws/edge
token: ${CLOUDPATH_TOKEN}      # 支持 ${ENV} 展开
edge_id: lab-1                 # 缺省用主机名（点号归一为 -）
poll_interval_s: 5             # 转储轮询
sync_interval_s: 600           # 周期对时
report_interval_s: 30          # 状态心跳兜底
devices:
  - id: demo-1
    adapter: stcb
    name: 参考板
    port: COM3                 # Linux: /dev/ttyUSB0
    baud: 9600
```

配置校验在启动时完成并给出可执行错误信息（缺 id/adapter/port、id 重复、协议前缀错误、
devices 为空），运行中不热加载（P1 有意为之：热加载与串口生命周期纠缠，收益低风险高）。

## 测试策略

| 层 | 文件 | 覆盖 |
|---|---|---|
| 协议解析 | `examples/stcb/parser_test.go` | 黄金样本（真实捕获行：损坏分隔符、噪声前缀、越界值）、事件归一、漂移回绕、HHMM 校验、标签 |
| 存储 | `internal/store/store_test.go` | 迁移到当前版本 + 幂等、设备/状态生命周期、事件过滤、命令过滤与超时、保留期清理（不误删在途命令）、统计、limit 夹取 |
| 服务链路 | `internal/server/server_test.go` | WS 全链路（快照→hello→state fan-out+落库→REST 命令→edge 收令→ack 落库+广播→白名单拒绝）、令牌鉴权、重启水合、healthz |
| 服务加固 | `internal/server/hardening_test.go` | 适配器/统计端点、nil-store 不 panic、命令限流、参数校验、未知设备与离线 edge、命令设备过滤、查询参数夹取、保留期、edge_id 校验、重连挤占不误标离线、安全头、SPA 回落与路径穿越 |
| Origin 策略 | `internal/server/origin_test.go` | 开发策略放行 localhost/无 Origin、拒绝外站；显式清单生效且防后缀伪装 |
| 边缘运行时 | `internal/edge/{config_test.go,wsclient_test.go}` | 配置默认值/`${ENV}` 展开/各类错误、离线只缓冲事件、在线入队、队满回落缓冲、缓冲溢出丢最旧、回放（含部分回放）、状态 diff 抑制与心跳兜底、重连强制补报 |
| 前端 | `pnpm exec tsc --noEmit` | 类型门禁（`strict` + `noUnusedLocals`） |
| e2e | 真机手工清单 | 见下；证据记录在 `.local/STATE.md`（不进公开仓库） |

真机验收清单（一台接串口的设备即可跑完）：

1. `task build` 出双二进制；起 server → 打开 :8080 内嵌管理台可见（无设备时是空状态而非报错）。
2. 起 edge → 管理台出现 edge 与设备，时钟/漂移/槽位有值，`GET /api/devices` 与之一致。
3. `POST …/commands {"cmd":"sync"}` → 200 `sent`，随后 ack `ok`，设备事件流出现 `SYNC-OK`，
   漂移回到 ±1 分钟。
4. `{"cmd":"trigger"}` / `{"cmd":"open"}` → 设备侧动作 + 对应事件入库并在页面实时出现。
5. 拔掉 USB → 设备在页面变离线（edge 退避重开），插回 → 自动恢复在线并重新对时。
6. 杀掉 server → edge 日志显示退避重连，期间产生的事件在重连后回放（页面能补看到）。
7. 重启 server → 设备仍在列表（水合），状态为离线，edge 重连后恢复。
8. 未知命令 → 400；高频下发 → 429；`-token` 启用后无令牌写操作 → 401。

## 里程碑

| # | 内容 | 验证门 | 状态 |
|---|---|---|---|
| M0 | monorepo 骨架：go.mod、cmd 双入口、Taskfile、webui scaffold、.gitignore | `task build` 出双二进制 | ✅ |
| M1 | `internal/api` 类型 + device 抽象 + `examples/stcb` 适配器与解析器 | `go test ./...` 绿（黄金样本含损坏行） | ✅ |
| M2 | server（chi + hub + SQLite）+ edge（串口 + WS + 轮询/对时） | 真机接入：REST 见设备、事件落库、WS fan-out、sync 后漂移≈0 | ✅ |
| M3 | React 管理台五页 + embed 单二进制 | 浏览器看真机实时状态；命令全链路生效 | ✅ |
| M4 | 加固：ack 跟踪、拔插重连、slog、漂移趋势、限流、保留期、Origin 策略、README、tag v0.1.0 | `task test` 全绿 + 真机清单 | ✅ |

## 扩展点（P2+）

- **接入协议**：`internal/edge` 之外再加一个 MQTT/HTTP 接入网关，复用同一 `api.Envelope`
  与 hub（新增 `internal/gateway`），设备抽象不变。
- **多租户与鉴权**：`authWrite` 已是单点，替换为 JWT/会话中间件即可；`devices` 表加 `tenant_id`
  走 v3 迁移。
- **按设备订阅**：浏览器 WS 增加 `subscribe` 消息类型，`broadcast` 改为按订阅集分发。
- **告警通道**：事件落库处已是单点（`handleEdgeWS` 的 `MsgEvent` 分支），挂一个 notifier 即可。
- **时序聚合**：`events` 表按天聚合到 `rollups` 表（v4 迁移），sweeper 里加一个任务。
- **OTA**：`isp` 命令已经能把设备送进烧录模式，补一个固件分发 + 进度上报的命令族。

## 实现偏差记录（相对最初设计稿）

| 设计稿 | 实际实现 | 原因 |
|---|---|---|
| `GET /api/devices/{id}` 单段路径 | `/api/devices/{edgeID}/{deviceID}` 两段 | 设备键本身含 `/`，单段需要转义，两段更直白 |
| `GET /api/devices/{id}/events` | `GET /api/events?device=` | 事件是全局资源，统一入口便于跨设备查询与筛选 |
| 样式用 shadcn/ui | 自建原语组件（`components/ui.tsx`）+ CSS 变量 | shadcn 引入 radix 全家桶与额外约定；本项目只需 Badge/Panel/StatTile 等少量原语，自建更轻且主题可控 |
| Vite 7 | Vite 6 | 定稿时 Vite 7 尚未发布/生态未跟上；6 已满足需求 |
| `examples/pillbox` 承载业务语义 | 不设业务示例包，语义留在适配器的标签层 | 核心与示例都保持行业无关，避免平台绑定具体业务 |
| 前端命令按钮硬编码 | 由 `GET /api/adapters` 白名单驱动 | 新增适配器零前端改动，且与 server 校验同源 |
| 未规划保留期/限流/Origin 策略 | 三者均已实现并测试 | 长时间运行与对外暴露的实际需要 |
| 未规划离线事件缓冲 | edge 侧有界缓冲 + 重连回放 | 事件不可重放，server 短暂不可用不应丢数据 |

## 约束（硬）

1. **不含任何第三方厂商固件/SDK/库/课件**：`firmware/` 只放协议参考说明；设备侧代码不进本仓库。
2. **核心设备/行业无关**：具体语义只存在于 `examples/<device>` 适配器。
3. **私有信息不入库**：构想、设备清单、验证证据只写 `.local/`（gitignored）。
4. **契约三处同步**：`internal/api/types.go` ↔ `webui/src/lib/types.ts` ↔ 本文档。
