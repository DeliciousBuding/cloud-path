-- Cloudpath SQLite schema v4（P2 租户数据隔离：devices/state/events/commands 归属 tenant）
-- 在 v3 的 tenant/users/sessions 之上补齐业务表 tenant_id 列，并把既有行原子回填到 default 租户。
-- SQLite 无法在 ALTER 上加约束，采用 DEFAULT 0 + 回填；tenant_id 恒指向 tenant(id)。

ALTER TABLE devices ADD COLUMN tenant_id INTEGER NOT NULL DEFAULT 0;
ALTER TABLE device_state ADD COLUMN tenant_id INTEGER NOT NULL DEFAULT 0;
ALTER TABLE events ADD COLUMN tenant_id INTEGER NOT NULL DEFAULT 0;
ALTER TABLE commands ADD COLUMN tenant_id INTEGER NOT NULL DEFAULT 0;

-- 确保 default 租户存在（v3 已建，但幂等保护迁移顺序/手工库）。
INSERT OR IGNORE INTO tenant(slug, name, created_at) VALUES('default', 'default', strftime('%s','now'));

-- 既有数据回填 default tenant。
UPDATE devices SET tenant_id = (SELECT id FROM tenant WHERE slug = 'default') WHERE tenant_id = 0;
UPDATE device_state SET tenant_id = (SELECT id FROM tenant WHERE slug = 'default') WHERE tenant_id = 0;
UPDATE events SET tenant_id = (SELECT id FROM tenant WHERE slug = 'default') WHERE tenant_id = 0;
UPDATE commands SET tenant_id = (SELECT id FROM tenant WHERE slug = 'default') WHERE tenant_id = 0;

-- tenant + 常用筛选列的隔离查询索引。
CREATE INDEX IF NOT EXISTS idx_devices_tenant ON devices(tenant_id);
CREATE INDEX IF NOT EXISTS idx_device_state_tenant ON device_state(tenant_id);
CREATE INDEX IF NOT EXISTS idx_events_tenant_device_ts ON events(tenant_id, device_id, ts);
CREATE INDEX IF NOT EXISTS idx_events_tenant_ts ON events(tenant_id, ts);
CREATE INDEX IF NOT EXISTS idx_commands_tenant_device ON commands(tenant_id, device_id, created_at);
CREATE INDEX IF NOT EXISTS idx_commands_tenant_status ON commands(tenant_id, status, created_at);
