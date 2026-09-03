# CloudPath Edge 客户端分发指引（给你自己的电脑用）

> 面向对象：拿到一个 CloudPath Server 地址和令牌后，要在**自己的 Windows / macOS / Linux 电脑**上
> 接入设备的人。不需要改任何代码，不需要访问服务器。
> 服务端落地见 [../README.md](../README.md)，项目总览见 [../../README.md](../../README.md)。

## 1. 你需要从管理员那里拿到三样东西

| 项 | 形状 | 例子（占位） |
|---|---|---|
| Server 地址 | `wss://<域名>/ws/edge` | `wss://console.example.com/ws/edge` |
| Edge 令牌 | `cp_` 开头的字符串 | `cp_xxxxxxxxxxxxxxxxxxxxxxxx` |
| 你的 `edge_id` | 字母数字加 `-_`，1–64 字符，全局唯一 | `alice-macbook` |

令牌是**凭据**：不要提交进 git、不要贴到公开聊天/issue、不要截图。
它只允许连 `/ws/edge`，不能读管理接口（用它请求 REST 会得到 `403`），
泄露了就让管理员 `DELETE /api/tokens/{id}` 吊销并换发。

## 2. 选对平台的二进制

先确认你的架构：

```powershell
# Windows PowerShell
$env:PROCESSOR_ARCHITECTURE      # AMD64 -> amd64；ARM64 -> arm64
```

```bash
# macOS / Linux
uname -m                        # arm64|aarch64 -> arm64；x86_64 -> amd64
```

Release 资产命名规范（`<version>` 例如 `v0.1.0`）：

```text
cloudpath-edge_<version>_<os>_<arch>[.exe]      # 你要的这个
cloudpath_<version>_<os>_<arch>[.exe]           # 可选：插件管理 CLI
cloudpath-server_<version>_<os>_<arch>[.exe]    # 服务端（客户端不需要）
checksums.txt                                   # 全部资产的 sha256
```

`<os>` = `linux` / `windows` / `darwin`，`<arch>` = `amd64` / `arm64`，六个平台都有。
拿错架构在 Linux/macOS 上会直接 `Exec format error`，在 Windows 上会报“不是有效的 Win32 程序”。

## 3. 每平台一句话安装并运行

### Windows（amd64 / arm64）

```powershell
# 下载 + 校验 + 运行（把 URL/文件名换成实际版本）
curl.exe -fsSLO https://github.com/DeliciousBuding/cloud-path/releases/download/v0.1.0/checksums.txt
curl.exe -fsSLO https://github.com/DeliciousBuding/cloud-path/releases/download/v0.1.0/cloudpath-edge_v0.1.0_windows_amd64.exe
certutil -hashfile cloudpath-edge_v0.1.0_windows_amd64.exe SHA256   # 与 checksums.txt 对照
copy ..\..\edge.example.yaml edge.yaml                              # 或自己新建 edge.yaml
.\cloudpath-edge_v0.1.0_windows_amd64.exe -config edge.yaml
```

### macOS（Apple Silicon = arm64 / Intel = amd64）

```bash
curl -fsSLO https://github.com/DeliciousBuding/cloud-path/releases/download/v0.1.0/checksums.txt
curl -fsSLO https://github.com/DeliciousBuding/cloud-path/releases/download/v0.1.0/cloudpath-edge_v0.1.0_darwin_arm64
shasum -a 256 -c checksums.txt --ignore-missing
chmod +x cloudpath-edge_v0.1.0_darwin_arm64
xattr -d com.apple.quarantine cloudpath-edge_v0.1.0_darwin_arm64   # 首次运行被 Gatekeeper 拦时
cp edge.example.yaml edge.yaml
./cloudpath-edge_v0.1.0_darwin_arm64 -config edge.yaml
```

### Linux（x86_64 = amd64 / aarch64 = arm64）

```bash
curl -fsSLO https://github.com/DeliciousBuding/cloud-path/releases/download/v0.1.0/checksums.txt
curl -fsSLO https://github.com/DeliciousBuding/cloud-path/releases/download/v0.1.0/cloudpath-edge_v0.1.0_linux_amd64
sha256sum -c checksums.txt --ignore-missing
chmod +x cloudpath-edge_v0.1.0_linux_amd64
cp edge.example.yaml edge.yaml
./cloudpath-edge_v0.1.0_linux_amd64 -config edge.yaml
```

> 不想下载二进制也可以从源码构建（需要 Go 1.26+）：
> `go build -o cloudpath-edge ./cmd/cloudpath-edge`，见 [../../README.md](../../README.md)。

## 4. 填 `edge.yaml`

母版是仓库根的 [edge.example.yaml](../../edge.example.yaml)（**不要改母版本体**，复制一份改）。
`edge.yaml` 属于本地私有配置，不要提交进任何仓库。

```yaml
server: wss://console.example.com/ws/edge   # 公网用 wss://；本机自建 server 用 ws://127.0.0.1:8080/ws/edge
token: ${CLOUDPATH_TOKEN}                   # 推荐：环境变量展开，明文不落盘
edge_id: alice-macbook                      # 管理员分配给你的唯一 ID

poll_interval_s: 5                          # 状态转储轮询周期（秒）
sync_interval_s: 600                        # 周期对时（秒）；设备钟漂移大就调小
report_interval_s: 30                       # 状态心跳兜底周期（秒）

devices:
  - id: board-1                             # edge 内唯一；全局键 = <edge_id>/<id>
    adapter: stcb                           # 必须是 GET /api/adapters 里列出的名字
    name: 我的板子                           # 展示名，可选
    port: /dev/ttyUSB0                      # Windows: COM3；macOS: /dev/cu.usbserial-*
    baud: 9600
```

字段含义与默认值：

| 字段 | 必填 | 说明 |
|---|---|---|
| `server` | 是 | Edge 接入端点。公网必须 `wss://`；末尾是 `/ws/edge` |
| `token` | 是（服务端启用鉴权时） | 支持 `${ENV}` 展开；先设环境变量再启动，避免明文进文件 |
| `edge_id` | 否 | 缺省用主机名（点号归一为 `-`）。多人共用一个 Server 时**必须显式指定且互不相同** |
| `poll_interval_s` | 否 | 默认 5 |
| `sync_interval_s` | 否 | 默认 600 |
| `report_interval_s` | 否 | 默认 30；状态无变化也会兜底上报一次 |
| `devices[].id` | 是 | 同一 edge 内唯一 |
| `devices[].adapter` | 是 | 适配器名，见 `GET /api/adapters`；写错会启动即报未知适配器 |
| `devices[].port` | 是 | 串口设备路径，各平台写法不同（见上） |
| `devices[].baud` | 否 | 默认 9600 |
| `devices[].name` | 否 | 仅展示用 |
| `plugin_host` | 否 | 外部 Driver 插件宿主，默认不启用；见母版注释与 [../../docs/architecture/plugin-system.md](../../docs/architecture/plugin-system.md) |

设置令牌环境变量（推荐做法）：

```powershell
# Windows PowerShell（当前会话有效）
$env:CLOUDPATH_TOKEN = "cp_你的令牌"
```

```bash
# macOS / Linux（当前会话有效，注意前面的空格可避免进 bash history）
 export CLOUDPATH_TOKEN="cp_你的令牌"
```

一个 edge 可以带多台设备：在 `devices:` 下加多个条目即可，每台设备各自独立监督，
一台掉线不影响其它台，也不影响别的电脑上的 edge。

## 5. 启动后应该看到什么

```text
level=INFO msg="edge started" edge=alice-macbook devices=1 server=wss://console.example.com/ws/edge ...
level=INFO msg="connected to server" url=wss://console.example.com/ws/edge edge=alice-macbook
```

- 看到 `connected to server` = 接入成功。此时管理员在 WebUI 的“边缘节点”页能看到你的 `edge_id` 为 online。
- 设备串口打不开时会打印 `device open failed ... retry_in=1s`，并按 1→2→4→8…→30s 指数退避重试；
  **Edge 仍然保持在线**，插好线后自动恢复，不需要重启进程。
- 网络断开时 Edge 自动重连（指数退避 + 抖动），期间事件进有界缓冲，重连后回放；
  状态消息幂等，重连即强制补报一次。
- 命令由 Server 下发到 Edge 执行并回执；设备离线时回执是 `failed / device offline`（链路正常，设备不可达）。

管理员侧验收（或你自己用管理令牌）：

```bash
curl -fsS -H "Authorization: Bearer <管理令牌>" https://console.example.com/api/edges
curl -fsS -H "Authorization: Bearer <管理令牌>" https://console.example.com/api/devices
```

## 6. 让它开机自启（可选）

- **Windows**：任务计划程序 → 创建任务 → 触发器“登录时” → 操作“启动程序”指向
  `cloudpath-edge_...exe`，参数 `-config <绝对路径>\edge.yaml`，起始于该目录；
  令牌用系统环境变量 `CLOUDPATH_TOKEN` 提供。
- **macOS**：`~/Library/LaunchAgents/` 下放一个 plist，`ProgramArguments` 指向二进制与
  `-config`，`RunAtLoad`/`KeepAlive` 置真；令牌用 `EnvironmentVariables` 或包装脚本提供。
- **Linux**：用户级 systemd unit（`systemctl --user enable --now cloudpath-edge.service`），
  `ExecStart=` 指向二进制，`Environment=` 只写变量名并从 0600 的 `EnvironmentFile=` 读值；
  串口权限需要账号在 `dialout`（Debian 系）或 `uucp`（Arch 系）组。

自启配置里同样**不要写明文令牌**：用环境变量文件或系统密钥管理，权限 0600。

## 7. 常见问题

| 现象 | 处理 |
|---|---|
| `401` / 连上就被断开 | 令牌错、已吊销或过期；找管理员重新签发 |
| `invalid edge_id` | `edge_id` 含非法字符或超长；只允许字母数字与 `-_`，1–64 |
| 启动即报未知 adapter | `adapter` 名字不在 `GET /api/adapters` 返回列表里 |
| `Serial port not found` | 端口名写错或没插线；Windows 用设备管理器看 COM 号，Linux `ls /dev/ttyUSB* /dev/ttyACM*`，macOS `ls /dev/cu.*` |
| Linux `permission denied` 打开串口 | 把账号加入 `dialout`/`uucp` 组后重新登录 |
| 串口被占用 | 关掉其它串口工具（串口独占）；同一端口不能被两个 edge 同时用 |
| 连不上但网络正常 | `server` 少了 `/ws/edge` 后缀，或公网用了 `ws://` 而不是 `wss://` |
| WS 握手被拒（403） | 服务端 Origin 白名单问题属于管理员侧；Edge 不带 Origin，通常是令牌问题 |
| 页面看不到我的设备 | 让管理员确认登录的是**同一个租户**的账号；跨租户互相不可见 |

## 8. 卸载

删掉下载的二进制与 `edge.yaml` 即可（客户端不写注册表、不装内核驱动、不在系统目录留文件）。
配过自启的话先移除对应的计划任务 / LaunchAgent / systemd unit。
数据都在 Server 端：管理员可按 `edge_id` 看到你历史上报的事件与命令记录。

## 9. 相关文档

| 文档 | 内容 |
|---|---|
| [../../edge.example.yaml](../../edge.example.yaml) | 配置母版（含逐行注释） |
| [../../README.md](../../README.md) | 项目总览、本地快速开始、配置参考 |
| [../../docs/protocol.md](../../docs/protocol.md) | Edge ↔ Server 线上协议契约 |
| [../../docs/security.md](../../docs/security.md) | 令牌、Origin、限流与安全基线 |
| [../../docs/architecture/plugin-system.md](../../docs/architecture/plugin-system.md) | 外部 Driver 插件宿主（`plugin_host`） |
| [../README.md](../README.md) | 服务端部署 SOP |
