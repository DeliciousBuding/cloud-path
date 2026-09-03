-- Cloudpath SQLite schema v7（插件控制面持久化）
-- 权威划分（docs/architecture/control-plane-sync.md §1、§5）：
--   Server 是 desired / revision / 审计唯一权威 → plugin_desired_instances、plugin_edge_revisions 只接受 Server 写方向；
--   Edge 是 observed 唯一权威 → plugin_installations、plugin_observations 是 Edge 上报的只读投影（read model），
--   Server 不得凭投影改写 desired，投影过期只由读侧标记 stale。
-- 安全边界（docs/architecture/tenant-security-policy.md §2.4）：
--   本 schema 永不存 secret 明文列；secret 只以 config_json 内的 `secret://<name>` handle 与 secret_refs 名称出现。
--   也永不存本机绝对路径、启动参数、环境变量或插件 stdout/stderr 原文；detail 为脱敏后的非敏感摘要。
-- 所有表主键首列均为 tenant_id：租户隔离由主键结构锁定，任何 upsert 都无法改写既有行的 tenant_id。

-- Server 权威期望态：一行 = 一个 (tenant, edge, instance) 插件实例。
-- revision 是该行最后一次写入时所属 (tenant_id, edge_id) 的 desired revision（见 plugin_edge_revisions）。
CREATE TABLE IF NOT EXISTS plugin_desired_instances(
  tenant_id INTEGER NOT NULL REFERENCES tenant(id),
  edge_id TEXT NOT NULL,
  instance_id TEXT NOT NULL,
  plugin_id TEXT NOT NULL,
  version TEXT NOT NULL DEFAULT '',
  enabled INTEGER NOT NULL DEFAULT 0,
  isolation TEXT NOT NULL DEFAULT '',
  config_json TEXT NOT NULL DEFAULT '{}',   -- map[string]string 的 JSON；值只含非敏感标量或 secret:// handle
  secret_refs TEXT NOT NULL DEFAULT '[]',   -- JSON []string，只含 handle 名称，永不含明文
  revision INTEGER NOT NULL DEFAULT 0 CHECK(revision >= 0),
  created_at INTEGER NOT NULL DEFAULT 0,
  updated_at INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY(tenant_id, edge_id, instance_id)
);

-- 每个 (tenant, edge) 的同步游标：单调 desired/applied revision + boot 身份 + 上报序号。
-- prev_boot_id 记住被取代的上一代 boot，用于丢弃旧 boot 迟到消息（control-plane-sync.md §8「重复/倒序消息」）。
CREATE TABLE IF NOT EXISTS plugin_edge_revisions(
  tenant_id INTEGER NOT NULL REFERENCES tenant(id),
  edge_id TEXT NOT NULL,
  desired_revision INTEGER NOT NULL DEFAULT 0 CHECK(desired_revision >= 0),
  applied_revision INTEGER NOT NULL DEFAULT 0 CHECK(applied_revision >= 0),
  boot_id TEXT NOT NULL DEFAULT '',
  prev_boot_id TEXT NOT NULL DEFAULT '',
  last_sequence INTEGER NOT NULL DEFAULT 0 CHECK(last_sequence >= 0),
  last_report_at INTEGER NOT NULL DEFAULT 0,
  last_ack_at INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY(tenant_id, edge_id)
);

-- Edge 上报的安装物投影（公开 manifest 事实，无路径/无凭据）。
CREATE TABLE IF NOT EXISTS plugin_installations(
  tenant_id INTEGER NOT NULL REFERENCES tenant(id),
  edge_id TEXT NOT NULL,
  plugin_id TEXT NOT NULL,
  version TEXT NOT NULL DEFAULT '',
  kind TEXT NOT NULL DEFAULT '',
  protocol INTEGER NOT NULL DEFAULT 0,
  digest TEXT NOT NULL DEFAULT '',
  trust_mode TEXT NOT NULL DEFAULT '',
  verified INTEGER NOT NULL DEFAULT 0,
  verified_publisher TEXT NOT NULL DEFAULT '',
  permissions_json TEXT NOT NULL DEFAULT '{}',
  contributions_json TEXT NOT NULL DEFAULT '{}',
  capabilities_json TEXT NOT NULL DEFAULT '[]',
  reported_at INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY(tenant_id, edge_id, plugin_id)
);

-- Edge 上报的实例实际态投影。desired 字段一律不入本表（desired/observed 永远分开存储）。
CREATE TABLE IF NOT EXISTS plugin_observations(
  tenant_id INTEGER NOT NULL REFERENCES tenant(id),
  edge_id TEXT NOT NULL,
  instance_id TEXT NOT NULL,
  plugin_id TEXT NOT NULL DEFAULT '',
  version TEXT NOT NULL DEFAULT '',
  host_online INTEGER NOT NULL DEFAULT 0,
  state TEXT NOT NULL DEFAULT '',
  health TEXT NOT NULL DEFAULT '',
  detail TEXT NOT NULL DEFAULT '',          -- 脱敏后的非敏感错误摘要，有长度上限由写侧保证
  restart_count INTEGER NOT NULL DEFAULT 0,
  last_healthy INTEGER NOT NULL DEFAULT 0,
  message_rate REAL NOT NULL DEFAULT 0,
  reported_at INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY(tenant_id, edge_id, instance_id)
);

-- 保留期清理（tenant-security-policy.md §3.1 plugin observed projection 默认 30 天）：
-- 按 (tenant, reported_at) 分批删除，恒带 tenant predicate。desired 不随 observed 清理。
CREATE INDEX IF NOT EXISTS idx_plugin_observations_tenant_reported ON plugin_observations(tenant_id, reported_at);
