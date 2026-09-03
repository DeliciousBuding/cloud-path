package application

import (
	"context"
	"encoding/json"

	"github.com/DeliciousBuding/cloud-path/sdk/go/rpc"
)

func register(impl ApplicationServer, s *rpc.Server) *rpc.Server {
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
	s.HandleUnary(MethodValidateBinding, func(ctx context.Context, body []byte) (any, error) {
		var req ValidateBindingRequest
		if err := json.Unmarshal(body, &req); err != nil {
			return nil, err
		}
		return impl.ValidateBinding(ctx, &req)
	})
	s.HandleStream(MethodHandleEvents, func(ctx context.Context, body []byte, stream *rpc.ServerStream) error {
		return impl.HandleEvents(ctx, &eventReader{s: stream}, &effectWriter{s: stream})
	})
	s.HandleUnary(MethodHandleRequest, func(ctx context.Context, body []byte) (any, error) {
		var req PluginHTTPRequest
		if err := json.Unmarshal(body, &req); err != nil {
			return nil, err
		}
		return impl.HandleRequest(ctx, &req)
	})
	s.HandleUnary(MethodRunJob, func(ctx context.Context, body []byte) (any, error) {
		var req RunJobRequest
		if err := json.Unmarshal(body, &req); err != nil {
			return nil, err
		}
		return impl.RunJob(ctx, &req)
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

type eventReader struct {
	s *rpc.ServerStream
}

func (r *eventReader) Recv(ctx context.Context) (*ApplicationEvent, error) {
	body, err := r.s.Recv(ctx)
	if err != nil {
		return nil, err
	}
	var event ApplicationEvent
	if err := json.Unmarshal(body, &event); err != nil {
		return nil, err
	}
	return &event, nil
}

type effectWriter struct {
	s *rpc.ServerStream
}

func (w *effectWriter) Send(ctx context.Context, effect *ApplicationEffect) error {
	return w.s.Send(ctx, effect)
}
