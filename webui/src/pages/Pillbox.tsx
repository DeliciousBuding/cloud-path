// 药盒控制面板（Schema 驱动）：把一台设备的 Descriptor / Capability 声明翻译成
// 「药格状态 + 命令 + 提醒/漏服历史」的产品视图。
//
// 本页面不结识任何具体命令名/字段名：
//   - 药格卡片 = Descriptor 声明的 Entity（主值取自 presentation/primaryProperty）
//   - 命令按钮 = Capability spec.actions / Descriptor commands / 适配器白名单（lib/descriptor.ts 推导）
//   - 提醒与漏服历史 = 该设备上报的真实事件与命令回执（REST + WS 实时）
// 空态/加载/错误态齐全；390px 下不产生横向溢出（长标识符一律 truncate/break）。

import { useEffect, useMemo, useState } from 'react'
import { useParams } from 'react-router'
import { useQuery } from '@tanstack/react-query'
import {History, Pill, RadioTower, RefreshCw, WifiOff} from 'lucide-react'
import { BackLink, Badge, EmptyState, ErrorState, PageHeader, Panel, StatusDot, TONE_TEXT_CLS } from '@/components/ui'
import { ActionPanel } from '@/components/ActionPanel'
import { CommandHistory } from '@/components/CommandHistory'
import { EventFeed } from '@/components/EventFeed'
import { RowSkeleton } from '@/components/Skeleton'
import { useLive } from '@/store/ws'
import { useDeviceDescriptor } from '@/hooks/useDescriptor'
import { useDevices } from '@/hooks/useDevices'
import {
  CATEGORY_LABEL, capabilityLabel, entityTitle, formatValue, primaryObservation, qualityTone,
} from '@/lib/descriptor'
import type { CapabilityIndex } from '@/lib/descriptor'
import type { DescriptorEntity, DeviceView } from '@/lib/types'
import { api } from '@/lib/api'
import { cn } from '@/lib/cn'
import { fmtDateTime, mergeEvents, optionLabel, timeAgo } from '@/lib/format'
import { deviceLabel } from '@/lib/edges'

/** 单个 Entity（药格/时钟/提醒…）的状态卡片：值取自声明的主观测，绝不猜业务字段名 */
function SlotCard({ entity, idx }: { entity: DescriptorEntity; idx: CapabilityIndex }) {
  const primary = primaryObservation(entity, idx)
  const value = primary ? formatValue(primary.value) : '—'
  const tone = primary ? qualityTone(primary.quality) : 'idle'
  const cap = primary ? capabilityLabel(primary.capability, idx) : '等待观测'
  return (
    <div className="card min-w-0 p-4" data-testid="slot-card">
      <div className="flex min-w-0 items-center gap-2">
        <Badge tone="idle" className="shrink-0">{CATEGORY_LABEL[entity.category] ?? entity.category}</Badge>
        <span className="min-w-0 truncate text-[13px] font-medium" title={entity.name || entity.unique_key}>
          {entityTitle(entity)}
        </span>
      </div>
      <div className="mt-3 flex min-w-0 items-end justify-between gap-3">
        <span className="min-w-0 flex-1 truncate text-[11px] text-ink-3"
          title={primary ? `${primary.capability} · ${primary.property}` : ''}>
          {cap}
        </span>
        <span className={cn('num min-w-0 break-all text-[22px] font-semibold leading-none tracking-tight', TONE_TEXT_CLS[tone])}
          title={`${value}${primary?.unit ? ` ${primary.unit}` : ''}`}>
          {value}{primary?.unit && <span className="ml-1 text-xs font-normal text-ink-3">{primary.unit}</span>}
        </span>
      </div>
    </div>
  )
}


/** 设备选择器：只出现在未锁定设备的场景；长 ID 用 optionLabel 截断保护 390px */
function DevicePicker({ devices, value, onChange, loading, error, onRetry }: {
  devices: DeviceView[]
  value: string
  onChange: (id: string) => void
  loading: boolean
  error: unknown
  onRetry: () => void
}) {
  if (loading) return <Panel><RowSkeleton rows={2} /></Panel>
  if (error) {
    return (
      <ErrorState icon={<WifiOff size={20} />} title="设备列表加载失败"
        hint="拿不到 GET /api/devices。这不代表没有设备接入 —— 请检查 server 是否可达后重试。"
        onRetry={onRetry} />
    )
  }
  if (devices.length === 0) {
    return (
      <EmptyState icon={<RadioTower size={24} />} title="还没有设备"
        hint="在边缘主机上配置 edge.yaml 并启动 cloudpath-edge，设备会自动注册到这里，之后即可在药盒控制面板操作。" />
    )
  }
  return (
    <Panel
      title={<span className="flex items-center gap-1.5"><Pill size={14} />选择设备</span>}
      right={<Badge tone="idle">{devices.length} 台</Badge>}
    >
      <label className="sr-only" htmlFor="pillbox-device">选择设备</label>
      <select
        id="pillbox-device"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        className="num min-w-0 w-full rounded-full border border-hairline bg-surface-2 px-3.5 py-2 font-mono text-xs outline-none transition-colors focus:border-accent"
      >
        {devices.map((d) => (
          <option key={d.id} value={d.id} title={d.id}>
            {optionLabel(d.name || d.id, 36)} · {optionLabel(d.id, 32)}
          </option>
        ))}
      </select>
      <p className="mt-2 text-[11px] leading-relaxed text-ink-3">
        选择要控制的设备；面板按设备自身声明展示药格与命令。
      </p>
    </Panel>
  )
}

/** 单设备的控制面板（设备键为非空时才挂载，保证各查询都只在有上下文时发起） */
function PillboxPanel({ deviceKey, dev, adapterCommands }: {
  deviceKey: string
  dev: DeviceView
  adapterCommands: string[]
}) {
  const [edge, devId] = deviceKey.split('/')
  const liveEvents = useLive((s) => s.events)

  const { descriptor, capabilities, commands } = useDeviceDescriptor(
    deviceKey, edge ?? '', devId ?? '', { device: dev, adapterCommands },
  )

  const { data: evHist, isLoading: evLoading } = useQuery({
    queryKey: ['pillbox-events', deviceKey], queryFn: () => api.events({ device: deviceKey, limit: 100 }),
    refetchInterval: 5000,
  })
  const events = useMemo(
    () => mergeEvents(liveEvents.filter((e) => e.device_id === deviceKey), evHist?.events ?? []),
    [liveEvents, evHist, deviceKey],
  )

  const slotEntities = useMemo(() => {
    if (!descriptor) return []
    return [...descriptor.entities].sort((a, b) => {
      const ca = a.category === 'actuator' ? 1 : 0
      const cb = b.category === 'actuator' ? 1 : 0
      return ca - cb || a.unique_key.localeCompare(b.unique_key)
    })
  }, [descriptor])


  return (
    <div className="space-y-5">
      {/* 设备实时状态横幅 */}
      <Panel className="min-w-0">
        <div className="flex flex-wrap items-center gap-x-4 gap-y-1.5">
          <span className="min-w-0 truncate text-[15px] font-semibold" title={dev.id}>{deviceLabel(dev)}</span>
          <Badge tone={dev.online ? 'ok' : 'idle'}>
            {dev.online ? '在线' : '离线'}
          </Badge>
          {!dev.online && (
            <span className="text-[11px] text-ink-3">以下是最后一次上报的内容，不代表当前状态</span>
          )}
          <span className="num ml-auto text-[11px] text-ink-3"
            title={`更新 ${fmtDateTime(dev.updated_at)} · 最后见 ${fmtDateTime(dev.last_seen)}`}>
            {dev.online ? `更新于 ${timeAgo(dev.updated_at)}` : `最后见 ${timeAgo(dev.last_seen)}`}
          </span>
        </div>
        <dl className="mt-3 grid grid-cols-2 gap-x-4 gap-y-1.5 border-t border-hairline pt-3 text-[11px] text-ink-3 sm:grid-cols-4">
          <div className="min-w-0"><dt className="truncate">边缘节点</dt><dd className="num truncate font-mono">{dev.edge_id || '—'}</dd></div>
          <div className="min-w-0"><dt className="truncate">适配器</dt><dd className="num truncate font-mono">{dev.adapter || '—'}</dd></div>
          <div className="min-w-0"><dt className="truncate">声明实体</dt><dd className="num truncate">{descriptor ? `${descriptor.entities.length} 个` : '无 Descriptor'}</dd></div>
          <div className="min-w-0"><dt className="truncate">串口</dt><dd className="num truncate font-mono">{dev.port || '—'}</dd></div>
        </dl>
      </Panel>

      {/* 药格/实体状态 */}
      <Panel
        title={<span className="flex items-center gap-1.5"><Pill size={14} />药格与设备状态</span>}
        right={<span className="text-[11px] text-ink-3">{descriptor ? `${slotEntities.length} 个实体` : '等待 Descriptor'}</span>}
      >
        {!descriptor ? (
          <p className="py-8 text-center text-sm text-ink-3">
            该设备还没有提供 Descriptor，暂时无法展示药格状态（命令区仍可从适配器白名单下发）。
          </p>
        ) : slotEntities.length === 0 ? (
          <p className="py-8 text-center text-sm text-ink-3">Descriptor 未声明 Entity，没有可展示的药格。</p>
        ) : (
          <div className="grid items-start gap-4 sm:grid-cols-2 lg:grid-cols-3">
            {slotEntities.map((e) => <SlotCard key={e.entity_id || e.unique_key} entity={e} idx={capabilities} />)}
          </div>
        )}
      </Panel>

      {/* 命令 + 提醒/漏服历史 */}
      <div className="grid items-start gap-5 lg:grid-cols-2">
        <ActionPanel deviceId={deviceKey} set={commands} adapterName={dev.adapter || '—'} />
        <div className="space-y-5">
          <Panel
            title={<span className="flex items-center gap-1.5"><History size={14} />提醒与漏服</span>}
            right={<span className="text-[11px] text-ink-3">{events.length} 条</span>}
          >
            {evLoading && events.length === 0 ? (
              <RowSkeleton rows={3} />
            ) : events.length === 0 ? (
              <p className="py-4 text-center text-sm text-ink-3">还没有提醒/漏服事件</p>
            ) : (
              <EventFeed events={events} showDevice={false} limit={50} fullTime />
            )}
          </Panel>
          <CommandHistory deviceId={deviceKey} actions={commands.actions} />
        </div>
      </div>
    </div>
  )
}

export default function Pillbox() {
  const { edgeId, deviceId } = useParams()
  const lockedKey = edgeId && deviceId
    ? `${decodeURIComponent(edgeId)}/${decodeURIComponent(deviceId)}`
    : null
  const { list, loading, error, refetch } = useDevices()
  const [selected, setSelected] = useState<string>(lockedKey ?? '')

  // 首次拿到设备列表后默认选中第一台（决策来自真实读面，不魔法猜测）
  useEffect(() => {
    if (!selected && list.length > 0) setSelected(list[0]?.id ?? '')
  }, [selected, list])

  const key = lockedKey ?? selected ?? ''
  const live = useLive((s) => (key ? s.devices[key] : undefined))
  const dev = live ?? list.find((d) => d.id === key) ?? null

  const { data: adapters } = useQuery({
    queryKey: ['adapters'], queryFn: api.adapters, staleTime: 5 * 60_000,
  })
  const adapterCommands = useMemo(() => {
    const a = adapters?.adapters.find((x) => x.name === dev?.adapter)
    return a?.commands ?? []
  }, [adapters, dev?.adapter])

  const summaryLine = dev
    ? `${deviceLabel(dev)} · ${dev.online ? '在线' : '离线'}`
    : null

  return (
    <>
      {!lockedKey && list.length > 0 && <BackLink to="/devices" label="设备" />}
      <PageHeader
        title="药盒控制"
        subtitle="查看每个药格的状态、下发设备命令、查看提醒与漏服历史"
        actions={summaryLine ? (
          <span className="flex items-center gap-1.5 text-sm text-ink-2">
            <StatusDot online={Boolean(dev?.online)} />
            <span className="max-w-[16rem] truncate" title={summaryLine}>{summaryLine}</span>
          </span>
        ) : undefined}
      />

      {!lockedKey && (
        <div className="mb-5">
          <DevicePicker devices={list} value={key} onChange={setSelected}
            loading={loading} error={!list.length ? error : null} onRetry={refetch} />
        </div>
      )}

      {!key ? (
        <EmptyState icon={<Pill size={24} />} title="请选择设备"
          hint="选择一台设备后，这里会展示它的药格状态、可下发命令与提醒/漏服历史。" />
      ) : !dev ? (
        <Panel><RowSkeleton rows={3} /></Panel>
      ) : (
        <PillboxPanel deviceKey={key} dev={dev} adapterCommands={adapterCommands} />
      )}

      <p className="mt-5 flex items-center gap-1.5 text-[11px] text-ink-3">
        <RefreshCw size={11} className="shrink-0" />
        药格与命令清单来自设备自身声明，当前值走实时通道；前端不写死设备字段。
      </p>
    </>
  )
}


