package pluginharness

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/DeliciousBuding/cloud-path/sdk/go/cloudpath/v1/driver"
	"github.com/DeliciousBuding/cloud-path/sdk/go/cloudpath/v1/status"
	"github.com/DeliciousBuding/cloud-path/sdk/go/transport"
)

// MockCore is the Core-side counterpart of MockDriver. It drives a real
// DriverClient over the in-process transport, dedupes incoming DriverMessage
// frames with a SequenceTracker, and records the accepted stream for
// assertions. It also owns both transport ends so tests can simulate a
// plugin crash.
type MockCore struct {
	cli  driver.DriverClient
	end  transport.Transport // the Core-side transport end
	peer transport.Transport // the plugin-side transport end

	tracker *driver.SequenceTracker

	mu        sync.Mutex
	accepted  []*driver.DriverMessage // deduped, in receive order
	rawCount  int                     // every frame observed, including drops
	watchErr  error
	recvDelay time.Duration

	watchCtx    context.Context
	watchCancel context.CancelFunc
	watchDone   chan struct{}
}

// MockCoreOption tunes a MockCore.
type MockCoreOption func(*MockCore)

// WithRecvDelay simulates a slow consumer: each accepted frame is delayed,
// which lets tests exercise transport backpressure.
func WithRecvDelay(d time.Duration) MockCoreOption {
	return func(c *MockCore) { c.recvDelay = d }
}

// NewMockCore wraps a DriverClient plus both pipe ends.
func NewMockCore(cli driver.DriverClient, coreEnd, peerEnd transport.Transport, opts ...MockCoreOption) *MockCore {
	c := &MockCore{
		cli:     cli,
		end:     coreEnd,
		peer:    peerEnd,
		tracker: driver.NewSequenceTracker(),
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// Initialize performs the Core-side handshake.
func (c *MockCore) Initialize(ctx context.Context, req *driver.InitializeRequest) (*driver.InitializeResponse, error) {
	return c.cli.Initialize(ctx, req)
}

// Describe fetches the plugin descriptor.
func (c *MockCore) Describe(ctx context.Context) (*driver.DriverDescriptor, error) {
	return c.cli.Describe(ctx)
}

// Execute forwards a command.
func (c *MockCore) Execute(ctx context.Context, req *driver.ExecuteRequest) (*driver.ExecuteResponse, error) {
	return c.cli.Execute(ctx, req)
}

// Health forwards a health probe.
func (c *MockCore) Health(ctx context.Context) (*driver.HealthResponse, error) {
	return c.cli.Health(ctx)
}

// StartWatch opens a Watch stream and consumes frames in the background,
// applying the (instance, device, sequence) dedup contract. Frames that Core
// would drop are counted in RawCount but not recorded.
func (c *MockCore) StartWatch(ctx context.Context, req *driver.WatchRequest) error {
	st, err := c.cli.Watch(ctx, req)
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.watchCtx, c.watchCancel = context.WithCancel(ctx)
	c.watchDone = make(chan struct{})
	c.mu.Unlock()

	go func() {
		defer close(c.watchDone)
		for {
			msg, err := st.Recv(c.watchCtx)
			if err != nil {
				c.mu.Lock()
				c.watchErr = err
				c.mu.Unlock()
				return
			}
			c.mu.Lock()
			c.rawCount++
			c.mu.Unlock()
			if c.tracker.AcceptMessage(msg) {
				c.mu.Lock()
				c.accepted = append(c.accepted, msg)
				c.mu.Unlock()
				if c.recvDelay > 0 {
					select {
					case <-time.After(c.recvDelay):
					case <-c.watchCtx.Done():
					}
				}
			}
		}
	}()
	return nil
}

// StopWatch cancels the background consumer and waits for it to exit.
func (c *MockCore) StopWatch() {
	c.mu.Lock()
	cancel := c.watchCancel
	done := c.watchDone
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
}

// Accepted returns a copy of deduped messages in receive order.
func (c *MockCore) Accepted() []*driver.DriverMessage {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]*driver.DriverMessage(nil), c.accepted...)
}

// RawCount is the number of frames observed (before dedup).
func (c *MockCore) RawCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.rawCount
}

// WatchErr is the terminal Watch error once the stream ends.
func (c *MockCore) WatchErr() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.watchErr
}

// WaitForMessage blocks until a deduped message satisfies pred or the
// timeout expires. It reports (found, true) on success.
func (c *MockCore) WaitForMessage(timeout time.Duration, pred func(*driver.DriverMessage) bool) (*driver.DriverMessage, bool) {
	deadline := time.Now().Add(timeout)
	for {
		for _, m := range c.Accepted() {
			if pred(m) {
				return m, true
			}
		}
		if time.Now().After(deadline) {
			return nil, false
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// WaitForCommandTerminal waits for a terminal CommandProgress of the given
// command and returns its state.
func (c *MockCore) WaitForCommandTerminal(timeout time.Duration, commandID string) (driver.CommandState, bool) {
	for {
		for _, m := range c.Accepted() {
			cp, ok := m.Union.(*driver.CommandProgress)
			if ok && cp.CommandID == commandID && cp.State >= driver.CommandStateSucceeded {
				return cp.State, true
			}
		}
		if timeout <= 0 {
			return driver.CommandStateUnspecified, false
		}
		timeout -= 5 * time.Millisecond
		time.Sleep(5 * time.Millisecond)
	}
}

// SimulateCrash tears down both transport ends, equivalent to the plugin
// process dying and the host dropping the channel. Pending calls and streams
// must then fail with an error rather than hang.
func (c *MockCore) SimulateCrash() {
	_ = c.end.Close()
	_ = c.peer.Close()
}

// LastSequence reports the tracked sequence for a scope.
func (c *MockCore) LastSequence(instanceID, deviceID string) uint64 {
	return c.tracker.Last(instanceID, deviceID)
}

// ExpectAccepted returns an error if the accepted set does not match the
// expected per-(instance, device) sequence list exactly.
func (c *MockCore) ExpectAccepted(want []*driver.DriverMessage) error {
	got := c.Accepted()
	if len(got) != len(want) {
		return fmt.Errorf("accepted %d messages, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].PluginInstanceID != want[i].PluginInstanceID ||
			got[i].DeviceID != want[i].DeviceID ||
			got[i].Sequence != want[i].Sequence {
			return fmt.Errorf("message %d: got (instance=%s device=%s seq=%d), want (instance=%s device=%s seq=%d)",
				i, got[i].PluginInstanceID, got[i].DeviceID, got[i].Sequence,
				want[i].PluginInstanceID, want[i].DeviceID, want[i].Sequence)
		}
	}
	return nil
}

// StatusCode extracts the code from an error that is (or wraps) a Status.
func StatusCode(err error) status.Code {
	if err == nil {
		return status.CodeOK
	}
	var st *status.Status
	if ok := asStatus(err, &st); ok {
		return st.Code
	}
	return status.CodeUnknown
}

func asStatus(err error, target **status.Status) bool {
	type causer interface{ Unwrap() error }
	for err != nil {
		if s, ok := err.(*status.Status); ok {
			*target = s
			return true
		}
		u, ok := err.(causer)
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}
