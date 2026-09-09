// White-listed renderers for Application UI sections.
//
// This file is intentionally data-driven: it contains no business vocabulary
// for pill boxes, music or any other showcase. Unknown values stay values;
// machine identifiers and raw JSON live in diagnostics/advanced details.
import { useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { AlertTriangle, BarChart3, Clock3, ListTree, Table2 } from 'lucide-react'
import { Badge, ErrorState, Panel, StatTile } from '@/components/ui'
import { RowSkeleton } from '@/components/Skeleton'
import { StructuredValue } from '@/components/StructuredValue'
import { ApplicationActions } from '@/components/plugin/ApplicationActions'
import { InstanceStatusSummary } from '@/components/plugin/DesiredObserved'
import { PluginConfigForm } from './PluginConfigForm'
import { PluginUIBridge } from './PluginUIBridge'
import { ApiError } from '@/lib/api'
import { appTime, bindingLabels, recordFieldLabel, recordHeadline, recordTimestamp, scheduleSummary, scheduleZone } from '@/lib/application-plane'
import { safeConfigEntries } from '@/lib/plugins'
import type {
  AppBindingsView, AppDomainRecordView, AppDomainRecordsView, AppJobsView, AppScheduledJobView,
  PluginCatalogView, PluginInstanceView, PluginUIField, PluginUISection,
} from '@/lib/types'

export interface SectionQuery<T> {
  isPending: boolean
  isError: boolean
  error: unknown
  isFetching: boolean
  data?: T
  refetch: () => unknown
}

export interface ApplicationSectionProps {
  instance: PluginInstanceView
  catalog: PluginCatalogView
  section: PluginUISection
  records: SectionQuery<AppDomainRecordsView>
  bindings: SectionQuery<AppBindingsView>
  jobs: SectionQuery<AppJobsView>
  presentation?: unknown
  running?: boolean
  readOnly: boolean
  lifecycleKey: string
}

function sectionTitle(section: PluginUISection, fallback: string): string {
  return section.title?.trim() || fallback
}

function SectionIntro({ text }: { text?: string }) {
  if (!text?.trim()) return null
  return <p className="mb-3 text-meta leading-relaxed text-ink-3">{text}</p>
}

function ReadContent<T>({ title, query, empty, emptyText, children }: {
  title: string
  query: SectionQuery<T>
  empty: boolean
  emptyText?: string
  children: ReactNode
}) {
  const { t } = useTranslation('plugin')
  if (query.isPending) return <div role="status" aria-label={t('plane.loadingAria', { title })}><RowSkeleton rows={3} /></div>
  if (query.isError) {
    const status = query.error instanceof ApiError ? query.error.status : undefined
    return <ErrorState compact title={status === 403 ? t('plane.permissionDenied') : t('plane.loadFailed', { title })}
      hint={status === 403 ? t('plane.permissionHint') : t('plane.loadHint')}
      onRetry={() => { void query.refetch() }} retrying={query.isFetching} />
  }
  if (empty) return <p className="py-3 text-body text-ink-3">{emptyText?.trim() || t('plane.empty', { title })}</p>
  return children
}

function parseRecord(record: AppDomainRecordView): { value: unknown; readable: boolean } {
  try { return { value: JSON.parse(record.data_json), readable: true } }
  catch { return { value: undefined, readable: false } }
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return Boolean(value) && typeof value === 'object' && !Array.isArray(value)
}

/** Read a flat or dotted field path from a record without evaluating expressions. */
function recordValue(source: Record<string, unknown>, key: string): unknown {
  if (!key.includes('.')) return source[key]
  let current: unknown = source
  for (const part of key.split('.')) {
    if (!isRecord(current)) return undefined
    current = current[part]
  }
  return current
}

function formatFieldValue(field: PluginUIField, value: unknown, t: (key: string, options?: Record<string, unknown>) => string): string {
  if (value === undefined || value === null || value === '') return t('record.empty')
  if (field.values && Object.prototype.hasOwnProperty.call(field.values, String(value))) return field.values[String(value)]
  if (typeof value === 'boolean') return value ? t('record.yes') : t('record.no')
  if (field.format === 'time' || /(^|_)(at|time)$/.test(field.key) || /^(start|end)$/.test(field.key)) {
    if (typeof value === 'number' && Number.isFinite(value)) return appTime(value)
    if (typeof value === 'string') return recordTimestamp(value) ?? value
  }
  if (typeof value === 'number' && Number.isFinite(value)) {
    if (field.format === 'percent') return `${Number((value * 100).toFixed(field.precision ?? 0))}%`
    const numeric = field.precision !== undefined ? value.toFixed(field.precision) : Number.isInteger(value) ? String(value) : String(Number(value.toFixed(2)))
    if (field.format === 'duration') return `${numeric}${field.unit || 's'}`
    return field.unit ? `${numeric} ${field.unit}` : numeric
  }
  return field.unit ? `${String(value)} ${field.unit}` : String(value)
}

function RecordFields({ record, fields, omitKeys = [] }: { record: AppDomainRecordView; fields?: PluginUIField[]; omitKeys?: readonly string[] }) {
  const { t } = useTranslation('plugin')
  const parsed = parseRecord(record)
  if (!parsed.readable || !isRecord(parsed.value)) return <p className="text-body text-warn">{t('plane.unreadable')}</p>
  if (!fields?.length) return <StructuredValue value={parsed.value} omitKeys={omitKeys} maxPreview={4} />
  const shown = fields.map((field) => ({ field, value: recordValue(parsed.value as Record<string, unknown>, field.key) }))
    .filter(({ field, value }) => !field.hideWhenEmpty || (value !== undefined && value !== null && value !== ''))
  if (shown.length === 0) return <p className="text-body text-ink-3">{t('record.noFilled')}</p>
  return <dl className="grid min-w-0 gap-x-8 gap-y-3 sm:grid-cols-2">
    {shown.map(({ field, value }) => <div key={field.key} className="min-w-0">
      <dt className="text-meta text-ink-3">{field.label || recordFieldLabel(field.key)}</dt>
      <dd className="mt-1 min-w-0 break-words text-body leading-relaxed text-ink-2 [overflow-wrap:anywhere]">
        {formatFieldValue(field, value, t)}
      </dd>
    </div>)}
  </dl>
}

function RecordDetails({ record }: { record: AppDomainRecordView }) {
  const { t } = useTranslation('plugin')
  return <details className="mt-2 min-w-0 text-meta text-ink-2">
    <summary className="flex min-h-touch cursor-pointer items-center">{t('plane.viewDetails')}</summary>
    <dl className="mt-2 space-y-1 rounded-tile bg-surface-2 px-3 py-2.5">
      <div><dt className="inline">{t('plane.recordId')}</dt><dd className="num inline break-all">{record.record_id}</dd></div>
      <div><dt className="inline">{t('plane.recordType')}</dt><dd className="num inline break-all">{record.record_type}</dd></div>
      <div><dt className="inline">{t('plane.version')}</dt><dd className="inline">{record.version || t('common.notProvided')}</dd></div>
    </dl>
    <pre tabIndex={0} role="group" aria-label={t('plane.rawAria')}
      className="num mt-2 max-h-64 overflow-auto whitespace-pre-wrap break-all rounded-tile bg-surface-2 p-3 font-mono text-meta">{record.data_json}</pre>
  </details>
}

function RecordItem({ record, index, presentation, section }: {
  record: AppDomainRecordView
  index: number
  presentation: string
  section: PluginUISection
}) {
  const { t } = useTranslation('plugin')
  const parsed = parseRecord(record)
  const headline = parsed.readable
    ? recordHeadline(parsed.value, t('plane.record', { number: index + 1 }))
    : { title: t('plane.record', { number: index + 1 }), usedKeys: [] }
  if (presentation === 'table') {
    return <tr className="border-t border-hairline align-top">
      <td className="min-w-40 px-3 py-3 text-body font-medium">{headline.title}</td>
      <td className="num whitespace-nowrap px-3 py-3 text-meta text-ink-3">{appTime(record.updated_at)}</td>
      <td className="px-3 py-3 text-body">{parsed.readable
        ? <RecordFields record={record} fields={section.fields} omitKeys={headline.usedKeys} />
        : <span className="text-warn">{t('plane.unreadableShort')}</span>}</td>
    </tr>
  }
  return <article className="min-w-0 border-t border-hairline py-4 first:border-0 first:pt-0">
    <div className="mb-3 flex flex-wrap items-baseline justify-between gap-2">
      <h3 className="min-w-0 break-words text-body font-medium [overflow-wrap:anywhere]">{headline.title}</h3>
      <p className="shrink-0 text-meta text-ink-3">{t('plane.updatedAt')} <time>{appTime(record.updated_at)}</time></p>
    </div>
    {parsed.readable
      ? <RecordFields record={record} fields={section.fields} omitKeys={headline.usedKeys} />
      : <p className="text-body text-warn">{t('plane.unreadable')}</p>}
    <RecordDetails record={record} />
  </article>
}

function recordsForSection(records: AppDomainRecordView[] | undefined, section: PluginUISection): AppDomainRecordView[] {
  return (records ?? []).filter((record) => !section.recordType || record.record_type === section.recordType)
}

function RecordsSection({ query, section }: { query: SectionQuery<AppDomainRecordsView>; section: PluginUISection }) {
  const { t } = useTranslation('plugin')
  const [expanded, setExpanded] = useState(false)
  const rows = recordsForSection(query.data?.records, section)
  const visible = expanded ? rows : rows.slice(0, 5)
  const presentation = section.presentation ?? (section.type === 'timeline' ? 'timeline' : 'list')
  const title = sectionTitle(section, section.type === 'timeline' ? t('sections.timeline') : t('sections.records'))
  return <Panel title={<span className="flex items-center gap-1.5"><ListTree size={14} />{title}</span>}>
    <SectionIntro text={section.description} />
    <ReadContent title={title} query={query} empty={rows.length === 0} emptyText={section.emptyText}>
      {presentation === 'table'
        ? <div className="overflow-x-auto rounded-tile border border-hairline">
          <table className="w-full min-w-[36rem] border-collapse text-left">
            <thead><tr className="text-meta text-ink-3"><th className="px-3 py-2 font-medium">{t('sections.record')}</th><th className="px-3 py-2 font-medium">{t('sections.time')}</th><th className="px-3 py-2 font-medium">{t('sections.content')}</th></tr></thead>
            <tbody>{visible.map((record, index) => <RecordItem key={record.record_id} record={record} index={index} presentation="table" section={section} />)}</tbody>
          </table>
        </div>
        : presentation === 'cards'
          ? <div className="grid gap-3 sm:grid-cols-2">{visible.map((record, index) => <div key={record.record_id} className="rounded-tile bg-surface-2 p-4"><RecordItem record={record} index={index} presentation="cards" section={section} /></div>)}</div>
          : <div className={presentation === 'timeline' ? 'border-l border-hairline pl-4' : ''}>{visible.map((record, index) => <RecordItem key={record.record_id} record={record} index={index} presentation={presentation} section={section} />)}</div>}
      {rows.length > 5 && <button type="button" className="btn btn-ghost mt-3" onClick={() => setExpanded((value) => !value)}>
        {expanded ? t('sections.showLess') : t('sections.showMore', { count: rows.length - 5 })}
      </button>}
    </ReadContent>
  </Panel>
}

function BindingTable({ query, presentation, section }: { query: SectionQuery<AppBindingsView>; presentation?: unknown; section: PluginUISection }) {
  const { t } = useTranslation('plugin')
  const title = sectionTitle(section, t('sections.bindings'))
  return <Panel title={<span className="flex items-center gap-1.5"><Table2 size={14} />{title}</span>}>
    <SectionIntro text={section.description} />
    <ReadContent title={title} query={query} empty={!query.data?.bindings.length} emptyText={section.emptyText}>
      <div className="overflow-x-auto rounded-tile border border-hairline">
        <table className="w-full min-w-[30rem] border-collapse text-left text-body">
          <thead><tr className="text-meta text-ink-3"><th className="px-3 py-2 font-medium">{t('sections.requirement')}</th><th className="px-3 py-2 font-medium">{t('sections.capability')}</th><th className="px-3 py-2 font-medium">{t('sections.entity')}</th></tr></thead>
          <tbody>{query.data?.bindings.map((binding) => {
            const labels = bindingLabels(binding, presentation)
            return <tr key={binding.requirement_id + binding.entity_id} className="border-t border-hairline">
              <td className="px-3 py-2">{binding.requirement_id}</td>
              <td className="px-3 py-2 text-ink-2">{labels.capability}</td>
              <td className="num px-3 py-2 font-mono text-meta">{labels.entity || binding.entity_id}</td>
            </tr>
          })}</tbody>
        </table>
      </div>
    </ReadContent>
  </Panel>
}

function ScheduleSection({ query, section }: { query: SectionQuery<AppJobsView>; section: PluginUISection }) {
  const { t } = useTranslation('plugin')
  const rows = query.data?.scheduled ?? []
  const title = sectionTitle(section, t('sections.schedule'))
  return <Panel title={<span className="flex items-center gap-1.5"><Clock3 size={14} />{title}</span>}>
    <SectionIntro text={section.description} />
    <ReadContent title={title} query={query} empty={rows.length === 0} emptyText={section.emptyText}>
      <div className="divide-y divide-hairline">{rows.map((job: AppScheduledJobView, index) => {
        const state = ({ active: t('plane.active'), cancelled: t('plane.cancelled'), paused: t('plane.paused') } as Record<string, string>)[job.state] ?? t('plane.statusUnknown')
        return <article key={job.schedule_id} className="py-3 first:pt-0">
          <div className="flex flex-wrap items-center gap-2"><h3 className="text-body font-medium">{t('plane.schedule', { number: index + 1 })}</h3><Badge tone={job.state === 'active' ? 'accent' : 'idle'}>{state}</Badge></div>
          <p className="mt-2 text-body">{scheduleSummary(job.cron)}<span className="ml-2 text-meta text-ink-3">{scheduleZone(job.timezone)}</span></p>
          <p className="mt-1 text-meta text-ink-3">{t('sections.next')}{job.next_run_at ? appTime(job.next_run_at, job.timezone) : t('plane.notScheduled')}</p>
        </article>
      })}</div>
    </ReadContent>
  </Panel>
}

const TECHNICAL_METRIC_KEY = /(^|_)(id|request|revision|version|schema|raw|config|binding|runtime|persistent|digest|error_code|entity|requirement|source|thresholds)$/i

function metricEntries(section: PluginUISection, records: AppDomainRecordView[]) {
  const latest = records[0] ? parseRecord(records[0]) : undefined
  const source = latest?.readable && isRecord(latest.value) ? latest.value : {}
  const fields = section.fields?.length ? section.fields : Object.entries(source)
    .filter(([key, value]) => !TECHNICAL_METRIC_KEY.test(key) && (typeof value === 'number' || typeof value === 'boolean' || typeof value === 'string'))
    .slice(0, 4)
    .map(([key]): PluginUIField => ({ key, label: recordFieldLabel(key) }))
  return fields.map((field) => ({ field, value: recordValue(source, field.key) }))
    .filter(({ field, value }) => !field.hideWhenEmpty || (value !== undefined && value !== null && value !== ''))
}

function MetricsSection({ section, records }: { section: PluginUISection; records: AppDomainRecordView[] }) {
  const { t } = useTranslation('plugin')
  const entries = metricEntries(section, recordsForSection(records, section))
  const title = sectionTitle(section, t('sections.metrics'))
  if (entries.length === 0) return <Panel title={title}><SectionIntro text={section.description} /><p className="text-body text-ink-3">{section.emptyText || t('sections.noMetrics')}</p></Panel>
  return <Panel title={<span className="flex items-center gap-1.5"><BarChart3 size={14} />{title}</span>}>
    <SectionIntro text={section.description} />
    <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-4">{entries.map(({ field, value }) => <StatTile key={field.key}
      label={field.label || recordFieldLabel(field.key)} value={formatFieldValue(field, value, t)} />)}</div>
  </Panel>
}

function ChartSection({ section, records }: { section: PluginUISection; records: AppDomainRecordView[] }) {
  const { t } = useTranslation('plugin')
  const title = sectionTitle(section, t('sections.trend'))
  const values = recordsForSection(records, section).map((record) => parseRecord(record).value).filter((value): value is Record<string, unknown> => isRecord(value))
  const requested = section.fields?.[0]
  const firstNumeric = requested
    ? [requested.key, values.find((value) => typeof recordValue(value, requested.key) === 'number')?.[requested.key]] as const
    : values.flatMap((value) => Object.entries(value).filter(([, item]) => typeof item === 'number'))[0]
  const series = firstNumeric && firstNumeric[0]
    ? values.map((value) => typeof recordValue(value, firstNumeric[0]) === 'number' ? recordValue(value, firstNumeric[0]) as number : 0)
    : []
  const max = Math.max(...series, 1)
  if (!firstNumeric || !firstNumeric[0] || series.length === 0) return <Panel title={title}><SectionIntro text={section.description} /><p className="text-body text-ink-3">{section.emptyText || t('sections.noTrend')}</p></Panel>
  const fieldLabel = requested?.label || recordFieldLabel(firstNumeric[0])
  return <Panel title={<span className="flex items-center gap-1.5"><BarChart3 size={14} />{title}</span>}>
    <SectionIntro text={section.description} />
    <p className="mb-3 text-meta text-ink-3">{t('sections.recent', { count: series.length, field: fieldLabel })}</p>
    <div className="flex h-28 items-end gap-1" role="img" aria-label={t('sections.trendAria', { field: fieldLabel })}>
      {series.slice().reverse().map((value, index) => <span key={index} title={String(value)} className="min-h-1 flex-1 rounded-t bg-accent/60" style={{ height: `${Math.max(4, value / max * 100)}%` }} />)}
    </div>
  </Panel>
}

function MarkdownSection({ section }: { section: PluginUISection }) {
  const { t } = useTranslation('plugin')
  const text = section.text ?? ''
  const lines = text.split(/\r?\n/)
  return <Panel title={sectionTitle(section, t('sections.description'))}>
    <SectionIntro text={section.description} />
    <div className="space-y-2 text-body leading-relaxed text-ink-2">
    {lines.map((line, index) => {
      if (line.startsWith('### ')) return <h4 key={index} className="font-semibold text-ink">{line.slice(4)}</h4>
      if (line.startsWith('## ')) return <h3 key={index} className="font-semibold text-ink">{line.slice(3)}</h3>
      if (line.startsWith('# ')) return <h2 key={index} className="font-semibold text-ink">{line.slice(2)}</h2>
      if (line.startsWith('- ')) return <p key={index} className="pl-3">• {line.slice(2)}</p>
      if (!line.trim()) return <div key={index} className="h-1" />
      return <p key={index}>{line}</p>
    })}
    </div></Panel>
}

function DiagnosticsSection({ instance, bindings, jobs }: { instance: PluginInstanceView; bindings: SectionQuery<AppBindingsView>; jobs: SectionQuery<AppJobsView> }) {
  const { t } = useTranslation('plugin')
  const config = safeConfigEntries(instance.desired.config)
  return <Panel title={<span className="flex items-center gap-1.5"><AlertTriangle size={14} />{t('sections.diagnostics')}</span>}>
    <dl className="space-y-2 text-body">
      <div className="flex justify-between gap-3"><dt className="text-ink-2">{t('sections.instance')}</dt><dd className="num min-w-0 truncate font-mono text-meta">{instance.desired.instance_id || instance.id}</dd></div>
      <div className="flex justify-between gap-3"><dt className="text-ink-2">{t('sections.state')}</dt><dd>{instance.has_observed ? instance.observed?.state || t('common.notProvided') : t('common.notReported')}</dd></div>
      <div className="flex justify-between gap-3"><dt className="text-ink-2">{t('sections.desiredRevision')}</dt><dd className="num">{instance.desired_revision}</dd></div>
      <div className="flex justify-between gap-3"><dt className="text-ink-2">{t('sections.appliedRevision')}</dt><dd className="num">{instance.applied_revision}</dd></div>
    </dl>
    <details className="mt-3 text-meta text-ink-2"><summary className="flex min-h-touch cursor-pointer items-center">{t('sections.rawConfig')}</summary>
      <div className="mt-2 space-y-2 rounded-tile bg-surface-2 p-3">
        {config.map((entry) => <p key={entry.key} className="break-all"><span className="text-ink-3">{entry.key}：</span>{entry.isSecret ? t('sections.secretHidden') : entry.value}</p>)}
        <p>{t('sections.bindingCount', { count: bindings.data?.bindings.length ?? 0, jobs: jobs.data?.job_descriptors.length ?? 0 })}</p>
      </div>
    </details>
  </Panel>
}

export function ApplicationSection(props: ApplicationSectionProps) {
  const { t } = useTranslation('plugin')
  const { instance, catalog, section, records, bindings, jobs, presentation, running, readOnly, lifecycleKey } = props
  const rows = records.data?.records ?? []
  switch (section.type) {
    case 'status':
      return <Panel title={sectionTitle(section, t('sections.status'))}><SectionIntro text={section.description} /><InstanceStatusSummary v={instance} /></Panel>
    case 'metrics':
      return <MetricsSection section={section} records={rows} />
    case 'actions':
      return <Panel title={sectionTitle(section, t('sections.actions'))}><SectionIntro text={section.description} /><ApplicationActions instanceID={instance.desired.instance_id} jobs={jobs.data}
        running={running} desiredEnabled={instance.desired.enabled} lifecycleKey={lifecycleKey} /></Panel>
    case 'records':
    case 'timeline':
      return <RecordsSection query={records} section={section} />
    case 'table':
      return section.source === 'bindings'
        ? <BindingTable query={bindings} presentation={presentation} section={section} />
        : <RecordsSection query={records} section={{ ...section, presentation: 'table' }} />
    case 'schedule':
      return <ScheduleSection query={jobs} section={section} />
    case 'chart':
      return <ChartSection section={section} records={rows} />
    case 'form':
      return <Panel title={sectionTitle(section, t('sections.settings'))}><SectionIntro text={section.description} /><PluginConfigForm instance={instance} section={section} readOnly={readOnly} /></Panel>
    case 'markdown':
      return <MarkdownSection section={section} />
    case 'diagnostics':
      return <DiagnosticsSection instance={instance} bindings={bindings} jobs={jobs} />
    case 'custom':
      return <PluginUIBridge pluginId={catalog.id} version={catalog.version || instance.desired.version} instance={instance} section={section} />
    default:
      return <div role="alert" className="rounded-tile bg-warn/12 px-3.5 py-3 text-body text-warn">{t('sections.unsupported')}</div>
  }
}
