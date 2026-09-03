import { useMemo, useState } from 'react'
import { Link, useParams } from 'react-router'
import { useQuery } from '@tanstack/react-query'
import {
  Activity, ArrowLeft, Braces, Command, Gauge, Grid3x3, History, LayoutDashboard, Radio,
  RadioTower, Sparkles, Terminal,
} from 'lucide-react'
import {
  Badge, EmptyState, ErrorState, KeyValue, Panel, Segmented, StatusDot, TabBar, TabPanel,
} from '@/components/ui'
import type { TabItem } from '@/components/ui'
import {
  DescriptorView, EntityPanel, JsonBlock, RawView, StatusBadge, SummaryChips, SummaryGroups,
} from '@/components/SchemaRenderer'
import { ActionPanel } from '@/components/ActionPanel'
import { TrendChart } from '@/components/TrendChart'
import { EventFeed } from '@/components/EventFeed'
import { CommandHistory } from '@/components/CommandHistory'
import { RowSkeleton } from '@/components/Skeleton'
import { api } from '@/lib/api'
import { useLive } from '@/store/ws'
import { useNow } from '@/hooks/useNow'
import { useDeviceDescriptor } from '@/hooks/useDescriptor'
import { capabilityLabel, humanize, summarizeDescriptor, summarizeRaw } from '@/lib/descriptor'
import { fmtDateTime, mergeEvents, timeAgo } from '@/lib/format'
import { deviceLabel } from '@/lib/edges'

type Tab = 'overview' | 'state' | 'controls' | 'events' | 'capabilities' | 'diagnostics'

/**
 * 设备详情（Schema 驱动，分区呈现）：
 *   概览 / 实时状态 / 控制 / 事件 / 能力 / 诊断
 *
 * 页面不认识任何设备字段名：主值、胶囊、分组、命令集、能力清单全部由 Descriptor +
 * Capability 声明推导；Descriptor 缺席时走通用回落（RawView + 适配器白名单命令），
 * 并明确标注「通用视图」，不假装自己懂这台设备。
 */
export default function DeviceDetail() {
  const { edgeId = '', deviceId = '' } = useParams()
  const key = `${decodeURIComponent(edgeId)}/${decodeURIComponent(deviceId)}`
  const now = useNow()
  const [tab, setTab] = useState<Tab>('overview')
  const [seriesKey, setSeriesKey] = useState('')

  const live = useLive((s) => s.devices[key])
  const liveEvents = useLive((s) => s.events)
  const series = useLive((s) => s.series[key]) ?? {}

  const { data: rest, error: devError, refetch } = useQuery({
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

  // 命令白名单唯一事实源是后端 /api/adapters；前端不自建清单
  const adapterCommands = useMemo(() => {
    const a = adapters?.adapters.find((x) => x.name === d?.adapter)
    return a?.commands ?? []
  }, [adapters, d?.adapter])

  const { descriptor, capabilities, source, commands } = useDeviceDescriptor(
    key, decodeURIComponent(edgeId), decodeURIComponent(deviceId),
    { device: d ?? null, adapterCommands },
  )

  const events = useMemo(
    () => mergeEvents(liveEvents.filter((e) => e.device_id === key), evHist?.events ?? []),
    [liveEvents, evHist, key],
  )

  const summary = useMemo(() => {
    if (!d) return null
    return descriptor ? summarizeDescriptor(descriptor, capabilities) : summarizeRaw(d.state)
  }, [d, descriptor, capabilities])

  const capRefs = useMemo(() => {
    if (!descriptor) return []
    const set = new Set<string>()
    for (const e of descriptor.entities) for (const c of e.capabilities) if (c) set.add(c)
    return [...set]
  }, [descriptor])

  const seriesKeys = Object.keys(series).sort()
  const activeSeries = seriesKey && series[seriesKey] ? seriesKey : (seriesKeys[0] ?? '')
  const activePoints = activeSeries ? (series[activeSeries] ?? []) : []

  if (!d) {
    return (
      <>
        <BackLink />
        {devError ? (
          // 接口失败不等于设备不存在：这两种结论对用户完全不同
          <ErrorState icon={<RadioTower size={20} />} title="设备信息加载失败"
            hint={`拿不到 ${key} 的详情（GET /api/devices/...）。这不代表设备不存在，请检查 server 是否可达后重试。`}
            onRetry={() => { void refetch() }} />
        ) : (
          <EmptyState icon={<RadioTower size={24} />} title="设备未注册"
            hint={`没有找到 ${key}。设备接入后会自动注册；请检查 edge 配置与连接。`} />
        )}
      </>
    )
  }

  const tabs: TabItem<Tab>[] = [
    { value: 'overview', label: '概览', icon: <LayoutDashboard size={13} /> },
    { value: 'state', label: '实时状态', icon: <Radio size={13} /> },
    { value: 'controls', label: '控制', icon: <Command size={13} />, count: commands.actions.length },
    { value: 'events', label: '事件', icon: <History size={13} />, count: events.length },
    { value: 'capabilities', label: '能力', icon: <Sparkles size={13} />, count: capRefs.length },
    { value: 'diagnostics', label: '诊断', icon: <Braces size={13} /> },
  ]

  return (
    <>
      <BackLink />

      <header className="mb-5 flex flex-wrap items-center gap-3 fade-up">
        <StatusDot online={d.online} />
        <h1 className="min-w-0 max-w-full truncate text-[26px] font-bold tracking-tight" title={d.id}>
          {d.name || deviceId}
        </h1>
        <Badge tone="accent" className="max-w-full truncate">{d.adapter || '未知适配器'}</Badge>
        {descriptor
          ? <StatusBadge status={descriptor.status} />
          : <Badge tone={d.online ? 'ok' : 'idle'}>{d.online ? '在线' : '离线'}</Badge>}
        {d.port && <Badge tone="idle" className="max-w-full truncate"><Terminal size={11} className="shrink-0" />{d.port}</Badge>}
        <Link to={`/edges/${encodeURIComponent(d.edge_id)}`}
          className="badge max-w-full bg-ink-3/10 text-ink-2 no-underline transition-colors hover:bg-accent/10 hover:text-accent"
          title={`边缘节点 ${d.edge_id}`}>
          <RadioTower size={11} className="shrink-0" />
          <span className="min-w-0 truncate">{d.edge_id}</span>
        </Link>
        <Badge tone={source === 'none' ? 'idle' : 'accent'}>
          <Sparkles size={11} />{source === 'none' ? '通用视图' : 'Schema 驱动'}
        </Badge>
        <span className="num ml-auto text-xs text-ink-3" title={`设备键 ${d.id}`}>
          {d.online ? `更新于 ${timeAgo(d.updated_at)}` : `最后见 ${timeAgo(d.last_seen)}`}
        </span>
      </header>

      <div className="mb-5">
        <TabBar items={tabs} value={tab} onChange={setTab} label="设备详情分区" />
      </div>

      {tab === 'overview' && (
        <TabPanel value={tab}>
          <div className="grid items-start gap-5 lg:grid-cols-3">
            <Panel className="lg:col-span-2" title="当前状态">
              {!d.online && (
                <div className="banner mb-4 rounded-xl" role="status">
                  设备离线：下面是最后一次上报的内容，不代表当前状态。
                </div>
              )}
              {summary?.primary ? (
                <div className="flex flex-wrap items-end gap-x-4 gap-y-2">
                  <div className="num min-w-0 text-[44px] font-semibold leading-none tracking-tight break-words">
                    {summary.primary.text}
                    {summary.primary.unit && (
                      <span className="ml-1 text-lg font-normal text-ink-3">{summary.primary.unit}</span>
                    )}
                  </div>
                  <p className="pb-1 text-sm text-ink-2">{summary.primary.label || summary.primary.title}</p>
                </div>
              ) : (
                <p className="py-6 text-center text-sm text-ink-3">
                  {d.online ? '设备已连接，但还没有可呈现的主值' : '设备离线，暂无可呈现的主值'}
                </p>
              )}
              {summary && summary.chips.length > 0 && (
                <div className="mt-5 border-t border-hairline pt-4">
                  <SummaryChips chips={summary.chips} />
                </div>
              )}
              {summary && summary.groups.length > 0 && (
                <div className="mt-5 border-t border-hairline pt-4">
                  <SummaryGroups groups={summary.groups} />
                </div>
              )}
            </Panel>

            <Panel title="关键事实">
              <dl className="space-y-2.5">
                <KeyValue k="名称" v={deviceLabel(d)} />
                <KeyValue k="在线" v={d.online ? '是' : '否'} />
                <KeyValue k="边缘节点" v={d.edge_id || '—'} mono />
                <KeyValue k="适配器" v={d.adapter || '—'} mono />
                <KeyValue k={d.online ? '最近更新' : '最后见'}
                  v={<span className="num">{fmtDateTime(d.online ? d.updated_at : d.last_seen)}</span>} />
                <KeyValue k="上报字段" v={`${Object.keys(d.state ?? {}).length} 个`} />
                <KeyValue k="声明能力" v={descriptor ? `${capRefs.length} 种` : '无 Descriptor'} />
                <KeyValue k="可下发命令" v={`${commands.actions.length} 条`} />
              </dl>
            </Panel>
          </div>
        </TabPanel>
      )}

      {tab === 'state' && (
        <TabPanel value={tab}>
          <div className="grid items-start gap-5 lg:grid-cols-3">
            <div className="min-w-0 lg:col-span-2">
              {descriptor
                ? <DescriptorView descriptor={descriptor} idx={capabilities} />
                : <RawView raw={d.state} title="上报字段（通用视图）" />}
            </div>
            <Panel
              title={<span className="flex items-center gap-1.5"><Gauge size={14} />会话数值趋势</span>}
              right={<span className="num text-[11px] text-ink-3">{activePoints.length} 点</span>}
            >
              {seriesKeys.length > 1 && (
                <div className="mb-3 overflow-x-auto pb-1">
                  <Segmented
                    label="数值序列"
                    options={seriesKeys.slice(0, 5).map((k) => ({ value: k, label: humanize(k) }))}
                    value={activeSeries}
                    onChange={setSeriesKey}
                  />
                </div>
              )}
              {seriesKeys.length === 0
                ? <p className="py-8 text-center text-xs text-ink-3">本次会话还没有数值观测</p>
                : <TrendChart points={activePoints} />}
              <p className="mt-3 border-t border-hairline pt-3 text-[11px] leading-relaxed text-ink-3">
                趋势是本次浏览器会话内采样得到的（最多 {240} 点），刷新页面即重新开始；历史数值请查事件与命令记录。
              </p>
            </Panel>
          </div>
        </TabPanel>
      )}

      {tab === 'controls' && (
        <TabPanel value={tab}>
          <div className="grid items-start gap-5 lg:grid-cols-2">
            <ActionPanel deviceId={key} set={commands} adapterName={d.adapter || '—'} />
            <CommandHistory deviceId={key} actions={commands.actions} />
          </div>
        </TabPanel>
      )}

      {tab === 'events' && (
        <TabPanel value={tab}>
          <Panel title={<span className="flex items-center gap-1.5"><Activity size={14} />事件时间线</span>}
            right={<span className="num text-[11px] text-ink-3">{events.length} 条</span>}>
            {evLoading && events.length === 0
              ? <RowSkeleton rows={5} />
              : events.length === 0
                ? <EmptyState icon={<History size={24} />} title="还没有事件"
                  hint="该设备上报事件后会出现在这里；也可以去活动页看全部设备的记录。" />
                : <EventFeed events={events} showDevice={false} limit={100} fullTime />}
          </Panel>
        </TabPanel>
      )}

      {tab === 'capabilities' && (
        <TabPanel value={tab}>
          {!descriptor ? (
            <EmptyState icon={<Sparkles size={24} />} title="该设备还没有提供 Descriptor"
              hint="没有 Descriptor 就不知道它声明了哪些 Capability，因此这里不猜。命令集此时回落到后端适配器白名单（见「控制」分区）。" />
          ) : (
            <div className="space-y-5">
              <Panel title="声明的 Capability"
                right={<span className="num text-[11px] text-ink-3">{capRefs.length} 种</span>}>
                {capRefs.length === 0 ? (
                  <p className="py-6 text-center text-sm text-ink-3">Descriptor 里没有声明 Capability</p>
                ) : (
                  <ul className="m-0 flex list-none flex-wrap gap-1.5 p-0">
                    {capRefs.map((ref) => {
                      const known = capabilities.byId.has(ref) || capabilities.byName.has(ref)
                      return (
                        <li key={ref} className="min-w-0 max-w-full">
                          <span className="badge max-w-full bg-ink-3/10 text-ink-2"
                            title={known ? ref : `${ref}（catalog 未收录，按未知能力回落）`}>
                            <span className="min-w-0 truncate">{capabilityLabel(ref, capabilities)}</span>
                          </span>
                        </li>
                      )
                    })}
                  </ul>
                )}
                <p className="mt-3 border-t border-hairline pt-3 text-[11px] leading-relaxed text-ink-3">
                  Capability ID 永不本地化；显示名取自 catalog 的 title，缺席时回落可读化的 ID 尾段。
                </p>
              </Panel>

              {descriptor.entities.length === 0 ? (
                <Panel><p className="py-6 text-center text-sm text-ink-3">Descriptor 没有声明 Entity</p></Panel>
              ) : (
                descriptor.entities.map((e) => (
                  <EntityPanel key={e.entity_id} entity={e} idx={capabilities} />
                ))
              )}
            </div>
          )}
        </TabPanel>
      )}

      {tab === 'diagnostics' && (
        <TabPanel value={tab}>
          <Panel
            title={<span className="flex items-center gap-1.5"><Braces size={14} />身份与原始状态（取证用）</span>}
            right={<span className="num text-[11px] text-ink-3">
              参考时间 {now.toLocaleTimeString('zh-CN', { hour12: false })}
            </span>}
          >
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
            <p className="mt-3 flex flex-wrap items-center gap-x-1 gap-y-0.5 border-t border-hairline pt-3 text-[11px] text-ink-3">
              <Grid3x3 size={11} className="shrink-0" />
              Entity {descriptor ? descriptor.entities.length : 0} 个 ·
              Capability 引用 {capRefs.length} 种 ·
              catalog 收录 {capabilities.docs.length} 份 ·
              数值序列 {seriesKeys.length} 条
            </p>
          </Panel>
        </TabPanel>
      )}
    </>
  )
}

function BackLink() {
  return (
    <Link to="/devices"
      className="mb-5 inline-flex items-center gap-1 text-sm text-ink-2 transition-colors hover:text-accent fade-up">
      <ArrowLeft size={15} /> 设备
    </Link>
  )
}