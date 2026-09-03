package auth

import (
	"crypto/sha256"
	"crypto/subtle"
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

// ConstantTimeEqual 恒时比较两个秘密字符串：先把双方散列成定长摘要，
// 再用 subtle 比较，避免长度早退泄露秘密内容。
func ConstantTimeEqual(a, b string) bool {
	ah := sha256.Sum256([]byte(a))
	bh := sha256.Sum256([]byte(b))
	return subtle.ConstantTimeCompare(ah[:], bh[:]) == 1
}

// TokenOK 校验共享服务令牌：令牌非空且命中 header 或 query，等价 admin。
// 比较走恒定时间，避免 legacy token 被时序侧信道探测。
func TokenOK(r *http.Request, token string) bool {
	if token == "" {
		return false
	}
	return ConstantTimeEqual(BearerToken(r), token) || ConstantTimeEqual(QueryToken(r), token)
}

// IsLoopbackRemote 判断来源是否为回环地址（无凭据写操作的放行条件）。
// 只看真实 TCP 对端（RemoteAddr），不采信任何转发头。
func IsLoopbackRemote(r *http.Request) bool {
	ip := remoteIP(r.RemoteAddr)
	return ip != nil && ip.IsLoopback()
}

// ClientIP 返回客户端 IP：仅当对端命中 trusted proxies 时才解析
// X-Real-IP / X-Forwarded-For；否则一律用 socket 对端 IP（防伪造绕过）。
func ClientIP(r *http.Request, proxies *TrustedProxies) string {
	ip := remoteIP(r.RemoteAddr)
	if ip == nil {
		return r.RemoteAddr
	}
	if !proxies.TrustsRemote(r.RemoteAddr) {
		return ip.String()
	}
	if xr := strings.TrimSpace(r.Header.Get("X-Real-IP")); xr != "" {
		if xip := net.ParseIP(xr); xip != nil {
			return xip.String()
		}
	}
	return clientFromForwardedFor(r.Header.Get("X-Forwarded-For"), proxies, ip.String())
}

// clientFromForwardedFor 从可信代理链右向左找第一个不可信地址，防客户端
// 自加可信 IP 伪造链头。
func clientFromForwardedFor(xff string, proxies *TrustedProxies, fallback string) string {
	parts := strings.Split(xff, ",")
	for i := len(parts) - 1; i >= 0; i-- {
		raw := strings.TrimSpace(parts[i])
		if raw == "" {
			continue
		}
		ip := remoteIP(raw)
		if ip == nil {
			continue
		}
		if !proxies.Contains(ip) {
			return ip.String()
		}
	}
	return fallback
}
