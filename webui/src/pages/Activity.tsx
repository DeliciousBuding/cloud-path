import { useMemo, useState } from 'react'
import { Link } from 'react-router'
import { useQuery } from '@tanstack/react-query'
import { Activity as ActivityIcon, FilterX, RefreshCw, Terminal, WifiOff } from 'lucide-react'
import { Badge, EmptyState, ErrorState, Panel, PageHeader, Segmented, Spinner } from '@/components/ui'
import { RowSkeleton } from '@/components/Skeleton'
import { EventFeed, commandDisplayMeta, commandFailureInfo, eventDisplayLabel } from '@/components/EventFeed'
import { api } from '@/lib/api'
import { useLive } from '@/store/ws'
import { useDevices } from '@/hooks/useDevices'
import { useEdges } from '@/hooks/useEdges'
import { useCapabilityIndex } from '@/hooks/useDescriptor'
import { cmdStatusMeta, fmtDateTime, fmtDay, fmtTime, mergeEvents, optionLabel } from '@/lib/format'
import { cn } from '@/lib/cn'
import type { CommandView } from '@/lib/types'
import type { CapabilityIndex } from '@/lib/descriptor'
import { usePageTitle } from '@/hooks/usePageTitle'

/** 单次拉取与当前展示共用同一上限；超出部分给出明确说明而不是静默截断 */
const PAGE_LIMIT = 200

type Tab = 'events' | 'commands'

/** 命令状态过滤项：取自平台级命令状态机（lib/format.ts CMD_STATUS_META），非设备语义 */
const STATUS_FILTERS = [
  { value: '', label: '全部状态' },
  { value: 'pending', label: '待发送' },
  { value: 'sent', label: '已发送' },
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
  usePageTitle('运行记录')

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

  /** 设备 ID → 用户起的名字：命令行的目标列展示人话名，机器 ID 收进 title */
  const deviceNames = useMemo(() => new Map(
    devices.filter((d) => d.name).map((d) => [d.id, d.name as string]),
  ), [devices])

  /** 过滤选项由当前数据里出现过的类型动态生成——前端不维护记录类型枚举 */
  const typeOptions = useMemo(() => {
    const set = new Set<string>()
    for (const e of mergeEvents(liveEvents, evQuery.data?.events ?? [])) set.add(e.type)
    return [...set].sort()
  }, [liveEvents, evQuery.data])

  const active = evQuery.isFetching || cmdQuery.isFetching
  const query = tab === 'events' ? evQuery : cmdQuery
  const eventFilter = Boolean(device || edge || types.size)
  const commandFilter = Boolean(device || edge || status)
  const anyFilter = tab === 'events' ? eventFilter : commandFilter
  const rows = Math.min(tab === 'events' ? events.length : commands.length, PAGE_LIMIT)
  const failedCount = commands.filter((c) => c.status === 'failed' || c.status === 'timeout').length
  const atLimit = tab === 'events'
    ? (evQuery.data?.events.length ?? 0) >= PAGE_LIMIT
    : (cmdQuery.data?.commands.length ?? 0) >= PAGE_LIMIT
  const subtitle = query.isLoading ? '正在加载记录…'
    : query.error ? '记录状态暂不可用'
      : tab === 'events'
        ? `设备主动上报的状态变化 · 最近 ${rows} 条`
        : `下发操作及执行结果 · 最近 ${rows} 条${failedCount > 0 ? ` · ${failedCount} 条失败或超时` : ''}`

  const clearAll = () => { setDevice(''); setEdge(''); setTypes(new Set()); setStatus('') }

  return (
    <>
      <PageHeader
        title="运行记录"
        subtitle={subtitle}
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
              label="记录类型"
              value={tab}
              onChange={(v) => setTab(v)}
              options={[
                { value: 'events', label: '状态记录', icon: <ActivityIcon size={12} /> },
                { value: 'commands', label: '操作记录', icon: <Terminal size={12} /> },
              ]}
            />
            <label className="sr-only" htmlFor="act-device">按设备筛选</label>
            <select id="act-device" value={device} onChange={(e) => setDevice(e.target.value)} className={SELECT_CLS}>
              <option value="">设备：全部</option>
              {devices.map((d) => (
                <option key={d.id} value={d.id}>
                  {optionLabel(d.name ? `${d.name}（${d.id}）` : d.id, 40)}
                </option>
              ))}
            </select>

            <label className="sr-only" htmlFor="act-edge">按网关筛选</label>
            <select id="act-edge" value={edge} disabled={Boolean(device)}
              onChange={(e) => setEdge(e.target.value)} className={cn(SELECT_CLS, 'disabled:opacity-50')}
              title={device ? '已按具体设备筛选' : undefined}>
              <option value="">网关：全部</option>
              {edges.map((e) => (
                <option key={e.edge_id} value={e.edge_id}>{optionLabel(e.edge_id, 40)}</option>
              ))}
            </select>

            {tab === 'commands' && (
              <>
                <label className="sr-only" htmlFor="act-status">按操作状态筛选</label>
                <select id="act-status" value={status} onChange={(e) => setStatus(e.target.value)} className={SELECT_CLS}>
                  {STATUS_FILTERS.map((s) => <option key={s.value} value={s.value}>{s.value ? s.label : '状态：全部'}</option>)}
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
            <details className="border-t border-hairline pt-3">
              <summary className="cursor-pointer select-none text-[12px] font-medium text-ink-2">
                按事件类型筛选
                {types.size > 0 && <span className="ml-1 text-accent">已选 {types.size} 项</span>}
              </summary>
              <div className="mt-2 flex flex-wrap items-center gap-1.5">
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
                    {eventDisplayLabel(t, index)}
                  </button>
                ))}
                {typeOptions.length > 24 && (
                  <span className="text-[12px] text-ink-3">另有 {typeOptions.length - 24} 种</span>
                )}
              </div>
            </details>
          )}
        </div>
      </Panel>

      {query.error ? (
        <ErrorState
          icon={<WifiOff size={20} />}
          title={tab === 'events' ? '状态记录加载失败' : '操作记录加载失败'}
          hint="暂时无法加载历史记录。实时连接收到的内容仍会显示；请稍后重试或联系管理员检查服务。"
          onRetry={() => { void query.refetch() }}
          retrying={query.isFetching}
        />
      ) : (
        <Panel>
          <div className="mb-3 flex flex-wrap items-center justify-between gap-2 border-b border-hairline pb-3">
            <span className="text-[12px] text-ink-3">
              {tab === 'events' ? '设备上报事件 · 新到旧' : '下发操作及执行结果 · 新到旧'}
            </span>
            <span className="flex items-center gap-2 text-[12px] text-ink-3">
              {query.isFetching && <Spinner size={12} />}
              <span className="num">当前 {rows} 条</span>
            </span>
          </div>
          {query.isLoading ? (
            <RowSkeleton rows={8} />
          ) : tab === 'events' ? (
            events.length === 0 ? (
              <EmptyState icon={<ActivityIcon size={24} />}
                title={eventFilter ? '没有匹配的状态记录' : '还没有状态记录'}
                hint={eventFilter ? '试试清除筛选条件，或换一个设备 / 网关 / 事件类型。' : '设备上报状态变化后会出现在这里。'} />
            ) : (
              <>
                {/* 长 ledger 本地滚动（Vercel: long ledgers may scroll locally）：
                    *  页面保持一屏可读，查找能力留在滚动容器内；组头 sticky 便于跨天定位 */}
                <div tabIndex={0} role="region" aria-label="状态记录列表"
                  className="max-h-[34rem] overflow-y-auto overscroll-contain pr-1">
                  <EventFeed events={events} limit={PAGE_LIMIT} dayGrouped />
                </div>
                {atLimit && <LimitNote what="状态记录" hint="可按设备、网关或事件类型筛选查看" />}
              </>
            )
          ) : commands.length === 0 ? (
            <EmptyState icon={<Terminal size={24} />}
              title={commandFilter ? '没有匹配的操作' : '还没有操作记录'}
              hint={commandFilter ? '试试清除筛选条件，或换一个设备 / 网关 / 状态。' : '在设备详情页下发操作后，执行结果会显示在这里。'} />
          ) : (
            <>
              <div tabIndex={0} role="region" aria-label="操作记录列表"
                className="max-h-[34rem] overflow-y-auto overscroll-contain pr-1">
                <CommandRows rows={commands} names={deviceNames} index={index} />
              </div>
              {atLimit && <LimitNote what="操作记录" hint="可按设备、网关或状态筛选查看" />}
            </>
          )}
        </Panel>
      )}
    </>
  )
}

function LimitNote({ what, hint }: { what: string; hint: string }) {
  return (
    <p className="mt-3 border-t border-hairline pt-3 text-center text-[12px] text-ink-3">
      仅显示最近 {PAGE_LIMIT} 条{what}（更早的记录仍在系统中，{hint}）
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
  for (const c of rows.slice(0, PAGE_LIMIT)) {
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
  const meta = commandDisplayMeta(c.cmd, index)
  const [edgeId, devId] = c.device_id.split('/')
  const failed = c.status === 'failed' || c.status === 'timeout'
  const failure = commandFailureInfo(c.result)
  const target = names.get(c.device_id) || devId
  return (
    // 390px：首行只放状态 / 操作 / 时刻；失败原因与目标放到第二行，避免四段横向挤成一团。
    <li className="grid min-w-0 grid-cols-[auto_minmax(0,1fr)_auto] items-center gap-x-3 gap-y-1 py-2.5 lg:grid-cols-[auto_minmax(10rem,auto)_minmax(0,1fr)_minmax(8rem,0.7fr)_auto]">
      <Badge tone={st.tone} className="shrink-0">{st.label}</Badge>
      <span className="min-w-0 truncate text-xs font-medium lg:col-start-2"
        title={`${c.cmd}${meta.hint ? ` · ${meta.hint}` : ''}${c.args ? ` · 参数: ${c.args}` : ''}${c.result && st.tone === 'ok' ? ` · 结果: ${c.result}` : ''}`}>
        {meta.label}
      </span>
      <div className="col-span-3 flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 lg:contents">
        {failed && (
          <span className="min-w-0 max-w-full break-words text-[12px] text-bad lg:col-start-3 lg:truncate" title={c.result}>
            失败原因：{failure.message} · {failure.next}
          </span>
        )}
        <Link
          to={`/devices/${encodeURIComponent(edgeId ?? '')}/${encodeURIComponent(devId ?? '')}`}
          className="min-w-0 max-w-full truncate text-[12px] text-ink-3 transition-colors hover:text-accent lg:col-start-4"
          title={`${c.device_id} · 查看设备`}
        >
          查看 {target}
        </Link>
      </div>
      <span className="num col-start-3 row-start-1 shrink-0 text-[12px] text-ink-3 lg:col-start-5 lg:row-start-1"
        title={c.acked_at ? `完成时间 ${fmtDateTime(c.acked_at)}` : fmtDateTime(c.created_at)}>
        {fmtTime(c.created_at)}
      </span>
    </li>
  )
}
