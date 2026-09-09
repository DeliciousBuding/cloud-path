import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import type { ReactNode } from 'react'
import { Badge, Button, ButtonLink, ErrorState, Panel, Select } from '@/components/ui'
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
  const { t } = useTranslation('plugin')
  if (query.isPending) return <div role="status" aria-label={t('plane.loadingAria', { title })}><RowSkeleton rows={3} /></div>
  if (query.isError) {
    const status = query.error instanceof ApiError ? query.error.status : undefined
    const denied = status === 403
    return <ErrorState compact title={denied ? t('plane.permissionDenied') : status === 400 ? t('plane.invalidFilter') : t('plane.loadFailed', { title })}
      hint={denied ? t('plane.permissionHint') : status === 400
        ? t('plane.filterHint') : t('plane.loadHint')}
      onRetry={() => { void query.refetch() }} retrying={query.isFetching} />
  }
  if (empty) return <p className="py-3 text-body text-ink-3">{emptyText ?? t('plane.empty', { title })}</p>
  return children
}

function TechnicalDetails({ children }: { children: ReactNode }) {
  const { t } = useTranslation('plugin')
  const [expanded, setExpanded] = useState(false)
  const id = useId()
  return <div className="mt-2 min-w-0">
    <Button variant="ghost" className="min-h-touch w-full sm:w-auto" aria-expanded={expanded} aria-controls={id}
      onClick={() => setExpanded(!expanded)}>{expanded ? t('plane.collapseDetails') : t('plane.viewDetails')}</Button>
    {expanded && <div id={id} className="mt-2 min-w-0 space-y-2 break-words text-meta text-ink-2 [overflow-wrap:anywhere]">{children}</div>}
  </div>
}

function RecordRow({ record, number }: { record: AppDomainRecordView; number: number }) {
  const { t } = useTranslation('plugin')
  let content: unknown
  let readable = true
  try { content = JSON.parse(record.data_json) } catch { readable = false }
  const headline = readable
    ? recordHeadline(content, t('plane.record', { number }))
    : { title: t('plane.record', { number }), usedKeys: [] }
  return <article className="min-w-0 border-t border-hairline py-5 first:border-0 first:pt-0">
    <div className="mb-3 flex flex-wrap items-baseline justify-between gap-2">
      <h3 className="min-w-0 break-words text-body font-medium [overflow-wrap:anywhere]"
        title={headline.title}>{headline.title}</h3>
      <p className="shrink-0 text-meta text-ink-3">{t('plane.updatedAt')} <time>{appTime(record.updated_at)}</time></p>
    </div>
    <div className="min-w-0 text-body">{readable ? <StructuredValue value={content} omitKeys={headline.usedKeys} />
      : <p className="text-warn">{t('plane.unreadable')}</p>}</div>
    <TechnicalDetails>
      <dl className="space-y-1">
        <div><dt className="inline">{t('plane.recordId')}</dt><dd className="inline">{record.record_id}</dd></div>
        <div><dt className="inline">{t('plane.recordType')}</dt><dd className="inline">{record.record_type}</dd></div>
        <div><dt className="inline">{t('plane.version')}</dt><dd className="inline">{record.version || t('common.notProvided')}</dd></div>
      </dl>
      <pre tabIndex={0} role="group" aria-label={t('plane.rawAria')}
        className="num max-h-64 overflow-auto whitespace-pre-wrap break-all rounded-tile bg-surface-2 p-3 font-mono text-meta">{record.data_json}</pre>
    </TechnicalDetails>
  </article>
}

function ScheduledRow({ job, number }: { job: AppScheduledJobView; number: number }) {
  const { t } = useTranslation('plugin')
  const state = ({ active: t('plane.active'), cancelled: t('plane.cancelled'), paused: t('plane.paused') } as Record<string, string>)[job.state] ?? t('plane.statusUnknown')
  return <article className="min-w-0 border-t border-hairline py-3 first:border-0">
    <div className="flex flex-wrap items-center gap-2">
      <h4 className="text-body font-medium">{t('plane.schedule', { number })}</h4>
      <Badge tone={job.state === 'active' ? 'accent' : 'idle'}>{state}</Badge>
    </div>
    <p className="mt-2 break-words text-body">{scheduleSummary(job.cron)}<span className="ml-2 text-meta text-ink-3">{scheduleZone(job.timezone)}</span></p>
    <dl className="mt-2 space-y-1 text-meta text-ink-2">
      <div><dt className="inline">{t('plane.nextRun')}</dt><dd className="inline">{job.state === 'active'
        ? job.next_run_at ? appTime(job.next_run_at, job.timezone) : t('plane.notScheduled') : t('plane.notRun')}</dd></div>
      <div><dt className="inline">{t('plane.lastRun')}</dt><dd className="inline">{job.last_run_at ? appTime(job.last_run_at, job.timezone) : t('plane.neverRun')}</dd></div>
      <div><dt className="inline">{t('plane.missedPolicy')}</dt><dd className="inline">{job.missed_policy === 'skip' ? t('plane.skip')
        : job.missed_policy === 'run_once' ? t('plane.runOnce') : t('plane.policyUnknown')}</dd></div>
    </dl>
    <TechnicalDetails>
      <p>{t('plane.scheduleId')}{job.schedule_id}</p><p>{t('plane.cron')}{job.cron}</p>
      <p>{t('plane.timezone')}{job.timezone || t('common.notProvided')}</p><p>{t('plane.stateRaw')}{job.state}</p>
      <p>{t('plane.version')}{job.revision}</p><p>{t('plane.missedRaw')}{job.missed_policy}</p>
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
  const { t } = useTranslation('plugin')
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
  if (!canRead) return <Panel title={t('plane.appData')} className="mb-5">
    <p className="text-body text-ink-2">{t('plane.loginHint')}</p>
    <ButtonLink to="/login" variant="ghost" className="mt-3">{t('plane.login')}</ButtonLink>
  </Panel>
  return <section className="mb-5 min-w-0 space-y-5" aria-label={t('plane.appData')}>
    <div className="flex flex-wrap items-center gap-2">
      <h2 className="text-body font-semibold">{t('plane.appData')}</h2>
      {runningState !== 'unknown' && <Badge tone={runningState === 'running' ? 'ok' : runningState === 'conflict' ? 'warn' : 'idle'}>{runningState === 'running' ? t('plane.appRunning') : runningState === 'conflict' ? t('plane.stateConflict') : desiredEnabled ? t('plane.appStopped') : t('plane.disabled')}</Badge>}
      <p role="status" className="text-meta text-ink-3">{status === 'open' ? t('plane.realtimeOpen')
        : status === 'connecting' ? t('plane.realtimeConnecting') : t('plane.realtimeClosed')}</p>
      {refreshing && <span className="text-meta text-ink-3">{t('plane.syncing')}</span>}
    </div>
    {runningConflict && <p className="text-body text-ink-2">{t('plane.conflictHint')}</p>}
    {runningState === 'stopped' && <p className="text-body text-ink-2">{desiredEnabled ? t('plane.stoppedHint') : t('plane.disabledHint')}</p>}
    <Panel title={t('plane.appActions')}>
      {(jobs.isPending || jobs.isError) && <ReadContent title={t('plane.appActions')} query={jobs} empty={false}>{null}</ReadContent>}
      <ApplicationActions instanceID={instanceID} jobs={jobs.data} running={actionRunning} conflict={runningConflict} desiredEnabled={desiredEnabled}
        lifecycleKey={JSON.stringify([lifecycleKey, runtimeState, desiredEnabled])} />
    </Panel>
    <Panel title={t('plane.appRecords')}>
      <details className="mb-4 min-w-0">
        <summary className="flex min-h-touch cursor-pointer items-center text-meta text-ink-2">{t('plane.filter')}{filter ? t('plane.filtered') : ''}</summary>
        <form className="mt-3 flex flex-wrap items-end gap-2" onSubmit={(event) => {
          event.preventDefault(); setOffset(0); setFilter(draft.trim())
        }}>
          <label className="min-w-0 text-meta text-ink-2">{t('plane.category')}
            <Select className="mt-1 block w-full" value={draft} onChange={(event) => setDraft(event.target.value)}>
              <option value="">{t('plane.allCategories')}</option>
              {recordTypes.map((type) => <option key={type} value={type}>{type}</option>)}
              {draft && !recordTypes.includes(draft) && <option value={draft}>{draft}</option>}
            </Select>
          </label>
          <Button type="submit" variant="ghost">{t('plane.applyFilter')}</Button>
          {(filter || draft) && <Button variant="ghost" onClick={() => {
            setFilter(''); setDraft(''); setOffset(0)
          }}>{t('plane.clearFilter')}</Button>}
        </form>
        <p className="mt-2 text-meta text-ink-3">{t('plane.filterHelp')}</p>
      </details>
      <ReadContent title={t('plane.appRecords')} query={records} empty={!rows.length} emptyText={filter ? t('plane.noRecords') : undefined}>
        <p className="mb-4 text-meta text-ink-3">{t('plane.recordsHelp')}</p>
        {rows.map((row, index) => <RecordRow key={JSON.stringify([row.record_type, row.record_id])} record={row} number={offset + index + 1} />)}
      </ReadContent>
      <nav aria-label={t('plane.pagination')} className="mt-3 flex flex-wrap items-center gap-2 text-meta text-ink-3">
        <Button variant="ghost" disabled={offset === 0 || records.isFetching}
          onClick={() => setOffset(Math.max(0, offset - APP_RECORD_PAGE_SIZE))}>{t('plane.previous')}</Button>
        <span>{t('plane.page', { page: offset / APP_RECORD_PAGE_SIZE + 1 })}</span>
        <Button variant="ghost" disabled={rows.length < APP_RECORD_PAGE_SIZE || records.isFetching || records.isError}
          onClick={() => setOffset(offset + APP_RECORD_PAGE_SIZE)}>{t('plane.next')}</Button>
      </nav>
    </Panel>
    <div className="grid min-w-0 items-start gap-5 lg:grid-cols-2">
    <Panel title={t('plane.bindings')}>
      <ReadContent title={t('plane.bindings')} query={bindings} empty={!bindings.data?.bindings.length}>
        <p className="mb-3 text-meta text-ink-3">{t('plane.bindingsHelp')}</p>
        <ul className="divide-y divide-hairline">{bindings.data?.bindings.map((binding, index) => {
          const labels = bindingLabels(binding, presentation)
          return <li key={JSON.stringify([binding.requirement_id, binding.entity_id])} className="min-w-0 py-3 first:pt-0">
            <p className="break-words text-body font-medium [overflow-wrap:anywhere]">{labels.entity || t('plane.bindingFallback', { number: index + 1 })}</p>
            {labels.capability !== labels.entity && <p className="mt-1 break-words text-meta text-ink-2">{labels.capability}</p>}
            <TechnicalDetails><p>{t('plane.bindingFallback', { number: binding.entity_id })}</p><p>{t('plane.capabilityId')}{binding.capability}</p><p>{t('plane.requirementId')}{binding.requirement_id}</p></TechnicalDetails>
          </li>
        })}</ul>
      </ReadContent>
    </Panel>
    <Panel title={t('plane.scheduled')}>
      <ReadContent title={t('plane.scheduled')} query={jobs} empty={!temporaryJobs.length && !jobs.data?.scheduled.length}>
        <p className="mb-4 text-meta text-ink-3">{t('plane.jobsHelp')}</p>
        <h3 className="text-body font-medium">{t('plane.temporary')}</h3>
        {temporaryJobs.length ? <ul className="mb-4 divide-y divide-hairline">{temporaryJobs.map((job) => {
          const descriptor = jobs.data?.job_descriptors?.find((item) => item.id === job)
          const title = descriptor?.title?.trim() || t('plane.background')
          return <li key={job} className="py-3">
            <p className="break-words text-body font-medium [overflow-wrap:anywhere]">{title}</p>
            <TechnicalDetails><p>{t('plane.jobId')}{job}</p></TechnicalDetails>
          </li>
        })}</ul> : <p className="mb-4 mt-2 text-meta text-ink-3">{t('plane.noTemporary')}</p>}
        <h3 className="text-body font-medium">{t('plane.savedSchedules')}</h3>
        {jobs.data?.scheduled.length ? jobs.data.scheduled.map((job, index) => <ScheduledRow key={job.schedule_id} job={job} number={index + 1} />)
          : <p className="mt-2 text-meta text-ink-3">{t('plane.noSchedules')}</p>}
      </ReadContent>
    </Panel>
    </div>
  </section>
}
