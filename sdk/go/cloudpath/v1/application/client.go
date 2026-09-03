package application

import (
	"context"
	"encoding/json"

	"github.com/DeliciousBuding/cloud-path/sdk/go/rpc"
)

type applicationClient struct {
	rc *rpc.Client
}

func (c *applicationClient) Initialize(ctx context.Context, req *InitializeRequest) (*InitializeResponse, error) {
	body, err := c.rc.Call(ctx, MethodInitialize, req)
	if err != nil {
		return nil, err
	}
	var resp InitializeResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *applicationClient) Describe(ctx context.Context) (*ApplicationDescriptor, error) {
	body, err := c.rc.Call(ctx, MethodDescribe, &DescribeRequest{})
	if err != nil {
		return nil, err
	}
	var resp ApplicationDescriptor
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *applicationClient) ConfigureInstance(ctx context.Context, req *ConfigureInstanceRequest) (*ConfigureInstanceResponse, error) {
	body, err := c.rc.Call(ctx, MethodConfigureInstance, req)
	if err != nil {
		return nil, err
	}
	var resp ConfigureInstanceResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *applicationClient) ValidateBinding(ctx context.Context, req *ValidateBindingRequest) (*ValidateBindingResponse, error) {
	body, err := c.rc.Call(ctx, MethodValidateBinding, req)
	if err != nil {
		return nil, err
	}
	var resp ValidateBindingResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *applicationClient) HandleRequest(ctx context.Context, req *PluginHTTPRequest) (*PluginHTTPResponse, error) {
	body, err := c.rc.Call(ctx, MethodHandleRequest, req)
	if err != nil {
		return nil, err
	}
	var resp PluginHTTPResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *applicationClient) RunJob(ctx context.Context, req *RunJobRequest) (*RunJobResponse, error) {
	body, err := c.rc.Call(ctx, MethodRunJob, req)
	if err != nil {
		return nil, err
	}
	var resp RunJobResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *applicationClient) Health(ctx context.Context) (*HealthResponse, error) {
	body, err := c.rc.Call(ctx, MethodHealth, &HealthRequest{})
	if err != nil {
		return nil, err
	}
	var resp HealthResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *applicationClient) Shutdown(ctx context.Context, req *ShutdownRequest) (*ShutdownResponse, error) {
	body, err := c.rc.Call(ctx, MethodShutdown, req)
	if err != nil {
		return nil, err
	}
	var resp ShutdownResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *applicationClient) HandleEvents(ctx context.Context) (ApplicationEventStream, error) {
	s, err := c.rc.OpenStream(ctx, MethodHandleEvents, &struct{}{})
	if err != nil {
		return nil, err
	}
	return &applicationEventStream{s: s}, nil
}

type applicationEventStream struct {
	s *rpc.ClientStream
}

func (st *applicationEventStream) Send(ctx context.Context, event *ApplicationEvent) error {
	return st.s.Send(ctx, event)
}

func (st *applicationEventStream) CloseSend(ctx context.Context) error {
	return st.s.CloseSend(ctx)
}

func (st *applicationEventStream) Recv(ctx context.Context) (*ApplicationEffect, error) {
	body, err := st.s.Recv(ctx)
	if err != nil {
		return nil, err
	}
	var effect ApplicationEffect
	if err := json.Unmarshal(body, &effect); err != nil {
		return nil, err
	}
	return &effect, nil
}

func (st *applicationEventStream) Cancel(ctx context.Context) error {
	return st.s.Cancel(ctx)
}
