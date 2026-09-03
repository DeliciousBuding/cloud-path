package plugincatalog

import (
	"errors"
	"strings"
	"testing"

	"github.com/DeliciousBuding/cloud-path/internal/api"
)

// stubProjection 是 ProjectionSource 的测试替身：按租户 slug 返回预置投影。
type stubProjection struct {
	installations map[string][]api.PluginInstallationStatusData
	instances     map[string][]ProjectionInstance
	err           error
}

func (s stubProjection) Installations(tenant string) ([]api.PluginInstallationStatusData, error) {
	return s.installations[tenant], s.err
}

func (s stubProjection) Instances(tenant string) ([]ProjectionInstance, error) {
	return s.instances[tenant], s.err
}

func sampleProjection() stubProjection {
	return stubProjection{
		installations: map[string][]api.PluginInstallationStatusData{
			"tenant-a": {{
				PluginID: "io.github.acme.driver", Version: "0.1.0", Kind: "Driver", Protocol: 1,
				Digest: "sha256:aa", Verified: true, VerifiedPublisher: "acme",
				Permissions: api.PluginPermissionsData{Secrets: []string{"api_token"}},
				Contributions: api.PluginContributionsData{Drivers: []api.PluginDriverContributionData{
					{ID: "stcb", Title: "STC-B Driver", Discovery: "manual"},
				}},
			}},
			"tenant-b": {{PluginID: "io.github.other.app", Version: "1.0.0", Kind: "Application"}},
		},
		instances: map[string][]ProjectionInstance{
			"tenant-a": {
				{
					TenantID: 1, Tenant: "tenant-a", EdgeID: "e1", InstanceID: "box1",
					PluginID: "io.github.acme.driver", Version: "0.1.0", Enabled: true,
					Isolation: "shared", Config: map[string]string{"api_token": "secret://api_token"},
					SecretRefs: []string{"api_token"}, ConfigPresent: true,
					HasObserved: true, State: "HEALTHY", Health: "HEALTHY", ObservedVersion: "0.1.0",
					EdgeOnline: true, DesiredRevision: 3, AppliedRevision: 3, LastAckAt: 99,
					UpdatedAt: 55, RowRevision: 3, RestartCount: 1, MessageRate: 2.5, LastHealthy: 77,
				},
				{
					TenantID: 1, Tenant: "tenant-a", EdgeID: "e2", InstanceID: "box2",
					PluginID: "io.github.acme.driver", Version: "0.1.0", Enabled: true,
					Isolation: "per-instance", HasObserved: false,
					EdgeOnline: false, DesiredRevision: 2, AppliedRevision: 0, Stale: true, Drift: true,
				},
			},
		},
	}
}

// TestAPIInstanceViewSeparatesDesiredObserved 锁定不变量 5 的视图映射：
// desired 与 observed 分别承载，未上报时 Observed 为 nil，字段不互相冒充。
func TestAPIInstanceViewSeparatesDesiredObserved(t *testing.T) {
	src := sampleProjection()
	views, err := InstanceViews(src, "tenant-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 2 {
		t.Fatalf("视图数 = %d, want 2", len(views))
	}
	byID := map[string]api.PluginInstanceView{}
	for _, v := range views {
		byID[v.ID] = v
	}
	got := byID["box1"]
	if got.TenantID != 1 || got.EdgeID != "e1" || !got.HasObserved || got.Observed == nil {
		t.Fatalf("box1 视图错误: %+v", got)
	}
	if got.Desired.PluginID != "io.github.acme.driver" || !got.Desired.Enabled ||
		got.Desired.Revision != 3 || got.Desired.UpdatedAt != 55 ||
		got.Desired.Config["api_token"] != "secret://api_token" ||
		len(got.Desired.SecretRefs) != 1 {
		t.Fatalf("box1 desired 字段错误: %+v", got.Desired)
	}
	if got.Observed.State != "HEALTHY" || got.Observed.Version != "0.1.0" ||
		got.Observed.RestartCount != 1 {
		t.Fatalf("box1 observed 字段错误: %+v", got.Observed)
	}
	if got.Drift || got.Stale || !got.EdgeOnline || got.AppliedRevision != 3 || got.LastAckAt != 99 {
		t.Fatalf("box1 drift/stale/revision 错误: %+v", got)
	}

	// 未上报：Observed 必须缺席，且 desired enabled 不得被渲染成健康。
	miss := byID["box2"]
	if miss.HasObserved || miss.Observed != nil {
		t.Fatalf("box2 未上报却给出 observed: %+v", miss)
	}
	if !miss.Drift || !miss.Stale || miss.EdgeOnline || miss.DesiredRevision != 2 || miss.AppliedRevision != 0 {
		t.Fatalf("box2 drift/stale 计算错误: %+v", miss)
	}
	if miss.Desired.Config != nil {
		t.Fatalf("无配置却给出 config map: %+v", miss.Desired.Config)
	}

	// 配置 map 必须是副本：改动响应不得回写内部缓存。
	got.Desired.Config["api_token"] = "mutated"
	again, err := InstanceViews(src, "tenant-a")
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range again {
		if v.ID == "box1" && v.Desired.Config["api_token"] != "secret://api_token" {
			t.Fatalf("config 未做副本，内部状态被响应改动污染: %+v", v.Desired.Config)
		}
	}
}

// TestInstanceViewsTenantScopeAndErrors 锁定租户作用域与源错误传播。
func TestInstanceViewsTenantScopeAndErrors(t *testing.T) {
	src := sampleProjection()
	views, err := InstanceViews(src, "tenant-b")
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 0 {
		t.Fatalf("tenant-b 看到 tenant-a 实例: %+v", views)
	}
	if _, err := InstanceViews(src, ""); err != nil {
		t.Fatalf("空租户（全局形态）应可用: %v", err)
	}
	if views, err := InstanceViews(nil, "tenant-a"); err != nil || len(views) != 0 {
		t.Fatalf("nil 源必须返回空而不是错误: %+v %v", views, err)
	}
	broken := sampleProjection()
	broken.err = errors.New("projection unavailable")
	if _, err := InstanceViews(broken, "tenant-a"); err == nil {
		t.Fatal("源错误必须传播（调用方映射为 500），不得静默返回空列表")
	}
}

// TestProjectionCatalogReadsRealSource 锁定目录读面来自注入的投影源：
// 安装物/实例都可查，nil 源返回空目录且不 panic。
func TestProjectionCatalogReadsRealSource(t *testing.T) {
	src := sampleProjection()
	c := NewProjectionCatalog(src)

	plugins, err := c.Plugins("tenant-a")
	if err != nil || len(plugins) != 1 {
		t.Fatalf("plugins = %+v err=%v", plugins, err)
	}
	p := plugins[0]
	if p.ID != "io.github.acme.driver" || p.Kind != "Driver" || !p.Verified ||
		p.Permissions.Secrets[0] != "api_token" || p.Contributes.Drivers[0].ID != "stcb" {
		t.Fatalf("plugin 视图字段错误: %+v", p)
	}
	one, ok, err := c.Plugin("tenant-a", "io.github.acme.driver")
	if err != nil || !ok || one.ID != p.ID {
		t.Fatalf("单插件查询 = %+v ok=%v err=%v", one, ok, err)
	}
	if _, ok, err := c.Plugin("tenant-b", "io.github.acme.driver"); err != nil || ok {
		t.Fatalf("跨租户插件查询应未找到: ok=%v err=%v", ok, err)
	}
	instances, err := c.Instances("tenant-a")
	if err != nil || len(instances) != 2 {
		t.Fatalf("instances = %+v err=%v", instances, err)
	}
	for _, in := range instances {
		switch in.ID {
		case "box1":
			if in.ObservedState != "HEALTHY" || in.Health != "HEALTHY" || !in.ConfigPresent ||
				in.EdgeID != "e1" || in.Metrics.RestartCount != 1 || in.Metrics.MessageRate != 2.5 {
				t.Fatalf("box1 legacy 视图错误: %+v", in)
			}
			if in.Metrics.CPUTime != -1 || in.Metrics.Handles != -1 || in.Metrics.Goroutines != -1 {
				t.Fatalf("不可观测指标必须标 -1: %+v", in.Metrics)
			}
		case "box2":
			// 无 observed 或 edge 离线：state/health 恒 unknown，绝不按 desired 虚报。
			if in.ObservedState != "unknown" || in.Health != "unknown" || !in.Stale || !in.Drift {
				t.Fatalf("box2 legacy 视图错误: %+v", in)
			}
		}
	}
	single, ok, err := c.Instance("tenant-a", "box2")
	if err != nil || !ok || single.EdgeID != "e2" {
		t.Fatalf("单实例查询 = %+v ok=%v err=%v", single, ok, err)
	}

	empty := NewProjectionCatalog(nil)
	if list, err := empty.Plugins("tenant-a"); err != nil || len(list) != 0 {
		t.Fatalf("nil 源目录应为空: %+v %v", list, err)
	}
	if list, err := empty.Instances("tenant-a"); err != nil || len(list) != 0 {
		t.Fatalf("nil 源实例应为空: %+v %v", list, err)
	}
	if _, ok, err := empty.Instance("tenant-a", "box1"); err != nil || ok {
		t.Fatalf("nil 源单实例应未找到: ok=%v err=%v", ok, err)
	}
}

// TestObservedNotTrustedWhenEdgeOffline 锁定：edge 离线时即使有 observed 投影，
// 历史 InstanceView 也必须呈现 unknown（投影过期只标记，不虚报健康）。
func TestObservedNotTrustedWhenEdgeOffline(t *testing.T) {
	src := stubProjection{instances: map[string][]ProjectionInstance{
		"tenant-a": {{
			TenantID: 1, Tenant: "tenant-a", EdgeID: "e1", InstanceID: "box1",
			PluginID: "p1", Version: "1.0.0", Enabled: true, HasObserved: true,
			State: "HEALTHY", Health: "HEALTHY", EdgeOnline: false, Stale: true,
			DesiredRevision: 1, AppliedRevision: 1,
		}},
	}}
	views, err := NewProjectionCatalog(src).Instances("tenant-a")
	if err != nil || len(views) != 1 {
		t.Fatalf("instances = %+v err=%v", views, err)
	}
	if views[0].ObservedState != "unknown" || views[0].Health != "unknown" || !views[0].Stale {
		t.Fatalf("edge 离线时虚报了 observed: %+v", views[0])
	}
	// 契约视图仍保留最后一次真实上报（标 stale），供 UI 显示「未上报/过期」。
	apiViews, err := InstanceViews(src, "tenant-a")
	if err != nil {
		t.Fatal(err)
	}
	if apiViews[0].Observed == nil || apiViews[0].Observed.State != "HEALTHY" || !apiViews[0].Stale {
		t.Fatalf("契约视图应保留真实上报并标 stale: %+v", apiViews[0])
	}
}

// TestSanitizeDetail 锁定暗卷 10 的最后一道闸：本机绝对路径与疑似凭据必须被脱敏，
// 且长度有界。
func TestSanitizeDetail(t *testing.T) {
	cases := []struct{ in, banned, want string }{
		{`open C:\Users\ding\secrets\api.txt: denied`, `C:\Users`, "[path]"},
		{`read /home/ding/.config/cloudpath/token.json failed`, "/home/ding", "[path]"},
		{`stat \\fileserver\share\plugin.dll error`, `\\fileserver`, "[path]"},
		{`password=hunter2 rejected`, "hunter2", "[REDACTED]"},
		{`api_key: abc123xyz`, "abc123xyz", "[REDACTED]"},
		{`authorization "Bearer zz9"`, "zz9", "[REDACTED]"},
	}
	for _, c := range cases {
		got := SanitizeDetail(c.in)
		if strings.Contains(got, c.banned) {
			t.Fatalf("SanitizeDetail(%q) 泄漏 %q: %q", c.in, c.banned, got)
		}
		if !strings.Contains(got, c.want) {
			t.Fatalf("SanitizeDetail(%q) = %q, want 含 %q", c.in, got, c.want)
		}
	}
	if got := SanitizeDetail("   "); got != "" {
		t.Fatalf("空白摘要应归一为空: %q", got)
	}
	long := SanitizeDetail(strings.Repeat("a", 4000))
	if len(long) > maxDetailLen+len("…") {
		t.Fatalf("摘要未截断: len=%d", len(long))
	}
	// 正常错误文本不受影响（不得把有用信息全抹掉）。
	if got := SanitizeDetail("plugin exited with code 3"); got != "plugin exited with code 3" {
		t.Fatalf("普通摘要被误伤: %q", got)
	}
}
