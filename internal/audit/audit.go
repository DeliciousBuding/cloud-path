// Package audit 是租户级、append-only、不可泄密的安全审计日志域。
// 只承载：事件/动作常量、结构化 metadata builder（敏感 key 自动 [REDACTED]）、
// request id 归一化与生成。持久化由 internal/store 负责，落点由 internal/server 编排。
package audit

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"strings"
)

// 动作常量：均为 "<域>.<动作>"，可查询、可扩展。
const (
	ActionSetup             = "auth.setup"
	ActionLogin             = "auth.login"
	ActionLogout            = "auth.logout"
	ActionUserCreate        = "user.create"
	ActionUserUpdate        = "user.update"
	ActionUserDisable       = "user.disable"
	ActionUserEnable        = "user.enable"
	ActionUserPasswordReset = "user.password_reset"
	ActionTokenCreate       = "token.create"
	ActionTokenRevoke       = "token.revoke"
	ActionCommandAccepted   = "command.accepted"
	ActionCommandRejected   = "command.rejected"
	ActionEdgeAuthSuccess   = "edge.auth.success"
	ActionEdgeAuthFailure   = "edge.auth.failure"
)

// 结果常量。
const (
	OutcomeSuccess = "success"
	OutcomeFailure = "failure"
)

// actor / target 类型常量。
const (
	ActorSystem = "system"
	ActorUser   = "user"
	ActorToken  = "token"
	ActorEdge   = "edge"

	TargetTenant = "tenant"
	TargetUser   = "user"
	TargetToken  = "token"
	TargetDevice = "device"
	TargetEdge   = "edge"
)

// Event 是一次待落库的审计事件（不含任何明文凭据）。
type Event struct {
	TenantID   int64
	ActorType  string
	ActorID    int64
	ActorName  string
	Action     string
	TargetType string
	TargetID   string
	Outcome    string
	RequestID  string
	RemoteIP   string
	Metadata   map[string]any
}

// Entry 是审计事件的可查询/JSON 视图（查询端点透出用）。
type Entry struct {
	ID         int64          `json:"id"`
	TenantID   int64          `json:"tenant_id"`
	ActorType  string         `json:"actor_type"`
	ActorID    int64          `json:"actor_id"`
	ActorName  string         `json:"actor_name"`
	Action     string         `json:"action"`
	TargetType string         `json:"target_type"`
	TargetID   string         `json:"target_id"`
	Outcome    string         `json:"outcome"`
	RequestID  string         `json:"request_id"`
	RemoteIP   string         `json:"remote_ip"`
	Metadata   map[string]any `json:"metadata"`
	CreatedAt  int64          `json:"created_at"`
}

// Redacted 是敏感 metadata 值的统一占位符。
const Redacted = "[REDACTED]"

// sensitiveKey 判定 key 是否命中敏感关键字（子串匹配，大小写不敏感）。
func sensitiveKey(key string) bool {
	k := strings.ToLower(key)
	for _, s := range []string{"password", "token", "secret", "cookie", "authorization", "session"} {
		if strings.Contains(k, s) {
			return true
		}
	}
	return false
}

// Metadata 是结构化 metadata builder：敏感 key 的值一律替换为 [REDACTED]。
// 调用方只允许通过类型化方法写入，禁止把请求原 body/header 整段塞入。
type Metadata struct {
	m map[string]any
}

func NewMetadata() *Metadata { return &Metadata{m: map[string]any{}} }

func (m *Metadata) String(key, val string) *Metadata    { m.put(key, val); return m }
func (m *Metadata) Int(key string, val int64) *Metadata { m.put(key, val); return m }
func (m *Metadata) Bool(key string, val bool) *Metadata { m.put(key, val); return m }

func (m *Metadata) put(key string, val any) {
	if sensitiveKey(key) {
		m.m[key] = Redacted
		return
	}
	m.m[key] = val
}

// Map 返回已构建的 metadata 映射（只读使用）。
func (m *Metadata) Map() map[string]any { return m.m }

// JSON 把 metadata 序列化为 JSON 对象；失败回落 "{}"。
func (m *Metadata) JSON() string {
	if len(m.m) == 0 {
		return "{}"
	}
	b, err := json.Marshal(m.m)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// MaxRequestIDLen 是请求方传入 X-Request-ID 的长度上限。
const MaxRequestIDLen = 128

// NormalizeRequestID 校验并规范化请求方传入的 X-Request-ID：
// 允许 [A-Za-z0-9._-]，长度 1..MaxRequestIDLen；非法返回空串（调用方回退自生成）。
func NormalizeRequestID(in string) string {
	in = strings.TrimSpace(in)
	if in == "" || len(in) > MaxRequestIDLen {
		return ""
	}
	for i := 0; i < len(in); i++ {
		c := in[i]
		ok := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') || c == '-' || c == '_' || c == '.'
		if !ok {
			return ""
		}
	}
	return in
}

// NewRequestID 生成服务端请求 ID（req- + 16 字节随机 hex）。
func NewRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "req-fallback"
	}
	return "req-" + hex.EncodeToString(b[:])
}

type ctxKeyRequestID struct{}

// WithRequestID 把请求 ID 挂进 context。
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxKeyRequestID{}, id)
}

// RequestID 从 context 读取请求 ID；无则空串。
func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(ctxKeyRequestID{}).(string)
	return id
}
