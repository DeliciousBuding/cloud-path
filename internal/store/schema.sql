-- Cloudpath SQLite schema v1（PRAGMA user_version 迁移）
CREATE TABLE IF NOT EXISTS devices(
  id TEXT PRIMARY KEY,            -- "<edge_id>/<device_id>"
  edge_id TEXT NOT NULL,
  adapter TEXT NOT NULL DEFAULT '',
  name TEXT NOT NULL DEFAULT '',
  port TEXT NOT NULL DEFAULT '',
  meta TEXT NOT NULL DEFAULT '{}',
  first_seen INTEGER NOT NULL,
  last_seen INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS device_state(
  device_id TEXT PRIMARY KEY REFERENCES devices(id),
  state TEXT NOT NULL DEFAULT '{}',
  online INTEGER NOT NULL DEFAULT 0,
  updated_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS events(
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  device_id TEXT NOT NULL,
  ts INTEGER NOT NULL,
  type TEXT NOT NULL,
  payload TEXT NOT NULL DEFAULT '{}'
);
CREATE INDEX IF NOT EXISTS idx_events_device_ts ON events(device_id, ts);
CREATE TABLE IF NOT EXISTS commands(
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  device_id TEXT NOT NULL,
  cmd TEXT NOT NULL,
  args TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'pending',   -- pending|sent|ok|failed|timeout
  created_at INTEGER NOT NULL,
  acked_at INTEGER,
  result TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_commands_status ON commands(status, created_at);