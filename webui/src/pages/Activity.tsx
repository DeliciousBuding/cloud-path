import { useMemo, useState } from 'react'
import { Link } from 'react-router'
import { useQuery } from '@tanstack/react-query'
import { Activity as ActivityIcon, FilterX, History, RefreshCw, Terminal } from 'lucide-react'
import { Badge, EmptyState, ErrorState, Panel, PageHeader, Segmented, Spinner } from '@/components/ui'
import { RowSkeleton } from '@/components/Skeleton'
import { EventFeed } from '@/components/EventFeed'
import { TrendChart } from '@/components/TrendChart'
import { api } from '@/lib/api'
import { useLive } from '@/store/ws'
import { useDevices } from '@/hooks/useDevices'
import { useEdges } from '@/hooks/useEdges'
import { useCapabilityIndex } from '@/hooks/useDescriptor'
import { bucketEventDensity, cmdMeta, cmdStatusMeta, eventLabel, fmtDateTime, fmtDay, fmtHourMin, fmtTime, mergeEvents, optionLabel } from '@/lib/format'
import { cn } from '@/lib/cn'
import type { CommandView } from '@/lib/types'
import type { CapabilityIndex } from '@/lib/descriptor'

/** 单次拉取上限：与后端 limit 上限一致，超出部分给出明确说明而不是静默截断 */
const PAGE_LIMIT = 500

type Tab = 'events' | 'commands'

/** 命令状态过滤项：取自平台级命令状态机（lib/format.ts CMD_STATUS_META），非设备语义 */
const STATUS_FILTERS = [
  { value: '', label: '全部状态' },
  { value: 'pending', label: '待发送' },
  { value: 'sent', label: '已下发' },
  { value: 'ok', label: '成功' },
  { value: 'failed', label: '失败' },
  { value: 'timeout', label: '超时' },
]

/** 下拉共用的样式（390px：min-w-0 + max-w-full，长设备名靠 option 自身截断） */
// 原生 select/option 不吃 CSS 截断：select 自身限宽 + overflow-hidden，option 文本另在 optionLabel 里收敛
const SELECT_CLS = 'min-w-0 max-w-full overflow-hidden rounded-full border border-hairline bg-surface px-3 py-1.5 text-xs font-medium outline-none transition-colors focus:border-accent'

/**
 * 活动页：事件与命令历史（/api/events、/api/commands），带设备 / 边缘 / 状态过滤。
 *
 * 约定：
 *   - 时间一律**绝对时间**（完整年月日时分秒），历史跨天时相对时间会误导；
 *   - 边缘过滤在前端按设备键前缀做（后端 commands/events 只接受 device 参数），
 *     并在有截断时明确说明，不假装「这就是全部」；
 *   - 事件流合并 WS 实时环形缓冲与 REST 历史并按 设备+时间+类型 去重。
 */
export default function Activity() {
  const [tab, setTab] = useState<Tab>('events')
  const [device, setDevice] = useState('')
  const [edge, setEdge] = useState('')
  const [types, setTypes] = useState<Set<string>>(new Set())
  const [status, setStatus] = useState('')

  const { list: devices } = useDevices()
  const { list: edges } = useEdges()
  const liveEvents = useLive((s) => s.events)
  const index = useCapabilityIndex()

  const evQuery = useQuery({
    queryKey: ['activity-events', device],
    queryFn: () => api.events({ device: device || undefined, limit: PAGE_LIMIT }),
    refetchInterval: 5000,
    enabled: tab === 'events',
  })
  const cmdQuery = useQuery({
    queryKey: ['activity-commands', device, status],
    queryFn: () => api.commands({ device: device || undefined, status: status || undefined, limit: PAGE_LIMIT }),
    refetchInterval: 5000,
    enabled: tab === 'commands',
  })

  const events = useMemo(() => {
    let live = liveEvents
    if (device) live = live.filter((e) => e.device_id === device)
    else if (edge) live = live.filter((e) => e.device_id.startsWith(`${edge}/`))
    return mergeEvents(live, evQuery.data?.events ?? [])
      .filter((e) => !edge || device || e.device_id.startsWith(`${edge}/`))
      .filter((e) => types.size === 0 || types.has(e.type))
  }, [liveEvents, evQuery.data, device, edge, types])

  const commands = useMemo(() => {
    const rows = cmdQuery.data?.commands ?? []
    return edge && !device ? rows.filter((c) => c.device_id.startsWith(`${edge}/`)) : rows
  }, [cmdQuery.data, edge, device])

  /** 事件密度（真实历史分布）：桶宽按跨度自动取人话单位；窗口起点如实标注，不承诺没覆盖的区间 */
  const density = useMemo(
    () => (tab === 'events' ? bucketEventDensity(events.map((e) => e.ts), Math.floor(Date.now() / 1000)) : null),
    [events, tab],
  )

  /** 设备 ID → 用户起的名字：命令行的目标列展示人话名，机器 ID 收进 title */
  const deviceNames = useMemo(() => new Map(
    devices.filter((d) => d.name).map((d) => [d.id, d.name as string]),
  ), [devices])

  /** 过滤选项由当前数据里出现过的类型动态生成——前端不维护事件类型枚举 */
  const typeOptions = useMemo(() => {
    const set = new Set<string>()
    for (const e of mergeEvents(liveEvents, evQuery.data?.events ?? [])) set.add(e.type)
    return [...set].sort()
  }, [liveEvents, evQuery.data])

  const active = evQuery.isFetching || cmdQuery.isFetching
  const query = tab === 'events' ? evQuery : cmdQuery
  const anyFilter = Boolean(device || edge || types.size || status)
  const rows = tab === 'events' ? events.length : commands.length
  const atLimit = ((tab === 'events' ? evQuery.data?.events.length : cmdQuery.data?.commands.length) ?? 0) >= PAGE_LIMIT

  const clearAll = () => { setDevice(''); setEdge(''); setTypes(new Set()); setStatus('') }

  return (
    <>
      <PageHeader
        title="活动"
        subtitle={`事件与命令历史 · 显示最近 ${rows} 条`}
        actions={
          <button type="button" className="btn btn-ghost" onClick={() => { void query.refetch() }} title="立即刷新">
            {active ? <Spinner size={13} /> : <RefreshCw size={13} />} 刷新
          </button>
        }
      />

      <Panel className="mb-5">
        <div className="flex flex-col gap-3">
          <div className="flex flex-wrap items-center gap-2">
            <Segmented
              label="活动类型"
              value={tab}
              onChange={(v) => setTab(v)}
              options={[
                { value: 'events', label: '事件流', icon: <ActivityIcon size={12} /> },
                { value: 'commands', label: '命令历史', icon: <Terminal size={12} /> },
              ]}
            />
            <label className="sr-only" htmlFor="act-device">按设备筛选</label>
            <select id="act-device" value={device} onChange={(e) => setDevice(e.target.value)} className={SELECT_CLS}>
              <option value="">全部设备</option>
              {devices.map((d) => (
                <option key={d.id} value={d.id}>
                  {optionLabel(d.name ? `${d.name}（${d.id}）` : d.id, 40)}
                </option>
              ))}
            </select>

            <label className="sr-only" htmlFor="act-edge">按边缘节点筛选</label>
            <select id="act-edge" value={edge} disabled={Boolean(device)}
              onChange={(e) => setEdge(e.target.value)} className={cn(SELECT_CLS, 'disabled:opacity-50')}
              title={device ? '已按具体设备筛选' : undefined}>
              <option value="">全部边缘节点</option>
              {edges.map((e) => (
                <option key={e.edge_id} value={e.edge_id}>{optionLabel(e.edge_id, 40)}</option>
              ))}
            </select>

            {tab === 'commands' && (
              <>
                <label className="sr-only" htmlFor="act-status">按命令状态筛选</label>
                <select id="act-status" value={status} onChange={(e) => setStatus(e.target.value)} className={SELECT_CLS}>
                  {STATUS_FILTERS.map((s) => <option key={s.value} value={s.value}>{s.label}</option>)}
                </select>
              </>
            )}

            {anyFilter && (
              <button type="button" onClick={clearAll} className="link flex items-center gap-0.5 text-[12px]" title="清除全部筛选">
                <FilterX size={11} /> 清除筛选
              </button>
            )}
          </div>

          {tab === 'events' && typeOptions.length > 0 && (
            <div className="flex flex-wrap items-center gap-1.5 border-t border-hairline pt-3">
              <span className="mr-0.5 text-[12px] text-ink-3">事件类型</span>
              {typeOptions.slice(0, 24).map((t) => (
                <button
                  key={t} type="button" onClick={() => setTypes((prev) => {
                    const next = new Set(prev)
                    if (next.has(t)) next.delete(t)
                    else next.add(t)
                    return next
                  })}
                  aria-pressed={types.has(t)} title={t}
                  className={cn('max-w-full truncate rounded-full px-3 py-1 text-[12px] font-medium transition-colors',
                    types.has(t) ? 'bg-accent text-accent-ink' : 'bg-ink-3/10 text-ink-2 hover:bg-ink-3/16')}
                >
                  {eventLabel(t, index)}
                </button>
              ))}
              {typeOptions.length > 24 && (
                <span className="text-[12px] text-ink-3">另有 {typeOptions.length - 24} 种</span>
              )}
            </div>
          )}
        </div>
      </Panel>

      {query.error ? (
        <ErrorState
          icon={<FilterX size={20} />}
          title={tab === 'events' ? '事件历史加载失败' : '命令历史加载失败'}
          hint="拿不到历史记录（可能是 server 不可达或存储未启用）。实时通道上报的内容不受影响。"
          onRetry={() => { void query.refetch() }}
          retrying={query.isFetching}
        />
      ) : (
        <Panel
          title={
            <span className="flex items-center gap-1.5">
              {tab === 'events' ? <ActivityIcon size={14} /> : <History size={14} />}
              {tab === 'events' ? '事件流' : '命令历史'}
            </span>
          }
          right={query.isFetching ? <Spinner size={12} className="text-ink-3" /> : undefined}
        >
          {query.isLoading ? (
            <RowSkeleton rows={8} />
          ) : tab === 'events' ? (
            events.length === 0 ? (
              <EmptyState icon={<ActivityIcon size={24} />} title="没有匹配的事件"
                hint={anyFilter ? '试试清除筛选条件，或换一个设备 / 边缘节点。' : '设备上报事件后会出现在这里。'} />
            ) : (
              <>
                {density && (
                  <div className="mb-4 border-b border-hairline pb-4">
                    <div className="mb-1.5 flex flex-wrap items-baseline justify-between gap-2">
                      <span className="text-[12px] font-medium text-ink-3">事件密度 · 每{density.label}</span>
                      <span className="num min-w-0 truncate text-[12px] text-ink-3"
                        title={`窗口 ${fmtDateTime(density.points[0].t)} 至今 · 共 ${events.length} 条 · 峰值 ${density.peak} 条/${density.label}`}>
                        共 {events.length} 条 · 峰值 {density.peak} 条/{density.label}
                        <span className="hidden md:inline"> · 窗口起 {fmtDateTime(density.points[0].t)}</span>
                      </span>
                    </div>
                    <TrendChart points={density.points} kind="bar" zeroBase hideY unit="条" height={88} xTick={fmtHourMin} />
                  </div>
                )}
                <EventFeed events={events} limit={200} dayGrouped />
                {atLimit && <LimitNote what="事件" />}
              </>
            )
          ) : commands.length === 0 ? (
            <EmptyState icon={<Terminal size={24} />} title="没有匹配的命令"
              hint={anyFilter ? '试试清除筛选条件或换一个状态。' : '在设备详情页下发命令后，回执会出现在这里。'} />
          ) : (
            <>
              <CommandRows rows={commands} names={deviceNames} index={index} />
              {atLimit && <LimitNote what="命令" />}
            </>
          )}
        </Panel>
      )}
    </>
  )
}

function LimitNote({ what }: { what: string }) {
  return (
    <p className="mt-3 border-t border-hairline pt-3 text-center text-[12px] text-ink-3">
      仅显示最近 {PAGE_LIMIT} 条{what}（更早的历史仍在数据库中，可按设备或状态筛选查看）
    </p>
  )
}

/**
 * 命令历史：跨天按天分组（组头承载日期，与事件流同一视觉语言），行内只留时刻。
 * 机器 cmd / args / 成功回执一律收进 title（悬停可查），只有失败原因才是需要行内呈现的人话信息。
 */
function CommandRows({ rows, names, index }: {
  rows: CommandView[]; names: Map<string, string>; index: CapabilityIndex
}) {
  const groups: { day: string; items: CommandView[] }[] = []
  for (const c of rows.slice(0, 200)) {
    const day = fmtDay(c.created_at)
    const last = groups[groups.length - 1]
    if (last && last.day === day) last.items.push(c)
    else groups.push({ day, items: [c] })
  }
  return (
    <div className="space-y-4">
      {groups.map((g, gi) => (
        <section key={`${g.day}-${gi}`}>
          <h4 className="mb-1 px-0.5 text-[12px] font-medium text-ink-3">{g.day}</h4>
          <ul className="divide-y divide-hairline">
            {g.items.map((c) => <CommandRow key={c.id} c={c} names={names} index={index} />)}
          </ul>
        </section>
      ))}
    </div>
  )
}

function CommandRow({ c, names, index }: {
  c: CommandView; names: Map<string, string>; index: CapabilityIndex
}) {
  const st = cmdStatusMeta(c.status)
  const meta = cmdMeta(c.cmd, undefined, index)
  const [edgeId, devId] = c.device_id.split('/')
  return (
    // 390px：徽标与时刻不收缩，命令名 / 失败原因 / 目标三段各自 truncate，整行不撑宽容器
    <li className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 py-2.5">
      <Badge tone={st.tone} className="shrink-0">{st.label}</Badge>
      <span className="min-w-0 truncate text-xs font-medium"
        title={`${meta.hint || c.cmd}${c.args ? ` · args: ${c.args}` : ''}${c.result && st.tone === 'ok' ? ` · 回执: ${c.result}` : ''}`}>
        {meta.label}
      </span>
      {c.result && st.tone !== 'ok' && (
        <span className="min-w-0 max-w-full truncate text-[12px] text-bad" title={c.result}>
          {c.result}
        </span>
      )}
      <Link
        to={`/devices/${encodeURIComponent(edgeId ?? '')}/${encodeURIComponent(devId ?? '')}`}
        className="min-w-0 max-w-[10rem] truncate text-[12px] text-ink-3 transition-colors hover:text-accent"
        title={`${c.device_id} · 查看设备`}
      >
        {names.get(c.device_id) || devId}
      </Link>
      <span className="num ml-auto shrink-0 text-[12px] text-ink-3"
        title={c.acked_at ? `回执 ${fmtDateTime(c.acked_at)}` : fmtDateTime(c.created_at)}>
        {fmtTime(c.created_at)}
      </span>
    </li>
  )
}
