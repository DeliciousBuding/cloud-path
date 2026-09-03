# Cloudpath HTTP API 契约

最后更新：2026-09-04

> 管理台与自动化客户端的唯一 HTTP 契约 SSOT。边缘节点走 WebSocket 协议，见 `protocol.md`。
> 基础路径：管理台同源（默认 `http://127.0.0.1:8080`）。所有 JSON 为 UTF-8。

## 1. 安全模型（三级）

| 级别 | 场景 | 必备配置 | 效果 |
|---|---|---|---|
| L0 单机 | 本机自用 | 默认（`-addr 127.0.0.1:8080`，无 token、无用户） | 全功能开放；**写操作仅接受回环来源**（绑定非回环时对外 403） |
| L1 内网/反代 | 团队内网或 TLS 反代后 | `-allowed-origins`；建议 `-token` | WS Origin 收紧；令牌客户端可写 |
| L2 公网 | 互联网暴露 | `-token` 或已创建账号（账号模式自动强制）；`-allowed-origins`；TLS 反代；可选 `-require-auth` | 读/写全部鉴权；限流/安全头/CSP 全开 |

不变量（实现必须满足，测试锁定）：

1. **无凭据不写**：未携带有效凭据（会话 cookie / Bearer 服务令牌）时，任何写操作（`POST /api/devices/*/commands`）只允许回环来源地址；否则 `403`。
2. **账号模式全鉴权**：一旦存在用户（setup 完成），除 `/healthz`、静态资源、`/api/auth/*` 外，全部 `/api/*` 与 `/ws` 需凭据；`-require-auth` 可在无用户时也强制读鉴权（配合服务令牌）。
3. **服务令牌**：`-token`（env `CLOUDPATH_TOKEN`）为共享服务令牌，等价 admin，用于 edge 接入与自动化；接受 `Authorization: Bearer <token>` 或查询参数 `?token=`（仅 WS 等无法带 header 的场景）。
4. **限流**：命令下发 `-cmd-rate`（默认 20/分/设备）→ `429`；登录 `-login-rate`（默认 5/分/IP）→ `429`（带 `Retry-After`）。
5. **安全响应头**（所有响应）：`X-Content-Type-Options: nosniff`、`X-Frame-Options: DENY`、`Referrer-Policy: no-referrer`、`Permissions-Policy: camera=(), microphone=(), geolocation=()`、CSP `default-src 'self'; script-src 'self' 'sha256-<内联主题脚本>'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self' ws: wss:; frame-ancestors 'none'; base-uri 'self'; form-action 'self'`。
6. **缓存**：`/`（index.html）`Cache-Control: no-cache`；`/assets/*`（内容哈希文件名）`public, max-age=31536000, immutable`。

## 2. 认证与多租户

### 2.1 实体

- `tenant`：`(id, slug, name, created_at)`。首装自动创建 `default`。所有设备/事件/命令行携带 `tenant_id`，查询按会话租户过滤（数据隔离）。
- `user`：`(id, tenant_id, username, name, role, password_hash, created_at, disabled)`。`role ∈ admin|operator|viewer`：admin=管理（P3 用户管理 API）、operator=可读可写、viewer=只读。密码哈希 argon2id（参数见实现常量），永不落明文。
- `session`：服务端会话表 `(id, user_id, created_at, expires_at, last_seen_at)`；cookie `cp_session`（HttpOnly、SameSite=Lax、登录态变更后轮换 id 防会话固定；反代 TLS 下加 Secure）。TTL `-session-days`（默认 7）。

### 2.2 端点

| 方法 路径 | 凭据 | 说明 |
|---|---|---|
| `POST /api/auth/setup` | 无（仅当用户数为 0） | 首装引导：创建 default 租户 + 首个 admin。已有用户 → `409` |
| `POST /api/auth/login` | 无 | `{username,password}` → `200 {user}` 并 set-cookie；错 → `401`；限流 → `429` |
| `POST /api/auth/logout` | 会话 | `204`，删会话清 cookie |
| `GET /api/auth/me` | 会话/令牌 | `{user:{id,username,name,role,tenant_id,tenant_slug}}`；无 → `401` |
| `GET /healthz` | 无 | 存活探针 `{ok,version,uptime_s,devices_online,devices_total,edges_online}` |
| `GET /api/devices` | 读 | 设备视图列表（含在线/状态/槽位） |
| `GET /api/devices/{edge}/{device}` | 读 | 单设备 |
| `POST /api/devices/{edge}/{device}/commands` | 写 | `{cmd,args}`；`cmd` 必须在适配器白名单；args ≤64 字节且无换行/NUL；限流 `429` |
| `GET /api/events?device=&since=&limit=` | 读 | 事件流（limit 默认 200，上限 1000） |
| `GET /api/edges` | 读 | 边缘节点（含离线） |
| `GET /api/commands?device=&status=&limit=` | 读 | 命令历史 |
| `GET /api/adapters` | 读 | 适配器命令白名单（前端命令面板事实源） |
| `GET /api/devices/{edge}/{device}/descriptor` | 读 | 单设备 Descriptor（Schema-driven UI 事实源） |
| `GET /api/descriptors` | 读 | 会话可见的全部设备 Descriptor + 随行 Capability catalog |
| `GET /api/capabilities` | 读 | Capability 列表 |
| `GET /api/overview` | 读 | Overview 首屏聚合读面（§5.1） |
| `GET /api/plugins` | 读 | 插件目录（§5.2） |
| `GET /api/plugins/{pluginID}` | 读 | 单插件视图（§5.2） |
| `GET /api/plugin-instances` | 读 | 插件实例列表（§5.3） |
| `GET /api/plugin-instances/{id}` | 读 | 单插件实例（§5.3） |
| `GET /api/audit?since=&action=&limit=` | admin | 审计日志（本租户，limit 上限 1000） |
| `GET /api/stats` | 读 | 计数/保留期/`auth_enabled`/`schema_version` |
| `GET /ws` | 读 | 浏览器实时通道（快照 + fan-out）；Origin 策略见下 |
| `GET /ws/edge` | 服务令牌 | edge 接入；hello 携带 `token` |

错误统一 `{"error":"<msg>"}`；`401` 未认证、`403` 无权限/来源受限、`404` 不存在、`409` 冲突、`429` 限流。

### 2.3 WS Origin 策略

`-allowed-origins`（逗号分隔 host 模式，支持 `*.example.com`）；留空 = 开发策略（请求同源 + localhost/127.0.0.1 任意端口）并在启动日志告警。非浏览器客户端（edge）不带 Origin，不受影响。

## 3. 多租户演进（P2 落地 / P3 扩展）

- P2：租户列 + 隔离查询 + 默认租户；edge hello 可带 `tenant`（缺省 default）；旧 `-token` 绑定 default 租户。
- P3：每租户多服务令牌、用户/RBAC 管理、租户切换 UI、按租户保留期。

### 3.1 RBAC

| 角色 | 权限 |
|---|---|
| `viewer` | 读取本租户设备、状态、事件、命令历史和插件状态 |
| `operator` | viewer + 下发设备命令、操作已授权 Plugin/Application Instance |
| `admin` | operator + 管理本租户用户、服务令牌、插件安装/权限与租户设置 |

跨租户资源统一返回 `404`；已认证但角色不足返回 `403`。禁用用户或重置密码后撤销其全部会话。
最后一个可用 admin 不允许被禁用或降级，返回 `409`。

### 3.2 用户管理端点

| 方法 路径 | 权限 | 说明 |
|---|---|---|
| `GET /api/users` | admin | 本租户用户列表；永不返回 password hash |
| `POST /api/users` | admin | `{username,name,role,password}`；username 在租户内唯一 |
| `PATCH /api/users/{id}` | admin | 修改 `name/role/disabled`；可选重置 `password` |

### 3.3 租户服务令牌

令牌格式为 `cp_` + 至少 32 字节随机数据。数据库只存 SHA-256 hash 与短 prefix，明文只在创建响应中返回一次；
比较使用常量时间，不得写日志。scope 为 `read|write|admin|edge` 的非空集合，权限取 scope 与角色模型的交集。

| 方法 路径 | 权限 | 说明 |
|---|---|---|
| `GET /api/tokens` | admin | 列出 id/name/prefix/scopes/created_at/last_used_at/revoked_at |
| `POST /api/tokens` | admin | `{name,scopes,expires_at?}` → `{token,...metadata}`；明文仅本次响应 |
| `DELETE /api/tokens/{id}` | admin | 吊销令牌，幂等返回 `204` |

兼容：旧 `-token` / `CLOUDPATH_TOKEN` 仍等价 default 租户 admin，但标记为 legacy；新部署优先使用租户 token。

## 4. 版本与兼容

- 契约变更随 `docs/design.md` 实现偏差记录同步；WS envelope `v` 字段独立演进（`protocol.md`）。
- 旧客户端（仅服务令牌）在账号模式下继续可用（令牌等价 admin）。

## 5. 插件控制面与聚合读面（v0.1）

> desired/observed 同步语义 SSOT：[architecture/control-plane-sync.md](architecture/control-plane-sync.md)；
> WS 侧 desired/status/ack 消息见 [protocol.md](protocol.md)。
> 不变量：**Desired 只由 Server 权威写入，Observed 只由 Edge 真实上报**，二者永远分开呈现，
> 绝不把「期望启用」渲染成「实际健康」。

### 5.1 `GET /api/overview`（读）

WebUI 首屏一次性聚合。所有计数来自真实 Edge 上报与 Server 权威态，禁止占位/假数据。载荷：

| 字段 | 含义 |
|---|---|
| `devices_online` / `devices_total` | 设备在线数 / 总数 |
| `edges_online` / `edges_total` | Edge 在线数 / 总数 |
| `plugins_active` / `plugins_desired` | 插件 observed 活跃数 / desired 启用数 |
| `commands_failed` | 失败命令数 |
| `recent_events` | 近期事件（`EventView[]`，新→旧） |
| `offline_devices` | 离线设备（`DeviceView[]`） |
| `failed_commands` | 失败命令（`CommandView[]`） |
| `server_time` | 服务器当前 Unix 秒 |

### 5.2 插件目录（读）

- `GET /api/plugins` → `{"plugins":[PluginView…]}`：当前租户可见的已安装插件目录；
  catalog 未配置时返回空列表（真实空态，不是错误）。
- `GET /api/plugins/{pluginID}` → 单个 `PluginView`；不存在 `404 {"error":"plugin not found"}`；
  catalog 内部错误 `500 {"error":"plugin catalog unavailable"}`。

### 5.3 插件实例（读）

`GET /api/plugin-instances` → `{"instances":[PluginInstanceView…]}`；
`GET /api/plugin-instances/{id}` → 单实例视图。跨租户/未知 id 一律 `404`（不泄漏存在性）。

`PluginInstanceView` 关键字段：

| 字段 | 含义 |
|---|---|
| `id` / `tenant_id` / `edge_id` | 实例主键与归属 |
| `desired` | Server 权威期望态 `{instance_id,plugin_id,version,enabled,isolation,config,secret_refs,revision,updated_at}`；`secret_refs` 只含 `secret://<name>` handle，永无明文 |
| `has_observed` / `observed` | Edge 上报投影（只有真实上报过才存在，Server 不合成）：`{state,health,version,detail,restart_count,last_healthy,reported_at}` |
| `edge_online` | 所属 Edge 是否在线 |
| `desired_revision` / `applied_revision` | 当前期望 revision vs Edge 已 ack applied 的 revision |
| `drift` | desired 与 applied 不一致 |
| `stale` | observed 上报超时（不可信） |
| `last_ack_at` | Edge 最近一次 ack 时间 |

### 5.4 插件实例写面（operator+）

| 方法 路径 | 权限 | 载荷 |
|---|---|---|
| `POST /api/plugin-instances` | operator | `{edge_id,instance_id,plugin_id,version,enabled?,isolation?,config?,secret_refs?,confirm_permissions?}` |
| `PATCH /api/plugin-instances/{id}` | operator | 字段全可选（只更新出现的字段）：`{version?,enabled?,isolation?,config?,secret_refs?,confirm_permissions?}` |
| `DELETE /api/plugin-instances/{id}` | operator；`purge:true` 需 admin | `{purge?}`；默认保留插件本地数据 |
| `POST /api/plugin-instances/{id}/reconcile` | operator | `{force?}`；重推当前 desired 快照 |

写成功统一 `200`（`PluginInstanceWriteResponse`）：`{id,revision,request_id,instance}`；
`revision` 为本次写后 tenant/edge 的新 desired revision。

写不变量（测试锁定）：

1. 每次合法写 = RBAC → 权限/配额检查 → 事务（desired revision +1）→ 审计 → WS 向目标 Edge 推最新快照。
2. `config`/`secret_refs` 只接受非敏感标量或 `secret://<name>` handle；键名形似凭据却给明文值 →
   `403 plugin_secret_forbidden`。明文 secret 永不进 DB / WS / 审计 / 日志。
3. 权限扩大类变更需 admin 或显式 `confirm_permissions:true`，否则
   `403 plugin_permission_confirmation_required`，且不产生新 revision。
4. 同租户 `instance_id` 重复 → `409 plugin_instance_conflict`。
5. `reconcile` 时目标 Edge 离线或发送队列满 → `409 plugin_edge_offline`（期望态已保存，
   Edge 重连后自动收敛）。

### 5.5 插件写面稳定错误码

错误响应统一 `{"error":"<code>","code":"<code>","message":"<人读文本>","request_id":"…"}`。
前端按 `code` 呈现，绝不解析 `message`。

| code | HTTP | 触发条件 |
|---|---|---|
| `plugin_invalid_config` | 400 | 载荷非法、config 键值不合法或超限 |
| `authentication_required` | 401 | 无有效凭据的写操作（非回环来源） |
| `plugin_secret_forbidden` | 403 | config/secret_refs 中出现明文 secret |
| `plugin_permission_confirmation_required` | 403 | 权限扩大未显式确认（非 admin） |
| `plugin_instance_not_found` | 404 | 实例不存在或跨租户 |
| `plugin_instance_conflict` | 409 | `instance_id` 已存在 |
| `plugin_edge_offline` | 409 | reconcile 时目标 Edge 离线 / 发送队列满 |
| `plugin_quota_exceeded` | 429 | 租户插件实例配额已满 |
| `plugin_store_unavailable` | 503 / 500 | 插件存储未接线（503）或写入失败（500） |

### 5.6 已知边界（v0.1 接受）

- **令牌会话无实时通道**：用租户服务令牌登录的 WebUI 只有 REST（Authorization header）。
  浏览器 `WebSocket` 无法携带自定义 header，而账号模式下 `/ws` 以会话 cookie 鉴权，因此令牌会话
  没有实时推送；UI 诚实显示「实时通道已断开」并定时刷新页面数据。账号密码会话（cookie）功能完整。
- `applied_revision` 仅由 Edge `plugin_ack(applied)` 推进；WS 下发是尽力而为，断线重连以全量
  desired 快照收敛（不回放断线期间的中间状态），详见 protocol.md / control-plane-sync.md。
