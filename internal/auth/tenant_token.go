package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
)

// TenantTokenPrefix 是租户服务令牌的固定前缀（docs/api.md §3.3）。
const TenantTokenPrefix = "cp_"

// tenantTokenRandomBytes 是令牌随机数据长度（契约：>=32 字节）。
const tenantTokenRandomBytes = 32

// NewTenantToken 生成 `cp_` + 32 字节随机数据的租户服务令牌。
// 返回明文（仅在创建响应返回一次）、SHA-256 十六进制 hash 与短 prefix。
func NewTenantToken() (plain, hash, prefix string, err error) {
	b := make([]byte, tenantTokenRandomBytes)
	if _, err := rand.Read(b); err != nil {
		return "", "", "", fmt.Errorf("auth: tenant token: %w", err)
	}
	plain = TenantTokenPrefix + base64.RawURLEncoding.EncodeToString(b)
	hash = HashToken(plain)
	prefix = plain
	if len(prefix) > 11 { // cp_ + 8 位随机数据，列表展示用
		prefix = prefix[:11]
	}
	return plain, hash, prefix, nil
}

// HashToken 返回 token 的 SHA-256 十六进制 hash（数据库只存 hash）。
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// VerifyTokenHash 常数时间比较两个 token hash。
func VerifyTokenHash(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// IsTenantToken 判断 token 是否为租户令牌形状（cp_ 前缀）。
func IsTenantToken(token string) bool {
	return strings.HasPrefix(token, TenantTokenPrefix)
}

// RoleRank 返回角色权限等级：admin=3 > operator=2 > viewer=1 > 未知=0。
func RoleRank(role string) int {
	switch role {
	case "admin":
		return 3
	case "operator":
		return 2
	case "viewer":
		return 1
	default:
		return 0
	}
}

// RoleAllows 判断角色是否满足所需角色（admin>operator>viewer 层级）。
func RoleAllows(role, need string) bool {
	return RoleRank(role) >= RoleRank(need)
}

// ScopeRole 把租户令牌 scopes 映射为等价角色。
// read→viewer、write→operator、admin→admin；edge 不授予 REST 角色。
// 空集合或仅 edge → ""（无 REST 权限）。
func ScopeRole(scopes []string) string {
	has := map[string]bool{}
	for _, s := range scopes {
		has[s] = true
	}
	switch {
	case has["admin"]:
		return "admin"
	case has["write"]:
		return "operator"
	case has["read"]:
		return "viewer"
	default:
		return ""
	}
}

// HasScope 判断 scopes 是否包含目标 scope。
func HasScope(scopes []string, target string) bool {
	for _, s := range scopes {
		if s == target {
			return true
		}
	}
	return false
}
