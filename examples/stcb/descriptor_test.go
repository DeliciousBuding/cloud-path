package stcb

import (
	"testing"

	"github.com/DeliciousBuding/cloud-path/sdk/go/driverkit"
	"github.com/DeliciousBuding/cloud-path/sdk/go/model"
)

// TestSTCBDescriptorValid 锁定 STC-B Descriptor/Capability catalog 的契约合法性：
// 静态 Descriptor 可过 descriptor.schema.json 语义校验，Entity 映射 clock/alarm/compartment-1..3，
// 且每个 Entity 声明的 Capability 引用都能在 catalog 里解析到（前后端 SchemaRenderer 不回落）。
func TestSTCBDescriptorValid(t *testing.T) {
	a := &Adapter{}

	// Port 使用占位符：公开边界要求不出现真实串口号。
	desc := a.Descriptor(driverkit.Config{ID: "d1", Name: "节点1", Port: "COM_PLACEHOLDER"})
	if err := desc.Validate(); err != nil {
		t.Fatalf("descriptor.Validate: %v", err)
	}
	if desc.DeviceID != "d1" || desc.ExternalID != "d1" {
		t.Fatalf("descriptor identity = %q/%q", desc.DeviceID, desc.ExternalID)
	}

	wantEntities := []string{
		"clock", "alarm",
		"compartment-1", "compartment-2", "compartment-3",
		"temperature", "illuminance", "hall", "vibration", "key",
		"buzzer", "led", "display", "motor",
	}
	if len(desc.Entities) != len(wantEntities) {
		t.Fatalf("entities = %d, want %d", len(desc.Entities), len(wantEntities))
	}
	byID := map[string]model.Entity{}
	for _, e := range desc.Entities {
		byID[e.EntityID] = e
	}
	for i, id := range wantEntities {
		e, ok := byID[id]
		if !ok {
			t.Fatalf("missing entity %q", id)
		}
		if i == 0 && len(e.Capabilities) != 1 {
			t.Fatalf("entity %q capabilities = %v", id, e.Capabilities)
		}
	}

	caps := a.Capabilities()
	if len(caps) != 12 {
		t.Fatalf("capabilities = %d, want 12", len(caps))
	}
	refs := map[string]bool{}
	for _, c := range caps {
		if err := c.Validate(); err != nil {
			t.Fatalf("capability %q.Validate: %v", c.Metadata.ID, err)
		}
		refs[c.Metadata.ID] = true
	}
	for _, e := range desc.Entities {
		for _, ref := range e.Capabilities {
			if !refs[ref] {
				t.Fatalf("entity %q references undeclared capability %q", e.EntityID, ref)
			}
		}
	}

	// clock 观测值可在设备实例上填充（走 stcbDescriptor 骨架 + setObservation 路径）。
	live := stcbDescriptor("d1")
	setObservation(&live, "clock", model.Observation{
		Capability: capClock, Property: "time", Value: "12:34", Quality: model.QualityGood,
	})
	if err := live.Validate(); err != nil {
		t.Fatalf("live descriptor.Validate: %v", err)
	}
	if got := live.Entities[0].Observations["time"].Value; got != "12:34" {
		t.Fatalf("clock observation = %v", got)
	}
}

// TestCapabilityActionsHaveMetadata 锁定契约§4：每个写执行器 capability 的 spec.actions
// 必须带 title + description + inputSchema（WebUI ActionPanel 才能渲染出带说明和参数的控件），
// 且 action key 必须真实存在于命令白名单（否则被 server 拒绝，属虚假控件）。
func TestCapabilityActionsHaveMetadata(t *testing.T) {
	a := &Adapter{}
	byID := map[string]model.Capability{}
	for _, c := range a.Capabilities() {
		byID[c.Metadata.ID] = c
	}
	writeCaps := []struct {
		id     string
		action string
	}{{capBuzzer, cmdBuzzer}, {capLED, cmdLED}, {capDisplay, cmdDisplay}, {capMotor, cmdMotor}}
	supported := map[string]bool{}
	for _, cmd := range a.SupportedCommands() {
		supported[cmd] = true
	}
	for _, wc := range writeCaps {
		c, ok := byID[wc.id]
		if !ok {
			t.Fatalf("缺少写能力 %q", wc.id)
		}
		act, ok := c.Spec.Actions[wc.action]
		if !ok {
			t.Errorf("%s 缺少 action %q", wc.id, wc.action)
			continue
		}
		if act.Title == "" {
			t.Errorf("%s action %q 缺 title（ActionPanel 无法渲染按钮说明）", wc.id, wc.action)
		}
		if act.Description == "" {
			t.Errorf("%s action %q 缺 description（ActionPanel 无法渲染说明）", wc.id, wc.action)
		}
		if len(act.InputSchema) == 0 {
			t.Errorf("%s action %q 缺 inputSchema（无法渲染参数输入）", wc.id, wc.action)
		}
		if !supported[wc.action] {
			t.Errorf("action %q 不在命令白名单 %v（会被 server 拒绝，属虚假控件）", wc.action, a.SupportedCommands())
		}
	}
	// 传感器能力为只读，不应声明 write action（与契约§4 表一致）。
	for _, id := range []string{capTemp, capIllum, capHall, capVib, capKey} {
		if c, ok := byID[id]; ok && len(c.Spec.Actions) != 0 {
			t.Errorf("只读能力 %q 不应声明 action: %v", id, c.Spec.Actions)
		}
	}
}
