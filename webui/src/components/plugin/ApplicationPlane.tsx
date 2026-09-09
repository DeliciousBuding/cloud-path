import { useId, useState } from 'react'
import type { ReactNode } from 'react'
import { Link } from 'react-router'
import { Badge, ErrorState, Panel } from '@/components/ui'
import { StructuredValue } from '@/components/StructuredValue'
import { ApplicationActions } from './ApplicationActions'
import { RowSkeleton } from '@/components/Skeleton'
import { APP_RECORD_PAGE_SIZE, useApplicationPlane } from '@/hooks/useApplicationPlane'
import { ApiError } from '@/lib/api'
import { appTime, applicationRunningState, bindingLabels, recordHeadline, scheduleSummary, scheduleZone } from '@/lib/application-plane'
import type { AppDomainRecordView, AppJobsView, AppScheduledJobView } from '@/lib/types'
import { authIdentity, useAuth } from '@/store/auth'

interface ReadQuery {
  isPending: boolean
  isError: boolean
  error: unknown
  isFetching: boolean
  refetch: () => unknown
}

function ReadContent({ title, query, empty, emptyText, children }: {
  title: string; query: ReadQuery; empty: boolean; emptyText?: string; children: ReactNode
}) {
  if (query.isPending) return <div role="status" aria-label={title + '加载中'}><RowSkeleton rows={3} /></div>
  if (query.isError) {
    const status = query.error instanceof ApiError ? query.error.status : undefined
    const denied = status === 403
    return <ErrorState compact title={denied ? '没有查看权限' : status === 400 ? '筛选条件无效' : title + '加载失败'}
      hint={denied ? '请联系管理员核对当前账号的访问权限。' : status === 400
        ? '请检查分类，或清除筛选后重试。' : '暂时无法取得最新数据，请重试。'}
      onRetry={() => { void query.refetch() }} retrying={query.isFetching} />
  }
  if (empty) return <p className="py-3 text-body text-ink-3">{emptyText ?? '暂无' + title}</p>
  return children
}

function TechnicalDetails({ children }: { children: ReactNode }) {
  const [expanded, setExpanded] = useState(false)
  const id = useId()
  return <div className="mt-2 min-w-0">
    <button type="button" className="btn btn-ghost min-h-touch w-full sm:w-auto" aria-expanded={expanded} aria-controls={id}
      onClick={() => setExpanded(!expanded)}>{expanded ? '收起技术详情' : '查看技术详情'}</button>
    {expanded && <div id={id} className="mt-2 min-w-0 space-y-2 break-words text-meta text-ink-2 [overflow-wrap:anywhere]">{children}</div>}
  </div>
}

function RecordRow({ record, number }: { record: AppDomainRecordView; number: number }) {
  let content: unknown
  let readable = true
  try { content = JSON.parse(record.data_json) } catch { readable = false }
  const headline = readable
    ? recordHeadline(content, `记录 ${number}`)
    : { title: `记录 ${number}`, usedKeys: [] }
  return <article className="min-w-0 border-t border-hairline py-5 first:border-0 first:pt-0">
    <div className="mb-3 flex flex-wrap items-baseline justify-between gap-2">
      <h3 className="min-w-0 break-words text-body font-medium [overflow-wrap:anywhere]"
        title={headline.title}>{headline.title}</h3>
      <p className="shrink-0 text-meta text-ink-3">更新于 <time>{appTime(record.updated_at)}</time></p>
    </div>
    <div className="min-w-0 text-body">{readable ? <StructuredValue value={content} omitKeys={headline.usedKeys} />
      : <p className="text-warn">记录内容无法读取，原文可在技术详情中核对。</p>}</div>
    <TechnicalDetails>
      <dl className="space-y-1">
        <div><dt className="inline">记录标识：</dt><dd className="inline">{record.record_id}</dd></div>
        <div><dt className="inline">分类代码：</dt><dd className="inline">{record.record_type}</dd></div>
        <div><dt className="inline">版本：</dt><dd className="inline">{record.version || '未提供'}</dd></div>
      </dl>
      <pre tabIndex={0} role="group" aria-label="记录原文"
        className="num max-h-64 overflow-auto whitespace-pre-wrap break-all rounded-tile bg-surface-2 p-3 font-mono text-meta">{record.data_json}</pre>
    </TechnicalDetails>
  </article>
}

function ScheduledRow({ job, number }: { job: AppScheduledJobView; number: number }) {
  const state = ({ active: '已启用', cancelled: '已取消', paused: '已暂停' } as Record<string, string>)[job.state] ?? '状态待确认'
  return <article className="min-w-0 border-t border-hairline py-3 first:border-0">
    <div className="flex flex-wrap items-center gap-2">
      <h4 className="text-body font-medium">计划 {number}</h4>
      <Badge tone={job.state === 'active' ? 'accent' : 'idle'}>{state}</Badge>
    </div>
    <p className="mt-2 break-words text-body">{scheduleSummary(job.cron)}<span className="ml-2 text-meta text-ink-3">{scheduleZone(job.timezone)}</span></p>
    <dl className="mt-2 space-y-1 text-meta text-ink-2">
      <div><dt className="inline">下次计划：</dt><dd className="inline">{job.state === 'active'
        ? job.next_run_at ? appTime(job.next_run_at, job.timezone) : '尚未安排' : '未安排'}</dd></div>
      <div><dt className="inline">最近调度：</dt><dd className="inline">{job.last_run_at ? appTime(job.last_run_at, job.timezone) : '尚未调度'}</dd></div>
      <div><dt className="inline">错过计划时：</dt><dd className="inline">{job.missed_policy === 'skip' ? '跳过'
        : job.missed_policy === 'run_once' ? '补执行一次' : '策略未提供说明'}</dd></div>
    </dl>
    <TechnicalDetails>
      <p>计划标识：{job.schedule_id}</p><p>时间表达式：{job.cron}</p>
      <p>时区标识：{job.timezone || '未提供'}</p><p>状态原值：{job.state}</p>
      <p>修订版：{job.revision}</p><p>错过策略：{job.missed_policy}</p>
    </TechnicalDetails>
  </article>
}

/** 临时任务是应用自动运行的后台项；手动操作只在“应用操作”出现，避免同一入口重复。 */
function backgroundJobIDs(jobs: AppJobsView | undefined): string[] {
  const descriptors = new Map((jobs?.job_descriptors ?? []).map((job) => [job.id, job]))
  return (jobs?.jobs ?? []).filter((id) => descriptors.get(id)?.manual_only !== true)
}

interface Props { instanceID: string; lifecycleKey?: string; runtimeState?: string; desiredEnabled?: boolean }

/** 切实例或账号时重建局部筛选/分页；不能把上一实例的视图状态带过来。 */
export function ApplicationPlane(props: Props) {
  const identity = useAuth(authIdentity)
  return <ApplicationPlaneContent key={identity + ':' + props.instanceID} {...props} />
}

function ApplicationPlaneContent({ instanceID, lifecycleKey, runtimeState, desiredEnabled = true }: Props) {
  const [offset, setOffset] = useState(0)
  const [filter, setFilter] = useState('')
  const [draft, setDraft] = useState('')
  const { records, bindings, jobs, presentation, status, running, canRead } = useApplicationPlane(instanceID, offset, filter, lifecycleKey)
  const runningState = applicationRunningState(running, runtimeState)
  const actionRunning = runningState === 'running' ? desiredEnabled
    : runningState === 'unknown' ? undefined : false
  const runningConflict = runningState === 'conflict'
  const rows = records.data?.records ?? []
  const temporaryJobs = backgroundJobIDs(jobs.data)
  const recordTypes = [...new Set(rows.map((row) => row.record_type).filter(Boolean))].sort()
  const refreshing = records.isFetching || bindings.isFetching || jobs.isFetching
  if (!canRead) return <Panel title="应用数据" className="mb-5">
    <p className="text-body text-ink-2">登录后可查看当前组织的应用记录、设备绑定和定时任务。</p>
    <Link to="/login" className="btn btn-ghost mt-3">前往登录</Link>
  </Panel>
  return <section className="mb-5 min-w-0 space-y-5" aria-label="应用数据">
    <div className="flex flex-wrap items-center gap-2">
      <h2 className="text-lead font-semibold">应用数据</h2>
      {runningState !== 'unknown' && <Badge tone={runningState === 'running' ? 'ok' : runningState === 'conflict' ? 'warn' : 'idle'}>{runningState === 'running' ? '应用运行中' : runningState === 'conflict' ? '状态冲突' : desiredEnabled ? '应用未运行' : '设置已停用'}</Badge>}
      <p role="status" className="text-meta text-ink-3">{status === 'open' ? '实时更新已连接'
        : status === 'connecting' ? '正在连接实时更新，暂以定时同步为准' : '实时更新已断开，暂以定时同步为准'}</p>
      {refreshing && <span className="text-meta text-ink-3">正在同步…</span>}
    </div>
    {runningConflict && <p className="text-body text-ink-2">运行状态来源不一致，暂时无法确认应用是否正在运行。</p>}
    {runningState === 'stopped' && <p className="text-body text-ink-2">{desiredEnabled ? '应用当前未运行。设备连接和临时任务会暂时清空，已保存的记录和计划仍可查看。' : '设置已停用，应用不会运行；已保存的记录和计划仍可查看。'}</p>}
    <Panel title="应用操作">
      {(jobs.isPending || jobs.isError) && <ReadContent title="应用操作" query={jobs} empty={false}>{null}</ReadContent>}
      <ApplicationActions instanceID={instanceID} jobs={jobs.data} running={actionRunning} conflict={runningConflict} desiredEnabled={desiredEnabled}
        lifecycleKey={JSON.stringify([lifecycleKey, runtimeState, desiredEnabled])} />
    </Panel>
    <Panel title="应用记录">
      <details className="mb-4 min-w-0">
        <summary className="flex min-h-touch cursor-pointer items-center text-meta text-ink-2">筛选记录{filter ? '（已筛选）' : ''}</summary>
        <form className="mt-3 flex flex-wrap items-end gap-2" onSubmit={(event) => {
          event.preventDefault(); setOffset(0); setFilter(draft.trim())
        }}>
          <label className="min-w-0 text-meta text-ink-2">分类
            <select className="input mt-1 block min-h-touch w-full" value={draft} onChange={(event) => setDraft(event.target.value)}>
              <option value="">全部分类</option>
              {recordTypes.map((type) => <option key={type} value={type}>{type}</option>)}
              {draft && !recordTypes.includes(draft) && <option value={draft}>{draft}</option>}
            </select>
          </label>
          <button type="submit" className="btn btn-ghost">筛选</button>
          {(filter || draft) && <button type="button" className="btn btn-ghost" onClick={() => {
            setFilter(''); setDraft(''); setOffset(0)
          }}>清除筛选</button>}
        </form>
        <p className="mt-2 text-meta text-ink-3">分类来自当前记录；选择后只显示这一类记录。</p>
      </details>
      <ReadContent title="应用记录" query={records} empty={!rows.length} emptyText={filter ? '此分类暂无记录' : undefined}>
        <p className="mb-4 text-meta text-ink-3">名称和状态优先显示；未定义的内容保留原始值，时间按当前时区显示。</p>
        {rows.map((row, index) => <RecordRow key={JSON.stringify([row.record_type, row.record_id])} record={row} number={offset + index + 1} />)}
      </ReadContent>
      <nav aria-label="应用记录分页" className="mt-3 flex flex-wrap items-center gap-2 text-meta text-ink-3">
        <button type="button" className="btn btn-ghost" disabled={offset === 0 || records.isFetching}
          onClick={() => setOffset(Math.max(0, offset - APP_RECORD_PAGE_SIZE))}>上一页</button>
        <span>第 {offset / APP_RECORD_PAGE_SIZE + 1} 页</span>
        <button type="button" className="btn btn-ghost" disabled={rows.length < APP_RECORD_PAGE_SIZE || records.isFetching || records.isError}
          onClick={() => setOffset(offset + APP_RECORD_PAGE_SIZE)}>下一页</button>
      </nav>
    </Panel>
    <div className="grid min-w-0 items-start gap-5 lg:grid-cols-2">
    <Panel title="设备绑定">
      <ReadContent title="设备绑定" query={bindings} empty={!bindings.data?.bindings.length}>
        <p className="mb-3 text-meta text-ink-3">以下是应用当前使用的设备功能；绑定会随应用停止而清空。</p>
        <ul className="divide-y divide-hairline">{bindings.data?.bindings.map((binding, index) => {
          const labels = bindingLabels(binding, presentation)
          return <li key={JSON.stringify([binding.requirement_id, binding.entity_id])} className="min-w-0 py-3 first:pt-0">
            <p className="break-words text-body font-medium [overflow-wrap:anywhere]">{labels.entity || '设备绑定 ' + (index + 1)}</p>
            {labels.capability !== labels.entity && <p className="mt-1 break-words text-meta text-ink-2">{labels.capability}</p>}
            <TechnicalDetails><p>实体标识：{binding.entity_id}</p><p>功能标识：{binding.capability}</p><p>需求标识：{binding.requirement_id}</p></TechnicalDetails>
          </li>
        })}</ul>
      </ReadContent>
    </Panel>
    <Panel title="定时任务">
      <ReadContent title="定时任务" query={jobs} empty={!temporaryJobs.length && !jobs.data?.scheduled.length}>
        <p className="mb-4 text-meta text-ink-3">临时任务随应用启停，保存的计划会保留。排定时间不代表已经执行成功。</p>
        <h3 className="text-body font-medium">临时任务</h3>
        {temporaryJobs.length ? <ul className="mb-4 divide-y divide-hairline">{temporaryJobs.map((job) => {
          const descriptor = jobs.data?.job_descriptors?.find((item) => item.id === job)
          const title = descriptor?.title?.trim() || '后台任务'
          return <li key={job} className="py-3">
            <p className="break-words text-body font-medium [overflow-wrap:anywhere]">{title}</p>
            <TechnicalDetails><p>任务标识：{job}</p></TechnicalDetails>
          </li>
        })}</ul> : <p className="mb-4 mt-2 text-meta text-ink-3">暂无临时任务</p>}
        <h3 className="text-body font-medium">已保存的计划</h3>
        {jobs.data?.scheduled.length ? jobs.data.scheduled.map((job, index) => <ScheduledRow key={job.schedule_id} job={job} number={index + 1} />)
          : <p className="mt-2 text-meta text-ink-3">暂无已保存的计划</p>}
      </ReadContent>
    </Panel>
    </div>
  </section>
}
