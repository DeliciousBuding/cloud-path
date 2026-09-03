// Package rpc is a small, handwritten RPC framing layer for the in-process
// transport. It deliberately contains no protobuf generated code and no
// gRPC: service stubs in sdk/go/cloudpath/v1/* build on Client and Server
// here, and the A4 Plugin Host can swap the underlying transport.Transport
// for a named pipe or Unix socket without touching service contracts.
//
// Frame semantics (see transport.Message):
//
//	client -> server   KindUnary      one request, exactly one terminal reply
//	server -> client   KindResponse   terminal unary reply (Err set on failure)
//	client -> server   KindStreamReq  open a server stream
//	either direction   KindStreamMsg  stream payload
//	client -> server   KindStreamEnd  half-close of the client send side
//	server -> client   KindStreamEndTerm server finished the stream (both sides)
//	client -> server   KindStreamCancel cancel the whole call
package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"

	"github.com/DeliciousBuding/cloud-path/sdk/go/cloudpath/v1/status"
	"github.com/DeliciousBuding/cloud-path/sdk/go/transport"
)

// Frame kinds mirrored from the transport package for call-site readability.
const (
	KindUnary         = transport.KindUnary
	KindResponse      = transport.KindResponse
	KindStreamReq     = transport.KindStreamReq
	KindStreamMsg     = transport.KindStreamMsg
	KindStreamEnd     = transport.KindStreamEnd
	KindStreamEndTerm = transport.KindStreamEndTerm
	KindStreamCancel  = transport.KindStreamCancel
)

const streamBuffer = 256

// Client is the connection-scoped RPC client. One Client multiplexes many
// unary calls and streams over one Transport and is safe for concurrent use.
type Client struct {
	tr transport.Transport

	mu       sync.Mutex
	nextID   uint64
	closed   bool
	closeErr error
	inflight map[uint64]chan transport.Message
	streams  map[uint64]*ClientStream
	done     chan struct{}
}

// NewClient wraps tr and starts the receive loop.
func NewClient(tr transport.Transport) *Client {
	c := &Client{
		tr:       tr,
		inflight: make(map[uint64]chan transport.Message),
		streams:  make(map[uint64]*ClientStream),
		done:     make(chan struct{}),
	}
	go c.recvLoop()
	return c
}

// Call performs a unary request. resp data is JSON-marshaled; the returned
// bytes are the JSON-marshaled response body.
func (c *Client) Call(ctx context.Context, method string, req any) ([]byte, error) {
	body, err := marshal(req)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	if c.closed {
		err := c.closeErr
		c.mu.Unlock()
		return nil, err
	}
	c.nextID++
	id := c.nextID
	ch := make(chan transport.Message, 1)
	c.inflight[id] = ch
	c.mu.Unlock()

	if err := c.tr.Send(ctx, transport.Message{CallID: id, Kind: KindUnary, Method: method, Body: body}); err != nil {
		c.mu.Lock()
		delete(c.inflight, id)
		c.mu.Unlock()
		return nil, sendError(err)
	}

	select {
	case m := <-ch:
		if m.Err != nil {
			return nil, m.Err
		}
		return m.Body, nil
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.inflight, id)
		c.mu.Unlock()
		return nil, ctx.Err()
	}
}

// OpenStream starts a server-streaming (or bidi) call.
func (c *Client) OpenStream(ctx context.Context, method string, req any) (*ClientStream, error) {
	body, err := marshal(req)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	if c.closed {
		err := c.closeErr
		c.mu.Unlock()
		return nil, err
	}
	c.nextID++
	id := c.nextID
	s := &ClientStream{c: c, id: id, recv: make(chan transport.Message, streamBuffer)}
	c.streams[id] = s
	c.mu.Unlock()

	if err := c.tr.Send(ctx, transport.Message{CallID: id, Kind: KindStreamReq, Method: method, Body: body}); err != nil {
		s.terminate(nil)
		return nil, sendError(err)
	}
	return s, nil
}

// ClientStream is the client side of one stream call.
type ClientStream struct {
	c    *Client
	id   uint64
	mu   sync.Mutex
	recv chan transport.Message
	done bool
}

// Send pushes one payload (bidi streams only).
func (s *ClientStream) Send(ctx context.Context, msg any) error {
	body, err := marshal(msg)
	if err != nil {
		return err
	}
	s.mu.Lock()
	done := s.done
	s.mu.Unlock()
	if done {
		return status.Errorf(status.CodeUnavailable, "stream %d already closed", s.id)
	}
	return sendError(s.c.tr.Send(ctx, transport.Message{CallID: s.id, Kind: KindStreamMsg, Body: body}))
}

// CloseSend half-closes the client send side. The server may keep sending.
func (s *ClientStream) CloseSend(ctx context.Context) error {
	return sendError(s.c.tr.Send(ctx, transport.Message{CallID: s.id, Kind: KindStreamEnd}))
}

// Cancel cancels the whole call; the server handler context is canceled.
func (s *ClientStream) Cancel(ctx context.Context) error {
	return sendError(s.c.tr.Send(ctx, transport.Message{CallID: s.id, Kind: KindStreamCancel}))
}

// Recv returns the next payload and io.EOF after the server ends the stream.
func (s *ClientStream) Recv(ctx context.Context) ([]byte, error) {
	select {
	case m, ok := <-s.recv:
		if !ok {
			return nil, io.EOF
		}
		if m.Err != nil {
			return nil, m.Err
		}
		return m.Body, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// terminate closes the stream locally (used when opening fails or the
// connection dies). terminal, if non-nil, is delivered to a pending Recv.
func (s *ClientStream) terminate(terminal *status.Status) {
	s.mu.Lock()
	if s.done {
		s.mu.Unlock()
		return
	}
	s.done = true
	s.mu.Unlock()

	// Unregister from the client so late frames are dropped.
	s.c.mu.Lock()
	delete(s.c.streams, s.id)
	s.c.mu.Unlock()

	if terminal != nil {
		s.recv <- transport.Message{CallID: s.id, Kind: KindStreamEndTerm, Err: terminal}
	}
	close(s.recv)
}

// push delivers a frame from the receive loop.
func (s *ClientStream) push(m transport.Message) {
	s.mu.Lock()
	done := s.done
	s.mu.Unlock()
	if done {
		return
	}
	select {
	case s.recv <- m:
	case <-s.c.done:
	}
}

func (c *Client) recvLoop() {
	defer close(c.done)
	for {
		m, err := c.tr.Recv(context.Background())
		if err != nil {
			c.failAll(err)
			return
		}
		switch m.Kind {
		case KindResponse:
			c.routeInflight(m)
		case KindStreamMsg, KindStreamEndTerm:
			c.routeStream(m)
		default:
			// The server must never open a stream toward the client.
			c.routeInflight(transport.Message{
				CallID: m.CallID,
				Err:    status.Errorf(status.CodeUnknown, "unexpected frame kind %d from server", m.Kind),
			})
		}
	}
}

func (c *Client) routeInflight(m transport.Message) {
	c.mu.Lock()
	ch := c.inflight[m.CallID]
	delete(c.inflight, m.CallID)
	c.mu.Unlock()
	if ch != nil {
		ch <- m
	}
}

func (c *Client) routeStream(m transport.Message) {
	c.mu.Lock()
	s := c.streams[m.CallID]
	c.mu.Unlock()
	if s != nil {
		s.push(m)
	}
}

func (c *Client) failAll(recvErr error) {
	st := status.Errorf(status.CodeUnavailable, "transport closed: %v", recvErr)

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	c.closeErr = st
	inflight := c.inflight
	streams := c.streams
	c.inflight = make(map[uint64]chan transport.Message)
	c.streams = make(map[uint64]*ClientStream)
	c.mu.Unlock()

	for _, ch := range inflight {
		ch <- transport.Message{Err: st}
	}
	for _, s := range streams {
		s.terminate(st)
	}
}

// Server dispatches frames from one Transport to registered handlers.
type Server struct {
	tr transport.Transport

	mu       sync.Mutex
	handlers map[string]*handler
	streams  map[uint64]*serverCall
}

type handler struct {
	unary  func(ctx context.Context, body []byte) (any, error)
	stream func(ctx context.Context, body []byte, stream *ServerStream) error
}

type serverCall struct {
	cancel      context.CancelFunc
	done        chan struct{}
	inbound     chan transport.Message
	inboundOnce sync.Once
}

// NewServer returns an empty dispatcher bound to tr.
func NewServer(tr transport.Transport) *Server {
	return &Server{
		tr:       tr,
		handlers: make(map[string]*handler),
		streams:  make(map[uint64]*serverCall),
	}
}

// HandleUnary registers a unary handler for method.
func (s *Server) HandleUnary(method string, fn func(ctx context.Context, body []byte) (any, error)) *Server {
	s.mu.Lock()
	defer s.mu.Unlock()
	h := s.handler(method)
	h.unary = fn
	return s
}

// HandleStream registers a stream handler for method.
func (s *Server) HandleStream(method string, fn func(ctx context.Context, body []byte, stream *ServerStream) error) *Server {
	s.mu.Lock()
	defer s.mu.Unlock()
	h := s.handler(method)
	h.stream = fn
	return s
}

func (s *Server) handler(method string) *handler {
	h, ok := s.handlers[method]
	if !ok {
		h = &handler{}
		s.handlers[method] = h
	}
	return h
}

func (s *Server) lookup(method string) (*handler, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	h, ok := s.handlers[method]
	return h, ok
}

// Serve consumes frames until the transport closes or ctx is canceled.
func (s *Server) Serve(ctx context.Context) error {
	for {
		m, err := s.tr.Recv(ctx)
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, transport.ErrClosed) {
				return nil
			}
			return err
		}
		switch m.Kind {
		case KindUnary:
			s.dispatchUnary(ctx, m)
		case KindStreamReq:
			s.dispatchStream(ctx, m)
		case KindStreamMsg, KindStreamEnd, KindStreamCancel:
			s.routeInbound(m)
		default:
			// A client never sends terminal frames; report the protocol error.
			_ = s.tr.Send(context.Background(), transport.Message{
				CallID: m.CallID,
				Kind:   KindResponse,
				Err:    status.Errorf(status.CodeUnknown, "unexpected frame kind %d from client", m.Kind),
			})
		}
	}
}

func (s *Server) dispatchUnary(parent context.Context, m transport.Message) {
	go func() {
		var (
			body []byte
			st   *status.Status
		)
		h, ok := s.lookup(m.Method)
		switch {
		case !ok:
			st = status.Errorf(status.CodeUnimplemented, "method %s not implemented", m.Method)
		default:
			resp, err := h.unary(parent, m.Body)
			if err != nil {
				st = statusFromError(err)
			} else {
				body, err = marshal(resp)
				if err != nil {
					st = status.Errorf(status.CodeInternal, "marshal response: %v", err)
				}
			}
		}
		_ = s.tr.Send(context.Background(), transport.Message{
			CallID: m.CallID,
			Kind:   KindResponse,
			Body:   body,
			Err:    st,
		})
	}()
}

func (s *Server) dispatchStream(parent context.Context, m transport.Message) {
	h, ok := s.lookup(m.Method)
	if !ok {
		_ = s.tr.Send(context.Background(), transport.Message{
			CallID: m.CallID,
			Kind:   KindStreamEndTerm,
			Err:    status.Errorf(status.CodeUnimplemented, "method %s not implemented", m.Method),
		})
		return
	}

	callCtx, cancel := context.WithCancel(parent)
	call := &serverCall{
		cancel:  cancel,
		done:    make(chan struct{}),
		inbound: make(chan transport.Message, streamBuffer),
	}
	s.mu.Lock()
	s.streams[m.CallID] = call
	s.mu.Unlock()

	stream := &ServerStream{tr: s.tr, id: m.CallID, inbound: call.inbound}
	go func() {
		defer func() {
			s.mu.Lock()
			delete(s.streams, m.CallID)
			s.mu.Unlock()
			cancel()
			close(call.done)
		}()
		err := h.stream(callCtx, m.Body, stream)
		var st *status.Status
		if err != nil && !errors.Is(err, io.EOF) {
			st = statusFromError(err)
		}
		_ = s.tr.Send(context.Background(), transport.Message{
			CallID: m.CallID,
			Kind:   KindStreamEndTerm,
			Err:    st,
		})
	}()
}

func (s *Server) routeInbound(m transport.Message) {
	s.mu.Lock()
	call := s.streams[m.CallID]
	s.mu.Unlock()
	if call == nil {
		_ = s.tr.Send(context.Background(), transport.Message{
			CallID: m.CallID,
			Kind:   KindStreamEndTerm,
			Err:    status.Errorf(status.CodeNotFound, "unknown stream %d", m.CallID),
		})
		return
	}

	switch m.Kind {
	case KindStreamMsg:
		// Blocking send: a full inbound queue propagates backpressure to the
		// peer instead of dropping frames.
		select {
		case call.inbound <- m:
		case <-call.done:
		}
	case KindStreamEnd:
		select {
		case call.inbound <- m:
		case <-call.done:
		}
		call.inboundOnce.Do(func() { close(call.inbound) })
	case KindStreamCancel:
		call.cancel()
		call.inboundOnce.Do(func() { close(call.inbound) })
	}
}

// ServerStream is the server side of one stream call.
type ServerStream struct {
	tr      transport.Transport
	id      uint64
	inbound chan transport.Message
}

// Send pushes one payload to the client.
func (s *ServerStream) Send(ctx context.Context, msg any) error {
	body, err := marshal(msg)
	if err != nil {
		return err
	}
	return sendError(s.tr.Send(ctx, transport.Message{CallID: s.id, Kind: KindStreamMsg, Body: body}))
}

// Recv reads one client payload; io.EOF once the client half-closes or
// cancels. Only bidi handlers should call Recv.
func (s *ServerStream) Recv(ctx context.Context) ([]byte, error) {
	select {
	case m, ok := <-s.inbound:
		if !ok {
			return nil, io.EOF
		}
		if m.Kind == KindStreamEnd {
			// Half-close marker: drain anything queued after it (there should
			// be none) and return EOF.
			return nil, io.EOF
		}
		return m.Body, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// marshal encodes req to JSON, treating nil as an empty object.
func marshal(req any) ([]byte, error) {
	if req == nil {
		return []byte("{}"), nil
	}
	b, err := json.Marshal(req)
	if err != nil {
		return nil, status.Errorf(status.CodeInvalidArgument, "marshal request: %v", err)
	}
	return b, nil
}

func sendError(err error) error {
	if errors.Is(err, transport.ErrClosed) {
		return status.Errorf(status.CodeUnavailable, "transport closed")
	}
	return err
}

func statusFromError(err error) *status.Status {
	if err == nil {
		return status.New()
	}
	var st *status.Status
	if errors.As(err, &st) {
		return st
	}
	switch {
	case errors.Is(err, context.Canceled):
		return status.Errorf(status.CodeCanceled, "canceled")
	case errors.Is(err, context.DeadlineExceeded):
		return status.Errorf(status.CodeDeadlineExceeded, "deadline exceeded")
	default:
		return status.Errorf(status.CodeUnknown, "%v", err)
	}
}
