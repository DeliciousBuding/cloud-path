package auth

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// SessionCookieName 是服务端会话 cookie 名（契约 §2.1：cp_session）。
const SessionCookieName = "cp_session"

// NewSessionID 生成 256-bit 随机会话 ID（base64url，无填充）。
func NewSessionID() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("auth: session id: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// SetSessionCookie 写 cp_session cookie：HttpOnly + SameSite=Lax；真实 TLS 或
// 可信反代声明的 https 下加 Secure。maxAge 为 TTL 秒（每次登录新 ID，防固定）。
func SetSessionCookie(w http.ResponseWriter, r *http.Request, sessionID string, maxAge int, proxies *TrustedProxies) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    sessionID,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   IsSecureRequest(r, proxies),
	})
}

// ClearSessionCookie 清 cookie（登出/会话失效）。
func ClearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		Expires:  time.Unix(1, 0),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

// IsSecureRequest 判断请求是否走 TLS：真实 TLS，或可信反代声明的
// X-Forwarded-Proto=https。未命中可信反代时伪造 proto 头一律忽略。
func IsSecureRequest(r *http.Request, proxies *TrustedProxies) bool {
	if r.TLS != nil {
		return true
	}
	if !proxies.TrustsRemote(r.RemoteAddr) {
		return false
	}
	return strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}
