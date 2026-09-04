# CloudPath 容器化部署（docker compose）

> 服务端 SSOT：`../../docs/design.md`；安全基线：`../../docs/security.md`；
> 原生二进制 + systemd 版 SOP：`../README.md`。本目录是 **compose 交付形态**，
> 一套命令把「控制面 + TLS 反代」跑起来，供演示与交付。edge（接真实串口的那台电脑）
> 仍建议在宿主机直跑，见 `../../edge.example.yaml`。

## 两种形态

| 文件 | 形态 | 暴露 | 用于 |
|---|---|---|---|
| `docker-compose.yml` | **L0 本机** | 仅 `127.0.0.1:8080` | 本机自用 / 开发 |
| `docker-compose.public.yml` | **L2 公网** | nginx `:80/:443`（TLS+WSS） | 公网演示 / 交付 |

两套都用同一个镜像（`../Dockerfile`，内嵌 WebUI），数据都在命名卷 `cloudpath-data:/data`，
**重建容器不丢库**——这正是你之前「初始化又弹出来」的根因：**库落到了不可持久的地方或换了个库**，
这套设计把库固定到卷上。

## 第一步：配置

```bash
cd deploy/compose
cp .env.example .env
# 编辑 .env，至少改：
#   CLOUDPATH_TOKEN / CLOUDPATH_SETUP_TOKEN（用随机值）
#   CLOUDPATH_ALLOWED_ORIGINS（= 你访问的域名）
#   PUBLIC_SERVER_NAME（公网形态）
```

生成随机令牌：
```bash
python -c "import secrets; print(secrets.token_hex(32))"
```

## L0 本机启动

```bash
docker compose -f docker-compose.yml up -d --build
docker compose -f docker-compose.yml ps
curl -fsS http://127.0.0.1:8080/healthz
# 首装管理员（本机回环）
curl -fsS -X POST http://127.0.0.1:8080/api/auth/setup \
  -H 'Content-Type: application/json' \
  --data '{"username":"admin","password":"<强密码>"}'
# 浏览器打开 http://127.0.0.1:8080
```

## L2 公网启动（重点）

```bash
# 1) 放证书（必须，否则 nginx 起不来）
mkdir -p certs
#    把 fullchain.pem / privkey.pem 放进 certs/（私钥只留在主机，切勿提交）
#    仅本地演示可自签：
#      openssl req -x509 -newkey rsa:2048 -nodes -days 365 \
#        -keyout certs/privkey.pem -out certs/fullchain.pem \
#        -subj "/CN=$(grep PUBLIC_SERVER_NAME .env | cut -d= -f2)"

# 2) 起栈（server 先健康，再起 nginx）
docker compose -f docker-compose.public.yml up -d --build
docker compose -f docker-compose.public.yml ps

# 3) 首装管理员 —— 用一次性 setup token（公网也能建）
#    从 .env 读取 CLOUDPATH_SETUP_TOKEN
curl -fsS -X POST "https://$(grep PUBLIC_SERVER_NAME .env | cut -d= -f2)/api/auth/setup" \
  -H 'Content-Type: application/json' \
  -H "X-Cloudpath-Setup-Token: $(grep CLOUDPATH_SETUP_TOKEN .env | cut -d= -f2)" \
  --data '{"username":"admin","password":"<强密码>"}'

# 4) 浏览器打开 https://<PUBLIC_SERVER_NAME>
```

> **顺序不可颠倒**：`REQUIRE_AUTH=true` 保证空库也鉴权 + `SETUP_TOKEN` 保证只有持令牌者能首装，
> 所以公网形态下没有「谁先到谁是管理员」的风险。首装成功即进入账号模式，setup token 作废。

## 关键安全项（这套 compose 已编码）

- server **不发布宿主端口**，只走栈内 network（`expose`）；唯一公网面 = nginx `:443`。
- `CLOUDPATH_REQUIRE_AUTH=true`（公网）——无用户也强制 `/api/*`、`/ws` 鉴权。
- `CLOUDPATH_TRUSTED_PROXIES=172.28.0.10/32`——**只**信 nginx 这台容器的 `X-Forwarded-*`，
  登录限流/审计拿到真实客户端 IP，且防伪造 XFF。
- `CLOUDPATH_ALLOWED_ORIGINS`——浏览器 WS 来源白名单（精确 host）。
- 非 root（镜像内 `USER cloudpath`）+ `cap_drop: ALL` + `no-new-privileges` + `read_only` 根文件系统。
- 持久卷 `cloudpath-data:/data`；`restart: unless-stopped`；显式健康检查。
- nginx 只做 TLS 终止 + WSS 透传，不复写 CSP/安全头（应用自带，覆盖会坏控制台）。

## 验证清单

```bash
# 公网面
curl -fsS https://<PUBLIC_SERVER_NAME>/healthz                      # 200
curl -s  -o /dev/null -w '%{http_code}\n' https://<PUBLIC_SERVER_NAME>/api/devices   # 401（未鉴权）
# 未授权 setup 必须被拒
curl -s  -o /dev/null -w '%{http_code}\n' -X POST https://<PUBLIC_SERVER_NAME>/api/auth/setup -H 'Content-Type: application/json' -d '{}'   # 403
# 登录 + 会话
curl -s -o /dev/null -w '%{http_code}\n' -X POST https://<PUBLIC_SERVER_NAME>/api/auth/login -H 'Content-Type: application/json' -d '{"username":"admin","password":"<强密码>"}'   # 200
```

## 停止 / 备份

```bash
docker compose -f docker-compose.public.yml down          # 停（数据保留在卷）
docker compose -f docker-compose.public.yml down -v       # 停并删卷（清库！慎用）
# 备份数据库
docker compose -f docker-compose.public.yml exec server sh -c 'ls -lh /data'
# 在宿主机备份卷：docker run --rm -v cloudpath-public_cloudpath-data:/data -v $(pwd):/backup \
#   alpine tar czf /backup/cloudpath-$(date +%F).tgz -C /data .
```

## CI/CD 与发布镜像（与仓库 workflows 联动）

仓库的 GitHub Actions 在 `v*` tag 上一次跑完（全部免费：public 仓库标准 runner + GHCR 免费）：

- `.github/workflows/ci.yml`：`webui` 构建一次 → `matrix-build` 6 平台并行（linux/amd64+arm64、windows/amd64+arm64、darwin/amd64+arm64）交叉编译并断言 → `matrix-verify` 合并校验 `checksums.txt`。每个 PR / main push 都全平台验证。
- `.github/workflows/release.yml`：打 `v*` tag → `webui` 一次 → 6 平台并行构建 18 个二进制 → 合并校验 → 发布 GitHub Release（含 `checksums.txt`）。
- `.github/workflows/container.yml`：main 推 `:latest`，tag 推 `:<tag>` + `:latest`；`buildx` 一次构建 `linux/amd64 + linux/arm64` 双 arch 镜像推到 GHCR（含 server + edge，WebUI 已内嵌）。

**compose 拉发布镜像**（而不是每次现场构建）：

```bash
# deploy/compose/.env 里设
CLOUDPATH_IMAGE=ghcr.io/<owner>/cloudpath:<tag>   # 例如 ghcr.io/deliciousbuding/cloudpath:v0.1.0
# 然后去掉 compose 的 build: 块，或直接：
docker compose -f docker-compose.public.yml up -d   # 不带 --build，优先 pull
```

公开仓库可匿名 pull，无需登录。本地演示仍可用默认 `cloudpath:local` 现场 `--build`。

## 文件清单

| 文件 | 用途 |
|---|---|
| `docker-compose.yml` | L0 本机 server（硬化的单机形态） |
| `docker-compose.public.yml` | L2 公网 server + nginx TLS 反代 |
| `.env.example` | 环境变量模板（真实值不进 git） |
| `nginx/cloudpath-site.conf.template` | nginx 站点模板（仅 `PUBLIC_SERVER_NAME` 用 envsubst 注入） |
| `certs/` | 放 `fullchain.pem` / `privkey.pem`（gitignore，不进库） |

