package driver

import (
	"context"
	"encoding/json"

	"github.com/DeliciousBuding/cloud-path/sdk/go/rpc"
)

// driverClient adapts the transport-level rpc.Client to DriverClient.
type driverClient struct {
	rc *rpc.Client
}

func (c *driverClient) Initialize(ctx context.Context, req *InitializeRequest) (*InitializeResponse, error) {
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

func (c *driverClient) Describe(ctx context.Context) (*DriverDescriptor, error) {
	body, err := c.rc.Call(ctx, MethodDescribe, &DescribeRequest{})
	if err != nil {
		return nil, err
	}
	var resp DriverDescriptor
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *driverClient) ConfigureInstance(ctx context.Context, req *ConfigureInstanceRequest) (*ConfigureInstanceResponse, error) {
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

func (c *driverClient) OpenDevice(ctx context.Context, req *OpenDeviceRequest) (*OpenDeviceResponse, error) {
	body, err := c.rc.Call(ctx, MethodOpenDevice, req)
	if err != nil {
		return nil, err
	}
	var resp OpenDeviceResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *driverClient) CloseDevice(ctx context.Context, req *CloseDeviceRequest) (*CloseDeviceResponse, error) {
	body, err := c.rc.Call(ctx, MethodCloseDevice, req)
	if err != nil {
		return nil, err
	}
	var resp CloseDeviceResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *driverClient) Execute(ctx context.Context, req *ExecuteRequest) (*ExecuteResponse, error) {
	body, err := c.rc.Call(ctx, MethodExecute, req)
	if err != nil {
		return nil, err
	}
	var resp ExecuteResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *driverClient) Health(ctx context.Context) (*HealthResponse, error) {
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

func (c *driverClient) Shutdown(ctx context.Context, req *ShutdownRequest) (*ShutdownResponse, error) {
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

func (c *driverClient) Discover(ctx context.Context, req *DiscoverRequest) (DiscoveryStream, error) {
	s, err := c.rc.OpenStream(ctx, MethodDiscover, req)
	if err != nil {
		return nil, err
	}
	return &discoveryStream{s: s}, nil
}

func (c *driverClient) Watch(ctx context.Context, req *WatchRequest) (DriverMessageStream, error) {
	s, err := c.rc.OpenStream(ctx, MethodWatch, req)
	if err != nil {
		return nil, err
	}
	return &driverMessageStream{s: s}, nil
}

type discoveryStream struct {
	s *rpc.ClientStream
}

func (st *discoveryStream) Recv(ctx context.Context) (*DiscoveryEvent, error) {
	body, err := st.s.Recv(ctx)
	if err != nil {
		return nil, err
	}
	var msg DiscoveryEvent
	if err := json.Unmarshal(body, &msg); err != nil {
		return nil, err
	}
	return &msg, nil
}

func (st *discoveryStream) Cancel(ctx context.Context) error { return st.s.Cancel(ctx) }

type driverMessageStream struct {
	s *rpc.ClientStream
}

func (st *driverMessageStream) Recv(ctx context.Context) (*DriverMessage, error) {
	body, err := st.s.Recv(ctx)
	if err != nil {
		return nil, err
	}
	var msg DriverMessage
	if err := json.Unmarshal(body, &msg); err != nil {
		return nil, err
	}
	return &msg, nil
}

func (st *driverMessageStream) Cancel(ctx context.Context) error { return st.s.Cancel(ctx) }
