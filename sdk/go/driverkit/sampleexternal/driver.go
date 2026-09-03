// Package sampleexternal 是一个「仓外风格」的参考 Driver 样例：只 import 公共 SDK
// （sdk/go/driverkit 与 sdk/go/model），不 import 任何 internal/*。它作为
// TestExternalStyleDriverCompiles 的编译/运行证明，也随 go build ./... 一起编译。
package sampleexternal

import (
	"context"

	"github.com/DeliciousBuding/cloud-path/sdk/go/driverkit"
	"github.com/DeliciousBuding/cloud-path/sdk/go/model"
)

// Driver 实现 driverkit.Adapter / DescriptorProvider / CapabilityProvider。
type Driver struct{}

var _ driverkit.Adapter = (*Driver)(nil)
var _ driverkit.DescriptorProvider = (*Driver)(nil)
var _ driverkit.CapabilityProvider = (*Driver)(nil)

func (Driver) Name() string { return "sample.external" }

func (Driver) SupportedCommands() []string { return []string{"ping"} }

func (Driver) Open(_ context.Context, _ driverkit.Config, _ func(driverkit.Event)) (driverkit.Device, error) {
	return nil, nil
}

func (Driver) Descriptor(cfg driverkit.Config) model.Descriptor {
	return model.Descriptor{
		DeviceID:   cfg.ID,
		ExternalID: cfg.ID,
		Status:     model.DeviceUnavailable,
		Entities:   []model.Entity{},
	}
}

func (Driver) Capabilities() []model.Capability { return nil }
