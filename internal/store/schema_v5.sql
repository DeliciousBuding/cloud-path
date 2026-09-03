-- Cloudpath SQLite schema v5（租户服务令牌 tenant_tokens）
-- 每租户多服务令牌：只存 SHA-256 hash 与短 prefix，明文仅在创建响应返回一次。
-- scopes 为 JSON 数组（read|write|admin|edge），权限取 scope 与角色模型交集。
CREATE TABLE IF NOT EXISTS tenant_tokens(
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  tenant_id INTEGER NOT NULL REFERENCES tenant(id),
  name TEXT NOT NULL DEFAULT '',
  prefix TEXT NOT NULL,
  hash TEXT NOT NULL,
  scopes TEXT NOT NULL DEFAULT '[]',
  expires_at INTEGER,           -- NULL=永不过期
  last_used_at INTEGER,
  revoked_at INTEGER,           -- NULL=未吊销
  created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_tenant_tokens_tenant ON tenant_tokens(tenant_id);
CREATE INDEX IF NOT EXISTS idx_tenant_tokens_hash ON tenant_tokens(hash);
CREATE INDEX IF NOT EXISTS idx_tenant_tokens_prefix ON tenant_tokens(prefix);
