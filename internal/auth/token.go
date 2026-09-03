package auth

import (
	"net"
	"net/http"
	"strings"
)

// BearerToken 提取 Authorization: Bearer <token>；无则空串。
func BearerToken(r *http.Request) string {
	return strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
}

// QueryToken 提取 ?token=（WS 等无法自定义 header 的场景）。
func QueryToken(r *http.Request) string {
	return r.URL.Query().Get("token")
}

// TokenOK 校验共享服务令牌：令牌非空且命中 header 或 query，等价 admin。
func TokenOK(r *http.Request, token string) bool {
	if token == "" {
		return false
	}
	return BearerToken(r) == token || QueryToken(r) == token
}

// IsLoopbackRemote 判断来源是否为回环地址（无凭据写操作的放行条件）。
func IsLoopbackRemote(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// ClientIP 返回客户端 IP：优先 X-Forwarded-For 首项（反代后），否则取 RemoteAddr。
func ClientIP(r *http.Request) string {
	if xff := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); xff != "" {
		if i := strings.IndexByte(xff, ','); i > 0 {
			xff = xff[:i]
		}
		return strings.TrimSpace(xff)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}
