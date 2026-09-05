package appruntime

import (
	"context"
	"errors"
	"testing"
	"time"

	coreapplication "github.com/DeliciousBuding/cloud-path/internal/application"
	sdkapplication "github.com/DeliciousBuding/cloud-path/sdk/go/cloudpath/v1/application"
)

const (
	testCapAlarm   = "cloudpath.dev/capability/alarm@1"
	testCapContact = "cloudpath.dev/capability/contact@1"
)

func testDescriptor() *sdkapplication.ApplicationDescriptor {
	return &sdkapplication.ApplicationDescriptor{
		ApplicationID: "app-sc",
		Version:       "0.1.0",
		Requirements: []sdkapplication.RequirementDescriptor{
			{ID: "reminder-output", Capability: testCapAlarm, Cardinality: "one"},
			{ID: "compartments", Capability: testCapContact, Cardinality: "one-or-more", MinItems: 3},
		},
	}
}

func testCandidates() []coreapplication.Candidate {
	return []coreapplication.Candidate{
		{EntityID: "alarm-1", TenantID: "tenant-a", Capabilities: []string{testCapAlarm}},
		{EntityID: "compartment-1", TenantID: "tenant-a", Capabilities: []string{testCapContact}},
		{EntityID: "compartment-2", TenantID: "tenant-a", Capabilities: []string{testCapContact}},
		{EntityID: "compartment-3", TenantID: "tenant-a", Capabilities: []string{testCapContact}},
		{EntityID: "alarm-foreign", TenantID: "tenant-b", Capabilities: []string{testCapAlarm}},
	}
}

func testBindings() []coreapplication.Binding {
	return []coreapplication.Binding{
		{RequirementID: "reminder-output", EntityID: "alarm-1"},
		{RequirementID: "compartments", EntityID: "compartment-1"},
		{RequirementID: "compartments", EntityID: "compartment-2"},
		{RequirementID: "compartments", EntityID: "compartment-3"},
	}
}

func testSpec() InstanceSpec {
	return InstanceSpec{
		ApplicationID:    "app-sc",
		PluginInstanceID: "inst-1",
		PluginID:         "app-plugin",
		TenantID:         "tenant-a",
		Candidates:       testCandidates(),
		Bindings:         testBindings(),
	}
}

func newTestRuntime(t *testing.T, cli sdkapplication.ApplicationClient, exec EffectExecutor, queueSize int) *Runtime {
	t.Helper()
	rt, err := NewRuntime(RuntimeOptions{
		Dialer:         func(pluginID string) (sdkapplication.ApplicationClient, error) { return cli, nil },
		Executor:       exec,
		EventQueueSize: queueSize,
	})
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	return rt
}

func startTestInstance(t *testing.T, rt *Runtime, spec InstanceSpec) *Instance {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	inst, err := rt.StartInstance(ctx, spec)
	if err != nil {
		t.Fatalf("StartInstance: %v", err)
	}
	return inst
}

func TestRuntimeStartsWithValidBinding(t *testing.T) {
	exec := &fakeExecutor{}
	stream := newFakeStream()
	cli := newFakeClient(testDescriptor(), stream)
	rt := newTestRuntime(t, cli, exec, 0)
	defer rt.Close(context.Background())

	inst := startTestInstance(t, rt, testSpec())
	if inst.State != StateRunning {
		t.Fatalf("state = %s, want running", inst.State)
	}
	if inst.ApplicationID != "app-sc" || inst.TenantID != "tenant-a" {
		t.Fatalf("unexpected instance snapshot: %+v", inst)
	}
	if len(cli.initReqs) != 1 || len(cli.validateReqs) != 1 || len(cli.configureReqs) != 1 {
		t.Fatalf("expected Initialize/ValidateBinding/ConfigureInstance once, got init=%d validate=%d configure=%d",
			len(cli.initReqs), len(cli.validateReqs), len(cli.configureReqs))
	}
	if len(inst.Bindings) != 4 {
		t.Fatalf("bindings = %d, want 4", len(inst.Bindings))
	}
}

func TestRuntimeRejectsInvalidBinding(t *testing.T) {
	exec := &fakeExecutor{}
	stream := newFakeStream()
	cli := newFakeClient(testDescriptor(), stream)
	rt := newTestRuntime(t, cli, exec, 0)
	defer rt.Close(context.Background())

	spec := testSpec()
	spec.Bindings = []coreapplication.Binding{
		{RequirementID: "reminder-output", EntityID: "alarm-foreign"},
		{RequirementID: "compartments", EntityID: "compartment-1"},
		{RequirementID: "compartments", EntityID: "compartment-2"},
		{RequirementID: "compartments", EntityID: "compartment-3"},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := rt.StartInstance(ctx, spec)
	if err == nil {
		t.Fatal("StartInstance accepted a cross-tenant binding")
	}
	var berr *coreapplication.BindingError
	if !errors.As(err, &berr) {
		t.Fatalf("expected *application.BindingError, got %T: %v", err, err)
	}
	if !berr.Result.HasCode(coreapplication.CodeCrossTenant) {
		t.Fatalf("expected cross_tenant issue, got %+v", berr.Result.Issues)
	}
}

func TestRequestAndJobDispatch(t *testing.T) {
	exec := &fakeExecutor{}
	stream := newFakeStream()
	cli := newFakeClient(testDescriptor(), stream)
	rt := newTestRuntime(t, cli, exec, 0)
	defer rt.Close(context.Background())
	startTestInstance(t, rt, testSpec())

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := rt.HandleRequest(ctx, "inst-1", &sdkapplication.PluginHTTPRequest{
		RequestID: "req-1",
		Method:    "GET",
		Path:      "/",
		Context:   sdkapplication.RequestContext{TenantID: "tenant-a"},
	})
	if err != nil {
		t.Fatalf("HandleRequest: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if len(cli.requestReqs) != 1 || cli.requestReqs[0].PluginInstanceID != "inst-1" || cli.requestReqs[0].Context.TenantID != "tenant-a" {
		t.Fatalf("request context not injected: %+v", cli.requestReqs)
	}

	job, err := rt.RunJob(ctx, "inst-1", &sdkapplication.RunJobRequest{JobID: "job-1", IdempotencyKey: "ik-1"})
	if err != nil {
		t.Fatalf("RunJob: %v", err)
	}
	if job.JobID != "job-1" {
		t.Fatalf("job id = %q, want job-1", job.JobID)
	}
	if len(cli.jobReqs) != 1 || cli.jobReqs[0].PluginInstanceID != "inst-1" {
		t.Fatalf("job context not injected: %+v", cli.jobReqs)
	}

	if _, err := rt.HandleRequest(ctx, "inst-1", &sdkapplication.PluginHTTPRequest{
		Method:  "GET",
		Path:    "/",
		Context: sdkapplication.RequestContext{TenantID: "tenant-b"},
	}); !errors.Is(err, ErrTenantMismatch) {
		t.Fatalf("cross-tenant request error = %v, want ErrTenantMismatch", err)
	}
}

// TestStopInstanceStreamOnlySkipsShutdownRPC 锁定共享进程多实例的停机语义：
// 只拆本实例会话（state → stopped），绝不发进程级 Shutdown RPC——参考应用
// 把 Shutdown 当进程退出信号，共享进程上任何实例发送都会连带杀死兄弟。
func TestStopInstanceStreamOnlySkipsShutdownRPC(t *testing.T) {
	exec := &fakeExecutor{}
	stream := newFakeStream()
	cli := newFakeClient(testDescriptor(), stream)
	rt := newTestRuntime(t, cli, exec, 0)
	defer rt.Close(context.Background())
	startTestInstance(t, rt, testSpec())

	if err := rt.StopInstanceStreamOnly("inst-1"); err != nil {
		t.Fatalf("StopInstanceStreamOnly: %v", err)
	}
	if n := cli.ShutdownCount(); n != 0 {
		t.Fatalf("Shutdown RPC 次数 = %d, want 0（共享进程禁发进程级关停）", n)
	}
	inst, err := rt.GetInstance("inst-1")
	if err != nil || inst.State != StateStopped {
		t.Fatalf("state = %s err=%v, want stopped", inst.State, err)
	}
}

// TestRuntimeCloseStopsInstance 锁定常规 Close/StopInstance 仍走优雅 Shutdown。
func TestRuntimeCloseStopsInstance(t *testing.T) {
	exec := &fakeExecutor{}
	stream := newFakeStream()
	cli := newFakeClient(testDescriptor(), stream)
	rt := newTestRuntime(t, cli, exec, 0)
	startTestInstance(t, rt, testSpec())
	if err := rt.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := cli.ShutdownCount(); n < 1 {
		t.Fatalf("Shutdown RPC 次数 = %d, want >=1（最后实例的正常关停仍应优雅）", n)
	}
}

func TestGracefulApplicationShutdown(t *testing.T) {
	exec := &fakeExecutor{}
	stream := newFakeStream()
	cli := newFakeClient(testDescriptor(), stream)
	rt := newTestRuntime(t, cli, exec, 0)
	defer rt.Close(context.Background())
	startTestInstance(t, rt, testSpec())

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := rt.StopInstance(ctx, "inst-1", "maintenance", 2*time.Second); err != nil {
		t.Fatalf("StopInstance: %v", err)
	}

	inst, err := rt.GetInstance("inst-1")
	if err != nil {
		t.Fatalf("GetInstance: %v", err)
	}
	if inst.State != StateStopped {
		t.Fatalf("state = %s, want stopped", inst.State)
	}
	if cli.ShutdownCount() != 1 || cli.LastShutdown().Reason != "maintenance" {
		t.Fatalf("shutdown calls = %d, last = %+v", cli.ShutdownCount(), cli.LastShutdown())
	}

	if err := rt.DispatchEvent(ctx, "inst-1", &sdkapplication.ApplicationEvent{Union: &sdkapplication.InstanceLifecycle{State: "running"}}); !errors.Is(err, ErrInstanceNotRunning) {
		t.Fatalf("dispatch after shutdown error = %v, want ErrInstanceNotRunning", err)
	}
}

func TestEventBackpressure(t *testing.T) {
	exec := &fakeExecutor{}
	stream := newFakeStream()
	stream.sendDelay = make(chan struct{})
	stream.sendStarted = make(chan struct{}, 1)
	cli := newFakeClient(testDescriptor(), stream)
	rt := newTestRuntime(t, cli, exec, 1)
	defer rt.Close(context.Background())
	startTestInstance(t, rt, testSpec())

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	event := func(id string) *sdkapplication.ApplicationEvent {
		return &sdkapplication.ApplicationEvent{Union: &sdkapplication.InstanceLifecycle{State: id}}
	}
	if err := rt.DispatchEvent(ctx, "inst-1", event("one")); err != nil {
		t.Fatalf("dispatch one: %v", err)
	}
	select {
	case <-stream.sendStarted:
	case <-time.After(time.Second):
		t.Fatal("sender never started the blocked Send")
	}

	if err := rt.DispatchEvent(ctx, "inst-1", event("two")); err != nil {
		t.Fatalf("dispatch two: %v", err)
	}

	shortCtx, shortCancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer shortCancel()
	err := rt.DispatchEvent(shortCtx, "inst-1", event("three"))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("dispatch three error = %v, want DeadlineExceeded", err)
	}

	close(stream.sendDelay)
	waitForSent := func(n int) {
		deadline := time.Now().Add(time.Second)
		for time.Now().Before(deadline) {
			if stream.SentCount() >= n {
				return
			}
			time.Sleep(time.Millisecond)
		}
		t.Fatalf("sent = %d, want >= %d", stream.SentCount(), n)
	}
	waitForSent(2)

	if err := rt.DispatchEvent(ctx, "inst-1", event("three")); err != nil {
		t.Fatalf("dispatch three after release: %v", err)
	}
	waitForSent(3)
}

func TestEffectIdempotency(t *testing.T) {
	exec := &fakeExecutor{}
	stream := newFakeStream()
	cli := newFakeClient(testDescriptor(), stream)
	rt := newTestRuntime(t, cli, exec, 0)
	defer rt.Close(context.Background())
	startTestInstance(t, rt, testSpec())

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	mk := func() Effect {
		return Effect{
			ID:               "effect-1",
			IdempotencyKey:   "notify-1",
			TenantID:         "tenant-a",
			Kind:             EffectSendNotification,
			SendNotification: &SendNotification{Title: "hello", Body: "world", Severity: "info"},
		}
	}
	res, err := rt.ExecuteEffects(ctx, "inst-1", []Effect{mk(), mk()})
	if err != nil {
		t.Fatalf("ExecuteEffects: %v", err)
	}
	if res.Executed != 1 {
		t.Fatalf("executed = %d, want 1", res.Executed)
	}
	if len(res.Results) != 2 || !res.Results[1].Duplicate {
		t.Fatalf("duplicate not recognized: %+v", res.Results)
	}
	if exec.Count() != 1 {
		t.Fatalf("executor calls = %d, want 1", exec.Count())
	}
}

// TestDomainRecordUpsertKeyIsContentAddressed 锁定 upsert 去重语义：
// 同 (record_type, record_id) 不同内容 = 不同幂等键（更新必须执行）；
// 同内容重放 = 同键（崩溃重投仍幂等）。
//
// 回归背景（2026-09-05 真板实测）：键只含 record_type/record_id 时，
// UpsertDomainRecord 被降级成一次性 create——窗口记录 opened→completed/
// missed、提醒回执落痕的全部更新都被 Duplicate 静默吞掉。
func TestDomainRecordUpsertKeyIsContentAddressed(t *testing.T) {
	src := EffectSource{PluginInstanceID: "inst-1", TenantID: "tenant-a"}
	mk := func(data string) Effect {
		raw := &sdkapplication.ApplicationEffect{
			PluginInstanceID: "inst-1",
			Union: &sdkapplication.UpsertDomainRecord{
				RecordType: "window", RecordID: "w-1", DataJSON: data, Version: "1",
			},
		}
		e, err := EffectFromSDK(raw, src)
		if err != nil {
			t.Fatalf("EffectFromSDK: %v", err)
		}
		return e
	}
	opened := mk(`{"state":"opened"}`)
	openedReplay := mk(`{"state":"opened"}`)
	completed := mk(`{"state":"completed","closed_at":"..."}`)
	if opened.IdempotencyKey != openedReplay.IdempotencyKey {
		t.Fatalf("同内容重放必须同键（幂等）: %q vs %q", opened.IdempotencyKey, openedReplay.IdempotencyKey)
	}
	if opened.IdempotencyKey == completed.IdempotencyKey {
		t.Fatalf("不同内容必须不同键（upsert 更新会被去重吞掉）: %q", opened.IdempotencyKey)
	}

	// 端到端：同一实例先后执行 opened 与 completed，两个都必须真正执行。
	exec := &fakeExecutor{}
	stream := newFakeStream()
	cli := newFakeClient(testDescriptor(), stream)
	rt := newTestRuntime(t, cli, exec, 0)
	defer rt.Close(context.Background())
	startTestInstance(t, rt, testSpec())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := rt.ExecuteEffects(ctx, "inst-1", []Effect{opened, completed})
	if err != nil {
		t.Fatalf("ExecuteEffects: %v", err)
	}
	if res.Executed != 2 {
		t.Fatalf("executed = %d, want 2（状态更新不得被去重）", res.Executed)
	}
	res, err = rt.ExecuteEffects(ctx, "inst-1", []Effect{openedReplay})
	if err != nil {
		t.Fatalf("ExecuteEffects replay: %v", err)
	}
	if res.Executed != 0 || len(res.Results) != 1 || !res.Results[0].Duplicate {
		t.Fatalf("同内容重放必须被识别为 Duplicate: %+v", res)
	}
}

func TestRejectUnknownEffect(t *testing.T) {
	exec := &fakeExecutor{}
	stream := newFakeStream()
	cli := newFakeClient(testDescriptor(), stream)
	rt := newTestRuntime(t, cli, exec, 0)
	defer rt.Close(context.Background())
	startTestInstance(t, rt, testSpec())

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	bad := Effect{
		ID:               "bad-1",
		IdempotencyKey:   "bad-key",
		TenantID:         "tenant-a",
		Kind:             EffectKind("exec_sql"),
		SendNotification: &SendNotification{Title: "x", Body: "DROP TABLE", Severity: "info"},
	}
	res, err := rt.ExecuteEffects(ctx, "inst-1", []Effect{bad})
	if err == nil {
		t.Fatal("ExecuteEffects accepted an unknown effect kind")
	}
	if res.Failed != 1 {
		t.Fatalf("failed = %d, want 1", res.Failed)
	}
	if exec.Count() != 0 {
		t.Fatalf("executor called %d times, want 0", exec.Count())
	}

	raw := &sdkapplication.ApplicationEffect{
		PluginInstanceID: "inst-1",
		SchemaVersion:    sdkapplication.SchemaVersion,
		Union:            &sdkapplication.DeleteDomainRecord{RecordType: "x", RecordID: "1"},
	}
	src := EffectSource{PluginInstanceID: "inst-1", TenantID: "tenant-a", Bindings: testBindings(), Candidates: testCandidates()}
	if _, err := EffectFromSDK(raw, src); !errors.Is(err, ErrUnknownEffect) {
		t.Fatalf("EffectFromSDK error = %v, want ErrUnknownEffect", err)
	}
}

func TestRejectCrossTenantEffect(t *testing.T) {
	exec := &fakeExecutor{}
	stream := newFakeStream()
	cli := newFakeClient(testDescriptor(), stream)
	rt := newTestRuntime(t, cli, exec, 0)
	defer rt.Close(context.Background())
	startTestInstance(t, rt, testSpec())

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	bad := Effect{
		ID:               "bad-2",
		IdempotencyKey:   "bad-key-2",
		TenantID:         "tenant-b",
		Kind:             EffectSendNotification,
		SendNotification: &SendNotification{Title: "x", Body: "y", Severity: "info"},
	}
	res, err := rt.ExecuteEffects(ctx, "inst-1", []Effect{bad})
	if !errors.Is(err, ErrTenantMismatch) {
		t.Fatalf("ExecuteEffects error = %v, want ErrTenantMismatch", err)
	}
	if res.Failed != 1 || exec.Count() != 0 {
		t.Fatalf("failed = %d, executor calls = %d", res.Failed, exec.Count())
	}

	raw := &sdkapplication.ApplicationEffect{
		PluginInstanceID: "inst-1",
		SchemaVersion:    sdkapplication.SchemaVersion,
		Union: &sdkapplication.RequestCommand{
			EntityID:       "alarm-foreign",
			Action:         "beep",
			IdempotencyKey: "cmd-1",
		},
	}
	src := EffectSource{PluginInstanceID: "inst-1", TenantID: "tenant-a", Bindings: testBindings(), Candidates: testCandidates()}
	if _, err := EffectFromSDK(raw, src); !errors.Is(err, ErrCrossTenantEffect) {
		t.Fatalf("EffectFromSDK error = %v, want ErrCrossTenantEffect", err)
	}
}

func TestBatchFailFastPartialSuccess(t *testing.T) {
	exec := &fakeExecutor{}
	stream := newFakeStream()
	cli := newFakeClient(testDescriptor(), stream)
	rt := newTestRuntime(t, cli, exec, 0)
	defer rt.Close(context.Background())
	startTestInstance(t, rt, testSpec())

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	good := Effect{
		ID:               "good-1",
		IdempotencyKey:   "good-key",
		TenantID:         "tenant-a",
		Kind:             EffectSendNotification,
		SendNotification: &SendNotification{Title: "ok", Body: "ok", Severity: "info"},
	}
	bad := Effect{
		ID:               "bad-3",
		IdempotencyKey:   "bad-key-3",
		TenantID:         "tenant-b",
		Kind:             EffectSendNotification,
		SendNotification: &SendNotification{Title: "bad", Body: "bad", Severity: "info"},
	}
	res, err := rt.ExecuteEffects(ctx, "inst-1", []Effect{good, bad})
	if !errors.Is(err, ErrTenantMismatch) {
		t.Fatalf("ExecuteEffects error = %v, want ErrTenantMismatch", err)
	}
	if res.Executed != 1 || res.Failed != 1 {
		t.Fatalf("executed = %d failed = %d, want partial success 1/1", res.Executed, res.Failed)
	}
	if exec.Count() != 1 {
		t.Fatalf("executor calls = %d, want 1", exec.Count())
	}
}
