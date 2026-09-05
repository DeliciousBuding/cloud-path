package server

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"testing"
	"time"

	"github.com/DeliciousBuding/cloud-path/internal/appruntime"
	"github.com/DeliciousBuding/cloud-path/internal/store"
	sdkapplication "github.com/DeliciousBuding/cloud-path/sdk/go/cloudpath/v1/application"
)

// ---- D2 Durable Scheduler 测试 ----
//
// 覆盖：cron 表达式解析/求值（含时区与永不触发）、store upsert/cancel/claim
// 生命周期、调度决策四语义（准点派发、停机 skip、run_once 补一次、节奏保持）、
// 坏 cron 防御性撤销。

func TestCronExprNextAfter(t *testing.T) {
	shanghai, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 9, 5, 10, 30, 0, 0, shanghai)

	cases := []struct {
		name string
		cron string
		from time.Time
		want time.Time
	}{
		{"every minute", "* * * * *", base, base.Add(time.Minute)},
		{"hourly", "0 * * * *", base, time.Date(2026, 9, 5, 11, 0, 0, 0, shanghai)},
		{"daily 0630", "30 6 * * *", base, time.Date(2026, 9, 6, 6, 30, 0, 0, shanghai)},
		{"step minutes", "*/15 * * * *", base, time.Date(2026, 9, 5, 10, 45, 0, 0, shanghai)},
		{"range+list", "10-20,40 9 * * *", base, time.Date(2026, 9, 6, 9, 10, 0, 0, shanghai)},
		{"month wrap", "0 0 1 2 *", base, time.Date(2027, 2, 1, 0, 0, 0, 0, shanghai)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			expr, err := parseCronExpr(tc.cron)
			if err != nil {
				t.Fatalf("parse %q: %v", tc.cron, err)
			}
			if got := expr.nextAfter(tc.from, shanghai); !got.Equal(tc.want) {
				t.Fatalf("nextAfter(%q) = %s, want %s", tc.cron, got, tc.want)
			}
		})
	}

	// 时区解释：UTC 视角下次日 06:30
	utcExpr, _ := parseCronExpr("30 6 * * *")
	got := utcExpr.nextAfter(time.Date(2026, 9, 5, 23, 0, 0, 0, time.UTC), time.UTC)
	if got.Hour() != 6 || got.Day() != 6 {
		t.Fatalf("utc next = %s, want 06:30 next day", got)
	}

	for _, bad := range []string{"", "* * * *", "60 * * * *", "* 24 * * *", "a * * * *",
		"*/0 * * * *", "5-1 * * * *", "* * 0 * *", "* * * 13 *", "* * * * 7"} {
		if _, err := parseCronExpr(bad); err == nil {
			t.Fatalf("parseCronExpr(%q) should fail", bad)
		}
	}

	// 2 月 30 日：永不触发 → 零值（调度器据此撤销）
	nofire, err := parseCronExpr("0 0 30 2 *")
	if err != nil {
		t.Fatal(err)
	}
	if got := nofire.nextAfter(base, shanghai); !got.IsZero() {
		t.Fatalf("feb-30 should never fire, got %s", got)
	}
}

func TestScheduledJobStoreLifecycle(t *testing.T) {
	_, _, st, _ := setupAppPlane(t)

	row, err := st.UpsertScheduledJob(store.ScheduledJobRow{
		TenantID: 1, InstanceID: "box-1", ScheduleID: "daily-report",
		Cron: "30 6 * * *", Timezone: "Asia/Shanghai", PayloadJSON: `{"k":1}`,
		MissedPolicy: "skip", NextRunAt: 1788568200,
	})
	if err != nil {
		t.Fatal(err)
	}
	if row.State != "active" || row.Revision != 1 {
		t.Fatalf("inserted = %+v", row)
	}

	// 重声明 = 更新 + revision+1（cancel 后重声明也回 active）
	if _, err := st.UpsertScheduledJob(store.ScheduledJobRow{
		TenantID: 1, InstanceID: "box-1", ScheduleID: "daily-report",
		Cron: "45 6 * * *", Timezone: "Asia/Shanghai",
		MissedPolicy: "skip", NextRunAt: 1788569100,
	}); err != nil {
		t.Fatal(err)
	}
	jobs, err := st.ListScheduledJobs(1, "box-1")
	if err != nil || len(jobs) != 1 {
		t.Fatalf("list = %d err=%v", len(jobs), err)
	}
	if jobs[0].Cron != "45 6 * * *" || jobs[0].Revision != 2 || jobs[0].State != "active" {
		t.Fatalf("updated = %+v", jobs[0])
	}

	// due 扫描边界：next_run_at<=now 才出现
	if due, err := st.ListScheduledJobsDue(1788569099); err != nil || len(due) != 0 {
		t.Fatalf("not yet due: %d err=%v", len(due), err)
	}
	due, err := st.ListScheduledJobsDue(1788569100)
	if err != nil || len(due) != 1 || due[0].ScheduleID != "daily-report" {
		t.Fatalf("due: %+v err=%v", due, err)
	}

	// cancel → 不再 due；list 仍可见（state 自述）
	if err := st.CancelScheduledJob(1, "box-1", "daily-report"); err != nil {
		t.Fatal(err)
	}
	if due, _ := st.ListScheduledJobsDue(1 << 40); len(due) != 0 {
		t.Fatalf("cancelled job still due: %+v", due)
	}
	jobs, _ = st.ListScheduledJobs(1, "box-1")
	if len(jobs) != 1 || jobs[0].State != "cancelled" {
		t.Fatalf("cancelled list = %+v", jobs)
	}

	// claim 推进（at-most-once 的持久基础）
	if err := st.ClaimScheduledJobRun(1, "box-1", "daily-report", 1788655500, 1788569100,
		"sj|box-1|daily-report|1788569100"); err != nil {
		t.Fatal(err)
	}
	jobs, _ = st.ListScheduledJobs(1, "box-1")
	if jobs[0].NextRunAt != 1788655500 || jobs[0].LastRunAt != 1788569100 ||
		jobs[0].LastDispatch != "sj|box-1|daily-report|1788569100" {
		t.Fatalf("claimed = %+v", jobs[0])
	}

	// 租户隔离
	if other, _ := st.ListScheduledJobs(2, "box-1"); len(other) != 0 {
		t.Fatalf("tenant leak: %+v", other)
	}
}

// newSchedulerTestHost 构造带真实 appruntime 的 AppHost（无运行实例——RunJob
// 在 claim 之后失败走 warn 分支，持久决策仍可从 DB 读回）。cron 校验在效果层，
// UpsertScheduledJob 不拦非法 cron，调度器防御测试可直接落库。
func newSchedulerTestHost(t *testing.T, srv *Server) *AppHost {
	t.Helper()
	rt, err := appruntime.NewRuntime(appruntime.RuntimeOptions{
		Dialer: func(string) (sdkapplication.ApplicationClient, error) {
			return nil, errors.New("no app in scheduler unit test")
		},
		Executor: noopEffectExecutor{},
		Logger:   slog.Default(),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Close(context.Background()) })
	return &AppHost{srv: srv, logger: slog.Default(), rt: rt}
}

type noopEffectExecutor struct{}

func (noopEffectExecutor) Execute(context.Context, appruntime.Effect) error { return nil }

func TestSchedulerOntimeDispatchAndCadence(t *testing.T) {
	srv, _, st, _ := setupAppPlane(t)
	h := newSchedulerTestHost(t, srv)

	shanghai, _ := time.LoadLocation("Asia/Shanghai")
	now := time.Now()
	planned := now.Add(-30 * time.Second) // 准点内（<90s）
	if _, err := st.UpsertScheduledJob(store.ScheduledJobRow{
		TenantID: 1, InstanceID: "box-1", ScheduleID: "tick",
		Cron: "* * * * *", Timezone: "Asia/Shanghai", MissedPolicy: "skip",
		NextRunAt: planned.Unix(),
	}); err != nil {
		t.Fatal(err)
	}

	h.dispatchScheduledJob(st, mustDue(t, st, now.Unix())[0], now)

	jobs, _ := st.ListScheduledJobs(1, "box-1")
	if len(jobs) != 1 {
		t.Fatalf("jobs = %d", len(jobs))
	}
	j := jobs[0]
	if j.LastDispatch != "sj|box-1|tick|"+strconv.FormatInt(planned.Unix(), 10) {
		t.Fatalf("ontime should dispatch, got key %q", j.LastDispatch)
	}
	if j.LastRunAt != planned.Unix() {
		t.Fatalf("last_run_at = %d, want planned %d", j.LastRunAt, planned.Unix())
	}
	// 节奏保持：next = 计划锚定的下一分钟边界，且绝不排进过去（跨过 now）
	want := planned.Truncate(time.Minute).Add(time.Minute).In(shanghai)
	for !want.After(now) {
		want = want.Add(time.Minute)
	}
	if j.NextRunAt != want.Unix() {
		t.Fatalf("next_run_at = %d, want %d (cadence from plan, not now)", j.NextRunAt, want.Unix())
	}
}

func TestSchedulerMissedSkipPolicy(t *testing.T) {
	srv, _, st, _ := setupAppPlane(t)
	h := newSchedulerTestHost(t, srv)

	now := time.Now()
	planned := now.Add(-20 * time.Minute) // 停机 20 分钟
	if _, err := st.UpsertScheduledJob(store.ScheduledJobRow{
		TenantID: 1, InstanceID: "box-1", ScheduleID: "tick",
		Cron: "* * * * *", Timezone: "Asia/Shanghai", MissedPolicy: "skip",
		NextRunAt: planned.Unix(),
	}); err != nil {
		t.Fatal(err)
	}

	h.dispatchScheduledJob(st, mustDue(t, st, now.Unix())[0], now)

	jobs, _ := st.ListScheduledJobs(1, "box-1")
	j := jobs[0]
	if j.LastDispatch != "" {
		t.Fatalf("skip policy must not dispatch, got %q", j.LastDispatch)
	}
	if j.NextRunAt <= now.Unix() {
		t.Fatalf("next_run_at = %d must be in future (> %d)", j.NextRunAt, now.Unix())
	}
}

func TestSchedulerMissedRunOncePolicy(t *testing.T) {
	srv, _, st, _ := setupAppPlane(t)
	h := newSchedulerTestHost(t, srv)

	now := time.Now()
	planned := now.Add(-20 * time.Minute)
	if _, err := st.UpsertScheduledJob(store.ScheduledJobRow{
		TenantID: 1, InstanceID: "box-1", ScheduleID: "tick",
		Cron: "* * * * *", Timezone: "Asia/Shanghai", MissedPolicy: "run_once",
		NextRunAt: planned.Unix(),
	}); err != nil {
		t.Fatal(err)
	}

	h.dispatchScheduledJob(st, mustDue(t, st, now.Unix())[0], now)

	jobs, _ := st.ListScheduledJobs(1, "box-1")
	j := jobs[0]
	// 补派发恰好一次：键绑定最早错过的计划时刻（不管错过了几个周期）
	if j.LastDispatch != "sj|box-1|tick|"+strconv.FormatInt(planned.Unix(), 10) {
		t.Fatalf("run_once should dispatch once for oldest missed, got %q", j.LastDispatch)
	}
	if j.LastRunAt != planned.Unix() || j.NextRunAt <= now.Unix() {
		t.Fatalf("run_once result = %+v", j)
	}
}

func TestSchedulerBadCronCancelled(t *testing.T) {
	srv, _, st, _ := setupAppPlane(t)
	h := newSchedulerTestHost(t, srv)

	now := time.Now()
	if _, err := st.UpsertScheduledJob(store.ScheduledJobRow{
		TenantID: 1, InstanceID: "box-1", ScheduleID: "broken",
		Cron: "not a cron", Timezone: "UTC", MissedPolicy: "skip",
		NextRunAt: now.Add(-time.Minute).Unix(),
	}); err != nil {
		t.Fatal(err)
	}

	h.dispatchScheduledJob(st, mustDue(t, st, now.Unix())[0], now)

	jobs, _ := st.ListScheduledJobs(1, "box-1")
	if len(jobs) != 1 || jobs[0].State != "cancelled" {
		t.Fatalf("bad cron should be cancelled: %+v", jobs)
	}
}

func mustDue(t *testing.T, st *store.Store, now int64) []store.ScheduledJobRow {
	t.Helper()
	due, err := st.ListScheduledJobsDue(now)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) == 0 {
		t.Fatal("no due job")
	}
	return due
}
