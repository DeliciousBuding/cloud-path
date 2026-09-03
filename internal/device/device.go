// Package device 是 sdk/go/driverkit 的内部薄兼容层：Core（edge/server）继续使用
// device.Command / device.Adapter / device.Register 等既有名字，单一实现与注册表都在
// 公共 driverkit 中，外部 Driver 插件只 import sdk/go/driverkit，不 import 本包。
package device

import (
	"github.com/DeliciousBuding/cloud-path/sdk/go/driverkit"
)

// 类型别名（与 driverkit 完全一致，禁止在本包复制结构）。
type (
	Command            = driverkit.Command
	Event              = driverkit.Event
	State              = driverkit.State
	Device             = driverkit.Device
	Config             = driverkit.Config
	Adapter            = driverkit.Adapter
	DescriptorProvider = driverkit.DescriptorProvider
	CapabilityProvider = driverkit.CapabilityProvider
	DescriptorSource   = driverkit.DescriptorSource
)

// Register 注册适配器（与 driverkit 共享同一注册表）。
func Register(a Adapter) { driverkit.Register(a) }

// Get 按名取适配器。
func Get(name string) (Adapter, bool) { return driverkit.Get(name) }

// Names 返回全部已注册适配器名（排序，确定性输出）。
func Names() []string { return driverkit.Names() }
