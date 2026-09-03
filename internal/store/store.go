// Package store 是 SQLite 持久层（modernc.org/sqlite，纯 Go 零 CGO）。
// schema 内嵌，启动时按 PRAGMA user_version 自动逐级迁移。
package store

import (
	"database/sql"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaV1 string

//go:embed schema_v2.sql
var schemaV2 string

// migrations 是有序迁移表：新增版本追加一项，永不修改已发布项。
var migrations = []struct {
	version int
	ddl     string
}{
	{1, schemaV1},
	{2, schemaV2},
}

// schemaVersion 是当前 schema 版本（迁移表最后一项）。
var schemaVersion = migrations[len(migrations)-1].version

// Store 持有数据库连接。所有方法并发安全（database/sql 连接池）。
type Store struct {
	db   *sql.DB
	path string
}

// Open 打开（或创建）数据库并执行迁移。path 为 ":memory:" 时用内存库（测试）。
func Open(path string) (*Store, error) {
	if path != ":memory:" {
		if dir := filepath.Dir(path); dir != "" && dir != "." {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return nil, fmt.Errorf("store: mkdir: %w", err)
			}
		}
	}
	dsn := path
	switch {
	case path == ":memory:":
		// 多连接共享同一内存库（裸 :memory: 每连接一库，测试会踩坑）
		dsn = "file:cloudpath_mem?mode=memory&cache=shared&_pragma=busy_timeout(5000)"
	default:
		dsn = "file:" + filepath.ToSlash(path) + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("store: open: %w", err)
	}
	// SQLite 写并发弱：小连接池 + WAL + busy_timeout，避免 database is locked
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(4)
	db.SetConnMaxLifetime(0)
	s := &Store{db: db, path: path}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) migrate() error {
	var version int
	if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return fmt.Errorf("store: read user_version: %w", err)
	}
	for _, m := range migrations {
		if version >= m.version {
			continue
		}
		if _, err := s.db.Exec(m.ddl); err != nil {
			return fmt.Errorf("store: schema v%d: %w", m.version, err)
		}
		if _, err := s.db.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, m.version)); err != nil {
			return fmt.Errorf("store: set user_version=%d: %w", m.version, err)
		}
	}
	return nil
}

// Version 返回当前 schema 版本（迁移后）。
func (s *Store) Version() int { return schemaVersion }

// Close 关闭数据库。
func (s *Store) Close() error { return s.db.Close() }

func now() int64 { return time.Now().Unix() }

// UpsertDevice 注册/刷新设备元信息。
func (s *Store) UpsertDevice(id, edgeID, adapter, name, port string) error {
	ts := now()
	_, err := s.db.Exec(`
		INSERT INTO devices(id, edge_id, adapter, name, port, first_seen, last_seen)
		VALUES(?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
			edge_id=excluded.edge_id, adapter=excluded.adapter,
			name=excluded.name, port=excluded.port, last_seen=excluded.last_seen`,
		id, edgeID, adapter, name, port, ts, ts)
	return err
}

// DeviceRow 是 devices 表一行。
type DeviceRow struct {
	ID        string
	EdgeID    string
	Adapter   string
	Name      string
	Port      string
	FirstSeen int64
	LastSeen  int64
}

// ListDevices 返回全部注册设备。
func (s *Store) ListDevices() ([]DeviceRow, error) {
	rows, err := s.db.Query(`SELECT id, edge_id, adapter, name, port, first_seen, last_seen FROM devices ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DeviceRow
	for rows.Next() {
		var d DeviceRow
		if err := rows.Scan(&d.ID, &d.EdgeID, &d.Adapter, &d.Name, &d.Port, &d.FirstSeen, &d.LastSeen); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// SetState 覆盖设备最新状态。
func (s *Store) SetState(deviceID, stateJSON string, online bool, updatedAt int64) error {
	o := 0
	if online {
		o = 1
	}
	_, err := s.db.Exec(`
		INSERT INTO device_state(device_id, state, online, updated_at) VALUES(?,?,?,?)
		ON CONFLICT(device_id) DO UPDATE SET
			state=excluded.state, online=excluded.online, updated_at=excluded.updated_at`,
		deviceID, stateJSON, o, updatedAt)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`UPDATE devices SET last_seen=? WHERE id=?`, updatedAt, deviceID)
	return err
}

// StateRow 是 device_state 一行。
type StateRow struct {
	DeviceID  string
	State     string
	Online    bool
	UpdatedAt int64
}

// GetStates 返回全部设备最新状态。
func (s *Store) GetStates() (map[string]StateRow, error) {
	rows, err := s.db.Query(`SELECT device_id, state, online, updated_at FROM device_state`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]StateRow{}
	for rows.Next() {
		var r StateRow
		var o int
		if err := rows.Scan(&r.DeviceID, &r.State, &o, &r.UpdatedAt); err != nil {
			return nil, err
		}
		r.Online = o != 0
		out[r.DeviceID] = r
	}
	return out, rows.Err()
}

// AddEvent 写入一条事件，返回行 ID。
func (s *Store) AddEvent(deviceID, typ, payload string, ts int64) (int64, error) {
	res, err := s.db.Exec(`INSERT INTO events(device_id, ts, type, payload) VALUES(?,?,?,?)`,
		deviceID, ts, typ, payload)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// EventRow 是 events 一行。
type EventRow struct {
	ID       int64
	DeviceID string
	Ts       int64
	Type     string
	Payload  string
}

// ListEvents 查询事件历史。deviceID 为空查全部；since>0 只取 ts>=since；limit<=0 默认 100（上限 1000）。
func (s *Store) ListEvents(deviceID string, since int64, limit int) ([]EventRow, error) {
	limit = clampLimit(limit)
	q := `SELECT id, device_id, ts, type, payload FROM events WHERE ts >= ?`
	args := []any{since}
	if deviceID != "" {
		q += ` AND device_id = ?`
		args = append(args, deviceID)
	}
	q += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EventRow
	for rows.Next() {
		var e EventRow
		if err := rows.Scan(&e.ID, &e.DeviceID, &e.Ts, &e.Type, &e.Payload); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// PruneEvents 删除 ts < before 的事件（保留期清理），返回删除行数。
func (s *Store) PruneEvents(before int64) (int64, error) {
	res, err := s.db.Exec(`DELETE FROM events WHERE ts < ?`, before)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// PruneCommands 删除 ts < before 的终态命令（保留期清理），返回删除行数。
// 仅清理已结算状态，避免误删仍在等待回执的命令行。
func (s *Store) PruneCommands(before int64) (int64, error) {
	res, err := s.db.Exec(`DELETE FROM commands WHERE created_at < ? AND status IN ('ok','failed','timeout')`, before)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// Stats 是存储侧计数（系统页展示用）。
type Stats struct {
	Devices     int64 `json:"devices"`
	Events      int64 `json:"events"`
	Commands    int64 `json:"commands"`
	OldestEvent int64 `json:"oldest_event"`
	SchemaVer   int   `json:"schema_version"`
}

// Stats 返回行数统计与最早事件时间（0 = 无事件）。
func (s *Store) Stats() (Stats, error) {
	var st Stats
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM devices`).Scan(&st.Devices); err != nil {
		return st, err
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM events`).Scan(&st.Events); err != nil {
		return st, err
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM commands`).Scan(&st.Commands); err != nil {
		return st, err
	}
	var oldest sql.NullInt64
	if err := s.db.QueryRow(`SELECT MIN(ts) FROM events`).Scan(&oldest); err != nil {
		return st, err
	}
	st.OldestEvent = oldest.Int64
	st.SchemaVer = schemaVersion
	return st, nil
}

// CreateCommand 建命令行（pending），返回命令 ID。
func (s *Store) CreateCommand(deviceID, cmd, args string) (int64, error) {
	res, err := s.db.Exec(`INSERT INTO commands(device_id, cmd, args, status, created_at) VALUES(?,?,?,?,?)`,
		deviceID, cmd, args, "pending", now())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UpdateCommandStatus 更新命令状态与回执。
func (s *Store) UpdateCommandStatus(id int64, status, result string) error {
	_, err := s.db.Exec(`UPDATE commands SET status=?, result=?, acked_at=? WHERE id=?`,
		status, result, now(), id)
	return err
}

// CommandRow 是 commands 一行。
type CommandRow struct {
	ID        int64
	DeviceID  string
	Cmd       string
	Args      string
	Status    string
	CreatedAt int64
	AckedAt   sql.NullInt64
	Result    string
}

// ListCommands 查询命令。deviceID/status 为空表示不过滤。
func (s *Store) ListCommands(deviceID, status string, limit int) ([]CommandRow, error) {
	limit = clampLimit(limit)
	q := `SELECT id, device_id, cmd, args, status, created_at, acked_at, result FROM commands WHERE 1=1`
	args := []any{}
	if deviceID != "" {
		q += ` AND device_id = ?`
		args = append(args, deviceID)
	}
	if status != "" {
		q += ` AND status = ?`
		args = append(args, status)
	}
	q += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CommandRow
	for rows.Next() {
		var c CommandRow
		if err := rows.Scan(&c.ID, &c.DeviceID, &c.Cmd, &c.Args, &c.Status, &c.CreatedAt, &c.AckedAt, &c.Result); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// TimeoutStaleCommands 把超过 ttl 仍 pending/sent 的命令标记 timeout，返回受影响行。
func (s *Store) TimeoutStaleCommands(ttl time.Duration) (int64, error) {
	cutoff := time.Now().Add(-ttl).Unix()
	res, err := s.db.Exec(`UPDATE commands SET status='timeout', acked_at=?, result='edge 未回执'
		WHERE status IN ('pending','sent') AND created_at < ?`, now(), cutoff)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// clampLimit 归一 limit：<=0 或 >1000 → 100（防止无界查询拖垮服务）。
func clampLimit(limit int) int {
	if limit <= 0 || limit > 1000 {
		return 100
	}
	return limit
}
