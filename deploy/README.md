# CloudPath 部署 SOP（原生二进制 + systemd + nginx）

最后更新：2026-09-04

> 目标形态：**原生 arm64 Linux 二进制 + systemd 单元 + nginx 反代（HTTPS + WSS）**，不使用容器。
> 本文是逐条可执行的落地手册；容器/compose 形态见 [docs/deploy.md](../docs/deploy.md)，
> 安全基线见 [docs/security.md](../docs/security.md)，客户端分发见 [deploy/edge/README.md](edge/README.md)。
>
> 本文所有主机名、域名、证书路径、用户名都是**占位示例**，落地时替换为你的实际值。
> 仓库内不出现任何真实凭据；填好的 env 文件只存在于目标主机（0600）。

## 0. 前置事实（开工前逐条确认）

| 项 | 要求 | 确认命令（目标主机） |
|---|---|---|
| CPU 架构 | 与产物架构一致（本 SOP 以 `aarch64`/arm64 为例） | `uname -m` |
| 模拟回退 | **不要依赖** qemu/binfmt：架构不匹配必然 `exec format error` | `uname -m` 对照产物断言 |
| init | systemd | `systemctl --version` |
| 反代 | nginx 已安装且 active，监听 80/443 | `nginx -v`、`systemctl is-active nginx` |
| 权限 | 执行账号有 sudo | `sudo -v` |
| 磁盘 | 数据目录所在分区有足够余量（SQLite + 事件保留期） | `df -h /var/lib` |
| DNS | 站点域名已解析到本机入口（走 CDN 时为 CDN 回源目标） | 由 DNS 侧确认 |
| TLS | 证书链 + 私钥已在本机，私钥仅 root 可读 | `ls -l <fullchain> <privkey>` |

约定本文使用的环境变量（先设置，后面命令直接复制）：

```bash
# 本机（构建/发布机）
VERSION=v0.1.0                      # 与 git tag 一致
SITE=console.example.com            # 对外域名（示例，替换为你的域名）

# 目标主机
HOST=deploy-user@deploy-host        # ssh 目标（示例，替换为你的主机）
PORT=8080                           # 应用回环端口，与 unit 中 CLOUDPATH_ADDR 一致
```

---

## 1. 步骤 1 — 架构断言（**上线前必做，硬门禁**）

生产主机是原生 arm64 且没有模拟回退，因此**任何一次投递前都必须断言产物架构**。
断言由 [scripts/assert_arch.py](../scripts/assert_arch.py) 完成：它同时解析可执行文件容器头
（ELF/PE/Mach-O）与 `go version -m` 的构建设置，两个独立来源必须一致，并可断言
`embed_ui` 构建标签；不满足即**非零退出**（不是打印警告）。

### 1a. 本机构建 + 断言

```bash
# 只构建生产需要的 Linux arm64 server（内含 WebUI），随后自动断言
task build:linux-arm64

# 或构建全平台发布矩阵（server/edge/cli × 6 平台）+ checksums.txt
task build:matrix
python scripts/build_matrix.py --verify-only --out dist   # 复跑断言 + 校验 checksums
```

单独复跑断言（产物已在 `bin/` 或 `dist/`）：

```bash
python scripts/assert_arch.py --expect-os linux --expect-arch arm64 --expect-tags embed_ui \
  bin/cloudpath-server_linux_arm64
# 期望输出 arch assertion PASS，退出码 0
echo "exit=$?"
```

> `--expect-tags embed_ui` 不是可选项：漏掉该标签会产出 API-only server，
> 站点能起但打开只有提示页，看起来像“部署成功”却是坏的。

### 1b. 用 Release 资产时

```bash
BASE=https://github.com/DeliciousBuding/cloud-path/releases/download/${VERSION}
curl -fsSLO "${BASE}/checksums.txt"
curl -fsSLO "${BASE}/cloudpath-server_${VERSION}_linux_arm64"
sha256sum -c checksums.txt --ignore-missing
python scripts/assert_arch.py --expect-os linux --expect-arch arm64 --expect-tags embed_ui \
  "cloudpath-server_${VERSION}_linux_arm64"
```

产物命名规范（同 [CHANGELOG.md](../CHANGELOG.md)）：

```text
cloudpath-server_<version>_<os>_<arch>[.exe]
cloudpath-edge_<version>_<os>_<arch>[.exe]
cloudpath_<version>_<os>_<arch>[.exe]
```

---

## 2. 步骤 2 — 传输产物并在主机侧复核

```bash
scp "dist/cloudpath-server_${VERSION}_linux_arm64" "$HOST:/tmp/cloudpath-server.new"
scp scripts/assert_arch.py "$HOST:/tmp/assert_arch.py"     # 主机侧复核用（stdlib only）
```

传输后**在主机侧再断言一次**（防传错文件、防拿错架构）：

```bash
ssh "$HOST" 'uname -m'                                      # 期望 aarch64
ssh "$HOST" 'python3 /tmp/assert_arch.py --expect-os linux --expect-arch arm64 --expect-tags embed_ui /tmp/cloudpath-server.new'
```

主机上没有 Python 3 时的等效快速核对（ELF 头第 18–19 字节小端 `b7 00` = EM_AARCH64）：

```bash
ssh "$HOST" 'head -c 20 /tmp/cloudpath-server.new | od -An -tx1'
# 期望：7f 45 4c 46 02 01 01 00 ... 最后两字节 b7 00
ssh "$HOST" 'command -v file >/dev/null && file /tmp/cloudpath-server.new'
# 期望包含：ELF 64-bit LSB ... executable, ARM aarch64
```

---

## 3. 步骤 3 — 创建服务用户与目录

专用非 root 系统账号 + 三个固定路径（与 unit 中的路径一致）：

```bash
ssh "$HOST" 'sudo useradd --system --home-dir /var/lib/cloudpath --shell /usr/sbin/nologin cloudpath || id cloudpath'
ssh "$HOST" 'sudo install -d -o root -g root -m 0755 /opt/cloudpath/bin'
ssh "$HOST" 'sudo install -d -o cloudpath -g cloudpath -m 0750 /var/lib/cloudpath'
ssh "$HOST" 'sudo install -d -o root -g root -m 0750 /etc/cloudpath'
```

| 路径 | 用途 | 属主/权限 |
|---|---|---|
| `/opt/cloudpath/bin/cloudpath-server` | 可执行文件 | `root:root` `0755` |
| `/var/lib/cloudpath/` | SQLite（`.db`/`-wal`/`-shm`）持久化目录 | `cloudpath:cloudpath` `0750` |
| `/etc/cloudpath/cloudpath-server.env` | 机密与站点参数 | `root:root` `0600` |

安装二进制（首次；升级见 §9）：

```bash
ssh "$HOST" 'sudo install -o root -g root -m 0755 /tmp/cloudpath-server.new /opt/cloudpath/bin/cloudpath-server'
ssh "$HOST" '/opt/cloudpath/bin/cloudpath-server -h 2>&1 | head -5'   # 能打印用法 = 架构正确、可执行
```

> 如果这一步报 `Exec format error`，说明架构断言被跳过或拿错产物：回到 §1，
> 不要在主机上“想办法跑起来”。

---

## 4. 步骤 4 — 安装 systemd 单元与环境文件

```bash
scp deploy/systemd/cloudpath-server.service "$HOST:/tmp/"
ssh "$HOST" 'sudo install -D -o root -g root -m 0644 /tmp/cloudpath-server.service /etc/systemd/system/cloudpath-server.service'
```

环境文件模板 [systemd/cloudpath-server.env.example](systemd/cloudpath-server.env.example)
**只有变量名没有值**。在主机上创建 0600 空文件后用编辑器填写——
**不要用 shell 重定向写机密**（会进 shell 历史与进程列表）：

```bash
ssh "$HOST"
# —— 以下在目标主机上执行 ——
sudo install -o root -g root -m 0600 /dev/null /etc/cloudpath/cloudpath-server.env
openssl rand -hex 32          # 生成 CLOUDPATH_TOKEN 的值（复制进编辑器，不要留在历史里）
sudoedit /etc/cloudpath/cloudpath-server.env
ls -l /etc/cloudpath/cloudpath-server.env        # 期望 -rw------- 1 root root
sudo cut -d= -f1 /etc/cloudpath/cloudpath-server.env   # 只核键名，不回显值
```

公网形态必填项：

| 键 | 值要点 |
|---|---|
| `CLOUDPATH_TOKEN` | ≥256 bit 随机；Edge/自动化引导用；日常优先改用 `POST /api/tokens` 的租户令牌 |
| `CLOUDPATH_ALLOWED_ORIGINS` | 浏览器访问的 host（不带 scheme）；非 443 端口要写 `host:port` |
| `CLOUDPATH_REQUIRE_AUTH` | `true`（尚无账号时也强制鉴权） |
| `CLOUDPATH_TRUSTED_PROXIES` | `127.0.0.1`（同机 nginx）；否则审计与登录限流只看到代理 IP |

非机密默认值（监听地址、数据库路径、日志格式）已写在 unit 的 `Environment=` 里，
不需要重复进 env 文件；空值等价于未设置，服务会用内置默认值。

启动并检查：

```bash
ssh "$HOST" 'sudo systemctl daemon-reload'
ssh "$HOST" 'sudo systemctl enable --now cloudpath-server.service'
ssh "$HOST" 'systemctl is-active cloudpath-server'
ssh "$HOST" 'journalctl -u cloudpath-server -n 30 --no-pager'
```

启动日志应包含 `cloudpath-server listening addr=127.0.0.1:8080`，且
`require_auth=true`、`trusted_proxies=[127.0.0.1]`、`origins=[<你的域名>]`。

---

## 5. 步骤 5 — 首装管理员账号（**必须在放开公网入口之前**）

`POST /api/auth/setup` 的放行判据是**真实 TCP 对端是否回环**，而不是转发头：同机 nginx
转过来的请求对端就是 `127.0.0.1`，因此**公网访客也能触发首装**。所以顺序必须是：
先从主机本机回环完成首装，再放开 nginx 站点（§6）。首个用户落库后服务立即进入账号模式
（全鉴权），重复 setup 返回 `409 already set up`。

```bash
# 在目标主机上（回环来源）
curl -fsS -X POST http://127.0.0.1:${PORT}/api/auth/setup \
  -H 'Content-Type: application/json' \
  --data '{"username":"admin","password":"<换成强密码>","name":"Admin"}'
# 期望 200：{"user":{...,"role":"admin","tenant_slug":"default"}}
# 若 409 already set up：说明已被首装，立刻查 §5b 的审计记录确认是谁
```

首装后自查审计（确认没有非预期的 setup/登录记录）：

```bash
curl -fsS -X POST http://127.0.0.1:${PORT}/api/auth/login \
  -H 'Content-Type: application/json' -c /tmp/cp.jar \
  --data '{"username":"admin","password":"<换成强密码>"}'
curl -fsS -b /tmp/cp.jar "http://127.0.0.1:${PORT}/api/audit?limit=20"
rm -f /tmp/cp.jar          # 会话 cookie 也是凭据，用完即删
```

### 5b. 为每台接入电脑创建 Edge 令牌

租户令牌形状 `cp_...`，**明文只在创建响应里返回一次**（库里只存 SHA-256 hash）。
scope 取 `edge`：它只能连 `/ws/edge`，不能读 REST（实测 `GET /api/devices` 返回 `403 permission denied`）。

```bash
curl -fsS -X POST http://127.0.0.1:${PORT}/api/auth/login \
  -H 'Content-Type: application/json' -c /tmp/cp.jar \
  --data '{"username":"admin","password":"<换成强密码>"}'
curl -fsS -b /tmp/cp.jar -X POST http://127.0.0.1:${PORT}/api/tokens \
  -H 'Content-Type: application/json' \
  --data '{"name":"classmate-pc-01","scopes":["edge"]}'
rm -f /tmp/cp.jar
```

也可以在 WebUI 的管理页（`/admin`，仅 `role=admin` 可见）创建用户与令牌。
一人一令牌、一台电脑一个 `edge_id`；令牌通过带外方式交付，不要贴进公开群或 issue。
吊销：`DELETE /api/tokens/{id}`（幂等 204）。

---

## 6. 步骤 6 — nginx 站点（HTTPS + WSS）

```bash
scp deploy/nginx/cloudpath.vectorcontrol.tech.conf "$HOST:/tmp/cloudpath-site.conf"
ssh "$HOST" 'sudo install -D -o root -g root -m 0644 /tmp/cloudpath-site.conf /etc/nginx/sites-available/cloudpath.conf'
ssh "$HOST" 'sudo ln -s /etc/nginx/sites-available/cloudpath.conf /etc/nginx/sites-enabled/cloudpath.conf'
```

上线前必须替换的三处（文件内用 `>>> REPLACE <<<` 标出）：

1. `ssl_certificate` / `ssl_certificate_key` → 本机真实证书路径（私钥仅 root 可读，绝不入仓库）。
2. `set_real_ip_from` → 若有 CDN/前置代理，填其公布的 IP 段（模板中已注释示例）。
3. `server_name` → 你的域名（文件名与内容同步改）。

同时确认应用侧 `CLOUDPATH_TRUSTED_PROXIES=127.0.0.1`（§4），否则应用忽略
`X-Forwarded-*`，审计与登录限流会记成代理 IP。

鉴权归属：CloudPath **自带**登录/RBAC/多租户，所以这个站点在反代层是
**public** 的——不要在 nginx 侧再挂一层 auth_request / basic auth，
它会和应用自己的会话 cookie、WS 令牌互相打架。

验证并重载：

```bash
ssh "$HOST" 'sudo nginx -t'                    # 必须 syntax is ok / test is successful
ssh "$HOST" 'sudo systemctl reload nginx'
ssh "$HOST" 'sudo ss -ltnp | grep -E ":80 |:443 |:8080 "'
# 期望：80/443 由 nginx 监听在 0.0.0.0；8080 只在 127.0.0.1
```

> HTTP/2：模板默认用兼容写法（`listen 443 ssl;` + 注释掉的 `http2 on;`）。
> nginx ≥ 1.25.1 可取消注释 `http2 on;`；老版本用 `listen 443 ssl http2;`。
> 两种写法都不要同时启用。

---

## 7. 步骤 7 — 健康检查与验收

```bash
# 1) 主机回环
curl -fsS http://127.0.0.1:${PORT}/healthz
#    期望 {"ok":true,"version":"...","devices_online":0,"devices_total":0,"edges_online":0}

# 2) 公网 HTTPS
curl -fsS "https://${SITE}/healthz"

# 3) HTTP 强制跳转
curl -sI "http://${SITE}/" | head -3                 # 期望 301 + Location: https://...

# 4) HSTS 由反代补、CSP 由应用返回（不能被反代覆盖）
curl -sI "https://${SITE}/healthz" | grep -iE 'strict-transport|content-security-policy|x-frame-options'

# 5) WSS 路径打通：未带凭据应 401（说明 TLS + 反代 + 路由 + 鉴权都生效）
curl -s -o /dev/null -w '%{http_code}\n' --http1.1 \
  -H 'Connection: Upgrade' -H 'Upgrade: websocket' \
  -H 'Sec-WebSocket-Version: 13' -H 'Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==' \
  "https://${SITE}/ws"

# 6) 带 Edge 令牌的 /ws/edge 应升级为 101
curl -s -o /dev/null -w '%{http_code}\n' --http1.1 \
  -H 'Connection: Upgrade' -H 'Upgrade: websocket' \
  -H 'Sec-WebSocket-Version: 13' -H 'Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==' \
  "https://${SITE}/ws/edge?token=<edge 令牌>"

# 7) 真实 Edge 接入（在另一台电脑上跑，见 deploy/edge/README.md）
curl -fsS -H 'Authorization: Bearer <管理令牌>' "https://${SITE}/api/edges"
#    期望该 edge_id online:true，devices 列出 <edge_id>/<device_id>

# 8) 命令闭环
curl -fsS -H 'Authorization: Bearer <管理令牌>' -X POST \
  "https://${SITE}/api/devices/<edge_id>/<device_id>/commands" \
  -H 'Content-Type: application/json' --data '{"cmd":"sync","args":""}'
curl -fsS -H 'Authorization: Bearer <管理令牌>' "https://${SITE}/api/commands?limit=5"
```

第 8 步的判读：命令落库返回 `status:"sent"`；设备当前不可达时回执为
`status:"failed"`、`result:"device offline"`——这是**正确行为**，说明命令链路通、
只是设备侧串口没开成，不要把它当成反代故障。

浏览器验收：打开 `https://<你的域名>` → 登录 → 概览/设备/边缘节点能看到刚接入的 Edge 与设备；
拔掉其中一台电脑的 Edge，该 Edge 变 offline 而其它 Edge 不受影响；恢复后自动重连。

---

## 8. 持久化、备份、日志、保留期

**持久化**：全部状态在 `/var/lib/cloudpath/cloudpath.db`（WAL 模式，另有 `-wal`/`-shm`）。
schema 由 store 自动迁移（当前 `schema_version` 可从 `GET /api/stats` 读到）；换机器 = 搬这个目录。

**备份**（二选一）：

```bash
# A. 在线一致性快照（主机有 sqlite3 时优先）
sudo -u cloudpath sqlite3 /var/lib/cloudpath/cloudpath.db ".backup '/var/backups/cloudpath-$(date +%F).db'"

# B. 冷备（没有 sqlite3 时；有短暂中断）
sudo systemctl stop cloudpath-server
sudo cp -a /var/lib/cloudpath "/var/backups/cloudpath-$(date +%F)"
sudo systemctl start cloudpath-server
```

不要只复制 `.db` 而忽略 `-wal`：那会丢掉尚未 checkpoint 的写入。
恢复前先备份现有目录，恢复后启动一次并检查 `/healthz` 与 `/api/devices`。
建议用主机已有的定时任务机制每日一次，保留 7–14 份。

**日志**：走 journald（unit 里 `StandardOutput=journal`，应用输出 JSON 结构化日志）。

```bash
journalctl -u cloudpath-server -f
journalctl -u cloudpath-server --since "-1h" --no-pager | grep -iE 'level=(ERROR|WARN)'
journalctl --disk-usage            # 上限在 /etc/systemd/journald.conf 的 SystemMaxUse
```

日志不打印令牌明文；但仍含客户端 IP、edge/device ID，采集/外发时按内部数据处理。

**保留期**：`CLOUDPATH_RETENTION_DAYS`（默认 30 天），后台每小时清理超期事件与终态命令；
在途命令不清，设备元数据不删。磁盘吃紧就调小保留期，而不是删库。

**资源上限**：unit 里已设 `MemoryMax=1G`、`MemoryHigh=768M`、`TasksMax=256`、`LimitNOFILE=65536`。
设备/Edge 规模上来后按实际 RSS 调整，别用无上限配置。

---

## 9. 升级

```bash
NEW_VERSION=v0.1.1     # 目标版本

# 1) 本机构建或下载新版本，并完成 §1 架构断言（不通过就不要往下走）
task build:linux-arm64

# 2) 备份数据库（§8）

# 3) 传输 + 主机侧复核架构（§2）
scp "bin/cloudpath-server_linux_arm64" "$HOST:/tmp/cloudpath-server.new"
ssh "$HOST" 'python3 /tmp/assert_arch.py --expect-os linux --expect-arch arm64 --expect-tags embed_ui /tmp/cloudpath-server.new'

# 4) 保留当前版本用于回滚，再替换并重启
ssh "$HOST" 'sudo cp -a /opt/cloudpath/bin/cloudpath-server /opt/cloudpath/bin/cloudpath-server.prev'
ssh "$HOST" 'sudo install -o root -g root -m 0755 /tmp/cloudpath-server.new /opt/cloudpath/bin/cloudpath-server'
ssh "$HOST" 'sudo systemctl restart cloudpath-server'

# 5) 验收
ssh "$HOST" 'sleep 3; curl -fsS http://127.0.0.1:8080/healthz'
curl -fsS "https://${SITE}/healthz"          # version 应为新版本
curl -fsS -H 'Authorization: Bearer <管理令牌>' "https://${SITE}/api/edges"
```

Edge 客户端与 Server 尽量同版本；`/api/edges` 会显示每个 Edge 上报的版本号。
升级后 Edge 会靠自身的指数退避自动重连，不需要重启客户端。
优雅关闭：服务收 `SIGTERM` 后先断 WS 长连接再关 HTTP（`TimeoutStopSec=20`）。

## 10. 回滚

回滚 = 换回上一个二进制（必要时连同升级前的数据库备份一起还原）。分步执行，每步确认后再走下一步。

1. 停服（此刻站点短暂不可用，Edge 会自动退避重连）：

```bash
ssh "$HOST" 'sudo systemctl stop cloudpath-server'
```

2. 换回保留的上一版二进制：

```bash
ssh "$HOST" 'sudo cp -a /opt/cloudpath/bin/cloudpath-server.prev /opt/cloudpath/bin/cloudpath-server'
```

3. 若新版本改过 schema（`GET /api/stats` 的 `schema_version` 变了），用 §8 的升级前备份还原数据目录；只降二进制不还原数据库有不可读风险。

4. 起服：

```bash
ssh "$HOST" 'sudo systemctl start cloudpath-server'
```

5. 验收（版本号应为旧版本，Edge 应自动重连回来）：

```bash
ssh "$HOST" 'curl -fsS http://127.0.0.1:8080/healthz'
curl -fsS "https://${SITE}/healthz"
```

**永远保留升级前那一份数据库备份**：降级二进制容易，降级 schema 不行。

---

## 11. 故障排查

| 现象 | 排查 |
|---|---|
| `Exec format error` | 架构不匹配。回到 §1/§2 断言；不要装模拟器绕过 |
| 服务起不来，journal 有权限/seccomp 错 | 核对 unit 加固项与 `ReadWritePaths=/var/lib/cloudpath`；数据目录属主必须是 `cloudpath` |
| 回环 `/healthz` 通、公网 502 | nginx `proxy_pass` 端口与 `CLOUDPATH_ADDR` 不一致；或主机安全模块（SELinux/AppArmor）拦了 nginx 的出站连接 |
| 页面能开但实时不刷新 | WSS 被掐：确认 `/ws`、`/ws/edge` 的 `Upgrade`/`Connection` 头与长 `proxy_read_timeout`；CDN 侧也要允许 WebSocket |
| 浏览器 WS 401 / 连不上 | `CLOUDPATH_ALLOWED_ORIGINS` 与实际访问 host:port 不一致；反代必须透传 `Host` |
| 审计里 `remote_ip` 全是 127.0.0.1 | 应用侧 `CLOUDPATH_TRUSTED_PROXIES` 未包含 nginx 来源，或 nginx 未设 `X-Forwarded-For` |
| Edge 连不上 | 令牌错/已吊销；`edge.yaml` 的 `server` 应为 `wss://<域名>/ws/edge`；`edge_id` 只允许字母数字 `-_`（1–64） |
| Edge online 但设备一直 offline | 串口打不开（Edge 日志 `device open failed` + 1/2/4/8s 指数退避）。设备侧问题，不是链路问题 |
| 命令回执 `failed / device offline` | 目标设备当前离线；命令链路本身正常 |
| 写操作 403 | 令牌 scope 不足（`edge` 令牌不能读 REST）；改用管理令牌或浏览器会话 |
| 命令返回 429 | 命中 `-cmd-rate`（默认 20 次/分/设备），响应带 `Retry-After` |
| 登录返回 429 | 命中 `-login-rate`（默认 5 次/分/IP）；确认 `CLOUDPATH_TRUSTED_PROXIES` 正确，否则所有客户端共享代理 IP 的配额 |
| 数据库写锁 / 磁盘满 | 查磁盘余量；调小保留期；确认 `-wal` 没有异常膨胀；备份按 §8 |

---

## 12. 上线检查清单

- [ ] §1 架构断言 PASS（本机 + 主机侧各一次），含 `embed_ui`
- [ ] `sha256sum -c checksums.txt` 通过（使用 Release 资产时）
- [ ] 专用系统账号 + 三个目录权限正确（`0755`/`0750`/`0600`）
- [ ] env 文件 `root:root 0600`；只核键名不回显值；机密不进 shell 历史与仓库
- [ ] `CLOUDPATH_ADDR` 为回环；`ss -ltnp` 显示应用端口未在公网监听
- [ ] **先完成首装账号，再放开 nginx 站点**（§5 → §6 顺序不可颠倒）
- [ ] `CLOUDPATH_ALLOWED_ORIGINS`、`CLOUDPATH_REQUIRE_AUTH=true`、`CLOUDPATH_TRUSTED_PROXIES` 已设
- [ ] `nginx -t` 通过并 reload；HTTP→HTTPS 301；HSTS 存在；CSP 由应用返回且未被反代覆盖
- [ ] `/healthz` 回环 + 公网均 200；`/ws` 未带凭据 401、带令牌 101
- [ ] 至少一台真实 Edge 从另一台电脑接入并 online；命令回执可见
- [ ] 备份任务已配置；`journalctl` 可查；保留期已设
- [ ] 升级/回滚路径演练过一次（保留 `.prev` 与升级前数据库备份）
- [ ] 客户端分发说明已发给使用者：[deploy/edge/README.md](edge/README.md)

---

## 13. 相关文件

| 文件 | 用途 |
|---|---|
| [systemd/cloudpath-server.service](systemd/cloudpath-server.service) | systemd 单元（加固 + 资源上限 + 持久化路径） |
| [systemd/cloudpath-server.env.example](systemd/cloudpath-server.env.example) | 环境变量模板（只有变量名，按 0600 安装） |
| [nginx/cloudpath.vectorcontrol.tech.conf](nginx/cloudpath.vectorcontrol.tech.conf) | 可直接安装的公网站点示例（HTTPS + WSS + CDN 真实 IP）；域名与证书路径需替换 |
| [nginx.conf](nginx.conf) | 与主机无关的通用反代模板（`console.example.com`） |
| [edge/README.md](edge/README.md) | 客户端（使用者自己电脑）分发与 `edge.yaml` 填写指引 |
| [config.example.env](config.example.env) | 容器/compose 形态的环境变量示例 |
| [docker-compose.yml](docker-compose.yml) | 容器形态 server（本文不使用；注意宿主架构必须与镜像架构一致） |
| [docker-compose.edge.yml](docker-compose.edge.yml) | 容器形态 edge overlay（串口需额外设备映射） |
| [../scripts/assert_arch.py](../scripts/assert_arch.py) | 产物架构断言门禁（硬失败） |
| [../scripts/build_matrix.py](../scripts/build_matrix.py) | 全平台构建矩阵 + checksums + `--verify-only` |
| [../Taskfile.yml](../Taskfile.yml) | `task build:linux-arm64` / `build:matrix` / `verify:arch` / `release:artifacts` |
| [../edge.example.yaml](../edge.example.yaml) | Edge 配置母版（复制为 `edge.yaml` 后填写，不入库） |
| [../CHANGELOG.md](../CHANGELOG.md) | 版本记录与产物命名规范 |
| [split/README.md](split/README.md) | 参考 Application 插件拆成独立仓库的可复现脚本 |
