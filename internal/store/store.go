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

//go:embed schema_v3.sql
var schemaV3 string

//go:embed schema_v4.sql
var schemaV4 string

//go:embed schema_v5.sql
var schemaV5 string

//go:embed schema_v6.sql
var schemaV6 string

// migration 是一次 schema 迁移：ddl 走单事务；custom 用于需要 PRAGMA foreign_keys
// 开关的表重建（如 v5 users 重建），由实现自行管理连接与事务。
type migration struct {
	version int
	ddl     string
	custom  func(*sql.DB) error
}

// migrations 是有序迁移表：新增版本追加一项，永不修改已发布项。
var migrations = []migration{
	{version: 1, ddl: schemaV1},
	{version: 2, ddl: schemaV2},
	{version: 3, ddl: schemaV3},
	{version: 4, ddl: schemaV4},
	{version: 5, custom: migrateV5},
	{version: 6, ddl: schemaV6},
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
		if m.custom != nil {
			if err := m.custom(s.db); err != nil {
				return fmt.Errorf("store: schema v%d: %w", m.version, err)
			}
		} else {
			// 每个版本一个事务：DDL 与数据回填同批原子提交，失败不留半迁移。
			tx, err := s.db.Begin()
			if err != nil {
				return fmt.Errorf("store: begin schema v%d: %w", m.version, err)
			}
			if _, err := tx.Exec(m.ddl); err != nil {
				_ = tx.Rollback()
				return fmt.Errorf("store: schema v%d: %w", m.version, err)
			}
			if err := tx.Commit(); err != nil {
				return fmt.Errorf("store: commit schema v%d: %w", m.version, err)
			}
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

// TenantIDBySlug 按租户 slug 读 id；不存在返回 sql.ErrNoRows。
func (s *Store) TenantIDBySlug(slug string) (int64, error) {
	var id int64
	if err := s.db.QueryRow(`SELECT id FROM tenant WHERE slug=?`, slug).Scan(&id); err != nil {
		return 0, err
	}
	return id, nil
}

// normalizeTenantID 把 0（default 兼容写法）解析成 default 租户 id；>0 原样返回。
func (s *Store) normalizeTenantID(tenantID int64) (int64, error) {
	if tenantID > 0 {
		return tenantID, nil
	}
	var id int64
	if err := s.db.QueryRow(`SELECT id FROM tenant WHERE slug='default'`).Scan(&id); err != nil {
		return 0, fmt.Errorf("store: resolve default tenant: %w", err)
	}
	return id, nil
}

// UpsertDevice 注册/刷新设备元信息（default 兼容包装，无账号开发模式沿用）。
func (s *Store) UpsertDevice(id, edgeID, adapter, name, port string) error {
	return s.upsertDevice(id, edgeID, adapter, name, port, 0)
}

// UpsertDeviceTenant 注册/刷新设备元信息并绑定租户（账号模式使用）。
func (s *Store) UpsertDeviceTenant(id, edgeID, adapter, name, port string, tenantID int64) error {
	return s.upsertDevice(id, edgeID, adapter, name, port, tenantID)
}

func (s *Store) upsertDevice(id, edgeID, adapter, name, port string, tenantID int64) error {
	tid, err := s.normalizeTenantID(tenantID)
	if err != nil {
		return err
	}
	ts := now()
	_, err = s.db.Exec(`
		INSERT INTO devices(id, edge_id, adapter, name, port, first_seen, last_seen, tenant_id)
		VALUES(?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
			edge_id=excluded.edge_id, adapter=excluded.adapter,
			name=excluded.name, port=excluded.port, last_seen=excluded.last_seen,
			tenant_id=excluded.tenant_id`,
		id, edgeID, adapter, name, port, ts, ts, tid)
	return err
}

// DeviceRow 是 devices 表一行。
type DeviceRow struct {
	ID         string
	EdgeID     string
	Adapter    string
	Name       string
	Port       string
	FirstSeen  int64
	LastSeen   int64
	TenantID   int64
	TenantSlug string
}

const deviceColumns = `d.id, d.edge_id, d.adapter, d.name, d.port, d.first_seen, d.last_seen, d.tenant_id, COALESCE(t.slug,'default')`

func scanDevice(scanner interface{ Scan(...any) error }) (DeviceRow, error) {
	var d DeviceRow
	err := scanner.Scan(&d.ID, &d.EdgeID, &d.Adapter, &d.Name, &d.Port, &d.FirstSeen, &d.LastSeen, &d.TenantID, &d.TenantSlug)
	return d, err
}

// ListDevices 返回全部注册设备（无账号开发模式沿用）。
func (s *Store) ListDevices() ([]DeviceRow, error) {
	return s.listDevices(0)
}

// ListDevicesTenant 返回指定租户的设备（账号模式）。
func (s *Store) ListDevicesTenant(tenantID int64) ([]DeviceRow, error) {
	return s.listDevices(tenantID)
}

func (s *Store) listDevices(tenantID int64) ([]DeviceRow, error) {
	q := `SELECT ` + deviceColumns + ` FROM devices d LEFT JOIN tenant t ON t.id = d.tenant_id`
	args := []any{}
	if tenantID > 0 {
		q += ` WHERE d.tenant_id = ?`
		args = append(args, tenantID)
	}
	q += ` ORDER BY d.id`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DeviceRow
	for rows.Next() {
		d, err := scanDevice(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// SetState 覆盖设备最新状态；tenant_id 由 devices 表继承（不信任调用方自报）。
func (s *Store) SetState(deviceID, stateJSON string, online bool, updatedAt int64) error {
	o := 0
	if online {
		o = 1
	}
	_, err := s.db.Exec(`
		INSERT INTO device_state(device_id, tenant_id, state, online, updated_at)
		SELECT id, tenant_id, ?, ?, ? FROM devices WHERE id=?
		ON CONFLICT(device_id) DO UPDATE SET
			tenant_id=(SELECT tenant_id FROM devices WHERE id=excluded.device_id),
			state=excluded.state, online=excluded.online, updated_at=excluded.updated_at`,
		stateJSON, o, updatedAt, deviceID)
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

// GetStates 返回全部设备最新状态（启动水合用）。
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

// AddEvent 写入一条事件，返回行 ID；tenant_id 由 devices 表继承，设备缺失时回落 default。
func (s *Store) AddEvent(deviceID, typ, payload string, ts int64) (int64, error) {
	res, err := s.db.Exec(`
		INSERT INTO events(device_id, tenant_id, ts, type, payload)
		VALUES(?, COALESCE((SELECT tenant_id FROM devices WHERE id=?), (SELECT id FROM tenant WHERE slug='default')), ?, ?, ?)`,
		deviceID, deviceID, ts, typ, payload)
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
	return s.listEvents(0, deviceID, since, limit)
}

// ListEventsTenant 查询指定租户的事件历史（账号模式）。
func (s *Store) ListEventsTenant(tenantID int64, deviceID string, since int64, limit int) ([]EventRow, error) {
	return s.listEvents(tenantID, deviceID, since, limit)
}

func (s *Store) listEvents(tenantID int64, deviceID string, since int64, limit int) ([]EventRow, error) {
	limit = clampLimit(limit)
	q := `SELECT id, device_id, ts, type, payload FROM events WHERE ts >= ?`
	args := []any{since}
	if tenantID > 0 {
		q += ` AND tenant_id = ?`
		args = append(args, tenantID)
	}
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

// Stats 返回全局行数统计与最早事件时间（无账号开发模式沿用）。
func (s *Store) Stats() (Stats, error) {
	return s.stats(0)
}

// StatsTenant 返回指定租户的行数统计与最早事件时间（账号模式）。
func (s *Store) StatsTenant(tenantID int64) (Stats, error) {
	return s.stats(tenantID)
}

func (s *Store) stats(tenantID int64) (Stats, error) {
	var st Stats
	where := ""
	args := []any{}
	if tenantID > 0 {
		where = ` WHERE tenant_id = ?`
		args = append(args, tenantID)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM devices`+where, args...).Scan(&st.Devices); err != nil {
		return st, err
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM events`+where, args...).Scan(&st.Events); err != nil {
		return st, err
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM commands`+where, args...).Scan(&st.Commands); err != nil {
		return st, err
	}
	var oldest sql.NullInt64
	if err := s.db.QueryRow(`SELECT MIN(ts) FROM events`+where, args...).Scan(&oldest); err != nil {
		return st, err
	}
	st.OldestEvent = oldest.Int64
	st.SchemaVer = schemaVersion
	return st, nil
}

// CreateCommand 建命令行（pending），返回命令 ID；tenant_id 由 devices 表继承，设备缺失时回落 default。
func (s *Store) CreateCommand(deviceID, cmd, args string) (int64, error) {
	res, err := s.db.Exec(`
		INSERT INTO commands(device_id, tenant_id, cmd, args, status, created_at)
		VALUES(?, COALESCE((SELECT tenant_id FROM devices WHERE id=?), (SELECT id FROM tenant WHERE slug='default')), ?, ?, 'pending', ?)`,
		deviceID, deviceID, cmd, args, now())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// CreateCommandTenant 在指定租户下为已存在设备建命令行；设备不存在或跨租户返回 sql.ErrNoRows。
func (s *Store) CreateCommandTenant(deviceID, cmd, args string, tenantID int64) (int64, error) {
	tid, err := s.normalizeTenantID(tenantID)
	if err != nil {
		return 0, err
	}
	res, err := s.db.Exec(`
		INSERT INTO commands(device_id, tenant_id, cmd, args, status, created_at)
		SELECT id, tenant_id, ?, ?, 'pending', ? FROM devices WHERE id=? AND tenant_id=?`,
		cmd, args, now(), deviceID, tid)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	if n == 0 {
		return 0, sql.ErrNoRows
	}
	return res.LastInsertId()
}

// UpdateCommandStatus 更新命令状态与回执（default 兼容包装）。
func (s *Store) UpdateCommandStatus(id int64, status, result string) error {
	_, err := s.db.Exec(`UPDATE commands SET status=?, result=?, acked_at=? WHERE id=?`,
		status, result, now(), id)
	return err
}

// UpdateCommandStatusScoped 只允许更新同租户、同设备的命令行，返回是否命中（防止跨租户 ack）。
func (s *Store) UpdateCommandStatusScoped(id int64, deviceID string, tenantID int64, status, result string) (bool, error) {
	tid, err := s.normalizeTenantID(tenantID)
	if err != nil {
		return false, err
	}
	res, err := s.db.Exec(`
		UPDATE commands SET status=?, result=?, acked_at=?
		WHERE id=? AND device_id=? AND tenant_id=?`,
		status, result, now(), id, deviceID, tid)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
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
	return s.listCommands(0, deviceID, status, limit)
}

// ListCommandsTenant 查询指定租户的命令（账号模式）。
func (s *Store) ListCommandsTenant(tenantID int64, deviceID, status string, limit int) ([]CommandRow, error) {
	return s.listCommands(tenantID, deviceID, status, limit)
}

func (s *Store) listCommands(tenantID int64, deviceID, status string, limit int) ([]CommandRow, error) {
	limit = clampLimit(limit)
	q := `SELECT id, device_id, cmd, args, status, created_at, acked_at, result FROM commands WHERE 1=1`
	args := []any{}
	if tenantID > 0 {
		q += ` AND tenant_id = ?`
		args = append(args, tenantID)
	}
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
