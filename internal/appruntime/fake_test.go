package appruntime

import (
	"context"
	"io"
	"sync"

	sdkapplication "github.com/DeliciousBuding/cloud-path/sdk/go/cloudpath/v1/application"
	"github.com/DeliciousBuding/cloud-path/sdk/go/cloudpath/v1/status"
)

type fakeExecutor struct {
	mu   sync.Mutex
	call []Effect
	err  error
}

func (e *fakeExecutor) Execute(ctx context.Context, effect Effect) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.call = append(e.call, effect)
	return e.err
}

func (e *fakeExecutor) Calls() []Effect {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]Effect(nil), e.call...)
}

func (e *fakeExecutor) Count() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.call)
}

type fakeClient struct {
	mu sync.Mutex

	initResp      *sdkapplication.InitializeResponse
	initErr       error
	desc          *sdkapplication.ApplicationDescriptor
	descErr       error
	validateResp  *sdkapplication.ValidateBindingResponse
	validateErr   error
	configureResp *sdkapplication.ConfigureInstanceResponse
	configureErr  error
	stream        sdkapplication.ApplicationEventStream
	streamErr     error
	requestResp   *sdkapplication.PluginHTTPResponse
	requestErr    error
	jobResp       *sdkapplication.RunJobResponse
	jobErr        error
	healthResp    *sdkapplication.HealthResponse
	healthErr     error
	shutdownResp  *sdkapplication.ShutdownResponse
	shutdownErr   error

	initReqs      []*sdkapplication.InitializeRequest
	validateReqs  []*sdkapplication.ValidateBindingRequest
	configureReqs []*sdkapplication.ConfigureInstanceRequest
	requestReqs   []*sdkapplication.PluginHTTPRequest
	jobReqs       []*sdkapplication.RunJobRequest
	shutdownReqs  []*sdkapplication.ShutdownRequest
}

func newFakeClient(desc *sdkapplication.ApplicationDescriptor, stream sdkapplication.ApplicationEventStream) *fakeClient {
	return &fakeClient{
		initResp:      &sdkapplication.InitializeResponse{NegotiatedProtocolVersion: sdkapplication.ProtocolVersion, Status: status.New()},
		desc:          desc,
		validateResp:  &sdkapplication.ValidateBindingResponse{Valid: true},
		configureResp: &sdkapplication.ConfigureInstanceResponse{AppliedRevision: 1, Status: status.New()},
		stream:        stream,
		requestResp:   &sdkapplication.PluginHTTPResponse{StatusCode: 200, Headers: map[string]string{}, Body: []byte("ok")},
		jobResp:       &sdkapplication.RunJobResponse{JobID: "job-1", Status: status.New(), ResultJSON: "{}"},
		healthResp:    &sdkapplication.HealthResponse{State: sdkapplication.HealthStateServing},
		shutdownResp:  &sdkapplication.ShutdownResponse{Status: status.New()},
	}
}

func (c *fakeClient) Initialize(ctx context.Context, req *sdkapplication.InitializeRequest) (*sdkapplication.InitializeResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.initReqs = append(c.initReqs, req)
	return c.initResp, c.initErr
}

func (c *fakeClient) Describe(ctx context.Context) (*sdkapplication.ApplicationDescriptor, error) {
	return c.desc, c.descErr
}

func (c *fakeClient) ConfigureInstance(ctx context.Context, req *sdkapplication.ConfigureInstanceRequest) (*sdkapplication.ConfigureInstanceResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.configureReqs = append(c.configureReqs, req)
	return c.configureResp, c.configureErr
}

func (c *fakeClient) ValidateBinding(ctx context.Context, req *sdkapplication.ValidateBindingRequest) (*sdkapplication.ValidateBindingResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.validateReqs = append(c.validateReqs, req)
	return c.validateResp, c.validateErr
}

func (c *fakeClient) HandleEvents(ctx context.Context) (sdkapplication.ApplicationEventStream, error) {
	return c.stream, c.streamErr
}

func (c *fakeClient) HandleRequest(ctx context.Context, req *sdkapplication.PluginHTTPRequest) (*sdkapplication.PluginHTTPResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.requestReqs = append(c.requestReqs, req)
	return c.requestResp, c.requestErr
}

func (c *fakeClient) RunJob(ctx context.Context, req *sdkapplication.RunJobRequest) (*sdkapplication.RunJobResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.jobReqs = append(c.jobReqs, req)
	return c.jobResp, c.jobErr
}

func (c *fakeClient) Health(ctx context.Context) (*sdkapplication.HealthResponse, error) {
	return c.healthResp, c.healthErr
}

func (c *fakeClient) Shutdown(ctx context.Context, req *sdkapplication.ShutdownRequest) (*sdkapplication.ShutdownResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.shutdownReqs = append(c.shutdownReqs, req)
	return c.shutdownResp, c.shutdownErr
}

func (c *fakeClient) ShutdownCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.shutdownReqs)
}

func (c *fakeClient) LastShutdown() *sdkapplication.ShutdownRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.shutdownReqs) == 0 {
		return nil
	}
	return c.shutdownReqs[len(c.shutdownReqs)-1]
}

type fakeStream struct {
	mu sync.Mutex

	sent           []*sdkapplication.ApplicationEvent
	effects        chan *sdkapplication.ApplicationEffect
	closed         bool
	closeSendCalls int
	cancelCalls    int

	// sendDelay, when non-nil, makes Send block until the channel is closed.
	sendDelay chan struct{}
	// sendStarted is signalled before a blocking Send waits.
	sendStarted chan struct{}
}

func newFakeStream() *fakeStream {
	return &fakeStream{effects: make(chan *sdkapplication.ApplicationEffect, 16)}
}

func (s *fakeStream) Send(ctx context.Context, event *sdkapplication.ApplicationEvent) error {
	if s.sendStarted != nil {
		select {
		case s.sendStarted <- struct{}{}:
		default:
		}
	}
	if s.sendDelay != nil {
		select {
		case <-s.sendDelay:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sent = append(s.sent, event)
	return nil
}

func (s *fakeStream) CloseSend(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	s.closeSendCalls++
	return nil
}

func (s *fakeStream) Recv(ctx context.Context) (*sdkapplication.ApplicationEffect, error) {
	select {
	case effect, ok := <-s.effects:
		if !ok {
			return nil, io.EOF
		}
		return effect, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *fakeStream) Cancel(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cancelCalls++
	return nil
}

func (s *fakeStream) Sent() []*sdkapplication.ApplicationEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*sdkapplication.ApplicationEvent(nil), s.sent...)
}

func (s *fakeStream) SentCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.sent)
}
