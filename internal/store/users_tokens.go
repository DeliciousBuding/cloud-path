package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// 用户/令牌管理的哨兵错误（docs/api.md §3.2/§3.3 语义）。
var (
	// ErrUsernameTaken 表示 username 已存在（租户内唯一，handler 回 409）。
	ErrUsernameTaken = errors.New("store: username already taken")
	// ErrLastAdmin 表示目标用户是最后一个可用 admin，禁止禁用/降级（handler 回 409）。
	ErrLastAdmin = errors.New("store: cannot disable or demote the last admin")
	// ErrInvalidRole 表示角色不在 admin|operator|viewer 内。
	ErrInvalidRole = errors.New("store: invalid role")
)

// TokenRow 是 tenant_tokens 表一行。明文只在创建响应返回，本结构不含明文。
type TokenRow struct {
	ID         int64
	TenantID   int64
	Name       string
	Prefix     string
	Hash       string
	Scopes     string // JSON 数组
	ExpiresAt  sql.NullInt64
	LastUsedAt sql.NullInt64
	RevokedAt  sql.NullInt64
	CreatedAt  int64
}

// UserPatch 是 PATCH /api/users/{id} 的可选字段集（nil=不改）。
type UserPatch struct {
	Name     *string
	Role     *string
	Password *string // 已哈希的 argon2id；设置即重置并撤销全部会话
	Disabled *bool
}

func validRole(role string) bool {
	return role == "admin" || role == "operator" || role == "viewer"
}

func scanAuthUser(row interface{ Scan(...any) error }) (AuthUser, error) {
	var u AuthUser
	var disabled int
	err := row.Scan(&u.ID, &u.TenantID, &u.Username, &u.Name, &u.Role, &u.PasswordHash,
		&u.CreatedAt, &disabled, &u.TenantSlug)
	if err != nil {
		return u, err
	}
	u.Disabled = disabled != 0
	return u, nil
}

const authUserColumns = `u.id, u.tenant_id, u.username, u.name, u.role, u.password_hash, u.created_at, u.disabled, t.slug`

// ListUsersTenant 返回指定租户的全部用户（不含密码哈希之外的敏感信息；handler 只透出视图）。
func (s *Store) ListUsersTenant(tenantID int64) ([]AuthUser, error) {
	rows, err := s.db.Query(
		`SELECT `+authUserColumns+` FROM users u JOIN tenant t ON t.id=u.tenant_id WHERE u.tenant_id=? ORDER BY u.id`,
		tenantID)
	if err != nil {
		return nil, fmt.Errorf("store: list users: %w", err)
	}
	defer rows.Close()
	var out []AuthUser
	for rows.Next() {
		u, err := scanAuthUser(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan user: %w", err)
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// GetUserByID 按 id 读用户（含租户 slug）；不存在返回 sql.ErrNoRows。
func (s *Store) GetUserByID(id int64) (AuthUser, error) {
	u, err := scanAuthUser(s.db.QueryRow(
		`SELECT `+authUserColumns+` FROM users u JOIN tenant t ON t.id=u.tenant_id WHERE u.id=?`, id))
	if err != nil {
		return u, err
	}
	return u, nil
}

// CreateUser 在租户内创建用户；username 冲突返回 ErrUsernameTaken。
func (s *Store) CreateUser(tenantID int64, username, name, role, passwordHash string) (AuthUser, error) {
	if !validRole(role) {
		return AuthUser{}, ErrInvalidRole
	}
	res, err := s.db.Exec(`
		INSERT INTO users(tenant_id,username,name,role,password_hash,created_at,disabled)
		VALUES(?,?,?,?,?,?,0)`,
		tenantID, username, name, role, passwordHash, now())
	if err != nil {
		if isUsernameTaken(err) {
			return AuthUser{}, ErrUsernameTaken
		}
		return AuthUser{}, fmt.Errorf("store: create user: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return AuthUser{}, fmt.Errorf("store: create user id: %w", err)
	}
	u, err := s.GetUserByID(id)
	if err != nil {
		return AuthUser{}, fmt.Errorf("store: read created user: %w", err)
	}
	return u, nil
}

// UpdateUser 修改用户字段并在同一事务内做「最后一个可用 admin」保护。
// 禁用或重置密码会撤销该用户全部会话。目标用户不存在或跨租户返回 sql.ErrNoRows。
func (s *Store) UpdateUser(tenantID, id int64, patch UserPatch) (AuthUser, error) {
	if patch.Role != nil && !validRole(*patch.Role) {
		return AuthUser{}, ErrInvalidRole
	}
	tx, err := s.db.Begin()
	if err != nil {
		return AuthUser{}, fmt.Errorf("store: begin update user: %w", err)
	}
	defer tx.Rollback()

	cur, err := scanAuthUser(tx.QueryRow(
		`SELECT `+authUserColumns+` FROM users u JOIN tenant t ON t.id=u.tenant_id WHERE u.id=? AND u.tenant_id=?`,
		id, tenantID))
	if err != nil {
		return AuthUser{}, err // 含 sql.ErrNoRows
	}

	name := cur.Name
	role := cur.Role
	disabled := cur.Disabled
	hash := cur.PasswordHash
	if patch.Name != nil {
		name = *patch.Name
	}
	if patch.Role != nil {
		role = *patch.Role
	}
	if patch.Disabled != nil {
		disabled = *patch.Disabled
	}
	if patch.Password != nil {
		hash = *patch.Password
	}

	// 最后一个可用 admin 保护：当前是启用中的 admin，且本次会失去 admin 身份（禁用或降级）。
	if cur.Role == "admin" && !cur.Disabled && (disabled || role != "admin") {
		var n int64
		if err := tx.QueryRow(
			`SELECT COUNT(*) FROM users WHERE tenant_id=? AND role='admin' AND disabled=0 AND id<>?`,
			cur.TenantID, id).Scan(&n); err != nil {
			return AuthUser{}, fmt.Errorf("store: count admins: %w", err)
		}
		if n == 0 {
			return AuthUser{}, ErrLastAdmin
		}
	}

	if _, err := tx.Exec(
		`UPDATE users SET name=?, role=?, disabled=?, password_hash=? WHERE id=? AND tenant_id=?`,
		name, role, boolToInt(disabled), hash, id, tenantID); err != nil {
		return AuthUser{}, fmt.Errorf("store: update user: %w", err)
	}
	// 禁用或密码重置 → 撤销全部会话（docs/api.md §3.1）。
	if disabled || patch.Password != nil {
		if _, err := tx.Exec(`DELETE FROM sessions WHERE user_id=?`, id); err != nil {
			return AuthUser{}, fmt.Errorf("store: revoke sessions: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return AuthUser{}, fmt.Errorf("store: commit update user: %w", err)
	}
	cur.Name, cur.Role, cur.Disabled, cur.PasswordHash = name, role, disabled, hash
	return cur, nil
}

// CountEnabledAdmins 返回租户内启用中的 admin 数（保护与测试用）。
func (s *Store) CountEnabledAdmins(tenantID int64) (int64, error) {
	var n int64
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM users WHERE tenant_id=? AND role='admin' AND disabled=0`, tenantID).Scan(&n); err != nil {
		return 0, fmt.Errorf("store: count admins: %w", err)
	}
	return n, nil
}

// DeleteSessionsByUser 撤销用户全部会话，返回删除行数（幂等）。
func (s *Store) DeleteSessionsByUser(userID int64) (int64, error) {
	res, err := s.db.Exec(`DELETE FROM sessions WHERE user_id=?`, userID)
	if err != nil {
		return 0, fmt.Errorf("store: revoke sessions: %w", err)
	}
	return res.RowsAffected()
}

// CreateTenantToken 落一条租户服务令牌（只存 hash/prefix/scopes）。
func (s *Store) CreateTenantToken(tenantID int64, name, hash, prefix, scopesJSON string, expiresAt *int64) (TokenRow, error) {
	var exp any
	if expiresAt != nil {
		exp = *expiresAt
	}
	res, err := s.db.Exec(`
		INSERT INTO tenant_tokens(tenant_id,name,prefix,hash,scopes,expires_at,created_at)
		VALUES(?,?,?,?,?,?,?)`,
		tenantID, name, prefix, hash, scopesJSON, exp, now())
	if err != nil {
		return TokenRow{}, fmt.Errorf("store: create tenant token: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return TokenRow{}, fmt.Errorf("store: create tenant token id: %w", err)
	}
	return s.GetTenantToken(id, tenantID)
}

// ListTenantTokens 返回租户全部令牌元数据（无明文）。
func (s *Store) ListTenantTokens(tenantID int64) ([]TokenRow, error) {
	rows, err := s.db.Query(`
		SELECT id,tenant_id,name,prefix,hash,scopes,expires_at,last_used_at,revoked_at,created_at
		FROM tenant_tokens WHERE tenant_id=? ORDER BY id`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("store: list tenant tokens: %w", err)
	}
	defer rows.Close()
	var out []TokenRow
	for rows.Next() {
		r, err := scanToken(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan token: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetTenantToken 按 id+租户读令牌（handler 校验归属用）。
func (s *Store) GetTenantToken(id, tenantID int64) (TokenRow, error) {
	return scanToken(s.db.QueryRow(`
		SELECT id,tenant_id,name,prefix,hash,scopes,expires_at,last_used_at,revoked_at,created_at
		FROM tenant_tokens WHERE id=? AND tenant_id=?`, id, tenantID))
}

// GetTenantTokenByHash 按 hash 读令牌（鉴权路径）；不存在返回 sql.ErrNoRows。
func (s *Store) GetTenantTokenByHash(hash string) (TokenRow, error) {
	return scanToken(s.db.QueryRow(`
		SELECT id,tenant_id,name,prefix,hash,scopes,expires_at,last_used_at,revoked_at,created_at
		FROM tenant_tokens WHERE hash=?`, hash))
}

// RevokeTenantToken 吊销租户内令牌（幂等语义由 handler 决定 404/204），返回是否命中。
func (s *Store) RevokeTenantToken(id, tenantID int64) (bool, error) {
	res, err := s.db.Exec(
		`UPDATE tenant_tokens SET revoked_at=? WHERE id=? AND tenant_id=? AND revoked_at IS NULL`,
		now(), id, tenantID)
	if err != nil {
		return false, fmt.Errorf("store: revoke token: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("store: revoke token rows: %w", err)
	}
	if n > 0 {
		return true, nil
	}
	// 命中但已吊销 → 仍算存在（幂等 204）；完全不存在 → sql.ErrNoRows。
	_, err = s.GetTenantToken(id, tenantID)
	if err != nil {
		return false, err
	}
	return true, nil
}

// TouchTenantToken 节流刷新 last_used_at（避免每请求一次写库）。
func (s *Store) TouchTenantToken(id, at int64) error {
	_, err := s.db.Exec(
		`UPDATE tenant_tokens SET last_used_at=? WHERE id=? AND (last_used_at IS NULL OR last_used_at < ?)`,
		at, id, at-60)
	if err != nil {
		return fmt.Errorf("store: touch token: %w", err)
	}
	return nil
}

func scanToken(row interface{ Scan(...any) error }) (TokenRow, error) {
	var t TokenRow
	err := row.Scan(&t.ID, &t.TenantID, &t.Name, &t.Prefix, &t.Hash, &t.Scopes,
		&t.ExpiresAt, &t.LastUsedAt, &t.RevokedAt, &t.CreatedAt)
	return t, err
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func isUsernameTaken(err error) bool {
	return err != nil && strings.Contains(err.Error(), "users.username")
}
