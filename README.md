<div align="center">

# Cloudpath · 云径

**设备无关的 IoT 接入与管理平台**：边缘代理聚合本地串口设备，中心服务统一监控、下发与持久化，
管理台通过 WebSocket 实时可视化。

[![Go](https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&logoColor=white)](https://go.dev/)
[![React](https://img.shields.io/badge/React-19-61DAFB?logo=react&logoColor=black)](https://react.dev/)
[![SQLite](https://img.shields.io/badge/SQLite-WAL-003B57?logo=sqlite&logoColor=white)](https://sqlite.org/)
[![License](https://img.shields.io/badge/license-MIT-blue)](LICENSE)

</div>

---

## 为什么

手上一台串口设备（工控小板、传感器、自制终端）想"上云看看"，通常要在三件麻烦事之间来回切换：
写轮询脚本、拼一个能看的界面、再想办法把命令发回去。Cloudpath 把这条链路做成基础设施：

```
设备(串口) ──> cloudpath-edge ──WS──> cloudpath-server ──WS──> 浏览器管理台
              每站点一个                REST + SQLite + 内嵌 UI      实时状态 / 事件 / 命令
```

- **设备无关**：核心不认识任何具体硬件。新设备 = 实现一个 `device.Adapter` 并在 `init()` 注册，
  核心零改动；命令白名单由适配器声明，前端命令面板自动跟随。
- **全链路实时**：edge→server→浏览器都是 WebSocket 长连接，状态变化秒级到面板；
  REST 只承担历史查询与管理操作。
- **单二进制**：前端构建产物 `go:embed` 进 server，一个可执行文件即可跑起来。
- **零 CGO**：SQLite 用 `modernc.org/sqlite`，交叉编译到 Linux/arm64 无需工具链。

## 界面

苹果极简风管理台：概览 / 设备 / 设备详情 / 事件 / 边缘节点 / 系统，浅色深色双主题，
移动端自适应，尊重系统"减少动效"偏好。

| 概览 | 设备详情 |
|---|---|
| ![概览](docs/assets/dashboard-dark.png) | ![设备详情](docs/assets/device-dark.png) |

## 快速开始

### 1. 起中心服务

```bash
git clone https://github.com/DeliciousBuding/cloudpath.git
cd cloudpath

# 需要 Go 1.26+、Node 20+、pnpm；task 可选（go install github.com/go-task/task/v3/cmd/task@latest）
task setup      # 或：go mod download && cd webui && pnpm install
task build      # 前端构建 → 内嵌 → bin/cloudpath-server(.exe) + bin/cloudpath-edge(.exe)

./bin/cloudpath-server            # 默认 127.0.0.1:8080，数据库 data/cloudpath.db
```

打开 <http://127.0.0.1:8080> 即是管理台（前端已内嵌）。

开发模式（前端热更新）：

```bash
task dev:server   # :8080，API + WS
task dev:web      # :5173，Vite dev server，代理 /api /ws /healthz 到 :8080
```

### 2. 接一台设备

```bash
cp edge.example.yaml edge.yaml    # 填 edge_id、串口与适配器
./bin/cloudpath-edge              # 或 task dev:edge
```

`edge.yaml`（真实串口清单不入库）：

```yaml
server: ws://127.0.0.1:8080/ws/edge
token: ${CLOUDPATH_TOKEN}         # server 未启用令牌时留空
edge_id: lab-1
poll_interval_s: 5                # 状态转储轮询
sync_interval_s: 600              # 周期对时
report_interval_s: 30             # 状态心跳兜底
devices:
  - id: demo-1
    adapter: stcb                 # examples/stcb：STC-B 学习板参考适配器
    name: 参考板
    port: COM3                    # Linux: /dev/ttyUSB0
    baud: 9600
```

edge 连上后设备自动出现在管理台：时钟、漂移、槽位、事件流实时刷新，命令可下发并看到回执。

### 3. 接入自己的设备

```go
// examples/mydev/mydev.go
package mydev

func init() { device.Register(&Adapter{}) }

type Adapter struct{}

func (a *Adapter) Name() string                    { return "mydev" }
func (a *Adapter) SupportedCommands() []string     { return []string{"sync", "dump"} }
func (a *Adapter) Open(ctx context.Context, cfg device.Config, onEvent func(device.Event)) (device.Device, error) {
    // 打开端口 → 起 RX 循环 → 返回实现 Snapshot/Send/Done/Close 的 Device
}
```

然后在 `cmd/cloudpath-edge/main.go` 与 `cmd/cloudpath-server/main.go` 里 `_ "…/examples/mydev"`
导入一次即可（server 需要它做命令白名单校验）。状态字段（`Raw map[string]any`）完全自定义，
前端按 `clock/drift_min/slots/state` 等通用键展示，未知键原样进"原始状态"面板。

## API

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/healthz` | 版本、运行时长、在线设备/边缘计数 |
| GET | `/api/devices` | 全部设备视图（内存态，含最后一次状态） |
| GET | `/api/devices/{edgeID}/{deviceID}` | 单台设备 |
| POST | `/api/devices/{edgeID}/{deviceID}/commands` | 下发命令 `{"cmd":"sync","args":""}` |
| GET | `/api/events?device=&since=&limit=` | 事件历史（新→旧，limit 上限 1000） |
| GET | `/api/commands?device=&status=&limit=` | 命令与回执状态 |
| GET | `/api/edges` | 边缘节点（在线连接 + 曾接入的离线节点） |
| GET | `/api/adapters` | 已注册适配器与命令白名单 |
| GET | `/api/stats` | 事件/命令/设备计数、保留期、schema 版本 |
| WS | `/ws` | 浏览器实时订阅（首帧全量快照，随后增量 fan-out） |
| WS | `/ws/edge` | 边缘接入（首帧必须是 `hello`） |

消息信封、命令与事件语义见 [docs/protocol.md](docs/protocol.md)；完整技术设计见
[docs/design.md](docs/design.md)。

## 运行与运维

```bash
cloudpath-server -addr 127.0.0.1:8080 -db data/cloudpath.db \
  -token "$CLOUDPATH_TOKEN" -retention-days 30 -cmd-rate 20 \
  -allowed-origins "console.example.com" -log-level info -log-format json
```

| 环境变量 | 默认 | 说明 |
|---|---|---|
| `CLOUDPATH_ADDR` | `127.0.0.1:8080` | 监听地址（默认只绑本机） |
| `CLOUDPATH_DB` | `data/cloudpath.db` | SQLite 路径（WAL） |
| `CLOUDPATH_TOKEN` | 空 | 设置后：edge hello 校验 + 写操作 Bearer 校验 + 浏览器 WS 令牌 |
| `CLOUDPATH_ALLOWED_ORIGINS` | 空 | WS 允许的浏览器 Origin 模式，逗号分隔；留空 = 开发策略（同源 + localhost） |
| `CLOUDPATH_RETENTION_DAYS` | `30` | 事件/终态命令保留天数，后台每小时清理 |
| `CLOUDPATH_CMD_RATE` | `20` | 单设备每分钟命令上限，超出返回 429 |
| `CLOUDPATH_LOG` / `CLOUDPATH_LOG_FORMAT` | `info` / `text` | 日志级别与格式（`json` 便于采集） |

稳定性设计（都有测试覆盖，`task test`）：

- **锁内不做磁盘 I/O**：内存态与持久化分离，广播不被写库拖慢。
- **半开连接收敛**：WS 双向 Ping 保活，Ping 失败即取消会话并触发清理/重连。
- **edge 自愈**：串口拔插退避重开；server 断线指数退避重连，断线期间**事件进有界缓冲**，
  重连后回放（状态消息幂等，重连即强制补报一次）。
- **命令闭环**：`pending → sent → ok/failed`，90 秒未回执由后台 sweeper 标 `timeout`；
  前端按钮跟踪 ack 并有 15 秒超时兜底。
- **重启不空白**：server 启动从 SQLite 水合设备与最后状态（一律标离线，等 edge 重新上报）。
- **输入收口**：命令白名单（适配器声明）+ 参数长度/控制字符校验 + `edge_id` 形状校验 +
  设备归属校验（edge 只能上报自己注册过的设备键）+ SPA 路径穿越防护。

## 目录

```
cloudpath/
├── cmd/{cloudpath-server,cloudpath-edge}/   # 双入口
├── internal/
│   ├── api/       # edge↔server↔浏览器 共享契约（信封与 DTO）
│   ├── device/    # Device/Adapter 接口 + 注册表（设备无关核心）
│   ├── edge/      # 设备监督、轮询/对时调度、WS 客户端、离线缓冲
│   ├── server/    # chi 路由、WS hub、命令下发、限流、保留期清理、内嵌 UI
│   ├── store/     # SQLite：schema 迁移(user_version)、设备/状态/事件/命令
│   └── logx/      # slog 初始化
├── examples/stcb/ # 参考适配器：STC-B 学习板（串口协议 + 解析器 + 黄金样本单测）
├── webui/         # React 19 + Vite + Tailwind 4 管理台（构建产物被 server 内嵌）
├── firmware/      # 设备侧协议参考说明（不含任何厂商固件/库）
└── docs/          # design.md（技术 SSOT）+ protocol.md（协议契约）
```

## 开发

```bash
task test       # go test ./... + 前端 tsc --noEmit
task vet        # go vet + gofmt 检查
task build      # 前端 + 双二进制（内嵌 UI）
task run        # 本地起全栈（:8080）
task clean      # 清理 bin/ dist/ data/
```

约定：提交信息 `type: 简述`（`feat|fix|docs|chore|refactor`）；契约变更必须同步
`internal/api/types.go` ↔ `webui/src/lib/types.ts` ↔ `docs/design.md` 三处。

## 路线图

- **P1（当前）** 本机/局域网：单 server + 多 edge、实时面板、命令闭环、SQLite 持久化。
- **P2** 多站点与公网：MQTT 接入、令牌/多租户鉴权、反代与 TLS 部署形态、告警通道。
- **P3** 设备生命周期：远程 OTA 刷机、固件版本盘点、批量下发编排。
- **P4** 数据价值：时序降采样与聚合、导出、看板自定义、SLA/依从性类分析。

## 许可

MIT © Cloudpath Authors — 见 [LICENSE](LICENSE)。
