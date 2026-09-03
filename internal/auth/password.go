// Package auth 提供 Cloudpath 的鉴权原语：argon2id 密码哈希、服务端会话 cookie、
// 服务令牌校验、登录限流与请求身份（Principal）解析（docs/api.md §1/§2）。
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// argon2id 参数（契约要求写成实现常量；Verify 只接受与之一致的哈希，
// 避免把攻击者可控的 m/t 交给 argon2 造成内存/CPU DoS）。
const (
	Argon2Time    uint32 = 3
	Argon2Memory  uint32 = 64 * 1024 // KiB
	Argon2Threads uint8  = 2
	Argon2KeyLen  uint32 = 32
	argon2SaltLen        = 16
)

// argon2Prefix 是自描述哈希前缀：$argon2id$v=19$m=65536,t=3,p=2$
var argon2Prefix = fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$",
	argon2.Version, Argon2Memory, Argon2Time, Argon2Threads)

// dummyPasswordHash 是未知用户登录时执行的固定假哈希：与真实 VerifyPassword
// 走同一 argon2id 参数，保证未知用户与已知用户的耗时同量级，收敛用户名枚举时序。
var dummyPasswordHash = func() string {
	salt := make([]byte, argon2SaltLen)
	copy(salt, "cloudpath-dummy-salt")
	key := argon2.IDKey([]byte("cloudpath-dummy-password"), salt, Argon2Time, Argon2Memory, Argon2Threads, Argon2KeyLen)
	return argon2Prefix +
		base64.RawStdEncoding.EncodeToString(salt) + "$" +
		base64.RawStdEncoding.EncodeToString(key)
}()

// HashPassword 生成可持久化的 argon2id 哈希（含参数与盐，自描述，永不落明文）。
func HashPassword(password string) (string, error) {
	salt := make([]byte, argon2SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("auth: salt: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, Argon2Time, Argon2Memory, Argon2Threads, Argon2KeyLen)
	return argon2Prefix +
		base64.RawStdEncoding.EncodeToString(salt) + "$" +
		base64.RawStdEncoding.EncodeToString(key), nil
}

// VerifyPassword 常数时间校验明文密码与哈希。格式/参数不合法一律 false。
func VerifyPassword(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	// $argon2id$v=19$m=65536,t=3,p=2$salt$hash
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false
	}
	var m, t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil {
		return false
	}
	if m != Argon2Memory || t != Argon2Time || p != Argon2Threads {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, t, m, p, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// DummyVerify 对未知用户执行一次完整 Argon2id 派生，结果丢弃。
// 登录路径对不存在的用户名也必须烧掉等量 CPU，避免通过响应时延枚举用户名。
func DummyVerify(password string) {
	VerifyPassword(dummyPasswordHash, password)
}
