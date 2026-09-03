// Package pluginruntime provides the production local socket transport that
// binds a plugin process to its host for one launch, plus the handshake and
// RPC bridges used by the A4 Plugin Host and SDK service layers.
//
// The first Windows version uses loopback TCP on a random port; Linux/macOS
// use a Unix domain socket. The wire format is a length-prefixed JSON
// transport.Message stream. Before any RPC frame is accepted, the dialing
// side sends a single authentication frame carrying the launch id and proof;
// the serving side validates it in constant time and never passes a bad
// proof through to an RPC handler.
package pluginruntime

import (
	"crypto/subtle"
	"errors"
	"time"
)

// Default transport limits. These are deliberately small enough to bound
// memory and idle time but generous enough for a local plugin connection.
const (
	DefaultMaxFrameSize     = 4 << 20 // 4 MiB
	DefaultMaxConnections   = 64
	DefaultHandshakeTimeout = 5 * time.Second
	DefaultReadTimeout      = 30 * time.Second
	DefaultWriteTimeout     = 30 * time.Second
)

var (
	// ErrAuthenticationFailed reports a rejected launch identity. It is
	// intentionally generic: the proof must never be echoed back.
	ErrAuthenticationFailed = errors.New("pluginruntime: authentication failed")
	// ErrFrameTooLarge reports a frame over Config.MaxFrameSize.
	ErrFrameTooLarge = errors.New("pluginruntime: frame exceeds limit")
)

// Config bounds the socket transport. A zero value is not valid; callers use
// DefaultConfig and override the fields they need.
type Config struct {
	// MaxFrameSize is the maximum JSON frame payload in bytes, both directions.
	MaxFrameSize int
	// MaxConnections bounds concurrent accepted connections on a listener.
	MaxConnections int
	// HandshakeTimeout bounds the first authentication frame exchange.
	HandshakeTimeout time.Duration
	// ReadTimeout is the per-frame read deadline on an established connection.
	ReadTimeout time.Duration
	// WriteTimeout is the per-frame write deadline on an established connection.
	WriteTimeout time.Duration
}

// DefaultConfig returns production defaults.
func DefaultConfig() Config {
	return Config{
		MaxFrameSize:     DefaultMaxFrameSize,
		MaxConnections:   DefaultMaxConnections,
		HandshakeTimeout: DefaultHandshakeTimeout,
		ReadTimeout:      DefaultReadTimeout,
		WriteTimeout:     DefaultWriteTimeout,
	}
}

func (c Config) withDefaults() Config {
	if c.MaxFrameSize <= 0 {
		c.MaxFrameSize = DefaultMaxFrameSize
	}
	if c.MaxConnections <= 0 {
		c.MaxConnections = DefaultMaxConnections
	}
	if c.HandshakeTimeout <= 0 {
		c.HandshakeTimeout = DefaultHandshakeTimeout
	}
	if c.ReadTimeout <= 0 {
		c.ReadTimeout = DefaultReadTimeout
	}
	if c.WriteTimeout <= 0 {
		c.WriteTimeout = DefaultWriteTimeout
	}
	return c
}

// Credentials is the per-launch identity shared by host and plugin through the
// process environment. Proof is treated as an opaque secret: it is compared in
// constant time and never logged.
type Credentials struct {
	LaunchID string
	Proof    string
}

// Validate reports whether both fields are present.
func (c Credentials) Validate() error {
	if c.LaunchID == "" {
		return errors.New("pluginruntime: empty launch id")
	}
	if c.Proof == "" {
		return errors.New("pluginruntime: empty proof")
	}
	return nil
}

// authFrame is the first frame written by the dialing side.
type authFrame struct {
	LaunchID string `json:"launch_id"`
	Proof    string `json:"proof"`
}

func (c Credentials) verify(a authFrame) bool {
	if subtle.ConstantTimeCompare([]byte(c.LaunchID), []byte(a.LaunchID)) != 1 {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(c.Proof), []byte(a.Proof)) == 1
}
