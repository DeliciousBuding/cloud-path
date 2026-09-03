# Cloudpath 安全与运维基线

最后更新：2026-09-03

> 部署者前置阅读。安全模型 SSOT 为 [api.md](api.md)；本文把契约落到
> 实际操作、风险分级和部署检查点。若代码/契约更新，以 [api.md](api.md) 和
> 当前 `cmd/cloudpath-server` 的帮助输出为准。

## 1. 暴露面

| 组件 | 暴露内容 | 默认边界 |
|---|---|---|
| `cloudpath-server` | REST `/api/*`、WS `/ws`、WS `/ws/edge`、内嵌 WebUI、`/healthz` | `127.0.0.1:8080` |
| `cloudpath-edge` | 本机串口/设备、WS 客户端 | 不监听网络端口 |
| SQLite 数据 | 设备元数据、状态、事件、命令、用户/会话 | 仅 server 进程/数据卷 |
| 浏览器管理台 | 同源静态资源 + 实时通道 | 与 server 同源 |

不要让以下内容直接暴露到互联网：`/data`、`edge.yaml`、`CLOUDPATH_TOKEN`、
部署 `.env`、串口设备。只有 443 由反向代理转发。

## 2. 风险分级（L0 / L1 / L2）

| 级别 | 场景 | 必备配置 | 主要风险与补救 |
|---|---|---|---|
| L0 单机 | 本机自用 | 默认：`-addr 127.0.0.1:8080`，无 token | 本机其他用户/恶意页面；保持回环绑定，不开放端口 |
| L1 内网/反代 | 团队内网或 TLS 反代后 | `-allowed-origins`；建议 `-token`；建议反代 TLS | 内网嗅探、跨站 WS；显式 Origin + 令牌 + TLS |
| L2 公网 | 互联网暴露 | `-token` + `-require-auth`；`-allowed-origins`；TLS 反代；限流/安全头 | 扫描、撞库、命令下发；所有 `/api/*` 与 `/ws` 必须凭据 |

`docs/api.md` 的三级模型同时定义了不变量：无凭据不得写、账号模式全鉴权、
服务令牌等价 admin、命令/登录限流、安全响应头、缓存策略。部署配置必须与
目标级别匹配，不能把 L0 配置直接放到公网。

当前实现（2026-09-03）已支持：`-token`、`-require-auth`、`-allowed-origins`、
`-login-rate`、`-session-days`、账号 setup/login/logout/me、会话 cookie、
L0 回环写放行和账号模式全鉴权。对应环境变量为 `CLOUDPATH_TOKEN`、
`CLOUDPATH_REQUIRE_AUTH`、`CLOUDPATH_ALLOWED_ORIGINS`、`CLOUDPATH_LOGIN_RATE`、
`CLOUDPATH_SESSION_DAYS`。

## 3. Token 模式

### 3.1 配置

- server：`-token <value>` 或 `CLOUDPATH_TOKEN=<value>`。
- edge：`token: ${CLOUDPATH_TOKEN}`（见 `edge.example.yaml`），启动时展开。
- 浏览器管理台/脚本：REST 请求使用 `Authorization: Bearer <value>`；浏览器
  WS 在无法自定义 header 的场景下使用 `?token=<value>`。

### 3.2 令牌强度与轮换

```bash
# Linux/macOS
openssl rand -hex 32
# Windows PowerShell
python -c "import secrets; print(secrets.token_hex(32))"
```

- 使用至少 256 bit 随机值，不要用 `123456`、主机名、项目名。
- 令牌只放环境变量、容器 secret 或本机未入库配置文件；`edge.yaml` 本身不入库。
- 轮换：同时更新 server 与所有 edge，再重启。公开泄露时立即轮换并检查
  `/api/commands` 是否出现异常写入。
- 日志中只记录是否启用 auth，不打印令牌值；请求日志也不应打印
  `Authorization` / `?token=`。

### 3.3 令牌作用

`CLOUDPATH_TOKEN` 一旦设置：edge `hello` 必须携带相同 token，浏览器
`/ws` 与 `/ws/edge` 受 token/Origin 校验，REST 写操作要求 Bearer。服务令牌
等价 admin（default 租户）。仅设 token 时只读 `/api/*` 仍按 L0/L1 策略；
公网应同时开启 `-require-auth`（`CLOUDPATH_REQUIRE_AUTH=true`），或先完成
账号 setup 进入账号模式。

## 4. `-allowed-origins` 与 WS Origin

- 命令形式：`-allowed-origins "console.example.com,*.example.com"`；
  环境变量：`CLOUDPATH_ALLOWED_ORIGINS`。
- 值是逗号分隔的 host 模式；不需要 `http://` / `https://` 前缀。
- 浏览器 Origin `https://console.example.com` 与
  `https://console.example.com.evil.test` 会按精确 host/通配规则区分，
  不能靠子串白名单。
- 端口不是 443 时必须写 host:port；例如 `console.example.com:8443`。
- 留空是开发策略：请求同源 + `localhost/*/127.0.0.1/*/[::1]/*`，启动日志会警告。
- 非浏览器客户端（edge、CLI）通常不带 Origin，不受该策略影响；它们仍受
  token 鉴权约束。
- 反代必须透传 `Host`，否则 Origin 策略可能误判。

## 5. 鉴权与 `-require-auth`

- `-token`：共享服务令牌模式，适合 edge、脚本、早期部署。
- `-require-auth`：在没有用户时也强制 `/api/*` 与 `/ws` 读/写鉴权；
  配合 `-token` 使用即可在 L2 形态下先隔离公网流量。
- 账号模式：`POST /api/auth/setup` 创建 default 租户和首个 admin；
  登录 `POST /api/auth/login` 返回会话 cookie `cp_session`
  （HttpOnly、SameSite=Lax、TLS 反代下 Secure）；`/api/auth/*` 豁免全鉴权。
- 完成 setup 后，即使未显式 `-require-auth`，也会自动进入账号模式
  （静态资源与 `/healthz` 除外）。
- 无论使用哪种模式，不要让 edge 使用浏览器 Origin；edge 接入只应来自受控
  主机/内网，并使用令牌。

## 6. 安全响应头

应用当前在全部响应上设置以下头：

| Header | 值 | 状态 |
|---|---|---|
| `X-Content-Type-Options` | `nosniff` | ✅ |
| `X-Frame-Options` | `DENY` | ✅ |
| `Referrer-Policy` | `no-referrer` | ✅ |
| `Permissions-Policy` | `camera=(), microphone=(), geolocation=()` | ✅ |
| `Content-Security-Policy` | `default-src 'self'; script-src 'self' 'sha256-jKH63gcAPxRiFu8qDqGCGYrEoEL5nCbt8h3hWkIeBB0='; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self' ws: wss:; frame-ancestors 'none'; base-uri 'self'; form-action 'self'` | ✅ |
| 静态缓存 | `/` = `no-cache`；`/assets/*` = `public, max-age=31536000, immutable` | ✅ |

部署注意事项：

- 反代不要用 `add_header Content-Security-Policy ...` 覆盖应用头；CSP
  含构建时内联脚本 hash，错误覆盖会破坏管理台。若必须由反代补充，读取当前
  实际 hash 后保持精确。
- 应用已返回上述安全头；nginx 示例只补 HSTS 与 `Permissions-Policy` 的
  反代层加强，避免重复添加相同头（`Permissions-Policy` 由应用返回，nginx
  示例仅作额外显式设置）。
- TLS 建议增加 `Strict-Transport-Security`（示例在 `deploy/nginx.conf`）。
- 反代必须设置 `X-Forwarded-Proto: https`，否则会话 cookie 在 TLS 反代后
  可能不会被标记 Secure。

## 7. 输入与资源防护

- 命令白名单由适配器声明；`cmd` 必须命中白名单，`args` 长度/字符校验，
  `edge_id`、设备归属校验（契约见 [api.md](api.md)）。
- `-cmd-rate` 默认 20 次/分/设备；`-login-rate` 默认 5 次/分/IP，超限返回
  `429 + Retry-After`。
- 请求体、WS 读限和 SQLite 连接均有上限/超时；部署时无需自行加全局
  `client_max_body_size` 覆盖业务。
- 日志脱敏：不输出 token、cookie、串口配置明文；采集日志时设置访问控制。

## 8. L2 公网部署基线

1. server 只绑 `127.0.0.1` 或内网；公网只由反代监听 443。
2. TLS 证书来自受信 CA；HTTP 301 到 HTTPS。
3. `CLOUDPATH_TOKEN` 随机且只通过 secret/env 注入。
4. `CLOUDPATH_ALLOWED_ORIGINS` 显式精确 host（必要时 host:port）。
5. 公网启用 `CLOUDPATH_REQUIRE_AUTH=true`；或先完成账号 setup。
6. 启用 `-cmd-rate`、`-login-rate`、`-retention-days`、JSON 日志。
7. 备份 `/data`；监控 `/healthz`；异常 `401/429` 和命令写入告警。
8. 升级/回滚前保存数据库与镜像/二进制版本。

## 9. 安全运维检查表

- [ ] 仓库无真实 token/串口路径/内网主机名
- [ ] `.env`/`edge.yaml` 未入库
- [ ] server 绑定地址正确（容器 0.0.0.0 但仅反达；宿主机 127.0.0.1）
- [ ] `CLOUDPATH_ALLOWED_ORIGINS` 与访问域名/端口匹配
- [ ] 公网 `CLOUDPATH_REQUIRE_AUTH=true` 或完成 setup
- [ ] TLS 与 HSTS 配置通过 `nginx -t`
- [ ] 健康检查、日志采集、备份与告警已配置
- [ ] 参考 [deploy.md](deploy.md) 完成部署
