import { useMemo, useState } from 'react'
import { Link, useParams } from 'react-router'
import { useQuery } from '@tanstack/react-query'
import {
  ArrowLeft, Braces, Gauge, Grid3x3, History, RadioTower, Sparkles, Terminal,
} from 'lucide-react'
import { Badge, EmptyState, KeyValue, Panel, Segmented, StatusDot } from '@/components/ui'
import { DescriptorView, RawView, StatusBadge } from '@/components/SchemaRenderer'
import { ActionPanel } from '@/components/ActionPanel'
import { TrendChart } from '@/components/TrendChart'
import { EventFeed } from '@/components/EventFeed'
import { CommandHistory } from '@/components/CommandHistory'
import { RowSkeleton } from '@/components/Skeleton'
import { api } from '@/lib/api'
import { useLive } from '@/store/ws'
import { useNow } from '@/hooks/useNow'
import { useDeviceDescriptor } from '@/hooks/useDescriptor'
import { humanize } from '@/lib/descriptor'
import { mergeEvents, timeAgo } from '@/lib/format'
import { cn } from '@/lib/cn'

/**
 * 设备详情（Schema 驱动）：
 *   - 有 Descriptor → DescriptorView（entities[].observations + Capability presentation 渲染）
 *     + ActionPanel（Capability actions / Descriptor commands 驱动的命令集）
 *   - 无 Descriptor → RawView 通用回落（按上报字段类型渲染）+ 适配器白名单命令
 * 页面不认识任何设备字段名；诊断面（原始 JSON、会话数值趋势）保持通用。
 */
export default function DeviceDetail() {
  const { edgeId = '', deviceId = '' } = useParams()
  const key = `${decodeURIComponent(edgeId)}/${decodeURIComponent(deviceId)}`
  const now = useNow()
  const [seriesKey, setSeriesKey] = useState('')

  const live = useLive((s) => s.devices[key])
  const liveEvents = useLive((s) => s.events)
  const series = useLive((s) => s.series[key]) ?? {}

  // 详情兜底（WS 未覆盖/刚重启）、事件历史与适配器命令白名单
  const { data: rest } = useQuery({
    queryKey: ['device', key], queryFn: () => api.device(edgeId, deviceId), refetchInterval: 10000,
  })
  const { data: evHist, isLoading: evLoading } = useQuery({
    queryKey: ['device-events', key], queryFn: () => api.events({ device: key, limit: 100 }),
    refetchInterval: 5000,
  })
  const { data: adapters } = useQuery({
    queryKey: ['adapters'], queryFn: api.adapters, staleTime: 5 * 60_000,
  })

  const d = live ?? rest

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

  const seriesKeys = Object.keys(series).sort()
  const activeSeries = seriesKey && series[seriesKey] ? seriesKey : (seriesKeys[0] ?? '')
  const activePoints = activeSeries ? (series[activeSeries] ?? []) : []

  if (!d) {
    return (
      <>
        <Link to="/devices" className="link mb-5 inline-flex items-center gap-1 text-sm">
          <ArrowLeft size={15} /> 设备
        </Link>
        <EmptyState icon={<RadioTower size={24} />} title="设备未注册"
          hint={`没有找到 ${key}。设备接入后会自动注册；请检查 edge 配置与连接。`} />
      </>
    )
  }

  return (
    <>
      <Link to="/devices"
        className="mb-5 inline-flex items-center gap-1 text-sm text-ink-2 transition-colors hover:text-accent fade-up">
        <ArrowLeft size={15} /> 设备
      </Link>

      <header className="mb-7 flex flex-wrap items-center gap-3 fade-up">
        <StatusDot online={d.online} />
        <h1 className="min-w-0 truncate text-[26px] font-bold tracking-tight">{d.name || deviceId}</h1>
        <Badge tone="accent">{d.adapter || '未知适配器'}</Badge>
        {descriptor
          ? <StatusBadge status={descriptor.status} />
          : <Badge tone={d.online ? 'ok' : 'idle'}>{d.online ? '在线' : '离线'}</Badge>}
        {d.port && <Badge tone="idle"><Terminal size={11} />{d.port}</Badge>}
        <Badge tone="idle"><RadioTower size={11} />{d.edge_id}</Badge>
        <Badge tone={source === 'none' ? 'idle' : 'accent'}>
          <Sparkles size={11} />{source === 'none' ? '通用视图' : 'Schema 驱动'}
        </Badge>
        <span className="num ml-auto text-xs text-ink-3" title={`设备键 ${d.id}`}>
          {d.online ? `更新于 ${timeAgo(d.updated_at)}` : `最后见 ${timeAgo(d.last_seen)}`}
        </span>
      </header>

      <div className="grid items-start gap-5 lg:grid-cols-3">
        {/* 主体：Schema 描述（或通用回落） */}
        <div className="lg:col-span-2">
          {descriptor
            ? <DescriptorView descriptor={descriptor} idx={capabilities} />
            : <RawView raw={d.state} title="上报字段（通用视图）" />}
        </div>

        {/* 命令面板：命令集来自声明（Capability actions / Descriptor commands），回落适配器白名单 */}
        <ActionPanel deviceId={key} set={commands} adapterName={d.adapter || '—'} />

        {/* 会话数值趋势：序列由通用采样得到（按属性名），不绑定任何具体字段 */}
        <Panel
          title={<span className="flex items-center gap-1.5"><Gauge size={14} />会话数值趋势</span>}
          right={<span className="num text-[11px] text-ink-3">{activePoints.length} 点</span>}
        >
          {seriesKeys.length > 1 && (
            <div className="mb-3 overflow-x-auto pb-1">
              <Segmented
                options={seriesKeys.slice(0, 5).map((k) => ({ value: k, label: humanize(k) }))}
                value={activeSeries}
                onChange={setSeriesKey}
              />
            </div>
          )}
          {seriesKeys.length === 0
            ? <p className="py-8 text-center text-xs text-ink-3">本次会话还没有数值观测</p>
            : <TrendChart points={activePoints} />}
        </Panel>

        {/* 事件时间线 */}
        <Panel title={<span className="flex items-center gap-1.5"><History size={14} />事件时间线</span>}
          className="lg:row-span-2">
          <div className="max-h-[420px] overflow-y-auto pr-1">
            {evLoading && events.length === 0
              ? <RowSkeleton rows={5} />
              : <EventFeed events={events} showDevice={false} limit={60} />}
          </div>
        </Panel>

        {/* 命令历史 */}
        <div className="lg:col-span-2">
          <CommandHistory deviceId={key} actions={commands.actions} />
        </div>

        {/* 身份与原始状态（取证用） */}
        <Panel className="lg:col-span-3"
          title={<span className="flex items-center gap-1.5"><Braces size={14} />身份与原始状态</span>}
          right={<span className="num text-[11px] text-ink-3">
            参考时间 {now.toLocaleTimeString('zh-CN', { hour12: false })}
          </span>}>
          <div className="grid gap-5 md:grid-cols-2">
            <dl className="min-w-0 space-y-2.5">
              <KeyValue k="设备键" v={d.id} mono />
              <KeyValue k="边缘节点" v={d.edge_id || '—'} mono />
              <KeyValue k="适配器" v={d.adapter || '—'} mono />
              <KeyValue k="串口" v={d.port || '—'} mono />
              <KeyValue k="在线" v={d.online ? '是' : '否'} />
              <KeyValue k="最后更新" v={d.updated_at ? timeAgo(d.updated_at) : '—'} />
              {descriptor && <KeyValue k="Descriptor 来源" v={source} mono />}
              {descriptor?.model && <KeyValue k="型号" v={descriptor.model} />}
            </dl>
            <pre className={cn('num max-h-56 overflow-auto rounded-xl bg-surface-2 p-3 font-mono text-[11px] leading-relaxed text-ink-2')}
              title="状态 JSON（诊断面：适配器上报的原始语义）">
              {JSON.stringify(d.state ?? {}, null, 2)}
            </pre>
          </div>
          <p className="mt-3 flex items-center gap-1 border-t border-hairline pt-3 text-[11px] text-ink-3">
            <Grid3x3 size={11} />
            Entity {descriptor ? descriptor.entities.length : 0} 个 ·
            Capability 引用 {descriptor
              ? new Set(descriptor.entities.flatMap((e) => e.capabilities)).size
              : 0} 种 ·
            catalog 收录 {capabilities.docs.length} 份
          </p>
        </Panel>
      </div>
    </>
  )
}