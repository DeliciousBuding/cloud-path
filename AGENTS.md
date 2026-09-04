# AGENTS.md

最后更新：2026-09-03

作用域：`cloudpath` 仓库全部目录。给 coding agent 的项目 SSOT：定位、硬边界、关键路径、命令与约定。

## 项目是什么

CloudPath（云径）是**云原生、插件驱动的互联物联网控制平台** monorepo：

- `cloudpath-edge`：边缘代理，管理本机串口设备，WS 长连接上报状态/事件、接收命令。
- `cloudpath-server`：中心服务，chi REST + WS hub + SQLite 持久化 + 内嵌 React 管理台，单二进制发布。
- `webui/`：React 19 管理台（苹果极简风），构建产物 `go:embed` 进 server。

技术栈定稿见 `docs/design.md`（技术 SSOT），设备协议契约见 `docs/protocol.md`。

## 硬边界

1. **核心设备无关**：`internal/*` 不得出现任何具体设备/行业语义。设备语义只允许存在于
   `examples/<device>` 适配器内（状态字段、标签、命令集）。
2. **契约三处同步**：改消息信封或 DTO 必须同时改 `internal/api/types.go`、
   `webui/src/lib/types.ts`、`docs/design.md`，缺一即为未完成。
3. **锁内不做磁盘 I/O**：`internal/server` 的 `s.mu` 只保护内存态；需要落库时锁内收集、
   锁外 `persistXxx`。`Store` 可能为 nil（API-only 模式），所有落库路径先判空。
4. **命令白名单**：命令合法性以适配器 `SupportedCommands()` 为唯一事实源，server 与前端都不得
   另建清单；参数需过长度/控制字符校验。
5. **不入库**：真实设备清单（`edge.yaml`）、运行数据（`data/`）、构建产物（`bin/`、`webui/dist/`）、
   凭据、私有构想（`.local/`）。示例配置只放 `edge.example.yaml`。
6. **不含任何第三方厂商固件/库/课件**：`firmware/` 只放协议参考说明。
7. 隐私：公开仓库不出现可识别个人信息、私有路径、内部业务语义。

## 关键路径

| 用途 | 路径 |
|------|------|
| 共享契约（信封/DTO） | `internal/api/types.go` ↔ `webui/src/lib/types.ts` |
| 设备抽象与注册表 | `internal/device/device.go` |
| 参考适配器（含黄金样本单测） | `examples/stcb/{stcb.go,parser.go,parser_test.go}` |
| 边缘运行时（监督/轮询/离线缓冲） | `internal/edge/{edge.go,wsclient.go,config.go}` |
| 中心服务（路由/hub/限流/保留期） | `internal/server/{server.go,ws.go}` |
| 持久层与迁移 | `internal/store/{store.go,schema.sql,schema_v2.sql}` |
| 前端页面/组件/store | `webui/src/{pages,components,store,hooks,lib}` |
| 设计系统（CSS 变量/动效/骨架） | `webui/src/index.css` |
| 一键任务 | `Taskfile.yml` |
| 技术设计 SSOT | `docs/design.md` |
| 协议契约 | `docs/protocol.md` |
| 私有层（构想/待办/验证证据，gitignored） | `.local/` |

## 常用命令

```bash
task setup        # go mod download + pnpm install
task build        # 前端构建 → 内嵌 → bin/ 双二进制
task test         # go test ./... + webui tsc --noEmit
task vet          # go vet + gofmt 检查
task dev:server   # :8080（API+WS，前端走 Vite 代理）
task dev:web      # :5173 Vite dev server
task dev:edge     # 读 ./edge.yaml
task run          # 生产模式本地全栈（内嵌 UI）
```

无 task 时：`go build -tags embed_ui -o bin/ ./cmd/cloudpath-server`、
`go test ./... -count=1`、`cd webui && pnpm build`。

真板验证（需要一台接在串口上的设备）：起 server → 起 edge → REST 查设备 → POST 命令 →
看事件与 ack。步骤与预期见 `docs/design.md` 的验收清单。

## 约定

- **Go**：`gofmt` + `go vet` 必须干净；错误要包裹上下文（`fmt.Errorf("…: %w", err)`）；
  结构化日志用 `slog`（不用 `fmt.Println`）；并发原语优先 ctx 取消而非 close(chan)。
- **前端**：TypeScript `strict` + `noUnusedLocals`；数据层 REST 走 TanStack Query、实时态走
  zustand（`store/ws.ts` 单例连接）；样式只用设计系统里的 CSS 变量与工具类，不写死颜色；
  组件保持展示型（数据从 hooks 进）。
- **命名**：目录/文件 kebab-case 或小写；Go 包名单词；前端组件 PascalCase。
- **提交**：`type: 中文简述`（`feat|fix|docs|chore|refactor`），一次一个意图；
  提交信息与验证结果一起写。里程碑打 tag（`v0.1.0`…）。
- **测试**：新增行为必须带测试。协议解析用真实捕获的黄金样本（含损坏行）；
  server 用 `httptest` + 真 WS 拨号；store 用临时目录 SQLite。
  注：Windows 上 `-race` 依赖 cgo，本机不可用，CI（Linux）再开。
- **文档分层**：README = 产品与快速开始；`docs/design.md` = 技术 SSOT；
  `docs/protocol.md` = 协议契约；本文件 = agent 规则。不要把变更流水写进这些文件（看 git log）。

## 不要做

- 不要把具体行业业务语义写进 `internal/*` 或前端组件。
- 不要在 README/docs 里堆实现细节（放 `docs/design.md`）。
- 不要提交 `data/*.db`、`bin/*`、`webui/dist/`、`edge.yaml`、`.local/`。
- 不要绕过命令白名单加"万能命令"通道（`raw` 已由适配器显式声明并受参数校验约束）。
