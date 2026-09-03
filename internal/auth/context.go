package auth

import "context"

// Principal 是解析后的请求身份：会话用户或服务令牌（等价 admin）。
type Principal struct {
	UserID     int64
	Username   string
	Name       string
	Role       string
	TenantID   int64
	TenantSlug string
	Token      bool // 服务令牌身份（无会话行）
}

type principalKey struct{}

// WithPrincipal 把解析出的身份挂进请求 context。
func WithPrincipal(ctx context.Context, p *Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

// FromContext 取请求身份；未认证返回 nil。
func FromContext(ctx context.Context) *Principal {
	p, _ := ctx.Value(principalKey{}).(*Principal)
	return p
}
