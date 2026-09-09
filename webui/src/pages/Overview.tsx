import { useMemo } from 'react'
import { Link } from 'react-router'
import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import {
  Activity, AlertTriangle, ArrowDown, ArrowRight, CheckCircle2, Cpu, History, Inbox,
  RefreshCw, WifiOff,
} from 'lucide-react'
import {
  Badge, EmptyState, ErrorState, Panel, PageHeader, Spinner, TONE_CLS, TONE_TEXT_CLS, type Tone,
} from '@/components/ui'
import { DeviceRow, DeviceRowHead } from '@/components/DeviceRow'
import { RowSkeleton } from '@/components/Skeleton'
import { EventFeed } from '@/components/EventFeed'
import { api } from '@/lib/api'
import { deviceShortName, overviewAlerts, overviewStats, type OverviewAlert, type OverviewStat } from '@/lib/overview'
import { mergeEvents, timeAgo } from '@/lib/format'
import { cn } from '@/lib/cn'
import { useNow } from '@/hooks/useNow'
import { usePageTitle } from '@/hooks/usePageTitle'
import { useDevices } from '@/hooks/useDevices'
import { useEdges } from '@/hooks/useEdges'
import { useOverview } from '@/hooks/useOverview'
import { useLive } from '@/store/ws'
import { i18n } from '@/i18n'

function fmtUptime(seconds: unknown): string | null {
  if (typeof seconds !== 'number' || !Number.isFinite(seconds) || seconds < 0) return null
  if (seconds < 60) return i18n.t('uptime.starting', { ns: 'overview' })
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) return i18n.t('uptime.minutes', { ns: 'overview', count: minutes })
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return i18n.t('uptime.hours', { ns: 'overview', count: hours })
  return i18n.t('uptime.days', { ns: 'overview', count: Math.floor(hours / 24) })
}

/**
 * 概览首屏按三段组织：现在怎样 → 需要关注 → 去哪里处理。
 *
 * 数据边界不变：计数、离线设备、失败操作和近期记录优先取聚合读面；聚合读面缺席时
 * 只使用设备和网关列表的真实字段降级，任何数字都不在前端编造。
 */
export default function Overview() {
  const { t } = useTranslation('overview')
  usePageTitle(t('title'))
  useNow()

  const { data, loading, isFetching, refetch } = useOverview()
  const { list: devices, loading: devLoading, error: devError, refetch: refetchDevices } = useDevices()
  const edges = useEdges()
  const liveEvents = useLive((s) => s.events)
  const { data: health } = useQuery({
    queryKey: ['health'], queryFn: api.health, refetchInterval: 30_000,
  })

  const feed = useMemo(
    () => mergeEvents(liveEvents, data?.recent_events ?? []).slice(0, 12),
    [liveEvents, data],
  )

  const serverOk = Boolean(data)
  const stats: OverviewStat[] | null = data
    ? overviewStats(data).map((s) => ({
      ...s,
      label: s.key === 'devices' ? t('stats.devices')
        : s.key === 'edges' ? t('stats.edges')
          : s.key === 'plugins' ? t('stats.plugins')
            : t('stats.commands'),
      emptyHint: s.key === 'devices' ? t('empty.devices')
        : s.key === 'edges' ? t('empty.edges')
          : s.key === 'plugins' ? t('empty.plugins')
            : t('empty.commands'),
    }))
    : null

  const devOk = !devLoading && !devError
  const edgesOk = !edges.loading && !edges.error
  // 首帧仍在读取聚合状态时，不用空列表提前渲染 0/0；列表通道失败或聚合失败后才降级。
  const fallbackStats: OverviewStat[] = !serverOk && !loading ? [
    ...(devOk ? [{
      key: 'devices' as const, label: t('stats.devices'),
      online: devices.filter((d) => d.online).length, total: devices.length,
      emptyHint: t('empty.devices'),
      tone: (devices.length === 0 ? 'idle' : devices.some((d) => d.online) ? 'ok' : 'bad') as Tone,
    }] : []),
    ...(edgesOk ? [{
      key: 'edges' as const, label: t('stats.edges'),
      online: edges.online, total: edges.list.length,
      emptyHint: t('empty.edges'),
      tone: (edges.list.length === 0 ? 'idle' : edges.online === 0 ? 'bad' : 'ok') as Tone,
    }] : []),
  ] : []
  const shownStats: OverviewStat[] | null = stats ?? (fallbackStats.length ? fallbackStats : null)

  const alerts: OverviewAlert[] = data
    ? overviewAlerts(data).map((a) => ({
      ...a,
      title: a.id === 'edges-offline' ? t('alerts.edgesOfflineTitle')
        : a.id === 'devices-offline' ? t('alerts.devicesOfflineTitle')
          : a.id === 'commands-failed' ? t('alerts.commandsFailedTitle')
            : a.id === 'plugins-gap' ? t('alerts.pluginsGapTitle') : a.title,
      hint: a.id === 'edges-offline' ? t('alerts.edgesOfflineHint')
        : a.id === 'devices-offline' ? t('alerts.devicesOfflineHint')
          : a.id === 'commands-failed' ? t('alerts.commandsFailedHint')
            : a.id === 'plugins-gap' ? t('alerts.pluginsGapHint') : a.hint,
    }))
    : []

  const fallbackAlerts: OverviewAlert[] = !serverOk ? [
    ...devices.filter((d) => !d.online).map((d): OverviewAlert => {
      const [edgeId, devId] = d.id.split('/')
      return {
        id: `dev-offline-${d.id}`, tone: 'warn', count: 1,
        to: `/devices/${encodeURIComponent(edgeId ?? '')}/${encodeURIComponent(devId ?? '')}`,
        title: t('fallback.deviceOfflineTitle', { name: deviceShortName(d) }),
        hint: t('fallback.deviceOfflineHint'),
      }
    }),
    ...edges.list.filter((e) => !e.online).map((e): OverviewAlert => ({
      id: `edge-offline-${e.edge_id}`, tone: 'bad', count: 1,
      to: `/edges/${encodeURIComponent(e.edge_id)}`,
      title: t('fallback.edgeOfflineTitle', { id: e.edge_id }),
      hint: t('fallback.edgeOfflineHint'),
    })),
  ] : []

  const attentionRows = [...alerts, ...fallbackAlerts]
  const attention = serverOk ? alerts.reduce((n, a) => n + a.count, 0) : fallbackAlerts.length
  const attentionCategories = attentionRows.length
  const deviceStat = shownStats?.find((s) => s.key === 'devices')
  const hasStats = Boolean(shownStats?.length)
  const stillLoading = !hasStats && (loading || devLoading)
  const partial = !serverOk && !loading && hasStats

  let nowTone: Tone = 'idle'
  let nowTitle = t('now.loadingTitle')
  let nowDetail = t('now.loadingDetail')
  let NowIcon = Activity

  if (!hasStats && !stillLoading) {
    nowTone = 'bad'
    nowTitle = t('now.unavailableTitle')
    nowDetail = t('now.unavailableDetail')
    NowIcon = WifiOff
  } else if (hasStats && attention > 0) {
    nowTone = attentionRows.some((a) => a.tone === 'bad') ? 'bad' : 'warn'
    nowTitle = t('now.attentionTitle', { count: attention })
    nowDetail = t('now.attentionDetail')
    NowIcon = AlertTriangle
  } else if (hasStats && (deviceStat?.total ?? 0) === 0) {
    nowTone = 'idle'
    nowTitle = t('now.waitingTitle')
    nowDetail = t('now.waitingDetail')
    NowIcon = Inbox
  } else if (hasStats) {
    nowTone = 'ok'
    nowTitle = t('now.healthyTitle')
    nowDetail = t('now.healthyDetail')
    NowIcon = CheckCircle2
  }

  const subtitle = data?.server_time
    ? t('subtitleUpdated', { time: timeAgo(data.server_time) })
    : fmtUptime(health?.uptime_s) ?? (health ? t('serviceHealthy') : t('latestStatus'))

  return (
    <>
      <PageHeader
        title={t('title')}
        subtitle={subtitle}
        actions={
          <button
            type="button"
            className="btn btn-ghost"
            onClick={() => { void refetch(); void refetchDevices() }}
            disabled={isFetching}
          >
            {isFetching ? <Spinner size={13} /> : <RefreshCw size={13} />} {t('refresh')}
          </button>
        }
      />

      <section
        className="card overflow-hidden"
        aria-labelledby="overview-now-title"
        role={!hasStats && !stillLoading ? 'alert' : undefined}
      >
        <div className={cn(hasStats && 'lg:grid lg:grid-cols-[minmax(0,1.35fr)_minmax(22rem,1fr)]')}>
          <div className="p-4 sm:p-6">
            <p className="text-meta font-medium text-ink-3">{t('now.section')}</p>
            <div className="mt-3 flex items-start gap-3">
              <span className={cn('flex h-8 w-8 shrink-0 items-center justify-center rounded-pill sm:h-9 sm:w-9', TONE_CLS[nowTone])}>
                <NowIcon size={18} strokeWidth={2} />
              </span>
              <div className="min-w-0">
                <h2 id="overview-now-title" className={cn('text-section-sm font-semibold leading-tight tracking-[-0.01em] sm:text-section', TONE_TEXT_CLS[nowTone])}>
                  {nowTitle}
                </h2>
                <p className="mt-1.5 max-w-[58ch] text-body text-ink-2">{nowDetail}</p>
              </div>
            </div>

            <div className="mt-4 flex flex-wrap items-center gap-2">
              {!hasStats && !stillLoading ? (
                <button type="button" className="btn btn-primary" onClick={() => { void refetch(); void refetchDevices() }} disabled={isFetching}>
                  {isFetching ? <Spinner size={13} /> : <RefreshCw size={13} />} {t('reload')}
                </button>
              ) : attention > 0 ? (
                <a href="#attention" className="btn btn-primary">{t('actions.viewAttention')} <ArrowDown size={14} /></a>
              ) : (deviceStat?.total ?? 0) === 0 && hasStats ? (
                <Link to="/edges" className="btn btn-primary">{t('actions.goEdges')} <ArrowRight size={14} /></Link>
              ) : hasStats ? (
                <Link to="/devices" className="btn btn-primary">{t('actions.viewDevices')} <ArrowRight size={14} /></Link>
              ) : null}
            </div>

            {partial && (
              <p className="mt-4 flex flex-wrap items-center gap-1.5 text-meta text-ink-3">
                <AlertTriangle size={12} className="shrink-0 text-warn" />
                {t('partial.message')}
                <button type="button" className="link" onClick={() => void refetch()}>{t('partial.reload')}</button>
              </p>
            )}
          </div>

          {hasStats && (
            <div className="border-t border-hairline bg-surface-2/60 p-4 sm:p-5 lg:border-l lg:border-t-0">
              <p className="text-meta font-medium text-ink-3">{t('keyStatus')}</p>
              <div className="mt-3 grid grid-cols-2 gap-x-4 gap-y-3">
                {shownStats?.map((s) => {
                  const value = s.key === 'commands' ? s.online : `${s.online}/${s.total}`
                  const hint = s.key === 'devices' || s.key === 'edges'
                    ? s.total === 0 ? s.emptyHint : t('hints.offlineDevices', { count: Math.max(0, s.total - s.online) })
                    : s.key === 'plugins'
                      ? s.total === 0 ? s.emptyHint : s.online === s.total ? t('hints.allHealthy') : t('hints.pluginsNotRunning', { count: s.total - s.online })
                      : s.online === 0 ? t('hints.noFailures') : t('hints.checkFailures')
                  return (
                    <div key={s.key} className="min-w-0 border-b border-hairline pb-2 last:border-b-0">
                      <div className="flex items-center justify-between gap-2">
                        <span className="min-w-0 truncate text-meta text-ink-3">{s.label}</span>
                        <span className={cn('num shrink-0 text-stat font-semibold leading-none', TONE_TEXT_CLS[s.tone])}>{value}</span>
                      </div>
                      <p className="mt-1 truncate text-micro text-ink-3" title={hint}>{hint}</p>
                    </div>
                  )
                })}
              </div>
            </div>
          )}
        </div>
      </section>

      <div className="mt-5 space-y-5">
        {stillLoading ? (
          <>
            <Panel><RowSkeleton rows={3} /></Panel>
            <Panel><RowSkeleton rows={5} /></Panel>
          </>
        ) : (
          <>
            <section id="attention" className="card scroll-mt-28 overflow-hidden">
              <div className="flex items-center justify-between gap-3 border-b border-hairline px-4 py-3.5 sm:px-5">
                <div className="min-w-0">
                  <p className="text-meta text-ink-3">{t('attention.section')}</p>
                  <h2 className="mt-0.5 flex items-center gap-1.5 text-lead font-semibold tracking-[-0.01em]">
                    <AlertTriangle size={14} className="text-warn" /> {t('attention.title')}
                  </h2>
                </div>
                {attention > 0 && <Badge tone="warn">
                    {t('attention.badge', { count: attention, categories: attentionCategories, items: attention })}
                  </Badge>}
              </div>

              {attentionRows.length === 0 ? (
                !hasStats && !stillLoading ? (
                  <div className="flex flex-wrap items-center gap-x-3 gap-y-2 px-4 py-4 sm:px-5">
                    <AlertTriangle size={16} className="shrink-0 text-warn" />
                    <p className="min-w-0 flex-1 text-body text-ink-2">{t('attention.unavailable')}</p>
                    <button type="button" className="link shrink-0 text-meta" onClick={() => { void refetch(); void refetchDevices() }}>
                      {t('attention.recheck')}
                    </button>
                  </div>
                ) : (
                  <div className="flex flex-wrap items-center gap-x-3 gap-y-2 px-4 py-4 sm:px-5">
                    <CheckCircle2 size={16} className="shrink-0 text-ok" />
                    <p className="min-w-0 flex-1 text-body text-ink-2">{t('attention.clear')}</p>
                    <Link to="/activity" className="link flex min-h-touch shrink-0 items-center gap-0.5 text-meta">
                      {t('attention.viewActivity')} <ArrowRight size={12} />
                    </Link>
                  </div>
                )
              ) : (
                <ul className="m-0 list-none divide-y divide-hairline p-0">
                  {attentionRows.map((a) => (
                    <li key={a.id}>
                      <Link
                        to={a.to}
                        className="group flex min-w-0 items-center gap-3 px-4 py-3.5 transition-colors hover:bg-ink-3/5 sm:px-5"
                      >
                        <span className={cn('h-2 w-2 shrink-0 rounded-pill',
                          a.tone === 'bad' ? 'bg-bad' : a.tone === 'warn' ? 'bg-warn' : 'bg-ink-3')} />
                        <span className="min-w-0 flex-1">
                          <span className="block truncate text-body font-medium">{a.title}{a.count > 1 ? t('attention.itemCount', { count: a.count }) : ''}</span>
                          <span className="mt-0.5 hidden truncate text-meta text-ink-3 sm:block">{a.hint}</span>
                        </span>
                        <span className="shrink-0 text-meta font-medium text-accent">{t('attention.action')}</span>
                        <ArrowRight size={13} className="shrink-0 text-ink-3 transition-transform group-hover:translate-x-0.5" />
                      </Link>
                    </li>
                  ))}
                </ul>
              )}
            </section>

            {hasStats && (
              <Panel
                title={<span className="flex items-center gap-1.5"><Cpu size={14} />{t('devices.title')}</span>}
                right={
                  <Link to="/devices" className="link flex min-h-touch items-center gap-0.5 text-meta">
                    {t('devices.viewAll')} <ArrowRight size={12} />
                  </Link>
                }
              >
                {devLoading ? (
                  <RowSkeleton rows={3} />
                ) : devError ? (
                  <ErrorState
                    plain compact
                    icon={<WifiOff size={20} />}
                    title={t('devices.errorTitle')}
                    hint={t('devices.errorHint')}
                    onRetry={refetchDevices}
                    retryLabel={t('devices.retry')}
                  />
                ) : devices.length === 0 ? (
                  <EmptyState
                    plain compact
                    icon={<Inbox size={24} />}
                    title={t('devices.emptyTitle')}
                    hint={t('devices.emptyHint')}
                    action={<Link to="/edges" className="btn btn-ghost">{t('devices.goEdges')} <ArrowRight size={13} /></Link>}
                  />
                ) : (
                  <ul className="m-0 list-none p-0">
                    <DeviceRowHead />
                    {devices.slice(0, 8).map((d) => <DeviceRow key={d.id} d={d} />)}
                  </ul>
                )}
                {devices.length > 8 && (
                  <Link to="/devices" className="link mt-3 flex min-h-touch items-center gap-0.5 border-t border-hairline pt-3 text-meta">
                    {t('devices.more', { count: devices.length - 8 })} <ArrowRight size={12} />
                  </Link>
                )}
              </Panel>
            )}

            <Panel
              title={<span className="flex items-center gap-1.5"><Activity size={14} />{t('activity.title')}</span>}
              right={
                <Link to="/activity" className="link flex min-h-touch items-center gap-0.5 text-meta">
                  {t('activity.viewAll')} <ArrowRight size={12} />
                </Link>
              }
            >
              {loading && feed.length === 0 ? (
                <RowSkeleton rows={5} />
              ) : feed.length === 0 ? (
                hasStats ? (
                  <EmptyState
                    plain compact
                    icon={<History size={24} />}
                    title={t('activity.emptyTitle')}
                    hint={t('activity.emptyHint')}
                    action={<Link to="/devices" className="btn btn-ghost">{t('activity.viewDevices')} <ArrowRight size={13} /></Link>}
                  />
                ) : (
                  <p className="py-8 text-center text-body text-ink-3">{t('activity.unavailable')}</p>
                )
              ) : (
                <EventFeed events={feed} limit={10} />
              )}
            </Panel>
          </>
        )}
      </div>
    </>
  )
}
