# Cloudpath HTTP API 契约

最后更新：2026-09-03

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
