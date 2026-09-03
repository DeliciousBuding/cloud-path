// Package transport defines the transport abstraction every handwritten RPC
// protocol sits on.
//
// The in-tree implementation is an in-process memory pipe used by tests and
// the conformance harness. Real gRPC / named pipe / Unix socket transports
// belong to the A4 Plugin Host and only need to satisfy Transport here; the
// service client/server layers in sdk/go/cloudpath/v1/* are transport-agnostic.
//
// The Message envelope is intentionally minimal and language-neutral: every
// RPC call gets a CallID and, when it starts a stream, a Method. Body carries
// the JSON-marshaled service message exactly as the handwritten Go structs
// encode it.
package transport

import (
	"context"
	"errors"
	"io"
	"sync"

	"github.com/DeliciousBuding/cloud-path/sdk/go/cloudpath/v1/status"
)

// Frame kinds. Keep in sync with sdk/go/rpc aliases.
const (
	KindUnary         uint8 = 1 // unary request; expects exactly one terminal frame
	KindResponse      uint8 = 2 // terminal unary response (Body set, Err nil)
	KindStreamReq     uint8 = 3 // open a stream; expects KindStreamMsg* then terminal frame
	KindStreamMsg     uint8 = 4 // stream message in either direction
	KindStreamEnd     uint8 = 5 // half-close from the sender; receiver keeps its send side
	KindStreamEndTerm uint8 = 6 // terminal frame closing a stream for both sides
	KindStreamCancel  uint8 = 7 // client cancels the whole call
)

// Message is the wire envelope carried by a Transport.
type Message struct {
	CallID uint64         `json:"call_id"`
	Kind   uint8          `json:"kind"`
	Method string         `json:"method,omitempty"`
	Body   []byte         `json:"body,omitempty"`
	Err    *status.Status `json:"error,omitempty"`
}

// ErrClosed is returned by Send/Recv once the transport is closed.
var ErrClosed = errors.New("cloudpath transport: closed")

// Transport is a duplex, ordered, backpressured message channel. It carries
// one RPC connection between a plugin process and its host. Implementations
// may be named pipes, Unix sockets, loopback TCP, or the in-memory pipe in
// this package; the protocol layers must not depend on which one is used.
type Transport interface {
	// Send delivers one message. It must block while the peer's receive
	// buffer is full (backpressure) and return ctx.Err on cancellation or
	// ErrClosed after Close.
	Send(ctx context.Context, m Message) error
	// Recv returns the next message in order, blocks when none is queued,
	// and returns io.EOF or ErrClosed once the transport shuts down.
	Recv(ctx context.Context) (Message, error)
	// Close tears the transport down and unblocks both directions.
	Close() error
}

// Pipe returns two ends of an in-process memory transport with the given
// per-direction queue capacity. Frames are ordered and never dropped:
// a full queue makes Send block, which is what lets callers test
// backpressure deterministically.
func Pipe(capacity int) (Transport, Transport) {
	if capacity < 1 {
		capacity = 1
	}
	a := &memTransport{recv: make(chan Message, capacity), done: make(chan struct{})}
	b := &memTransport{recv: make(chan Message, capacity), done: make(chan struct{})}
	a.send, b.send = b.recv, a.recv
	return a, b
}

type memTransport struct {
	recv      chan Message
	send      chan Message
	closeOnce sync.Once
	done      chan struct{}
}

func (m *memTransport) Send(ctx context.Context, msg Message) error {
	select {
	case m.send <- msg:
		return nil
	case <-m.done:
		return ErrClosed
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *memTransport) Recv(ctx context.Context) (Message, error) {
	select {
	case msg, ok := <-m.recv:
		if !ok {
			return Message{}, io.EOF
		}
		return msg, nil
	case <-m.done:
		// Drain any already-queued messages first so a graceful Close is not
		// lossy for the receiving side.
		select {
		case msg, ok := <-m.recv:
			if !ok {
				return Message{}, io.EOF
			}
			return msg, nil
		default:
		}
		return Message{}, ErrClosed
	case <-ctx.Done():
		return Message{}, ctx.Err()
	}
}

func (m *memTransport) Close() error {
	m.closeOnce.Do(func() { close(m.done) })
	return nil
}
