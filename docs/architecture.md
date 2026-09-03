# Cloudpath 架构设计

Cloudpath（云径）是一个通用 IoT 设备接入与管理平台：把分散的串口/边缘设备接入网络，
统一监控、下发与刷机。设备无关 —— STC-B 板是第一个官方适配的 reference device，取药
小药盒是第一个 reference application。

## 组件

| 组件 | 角色 | 语言 | 目录 |
|---|---|---|---|
| cloudpath-edge | 边缘代理：串口接入 N 台设备，聚合状态并上报 | Go | `cmd/cloudpath-edge` + `internal/edge` |
| cloudpath-server | 中心后端：接收上报、聚合状态、API、命令下发 | Go | `cmd/cloudpath-server` + `internal/server` |
| webui | 管理台：设备状态/事件/下发控制 | 静态前端 | `webui/`（server embed） |
| firmware | 上板固件（设备侧软件） | C / Keil | `firmware/` |
| examples | 官方适配与示例：设备适配 + 业务 demo | Go | `examples/` |

## 目录结构

```
cloudpath/
├── go.mod / README.md / LICENSE
├── docs/
│   ├── grill/                  # 决策日志
│   ├── architecture.md
│   └── protocol.md             # 设备协议契约（设备无关）
├── cmd/
│   ├── cloudpath-edge/         # edge 二进制入口
│   └── cloudpath-server/       # server 二进制入口
├── internal/
│   ├── edge/                   # 串口接入、设备注册、轮询、上报
│   ├── server/                 # API、状态聚合、命令下发
│   ├── protocol/               # 设备协议抽象（Reader/Writer/Command/Dump）
│   ├── device/                 # 设备抽象接口 + registry（插件式）
│   ├── store/                  # 状态存储（内存 → SQLite）
│   └── transport/              # edge↔server（HTTP 先行，MQTT 预留）
├── webui/                      # 前端静态资源（server embed）
├── examples/
│   ├── stcb/                   # STC-B reference device 适配
│   └── pillbox/                # 药盒 reference application
└── firmware/                   # 上板固件（C/Keil，独立于 go.mod）
```

## 数据流

```
设备(串口) ──> cloudpath-edge(每站点一个)
                   │ 设备协议解析 + 状态缓存 + 轮询
                   │ HTTP / MQTT(P2)
                   ▼
              cloudpath-server(中心)
                   │ 聚合 + 命令下发 + 存储
                   ▼
                webui 管理台
```

## 设备抽象（核心设计）

设备无关的关键是一层接口，而不是写死某块板：

```go
// internal/device
type Device interface {
    ID() string
    Snapshot() State            // 统一状态视图（在线/时钟/槽位/事件）
    Send(cmd Command) error     // 统一命令（对时/转储/触发/刷机）
}

type Adapter interface {        // 具体设备适配器（如 STC-B）
    Open(port string) (Device, error)
    // 内部处理该设备的串口协议与命令映射
}
```

`examples/stcb` 实现 STC-B 的 Adapter（串口协议、转储解析、`S/R/O/T/D` 命令）；
`examples/pillbox` 在其上叠加业务语义（药盒槽位/服药提醒）。新增设备 = 新增一个 Adapter，
不改核心。

## 安全边界

- edge/server 默认监听本机/内网，公网暴露须置于自有反向代理 + 鉴权之后。
- 设备清单、串口路径、凭据一律配置注入，不入库。
- 任何令牌/密钥通过环境变量或本地配置注入，不写代码。