import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import type { TFunction } from 'i18next'
import { Link, useParams, useSearchParams } from 'react-router'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import {Activity, ArrowRight, Braces, Command, Grid3x3, History, LayoutDashboard, RadioTower, Sparkles, Zap} from 'lucide-react'
import {
  BackLink, Badge, EmptyState, ErrorState, KeyValue, Panel, Segmented, StatusDot, TabBar, TabPanel,
} from '@/components/ui'
import type { TabItem } from '@/components/ui'
import {
  CapabilityBrowser, EntityInventory, JsonBlock, MetricTile, RawView, StateMatrix,
} from '@/components/SchemaRenderer'
import { ActionPanel } from '@/components/ActionPanel'
import { CommandHistory } from '@/components/CommandHistory'
import { TrendChart } from '@/components/TrendChart'
import { EventFeed, eventDisplayLabel } from '@/components/EventFeed'
import { RowSkeleton } from '@/components/Skeleton'
import { api, isNotFound } from '@/lib/api'
import { useLive } from '@/store/ws'
import { useNow } from '@/hooks/useNow'
import { useDeviceDescriptor } from '@/hooks/useDescriptor'
import { useEdges } from '@/hooks/useEdges'
import type { DescriptorSource } from '@/hooks/useDescriptor'
import {
  deviceStatusMeta, entityTitle, formatTimestamp, formatValue, metricTiles, observationsOf, primaryObservation,
  propertyLabel,
  qualityTone, summarizeRaw, unitLabel, widgetFor,
} from '@/lib/descriptor'
import type { SummaryValue } from '@/lib/descriptor'
import { fmtDateTime, mergeEvents, optionLabel, payloadLabel, timeAgo } from '@/lib/format'

const DESCRIPTOR_SOURCE_KEY: Record<DescriptorSource, string> = {
  ws: 'source.ws', inline: 'source.inline', rest: 'source.rest', bulk: 'source.bulk', none: 'source.none', error: 'source.error',
}

const STATE_VALUE_KEY: Record<string, string> = {
  free: 'stateValue.free', busy: 'stateValue.busy', idle: 'stateValue.idle', running: 'stateValue.running',
  stopped: 'stateValue.stopped', on: 'stateValue.on', off: 'stateValue.off', clock: 'stateValue.clock',
}

function displayStateValue(value: unknown, t: TFunction): string {
  const key = typeof value === 'string' ? STATE_VALUE_KEY[value] : undefined
  return key ? t(key) : formatValue(value)
}

function descriptorErrorCopy(status: number | null, t: TFunction): { title: string; hint: string } {
  if (status === 504) return {
    title: t('error.descriptor.timeoutTitle'),
    hint: t('error.descriptor.timeoutHint'),
  }
  if (status === 502 || status === 503) return {
    title: t('error.descriptor.unavailableTitle'),
    hint: t('error.descriptor.unavailableHint'),
  }
  return {
    title: t('error.descriptor.loadTitle'),
    hint: t('error.descriptor.loadHint'),
  }
}
import { isStaleObs } from '@/components/SchemaRenderer'
import { usePageTitle } from '@/hooks/usePageTitle'

type Tab = 'overview' | 'controls' | 'events' | 'advanced'
type AdvancedView = 'state' | 'capabilities' | 'diagnostics'

/**
 * 设备详情（Schema 驱动，四分区职责正交）：
 *   概览（人看）/ 设备操作（执行）/ 记录（时间线）/ 高级（状态、能力与诊断）
 *
 * human-first：默认视图只有展示名 + 当前值 + 单位 + 状态 + 新鲜度；
 * 机器 ID / Capability URI / raw JSON 只出现在「高级」里的能力与诊断区（按需展开）。
 * 页面不认识任何设备字段名：一切由 Descriptor + Capability 声明推导，缺席走通用回落。
 */
export default function DeviceDetail() {
  const { t, i18n } = useTranslation('devices')
  const { edgeId = '', deviceId = '' } = useParams()
  const key = `${decodeURIComponent(edgeId)}/${decodeURIComponent(deviceId)}`
  const now = useNow()
  const nowSec = Math.floor(now.getTime() / 1000)
  const [searchParams, setSearchParams] = useSearchParams()
  const requestedTab = searchParams.get('tab')
  const requestedView = searchParams.get('view')
  const legacyAdvanced: AdvancedView | undefined = requestedTab === 'state' ? 'state'
    : requestedTab === 'capabilities' ? 'capabilities'
      : requestedTab === 'diagnostics' ? 'diagnostics' : undefined
  const isAdvancedView = (value: string | null): value is AdvancedView =>
    value === 'state' || value === 'capabilities' || value === 'diagnostics'
  const defaultTab: Tab = requestedTab == null && !legacyAdvanced ? 'controls' : 'overview'
  const tab: Tab = (['overview', 'controls', 'events', 'advanced'] as const)
    .find((value) => value === requestedTab) ?? (legacyAdvanced ? 'advanced' : defaultTab)
  const advancedView: AdvancedView = isAdvancedView(requestedView) ? requestedView : legacyAdvanced ?? 'diagnostics'
  const setTab = (value: Tab) => setSearchParams((previous) => {
    const next = new URLSearchParams(previous)
    if (value === 'overview') {
      next.set('tab', 'overview'); next.delete('view')
    } else if (value === 'advanced') {
      next.set('tab', 'advanced'); next.set('view', advancedView)
    } else {
      next.set('tab', value); next.delete('view')
    }
    return next
  })
  const setAdvancedView = (value: AdvancedView) => setSearchParams((previous) => {
    const next = new URLSearchParams(previous)
    next.set('tab', 'advanced'); next.set('view', value)
    return next
  })
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
  const edgeList = useEdges()

  const d = live ?? rest
  usePageTitle(d ? (d.name || d.id) : t('page.title'))

  const edgeOnline = edgeList.list.find((edge) => edge.edge_id === d?.edge_id)?.online
  const actionsOnline = Boolean(d?.online) && edgeOnline !== false
  const actionsOfflineReason = actionsOnline ? undefined
    : edgeOnline === false
      ? t('detail.controls.gatewayOffline')
      : t('detail.controls.deviceOffline')

  // 操作白名单唯一事实源是后端 /api/adapters；前端不自建清单
  const adapterCommands = useMemo(() => {
    const a = adapters?.adapters?.find((x) => x.name === d?.adapter)
    return a?.commands ?? []
  }, [adapters, d?.adapter])

  const {
    descriptor, capabilities, source, commands,
    error: descriptorError, errorStatus: descriptorErrorStatus,
  } = useDeviceDescriptor(
    key, decodeURIComponent(edgeId), decodeURIComponent(deviceId),
    { device: d ?? null, adapterCommands },
  )
  const queryClient = useQueryClient()
  const [descriptorRetrying, setDescriptorRetrying] = useState(false)
  const descriptorFailed = !descriptor && (source === 'error' || descriptorError != null)
  const descriptorFailure = descriptorErrorCopy(descriptorErrorStatus, t)
  const retryDescriptor = () => {
    setDescriptorRetrying(true)
    void Promise.all([
      queryClient.refetchQueries({ queryKey: ['descriptors'] }),
      queryClient.refetchQueries({ queryKey: ['descriptor', key] }),
      queryClient.refetchQueries({ queryKey: ['capabilities'] }),
    ]).catch(() => {}).finally(() => setDescriptorRetrying(false))
  }

  /** 控制页的执行器实体：只读现状与操作区并排（观测值与操作输入分离） */
  const actuators = useMemo(
    () => (descriptor?.entities ?? []).filter((e) => e.category === 'actuator'),
    [descriptor],
  )

  /** 高级状态视图也遵守同一套展示词典；未知值原样保留，不猜设备语义。 */
  const displayDescriptor = useMemo(() => {
    if (!descriptor) return descriptor
    return {
      ...descriptor,
      entities: descriptor.entities.map((entity) => ({
        ...entity,
        observations: entity.observations ? Object.fromEntries(
          Object.entries(entity.observations).map(([key, observation]) => [
            key,
            {
              ...observation,
              value: typeof observation.value === 'string' ? displayStateValue(observation.value, t) : observation.value,
            },
          ]),
        ) : entity.observations,
      })),
    }
  }, [descriptor, t])

  const events = useMemo(
    () => mergeEvents(liveEvents.filter((e) => e.device_id === key), evHist?.events ?? []),
    [liveEvents, evHist, key],
  )

  /** 概览 KPI：Descriptor 主观测推导；缺席时回落 raw 标量（通用，不写设备特例） */
  const tiles = useMemo<SummaryValue[]>(() => {
    if (descriptor) return metricTiles(descriptor, capabilities, 4)
      .map((tile) => ({ ...tile, text: displayStateValue(tile.text, t) }))
    const raw = summarizeRaw(d?.state)
    return [raw.primary, ...raw.chips].filter((x): x is SummaryValue => Boolean(x)).slice(0, 4)
      .map((tile) => ({ ...tile, text: displayStateValue(tile.text, t) }))
  }, [d, descriptor, capabilities, t])

  const capRefs = useMemo(() => {
    if (!descriptor) return []
    const set = new Set<string>()
    for (const e of descriptor.entities) for (const c of e.capabilities) if (c) set.add(c)
    return [...set]
  }, [descriptor])

  /** 事件类型 → 展示名（过滤器选项；脏数据统一显示为无效事件） */
  const eventKinds = useMemo(() => {
    const m = new Map<string, string>()
    for (const e of events) if (!m.has(e.type)) m.set(e.type, eventDisplayLabel(e.type, capabilities, payloadLabel(e.payload)))
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
        <BackLink to="/devices" label={t('detail.back')} />
        {devIsPending ? (
          <Panel><RowSkeleton rows={5} /></Panel>
        ) : isNotFound(devError) ? (
          <EmptyState icon={<RadioTower size={24} />} title={t('error.notFoundTitle')}
            hint={t('error.notFoundHint', { key })} />
        ) : (
          <ErrorState icon={<RadioTower size={20} />} title={t('error.loadTitle')}
            hint={t('error.loadHint')}
            onRetry={() => { void refetch() }} />
        )}
      </>
    )
  }

  const tabs: TabItem<Tab>[] = [
    { value: 'controls', label: t('detail.tabs.controls'), icon: <Command size={13} /> },
    { value: 'overview', label: t('detail.tabs.overview'), icon: <LayoutDashboard size={13} /> },
    { value: 'events', label: t('detail.tabs.events'), icon: <History size={13} /> },
    { value: 'advanced', label: t('detail.tabs.advanced'), icon: <Braces size={13} /> },
  ]

  return (
    <>
      <BackLink to="/devices" label={t('detail.back')} />

      <header className="mb-5 flex flex-wrap items-center gap-3 fade-up">
        <StatusDot online={d.online} />
        <h1 className="min-w-0 max-w-full truncate text-[24px] font-semibold tracking-[-0.01em]" title={d.id}>
          {d.name || deviceId}
        </h1>
        <Badge tone={deviceStatusMeta(d.online, descriptor?.status).tone}>{deviceStatusMeta(d.online, descriptor?.status).label}</Badge>
        <span className="num ml-auto truncate font-mono text-[11px] text-ink-3" title={t('detail.header.deviceId', { id: d.id })}>
          {d.online
            ? t('detail.header.updatedAt', { time: timeAgo(d.updated_at) })
            : t('detail.header.lastSeen', { time: timeAgo(d.last_seen) })}
        </span>
      </header>

      <div className="mb-5 [&_button]:min-h-11 sm:[&_button]:min-h-0">
        <TabBar items={tabs} value={tab} onChange={setTab} label={t('detail.tabsAria')} />
      </div>

      {descriptorFailed && (
        <div className="mb-5">
          <ErrorState compact icon={<Sparkles size={20} />}
            title={descriptorFailure.title} hint={descriptorFailure.hint}
            onRetry={retryDescriptor} retrying={descriptorRetrying} />
        </div>
      )}

      {tab === 'advanced' && (
        <div className="mb-5 [&_button]:min-h-11 sm:[&_button]:min-h-0">
          <Segmented
            label={t('detail.advanced.label')}
            options={[
              { value: 'state' as AdvancedView, label: t('detail.advanced.state') },
              { value: 'capabilities' as AdvancedView, label: t('detail.advanced.capabilities') },
              { value: 'diagnostics' as AdvancedView, label: t('detail.advanced.diagnostics') },
            ]}
            value={advancedView}
            onChange={setAdvancedView}
          />
        </div>
      )}

      {tab === 'overview' && (
        <TabPanel value={tab}>
          <div className="space-y-5">
            {/* 首屏 KPI：一眼读懂「这台设备现在怎么样」；大字号只给主指标 */}
            <div className="grid grid-cols-2 gap-2.5 lg:grid-cols-4">
              {tiles.map((t, i) => <MetricTile key={`${t.label}-${i}`} v={t} />)}
            </div>
            {tiles.length === 0 && (
              <p className="py-2 text-center text-sm text-ink-3">
                {d.online ? t('detail.overview.connectedNoPrimary') : t('detail.overview.offlineNoPrimary')}
              </p>
            )}
            {/* 事实横条：KPI 之后立即回答「这台设备健康吗」；KV 多列铺满通栏，不搁浅在窄轨 */}
            <Panel title={t('detail.overview.summary')}>
              <dl className="grid gap-x-10 gap-y-2.5 sm:grid-cols-2 xl:grid-cols-3">
                <KeyValue k={d.online ? t('detail.overview.lastUpdate') : t('detail.overview.lastSeen')}
                  v={<span className="num">{fmtDateTime(d.online ? d.updated_at : d.last_seen)}</span>} />
                <KeyValue k={t('detail.overview.capabilities')} v={descriptorFailed ? t('detail.overview.loadFailed') : descriptor ? t('detail.overview.itemCount', { count: capRefs.length }) : t('detail.overview.notSynced')} />
                <KeyValue k={t('detail.overview.actions')} v={descriptorFailed ? t('detail.overview.loadFailed') : t('detail.overview.itemCount', { count: commands.actions.length })} />
              </dl>
            </Panel>
            {/* 双 ledger 互为 peer：等高互不牵制，空洞无处产生；概览只看最近 8 条，全部历史在各自页 */}
            <div className="grid items-start gap-5 lg:grid-cols-2">
              <Panel
                title={<span className="flex items-center gap-1.5"><Activity size={14} />{t('detail.overview.recentEvents')}</span>}
                right={
                  <button type="button" onClick={() => setTab('events')}
                    className="link flex items-center gap-0.5 text-xs">
                    {t('detail.overview.viewRecord')} <ArrowRight size={12} />
                  </button>
                }>
                {events.length === 0
                  ? <p className="py-6 text-center text-sm text-ink-3">{t('detail.overview.noEvents')}</p>
                  : <EventFeed events={events} showDevice={false} limit={8} />}
              </Panel>
              <CommandHistory deviceId={key} targetLabel={d.name || deviceId} actions={commands.actions} limit={8} online={actionsOnline} />
            </div>
          </div>
        </TabPanel>
      )}

      {tab === 'advanced' && advancedView === 'state' && (
        <TabPanel value={tab}>
          <div className="min-w-0">
            {/* 连接态一行说清；绝对新鲜度在页头只说一次 */}
            <div className="mb-3 flex flex-wrap items-center justify-between gap-2 px-0.5">
              <span className="flex items-center gap-2">
                <StatusDot online={d.online} />
                <span className="text-[12px] font-medium text-ink-2">{d.online ? t('detail.state.live') : t('detail.state.offline')}</span>
                {!d.online && <Badge tone="warn">{t('detail.state.showingLastUpdate')}</Badge>}
              </span>
                <Segmented
                  label={t('detail.state.viewLabel')}
                  options={[
                    { value: 'rows', label: t('detail.state.rows') },
                    { value: 'table', label: t('detail.state.table') },
                    { value: 'trend', label: t('detail.state.trend') },
                  ]}
                  value={stateView}
                  onChange={setStateView}
                />
              </div>
              {stateView === 'rows' && (displayDescriptor
                ? <StateMatrix descriptor={displayDescriptor} idx={capabilities} nowSec={nowSec} series={series} />
                : <RawView raw={d.state} title={t('raw.title')} />)}
              {stateView === 'table' && (displayDescriptor
                ? <StateTable descriptor={displayDescriptor} idx={capabilities} nowSec={nowSec} />
                : <RawView raw={d.state} title={t('raw.genericTitle')} />)}
              {stateView === 'trend' && (
                <div>
                  <div className="mb-3 flex flex-wrap items-center gap-2">
                    <Segmented
                      label={t('detail.state.rangeLabel')}
                      options={[
                        { value: '1', label: t('detail.state.lastMinute') },
                        { value: '5', label: t('detail.state.lastFiveMinutes') },
                        { value: '0', label: t('detail.state.session') },
                      ]}
                      value={String(rangeMin)}
                      onChange={(v) => setRangeMin(Number(v))}
                    />
                    <Segmented
                      label={t('detail.state.chartLabel')}
                      options={[{ value: 'area', label: t('detail.state.area') }, { value: 'line', label: t('detail.state.line') }]}
                      value={chartKind}
                      onChange={(v) => setChartKind(v)}
                    />
                  </div>
                  {seriesKeys.length === 0 ? (
                    <p className="py-8 text-center text-xs text-ink-3">
                      {t('detail.state.noTrend')}
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
                                <span className="num text-[12px] text-ink-3">{t('detail.state.points', { count: pts.length })}</span>
                              </span>
                            </div>
                            <TrendChart points={pts} kind={chartKind} height={104} unit={seriesUnit(k)} />
                          </div>
                        )
                      })}
                    </div>
                  )}
                  <p className="mt-3 px-0.5 text-[12px] leading-relaxed text-ink-3">
                    {t('detail.state.trendNote')}
                  </p>
                </div>
              )}
          </div>
        </TabPanel>
      )}

      {tab === 'controls' && (
        <TabPanel value={tab}>
          <div className="min-w-0 space-y-5">
            {/* 观测值与操作输入分离：只读现状与操作区并排，避免「看着像已执行」 */}
            <div className="grid items-start gap-5 lg:grid-cols-3">
              {descriptorFailed
                ? <Panel title={t('detail.controls.title')} className="lg:col-span-2">
                  <p className="py-4 text-center text-sm text-ink-3">{t('detail.controls.descriptorFailed')}</p>
                </Panel>
                : <ActionPanel deviceId={key} targetLabel={d.name || deviceId} set={commands} online={actionsOnline} offlineReason={actionsOfflineReason} className="lg:col-span-2" />}
              {actuators.length > 0 && (
                <Panel title={<span className="flex items-center gap-1.5"><Zap size={14} />{t('detail.controls.actuatorState')}</span>}>
                  <dl className="space-y-2.5">
                    {actuators.map((e) => {
                    const o = primaryObservation(e, capabilities)
                    const v = !o ? t('detail.controls.noData')
                      : widgetFor(o, capabilities) === 'timestamp' ? formatTimestamp(o.value)
                        : `${displayStateValue(o.value, t)}${o.unit ? ` ${unitLabel(o.unit) ?? o.unit}` : ''}`
                    return <KeyValue key={e.unique_key} k={entityTitle(e)} v={v} />
                  })}
                  </dl>
                </Panel>
              )}
            </div>
          </div>
        </TabPanel>
      )}

      {tab === 'events' && (
        <TabPanel value={tab}>
          <div className="space-y-5">
            <CommandHistory deviceId={key} targetLabel={d.name || deviceId} actions={commands.actions} online={actionsOnline} />
            <details className="card overflow-hidden">
              <summary className="flex cursor-pointer select-none items-center justify-between gap-3 px-4 py-3 text-[13px] font-semibold">
                <span className="flex items-center gap-1.5"><Activity size={14} />{t('detail.events.title')}</span>
                <span className="num text-[12px] font-normal text-ink-3">{t('detail.events.count', { count: events.length })}</span>
              </summary>
              <div className="border-t border-hairline p-4">
                {eventKinds.length > 1 && (
                  <div className="mb-3 flex items-center gap-2">
                    <label htmlFor="ev-kind" className="shrink-0 text-[12px] text-ink-3">{t('detail.events.filter')}</label>
                    <select id="ev-kind" value={kindFilter} onChange={(e) => setKindFilter(e.target.value)}
                      className="input input-sm min-h-11 min-w-0 max-w-[18rem] sm:min-h-0">
                      <option value="">{t('detail.events.all')}</option>
                      {eventKinds.map(([t, l]) => <option key={t} value={t}>{optionLabel(l, 40)}</option>)}
                    </select>
                  </div>
                )}
                {evLoading && events.length === 0
                  ? <RowSkeleton rows={5} />
                  : shownEvents.length === 0
                    ? <p className="py-4 text-center text-sm text-ink-3">{t('detail.events.empty')}</p>
                    : (
                      <>
                        <EventFeed events={shownEvents} showDevice={false} limit={30} dayGrouped />
                        {shownEvents.length > 30 && (
                          <Link to="/activity" className="link mt-3 flex min-h-11 items-center gap-0.5 border-t border-hairline pt-3 text-xs">
                            {t('detail.events.limited', { count: shownEvents.length })} <ArrowRight size={12} />
                          </Link>
                        )}
                      </>
                    )}
              </div>
            </details>
          </div>
        </TabPanel>
      )}

      {tab === 'advanced' && advancedView === 'capabilities' && (
        <TabPanel value={tab}>
          {descriptorFailed ? (
            <Panel title={<span className="flex items-center gap-1.5"><Sparkles size={14} />{t('detail.capabilities.title')}</span>}>
              <p className="py-4 text-center text-sm text-ink-3">{t('detail.capabilities.loadFailed')}</p>
            </Panel>
          ) : !descriptor ? (
            <EmptyState icon={<Sparkles size={24} />} title={t('detail.capabilities.emptyTitle')}
              hint={t('detail.capabilities.emptyHint')} />
          ) : (
            <Panel
              title={<span className="flex items-center gap-1.5"><Sparkles size={14} />{t('detail.capabilities.title')}</span>}
              right={<span className="num text-[12px] text-ink-3">{t('detail.capabilities.synced', { count: capRefs.length, docs: capabilities.docs.length })}</span>}>
              <CapabilityBrowser descriptor={descriptor} idx={capabilities} />
              <p className="mt-3 border-t border-hairline pt-3 text-[12px] leading-relaxed text-ink-3">
                {t('detail.capabilities.note')}<span className="sm:hidden">{t('detail.capabilities.mobileNote')}</span>
              </p>
            </Panel>
          )}
        </TabPanel>
      )}

      {tab === 'advanced' && advancedView === 'diagnostics' && (
        <TabPanel value={tab}>
          <div className="space-y-5">
            <Panel
              title={<span className="flex items-center gap-1.5"><Braces size={14} />{t('detail.diagnostics.title')}</span>}
              right={<span className="num text-[12px] text-ink-3">
                {t('detail.diagnostics.referenceTime', { time: now.toLocaleTimeString(i18n.language, { hour12: false }) })}
              </span>}>
              <div className="grid gap-5 md:grid-cols-2">
                <dl className="min-w-0 space-y-2.5">
                  <KeyValue k={t('detail.diagnostics.deviceId')} v={d.id} mono />
                  <KeyValue k={t('detail.diagnostics.gateway')} v={d.edge_id || '—'} mono />
                  <KeyValue k={t('detail.diagnostics.deviceType')} v={d.adapter || '—'} mono />
                  <KeyValue k={t('detail.diagnostics.port')} v={d.port || '—'} mono />
                  <KeyValue k={t('detail.diagnostics.online')} v={d.online ? t('detail.diagnostics.yes') : t('detail.diagnostics.no')} />
                  <KeyValue k={t('detail.diagnostics.lastUpdate')} v={<span className="num">{fmtDateTime(d.updated_at)}</span>} />
                  <KeyValue k={t('detail.diagnostics.lastSeen')} v={<span className="num">{fmtDateTime(d.last_seen)}</span>} />
                  <KeyValue k={t('detail.diagnostics.descriptorSource')} v={
                    <span title={t('detail.diagnostics.sourceTitle', { source })}>
                      {descriptorFailed ? t('detail.overview.loadFailed') : t(DESCRIPTOR_SOURCE_KEY[source])}
                    </span>
                  } />
                  {descriptor?.manufacturer && <KeyValue k={t('detail.diagnostics.manufacturer')} v={descriptor.manufacturer} />}
                  {descriptor?.model && <KeyValue k={t('detail.diagnostics.model')} v={descriptor.model} />}
                  {descriptor?.external_id && <KeyValue k={t('detail.diagnostics.externalId')} v={descriptor.external_id} mono />}
                </dl>
                <JsonBlock value={d.state ?? {}} label={t('detail.diagnostics.rawState')} />
              </div>
              {descriptor && (
                <details className="mt-3">
                  <summary className="flex min-h-11 cursor-pointer select-none items-center text-[12px] text-ink-3 transition-colors hover:text-ink-2">
                    {t('detail.diagnostics.descriptorRaw')}
                  </summary>
                  <JsonBlock className="mt-1.5" value={descriptor} maxHeight="max-h-56" label={t('detail.diagnostics.descriptorRaw')} />
                </details>
              )}
              <p className="mt-3 flex flex-wrap items-center gap-x-1 gap-y-0.5 border-t border-hairline pt-3 text-[12px] text-ink-3">
                <Grid3x3 size={11} className="shrink-0" />
                {t('detail.diagnostics.summary', {
                  entities: descriptor ? descriptor.entities.length : 0,
                  capabilities: capRefs.length,
                  docs: capabilities.docs.length,
                  series: seriesKeys.length,
                })}
              </p>
            </Panel>
            {descriptor && (
              <Panel title={<span className="flex items-center gap-1.5"><Grid3x3 size={14} />{t('detail.diagnostics.inventoryTitle')}</span>}>
                <EntityInventory descriptor={descriptor} />
                <p className="mt-2 text-[11px] text-ink-3 sm:hidden">{t('detail.diagnostics.mobileNote')}</p>
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
  const { t } = useTranslation('devices')
  const rows = descriptor.entities.flatMap((e) =>
    observationsOf(e).map((o) => ({ e, o })))
  if (!rows.length) return <p className="py-6 text-center text-sm text-ink-3">{t('detail.stateTable.empty')}</p>
  return (
    <div className="card overflow-x-auto" tabIndex={0} role="region" aria-label={t('detail.stateTable.aria')}>
      <table className="w-full min-w-[44rem] border-collapse text-left text-xs">
        <thead>
          <tr className="border-b border-hairline text-[12px] text-ink-3">
            <th className="px-3 py-2 font-medium">{t('detail.stateTable.entity')}</th>
            <th className="px-3 py-2 font-medium">{t('detail.stateTable.property')}</th>
            <th className="px-3 py-2 text-right font-medium">{t('detail.stateTable.value')}</th>
            <th className="px-3 py-2 font-medium">{t('detail.stateTable.quality')}</th>
            <th className="px-3 py-2 text-right font-medium">{t('detail.stateTable.received')}</th>
          </tr>
        </thead>
        <tbody className="divide-y divide-hairline">
          {rows.map(({ e, o }) => (
            <tr key={`${e.entity_id}.${o.property}`}>
              <td className="whitespace-nowrap px-3 py-1.5">{entityTitle(e)}</td>
              <td className="whitespace-nowrap px-3 py-1.5 text-ink-2">
                {propertyLabel(o.property, o.capability, idx)}
              </td>
              <td className="num px-3 py-1.5 text-right font-medium">
                {widgetFor(o, idx) === 'timestamp' ? formatTimestamp(o.value) : displayStateValue(o.value, t)}
                {o.unit && <span className="ml-0.5 font-normal text-ink-3">{unitLabel(o.unit)}</span>}
              </td>
              <td className="px-3 py-1.5">
                {o.quality && o.quality !== 'good'
                  ? <Badge tone={qualityTone(o.quality)}>{t(`quality.${o.quality}`)}</Badge>
                  : <span className="text-ink-3">—</span>}
              </td>
              <td className="num whitespace-nowrap px-3 py-1.5 text-right font-mono text-[11px] text-ink-3">
                {o.received_at ? formatTimestamp(o.received_at) : '—'}
                {o.received_at && isStaleObs(o, nowSec) && (
                  <Badge tone="warn" className="ml-1">{t('detail.stateTable.stale')}</Badge>
                )}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
