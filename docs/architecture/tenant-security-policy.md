# Tenant Security Policy, Retention, Quotas, and Plugin Secrets

最后更新：2026-09-03

> 状态：**v0.1 目标契约，尚未全部实现**。本文只定义防滥用与最小秘密边界，不引入计费系统或中心密钥库。

## 1. 设计目标

CloudPath 的租户隔离不仅是“查询时带 tenant id”，还必须保证单个租户不能耗尽全局内存、SQLite、WebSocket 或审计容量。最低安全闭环包括：

1. 明文插件 secret 不跨 Server/WS/SQLite 边界；
2. 每租户数据按各自保留期清理，不误删其他租户；
3. 高成本资源有硬配额，达到上限时原子拒绝；
4. 拒绝行为有稳定机器错误码和节流后的安全审计；
5. 默认值可直接安全运行，未配置不等于无限制。

## 2. Plugin secret handle

### 2.1 引用与声明

插件 manifest 的 `permissions.secrets` 只允许稳定的 secret **名称**，例如 `api_token`，不得写值、路径、URL 或模板表达式。

实例配置通过 `secret://<name>` 引用。handle 只表达“该实例需要一个名为 `<name>` 的本地秘密”，不携带明文，也不指向用户提供的任意文件路径。

### 2.2 明文边界

v0.1 不提供中心 `/api/secrets`，Server 不存储、不解析、不转发明文。Edge 本地 secret provider 是唯一明文来源：

```text
<secret-root>/<tenant>/<instance>/<name>
```

文件实现必须：

- tenant / instance / name 都经过安全 segment 校验，禁止路径逃逸；
- 文件权限为 owner-only（Unix 0600；Windows 使用当前用户 ACL 的等价保护）；
- 不接受符号链接或重解析点逃逸；
- 读取大小有界；
- 只在插件启动/重启时读取，注入该实例进程后立即释放临时缓冲。

### 2.3 双重授权

解析 handle 必须同时满足：

1. manifest 的 `permissions.secrets` 声明该名称；
2. 实例配置显式绑定该 handle。

任一条件不满足、文件缺失、权限不安全或已删除时，实例启动/reconcile fail-closed。不得从日志、旧环境变量、上一次进程或 desired cache 回落旧值。

### 2.4 日志与审计

允许记录：plugin id、instance id、secret name、结果码。禁止记录：值、字节长度之外的可识别摘要、绝对路径、环境变量完整内容。

公共 DTO、audit metadata 和 Catalog 只出现 handle/name。日志脱敏必须覆盖大小写变体、URL query、header、JSON key 和 env 形式。

### 2.5 轮换与吊销

- 轮换：原子替换 Edge secret 文件并重启/reconcile 实例；新进程只读取新值。
- 吊销：删除本地 secret 后重启/reconcile 必须失败；旧进程需由管理员停止或触发 reconcile。
- v0.1 不做 secret 版本历史、远程分发或自动轮换；这些属于 v0.2。

## 3. Tenant retention

### 3.1 默认策略

| 资源 | 默认 | 范围/规则 |
|---|---:|---|
| events | 30 天 | 1–3650 天 |
| terminal commands | 30 天 | 1–3650 天 |
| audit events | 90 天 | 7–3650 天 |
| sessions | 到 `expires_at` | 过期即删 |
| revoked/expired tenant tokens | 7 天 | 只保留元数据，不保留明文 |
| current device state | 常驻 | 设备删除时清理 |
| plugin observed projection | 30 天 | desired 不随 observed 清理 |

每个字段可为 NULL，表示继承 Server 默认值；不能用 0 表示无限。需要无限保留时必须在未来版本设计独立权限与存储预算，v0.1 不提供。

### 3.2 Sweeper

Sweeper 仍运行在 `cloudpath-server` 内部：

- 每次按 tenant 分批、带 tenant predicate 删除；
- 使用短事务和上限，避免一次清理长期占写锁；
- 清理失败不影响 HTTP/WS 服务，但记录不含数据内容的错误；
- Server 重启后可重复执行，结果幂等；
- sessions 清理必须实际接入 sweeper，而不是只有未调用的 store 方法。

### 3.3 管理权限

只有 admin 可修改 retention；viewer/operator 只读。修改产生 `tenant.retention.updated` audit event，记录旧/新天数，不记录任何业务数据。

## 4. Tenant quotas

v0.1 quota 只用于防滥用，不用于计费。

| 资源 | 默认硬上限 | 拒绝点 |
|---|---:|---|
| devices | 200 | 注册/首次持久化事务内 |
| online edges | 50 | Edge hello 前 |
| browser WebSocket | 20 | 浏览器 WS accept 前 |
| active tenant tokens | 100 | token 创建事务内 |
| users | 100 | user 创建事务内 |
| events per minute | 600 | Edge event 写入前 |
| plugin instances | 100 | desired instance 创建事务内 |

### 4.1 原子性

计数与写入必须在同一数据库写事务或同一内存锁内完成。禁止“先 COUNT、解锁、再 INSERT”的竞态。并发两个刚好到上限的请求只能有一个成功。

### 4.2 错误语义

- REST：HTTP 429，响应包含稳定 `code`，例如 `quota_devices`、`quota_tokens`。
- Edge hello：WebSocket policy violation，日志/审计只记录 tenant、resource、limit、usage。
- event rate：丢弃/拒绝该事件并保持连接；持续超限可在有界阈值后关闭 Edge。
- quota 拒绝不得增加 desired revision、不得留下部分行。

### 4.3 审计节流

`quota.exceeded` 每个 tenant/resource 最多每分钟写一条，防止攻击者用拒绝事件打爆 audit 表。被节流只影响重复 audit，不影响每次请求的实际拒绝。

## 5. 数据模型

租户策略建议单表 `tenant_settings`：

- `tenant_id` 唯一外键；
- retention 字段；
- quota 字段；
- `updated_at`；
- 所有字段有 DB `CHECK`；NULL 表示继承默认。

Plugin control plane 的 v7 schema 与 tenant policy schema 必须由同一个 Store lane 统一编号和迁移顺序，禁止两个并行 lane 都声明“schema v7”。

## 6. 不变量

1. 配额/保留期查询必须显式 tenant-scoped；缺 tenant principal 时不得进入多租户写路径。
2. quota 拒绝不产生业务写入或 revision 变化。
3. 清理 Tenant A 不得读取、计数或删除 Tenant B。
4. Edge/浏览器断开必须释放在线连接配额。
5. secret name 不等于 secret value；把 handle 列入 Catalog 是安全的，把解析值列入 Catalog 是漏洞。
6. audit 失败不能把已拒绝请求变成成功，也不能泄露请求中的敏感内容。

## 7. 最小测试矩阵

- `TestSecretHandleRejectsTraversal`
- `TestManifestSecretsAreNamesOnly`
- `TestUndeclaredSecretFailsClosed`
- `TestPluginEnvironmentCarriesOnlyBoundSecrets`
- `TestSecretValueNeverAppearsInLogsOrCatalog`
- `TestPruneByTenantKeepsOtherTenants`
- `TestAuditRetentionCutoff`
- `TestSweeperPrunesExpiredSessions`
- `TestQuotaRejectsAtLimit`
- `TestQuotaConcurrentAtomic`
- `TestQuotaRejectDoesNotAdvanceRevision`
- `TestQuotaAuditIsRateLimited`
- `TestEdgeAndBrowserDisconnectReleaseQuota`

反向验证至少包括：移除 tenant predicate 会误删并令测试失败；移除原子 admit 会在并发测试中超过上限；把 secret 值放入 DTO/log 会触发泄漏测试。

## 8. v0.1 与后续边界

v0.1 必须实现：本地 secret provider + 双重授权、per-tenant retention、devices/edges/browser WS/tokens/users/events/plugin instances 硬配额、稳定错误码和审计。

v0.2 可实现：中心 KMS/Vault、远程 secret 分发、secret 版本历史、自动轮换、计费配额、分布式 limiter、多 Server 全局配额。