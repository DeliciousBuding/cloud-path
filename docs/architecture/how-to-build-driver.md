# How to Build a New CloudPath Driver

本文是「新增一种硬件时，如何不修改 CloudPath Core」的操作入口。Core 只认识
`Device / Entity / Capability / Observation / Event / Command`，不识别任何具体硬件。
你只需要实现一个 **Driver Plugin**，把硬件翻译成这套能力模型。

## 1. 快速起点

1. 复制官方模板 `templates/go-plugin/driver/` 为新仓库，例如
   `cloud-path-driver-esp32`。
2. 用 `python scripts/rename.py` 改 plugin id、Go module、二进制名、标题和能力字面量；
   模板内的说明见 `templates/go-plugin/README.md`。
3. 把 `go.mod` 里指向本地 core 的 `replace` 去掉，改为依赖已发布的
   `github.com/DeliciousBuding/cloud-path vX.Y.Z`。
4. 实现你的 `driver.DriverServer`；只依赖公开 SDK（`sdk/go/cloudpath/v1/*`），
   **不得 import `github.com/DeliciousBuding/cloud-path/internal/*`**。
   校验器 `scripts/validate_manifest.py plugin.yaml --dir .` 会强制这条红线。

参考实现：<https://github.com/DeliciousBuding/cloud-path-driver-stcb>（STC-B，串口，14 entity）。

## 2. 必须实现的契约

Driver Protocol v1 的接口在
`sdk/go/cloudpath/v1/driver.DriverServer`。一个最小但完整的 Driver 需要：

| 方法 | 作用 | 必须注意 |
|---|---|---|
| `Initialize` | 握手、协商协议版本 | 返回协商版本；版本不匹配要 fail，不能静默接受 |
| `Describe` | 声明稳定 driver id + Capability 描述 | capability 用 `cloudpath.dev/capability/<name>@<semver>`；破坏性变化升 `@2`，不原地改 `@1` |
| `ConfigureInstance` | 保存插件实例配置 | 物理绑定（串口/设备 ID 等）从这里进入；拒绝非法 JSON |
| `Discover` | 上报设备 | 没有自动扫描时如实返回 0 或配置指向的那台设备 |
| `OpenDevice` | 打开真实硬件 | 多设备 Driver 按 `device_id + connection_hints` 打开对应端口；配置缺失/打开失败不得伪装在线 |
| `CloseDevice` | 关闭硬件 | 幂等，不泄漏句柄 |
| `Watch` | 推送 Entity/Observation/Event/DeviceStatus | 每实例/设备用单调 sequence；观测值来源必须是真实状态 |
| `Execute` | 执行命令 | 按请求中的 `device_id` 路由；**不把「写串口成功」当成「设备执行成功」**，按真实 ACK/ERROR 返回 SUCCEEDED/FAILED |
| `Health` | 健康检查 | Host 据此判断 `plugin healthy` |
| `Shutdown` | 优雅退出 | 释放串口/子进程，不依赖 kill |

`Describe` 不只是自述给 Edge 看：Edge 会把它转成 Capability 文档并经 `capabilities` 消息上报
Server，最终出现在 `GET /api/capabilities` 与前端 Schema 驱动 UI 上。所以这些字段是**用户可见的**，
不是装饰：

- `Title` → 能力卡片标题；
- `Properties[].Unit` / `Access` → 观测值的单位与读写标记；
- `Actions[].Name` → WebUI 命令面板里的按钮（命令集就来自这里，前端不硬编码任何硬件）；
- `Actions[].InputSchemaJSON` → 命令参数表单。留空则只能下发无参命令。

`pluginmain` 会负责把 `DriverServer` 发布为可被 Plugin Host 拉起的进程；入口参考
`cloud-path-driver-stcb/cmd/cloudpath-driver-stcb/main.go`。

## 3. Manifest（plugin.yaml）

`plugin.yaml` 是安装/信任/权限披露的机器契约，一经发布字段不得随意改变：

- `id`：稳定且与 `go.mod` module、二进制名保持可推导关系。
- `contributes.drivers[0].id`：稳定 driver id，等于 `Describe().DriverID`，
  也等于 `edge.yaml` 里的 `devices[].adapter`。
- `capabilities`：必须与 `Describe` 返回的 Capability 清单一致。
- `permissions`：只声明实现真的会用到的权限；`hardware: [serial]` 之类不得在 patch 静默扩大。
- `compatibility.core`：声明支持的 Core 版本范围，避免不兼容安装。

用 `scripts/validate_manifest.py plugin.yaml --dir .` 本地校验；安装时
`cloudpath plugin inspect/install` 还会按 `spec/plugin-manifest.schema.json` 复核。发布的二进制已内嵌这份
schema（`pluginschema.go`），所以在没有仓库 checkout 的干净机器上也能直接安装；`-schema PATH` 只用于覆盖。

## 4. 配置（物理绑定）

Driver 配置分两层，避免单实例多设备时后一块板覆盖前一块板：

- **实例默认配置**：`ConfigureInstance` 保存插件级默认值和策略。
- **设备物理绑定**：每次 `OpenDevice` 的 `device_id + connection_hints` 携带该设备的
  `port / baud / name / protocol`。Edge 会从自己的 `edge.yaml devices[]` 生成这些提示；
  Server 不保存本机串口路径，也不把一个设备的端口复用给另一设备。
- `ExecuteRequest.device_id` 是命令路由键；多设备 Driver 不得用「当前设备」全局变量。
- 本地快速验证时，也可用 CLI 显式配置实例默认值后启动 host：

```bash
cloudpath plugin enable <plugin-id> -config <instance-config.json> ...
cloudpath plugin host ...
```

`cloud-path-driver-stcb/config.example.json` 是串口类 Driver 的实例配置样例。

## 5. 测试、发布、接入

```bash
go build ./...
go vet ./...
go test ./... -count=1
python scripts/validate_manifest.py plugin.yaml --dir .
```

推送 `v*` tag 触发 Release；给仓库加 GitHub topic `cloudpath-plugin` 后，
`cloudpath plugin search` 能发现，`cloudpath plugin install <repo> --digest sha256:<hex>` 可安装。

匿名 GitHub API 只有 60 次/小时（按出口 IP），discover/install 很容易撞 `ERR_RATE_LIMITED`。
设置 `GITHUB_TOKEN`（或 `GH_TOKEN`）后 CLI 会带 `Authorization: Bearer` 走认证额度；令牌只从环境读，
不落 lock、不落日志、不进安装证据。
真板验证必须走：

```text
WebUI → Server → Edge → Driver → 硬件 → ACK/Event/State → Server → WebUI
```

不能靠代码存在、CI 绿或模拟器补绿冒充硬件完成。

## 6. 完成标准（自查）

- [ ] Core 未新增任何 `internal/*` 特例，只新增 Driver 仓库。
- [ ] `cloudpath plugin install` 校验 digest/权限/compatibility 通过。
- [ ] Edge 加载 Driver 后设备自动出现，状态/事件实时上行。
- [ ] WebUI 命令按钮来自 Capability actions，不硬编码该硬件。
- [ ] 命令有真实 ACK/ERROR 语义；离线时 Server 诚实显示 offline。
- [ ] 驱动只依赖公开 SDK，`validate_manifest.py` 无 internal import。