package edge

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/DeliciousBuding/cloud-path/internal/api"
	"github.com/DeliciousBuding/cloud-path/internal/model"
	"github.com/DeliciousBuding/cloud-path/sdk/go/cloudpath/v1/driver"
	"github.com/DeliciousBuding/cloud-path/sdk/go/transport"
)

// ---- Capability 文档上报（外部 Driver 能力进入 Server catalog 的唯一通道）----

// lastCapabilities 取最近一条 capabilities 信封的载荷。
func lastCapabilities(envs []api.Envelope) (api.CapabilitiesData, bool) {
	var out api.CapabilitiesData
	found := false
	for _, e := range envs {
		if e.Type != api.MsgCapabilities {
			continue
		}
		if err := json.Unmarshal(e.Data, &out); err == nil {
			found = true
		}
	}
	return out, found
}

// TestCapabilitiesReportedOnConnectAndReconnect 锁定：Edge 连上 Server 即上报
// Capability 文档，且**同型号多设备只报一份**（按声明者去重）；断线重连后必须重报
// （Server 侧文档随连接生命周期存在，不重报就等于重连后 UI 丢能力说明）。
func TestCapabilitiesReportedOnConnectAndReconnect(t *testing.T) {
	rec := startEdgeRecorder(t, false)
	cfg := demoConfig(rec.url("ws"), "d1", "d2")
	runEdge(t, cfg)

	rec.waitHello(t, 1)
	rec.waitAfter(t, 0, 20*time.Second, func(envs []api.Envelope) bool {
		data, ok := lastCapabilities(envs)
		return ok && len(data.Sources) == 1
	}, "首连未上报 Capability 文档")

	data, _ := lastCapabilities(rec.all())
	if got := data.Sources[0].Source; got != "demo" {
		t.Fatalf("声明者 = %q, want demo（适配器名即声明者）", got)
	}
	if len(data.Sources[0].Capabilities) == 0 {
		t.Fatal("Capability 文档为空：适配器自述的 catalog 没被搬上来")
	}
	ids := map[string]bool{}
	for _, c := range data.Sources[0].Capabilities {
		if err := c.Validate(); err != nil {
			t.Fatalf("上报的 Capability 文档非法 %q: %v", c.Metadata.ID, err)
		}
		ids[c.Metadata.ID] = true
	}
	if !ids["cloudpath.dev/capability/toggle@1"] {
		t.Fatalf("缺少 demo 的 toggle 能力，实得 %v", ids)
	}

	// 断线重连后必须重报（不是只在首连报一次）。
	mark := rec.mark()
	rec.dropCurrent()
	rec.waitHello(t, 2)
	rec.waitAfter(t, mark, 20*time.Second, func(envs []api.Envelope) bool {
		_, ok := lastCapabilities(envs)
		return ok
	}, "重连后未重报 Capability 文档")
}

// TestCapabilityDocsConversion 锁定 Driver Protocol 描述符 → Core Capability 文档的
// 契约搬运：标题/属性/事件/action inputSchema 一项都不能丢，非法文档必须被跳过
// （否则 Server 侧整批 Validate 失败，一个坏插件会拖垮全部能力说明）。
func TestCapabilityDocsConversion(t *testing.T) {
	in := []driver.CapabilityDescriptor{
		{
			ID: "io.github.example/capability/temperature@2", Title: "Temperature",
			Properties: []driver.PropertyDescriptor{
				{Name: "value", Type: "number", Unit: "Cel", Access: "read", Quality: []string{"good"}},
				{Name: "  "}, // 空名属性必须丢弃
			},
			Events:  []driver.EventDescriptor{{Name: "high", PayloadSchemaJSON: `{"type":"object"}`}},
			Actions: []driver.ActionDescriptor{{Name: "calibrate", InputSchemaJSON: `{"type":"object"}`}},
		},
		{
			ID: "io.github.example/capability/led@1", Title: "LED Bank",
			Properties: []driver.PropertyDescriptor{{Name: "mask", Type: "integer", Access: "read"}},
			Actions:    []driver.ActionDescriptor{{Name: "led", InputSchemaJSON: "not-json"}},
		},
		{ID: "not-a-capability-id", Title: "非法 ID 必须被跳过"},
		{ID: "   ", Title: "空 ID 必须被跳过"},
	}

	out := capabilityDocs("example", in)
	if len(out) != 2 {
		t.Fatalf("合法文档数 = %d, want 2（非法/空 ID 各跳过一条）: %+v", len(out), out)
	}

	temp := out[0]
	if temp.APIVersion != model.CapabilityAPIVersion || temp.Kind != model.CapabilityKind {
		t.Fatalf("apiVersion/kind 未按契约填: %+v", temp)
	}
	if temp.Metadata.Version != 2 || temp.Metadata.Title != "Temperature" {
		t.Fatalf("metadata 搬运错误: %+v", temp.Metadata)
	}
	if len(temp.Spec.Properties) != 1 {
		t.Fatalf("属性数 = %d, want 1（空名属性丢弃）", len(temp.Spec.Properties))
	}
	p := temp.Spec.Properties["value"]
	if p.Type != "number" || p.Unit != "Cel" || p.Access != model.PropertyRead ||
		len(p.Quality) != 1 || p.Quality[0] != model.QualityGood {
		t.Fatalf("属性字段搬运错误: %+v", p)
	}
	if _, ok := temp.Spec.Events["high"]; !ok {
		t.Fatalf("事件声明丢失: %+v", temp.Spec.Events)
	}
	act, ok := temp.Spec.Actions["calibrate"]
	if !ok || act.InputSchema["type"] != "object" {
		t.Fatalf("action inputSchema 丢失: %+v", temp.Spec.Actions)
	}

	// 非法 JSON Schema 只能降级为 nil，不能让整条文档作废（命令集仍以 action 键为准）。
	led := out[1]
	ledAct, ok := led.Spec.Actions["led"]
	if !ok {
		t.Fatalf("action 键丢失: %+v", led.Spec.Actions)
	}
	if ledAct.InputSchema != nil {
		t.Fatalf("非法 inputSchema 应为 nil, got %+v", ledAct.InputSchema)
	}
	if led.Metadata.Version != 1 {
		t.Fatalf("版本号解析错误: %+v", led.Metadata)
	}
}

// TestCapabilityVersionParsing 锁定 "@N" 版本解析的边界（缺失/非法一律 0，不 panic）。
func TestCapabilityVersionParsing(t *testing.T) {
	cases := map[string]int{
		"a/capability/b@1":   1,
		"a/capability/b@12":  12,
		"a/capability/b@0":   0,
		"a/capability/b":     0,
		"a/capability/b@":    0,
		"a/capability/b@x":   0,
		"a/capability/b@-1":  0,
		"a@1/capability/b@3": 3, // 取最后一个 @
	}
	for id, want := range cases {
		if got := capabilityVersion(id); got != want {
			t.Fatalf("capabilityVersion(%q) = %d, want %d", id, got, want)
		}
	}
}

// This fixture uses the real SDK RPC server/client over a byte-body transport.
// Only unused lifecycle methods are embedded; Describe is not a fake client call.
type actionMetadataRPCDriver struct {
	driver.DriverServer
	descriptor *driver.DriverDescriptor
}

func (d *actionMetadataRPCDriver) Initialize(context.Context, *driver.InitializeRequest) (*driver.InitializeResponse, error) {
	return &driver.InitializeResponse{NegotiatedProtocolVersion: driver.ProtocolVersion}, nil
}

func (d *actionMetadataRPCDriver) Describe(context.Context) (*driver.DriverDescriptor, error) {
	return d.descriptor, nil
}

type actionMetadataRPCHost struct {
	PluginHost
	client driver.DriverClient
}

func (h *actionMetadataRPCHost) DriverClient(string) (driver.DriverClient, error) {
	return h.client, nil
}

// Driver JSON -> SDK decode -> RPC encode/decode -> externalAdapter -> Edge
// capability catalog -> public JSON. Losing any hop must fail this test.
func TestDriverActionMetadataRPCCatalogRoundTrip(t *testing.T) {
	const wire = `{"driver_id":"metadata-fixture","version":"1.0.0","schema_versions":["1"],"capabilities":[{"id":"io.test/capability/settings@1","title":"Settings","properties":[],"events":[],"actions":[{"name":"calibrate","input_schema_json":"{\"type\":\"object\",\"required\":[\"offset\"]}","result_schema_json":"{\"type\":\"boolean\"}","title":"标定","description":"  更新偏移量  ","destructive":true,"confirmation":"执行「标定」？\n设备设置将更新。"},{"name":"preview","input_schema_json":"{}","result_schema_json":"{}","title":"预览","description":"不修改配置","destructive":false,"confirmation":"确认预览？"},{"name":"legacy_action","input_schema_json":"{\"type\":\"object\"}","result_schema_json":"{}"},{"name":"opaque","input_schema_json":"not-json","result_schema_json":"{}","title":"不透明参数","destructive":true,"confirmation":"确认执行该声明动作？"},{"name":"  ","input_schema_json":"{}","result_schema_json":"{}","title":"空名不进命令集","destructive":true}]}]}`
	var descriptor driver.DriverDescriptor
	if err := json.Unmarshal([]byte(wire), &descriptor); err != nil {
		t.Fatalf("decode driver fixture: %v", err)
	}
	clientEnd, serverEnd := transport.Pipe(4)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	server := driver.NewRPCServer(serverEnd, &actionMetadataRPCDriver{descriptor: &descriptor})
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		_ = clientEnd.Close()
		_ = serverEnd.Close()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("RPC server did not stop")
		}
	})
	adapter := newExternalAdapter(&actionMetadataRPCHost{client: driver.NewClient(clientEnd)}, descriptor.DriverID)
	caps := adapter.Capabilities()
	if len(caps) != 1 {
		t.Fatalf("Describe RPC did not yield the capability: %+v", caps)
	}
	if got := caps[0].Spec.Actions["calibrate"]; got.Title != "标定" || got.Description != "  更新偏移量  " || !got.Destructive || got.Confirmation != "执行「标定」？\n设备设置将更新。" {
		t.Fatalf("action metadata lost between Driver RPC and Edge: %+v", got)
	}
	if got := adapter.SupportedCommands(); !reflect.DeepEqual(got, []string{"calibrate", "preview", "legacy_action", "opaque"}) {
		t.Fatalf("metadata changed the command whitelist: %v", got)
	}

	// Serialize the same typed envelope/payload as reportCapabilities. Inspect
	// public JSON keys rather than decoding back into the same model struct.
	data, err := json.Marshal(api.CapabilitiesData{Sources: []api.CapabilitySource{{Source: adapter.Name(), Capabilities: caps}}})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(api.Envelope{V: api.Version, Type: api.MsgCapabilities, Data: data})
	if err != nil {
		t.Fatal(err)
	}
	var received struct {
		Type string `json:"type"`
		Data struct {
			Sources []struct {
				Capabilities []struct {
					Spec struct {
						Actions map[string]any `json:"actions"`
					} `json:"spec"`
				} `json:"capabilities"`
			} `json:"sources"`
		} `json:"data"`
	}
	if err := json.Unmarshal(encoded, &received); err != nil {
		t.Fatalf("decode capability envelope: %v", err)
	}
	if received.Type != string(api.MsgCapabilities) || len(received.Data.Sources) != 1 || len(received.Data.Sources[0].Capabilities) != 1 {
		t.Fatalf("unexpected capability envelope: %s", encoded)
	}
	var want map[string]any
	if err := json.Unmarshal([]byte(`{"calibrate":{"title":"标定","description":"  更新偏移量  ","inputSchema":{"type":"object","required":["offset"]},"destructive":true,"confirmation":"执行「标定」？\n设备设置将更新。"},"preview":{"title":"预览","description":"不修改配置","confirmation":"确认预览？"},"legacy_action":{"inputSchema":{"type":"object"}},"opaque":{"title":"不透明参数","destructive":true,"confirmation":"确认执行该声明动作？"}}`), &want); err != nil {
		t.Fatal(err)
	}
	got := received.Data.Sources[0].Capabilities[0].Spec.Actions
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("public action metadata changed:\n got %#v\nwant %#v\nwire %s", got, want, encoded)
	}
}
