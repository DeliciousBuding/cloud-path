# Cloudpath 部署指南

最后更新：2026-09-03

> 本文面向部署者，覆盖本地、Docker、edge 容器与反向代理。安全基线见
> [security.md](security.md)，HTTP 契约见 [api.md](api.md)，源码入口见
> [README](../README.md)。

## 1. 前置条件

- Go 1.26+、Node 20+、pnpm 9（本地开发/构建）
- Docker 24+ / Docker Compose v2（容器部署）
- 反向代理示例：nginx 1.25+（TLS）
- 真机 edge：Linux 串口设备 `/dev/ttyUSB0` 等；Windows 建议直接在宿主机跑
  `cloudpath-edge`（容器访问 `COM3` 需要额外的串口映射）

## 2. 本地构建与运行

```bash
git clone https://github.com/DeliciousBuding/cloud-path.git
cd cloud-path

go mod download
cd webui && pnpm install --frozen-lockfile && cd ..
go build -tags embed_ui -trimpath -o bin/cloudpath-server ./cmd/cloudpath-server
go build -trimpath -o bin/cloudpath-edge ./cmd/cloudpath-edge
```

生产模式本地运行 server（默认只绑 `127.0.0.1:8080`，前端已内嵌）：

```bash
CLOUDPATH_TOKEN="$(openssl rand -hex 32)" \
CLOUDPATH_ALLOWED_ORIGINS="console.example.com" \
./bin/cloudpath-server \
  -log-level info -log-format json
```

Windows PowerShell 示例：

```powershell
$env:CLOUDPATH_TOKEN = python -c "import secrets; print(secrets.token_hex(32))"
$env:CLOUDPATH_ALLOWED_ORIGINS = "console.example.com"
.\bin\cloudpath-server.exe -log-level info -log-format json
```

验证：

```bash
curl -fsS http://127.0.0.1:8080/healthz
```

> server 默认监听回环地址。若要放在宿主机反代后面，把监听地址改为
> `0.0.0.0:8080`（容器镜像已默认如此），并让反代只从本机或内网访问。

## 3. Docker 构建与运行

Dockerfile 是三阶段构建：`webui` 用 `pnpm build` 产出 `webui/dist`，
`builder` 以 `-tags embed_ui` 编译 server、再编译 edge，最终运行镜像使用
Alpine + 非 root 用户，默认 `CLOUDPATH_ADDR=0.0.0.0:8080`、
`CLOUDPATH_DB=/data/cloudpath.db`。

```bash
docker build -t cloudpath:local .
docker run --rm --name cloudpath-server \
  -p 127.0.0.1:8080:8080 \
  -e CLOUDPATH_TOKEN="$CLOUDPATH_TOKEN" \
  -e CLOUDPATH_ALLOWED_ORIGINS="console.example.com" \
  -v cloudpath-data:/data \
  cloudpath:local
```

用 compose（推荐；容器形态参考 `deploy/compose/`）：

```bash
cd deploy/compose
cp .env.example .env
# 编辑 .env，至少替换 CLOUDPATH_TOKEN / CLOUDPATH_SETUP_TOKEN / CLOUDPATH_ALLOWED_ORIGINS
# L0 本机（只绑 127.0.0.1:8080）：
docker compose -f docker-compose.yml up -d --build
# L2 公网（server + nginx TLS/WSS 反代，见 deploy/compose/README.md）：
docker compose -f docker-compose.public.yml up -d --build
curl -fsS http://127.0.0.1:8080/healthz
```

`deploy/compose/` 提供两套形态：`docker-compose.yml`（L0 本机）与
`docker-compose.public.yml`（L2 公网，含 nginx TLS）。`CLOUDPATH_BIND` 默认
`127.0.0.1:8080`（只暴露本机），公网不要改成 `0.0.0.0:8080` 直出；TLS 交给反代。

> ⚠️ **容器首装**：Docker 端口映射后 server 进程看到的源 IP 是网关（非回环），所以从宿主
> `POST /api/auth/setup` 会 403。应在 server 容器内走回环，或设 `CLOUDPATH_SETUP_TOKEN`
> 后带 `X-Cloudpath-Setup-Token` 头首装（详见 `deploy/compose/README.md`）。

## 4. edge 容器（可选）

镜像同时包含 `cloudpath-edge`。Linux 上可用 compose overlay 启动：

```bash
cd deploy
# 按实际设备修改 deploy/edge.example.yaml，例如端口/边 ID/设备名
docker compose -f docker-compose.yml -f docker-compose.edge.yml up -d --build
docker compose -f docker-compose.yml -f docker-compose.edge.yml ps
```

要点：

- `deploy/edge.example.yaml` 使用容器网络主机名 `ws://server:8080/ws/edge`；
  宿主机直跑时改成 `ws://127.0.0.1:8080/ws/edge`。
- `token: ${CLOUDPATH_TOKEN}` 由 edge 配置加载器展开，容器环境变量
  `CLOUDPATH_TOKEN` 与 server 保持一致。
- Linux 真实串口需要在 `deploy/docker-compose.edge.yml` 中取消注释并填写
  `devices` 映射；Windows 建议在宿主机直接运行
  `.\bin\cloudpath-edge.exe -config edge.yaml`。
- 启动后再查 `/healthz` 与 `/api/edges`，确认 edge 注册成功。

## 5. 反向代理部署（TLS）

推荐拓扑：

```text
Internet/TLS
   |
nginx (443, 80 -> 301)
   |
cloudpath-server (127.0.0.1:8080 或容器内网)
   |
SQLite 卷 /data
```

示例配置见 [nginx 配置](../deploy/nginx.conf)。最小步骤：

```bash
sudo cp deploy/nginx.conf /etc/nginx/sites-available/cloudpath
# 修改 console.example.com 与证书路径
sudo nginx -t
sudo systemctl reload nginx
```

同时必须给 server 设置：

```text
CLOUDPATH_ADDR=0.0.0.0:8080        # 容器/内网监听；宿主机反代可保持 127.0.0.1
CLOUDPATH_TOKEN=<强随机令牌>
CLOUDPATH_ALLOWED_ORIGINS=console.example.com
```

反向代理注意事项：

- WebSocket 必须转发 `Upgrade` / `Connection`；上方示例已处理。
- `CLOUDPATH_ALLOWED_ORIGINS` 填浏览器访问的 host（如 `console.example.com`），
  不要填 `http://` 前缀；如端口不是 443，需带端口。
- 不要在 NAT 上把 8080 暴露到公网；server 只应被反代在本机/内网访问。
- `X-Forwarded-Proto` 用于未来的安全 cookie/TLS 感知；当前服务并未依赖它判断
  鉴权，不能据此放宽边界。

## 6. 健康检查、日志与数据

- `GET /healthz`：`ok`、版本、uptime、在线设备/edge 数。
- `GET /api/stats`：设备/事件/命令计数、保留期、schema 版本（见 [api.md](api.md)）。
- 生产建议 `CLOUDPATH_LOG_FORMAT=json`，采集 `stdout/stderr`。
- SQLite 数据在 `/data/cloudpath.db`（容器卷 `cloudpath-data`）。备份：
  - 停服后复制数据库；
  - 运行中复制需同时复制 `.db`、`.db-wal`、`.db-shm`，并确保在一致性快照下操作；
  - 恢复前先备份旧库，恢复后启动一次并检查 `/healthz` 与 `/api/devices`。
- `-retention-days` 控制事件/命令保留（默认 30 天），不删设备元数据。
- 升级前先 `docker compose ... down` 或发送 `SIGTERM`，容器会走优雅关闭。

## 7. 升级与回滚

```bash
docker build -t cloudpath:local .
docker compose -f docker-compose.yml down
docker compose -f docker-compose.yml up -d
```

- 保留同一数据卷可保留设备/事件/命令；schema 由 store 自动迁移。
- 回滚：关闭新容器，用旧镜像/旧二进制启动；先备份数据库，避免降级后
  无法读取较新 schema。
- 后端与 edge 尽量同版本；`/api/edges` 会显示 edge 版本。

## 8. 常见故障

| 现象 | 排查 |
|---|---|
| 容器健康检查失败 | 看 `docker compose logs server`；确认 `CLOUDPATH_ADDR=0.0.0.0:8080` |
| WS 握手被拒 | 检查 `CLOUDPATH_ALLOWED_ORIGINS` 是否匹配浏览器 Origin host/port |
| edge 连接失败 | 对比 `CLOUDPATH_TOKEN`；`edge.yaml` 的 `server` 地址是否可路由 |
| 串口打不开 | 检查宿主机设备权限与容器 `devices` 映射；Windows 优先宿主机进程 |
| 写命令 401 | 使用 `Authorization: Bearer <token>`，或检查 server 是否带 token |
| 数据库写锁 | 确认 `/data` 卷可写、磁盘不超；sqlite busy_timeout 已配置，仍报错时缩短保留期或检查 IO |

## 9. 部署清单

- [ ] 生成并只通过环境变量注入强随机 `CLOUDPATH_TOKEN`
- [ ] `CLOUDPATH_ALLOWED_ORIGINS` 显式配置
- [ ] 公网启用 TLS，只暴露 443
- [ ] `docker compose up -d --build` 后 `curl /healthz`
- [ ] 启动 edge 后检查 `/api/edges`
- [ ] 配置备份与告警（日志、健康检查）
- [ ] 阅读 [security.md](security.md) 后再上线
