# AGENTS.md

最后更新：2026-09-05

作用域：`cloudpath` 仓库全部目录。给 coding agent 的项目 SSOT：定位、硬边界、关键路径、命令与约定。

## 项目是什么

CloudPath（云径）是**云原生、插件驱动的互联物联网控制平台** monorepo：

- `cloudpath-edge`：边缘代理，管理本机串口设备，WS 长连接上报状态/事件、接收命令。
- `cloudpath-server`：中心服务，chi REST + WS hub + SQLite 持久化 + 内嵌 React 管理台，单二进制发布。
- `webui/`：React 19 管理台（苹果极简风），构建产物 `go:embed` 进 server。

技术栈定稿见 `docs/design.md`（技术 SSOT），设备协议契约见 `docs/protocol.md`。

## 硬边界

1. **核心设备无关**：`internal/*` 不得出现任何具体设备/行业语义。设备语义只允许存在于
   `examples/<device>` 适配器内（状态字段、标签、命令集）。STC-B / 取药小药盒 / 多板玩法只是 reference implementation 与 showcase，**不得为演示向 Core 塞特例**；demo 侧需求若要改 Core，先证明它是通用 primitive。判断句：这段逻辑换个设备、换个业务还成立吗？不成立 → 属 demo 侧或插件侧。
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

## 规划纪律（提任何新工作前先过本节）

**状态词表**——只允许这四类，禁用 ✅ / 🔄 / 「基本完成」/ 「在研」等模糊记号：

| 状态 | 含义 | 判据 |
|---|---|---|
| `VERIFIED` | 真机 / E2E / release / deployment 已证明 | 私有层有证据文件，或给出可复跑命令 |
| `IMPLEMENTED` | 代码完成、测试通过，**但没真机** | 有单测，无真板证据 |
| `PLANNED` | 已决定下一步，在执行队列里 | 有 owner 与 Gate |
| `IDEA` | 纯候选，**不进执行队列** | 一律只写私有层的候选登记表 |

**五条决策原则**

1. Evidence > Architecture　2. Finish > Expand　3. One reusable primitive > three demo features
4. Real hardware > mocked success　5. Current blocker > future possibility

**北极星**：新增一种硬件不改 Core；新增一种业务不改 Driver；所有关键路径都有可自动重复的真实 E2E。

**没有新证据时不新增架构。** 私有层只有四份事实源，各答一个问题：长期定义（最终是什么）、当前证据（今天证明了什么、谁在做什么、当前 blocker、下一个 Gate）、里程碑（最多三个，各有 Gate 与 Done Definition）、候选登记（全部候选 + 毕业判据，不得反向污染架构）。新增第五份规划文档即违规。本文件只写这四类的**约束性规则**，不点名私有层的具体文件（公开文档点名即坏链接）。
## 关键路径

| 用途 | 路径 |
|------|------|
| 共享契约（信封/DTO） | `internal/api/types.go` ↔ `webui/src/lib/types.ts` |
| 设备抽象与注册表 | `internal/device/device.go` |
| 参考适配器（进程内，无硬件） | `examples/demo/{demo.go,demo_test.go}` |
| 边缘运行时（监督/轮询/离线缓冲） | `internal/edge/{edge.go,wsclient.go,config.go}` |
| 中心服务（路由/hub/限流/保留期） | `internal/server/{server.go,ws.go}` |
| 持久层与迁移 | `internal/store/{store.go,schema.sql,schema_v2.sql}` |
| 前端页面/组件/store | `webui/src/{pages,components,store,hooks,lib}` |
| 容器化部署（L0/L2 + TLS） | `deploy/compose/{docker-compose.yml,docker-compose.public.yml}` |
| CI/CD 发版（6 平台二进制 + GHCR 多 arch 镜像） | `.github/workflows/{ci,release,container}.yml` |
| 设计系统（CSS 变量/动效/骨架） | `webui/src/index.css` |
| 一键任务 | `Taskfile.yml` |
| 技术设计 SSOT | `docs/design.md` |
| 协议契约 | `docs/protocol.md` |
| 私有层（gitignored，不入库） | 四份事实源：长期定义 / 当前证据与执行边界 / 里程碑 / 候选登记；另有索引、验证证据、分析与工具子目录。公开文档只泛指「私有层」，不点名其中文件 |

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

# 发布矩阵（6 平台二进制 + 架构断言 + checksums）：task build:matrix；复跑校验：task verify:arch；发布前聚合门禁：task verify

无 task 时：`go build -tags embed_ui -o bin/ ./cmd/cloudpath-server`、
`go test ./... -count=1`、`cd webui && pnpm build`。

真板验证（需要一台接在串口上的设备）：起 server → 起 edge → REST 查设备 → POST 命令 →
看事件与 ack。步骤与预期见 `docs/design.md` 的验收清单。

## 约定

- **Go**：`gofmt` + `go vet` 必须干净；错误要包裹上下文（`fmt.Errorf("…: %w", err)`）；
  结构化日志用 `slog`（不用 `fmt.Println`）；并发原语优先 ctx 取消而非 close(chan)。
- **前端**：TypeScript `strict` + `noUnusedLocals`；数据层 REST 走 TanStack Query、实时态走
  zustand（`store/ws.ts` 单例连接）；样式只用设计系统里的语义 token 与工具类，不写死颜色、任意 px 字号、裸圆角、裸层级或裸动效时长；
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
