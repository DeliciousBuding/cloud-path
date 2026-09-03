package driver

import (
	"context"
	"encoding/json"

	"github.com/DeliciousBuding/cloud-path/sdk/go/rpc"
)

// register wires a DriverServer implementation into a transport dispatcher.
func register(impl DriverServer, s *rpc.Server) *rpc.Server {
	s.HandleUnary(MethodInitialize, func(ctx context.Context, body []byte) (any, error) {
		var req InitializeRequest
		if err := json.Unmarshal(body, &req); err != nil {
			return nil, err
		}
		return impl.Initialize(ctx, &req)
	})
	s.HandleUnary(MethodDescribe, func(ctx context.Context, body []byte) (any, error) {
		return impl.Describe(ctx)
	})
	s.HandleUnary(MethodConfigureInstance, func(ctx context.Context, body []byte) (any, error) {
		var req ConfigureInstanceRequest
		if err := json.Unmarshal(body, &req); err != nil {
			return nil, err
		}
		return impl.ConfigureInstance(ctx, &req)
	})
	s.HandleStream(MethodDiscover, func(ctx context.Context, body []byte, stream *rpc.ServerStream) error {
		var req DiscoverRequest
		if err := json.Unmarshal(body, &req); err != nil {
			return err
		}
		return impl.Discover(ctx, &req, &discoveryWriter{s: stream})
	})
	s.HandleUnary(MethodOpenDevice, func(ctx context.Context, body []byte) (any, error) {
		var req OpenDeviceRequest
		if err := json.Unmarshal(body, &req); err != nil {
			return nil, err
		}
		return impl.OpenDevice(ctx, &req)
	})
	s.HandleUnary(MethodCloseDevice, func(ctx context.Context, body []byte) (any, error) {
		var req CloseDeviceRequest
		if err := json.Unmarshal(body, &req); err != nil {
			return nil, err
		}
		return impl.CloseDevice(ctx, &req)
	})
	s.HandleStream(MethodWatch, func(ctx context.Context, body []byte, stream *rpc.ServerStream) error {
		var req WatchRequest
		if err := json.Unmarshal(body, &req); err != nil {
			return err
		}
		return impl.Watch(ctx, &req, &driverMessageWriter{s: stream})
	})
	s.HandleUnary(MethodExecute, func(ctx context.Context, body []byte) (any, error) {
		var req ExecuteRequest
		if err := json.Unmarshal(body, &req); err != nil {
			return nil, err
		}
		return impl.Execute(ctx, &req)
	})
	s.HandleUnary(MethodHealth, func(ctx context.Context, body []byte) (any, error) {
		return impl.Health(ctx)
	})
	s.HandleUnary(MethodShutdown, func(ctx context.Context, body []byte) (any, error) {
		var req ShutdownRequest
		if err := json.Unmarshal(body, &req); err != nil {
			return nil, err
		}
		return impl.Shutdown(ctx, &req)
	})
	return s
}

type discoveryWriter struct {
	s *rpc.ServerStream
}

func (w *discoveryWriter) Send(ctx context.Context, msg *DiscoveryEvent) error {
	return w.s.Send(ctx, msg)
}

type driverMessageWriter struct {
	s *rpc.ServerStream
}

func (w *driverMessageWriter) Send(ctx context.Context, msg *DriverMessage) error {
	return w.s.Send(ctx, msg)
}
