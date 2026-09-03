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

// SetSessionCookie 写 cp_session cookie：HttpOnly + SameSite=Lax；反代 TLS 下加 Secure。
// maxAge 为 TTL 秒（每次登录新 ID，天然防会话固定）。
func SetSessionCookie(w http.ResponseWriter, r *http.Request, sessionID string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    sessionID,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   IsSecureRequest(r),
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

// IsSecureRequest 判断请求是否走 TLS：直连 TLS 或反代 X-Forwarded-Proto=https。
func IsSecureRequest(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}
