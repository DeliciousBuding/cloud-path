import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'
import { Link } from 'react-router'
import { ChevronRight } from 'lucide-react'
import { Badge } from './ui'
import { useDeviceDescriptor } from '@/hooks/useDescriptor'
import { deviceStatusMeta, metricTiles } from '@/lib/descriptor'
import { fmtDateTime } from '@/lib/format'
import type { DeviceView } from '@/lib/types'

/**
 * 设备列表行：Name / 网关 / 关键读数 / Status / Last Seen。
 * 1440px 视口起显示关键读数列（声明主观测至多两条）；窄屏把读数与最近上报收在同一行，
 * 避免「状态点 + 状态文字」和「读数标题」重复占用扫读空间。
 *
 * 桌面（lg）是紧凑表格行；窄屏自动堆叠并补上列名，因此 390px 下每一列都仍可读、可点。
 * 读数一律由 Descriptor 声明推导（primaryObservation + presentation），前端不维护字段清单；
 * Descriptor 缺席时明确说「等待声明」，不猜也不编。
 */
export function DeviceRow({ d }: { d: DeviceView }) {
  const { t } = useTranslation('devices')
  const [edgeId, devId] = d.id.split('/')
  const { descriptor, capabilities } = useDeviceDescriptor(d.id, edgeId ?? '', devId ?? '', { device: d })

  // 舰队行的「关键读数」：声明主观测至多两条（metricTiles 与概览 KPI 同一推导，无设备特例）
  const metrics = useMemo(() => (descriptor ? metricTiles(descriptor, capabilities, 2) : []), [descriptor, capabilities])

  const st = deviceStatusMeta(d.online, descriptor?.status)
  const statusLabel = d.online
    ? descriptor?.status === 'degraded' ? t('status.degraded') : t('status.online')
    : t('status.offline')
  const name = d.name || devId || d.id
  const seen = d.online ? d.updated_at : d.last_seen
  // 鼠标/键盘意图出现后再取详情路由 chunk；不进入页面就不下载。
  const preloadDeviceDetail = () => { void import('@/pages/DeviceDetail') }
  const gatewayTitle = [d.edge_id, d.adapter ? t('row.deviceType', { type: d.adapter }) : '', d.port]
    .filter(Boolean).join(' · ')
  return (
    <li className="grid grid-cols-[minmax(0,1fr)_auto] gap-x-4 gap-y-1 border-b border-hairline px-4 py-3 last:border-b-0 lg:grid-cols-[minmax(0,1.5fr)_minmax(0,0.9fr)_5.5rem_9.5rem] lg:items-center xl:grid-cols-[minmax(0,1.35fr)_minmax(0,0.8fr)_minmax(0,1.2fr)_5.5rem_9.5rem]">
      {/* Name（窄屏把状态徽标放在同一行，不重复状态点） */}
      <div className="col-span-2 flex min-w-0 items-center gap-2 lg:col-span-1">
        <Link
          to={`/devices/${encodeURIComponent(edgeId ?? '')}/${encodeURIComponent(devId ?? '')}`}
          className="flex min-h-touch min-w-0 items-center gap-1 text-compact font-medium no-underline hover:text-accent sm:min-h-0"
          title={`${name} · ${d.id}`}
          onPointerEnter={preloadDeviceDetail}
          onFocus={preloadDeviceDetail}
        >
          <span className="min-w-0 truncate">{name}</span>
          <ChevronRight size={11} className="shrink-0 text-ink-3" />
        </Link>
        <span className="num hidden shrink-0 truncate font-mono text-micro text-ink-3 2xl:inline" title={d.id}>
          {devId}
        </span>
        <span className="ml-auto shrink-0 lg:hidden">
          <Badge tone={st.tone}>{statusLabel}</Badge>
        </span>
      </div>

      {/* 网关 */}
      <div className="num col-span-2 min-w-0 truncate text-meta text-ink-2 lg:col-span-1" title={gatewayTitle}>
        <span className="text-ink-3 lg:hidden">{t('row.gatewayPrefix')}</span>
        {d.edge_id || '—'}
      </div>

      {/* 关键读数（声明主观测；能力全量事实面在详情页「能力」tab） */}
      <div className="col-span-2 flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 lg:hidden xl:col-span-1 xl:flex">
        {!descriptor ? (
          <span className="text-meta text-ink-3" title={t('row.metricsWaitingTitle')}>{t('row.metricsWaiting')}</span>
        ) : metrics.length === 0 ? (
          <span className="text-meta text-ink-3" title={t('row.noMetricsTitle')}>{t('row.noMetrics')}</span>
        ) : (
          metrics.map((m, i) => (
            <span key={`${m.label}-${i}`} className="num flex min-w-0 items-baseline gap-1 text-meta" title={m.title}>
              <span className="min-w-0 truncate text-ink-3">{m.label}</span>
              <span className="min-w-0 truncate font-medium text-ink-2">
                {m.text}{m.unit ? ` ${m.unit}` : ''}
              </span>
            </span>
          ))
        )}
        <span className="num ml-auto shrink-0 font-mono text-micro text-ink-3 lg:hidden"
          title={seen ? fmtDateTime(seen) : t('row.never')}>
          {d.online
            ? t('row.updatedAt', { time: seen ? fmtDateTime(seen) : t('row.never') })
            : t('row.lastOnline', { time: seen ? fmtDateTime(seen) : t('row.never') })}
        </span>
      </div>

      {/* Online / Offline（桌面列；窄屏已在名称行呈现） */}
      <div className="hidden lg:block">
        <Badge tone={st.tone}>{statusLabel}</Badge>
      </div>

      {/* Last Seen（桌面列；窄屏已并入读数行） */}
      <div className="num hidden min-w-0 truncate text-right font-mono text-micro text-ink-3 lg:block" title={seen ? fmtDateTime(seen) : t('row.never')}>
        {seen ? fmtDateTime(seen) : t('row.never')}
      </div>
    </li>
  )
}

/** 列表表头（仅桌面；窄屏每行自带列名，故隐藏） */
export function DeviceRowHead() {
  const { t } = useTranslation('devices')
  return (
    <li
      aria-hidden
      className="hidden gap-x-4 px-4 pb-2 text-meta font-medium text-ink-3 lg:grid lg:grid-cols-[minmax(0,1.5fr)_minmax(0,0.9fr)_5.5rem_9.5rem] xl:grid-cols-[minmax(0,1.35fr)_minmax(0,0.8fr)_minmax(0,1.2fr)_5.5rem_9.5rem]"
    >
      <span>{t('row.device')}</span><span>{t('row.gateway')}</span><span className="hidden xl:inline">{t('row.keyData')}</span><span>{t('row.status')}</span><span className="text-right">{t('row.lastReport')}</span>
    </li>
  )
}
