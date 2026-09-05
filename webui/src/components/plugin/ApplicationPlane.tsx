import { useId, useState } from 'react'
import type { ReactNode } from 'react'
import { Link } from 'react-router'
import { Badge, ErrorState, Panel } from '@/components/ui'
import { RowSkeleton } from '@/components/Skeleton'
import { APP_RECORD_PAGE_SIZE, useApplicationPlane } from '@/hooks/useApplicationPlane'
import { ApiError } from '@/lib/api'
import { appTime, bindingLabels, recordFieldLabel, scheduleSummary, scheduleZone } from '@/lib/application-plane'
import type { AppDomainRecordView, AppScheduledJobView } from '@/lib/types'
import { useAuth } from '@/store/auth'

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
        ? '请检查分类标识，或清除筛选后重试。' : '暂时无法取得最新数据，请重试。'}
      onRetry={() => { void query.refetch() }} retrying={query.isFetching} />
  }
  if (empty) return <p className="py-3 text-sm text-ink-3">{emptyText ?? '暂无' + title}</p>
  return children
}

function TechnicalDetails({ children }: { children: ReactNode }) {
  const [expanded, setExpanded] = useState(false)
  const id = useId()
  return <div className="mt-2 min-w-0">
    <button type="button" className="btn btn-ghost" aria-expanded={expanded} aria-controls={id}
      onClick={() => setExpanded(!expanded)}>{expanded ? '收起技术详情' : '查看技术详情'}</button>
    {expanded && <div id={id} className="mt-2 min-w-0 space-y-2 break-all text-xs text-ink-2">{children}</div>}
  </div>
}

/** 数据只按结构展示；未知字段给序号，未知业务枚举保留应用原值，不在平台维护词典。 */
function StructuredValue({ value, depth = 0 }: { value: unknown; depth?: number }) {
  if (value === null) return <span className="text-ink-3">未填写</span>
  if (typeof value !== 'object') return <span className="whitespace-pre-wrap break-all">{
    typeof value === 'boolean' ? value ? '是' : '否' : value === '' ? '空文本' : String(value)
  }</span>
  const entries = Object.entries(value as object)
  if (entries.length === 0) return <span className="text-ink-3">暂无内容</span>
  if (depth >= 2) return <span className="text-ink-3">{entries.length} 项嵌套内容，详见技术详情</span>
  return <dl className="min-w-0 space-y-2">
    {entries.slice(0, 6).map(([key, item], index) => <div key={key} className="min-w-0">
      <dt className="break-all text-xs text-ink-3">{Array.isArray(value) ? '第 ' + (index + 1) + ' 项' : recordFieldLabel(key, index)}</dt>
      <dd className="mt-0.5 min-w-0 break-all"><StructuredValue value={item} depth={depth + 1} /></dd>
    </div>)}
    {entries.length > 6 && <div className="text-xs text-ink-3"><dt className="sr-only">其余内容</dt>
      <dd>另有 {entries.length - 6} 项，详见技术详情</dd></div>}
  </dl>
}

function RecordRow({ record, number }: { record: AppDomainRecordView; number: number }) {
  let content: unknown
  let readable = true
  try { content = JSON.parse(record.data_json) } catch { readable = false }
  return <article className="min-w-0 border-t border-hairline py-4 first:border-0 first:pt-0">
    <div className="mb-3 flex flex-wrap items-baseline justify-between gap-2">
      <h3 className="text-sm font-medium">记录 {number}</h3>
      <p className="text-xs text-ink-3">更新于 <time>{appTime(record.updated_at)}</time></p>
    </div>
    <div className="min-w-0 text-sm">{readable ? <StructuredValue value={content} />
      : <p className="text-warn">记录内容无法读取，原文可在技术详情中核对。</p>}</div>
    <TechnicalDetails>
      <dl className="space-y-1">
        <div><dt className="inline">记录标识：</dt><dd className="inline">{record.record_id}</dd></div>
        <div><dt className="inline">分类标识：</dt><dd className="inline">{record.record_type}</dd></div>
        <div><dt className="inline">版本：</dt><dd className="inline">{record.version || '未提供'}</dd></div>
      </dl>
      <pre tabIndex={0} role="group" aria-label="记录原文"
        className="num max-h-64 overflow-auto whitespace-pre-wrap break-all rounded-lg bg-surface-2 p-3 font-mono text-xs">{record.data_json}</pre>
    </TechnicalDetails>
  </article>
}

function ScheduledRow({ job, number }: { job: AppScheduledJobView; number: number }) {
  const state = ({ active: '已启用', cancelled: '已取消', paused: '已暂停' } as Record<string, string>)[job.state] ?? '状态待确认'
  return <article className="min-w-0 border-t border-hairline py-3 first:border-0">
    <div className="flex flex-wrap items-center gap-2">
      <h4 className="text-sm font-medium">计划 {number}</h4>
      <Badge tone={job.state === 'active' ? 'accent' : 'idle'}>{state}</Badge>
    </div>
    <p className="mt-2 break-words text-sm">{scheduleSummary(job.cron)}<span className="ml-2 text-xs text-ink-3">{scheduleZone(job.timezone)}</span></p>
    <dl className="mt-2 space-y-1 text-xs text-ink-2">
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

interface Props { instanceID: string; lifecycleKey?: string }

/** 切实例或账号时重建局部筛选/分页；不能把上一实例的视图状态带过来。 */
export function ApplicationPlane(props: Props) {
  const identity = useAuth((s) => [s.user?.tenant_id, s.user?.id].join(':'))
  return <ApplicationPlaneContent key={identity + ':' + props.instanceID} {...props} />
}

function ApplicationPlaneContent({ instanceID, lifecycleKey }: Props) {
  const [offset, setOffset] = useState(0)
  const [filter, setFilter] = useState('')
  const [draft, setDraft] = useState('')
  const { records, bindings, jobs, presentation, status, running, canRead } = useApplicationPlane(instanceID, offset, filter, lifecycleKey)
  const rows = records.data?.records ?? []
  const refreshing = records.isFetching || bindings.isFetching || jobs.isFetching
  if (!canRead) return <Panel title="应用数据" className="mb-5">
    <p className="text-sm text-ink-2">登录后可查看当前租户的应用记录、设备绑定和定时任务。</p>
    <Link to="/login" className="btn btn-ghost mt-3">前往登录</Link>
  </Panel>
  return <section className="mb-5 min-w-0 space-y-5" aria-label="应用数据">
    <div className="flex flex-wrap items-center gap-2">
      <h2 className="text-[15px] font-semibold">应用数据</h2>
      {running !== undefined && <Badge tone={running ? 'ok' : 'idle'}>{running ? '应用运行中' : '应用未运行'}</Badge>}
      <p role="status" className="text-xs text-ink-3">{status === 'open' ? '实时更新已连接'
        : status === 'connecting' ? '正在连接实时更新，暂以定时同步为准' : '实时更新已断开，暂以定时同步为准'}</p>
      {refreshing && <span className="text-xs text-ink-3">正在同步…</span>}
    </div>
    {running === false && <p className="text-sm text-ink-2">应用当前未运行。设备绑定和运行期任务会暂时清空，已保存的记录和计划仍可查看。</p>}
    <Panel title="应用记录">
      <details className="mb-4 min-w-0">
        <summary className="cursor-pointer text-xs text-ink-2">筛选记录{filter ? '（已筛选）' : ''}</summary>
        <form className="mt-3 flex flex-wrap items-end gap-2" onSubmit={(event) => {
          event.preventDefault(); setOffset(0); setFilter(draft.trim())
        }}>
          <label className="min-w-0 text-xs text-ink-2">分类标识
            <input className="input mt-1 block w-full" value={draft} placeholder="全部分类"
              onChange={(event) => setDraft(event.target.value)} />
          </label>
          <button type="submit" className="btn btn-ghost">筛选</button>
          {(filter || draft) && <button type="button" className="btn btn-ghost" onClick={() => {
            setFilter(''); setDraft(''); setOffset(0)
          }}>清除筛选</button>}
        </form>
        <p className="mt-2 text-xs text-ink-3">分类标识由应用提供，可在记录的技术详情中查看。</p>
      </details>
      <ReadContent title="应用记录" query={records} empty={!rows.length} emptyText={filter ? '此分类暂无记录' : undefined}>
        <p className="mb-4 text-xs text-ink-3">内容按应用原值展示。未提供中文名称的字段以数据项编号区分，原始标识见技术详情。</p>
        {rows.map((row, index) => <RecordRow key={JSON.stringify([row.record_type, row.record_id])} record={row} number={offset + index + 1} />)}
      </ReadContent>
      <nav aria-label="应用记录分页" className="mt-3 flex flex-wrap items-center gap-2 text-xs text-ink-3">
        <button type="button" className="btn btn-ghost" disabled={offset === 0 || records.isFetching}
          onClick={() => setOffset(Math.max(0, offset - APP_RECORD_PAGE_SIZE))}>上一页</button>
        <span>第 {offset / APP_RECORD_PAGE_SIZE + 1} 页</span>
        <button type="button" className="btn btn-ghost" disabled={rows.length < APP_RECORD_PAGE_SIZE || records.isFetching || records.isError}
          onClick={() => setOffset(offset + APP_RECORD_PAGE_SIZE)}>下一页</button>
      </nav>
    </Panel>
    <Panel title="设备绑定">
      <ReadContent title="设备绑定" query={bindings} empty={!bindings.data?.bindings.length}>
        <p className="mb-3 text-xs text-ink-3">以下是应用当前使用的设备能力；绑定会随应用停止而清空。</p>
        <ul className="divide-y divide-hairline">{bindings.data?.bindings.map((binding, index) => {
          const labels = bindingLabels(binding, presentation)
          return <li key={JSON.stringify([binding.requirement_id, binding.entity_id])} className="min-w-0 py-3 first:pt-0">
            <p className="break-all text-sm font-medium">{labels.entity || '设备绑定 ' + (index + 1)}</p>
            <p className="mt-1 break-words text-xs text-ink-2">{labels.capability}</p>
            <TechnicalDetails><p>实体标识：{binding.entity_id}</p><p>能力标识：{binding.capability}</p><p>需求标识：{binding.requirement_id}</p></TechnicalDetails>
          </li>
        })}</ul>
      </ReadContent>
    </Panel>
    <Panel title="定时任务">
      <ReadContent title="定时任务" query={jobs} empty={!jobs.data?.jobs.length && !jobs.data?.scheduled.length}>
        <p className="mb-4 text-xs text-ink-3">运行期任务随应用启停，保存的计划会保留。调度时间不代表执行成功。</p>
        <h3 className="text-sm font-medium">运行期任务</h3>
        {jobs.data?.jobs.length ? <ul className="mb-4 divide-y divide-hairline">{jobs.data.jobs.map((job, index) => <li key={job} className="py-3">
          <p className="text-sm">任务 {index + 1}</p>
          <TechnicalDetails><p>任务标识：{job}</p></TechnicalDetails>
        </li>)}</ul> : <p className="mb-4 mt-2 text-xs text-ink-3">暂无运行期任务</p>}
        <h3 className="text-sm font-medium">已保存的计划</h3>
        {jobs.data?.scheduled.length ? jobs.data.scheduled.map((job, index) => <ScheduledRow key={job.schedule_id} job={job} number={index + 1} />)
          : <p className="mt-2 text-xs text-ink-3">暂无已保存的计划</p>}
      </ReadContent>
    </Panel>
  </section>
}
