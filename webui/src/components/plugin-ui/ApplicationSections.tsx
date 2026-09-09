// White-listed renderers for Application UI sections.
//
// This file is intentionally data-driven: it contains no business vocabulary
// for pill boxes, music or any other showcase. Unknown values stay values;
// machine identifiers and raw JSON live in diagnostics/advanced details.
import type { ReactNode } from 'react'
import { AlertTriangle, BarChart3, Clock3, ListTree, Table2 } from 'lucide-react'
import { Badge, ErrorState, Panel, StatTile } from '@/components/ui'
import { RowSkeleton } from '@/components/Skeleton'
import { StructuredValue } from '@/components/StructuredValue'
import { ApplicationActions } from '@/components/plugin/ApplicationActions'
import { InstanceStatusSummary } from '@/components/plugin/DesiredObserved'
import { PluginConfigForm } from './PluginConfigForm'
import { PluginUIBridge } from './PluginUIBridge'
import { ApiError } from '@/lib/api'
import { appTime, bindingLabels, recordFieldLabel, recordHeadline, scheduleSummary, scheduleZone } from '@/lib/application-plane'
import { safeConfigEntries } from '@/lib/plugins'
import type {
  AppBindingsView, AppDomainRecordView, AppDomainRecordsView, AppJobsView, AppScheduledJobView,
  PluginCatalogView, PluginInstanceView, PluginUISection,
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
  configSchema?: string
  running?: boolean
  readOnly: boolean
  lifecycleKey: string
}

function ReadContent<T>({ title, query, empty, children }: {
  title: string
  query: SectionQuery<T>
  empty: boolean
  children: ReactNode
}) {
  if (query.isPending) return <div role="status" aria-label={title + '加载中'}><RowSkeleton rows={3} /></div>
  if (query.isError) {
    const status = query.error instanceof ApiError ? query.error.status : undefined
    return <ErrorState compact title={status === 403 ? '没有查看权限' : title + '加载失败'}
      hint={status === 403 ? '请联系管理员核对当前账号的访问权限。' : '暂时无法取得最新数据，请重试。'}
      onRetry={() => { void query.refetch() }} retrying={query.isFetching} />
  }
  if (empty) return <p className="py-3 text-sm text-ink-3">暂无{title}</p>
  return children
}

function parseRecord(record: AppDomainRecordView): { value: unknown; readable: boolean } {
  try { return { value: JSON.parse(record.data_json), readable: true } }
  catch { return { value: undefined, readable: false } }
}

function RecordDetails({ record }: { record: AppDomainRecordView }) {
  return <details className="mt-2 min-w-0 text-xs text-ink-2">
    <summary className="flex min-h-11 cursor-pointer items-center">查看技术详情</summary>
    <dl className="mt-2 space-y-1 rounded-lg bg-surface-2 px-3 py-2.5">
      <div><dt className="inline">记录标识：</dt><dd className="num inline break-all">{record.record_id}</dd></div>
      <div><dt className="inline">分类代码：</dt><dd className="num inline break-all">{record.record_type}</dd></div>
      <div><dt className="inline">版本：</dt><dd className="inline">{record.version || '未提供'}</dd></div>
    </dl>
    <pre tabIndex={0} role="group" aria-label="记录原文"
      className="num mt-2 max-h-64 overflow-auto whitespace-pre-wrap break-all rounded-lg bg-surface-2 p-3 font-mono text-xs">{record.data_json}</pre>
  </details>
}

function RecordItem({ record, index, presentation }: {
  record: AppDomainRecordView
  index: number
  presentation: string
}) {
  const parsed = parseRecord(record)
  const headline = parsed.readable
    ? recordHeadline(parsed.value, `记录 ${index + 1}`)
    : { title: `记录 ${index + 1}`, usedKeys: [] }
  if (presentation === 'table') {
    return <tr className="border-t border-hairline align-top">
      <td className="min-w-40 px-3 py-3 text-sm font-medium">{headline.title}</td>
      <td className="num whitespace-nowrap px-3 py-3 text-xs text-ink-3">{appTime(record.updated_at)}</td>
      <td className="px-3 py-3 text-sm">{parsed.readable
        ? <StructuredValue value={parsed.value} omitKeys={headline.usedKeys} />
        : <span className="text-warn">记录内容无法读取</span>}</td>
    </tr>
  }
  return <article className="min-w-0 border-t border-hairline py-5 first:border-0 first:pt-0">
    <div className="mb-3 flex flex-wrap items-baseline justify-between gap-2">
      <h3 className="min-w-0 break-words text-sm font-medium [overflow-wrap:anywhere]">{headline.title}</h3>
      <p className="shrink-0 text-xs text-ink-3">更新于 <time>{appTime(record.updated_at)}</time></p>
    </div>
    {parsed.readable
      ? <div className="min-w-0 text-sm"><StructuredValue value={parsed.value} omitKeys={headline.usedKeys} /></div>
      : <p className="text-sm text-warn">记录内容无法读取，原文可在技术详情中核对。</p>}
    <RecordDetails record={record} />
  </article>
}

function recordsForSection(records: AppDomainRecordView[] | undefined, section: PluginUISection): AppDomainRecordView[] {
  return (records ?? []).filter((record) => !section.recordType || record.record_type === section.recordType)
}

function RecordsSection({ query, section }: { query: SectionQuery<AppDomainRecordsView>; section: PluginUISection }) {
  const rows = recordsForSection(query.data?.records, section)
  const presentation = section.presentation ?? (section.type === 'timeline' ? 'timeline' : 'list')
  return <Panel title={<span className="flex items-center gap-1.5"><ListTree size={14} />{section.type === 'timeline' ? '时间线' : '记录'}</span>}>
    <ReadContent title="记录" query={query} empty={rows.length === 0}>
      {presentation === 'table'
        ? <div className="overflow-x-auto rounded-lg border border-hairline">
          <table className="w-full min-w-[36rem] border-collapse text-left">
            <thead><tr className="text-xs text-ink-3"><th className="px-3 py-2 font-medium">记录</th><th className="px-3 py-2 font-medium">时间</th><th className="px-3 py-2 font-medium">内容</th></tr></thead>
            <tbody>{rows.map((record, index) => <RecordItem key={record.record_id} record={record} index={index} presentation="table" />)}</tbody>
          </table>
        </div>
        : presentation === 'cards'
          ? <div className="grid gap-3 sm:grid-cols-2">{rows.map((record, index) => <div key={record.record_id} className="rounded-lg bg-surface-2 p-3"><RecordItem record={record} index={index} presentation="cards" /></div>)}</div>
          : <div className={presentation === 'timeline' ? 'border-l border-hairline pl-4' : ''}>{rows.map((record, index) => <RecordItem key={record.record_id} record={record} index={index} presentation={presentation} />)}</div>}
    </ReadContent>
  </Panel>
}

function BindingTable({ query, presentation }: { query: SectionQuery<AppBindingsView>; presentation?: unknown }) {
  return <Panel title={<span className="flex items-center gap-1.5"><Table2 size={14} />设备绑定</span>}>
    <ReadContent title="设备绑定" query={query} empty={!query.data?.bindings.length}>
      <div className="overflow-x-auto rounded-lg border border-hairline">
        <table className="w-full min-w-[30rem] border-collapse text-left text-sm">
          <thead><tr className="text-xs text-ink-3"><th className="px-3 py-2 font-medium">需求</th><th className="px-3 py-2 font-medium">功能</th><th className="px-3 py-2 font-medium">实体</th></tr></thead>
          <tbody>{query.data?.bindings.map((binding) => {
            const labels = bindingLabels(binding, presentation)
            return <tr key={binding.requirement_id + binding.entity_id} className="border-t border-hairline">
              <td className="px-3 py-2">{binding.requirement_id}</td>
              <td className="px-3 py-2 text-ink-2">{labels.capability}</td>
              <td className="num px-3 py-2 font-mono text-xs">{labels.entity || binding.entity_id}</td>
            </tr>
          })}</tbody>
        </table>
      </div>
    </ReadContent>
  </Panel>
}

function ScheduleSection({ query }: { query: SectionQuery<AppJobsView> }) {
  const rows = query.data?.scheduled ?? []
  return <Panel title={<span className="flex items-center gap-1.5"><Clock3 size={14} />计划</span>}>
    <ReadContent title="计划" query={query} empty={rows.length === 0}>
      <div className="divide-y divide-hairline">{rows.map((job: AppScheduledJobView, index) => {
        const state = ({ active: '已启用', cancelled: '已取消', paused: '已暂停' } as Record<string, string>)[job.state] ?? '状态待确认'
        return <article key={job.schedule_id} className="py-3 first:pt-0">
          <div className="flex flex-wrap items-center gap-2"><h3 className="text-sm font-medium">计划 {index + 1}</h3><Badge tone={job.state === 'active' ? 'accent' : 'idle'}>{state}</Badge></div>
          <p className="mt-2 text-sm">{scheduleSummary(job.cron)}<span className="ml-2 text-xs text-ink-3">{scheduleZone(job.timezone)}</span></p>
          <p className="mt-1 text-xs text-ink-3">下次：{job.next_run_at ? appTime(job.next_run_at, job.timezone) : '尚未安排'}</p>
        </article>
      })}</div>
    </ReadContent>
  </Panel>
}

function metricEntries(section: PluginUISection, records: AppDomainRecordView[]) {
  const latest = records[0] ? parseRecord(records[0]) : undefined
  const source = latest?.readable && latest.value && typeof latest.value === 'object' && !Array.isArray(latest.value)
    ? latest.value as Record<string, unknown> : {}
  if (section.fields?.length) return section.fields.map((field) => ({ label: field.label || recordFieldLabel(field.key), value: source[field.key] }))
  return Object.entries(source).filter(([, value]) => typeof value === 'number' || typeof value === 'boolean' || typeof value === 'string').slice(0, 6)
    .map(([key, value]) => ({ label: recordFieldLabel(key), value }))
}

function MetricsSection({ section, records }: { section: PluginUISection; records: AppDomainRecordView[] }) {
  const entries = metricEntries(section, recordsForSection(records, section))
  if (entries.length === 0) return <Panel title="指标"><p className="text-sm text-ink-3">暂无可用指标。</p></Panel>
  return <Panel title={<span className="flex items-center gap-1.5"><BarChart3 size={14} />指标</span>}>
    <div className="grid grid-cols-2 gap-3 sm:grid-cols-3">{entries.map((entry) => <StatTile key={entry.label} label={entry.label}
      value={typeof entry.value === 'boolean' ? (entry.value ? '是' : '否') : entry.value === undefined ? '—' : String(entry.value)} />)}</div>
  </Panel>
}

function ChartSection({ section, records }: { section: PluginUISection; records: AppDomainRecordView[] }) {
  const values = recordsForSection(records, section).map((record) => parseRecord(record).value).filter((value): value is Record<string, unknown> => Boolean(value) && typeof value === 'object' && !Array.isArray(value))
  const firstNumeric = values.flatMap((value) => Object.entries(value).filter(([, item]) => typeof item === 'number'))[0]
  const series = firstNumeric ? values.map((value) => typeof value[firstNumeric[0]] === 'number' ? value[firstNumeric[0]] as number : 0) : []
  const max = Math.max(...series, 1)
  if (!firstNumeric || series.length === 0) return <Panel title="趋势"><p className="text-sm text-ink-3">暂无可用数值趋势。</p></Panel>
  return <Panel title={<span className="flex items-center gap-1.5"><BarChart3 size={14} />趋势</span>}>
    <p className="mb-3 text-xs text-ink-3">最近 {series.length} 条记录中的 {recordFieldLabel(firstNumeric[0])}</p>
    <div className="flex h-28 items-end gap-1" role="img" aria-label={recordFieldLabel(firstNumeric[0]) + '趋势'}>
      {series.slice().reverse().map((value, index) => <span key={index} title={String(value)} className="min-h-1 flex-1 rounded-t bg-accent/60" style={{ height: `${Math.max(4, value / max * 100)}%` }} />)}
    </div>
  </Panel>
}

function MarkdownSection({ text }: { text: string }) {
  const lines = text.split(/\r?\n/)
  return <Panel title="说明"><div className="space-y-2 text-sm leading-relaxed text-ink-2">
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
  const config = safeConfigEntries(instance.desired.config)
  return <Panel title={<span className="flex items-center gap-1.5"><AlertTriangle size={14} />诊断</span>}>
    <dl className="space-y-2 text-sm">
      <div className="flex justify-between gap-3"><dt className="text-ink-2">实例</dt><dd className="num min-w-0 truncate font-mono text-xs">{instance.desired.instance_id || instance.id}</dd></div>
      <div className="flex justify-between gap-3"><dt className="text-ink-2">运行状态</dt><dd>{instance.has_observed ? instance.observed?.state || '未提供' : '尚未上报'}</dd></div>
      <div className="flex justify-between gap-3"><dt className="text-ink-2">设置版本</dt><dd className="num">{instance.desired_revision}</dd></div>
      <div className="flex justify-between gap-3"><dt className="text-ink-2">运行状态版本</dt><dd className="num">{instance.applied_revision}</dd></div>
    </dl>
    <details className="mt-3 text-xs text-ink-2"><summary className="flex min-h-11 cursor-pointer items-center">原始配置与绑定</summary>
      <div className="mt-2 space-y-2 rounded-lg bg-surface-2 p-3">
        {config.map((entry) => <p key={entry.key} className="break-all"><span className="text-ink-3">{entry.key}：</span>{entry.isSecret ? '密钥名称已隐藏内容' : entry.value}</p>)}
        <p>绑定：{bindings.data?.bindings.length ?? 0} 项 · 操作：{jobs.data?.job_descriptors.length ?? 0} 项</p>
      </div>
    </details>
  </Panel>
}

export function ApplicationSection(props: ApplicationSectionProps) {
  const { instance, catalog, section, records, bindings, jobs, presentation, configSchema, running, readOnly, lifecycleKey } = props
  const rows = records.data?.records ?? []
  switch (section.type) {
    case 'status':
      return <Panel title="当前状态"><InstanceStatusSummary v={instance} /></Panel>
    case 'metrics':
      return <MetricsSection section={section} records={rows} />
    case 'actions':
      return <Panel title="可执行操作"><ApplicationActions instanceID={instance.desired.instance_id} jobs={jobs.data}
        running={running} desiredEnabled={instance.desired.enabled} lifecycleKey={lifecycleKey} /></Panel>
    case 'records':
    case 'timeline':
      return <RecordsSection query={records} section={section} />
    case 'table':
      return section.source === 'bindings'
        ? <BindingTable query={bindings} presentation={presentation} />
        : <RecordsSection query={records} section={{ ...section, presentation: 'table' }} />
    case 'schedule':
      return <ScheduleSection query={jobs} />
    case 'chart':
      return <ChartSection section={section} records={rows} />
    case 'form':
      return <Panel title="设置"><PluginConfigForm instance={instance} section={section} configSchema={configSchema} readOnly={readOnly} /></Panel>
    case 'markdown':
      return <MarkdownSection text={section.text ?? ''} />
    case 'diagnostics':
      return <DiagnosticsSection instance={instance} bindings={bindings} jobs={jobs} />
    case 'custom':
      return <PluginUIBridge pluginId={catalog.id} version={catalog.version || instance.desired.version} instance={instance} section={section} />
    default:
      return <div role="alert" className="rounded-lg bg-warn/12 px-3.5 py-3 text-sm text-warn">这个页面包含当前版本无法显示的内容。请更新平台或联系插件维护者。</div>
  }
}
