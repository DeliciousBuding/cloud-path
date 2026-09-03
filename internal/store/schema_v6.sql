-- Cloudpath SQLite schema v6（租户审计日志 audit_events）
-- append-only：业务层只提供 INSERT 与 tenant-scoped 查询，不提供 update/delete；
-- 查询恒按 tenant_id 过滤（跨租户不可见），tenant+created_at/action 建索引。
CREATE TABLE IF NOT EXISTS audit_events(
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  tenant_id INTEGER NOT NULL REFERENCES tenant(id),
  actor_type TEXT NOT NULL DEFAULT '',
  actor_id INTEGER NOT NULL DEFAULT 0,
  actor_name TEXT NOT NULL DEFAULT '',
  action TEXT NOT NULL,
  target_type TEXT NOT NULL DEFAULT '',
  target_id TEXT NOT NULL DEFAULT '',
  outcome TEXT NOT NULL DEFAULT '',
  request_id TEXT NOT NULL DEFAULT '',
  remote_ip TEXT NOT NULL DEFAULT '',
  metadata_json TEXT NOT NULL DEFAULT '{}',
  created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_audit_events_tenant_created ON audit_events(tenant_id, created_at);
CREATE INDEX IF NOT EXISTS idx_audit_events_tenant_action ON audit_events(tenant_id, action);
