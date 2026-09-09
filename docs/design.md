# CloudPath 技术设计（当前实现）

本文是 CloudPath 的技术设计基线：技术栈、进程模型、目录、接口、存储、前端数据与行为边界、安全、测试与验证边界。WebUI 的呈现、排版、布局与交互设计见 [webui/DESIGN.md](../webui/DESIGN.md)。
设备侧协议见 [protocol.md](protocol.md)；架构状态分层见 [architecture.md](architecture.md)；
面向使用者的说明见根 [README.md](../README.md)。

> 状态：以当前 `main` 为实现基线，最新发布版本为 `v0.2.28`。外部 Driver Host、Registry、
> Application Runtime 与多租户隔离已实现；目标态与未实现项见 [architecture.md](architecture.md) §11。
> 历史记录用于解释设计取舍，不代表当前缺口。

## 当前基线与范围

**当前基线**：设备接入与边缘监督、WebSocket 实时链路、账号/RBAC/多租户隔离、插件发现与验证安装、
外部 Driver Plugin Host、Server AppHost + Application Runtime、SQLite 持久化与单二进制部署。

**当前范围外（目标态）**：Connector/Transform 运行时、MQTT/Modbus 接入、远程 OTA 编排、
时序聚合与业务分析、中心 KMS/Vault、分布式全局配额与多 Server 部署。架构保留扩展点，但不把这些
目标态写成现状。

## 技术栈

| 层 | 选型 | 理由 |
|---|---|---|
| 语言 | Go 1.26 | 单二进制、跨平台交叉编译（server 可直接上 VPS/arm64） |
| HTTP 路由 | chi v5 | 轻量惯用，中间件支持好 |
| WebSocket | coder/websocket | context 原生、活跃维护；edge↔server 与 server↔浏览器共用 |
| 存储 | SQLite（modernc.org/sqlite） | 纯 Go 零 CGO，交叉编译无负担；WAL + busy_timeout |
| 日志 | stdlib `log/slog` | 结构化日志，零依赖，支持 text/json |
| 串口 | go.bug.st/serial | 跨平台、无 CGO |
| 前端 | React 19 + TypeScript + Vite 6 | 现代 SPA 标准栈，`strict` + `noUnusedLocals` |
| 路由 | React Router 7 | 声明式 SPA 路由 + 路由级懒加载 |
| 样式 | Tailwind CSS 4 + 自建 primitive + shadcn/Radix 交互原语 | 语义 token 在 `webui/src/index.css`；Button/Input/Select 等基础原语在 `webui/src/components/ui.tsx`，Dialog/Drawer/Menu/Popover/Table 等复杂交互采用 shadcn 结构 + Radix，视觉仍由 CloudPath token 控制 |
| 表格 | TanStack Table 9 | 列定义、排序、列可见性与筛选工具栏复用，避免每页手搓 `<table>` |
| 数据层 | TanStack Query 5（REST）+ zustand 5（WS 实时态） | 查询缓存与实时推送分离 |
| 图表 | recharts | 漂移趋势（按需加载 chunk） |
| 图标 | lucide-react | 线性图标，随 `currentColor` |
| 包管理 | pnpm | 快、磁盘友好 |
| 任务编排 | Taskfile.yml（go-task） | 跨平台 dev/build/test/lint 一键 |

## 进程模型与数据流

```text
设备(串口/网络) ──> 外部 Driver Plugin（独立进程）
                         │ Driver Protocol v1：Describe / Watch / Execute
                         ▼
                  cloudpath-edge（每站点一个）
                         │ Plugin Host、设备监督、离线事件缓冲、WS 重连
                         ▼
                  cloudpath-server（中心，单二进制）
                         ├─ REST / WS hub + tenant / RBAC / audit
                         ├─ Plugin control plane：desired / revision / observed / Registry
                         ├─ AppHost + appruntime：Application Plugin
                         ├─ SQLite：tenant / device / state / event / command / plugin data
                         └─ embed：webui/dist 静态资源
                         ▼
                  React 管理台（浏览器，WS 实时 + REST 历史）
```

要点：账号会话下 **edge→server→浏览器 WebSocket 链路**，状态变化实时到达面板；REST 承担历史
查询与管理操作。服务令牌会话没有浏览器实时通道，边界见 [api.md](api.md#57-已知边界当前接受)。命令走 server→edge 的 WS 下行，带 ack 回执落库，前端按 `command_id` 结算。外部 Driver
拥有硬件连接与协议解析；内置 `demo` 仅用于无硬件参考，不承载具体设备语义。

## 目录结构

```text
cloudpath/
├── cmd/                         # cloudpath-server / cloudpath-edge / cloudpath CLI
├── internal/
│   ├── api/                     # REST/WS 共享类型
│   ├── device/                  # Device/Adapter 核心接口与注册表
│   ├── edge/                    # Edge 运行时、外部 Driver 桥接、Plugin Host
│   ├── pluginhost/              # 插件进程握手、传输、监督
│   ├── plugincontrol/           # desired/applied/observed 收敛与 secret handle
│   ├── registry/                # GitHub 发现、Manifest/digest/兼容校验、lockfile
│   ├── server/                  # REST/WS、鉴权、审计、命令与 AppHost
│   ├── appruntime/ + application/ # Application Runtime 与绑定/领域模型
│   ├── store/                   # SQLite schema 迁移与持久化
│   └── tenantpolicy/ + secrethandle/ + plugincatalog/
├── sdk/go/                      # 公开插件 SDK、模型、RPC、transport、pluginmain
├── spec/ + proto/               # Manifest/Capability schema 与协议定义
├── templates/go-plugin/         # Driver/Application 插件模板
├── webui/                       # React SPA，构建产物被 server 内嵌
├── examples/                    # demo adapter 与历史参考应用
├── deploy/ + firmware/ + scripts/ + docs/
└── testing/plugin-harness/      # 黑盒一致性测试
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

type Adapter interface {                              // examples/demo 实现
    Name() string
    SupportedCommands() []string                      // 命令白名单（server 据此拒绝未知命令）
    Open(ctx context.Context, cfg Config, onEvent func(Event)) (Device, error)
}
// 注册表：适配器以 init() 注册，edge 按配置名实例化，server 用它校验白名单
// → 新增设备不改核心，前端命令面板自动跟随 GET /api/adapters
```

外部 Driver 通过 Driver Protocol v1 桥接为 `device.Adapter`；`Raw` 只保留兼容与诊断用途，主路径使用
Descriptor / Entity / Capability / Observation。核心与前端都不对具体设备做分支判断。

## API 接口

### WebSocket 协议

WS 信封、消息类型、方向和字段以 [protocol.md](protocol.md) 与 `internal/api/types.go` 为准。
设计侧只保留一条不变量：

**事件同形不变量**：server 对 `event` 只做一次 `json.Marshal(EventData)`，落库与广播
共用这份 payload；浏览器端不得重建载荷形状。否则同一条事件在实时列表与历史里会呈现
两种样子（曾发生过：前端把载荷重建成 `{"label":""}`，`entity_id` 被丢弃，Capability
绑定的路由依据随之消失）。

### REST

HTTP 路由与 DTO 的**唯一**文档事实源是 [`api.md`](api.md)：安全模型三级、鉴权与多租户、
RBAC、插件控制面写面、Application Data Plane 与稳定错误码都在那里。本文不再复制路由表——
它已经漂移过一次（缺二十余条已实现路由），复制即负债；这里只留设计层面的约定：

- **错误体**统一 `{"error":"…"}`；插件写面另有稳定错误码（`api.md` §5.6）。
- **状态码语义**：400 参数/白名单、401 凭据缺失或失效、403 无凭据的非回环写、404 资源不存在、
  409 edge 离线或状态冲突、429 限流、503 存储不可用或 edge 队列满。
- **两段设备路径**：设备键本身含 `/`（`{edgeID}/{deviceID}`），用两段路径参数而不是转义单段。
- **实时通道**：浏览器 `GET /ws`（会话 cookie 优先，`?token=` 只用于带不了 header 的场景）、
  边缘 `GET /ws/edge`；内嵌 SPA 由 `/*` 兜底并做路径穿越防护，但兜底**不含** `/api/*`
  与缺失的 `/assets/*`——那两类回 404，否则不存在的端点会用 200 + HTML 谎报成功。

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

-- v12（migrate_v12.go）：数值采样历史（按设备/序列/秒）
observation_samples(tenant_id, device_id, series_key, ts, value, quality, PK(tenant_id,device_id,series_key,ts))

-- v13（migrate_v13.go）：失败/超时操作的人工处理时间
commands.handled_at INTEGER NULL
```

迁移是有序表（`internal/store/store.go` 的 `migrations`）：新增版本追加一项，**永不修改已发布项**。
当前 `PRAGMA user_version = 13`（v0.2.20 持久化最后已知 Descriptor，v12 增加数值采样历史，v13 增加失败操作处理状态）；上面的 v1/v2 只是基础表示意，
完整迁移见 `internal/store/migrate_v*.go`。连接池上限 4 + WAL + `busy_timeout(5000)`，避免 `database is locked`。

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
8. **命令状态**：`pending → sent → ok|failed|timeout`；90 秒未回执由 sweeper 标 `timeout`；
   前端按钮跟踪 `command_id` 的 ack，另有 15 秒超时兜底提示。
9. **重启不空白**：server 启动从 SQLite 水合设备与最后状态，一律标离线，等 edge 重新上报。
10. **输入收口**：命令白名单（适配器声明）、参数长度 ≤64 UTF-8 字节且不含换行/NUL、
    `edge_id` 形状校验（字母数字 `-_`，1–64）、设备归属校验（edge 只能上报自己注册过的键）、
    请求体 `MaxBytesReader(4096)`、WS 读上限（edge 64KB / 浏览器 4KB）、SPA 路径穿越防护。

## 安全边界

- server 默认只绑 `127.0.0.1`；公网部署必须置于自有反代 + TLS 之后。
- 鉴权支持账号会话、tenant token 与本地开放模式；实际形态和权限矩阵以 [api.md](api.md) 为准。
- `CLOUDPATH_TOKEN` 一启用：edge hello 校验、浏览器写操作 Bearer 校验、浏览器 WS `?token=` 校验。
  令牌只经环境变量/配置注入（`edge.yaml` 支持 `${ENV}` 展开），不入库。
- WS Origin 策略：`CLOUDPATH_ALLOWED_ORIGINS` 显式清单（公网形态）；留空 = 开发策略
  （请求自身 host 始终放行 + `localhost:*`/`127.0.0.1:*`/`[::1]:*`），启动时告警提示收紧。
  非浏览器客户端不带 Origin，不受影响。
- 命令限流：单设备默认 20 次/分钟，超出 429（防跑飞的 UI/脚本刷串口）。
- 数据保留：事件与终态命令默认保留 30 天，后台每小时清理（在途命令不清）。
- 设备清单/串口路径/令牌全部本地配置注入（`edge.yaml`、`.local/` 均 gitignored）。

## 前端（React Router 7）

> WebUI 的呈现、排版、布局与交互设计以 [`webui/DESIGN.md`](../webui/DESIGN.md) 为准；
> 本节只保留路由、数据获取、行为规则与安全边界。

| 路由 | 页面 | 内容 |
|---|---|---|
| `/` | 概览 | 在线设备/网关、运行实例、近 24 小时失败操作、需要关注的状态、设备与事件 |
| `/devices` | 设备 | 全部设备列表（WS 快照优先，REST 轮询兜底） |
| `/devices/:edgeId/:deviceId` | 设备详情 | 声明驱动的观测概览、能力、命令控制、事件与历史、技术诊断 |
| `/devices/:edgeId/:deviceId/trends/:seriesKey` | 趋势详情 | 单序列历史波形、时间范围、Brush 缩放与采样明细；REST 历史读取，离线设备仍可查看 |
| `/activity` | 运行记录 | 状态记录与操作记录、设备/类型筛选、实时与历史合并；`/events` 为兼容重定向 |
| `/edges` | 网关（Edge） | 在线/离线网关、版本、最后在线、所辖设备跳转 |
| `/plugins` | 应用与插件 | 默认实例列表、按需查看已安装与目录 |
| `/plugins/:id` | 运行实例 | 应用记录、设备绑定、定时任务；配置与技术信息按需展开 |
| `/settings` | 设置 | 服务状态、实时连接、令牌、存储统计、适配器清单、关于 |
| `/admin` | 成员与访问权限 | 管理员管理成员、角色和访问令牌 |
| `/setup` / `/login` | 初始化 / 登录 | 首装向导与账号登录；实时通道跟随登录态 |

设备操作只保留设备详情一个入口，控制分区可用 `?tab=controls` 直达；其它分区同样由明确的
`tab` 查询参数选择，未知值回落概览。趋势详情是设备详情的子资源路由，卡片点击进入独立可刷新页面；
历史采样走 REST，与设备在线态解耦。旧 `/pillbox` 链接跳设备列表，带设备的旧链接跳对应
设备控制区，不再把设备事件与实体观测包装成业务记录。应用结果位于运行实例详情。

概览的失败统计与最新 20 条预览使用同一次 `server_time` 采样的闭区间
`[server_time - 86400, server_time]`，按 `acked_at`（缺失时 `created_at`）归属时间窗；
计数来自完整匹配集，不因预览截断。原始命令历史与保留期不变。聚合来源不可用时返回 `503`，
客户端必须保留错误来源并允许重试，不能把不可用伪装成没有失败。

概览把实时故障与历史失败分开：网关/设备离线等实时问题随真实状态恢复自动消失，不提供“已读”；近 24 小时的失败/超时操作属于历史记录，可在运行记录中单条或批量标记为已处理。标记后不再进入概览待处理计数，但原始记录仍保留，并可用 `handled=unhandled|handled` 筛选。批量操作在无筛选时覆盖窗口内全部未处理项，有筛选时只覆盖当前列表。

状态管理：zustand 持有 WS 实时快照（设备 map + 事件环形缓冲 300 条 + 会话级漂移历史 240 点 +
ack map）；TanStack Query 管 REST（设备/事件/命令/统计/数值采样历史）。`store/ws.ts` 是单例连接，
自带指数退避重连（1→15s + 抖动）与令牌变更重连。

数据获取约定：页面**同时**消费实时层与 REST（`useDevices`/`useEdges` 合并两者），
因此实时通道断开时面板仍可用；事件流用 `mergeEvents` 按 `设备+时间+类型` 去重合并。
趋势详情以 REST 历史为基线，叠加当前会话 WS 点；历史读取不要求设备在线。

开发态：Vite dev server（:5173）代理 `/api` `/ws` `/healthz` 到 :8080；
生产态：`vite build` → `webui/dist` → `go:embed`（构建标签 `embed_ui`，未启用时有 stub 兜底，
server 退化为 API-only 并返回可读提示）。

### 设备命令与应用实例边界

设备动作的 `title` / `description` / `destructive` / `confirmation` 往返字段在
[protocol.md](protocol.md#capabilities)；HTTP 写面、RBAC 和 Application Data Plane 在
[api.md](api.md) 与[应用输入与操作契约](#应用输入与操作契约)。

WebUI 的表单、确认、身份切换、应用记录/绑定/任务呈现规则统一放在
[webui/DESIGN.md](../webui/DESIGN.md#设备命令与应用实例)，本节不复制交互细节。

### 应用输入与操作契约

- **观测输入**：Edge 的 state 可携带 `observations: [{entity_id, observations: {property: Observation}}]`。
  即使数值未变、Descriptor 被语义去重，新的真实采样时间仍随 state 上报。旧 Edge 没有此字段时，
  Core 不从 raw 的字段名猜实体或伪造样本。AppHost 只向同租户、实际绑定了该实体和能力的应用投递
  `cloudpath.dev/event/property-observed@1` CapabilityEvent，PayloadJSON 是一条完整 Observation。
  保留 observed_at / received_at / quality / sequence；离线状态降为 unavailable，不升级坏数据为正常。
  **身份边界**：命令/事件当前只按全局唯一 `entity_id` 绑定和路由，`(device_key, entity_id)` 尚未贯穿；
  同租户同型号多板必须由 Driver 保证 `entity_id` 全局唯一，否则属于单板边界，不承诺多板正确路由。
- **明确分配**：实例 config 的可选 `app_bindings` 是完整 Binding 数组的 JSON 字符串，按给定顺序选择稳定
  entity_id。复用 Binder.Validate 核对实际设备租户、能力、基数与重复占用；非法选择必须失败，不能换绑
  到任意在线实体。缺省仍自动匹配。应用只接收 ValidateBinding，不读取此控制面配置。
- **用户操作**：JobDescriptor.manual_only=true 的任务只由 operator/admin 显式请求，绝不进入分钟循环。
  名称、标题和输入 schema 来自运行中插件，不在 Core 维护应用动作列表。未设置此标记的旧任务保持既有
  自动执行语义；durable schedule_job 仍是显式声明的调度路径。
- **执行结果**：手动调用要求调用方生成并在同一逻辑请求重试时复用 idempotency_key。Core 限流、限制输入
  为 4 KiB JSON object、记录无参数值的审计；业务参数与幂等语义由插件验证。插件非 OK 状态返回错误 HTTP，
  不包装为成功。HTTP 成功只证明插件处理了请求，物理动作的成功仍需 RequestCompleted 和设备 ACK。
  网络超时不自动重试，应先核对领域记录，再以同一 key 重试。需要这些能力的插件要求 Core >=0.2.15。

## 配置与环境

**server**（flag 与环境变量等价，flag 优先）：

| flag | env | 默认 |
|---|---|---|
| `-addr` | `CLOUDPATH_ADDR` | `127.0.0.1:8080` |
| `-db` | `CLOUDPATH_DB` | `data/cloudpath.db` |
| `-token` | `CLOUDPATH_TOKEN` | 空（无鉴权） |
| `-webui` | `CLOUDPATH_WEBUI` | 空（用内嵌产物） |
| `-allowed-origins` | `CLOUDPATH_ALLOWED_ORIGINS` | 空（开发策略） |
| `-require-auth` | `CLOUDPATH_REQUIRE_AUTH` | `false` |
| `-retention-days` | `CLOUDPATH_RETENTION_DAYS` | `30` |
| `-cmd-rate` | `CLOUDPATH_CMD_RATE` | `20` |
| `-login-rate` | `CLOUDPATH_LOGIN_RATE` | `5` |
| `-session-days` | `CLOUDPATH_SESSION_DAYS` | `7` |
| `-setup-token` | `CLOUDPATH_SETUP_TOKEN` | 空 |
| `-trusted-proxies` | `CLOUDPATH_TRUSTED_PROXIES` | 空 |
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
    adapter: demo              # 无硬件参考适配器；外部 Driver 需启用 plugin_host
    name: 参考设备
```

配置校验在启动时完成并给出可执行错误信息（缺 id/adapter、需要真实端口的适配器缺 port、id 重复、
协议前缀错误、devices 为空），运行中不热加载（当前有意为之：热加载与串口生命周期纠缠，收益低风险高）。

## 测试策略

| 层 | 文件 | 覆盖 |
|---|---|---|
| 协议解析 | `cloud-path-driver-stcb/plugin/parser_test.go`（独立仓） | 黄金样本（真实捕获行：损坏分隔符、噪声前缀、越界值）、事件归一、漂移回绕、HHMM 校验、标签 |
| 存储 | `internal/store/store_test.go` | 迁移到当前版本 + 幂等、设备/状态生命周期、事件过滤、命令过滤与超时、保留期清理（不误删在途命令）、统计、limit 夹取 |
| 服务链路 | `internal/server/server_test.go` | WS 完整链路（快照→hello→state fan-out+落库→REST 命令→edge 收令→ack 落库+广播→白名单拒绝）、令牌鉴权、重启水合、healthz |
| 服务加固 | `internal/server/hardening_test.go` | 适配器/统计端点、nil-store 不 panic、命令限流、参数校验、未知设备与离线 edge、命令设备过滤、查询参数夹取、保留期、edge_id 校验、重连挤占不误标离线、安全头、SPA 回落与路径穿越、未路由 `/api/*` 与缺失 `/assets/*` 回 404 而非 index.html、鉴权形态三档如实上报 |
| Origin 策略 | `internal/server/origin_test.go` | 开发策略放行 localhost/无 Origin、拒绝外站；显式清单生效且防后缀伪装 |
| 边缘运行时 | `internal/edge/{config_test.go,wsclient_test.go}` | 配置默认值/`${ENV}` 展开/各类错误、离线只缓冲事件、在线入队、队满回落缓冲、缓冲溢出丢最旧、回放（含部分回放）、状态 diff 抑制与心跳兜底、重连强制补报 |
| 前端 | `pnpm exec tsc --noEmit` + `pnpm test` | 类型门禁（`strict` + `noUnusedLocals`）与 Vitest 行为回归；不把源码 class/样式扫描当行为测试 |
| 类型一致性 | `scripts/check_contract.py`（`task check:contract`） | Go `internal/api/types.go` ↔ TS `webui/src/lib/types.ts` 同名类型的 JSON 字段集合一致（含 `extends` 平面化）；`--self-test` 是解析器红队自检 |
| e2e | 真机手工清单 | 见下；验证证据按发布/真板记录另行归档，不写入公开仓 |

参考设备真机回归清单（使用任意已声明动作的真实 Driver；多设备现场 E2E 另列验证）：

1. `task build` 出双二进制；启动 server 后内嵌管理台可见，无设备时是空状态而非报错。
2. 启动 edge 后，网关与设备出现在管理台；`GET /api/edges`、`GET /api/devices` 与界面一致。
3. 对一个 Driver 声明的动作执行 `POST …/commands`，观察 `sent` 与随后 ack 的真实终态；
   设备事件按声明入库并实时出现。
4. 拔掉设备连接后设备转为离线，重新连接后 Edge 自动恢复监督并重新上报。
5. 停止 server 后 Edge 继续运行并缓冲事件；server 恢复后事件按离线缓冲语义回放。
6. 重启 server 后设备仍可水合，状态先标离线，Edge 重连后恢复。
7. 未声明命令返回 400；高频下发返回 429；启用鉴权后无凭据写操作返回 401。


## 后续扩展（当前范围外）

- **接入协议**：`internal/edge` 之外再加一个 MQTT/HTTP 接入网关，复用同一 `api.Envelope`
  与 hub（新增 `internal/gateway`），设备抽象不变。
- **按设备订阅**：浏览器 WS 增加 `subscribe` 消息类型，`broadcast` 改为按订阅集分发。
- **告警通道**：事件落库处已是单点（`handleEdgeWS` 的 `MsgEvent` 分支），挂一个 notifier 即可。
- **时序聚合**：`events` 表按天聚合到 `rollups` 表（v4 迁移），sweeper 里加一个任务。
- **OTA**：`isp` 命令已经能把设备送进烧录模式，补一个固件分发 + 进度上报的命令族。

## 实现偏差记录（相对最初设计稿）

| 设计稿 | 实际实现 | 原因 |
|---|---|---|
| `GET /api/devices/{id}` 单段路径 | `/api/devices/{edgeID}/{deviceID}` 两段 | 设备键本身含 `/`，单段需要转义，两段更直白 |
| `GET /api/devices/{id}/events` | `GET /api/events?device=` | 事件是全局资源，统一入口便于跨设备查询与筛选 |
| 样式用 shadcn/ui | 基础原语自建（`components/ui.tsx`）+ 复杂交互采用 shadcn/Radix | 简单控件保留自建以控制视觉和体积；Dialog/Drawer/Menu/Popover/Table 的焦点、portal 与定位行为交给成熟原语，避免重复手搓 |
| Vite 7 | Vite 6 | 定稿时 Vite 7 尚未发布、工具链未跟上；6 已满足需求 |
| `examples/pillbox` 承载业务语义 | 不设业务示例包，语义留在适配器的标签层 | 核心与示例都保持行业无关，避免平台绑定具体业务 |
| 前端命令按钮硬编码 | 由 `GET /api/adapters` 白名单驱动 | 新增适配器零前端改动，且与 server 校验同源 |
| 未规划保留期/限流/Origin 策略 | 三者均已实现并测试 | 长时间运行与对外暴露的实际需要 |
| 未规划离线事件缓冲 | edge 侧有界缓冲 + 重连回放 | 事件不可重放，server 短暂不可用不应丢数据 |

## 约束（硬）

1. **不含任何第三方厂商固件/SDK/库/课件**：`firmware/` 只放协议参考说明；设备侧代码不进本仓库。
2. **核心设备/行业无关**：具体设备语义只存在于外部 Driver 插件；内置 `demo` 仅作无硬件参考。
3. **私有信息不入库**：构想、设备清单、验证证据只写 `.local/`（gitignored）。
4. **类型定义三处同步**：`internal/api/types.go` ↔ `webui/src/lib/types.ts` ↔ 本文档的 WS 信封表；
   HTTP 路由与 DTO 的文档家是 `api.md`。同一份接口定义只允许一个文档落点，别处只放指针。
   代码那一半由 `scripts/check_contract.py` 机器守（CI `public-boundary`）：同名类型字段集不一致就失败。
