# Plugin Control Plane Synchronization

最后更新：2026-09-03

> 状态：**目标契约，尚未全部实现**。本文定义 CloudPath Server 与 Edge 之间的插件期望态、实际态、离线行为和审计边界。实现状态以测试和 `docs/design.md` 为准。

## 1. 问题与第一性结论

插件系统需要同时回答四个问题：

1. 租户希望哪个 Edge 运行哪些插件实例？
2. Edge 实际安装了什么、运行到了什么状态？
3. Edge 离线时是否还能继续运行最后一次确认的配置？
4. 谁修改了配置、何时修改、Edge 是否成功应用？

这些事实不能由同一个无边界的文件目录同时承担。CloudPath 采用以下权威划分：

- **Server 是期望态和审计的唯一权威源**：租户、身份、权限和管理操作都在 Server，因此期望态必须在 Server 持久化并生成单调递增的 revision。
- **Edge 是实际态的唯一观测源**：进程、句柄、健康、重启次数和本地安装物只存在于 Edge；Server 只能保存 Edge 上报的投影。
- **Edge 本地状态是离线执行缓存，不是第二个控制面**：断网时继续运行最后一次已应用的 revision；重连后由 Server revision 决定是否更新。
- **同步采用声明式快照，不采用只发一次的命令队列**：快照可重放、幂等、可在断线重连后自动收敛。

```text
Operator / WebUI / CLI
          │ tenant-authenticated REST
          ▼
Server desired state + audit log        (authoritative)
          │ plugin_desired(revision, snapshot)
          ▼
Edge reconciler + local applied cache   (offline-capable projection)
          │ plugin_status(applied_revision, observed snapshot)
          ▼
Server observed projection              (read model for Catalog/UI)
```

## 2. 不变量

1. `tenant_id + edge_id + instance_id` 唯一标识一个插件实例；不同租户不得复用内存对象、持久化行或消息路由。
2. Server revision 对每个 tenant/edge 单调递增；Edge 不得应用小于当前 `applied_revision` 的快照。
3. 相同 revision + 相同 payload 重放必须无副作用；相同 revision + 不同 payload 必须拒绝并记录协议错误。
4. Edge 离线不等于实例应停止；实例继续遵循最后一次已成功应用的快照。
5. Server 不把“期望启用”展示为“实际健康”；desired 与 observed 永远分别存储、分别渲染。
6. Edge 上报不得包含明文 secret、访问令牌、本地绝对路径或插件 stdout/stderr 原文。
7. 未在本租户绑定的 Edge 上报、ack 或 observed snapshot 一律 fail-closed。
8. 写操作先落 Server 事务与审计，再异步下发；Edge ack 失败不能回滚已经发生的审计事实。

## 3. 状态模型

### 3.1 Server 期望态

每个实例至少包含：

- `tenant_id`
- `edge_id`
- `instance_id`
- `plugin_id`
- 固定 `version`
- `enabled`
- `isolation`
- 非敏感配置 JSON
- secret handle 列表（只存引用，不存明文）
- `revision`
- `created_at` / `updated_at`

Server 每次创建、更新、启停或删除实例时，在一个事务内：

1. 校验 RBAC、插件权限、目标 Edge 归属和租户配额；
2. 更新期望态；
3. 增加 tenant/edge revision；
4. 追加 audit event；
5. 提交后通知在线 Edge；离线 Edge 在下次重连时获取完整快照。

### 3.2 Edge 应用缓存

Edge 保存：

- 最近一次成功应用的 revision；
- 对应期望态快照的规范化摘要；
- 每个实例的本地应用结果；
- 本地安装物与 `plugins.lock`；
- 不可逆本地错误的非敏感摘要。

写文件继续使用同目录临时文件 + fsync + 原子 rename。新快照应用成功后才推进 `applied_revision`；部分失败时 revision 不前进，并在 ack 中逐实例报告失败。

### 3.3 Server 实际态投影

Edge 上报的实际态至少包含：

- 安装物：plugin id、version、kind、protocol、digest、trust mode、verified、publisher、manifest 的公开字段；
- 实例：instance id、plugin id、运行 state、health、restart count、last healthy、非敏感错误摘要；
- `applied_revision`、report sequence、reported_at；
- Edge 启动标识 `boot_id`，用于识别进程重启和拒绝旧连接迟到消息。

Server 保存该投影供 Catalog/UI 查询。投影过期只标记 `stale/offline`，不得凭空改写 desired。

## 4. 协议扩展

现有 WS 信封保持版本 1；以下是向后兼容的新增消息类型。旧 Server/Edge 对未知消息必须忽略并记录 debug，不得断开连接。

### 4.1 `plugin_status`（Edge → Server）

```json
{
  "v": 1,
  "type": "plugin_status",
  "ts": 0,
  "data": {
    "boot_id": "opaque-random-id",
    "sequence": 1,
    "applied_revision": 3,
    "installations": [],
    "instances": []
  }
}
```

要求：同一 `boot_id` 下 sequence 单调递增；Server 忽略重复和倒序上报。新 `boot_id` 可从 sequence 1 开始。

### 4.2 `plugin_desired`（Server → Edge）

```json
{
  "v": 1,
  "type": "plugin_desired",
  "ts": 0,
  "data": {
    "revision": 4,
    "instances": []
  }
}
```

Server 在 Edge hello 成功后发送当前完整快照；期望态变更后再次发送。配置中的 secret 只能以 handle 表示。

### 4.3 `plugin_ack`（Edge → Server）

```json
{
  "v": 1,
  "type": "plugin_ack",
  "ts": 0,
  "data": {
    "revision": 4,
    "status": "applied",
    "results": []
  }
}
```

`status` 仅允许 `applied` / `rejected` / `failed`。错误文本必须经过长度限制和 secret/path 脱敏。

## 5. 持久化边界

建议新增三组表，迁移必须保持 DDL 与 `PRAGMA user_version` 原子提交：

1. `plugin_desired_instances`：Server 权威期望态；主键 `(tenant_id, edge_id, instance_id)`。
2. `plugin_edge_revisions`：每个 tenant/edge 的 desired revision、applied revision、boot id、last sequence、last report/ack 时间。
3. `plugin_installations` 与 `plugin_observations`：Edge 上报的只读投影；所有查询必须带 tenant id。

删除期望态不立即删除审计；投影可按租户保留策略清理。任何 upsert 不得修改既有行的 tenant id。

## 6. 管理 API

v0.1 最小写面：

- `POST /api/plugin-instances`
- `PATCH /api/plugin-instances/{id}`
- `DELETE /api/plugin-instances/{id}`（默认保留插件数据，显式 purge 才删除）
- `POST /api/plugin-instances/{id}/reconcile`

权限：viewer 只读；operator 可创建、修改、启停和 reconcile；扩大插件权限、purge 数据、修改 secret binding 要求 admin 或显式确认。所有写操作生成 request id 和 audit event。

本地 `cloudpath plugin enable/disable/update/remove` 在 Server-managed 模式下应改为调用管理 API。保留显式 `--local` break-glass 模式，但它不得伪装成 Server desired，也必须在下一次同步时接受 Server revision 收敛。

## 7. Secret 边界

期望态只携带形如 `secret://<name>` 的 handle。Server 只保存和转发 handle，**永远不接收或解析明文**。明文 secret：

- 只存在于 Edge 本地、按 tenant/instance 隔离的 secret provider（文件实现要求 0600 或平台等价权限）与目标插件进程的短期内存；
- 不进入 WS、SQLite、audit metadata、slog、`plugins.lock`、desired-state JSON 或 UI response；
- Edge 仅在插件 manifest 已声明该 secret name，且实例配置显式绑定该 handle 时解析；
- handle 不存在、已吊销、路径权限不安全或声明不符时，实例 reconcile fail-closed；不得回落到旧明文缓存。

这样 secret 不跨越网络信任边界，也不要求 v0.1 自建中心密钥库。具体规则见 [tenant-security-policy.md](tenant-security-policy.md)。

## 8. 故障与恢复

| 故障 | 处理 |
|---|---|
| Edge 离线 | Server 保留 desired；Edge 继续最后已应用 revision；UI 显示 observed stale |
| Server 重启 | 从 SQLite 恢复 desired/revision/projection；Edge 重连后完整重放 |
| Edge 重启 | 从本地 applied cache 启动；用新 boot id 上报；Server 发送当前 desired |
| 重复/倒序消息 | 幂等忽略；相同 revision 不同摘要拒绝并审计协议异常 |
| 单实例应用失败 | 整个 revision 不确认；ack 返回逐实例结果；保留上一个完整已应用快照 |
| 插件崩溃 | desired 不变；observed state/health/restart count 更新 |
| 权限扩大 | 未显式确认不生成新 desired revision |
| 秘密已吊销 | reconcile 失败；不回落旧明文；审计只记录 handle 名称/版本 |

## 9. 实施顺序与并行边界

1. **契约冻结**：本文 + API DTO +协议测试。
2. **并行实现**：
   - Store lane：schema/migration/repository；
   - Edge lane：状态采集、本地 cache、reconciler、WS report/ack；
   - Server lane：WS ingest/downlink、Catalog SourceReader、只读投影；
   - Security lane：secret handle/policy/quota 的纯领域逻辑。
3. **接缝集成**：Server 管理写 API + audit；CLI 远端模式；Catalog UI 接真实数据。
4. **验收**：断网、重连、重复消息、跨租户、stale boot、权限扩大、secret 吊销的真实 WS + SQLite E2E。

共享写点唯一归属：Store lane 独占 `internal/store/**`；Edge lane 独占 Edge 运行时；Server lane 独占 Server 路由/WS；API 契约在并行开始前先合并。

## 10. 完成定义

只有同时满足以下条件，才能称插件控制面完成：

- Server 写 desired，Edge 应用并 ack，Server Catalog 读到真实 observed；
- Edge 断线重连后自动收敛且不中断上一个已应用配置；
- desired/observed、tenant、revision、boot/sequence 均有反向测试；
- 所有写操作进入审计，所有 secret 只以 handle 出现在配置和日志；
- Core、WebUI、模板、公开审计和链接聚合门禁全绿；
- 至少一个真实独立 Driver Plugin 和一个 Application Plugin 完成从发现、验证安装到运行的端到端验证。
