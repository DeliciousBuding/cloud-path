# Cloudpath 技术设计（P1 定稿）

本文是 P1 实现的技术 SSOT：技术栈、进程模型、目录、API 契约、存储、前端、安全、测试与
里程碑。设备协议契约见 [protocol.md](protocol.md)。

## 目标 / 非目标

**P1 目标**：插上一台串口设备 → edge 解析状态 → WebSocket 实时上报 server → React 管理台
看到设备卡片（时钟/漂移/业务状态/事件流），可下发命令（对时/转储/触发），事件与命令持久化
到 SQLite。单二进制部署（webui 内嵌 server）。

**非目标（P1 不做）**：公网暴露与多租户鉴权（P2+）、MQTT（P2）、远程 OTA 刷机（P3）、
依从性分析（P4）。架构上为它们留好扩展点。

## 技术栈（全部现代选型）

| 层 | 选型 | 理由 |
|---|---|---|
| 语言 | Go 1.26 | 单二进制、跨平台交叉编译（server 未来直接上 VPS/arm64） |
| HTTP 路由 | chi v5 | 轻量惯用，中间件生态好 |
| WebSocket | coder/websocket | context 原生、活跃维护，edge↔server 与 server↔浏览器共用 |
| 存储 | SQLite（modernc.org/sqlite） | 纯 Go 零 CGO，交叉编译无负担；事件溯源式持久化 |
| 日志 | stdlib log/slog | 结构化日志，零依赖 |
| 串口 | go.bug.st/serial | 跨平台、无 CGO |
| 前端 | React 19 + TypeScript + Vite 7 | 现代 SPA 标准栈 |
| 路由 | React Router 7 | 声明式 SPA 路由 |
| 样式/组件 | Tailwind CSS 4 + shadcn/ui | 高级感管理台的最短路径 |
| 数据层 | TanStack Query 5（REST 历史）+ zustand（WS 实时态） | 查询缓存与实时推送分离 |
| 图表 | recharts | 漂移趋势/事件频率（M4） |
| 包管理 | pnpm | 已在机器上，快 |
| 任务编排 | Taskfile.yml（go-task） | 跨平台 dev/build/test 一键 |

## 进程模型与数据流

```
设备(串口) ──> cloudpath-edge(每站点一个)
                  │  适配器解析协议 → 状态/事件
                  │  WS 长连接（上报 state/event，接收 command，断线指数退避重连）
                  ▼
             cloudpath-server(中心，单二进制)
                  │  ├─ WS hub：edge 连接池 + 浏览器订阅 fan-out
                  │  ├─ REST API：设备/事件历史/命令下发
                  │  ├─ SQLite：devices / device_state / events / commands
                  │  └─ embed：webui/dist 静态资源
                  ▼
             React 管理台（浏览器，WS 实时 + REST 历史）
```

要点：**全链路 WebSocket**（edge→server→浏览器），状态变化秒级到达面板；REST 只承担
历史查询与管理操作。命令走 server→edge 的 WS 下行，带 ack 回执落库。

## 目录结构

```
cloudpath/
├── go.mod                      # module github.com/DeliciousBuding/cloudpath
├── Taskfile.yml
├── cmd/
│   ├── cloudpath-server/main.go
│   └── cloudpath-edge/main.go
├── internal/
│   ├── api/                    # REST/WS 共享类型（信封、消息、DTO）——两侧唯一契约
│   ├── device/                 # Device/Adapter 接口 + 注册表（设备无关核心）
│   ├── edge/                   # 运行时：设备管理、轮询/对时调度、WS 客户端、重连
│   ├── server/                 # chi 路由、WS hub、聚合、命令下发、webui embed
│   ├── store/                  # SQLite：schema 迁移(user_version)、devices/events/commands
│   └── logx/                   # slog 初始化
├── examples/
│   ├── stcb/                   # STC-B 适配器：串口协议实现 + 解析器 + 单测（黄金样本）
│   └── pillbox/                # 业务叠加：槽位语义/漂移展示（reference application）
├── webui/                      # React SPA（Vite），构建产物被 server embed
│   └── src/{pages,components,hooks,lib}
├── firmware/                   # 仅协议参考说明；课程固件/BSP 库绝不入库（见约束）
└── docs/                       # design.md(本文) + protocol.md + grill/
```

## 设备抽象（核心不变量）

```go
// internal/device
type State struct {
    Online   bool              `json:"online"`
    Raw      map[string]any    `json:"raw"`       // 适配器自定义状态（时钟/槽位…）
    UpdatedAt time.Time        `json:"updated_at"`
}

type Device interface {
    ID() string
    Snapshot() State
    Send(ctx context.Context, cmd Command) error
    Close() error
}

type Adapter interface {          // examples/stcb 实现
    Name() string
    Open(cfg DeviceConfig) (Device, error)
}
// 注册表：adapter 以 init() 注册，edge 按配置名实例化 —— 新增设备不改核心
```

## API 契约

### WS 消息信封（edge↔server、server↔浏览器统一）

```json
{ "v": 1, "type": "hello|state|event|command|command_ack|ping|pong",
  "device": "desk-1/bed1", "ts": 1788408968, "data": { } }
```

- `hello`（edge→server）：`{edge_id, token, devices:[{id, adapter}]}`，鉴权+注册
- `state`（edge→server→浏览器）：设备状态快照（全量，低频 diff 抑制）
- `event`（edge→server→浏览器）：设备事件 `{type, label}`，落库 events 表
- `command`（server→edge）：`{cmd:"sync|dump|raw", args}`，浏览器经 REST 触发
- `command_ack`（edge→server）：`{command_id, status:"sent|ok|failed", detail}`，更新 commands 表
- 浏览器连接 `/ws` 订阅全量 fan-out（P1 单租户，不做按设备订阅过滤）

### REST（chi，前缀 /api）

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | /api/devices | 设备列表 + 最新状态 |
| GET | /api/devices/{id} | 单设备详情 |
| GET | /api/devices/{id}/events?since=&limit= | 事件历史（分页） |
| POST | /api/devices/{id}/commands | 下发命令 `{cmd,args}` → 建 commands 行，WS 推给 edge |
| GET | /api/edges | 在线 edge 列表 |
| GET | /api/commands?status=pending | 命令与 ack 状态 |
| GET | /healthz | 存活探针 |
| GET | /ws | 浏览器实时订阅 |

## SQLite Schema（v1，PRAGMA user_version 迁移）

```sql
CREATE TABLE devices(
  id TEXT PRIMARY KEY, edge_id TEXT, adapter TEXT, name TEXT,
  meta TEXT DEFAULT '{}', first_seen INTEGER, last_seen INTEGER);
CREATE TABLE device_state(
  device_id TEXT PRIMARY KEY, state TEXT, updated_at INTEGER);
CREATE TABLE events(
  id INTEGER PRIMARY KEY AUTOINCREMENT, device_id TEXT, ts INTEGER,
  type TEXT, payload TEXT DEFAULT '{}');
CREATE TABLE commands(
  id INTEGER PRIMARY KEY AUTOINCREMENT, device_id TEXT, cmd TEXT, args TEXT,
  status TEXT DEFAULT 'pending',  -- pending|sent|ok|failed|timeout
  created_at INTEGER, acked_at INTEGER, result TEXT);
CREATE INDEX idx_events_device_ts ON events(device_id, ts);
```

## 前端（React Router 7 路由）

| 路由 | 页面 | 内容 |
|---|---|---|
| `/` | Dashboard | 设备卡片栅格（WS 实时）、在线统计、最近事件流 |
| `/devices/:id` | 设备详情 | 时钟+漂移仪表、业务状态（如药盒三槽）、命令面板、事件时间线（REST 历史+WS 增量） |
| `/events` | 全局事件 | 过滤（设备/类型/时间）、分页 |
| `/edges` | 边缘节点 | edge 在线状态、所辖设备、重连记录 |
| `/settings` | 系统 | 版本/运行时长/配置摘要（P1 只读） |

状态管理：zustand 持有 WS 实时快照（device map + 事件环形缓冲）；TanStack Query 管
REST 历史（事件分页、命令状态轮询）。WS hook 自带断线重连与心跳。

开发态：Vite dev server 代理 `/api` 与 `/ws` 到 :8080；生产态 `vite build` → `webui/dist`
→ `go:embed` 进 server 二进制。

## 配置与环境

**server**（env/flags）：`CLOUDPATH_ADDR`（默认 127.0.0.1:8080）、`CLOUDPATH_DB`
（默认 data/cloudpath.db）、`CLOUDPATH_TOKEN`（设置后 edge/写操作强制鉴权）。

**edge**（edge.yaml，本地私有不入库；仓库带 edge.example.yaml）：

```yaml
server: ws://127.0.0.1:8080/ws/edge
token: ${CLOUDPATH_TOKEN}
edge_id: desk-1
poll_interval_s: 5
sync_interval_s: 600
devices:
  - id: bed1
    adapter: stcb
    port: COM3        # Windows；Linux 为 /dev/ttyUSB0
    baud: 9600
```

## 安全边界

- server 默认只绑 127.0.0.1；公网暴露（P2）必须置于自有反代+鉴权后。
- `CLOUDPATH_TOKEN` 一启用：edge hello 校验、浏览器写操作（命令下发）校验；P1 本机开发可空。
- 设备清单/串口路径/token 全部本地配置注入，不入库（`.local/` 与 edge.yaml 均 gitignored）。
- 命令白名单：适配器声明支持的命令集，server 拒绝未知命令（防注入）。

## 测试策略

| 层 | 手段 |
|---|---|
| 协议解析（examples/stcb） | 单测 + 黄金样本（真实串口捕获行，含损坏行/NUL/NOEOL） |
| store | SQLite :memory: 全量 CRUD/迁移测试 |
| server hub | httptest + websocket 拨号：fan-out、ack 状态机 |
| edge | 假串口（io.Pipe 实现 Port 接口）注入，测轮询/重连/命令执行 |
| e2e | 真板手工验证清单（记录在 .local/STATE.md，不进公开仓库） |

## 里程碑（每步可验证、单独提交）

| # | 内容 | 验证门 |
|---|---|---|
| M0 | monorepo 骨架：go.mod、cmd 双入口、Taskfile、webui Vite scaffold、.gitignore | `task build` 出双二进制；`task dev` 前后端联通（代理） |
| M1 | internal/api 类型 + device 抽象 + examples/stcb 适配器与解析器 | `go test ./...` 绿（黄金样本含损坏行） |
| M2 | server（chi+hub+SQLite）+ edge（串口+WS 客户端+轮询/对时） | 真板接入：REST 见设备、事件落库、WS fan-out 可见、sync 后漂移≈0 |
| M3 | React 管理台五页 + embed 单二进制 | 浏览器看真板实时状态；按钮下发命令全链路生效 |
| M4 | 加固：命令 ack 跟踪、拔插重连、slog、recharts 漂移趋势、README、tag v0.1.0 | 拔插 USB 自动恢复；`task test` 全绿 |

首次推送公开仓库前：leak_guard 可见性门禁登记（hook-kit `audit --apply`），并全仓扫描
确认无课程材料/凭据/个人路径。

## 约束（硬）

1. **课程资产不进 public**：官方 BSP 库（STCBSP_V3.6.LIB）、课件、官方模板、课程抓取物
   一律不进本仓库；`firmware/` P1 只放协议参考说明，通用参考固件（不依赖课程 BSP、
   直接寄存器/库函数实现）另列 P1.5 任务。
2. 药盒业务语义只存在于 `examples/pillbox`，核心（internal/*）设备无关。
3. 私有构想/设备清单/验证证据只写 `.local/`（gitignored）。