package store

import "database/sql"

// ScheduledJobRow 是 scheduled_jobs 一行（Durable Scheduler 的持久态）。
type ScheduledJobRow struct {
	TenantID     int64
	InstanceID   string
	ScheduleID   string
	Cron         string
	Timezone     string
	PayloadJSON  string
	MissedPolicy string // skip | run_once
	NextRunAt    int64  // 计划触发时刻（unix 秒）
	LastRunAt    int64
	LastDispatch string
	State        string // active | cancelled
	Revision     int64
	CreatedAt    int64
	UpdatedAt    int64
}

// 租户不能直接写 missed_policy 之外的 state（cancelled 只经 CancelScheduledJob）。
const scheduledJobStateActive = "active"

// UpsertScheduledJob 写入/更新一条声明式定时任务（schedule_job 效果落点）。
// 同 (tenant, instance, schedule_id) 重声明 = 更新（cron/payload/策略/next_run_at
// 重算）；state 一律回到 active（cancel 后重声明 = 重新生效，与
// UpsertDomainRecord 的「最终态收敛」同一哲学）。revision 递增。
func (s *Store) UpsertScheduledJob(row ScheduledJobRow) (ScheduledJobRow, error) {
	tid, err := s.normalizeTenantID(row.TenantID)
	if err != nil {
		return ScheduledJobRow{}, err
	}
	ts := row.UpdatedAt
	if ts <= 0 {
		ts = now()
	}
	_, err = s.exec(`
		INSERT INTO scheduled_jobs(tenant_id, instance_id, schedule_id, cron, timezone,
			payload_json, missed_policy, next_run_at, last_run_at, last_dispatch,
			state, revision, created_at, updated_at)
		VALUES(?,?,?,?,?,?,?,?,0,'',?,1,?,?)
		ON CONFLICT(tenant_id, instance_id, schedule_id) DO UPDATE SET
			cron=excluded.cron, timezone=excluded.timezone,
			payload_json=excluded.payload_json, missed_policy=excluded.missed_policy,
			next_run_at=excluded.next_run_at, state='active',
			revision=scheduled_jobs.revision+1, updated_at=excluded.updated_at`,
		tid, row.InstanceID, row.ScheduleID, row.Cron, row.Timezone,
		row.PayloadJSON, row.MissedPolicy, row.NextRunAt, scheduledJobStateActive, ts, ts)
	if err != nil {
		return ScheduledJobRow{}, err
	}
	out := row
	out.TenantID = tid
	out.State = scheduledJobStateActive
	out.Revision = 1
	out.UpdatedAt = ts
	return out, nil
}

// CancelScheduledJob 撤销一条定时任务（cancel_job 效果落点）。幂等：不存在或
// 已取消均不报错（删除语义的收敛写）。
func (s *Store) CancelScheduledJob(tenantID int64, instanceID, scheduleID string) error {
	tid, err := s.normalizeTenantID(tenantID)
	if err != nil {
		return err
	}
	_, err = s.exec(`
		UPDATE scheduled_jobs SET state='cancelled', updated_at=?
		WHERE tenant_id=? AND instance_id=? AND schedule_id=?`,
		now(), tid, instanceID, scheduleID)
	return err
}

// ListScheduledJobsDue 列出到期任务（state=active AND next_run_at<=now），
// 按计划时刻升序（错过最久的先被处理）。
func (s *Store) ListScheduledJobsDue(now int64) ([]ScheduledJobRow, error) {
	rows, err := s.db.Query(`
		SELECT tenant_id, instance_id, schedule_id, cron, timezone, payload_json,
			missed_policy, next_run_at, last_run_at, last_dispatch, state, revision,
			created_at, updated_at
		FROM scheduled_jobs WHERE state='active' AND next_run_at<=?
		ORDER BY next_run_at, tenant_id, instance_id, schedule_id LIMIT 500`, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanScheduledJobs(rows)
}

// ListScheduledJobs 按实例列定时任务（D1 jobs 读面；含 cancelled，state 自述）。
func (s *Store) ListScheduledJobs(tenantID int64, instanceID string) ([]ScheduledJobRow, error) {
	tid, err := s.normalizeTenantID(tenantID)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`
		SELECT tenant_id, instance_id, schedule_id, cron, timezone, payload_json,
			missed_policy, next_run_at, last_run_at, last_dispatch, state, revision,
			created_at, updated_at
		FROM scheduled_jobs WHERE tenant_id=? AND instance_id=?
		ORDER BY schedule_id`, tid, instanceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanScheduledJobs(rows)
}

// ClaimScheduledJobRun 领取一次运行：先持久化推进 next_run_at / last_run_at /
// last_dispatch，再由调用方派发（claim-then-dispatch）。崩溃发生在 claim 之后、
// 派发之前 → 该次运行静默跳过（at-most-once，符合平台「不回放过期副作用」
// 哲学）；崩溃不可能造成重复派发（next_run_at 已推进，同 run 不会被再次选中）。
// dispatchKey 为空表示本次不派发（skip 策略只推进）。
func (s *Store) ClaimScheduledJobRun(tenantID int64, instanceID, scheduleID string, nextRunAt, lastRunAt int64, dispatchKey string) error {
	tid, err := s.normalizeTenantID(tenantID)
	if err != nil {
		return err
	}
	_, err = s.exec(`
		UPDATE scheduled_jobs
		SET next_run_at=?, last_run_at=?, last_dispatch=?, updated_at=?
		WHERE tenant_id=? AND instance_id=? AND schedule_id=?`,
		nextRunAt, lastRunAt, dispatchKey, now(),
		tid, instanceID, scheduleID)
	return err
}

func scanScheduledJobs(rows *sql.Rows) ([]ScheduledJobRow, error) {
	var out []ScheduledJobRow
	for rows.Next() {
		var r ScheduledJobRow
		if err := rows.Scan(&r.TenantID, &r.InstanceID, &r.ScheduleID, &r.Cron, &r.Timezone,
			&r.PayloadJSON, &r.MissedPolicy, &r.NextRunAt, &r.LastRunAt, &r.LastDispatch,
			&r.State, &r.Revision, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
