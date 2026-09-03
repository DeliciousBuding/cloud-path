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

	desc := a.Descriptor(driverkit.Config{ID: "d1", Name: "节点1", Port: "COM9"})
	if err := desc.Validate(); err != nil {
		t.Fatalf("descriptor.Validate: %v", err)
	}
	if desc.DeviceID != "d1" || desc.ExternalID != "d1" {
		t.Fatalf("descriptor identity = %q/%q", desc.DeviceID, desc.ExternalID)
	}

	wantEntities := []string{"clock", "alarm", "compartment-1", "compartment-2", "compartment-3"}
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
	if len(caps) != 3 {
		t.Fatalf("capabilities = %d, want 3", len(caps))
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
