-- Cloudpath SQLite schema v2（迁移：补齐检索索引）
-- 事件按时间窗查询（?since= / 保留期清理）与命令按设备查询都会随数据量增长变慢。
CREATE INDEX IF NOT EXISTS idx_events_ts ON events(ts);
CREATE INDEX IF NOT EXISTS idx_commands_device ON commands(device_id, created_at);
