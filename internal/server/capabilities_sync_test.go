package server

import (
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/DeliciousBuding/cloud-path/internal/api"
	"github.com/DeliciousBuding/cloud-path/internal/model"
)

// ---- Edge 上报的 Capability 文档进入 /api/capabilities（设备无关）----
//
// 为什么必须有这条回归：外部 Driver 的能力文档（标题/属性/action inputSchema）只存在于
// Edge 侧插件进程里，而前端 Schema 驱动 UI 的命令面板读的是 Server 的 /api/capabilities。
// 缺了它，装了新 Driver 的设备在 WebUI 上只有裸观测值、没有任何命令按钮。

const extCapID = "io.github.example/capability/ext-sensor@1"

func extCapability(title string) model.Capability {
	return model.Capability{
		APIVersion: model.CapabilityAPIVersion,
		Kind:       model.CapabilityKind,
		Metadata:   model.CapabilityMetadata{ID: extCapID, Version: 1, Title: title},
		Spec: model.CapabilitySpec{
			Properties: map[string]model.Property{
				"value": {Type: "number", Unit: "Cel", Access: model.PropertyRead},
			},
			Actions: map[string]model.ActionDecl{
				"diag": {InputSchema: map[string]any{"type": "object"}},
			},
		},
	}
}

func fetchCatalog(t *testing.T, url string) map[string]model.Capability {
	t.Helper()
	var resp struct {
		Capabilities []model.Capability `json:"capabilities"`
	}
	getJSON(t, url+"/api/capabilities", &resp)
	out := make(map[string]model.Capability, len(resp.Capabilities))
	for _, c := range resp.Capabilities {
		out[c.Metadata.ID] = c
	}
	return out
}

func waitCatalog(t *testing.T, url string, cond func(map[string]model.Capability) bool, why string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if cond(fetchCatalog(t, url)) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%s（超时 20s）", why)
}

func reportCapabilities(t *testing.T, ws *websocket.Conn, data api.CapabilitiesData) {
	t.Helper()
	writeEnv(t, ws, api.Envelope{V: api.Version, Type: api.MsgCapabilities,
		Ts: time.Now().Unix(), Data: rawData(t, data)})
}

func oneSource(docs ...model.Capability) api.CapabilitiesData {
	return api.CapabilitiesData{Sources: []api.CapabilitySource{
		{Source: "ext-driver", Capabilities: docs},
	}}
}

func TestEdgeReportedCapabilitiesEnterCatalog(t *testing.T) {
	srv, ts := setup(t)
	ews := dial(t, wsURL(ts.URL, "/ws/edge"))
	writeEnv(t, ews, api.Envelope{
		V: api.Version, Type: api.MsgHello, Ts: time.Now().Unix(),
		Data: rawData(t, api.HelloData{
			EdgeID: "e-ext", Version: "test",
			Devices: []api.DeviceMeta{{ID: "d1", Adapter: "ext-driver", Name: "外部驱动设备"}},
		}),
	})

	// 1) 合法文档进 catalog，非法文档单条跳过（一个坏插件不得拖垮整批）。
	bad := extCapability("外部传感器")
	bad.APIVersion = "wrong/v1"
	reportCapabilities(t, ews, oneSource(extCapability("外部传感器"), bad))
	waitCatalog(t, ts.URL, func(m map[string]model.Capability) bool {
		c, ok := m[extCapID]
		if !ok || c.Metadata.Title != "外部传感器" {
			return false
		}
		if _, has := c.Spec.Actions["diag"]; !has {
			return false // action inputSchema 是命令面板的事实源，不能丢
		}
		_, demo := m["cloudpath.dev/capability/toggle@1"]
		return demo // 进程内 catalog 必须仍在：合并而非替换
	}, "Edge 上报的 Capability 文档未进入 /api/capabilities")

	// 2) 同一 ID 进程内优先：插件不得改写平台契约语义。
	reportCapabilities(t, ews, oneSource(model.Capability{
		APIVersion: model.CapabilityAPIVersion, Kind: model.CapabilityKind,
		Metadata: model.CapabilityMetadata{
			ID: "cloudpath.dev/capability/toggle@1", Version: 1, Title: "被插件改写的标题",
		},
	}))
	time.Sleep(300 * time.Millisecond)
	if got := fetchCatalog(t, ts.URL)["cloudpath.dev/capability/toggle@1"].Metadata.Title; got == "被插件改写的标题" {
		t.Fatal("插件改写了平台进程内 Capability 文档：同 ID 必须以 Server 进程内为准")
	}

	// 3) 全量覆盖语义：报空集即摘除（插件停用/卸载后不留幽灵能力）。
	reportCapabilities(t, ews, api.CapabilitiesData{})
	waitCatalog(t, ts.URL, func(m map[string]model.Capability) bool {
		_, ok := m[extCapID]
		return !ok
	}, "上报空集后 Capability 文档未从 catalog 摘除")

	// 4) 断线清理：文档随连接生命周期存在，离线 Edge 的能力不得继续挂在 catalog 上。
	reportCapabilities(t, ews, oneSource(extCapability("外部传感器")))
	waitCatalog(t, ts.URL, func(m map[string]model.Capability) bool {
		_, ok := m[extCapID]
		return ok
	}, "重新上报后 Capability 文档未回到 catalog")

	ews.CloseNow()
	waitEdgeOffline(t, srv, "e-ext")
	waitCatalog(t, ts.URL, func(m map[string]model.Capability) bool {
		_, ok := m[extCapID]
		return !ok
	}, "Edge 断线后 Capability 文档未清理")
}

// TestCapabilitiesMsgRejectsOversizedBatch 锁定规模上限：超限整批拒绝并保留旧文档，
// 不得让一个插件把 catalog 撑爆（单条 WS 消息另有 wsReadLimit 兜底）。
func TestCapabilitiesMsgRejectsOversizedBatch(t *testing.T) {
	_, ts := setup(t)
	ews := dial(t, wsURL(ts.URL, "/ws/edge"))
	writeEnv(t, ews, api.Envelope{
		V: api.Version, Type: api.MsgHello, Ts: time.Now().Unix(),
		Data: rawData(t, api.HelloData{EdgeID: "e-big", Version: "test",
			Devices: []api.DeviceMeta{{ID: "d1", Adapter: "ext-driver"}}}),
	})
	reportCapabilities(t, ews, oneSource(extCapability("外部传感器")))
	waitCatalog(t, ts.URL, func(m map[string]model.Capability) bool {
		_, ok := m[extCapID]
		return ok
	}, "首次上报未生效")

	// 用最小文档：整批仍须落在 wsReadLimit(64KB) 之内，才能命中语义上限而不是传输上限。
	tooMany := make([]model.Capability, maxCapabilitiesPerSource+1)
	for i := range tooMany {
		tooMany[i] = model.Capability{
			APIVersion: model.CapabilityAPIVersion, Kind: model.CapabilityKind,
			Metadata: model.CapabilityMetadata{ID: extCapID, Version: 1},
		}
	}
	reportCapabilities(t, ews, oneSource(tooMany...))
	time.Sleep(300 * time.Millisecond)
	if _, ok := fetchCatalog(t, ts.URL)[extCapID]; !ok {
		t.Fatal("超限批次应被整批拒绝并保留旧文档，实际旧文档被清掉了")
	}
}
