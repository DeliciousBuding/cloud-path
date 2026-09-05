import { useMemo, useState } from 'react'
import { Link, useParams } from 'react-router'
import { useQuery } from '@tanstack/react-query'
import {Activity, ArrowRight, Braces, Command, Grid3x3, History, LayoutDashboard, Radio, RadioTower, Sparkles, Terminal, Zap} from 'lucide-react'
import {
  BackLink, Badge, EmptyState, ErrorState, KeyValue, Panel, Segmented, StatusDot, TabBar, TabPanel,
} from '@/components/ui'
import type { TabItem } from '@/components/ui'
import {
  CapabilityBrowser, EntityInventory, JsonBlock, MetricTile, RawView, StateMatrix, StatusBadge,
} from '@/components/SchemaRenderer'
import { ActionPanel } from '@/components/ActionPanel'
import { CommandHistory } from '@/components/CommandHistory'
import { TrendChart } from '@/components/TrendChart'
import { EventFeed } from '@/components/EventFeed'
import { RowSkeleton } from '@/components/Skeleton'
import { api, isNotFound } from '@/lib/api'
import { useLive } from '@/store/ws'
import { useNow } from '@/hooks/useNow'
import { useDeviceDescriptor } from '@/hooks/useDescriptor'
import {
  entityTitle, formatTimestamp, formatValue, metricTiles, observationsOf, primaryObservation,
  propertyLabel,
  qualityTone, summarizeRaw, unitLabel, widgetFor, QUALITY_LABEL,
} from '@/lib/descriptor'
import type { SummaryValue } from '@/lib/descriptor'
import { eventLabel, fmtDateTime, mergeEvents, optionLabel, payloadLabel, timeAgo } from '@/lib/format'
import { isStaleObs } from '@/components/SchemaRenderer'
import { usePageTitle } from '@/hooks/usePageTitle'

type Tab = 'overview' | 'state' | 'controls' | 'events' | 'capabilities' | 'diagnostics'

/**
 * 设备详情（Schema 驱动，六分区职责正交）：
 *   概览（人看）/ 实时状态（运维看）/ 控制（操作）/ 事件（时间线）/ 能力（开发者）/ 诊断（排障）
 *
 * human-first：默认视图只有展示名 + 当前值 + 单位 + 状态 + 新鲜度；
 * 机器 ID / Capability URI / raw JSON 只出现在「能力」Inspector 与「诊断」页（按需展开）。
 * 页面不认识任何设备字段名：一切由 Descriptor + Capability 声明推导，缺席走通用回落。
 */
export default function DeviceDetail() {
  const { edgeId = '', deviceId = '' } = useParams()
  const key = `${decodeURIComponent(edgeId)}/${decodeURIComponent(deviceId)}`
  const now = useNow()
  const nowSec = Math.floor(now.getTime() / 1000)
  const [tab, setTab] = useState<Tab>('overview')
  const [kindFilter, setKindFilter] = useState('')
  const [stateView, setStateView] = useState<'rows' | 'table' | 'trend'>('rows')
  const [rangeMin, setRangeMin] = useState(0)
  const [chartKind, setChartKind] = useState<'area' | 'line'>('area')

  const live = useLive((s) => s.devices[key])
  const liveEvents = useLive((s) => s.events)
  const series = useLive((s) => s.series[key]) ?? {}

  const { data: rest, error: devError, isPending: devIsPending, refetch } = useQuery({
    queryKey: ['device', key], queryFn: () => api.device(edgeId, deviceId),
    refetchInterval: 10000, retry: false,
  })
  const { data: evHist, isLoading: evLoading } = useQuery({
    queryKey: ['device-events', key], queryFn: () => api.events({ device: key, limit: 100 }),
    refetchInterval: 5000,
  })
  const { data: adapters } = useQuery({
    queryKey: ['adapters'], queryFn: api.adapters, staleTime: 5 * 60_000,
  })

  const d = live ?? rest
  usePageTitle(d ? (d.name || d.id) : '设备')

  // 命令白名单唯一事实源是后端 /api/adapters；前端不自建清单
  const adapterCommands = useMemo(() => {
    const a = adapters?.adapters.find((x) => x.name === d?.adapter)
    return a?.commands ?? []
  }, [adapters, d?.adapter])

  const { descriptor, capabilities, source, commands } = useDeviceDescriptor(
    key, decodeURIComponent(edgeId), decodeURIComponent(deviceId),
    { device: d ?? null, adapterCommands },
  )

  /** 控制页的执行器实体：只读现状与命令区并排（观测值与命令输入分离） */
  const actuators = useMemo(
    () => (descriptor?.entities ?? []).filter((e) => e.category === 'actuator'),
    [descriptor],
  )

  const events = useMemo(
    () => mergeEvents(liveEvents.filter((e) => e.device_id === key), evHist?.events ?? []),
    [liveEvents, evHist, key],
  )

  /** 概览 KPI：Descriptor 主观测推导；缺席时回落 raw 标量（通用，不写设备特例） */
  const tiles = useMemo<SummaryValue[]>(() => {
    if (descriptor) return metricTiles(descriptor, capabilities, 4)
    const raw = summarizeRaw(d?.state)
    return [raw.primary, ...raw.chips].filter((x): x is SummaryValue => Boolean(x)).slice(0, 4)
  }, [d, descriptor, capabilities])

  const capRefs = useMemo(() => {
    if (!descriptor) return []
    const set = new Set<string>()
    for (const e of descriptor.entities) for (const c of e.capabilities) if (c) set.add(c)
    return [...set]
  }, [descriptor])

  /** 事件类型 → 展示名（过滤器选项；脏数据统一显示为无效事件） */
  const eventKinds = useMemo(() => {
    const m = new Map<string, string>()
    for (const e of events) if (!m.has(e.type)) m.set(e.type, eventLabel(e.type, capabilities, payloadLabel(e.payload)))
    return [...m.entries()]
  }, [events, capabilities])
  const shownEvents = useMemo(
    () => (kindFilter ? events.filter((e) => e.type === kindFilter) : events),
    [events, kindFilter],
  )

  // 序列键 = raw 顶层字段名（entity.property 点分）：按 Descriptor 声明的 Entity 序排，
  // 让设备自己认为重要的观测（时钟/温度…）排在趋势选择器前面，而不是字母序。
  const seriesKeys = useMemo(() => {
    const keys = Object.keys(series).sort()
    if (!descriptor) return keys
    const order = new Map<string, number>()
    descriptor.entities.forEach((e, i) => order.set(e.entity_id, i))
    const rank = (k: string) => {
      const dot = k.lastIndexOf('.')
      return dot > 0 ? (order.get(k.slice(0, dot)) ?? 999) : 998
    }
    return keys.sort((a, b) => rank(a) - rank(b) || a.localeCompare(b))
  }, [series, descriptor])

  /** 序列键展示名：实体中文名 · 属性中文名；Descriptor 缺席时回落属性词典/humanize */
  const seriesLabel = (k: string): string => {
    const dot = k.lastIndexOf('.')
    if (dot <= 0) return propertyLabel(k)
    const entId = k.slice(0, dot)
    const prop = k.slice(dot + 1)
    const ent = descriptor?.entities.find((e) => e.entity_id === entId || e.unique_key === entId)
    if (!ent) return propertyLabel(prop)
    const cap = ent.observations?.[prop]?.capability ?? ent.capabilities[0]
    return `${entityTitle(ent)} · ${propertyLabel(prop, cap, capabilities)}`
  }

  /** 序列单位：声明里的人话单位（°C / 秒…）；趋势图右上角直接标签与 Tooltip 共用。
   *  键形态与 StateTile 火花线同一回落链：entity.property 点分 / 裸实体 id / 裸属性名 */
  const seriesUnit = (k: string): string | undefined => {
    const dot = k.lastIndexOf('.')
    if (dot > 0) {
      const ent = descriptor?.entities.find((e) => e.entity_id === k.slice(0, dot) || e.unique_key === k.slice(0, dot))
      return unitLabel(ent?.observations?.[k.slice(dot + 1)]?.unit)
    }
    const ents = descriptor?.entities ?? []
    // raw 键是适配器别名（如 uptime_s ↔ entity uptime）：精确匹配优先，前缀别名兜底
    const exact = ents.find((e) => e.entity_id === k || e.unique_key === k)
    const cand = exact ? [exact] : ents.filter((e) => k.startsWith(`${e.entity_id}_`))
    for (const e of cand) {
      const u = unitLabel(primaryObservation(e, capabilities)?.unit)
      if (u) return u
    }
    for (const e of ents) {
      const u = unitLabel(e.observations?.[k]?.unit)
      if (u) return u
    }
    return undefined
  }

  // 详情未到手时三态分明：加载中（骨架）/ 404（未注册空态）/ 其它失败（错误态 + 重试）。
  // 少一个加载态，首帧就会闪「设备未注册」；少一个 404 判定，「这台设备没接入」会被误报成「server 挂了」。
  if (!d) {
    return (
      <>
        <BackLink to="/devices" label="设备" />
        {devIsPending ? (
          <Panel><RowSkeleton rows={5} /></Panel>
        ) : isNotFound(devError) ? (
          <EmptyState icon={<RadioTower size={24} />} title="设备未注册"
            hint={`没有找到 ${key}。设备接入后会自动注册；请检查 edge 配置与连接。`} />
        ) : (
          <ErrorState icon={<RadioTower size={20} />} title="设备信息加载失败"
            hint={`拿不到 ${key} 的详情。这不代表设备不存在，请检查 server 是否可达后重试。`}
            onRetry={() => { void refetch() }} />
        )}
      </>
    )
  }

  const tabs: TabItem<Tab>[] = [
    { value: 'overview', label: '概览', icon: <LayoutDashboard size={13} /> },
    { value: 'state', label: '实时状态', icon: <Radio size={13} /> },
    { value: 'controls', label: '控制', icon: <Command size={13} /> },
    { value: 'events', label: `事件 ${events.length || ''}`.trim(), icon: <History size={13} /> },
    { value: 'capabilities', label: `能力 ${capRefs.length || ''}`.trim(), icon: <Sparkles size={13} /> },
    { value: 'diagnostics', label: '诊断', icon: <Braces size={13} /> },
  ]

  return (
    <>
      <BackLink to="/devices" label="设备" />

      <header className="mb-5 flex flex-wrap items-center gap-3 fade-up">
        <StatusDot online={d.online} />
        <h1 className="min-w-0 max-w-full truncate text-[24px] font-semibold tracking-[-0.01em]" title={d.id}>
          {d.name || deviceId}
        </h1>
        {d.adapter && (
          <span className="min-w-0 truncate font-mono text-[11px] text-ink-3" title={`适配器 ${d.adapter}`}>{d.adapter}</span>
        )}
        {descriptor
          ? <StatusBadge status={descriptor.status} />
          : <Badge tone={d.online ? 'ok' : 'idle'}>{d.online ? '在线' : '离线'}</Badge>}
        {d.port && (
          <span className="flex min-w-0 items-center gap-1 truncate font-mono text-[11px] text-ink-3" title={`串口 ${d.port}`}>
            <Terminal size={11} className="shrink-0" />{d.port}
          </span>
        )}
        <Link to={`/edges/${encodeURIComponent(d.edge_id)}`}
          className="flex min-w-0 max-w-full items-center gap-1 text-[12px] text-ink-3 no-underline transition-colors hover:text-accent"
          title={`边缘节点 ${d.edge_id}`}>
          <RadioTower size={11} className="shrink-0" />
          <span className="min-w-0 truncate">{d.edge_id}</span>
        </Link>
        <span className="num ml-auto truncate font-mono text-[11px] text-ink-3" title={`设备键 ${d.id}`}>
          {d.online ? `更新于 ${timeAgo(d.updated_at)}` : `最后见 ${timeAgo(d.last_seen)}`}
        </span>
      </header>

      <div className="mb-5">
        <TabBar items={tabs} value={tab} onChange={setTab} label="设备详情分区" />
      </div>

      {tab === 'overview' && (
        <TabPanel value={tab}>
          <div className="space-y-5">
            {/* 首屏 KPI：一眼读懂「这台设备现在怎么样」；大字号只给主指标 */}
            <div className="grid grid-cols-2 gap-2.5 lg:grid-cols-4">
              {tiles.map((t, i) => <MetricTile key={`${t.label}-${i}`} v={t} />)}
            </div>
            {tiles.length === 0 && (
              <p className="py-2 text-center text-sm text-ink-3">
                {d.online ? '设备已连接，但还没有可呈现的主值' : '设备离线，暂无可呈现的主值'}
              </p>
            )}
            <div className="grid items-start gap-5 lg:grid-cols-3">
              <Panel className="lg:col-span-2"
                title={<span className="flex items-center gap-1.5"><Activity size={14} />最近活动</span>}
                right={
                  <button type="button" onClick={() => setTab('events')}
                    className="link flex items-center gap-0.5 text-xs">
                    全部事件 <ArrowRight size={12} />
                  </button>
                }>
                {events.length === 0
                  ? <p className="py-6 text-center text-sm text-ink-3">还没有事件上报</p>
                  : <EventFeed events={events} showDevice={false} limit={8} />}
              </Panel>
              <Panel title="设备状况">
                <dl className="space-y-2.5">
                  <KeyValue k="在线" v={d.online ? '是' : '否'} />
                  <KeyValue k="边缘节点" v={d.edge_id || '—'} mono />
                  <KeyValue k="适配器" v={d.adapter || '—'} mono />
                  {d.port && <KeyValue k="串口" v={d.port} mono />}
                  <KeyValue k={d.online ? '最近更新' : '最后见'}
                    v={<span className="num">{fmtDateTime(d.online ? d.updated_at : d.last_seen)}</span>} />
                  <KeyValue k="声明能力" v={descriptor ? `${capRefs.length} 种` : '无 Descriptor'} />
                  <KeyValue k="可下发命令" v={`${commands.actions.length} 条`} />
                </dl>
              </Panel>
            </div>
            {/* 命令成败是「最近发生了什么」的一半（用户自己的操作）：通栏置底，
                避免 8+4 两栏高度互相牵制踢出空洞；概览只看最近 8 条，全部历史在控制页 */}
            <CommandHistory deviceId={key} actions={commands.actions} limit={8}
              footer={
                <button type="button" onClick={() => setTab('controls')}
                  className="link flex items-center gap-0.5 text-xs">
                  到控制页看全部 <ArrowRight size={12} />
                </button>
              } />
          </div>
        </TabPanel>
      )}

      {tab === 'state' && (
        <TabPanel value={tab}>
          <div className="min-w-0">
            {/* 连接态一行说清；绝对新鲜度在页头只说一次 */}
            <div className="mb-3 flex flex-wrap items-center justify-between gap-2 px-0.5">
              <span className="flex items-center gap-2">
                <StatusDot online={d.online} />
                <span className="text-[12px] font-medium text-ink-2">{d.online ? '实时' : '离线'}</span>
                {!d.online && <Badge tone="warn">展示最后一次上报的内容</Badge>}
              </span>
                <Segmented
                  label="状态视图"
                  options={[
                    { value: 'rows', label: '分组' },
                    { value: 'table', label: '表格' },
                    { value: 'trend', label: '趋势' },
                  ]}
                  value={stateView}
                  onChange={setStateView}
                />
              </div>
              {stateView === 'rows' && (descriptor
                ? <StateMatrix descriptor={descriptor} idx={capabilities} nowSec={nowSec} series={series} />
                : <RawView raw={d.state} title="上报字段（通用视图）" />)}
              {stateView === 'table' && (descriptor
                ? <StateTable descriptor={descriptor} idx={capabilities} nowSec={nowSec} />
                : <RawView raw={d.state} title="上报字段（通用视图）" />)}
              {stateView === 'trend' && (
                <div>
                  <div className="mb-3 flex flex-wrap items-center gap-2">
                    <Segmented
                      label="时间范围"
                      options={[
                        { value: '1', label: '近 1 分' },
                        { value: '5', label: '近 5 分' },
                        { value: '0', label: '全会话' },
                      ]}
                      value={String(rangeMin)}
                      onChange={(v) => setRangeMin(Number(v))}
                    />
                    <Segmented
                      label="图表形态"
                      options={[{ value: 'area', label: '面积图' }, { value: 'line', label: '折线图' }]}
                      value={chartKind}
                      onChange={(v) => setChartKind(v)}
                    />
                  </div>
                  {seriesKeys.length === 0 ? (
                    <p className="py-8 text-center text-xs text-ink-3">
                      暂无趋势数据——设备上报数值后会自动开始采样
                    </p>
                  ) : (
                    <div className="grid gap-2.5 md:grid-cols-2 2xl:grid-cols-3">
                      {seriesKeys.map((k) => {
                        const pts = rangeMin > 0
                          ? (series[k] ?? []).filter((pt) => pt.t >= nowSec - rangeMin * 60)
                          : (series[k] ?? [])
                        return (
                          <div key={k} className="card min-w-0 p-3.5">
                            <div className="flex items-baseline justify-between gap-2">
                              <span className="min-w-0 truncate text-[12px] font-medium text-ink-2">
                                {seriesLabel(k)}
                              </span>
                              <span className="flex shrink-0 items-baseline gap-1.5">
                                <span className="num text-[13px] font-semibold tracking-[-0.01em]">
                                  {pts.length ? formatValue(pts[pts.length - 1].v) : '—'}
                                </span>
                                <span className="num text-[12px] text-ink-3">{pts.length} 点</span>
                              </span>
                            </div>
                            <TrendChart points={pts} kind={chartKind} height={104} unit={seriesUnit(k)} />
                          </div>
                        )
                      })}
                    </div>
                  )}
                  <p className="mt-3 px-0.5 text-[12px] leading-relaxed text-ink-3">
                    趋势只记录本页打开期间的数据（最多 240 点）；更早的数值请查事件与命令记录。
                  </p>
                </div>
              )}
          </div>
        </TabPanel>
      )}

      {tab === 'controls' && (
        <TabPanel value={tab}>
          <div className="min-w-0 space-y-5">
            {/* 观测值与命令输入分离：只读现状与命令区并排，避免「看着像已执行」 */}
            <div className="grid items-start gap-5 lg:grid-cols-2">
              <ActionPanel deviceId={key} set={commands} adapterName={d.adapter} />
              {actuators.length > 0 && (
                <Panel title={<span className="flex items-center gap-1.5"><Zap size={14} />当前状态（只读）</span>}>
                  <dl className="space-y-2.5">
                    {actuators.map((e) => {
                    const o = primaryObservation(e, capabilities)
                    const v = !o ? '暂无数据'
                      : widgetFor(o, capabilities) === 'timestamp' ? formatTimestamp(o.value)
                        : `${formatValue(o.value)}${o.unit ? ` ${o.unit}` : ''}`
                    return <KeyValue key={e.unique_key} k={entityTitle(e)} v={v} />
                  })}
                  </dl>
                </Panel>
              )}
            </div>
            {/* 命令历史通栏置底：列表型内容不挤在半宽列里与命令区比高 */}
            <CommandHistory deviceId={key} actions={commands.actions} />
          </div>
        </TabPanel>
      )}

      {tab === 'events' && (
        <TabPanel value={tab}>
          <Panel
            title={<span className="flex items-center gap-1.5"><History size={14} />事件时间线</span>}
            right={<span className="num text-[12px] text-ink-3">{shownEvents.length === events.length ? `${events.length} 条` : `${shownEvents.length} / ${events.length} 条`}</span>}>
            {eventKinds.length > 1 && (
              <div className="mb-3 flex items-center gap-2">
                <label htmlFor="ev-kind" className="shrink-0 text-[12px] text-ink-3">筛选</label>
                <select id="ev-kind" value={kindFilter} onChange={(e) => setKindFilter(e.target.value)}
                  className="input input-sm min-w-0 max-w-[18rem]">
                  <option value="">全部事件</option>
                  {eventKinds.map(([t, l]) => <option key={t} value={t}>{optionLabel(l, 40)}</option>)}
                </select>
              </div>
            )}
            {evLoading && events.length === 0
              ? <RowSkeleton rows={5} />
              : shownEvents.length === 0
                ? <EmptyState icon={<History size={24} />} title="还没有事件"
                  hint="该设备上报事件后会出现在这里；也可以去活动页看全部设备的记录。" />
                : (
                  <>
                    <EventFeed events={shownEvents} showDevice={false} limit={30} dayGrouped />
                    {shownEvents.length > 30 && (
                      <Link to="/activity" className="link mt-3 flex items-center gap-0.5 border-t border-hairline pt-3 text-xs">
                        仅显示最近 30 条（共 {shownEvents.length} 条）· 去活动页查完整历史 <ArrowRight size={12} />
                      </Link>
                    )}
                  </>
                )}
          </Panel>
        </TabPanel>
      )}

      {tab === 'capabilities' && (
        <TabPanel value={tab}>
          {!descriptor ? (
            <EmptyState icon={<Sparkles size={24} />} title="该设备还没有上报能力声明"
              hint="没有声明就不知道它具备哪些能力，因此这里不猜。命令集此时回落到后端适配器白名单（见「控制」分区）。" />
          ) : (
            <Panel
              title={<span className="flex items-center gap-1.5"><Sparkles size={14} />声明的能力</span>}
              right={<span className="num text-[12px] text-ink-3">{capRefs.length} 种 · catalog 收录 {capabilities.docs.length} 份</span>}>
              <CapabilityBrowser descriptor={descriptor} idx={capabilities} />
              <p className="mt-3 border-t border-hairline pt-3 text-[12px] leading-relaxed text-ink-3">
                点击行展开 Inspector（属性/动作/事件/schema）；显示名优先取声明的中文标题。
              </p>
            </Panel>
          )}
        </TabPanel>
      )}

      {tab === 'diagnostics' && (
        <TabPanel value={tab}>
          <div className="space-y-5">
            <Panel
              title={<span className="flex items-center gap-1.5"><Braces size={14} />身份与原始状态</span>}
              right={<span className="num text-[12px] text-ink-3">
                参考时间 {now.toLocaleTimeString('zh-CN', { hour12: false })}
              </span>}>
              <div className="grid gap-5 md:grid-cols-2">
                <dl className="min-w-0 space-y-2.5">
                  <KeyValue k="设备键" v={d.id} mono />
                  <KeyValue k="边缘节点" v={d.edge_id || '—'} mono />
                  <KeyValue k="适配器" v={d.adapter || '—'} mono />
                  <KeyValue k="串口" v={d.port || '—'} mono />
                  <KeyValue k="在线" v={d.online ? '是' : '否'} />
                  <KeyValue k="最后更新" v={<span className="num">{fmtDateTime(d.updated_at)}</span>} />
                  <KeyValue k="最后见" v={<span className="num">{fmtDateTime(d.last_seen)}</span>} />
                  <KeyValue k="Descriptor 来源" v={source} mono />
                  {descriptor?.manufacturer && <KeyValue k="厂商" v={descriptor.manufacturer} />}
                  {descriptor?.model && <KeyValue k="型号" v={descriptor.model} />}
                  {descriptor?.external_id && <KeyValue k="外部 ID" v={descriptor.external_id} mono />}
                </dl>
                <JsonBlock value={d.state ?? {}} label="状态原始 JSON（适配器上报的原始语义）" />
              </div>
              {descriptor && (
                <details className="mt-3">
                  <summary className="cursor-pointer select-none text-[12px] text-ink-3 transition-colors hover:text-ink-2">
                    Descriptor 原始 JSON
                  </summary>
                  <JsonBlock className="mt-1.5" value={descriptor} maxHeight="max-h-56" label="Descriptor 原始 JSON" />
                </details>
              )}
              <p className="mt-3 flex flex-wrap items-center gap-x-1 gap-y-0.5 border-t border-hairline pt-3 text-[12px] text-ink-3">
                <Grid3x3 size={11} className="shrink-0" />
                Entity {descriptor ? descriptor.entities.length : 0} 个 ·
                Capability 引用 {capRefs.length} 种 ·
                catalog 收录 {capabilities.docs.length} 份 ·
                数值序列 {seriesKeys.length} 条
              </p>
            </Panel>
            {descriptor && (
              <Panel title={<span className="flex items-center gap-1.5"><Grid3x3 size={14} />Entity 清单（机器原文）</span>}>
                <EntityInventory descriptor={descriptor} />
              </Panel>
            )}
          </div>
        </TabPanel>
      )}
    </>
  )
}

/** 表格形态：全 Entity 观测的密集行（运维扫读用）；质量/ stale 只标异常 */
function StateTable({ descriptor, idx, nowSec }: {
  descriptor: import('@/lib/types').DeviceDescriptor
  idx: import('@/lib/descriptor').CapabilityIndex
  nowSec: number
}) {
  const rows = descriptor.entities.flatMap((e) =>
    observationsOf(e).map((o) => ({ e, o })))
  if (!rows.length) return <p className="py-6 text-center text-sm text-ink-3">Descriptor 未声明观测</p>
  return (
    <div className="card overflow-x-auto">
      <table className="w-full border-collapse text-left text-xs">
        <thead>
          <tr className="border-b border-hairline text-[12px] text-ink-3">
            <th className="px-3 py-2 font-medium">实体</th>
            <th className="px-3 py-2 font-medium">属性</th>
            <th className="px-3 py-2 text-right font-medium">当前值</th>
            <th className="px-3 py-2 font-medium">质量</th>
            <th className="px-3 py-2 text-right font-medium">接收</th>
          </tr>
        </thead>
        <tbody className="divide-y divide-hairline">
          {rows.map(({ e, o }) => (
            <tr key={`${e.entity_id}.${o.property}`}>
              <td className="max-w-[10rem] truncate px-3 py-1.5">{entityTitle(e)}</td>
              <td className="max-w-[10rem] truncate px-3 py-1.5 text-ink-2">
                {propertyLabel(o.property, o.capability, idx)}
              </td>
              <td className="num px-3 py-1.5 text-right font-medium">
                {widgetFor(o, idx) === 'timestamp' ? formatTimestamp(o.value) : formatValue(o.value)}
                {o.unit && <span className="ml-0.5 font-normal text-ink-3">{unitLabel(o.unit)}</span>}
              </td>
              <td className="px-3 py-1.5">
                {o.quality && o.quality !== 'good'
                  ? <Badge tone={qualityTone(o.quality)}>{QUALITY_LABEL[o.quality]}</Badge>
                  : <span className="text-ink-3">—</span>}
              </td>
              <td className="num whitespace-nowrap px-3 py-1.5 text-right font-mono text-[11px] text-ink-3">
                {o.received_at ? formatTimestamp(o.received_at) : '—'}
                {o.received_at && isStaleObs(o, nowSec) && (
                  <Badge tone="warn" className="ml-1"> stale</Badge>
                )}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

