-- Cloudpath SQLite schema v8（租户策略：保留期 / 配额）
-- 契约来源：docs/architecture/tenant-security-policy.md §3.1（保留期默认与范围）、
-- §4（配额硬上限与拒绝点）、§5（数据模型：单表、字段有 DB CHECK、NULL 表示继承默认）、
-- §6（不变量：配额拒绝不产生业务写入或 revision 变化；清理 A 不得触及 B）。
-- 设计要点：
--   * 单表 tenant_policies，主键即 tenant_id → upsert 结构上不可能改写既有行的 tenant_id；
--   * 每个字段可为 NULL，语义是「继承 Server 默认值」；绝不用 0 表示无限（0 直接被 CHECK 拒绝）；
--   * 字段集与 internal/tenantpolicy 的 RetentionDays / Quotas 一一对应，避免两处默认值漂移；
--   * 本表不含任何 secret 列：secret 明文永不进 SQLite（tenant-security-policy.md §2.2）。

CREATE TABLE IF NOT EXISTS tenant_policies(
  tenant_id INTEGER NOT NULL PRIMARY KEY REFERENCES tenant(id),

  -- 保留期（天）。NULL=继承默认（events 30 / commands 30 / audit 90 / revoked tokens 7 / plugin observed 30）。
  retention_events_days INTEGER
    CHECK(retention_events_days IS NULL OR (retention_events_days BETWEEN 1 AND 3650)),
  retention_commands_days INTEGER
    CHECK(retention_commands_days IS NULL OR (retention_commands_days BETWEEN 1 AND 3650)),
  retention_audit_days INTEGER
    CHECK(retention_audit_days IS NULL OR (retention_audit_days BETWEEN 7 AND 3650)),
  retention_revoked_tokens_days INTEGER
    CHECK(retention_revoked_tokens_days IS NULL OR (retention_revoked_tokens_days BETWEEN 1 AND 3650)),
  retention_plugin_observed_days INTEGER
    CHECK(retention_plugin_observed_days IS NULL OR (retention_plugin_observed_days BETWEEN 1 AND 3650)),

  -- 配额（硬上限，防滥用非计费）。NULL=继承默认（devices 200 / edges 50 / browser ws 20 /
  -- tokens 100 / users 100 / events per min 600 / plugin instances 100）。
  quota_devices INTEGER
    CHECK(quota_devices IS NULL OR (quota_devices BETWEEN 1 AND 1000000)),
  quota_edges INTEGER
    CHECK(quota_edges IS NULL OR (quota_edges BETWEEN 1 AND 1000000)),
  quota_browser_ws INTEGER
    CHECK(quota_browser_ws IS NULL OR (quota_browser_ws BETWEEN 1 AND 1000000)),
  quota_tokens INTEGER
    CHECK(quota_tokens IS NULL OR (quota_tokens BETWEEN 1 AND 1000000)),
  quota_users INTEGER
    CHECK(quota_users IS NULL OR (quota_users BETWEEN 1 AND 1000000)),
  quota_events_per_min INTEGER
    CHECK(quota_events_per_min IS NULL OR (quota_events_per_min BETWEEN 1 AND 1000000)),
  quota_plugin_instances INTEGER
    CHECK(quota_plugin_instances IS NULL OR (quota_plugin_instances BETWEEN 1 AND 1000000)),

  updated_at INTEGER NOT NULL DEFAULT 0
);
