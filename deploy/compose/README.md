# CloudPath 容器化部署（docker compose）

容器形态参考：`server`（含 WebUI）+ 可选 `nginx` TLS 反代，数据在命名卷 `/data`。
全量部署指南见 [`../../docs/deploy.md`](../../docs/deploy.md)，安全基线见
[`../../docs/security.md`](../../docs/security.md)（本文只给能直接跑的命令，不复述设计）。

## 文件清单

| 文件 | 用途 |
|---|---|
| `docker-compose.yml` | L0 本机：只绑 `127.0.0.1:8080` 的 server |
| `docker-compose.public.yml` | L2 公网：server + nginx TLS/WSS 反代（80/443） |
| `.env.example` | 环境变量模板（复制为 `.env`；真实值不进 git） |
| `nginx/cloudpath-site.conf.template` | nginx 站点模板（`PUBLIC_SERVER_NAME` 用 envsubst 注入） |
| `certs/` | 放 `fullchain.pem` / `privkey.pem`（gitignore） |

## 配置

```bash
cp .env.example .env
# 至少改：CLOUDPATH_TOKEN / CLOUDPATH_SETUP_TOKEN（随机值）、
#         CLOUDPATH_ALLOWED_ORIGINS（= 访问域名）、PUBLIC_SERVER_NAME（公网）
python -c "import secrets; print(secrets.token_hex(32))"   # 生成随机令牌
```

## L0 本机

```bash
docker compose -f docker-compose.yml up -d --build
curl -fsS http://127.0.0.1:8080/healthz
```

## Application 插件（可选）

在 `.env` 中设 `CLOUDPATH_APP_HOST=true`，重建 server 后启用应用宿主。
镜像和两个 Compose 模板均把插件安装目录（`CLOUDPATH_APP_PLUGINS_DIR`）、
lockfile（`CLOUDPATH_APP_LOCK`）与宿主状态（`CLOUDPATH_APP_STATE_DIR`）默认放在
持久卷 `/data` 下；配置变量见 `.env.example`。现有插件安装目录与 lockfile
须一同迁入该卷，插件可执行文件必须匹配容器的 OS/arch，并对 uid 65532 可读/可执行。

不要沿用原生服务的相对 `data/...` 路径：容器工作目录是 `/app`，根文件系统
只读；进程健康不代表插件安装目录已找到。除 `/healthz` 外，还应检查应用实例
的 `/records`、`/bindings`、`/jobs`，确认实际运行状态与绑定。

## L2 公网（重点）

```bash
# 1) 证书（nginx 缺 cert 起不来）
mkdir -p certs   # 放 fullchain.pem / privkey.pem（私钥只留主机，勿提交；本地演示可自签）
# 2) 起栈
docker compose -f docker-compose.public.yml up -d --build
# 3) 首装管理员 —— 公网用一次性 setup token（先首装再放开公网）
curl -fsS -X POST "https://<域名>/api/auth/setup" \
  -H 'Content-Type: application/json' \
  -H "X-Cloudpath-Setup-Token: $(grep CLOUDPATH_SETUP_TOKEN .env | cut -d= -f2)" \
  --data '{"username":"admin","password":"<强密码>"}'
# 浏览器打开 https://<域名>
```

## ⚠️ 容器首装走回环或带 token

Docker 端口映射会让 server 进程看到的源 IP 变成网关（非回环），所以**从宿主直接
`POST /api/auth/setup` 会 403**。用下面之一：

- L0 容器内回环：`docker compose -f docker-compose.yml exec -T server wget -qO- --post-data='{"username":"admin","password":"<强密码>"}' --header='Content-Type: application/json' http://127.0.0.1:8080/api/auth/setup`
- 或 `.env` 设 `CLOUDPATH_SETUP_TOKEN` 后，从宿主带 `X-Cloudpath-Setup-Token` 头首装（见 L2）。

## 拉发布镜像（可选）

`.env` 设 `CLOUDPATH_IMAGE=ghcr.io/<owner>/cloudpath:<tag>` 后 `docker compose ... up -d`
（不带 `--build`）即优先 pull，无需现场构建。镜像由 `.github/workflows/container.yml` 发布。

## 验证

```bash
curl -fsS https://<域名>/healthz                 # 200
curl -s -o /dev/null -w '%{http_code}' https://<域名>/api/devices       # 401（未鉴权）
curl -s -o /dev/null -w '%{http_code}' -X POST https://<域名>/api/auth/setup   -H 'Content-Type: application/json' -d '{}'                          # 403（未授权首装）
# 未带会话的 /ws：401
```

## 停止 / 备份

```bash
docker compose -f docker-compose.public.yml down      # 停（数据保留）
docker compose -f docker-compose.public.yml down -v   # 停+清卷（慎用）
# 备份：docker run --rm -v cloudpath-public_cloudpath-data:/data -v $(pwd):/backup alpine tar czf /backup/cp-$(date +%F).tgz -C /data .
```

参考：安全模型 [`../../docs/security.md`](../../docs/security.md) · 全量部署
[`../../docs/deploy.md`](../../docs/deploy.md) · 原生 systemd 版
[`../../deploy/README.md`](../../deploy/README.md) · CI/CD
[`../../.github/workflows/`](../../.github/workflows/)
