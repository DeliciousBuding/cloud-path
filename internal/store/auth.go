package store

import (
	"database/sql"
	"fmt"
)

// TenantRow 是 tenant 表一行。
type TenantRow struct {
	ID        int64
	Slug      string
	Name      string
	CreatedAt int64
}

// UserRow 是 users 表一行。密码只存 argon2id 哈希（永不落明文）。
type UserRow struct {
	ID           int64
	TenantID     int64
	Username     string
	Name         string
	Role         string // admin|operator|viewer
	PasswordHash string
	CreatedAt    int64
	Disabled     bool
}

// AuthUser 是鉴权查询结果：用户 + 所属租户 slug + 会话最近活跃时间。
type AuthUser struct {
	UserRow
	TenantSlug      string
	SessionLastSeen int64 // 仅 UserBySession 填充
}

// SessionRow 是 sessions 表一行。
type SessionRow struct {
	ID         string
	UserID     int64
	CreatedAt  int64
	ExpiresAt  int64
	LastSeenAt int64
}

// EnsureDefaultTenant 幂等创建 default 租户（首装自动创建），返回其 id。
func (s *Store) EnsureDefaultTenant() (int64, error) {
	if _, err := s.db.Exec(
		`INSERT OR IGNORE INTO tenant(slug,name,created_at) VALUES('default','default',?)`, now()); err != nil {
		return 0, fmt.Errorf("store: ensure default tenant: %w", err)
	}
	var id int64
	if err := s.db.QueryRow(`SELECT id FROM tenant WHERE slug='default'`).Scan(&id); err != nil {
		return 0, fmt.Errorf("store: read default tenant: %w", err)
	}
	return id, nil
}

// CreateTenant 创建新租户（P2 测试/后续租户管理使用），返回其 id。
func (s *Store) CreateTenant(slug, name string) (int64, error) {
	res, err := s.db.Exec(`INSERT INTO tenant(slug,name,created_at) VALUES(?,?,?)`, slug, name, now())
	if err != nil {
		return 0, fmt.Errorf("store: create tenant: %w", err)
	}
	return res.LastInsertId()
}

// GetTenantBySlug 按 slug 读租户；不存在返回 sql.ErrNoRows。
func (s *Store) GetTenantBySlug(slug string) (TenantRow, error) {
	var t TenantRow
	err := s.db.QueryRow(`SELECT id,slug,name,created_at FROM tenant WHERE slug=?`, slug).
		Scan(&t.ID, &t.Slug, &t.Name, &t.CreatedAt)
	if err != nil {
		return t, err
	}
	return t, nil
}

// CountUsers 返回用户总数（判定账号模式/首装状态）。
func (s *Store) CountUsers() (int64, error) {
	var n int64
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
		return 0, fmt.Errorf("store: count users: %w", err)
	}
	return n, nil
}

// CreateInitialAdmin 原子首装：确保 default 租户存在，且仅当用户数为 0 时创建首个 admin。
// created=false 表示已有用户（调用方回 409）。事务保证并发首装只会成功一次。
func (s *Store) CreateInitialAdmin(username, name, passwordHash string) (AuthUser, bool, error) {
	var out AuthUser
	tx, err := s.db.Begin()
	if err != nil {
		return out, false, fmt.Errorf("store: begin setup: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(
		`INSERT OR IGNORE INTO tenant(slug,name,created_at) VALUES('default','default',?)`, now()); err != nil {
		return out, false, fmt.Errorf("store: setup tenant: %w", err)
	}
	var tenantID int64
	if err := tx.QueryRow(`SELECT id FROM tenant WHERE slug='default'`).Scan(&tenantID); err != nil {
		return out, false, fmt.Errorf("store: setup read tenant: %w", err)
	}
	res, err := tx.Exec(`
		INSERT INTO users(tenant_id,username,name,role,password_hash,created_at,disabled)
		SELECT ?,?,?,?,?,?,0 WHERE NOT EXISTS (SELECT 1 FROM users)`,
		tenantID, username, name, "admin", passwordHash, now())
	if err != nil {
		return out, false, fmt.Errorf("store: setup admin: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return out, false, fmt.Errorf("store: setup rows: %w", err)
	}
	if n == 0 {
		return out, false, nil // 已有用户：原子判定失败
	}
	id, err := res.LastInsertId()
	if err != nil {
		return out, false, fmt.Errorf("store: setup id: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return out, false, fmt.Errorf("store: setup commit: %w", err)
	}
	out = AuthUser{
		UserRow: UserRow{
			ID: id, TenantID: tenantID, Username: username, Name: name,
			Role: "admin", PasswordHash: passwordHash, CreatedAt: now(),
		},
		TenantSlug: "default",
	}
	return out, true, nil
}

// GetUserByUsername 按用户名读用户（含租户 slug）；不存在返回 sql.ErrNoRows。
func (s *Store) GetUserByUsername(username string) (AuthUser, error) {
	var u AuthUser
	var disabled int
	err := s.db.QueryRow(`
		SELECT u.id,u.tenant_id,u.username,u.name,u.role,u.password_hash,u.created_at,u.disabled,t.slug
		FROM users u JOIN tenant t ON t.id=u.tenant_id WHERE u.username=?`, username).
		Scan(&u.ID, &u.TenantID, &u.Username, &u.Name, &u.Role, &u.PasswordHash,
			&u.CreatedAt, &disabled, &u.TenantSlug)
	if err != nil {
		return u, err
	}
	u.Disabled = disabled != 0
	return u, nil
}

// CreateSession 落一条服务端会话（登录成功调用；每次登录都新 ID，天然防会话固定）。
func (s *Store) CreateSession(id string, userID, expiresAt int64) error {
	ts := now()
	_, err := s.db.Exec(`
		INSERT INTO sessions(id,user_id,created_at,expires_at,last_seen_at) VALUES(?,?,?,?,?)`,
		id, userID, ts, expiresAt, ts)
	if err != nil {
		return fmt.Errorf("store: create session: %w", err)
	}
	return nil
}

// UserBySession 按会话 ID 读有效用户；过期/不存在/用户被禁用一律返回 sql.ErrNoRows。
func (s *Store) UserBySession(id string, at int64) (AuthUser, error) {
	var u AuthUser
	var disabled int
	err := s.db.QueryRow(`
		SELECT u.id,u.tenant_id,u.username,u.name,u.role,u.password_hash,u.created_at,u.disabled,t.slug,s.last_seen_at
		FROM sessions s JOIN users u ON u.id=s.user_id JOIN tenant t ON t.id=u.tenant_id
		WHERE s.id=? AND s.expires_at>?`, id, at).
		Scan(&u.ID, &u.TenantID, &u.Username, &u.Name, &u.Role, &u.PasswordHash,
			&u.CreatedAt, &disabled, &u.TenantSlug, &u.SessionLastSeen)
	if err != nil {
		return u, err
	}
	u.Disabled = disabled != 0
	if u.Disabled {
		return AuthUser{}, sql.ErrNoRows
	}
	return u, nil
}

// TouchSession 刷新会话最近活跃时间（节流调用，避免每请求一次写）。
func (s *Store) TouchSession(id string, lastSeen int64) error {
	_, err := s.db.Exec(
		`UPDATE sessions SET last_seen_at=? WHERE id=? AND expires_at>?`, lastSeen, id, lastSeen)
	if err != nil {
		return fmt.Errorf("store: touch session: %w", err)
	}
	return nil
}

// DeleteSession 删除会话（登出；幂等，不存在也返回 nil）。
func (s *Store) DeleteSession(id string) error {
	if _, err := s.db.Exec(`DELETE FROM sessions WHERE id=?`, id); err != nil {
		return fmt.Errorf("store: delete session: %w", err)
	}
	return nil
}

// PruneSessions 删除过期会话，返回删除行数（保留期维护复用）。
func (s *Store) PruneSessions(before int64) (int64, error) {
	res, err := s.db.Exec(`DELETE FROM sessions WHERE expires_at <= ?`, before)
	if err != nil {
		return 0, fmt.Errorf("store: prune sessions: %w", err)
	}
	return res.RowsAffected()
}
