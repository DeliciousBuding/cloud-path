import { useMemo } from 'react'
import { Link, useParams } from 'react-router'
import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { ArrowRight, Cpu, History, Network, Server } from 'lucide-react'
import { BackLink, Badge, EmptyState, ErrorState, KeyValue, Panel } from '@/components/ui'
import { RowSkeleton } from '@/components/Skeleton'
import { EventFeed } from '@/components/EventFeed'
import { api } from '@/lib/api'
import { useDevices } from '@/hooks/useDevices'
import { useEdges } from '@/hooks/useEdges'
import { useLive } from '@/store/ws'
import { deviceLabel, edgeFacts } from '@/lib/edges'
import { fmtDateTime, mergeEvents, timeAgo } from '@/lib/format'
import { useNow } from '@/hooks/useNow'
import { usePageTitle } from '@/hooks/usePageTitle'

/**
 * 网关详情：这台主机的连接事实 + 它名下所有设备的当前状态 + 相关事件。
 * 该网关离线时页面依然完整可读（历史事实与设备清单都在），不因为离线就变空白。
 */
export default function EdgeDetail() {
  const { t } = useTranslation('edges')
  const { edgeId = '' } = useParams()
  const id = decodeURIComponent(edgeId)
  const { list: edges, loading: edgeLoading, error: edgeError, refetch } = useEdges()
  const { list: devices } = useDevices()
  const liveEvents = useLive((s) => s.events)
  useNow() // 头部「连接于 X 前」每秒走字

  const facts = useMemo(() => edgeFacts(edges, devices), [edges, devices])
  const f = facts.find((x) => x.edge.edge_id === id)
  usePageTitle(f ? t('detail.titleWithId', { id: f.edge.edge_id }) : t('detail.title'))

  const { data: evHist, isLoading: evLoading, error: evError, refetch: refetchEvents } = useQuery({
    queryKey: ['edge-events', id],
    // /api/events 只接受 device 参数：按网关取最近事件后在前端按设备键前缀归属
    queryFn: () => api.events({ limit: 300 }),
    refetchInterval: 8000,
  })

  const events = useMemo(() => {
    const all = mergeEvents(liveEvents, evHist?.events ?? [])
    return all.filter((e) => e.device_id.startsWith(`${id}/`)).slice(0, 60)
  }, [liveEvents, evHist, id])

  if (edgeLoading) {
    return (
      <>
        <BackLink to="/edges" label={t('detail.back')} />
        <Panel><RowSkeleton rows={4} /></Panel>
      </>
    )
  }

  if (!f) {
    return (
      <>
        <BackLink to="/edges" label={t('detail.back')} />
        {edgeError ? (
          <ErrorState icon={<Network size={20} />} title={t('detail.loadErrorTitle')}
            hint={t('detail.loadErrorHint', { id })}
            onRetry={refetch} />
        ) : (
        <EmptyState icon={<Network size={24} />} title={t('detail.notFoundTitle')}
          hint={t('detail.notFoundHint', { id })} />
        )}
      </>
    )
  }

  const e = f.edge
  return (
    <>
      <BackLink to="/edges" label={t('detail.back')} />

      <header className="mb-7 flex flex-wrap items-center gap-3">
        <h1 className="min-w-0 max-w-full truncate font-mono text-hero font-semibold" title={e.edge_id}>
          {e.edge_id}
        </h1>
        <Badge tone={e.online ? 'ok' : 'idle'}>
          {e.online ? t('detail.statusOnline') : t('detail.statusOffline')}
        </Badge>
        {e.version && (
          <span className="min-w-0 truncate font-mono text-micro text-ink-3"
            title={t('detail.versionTitle', { version: e.version })}>{e.version}</span>
        )}
        <span className="ml-auto text-meta text-ink-3"
          title={e.connected_at ? fmtDateTime(e.connected_at) : undefined}>
          {e.online ? t('detail.connectedAt') : t('detail.lastOnlinePrefix')}
          {e.connected_at ? <span className="num">{timeAgo(e.connected_at)}</span> : '—'}
        </span>
      </header>

      {!e.online && (
        <div className="banner mb-5 rounded-tile" role="status">
          {t('detail.offlineBanner')}
        </div>
      )}

      <div className="grid items-start gap-5 lg:grid-cols-3">
        <Panel title={<span className="flex items-center gap-1.5"><Server size={14} />{t('detail.infoTitle')}</span>}>
          <dl className="space-y-2.5">
            <KeyValue k={t('detail.id')} v={e.edge_id} mono />
            <KeyValue k={t('detail.version')} v={e.version || t('detail.unknown')} mono />
            <KeyValue wrap k={t('detail.lastUpdate')}
              v={<span className="num font-mono">{f.lastReport ? fmtDateTime(f.lastReport) : t('detail.neverUpdated')}</span>} />
            <KeyValue k={t('detail.connectedDevices')}
              v={t('detail.connectedDevicesValue', { total: f.devices.length, online: f.onlineDevices })} />
            {f.declared.length !== f.devices.length && (
              <KeyValue k={t('detail.discoveredDevices')} v={t('detail.discoveredDevicesValue', { count: f.declared.length })} />
            )}
          </dl>
          {f.declared.length !== f.devices.length && (
            <p className="mt-3 border-t border-hairline pt-3 text-meta leading-relaxed text-ink-3">
              {t('detail.mismatch')}
            </p>
          )}
        </Panel>

        <Panel className="lg:col-span-2"
          title={<span className="flex items-center gap-1.5"><Cpu size={14} />{t('detail.devicesTitle')}</span>}
          right={<span className="text-meta text-ink-3">{t('detail.devicesOnline', { online: f.onlineDevices, total: f.devices.length })}</span>}>
          {f.devices.length === 0 ? (
            <p className="py-8 text-center text-body text-ink-3">{t('detail.devicesEmpty')}</p>
          ) : (
            <ul className="divide-y divide-hairline">
              {f.devices.map((d) => {
                const dev = d.id.split('/').pop() ?? d.id
                return (
                  <li key={d.id} className="flex min-w-0 flex-col gap-2 py-3 sm:flex-row sm:items-center">
                    <Badge tone={d.online ? 'ok' : 'idle'} className="w-fit shrink-0">
                      {d.online ? t('detail.online') : t('detail.offline')}
                    </Badge>
                    <Link to={`/devices/${encodeURIComponent(e.edge_id)}/${encodeURIComponent(dev)}`}
                      className="flex min-h-touch min-w-0 flex-1 flex-col justify-center no-underline">
                      <span className="block truncate text-compact font-medium hover:text-accent" title={deviceLabel(d)}>
                        <span className="sr-only">{d.online ? t('detail.onlineSr') : t('detail.offlineSr')}</span>
                        {deviceLabel(d)}
                      </span>
                      <span className="num block truncate font-mono text-micro text-ink-3"
                        title={`${d.adapter || t('detail.unknownAdapter')}${d.port ? ` · ${d.port}` : ''}`}>
                        {d.port ? t('detail.port', { port: d.port }) : t('detail.portUnknown')}
                      </span>
                    </Link>
                    <span className="num shrink-0 font-mono text-micro text-ink-3 sm:text-right"
                      title={d.online ? t('detail.lastUpdate') : t('detail.lastOnline')}>
                      <span className="sm:hidden">{t('detail.lastReport')} </span>
                      {(d.online ? d.updated_at : d.last_seen)
                        ? fmtDateTime(d.online ? d.updated_at : d.last_seen) : '—'}
                    </span>
                  </li>
                )
              })}
            </ul>
          )}
        </Panel>

        <Panel className="lg:col-span-3"
          title={<span className="flex items-center gap-1.5"><History size={14} />{t('detail.eventsTitle')}</span>}
          right={<span className="num text-meta text-ink-3">{t('detail.eventsCount', { count: events.length })}</span>}>
          {evLoading && events.length === 0 ? (
            <RowSkeleton rows={5} />
          ) : evError && events.length === 0 ? (
            <ErrorState compact icon={<History size={20} />} title={t('detail.eventsLoadError')}
              hint={t('detail.eventsLoadHint')}
              onRetry={() => { void refetchEvents() }} />
          ) : events.length === 0 ? (
            <p className="py-8 text-center text-body text-ink-3">{t('detail.eventsEmpty')}</p>
          ) : (
            <>
              <EventFeed events={events} limit={20} dayGrouped />
              <div className="mt-3 flex flex-wrap items-center justify-between gap-2 border-t border-hairline pt-3 text-meta">
                <span className="text-ink-3">
                  {events.length > 20 ? t('detail.eventsMore', { count: events.length - 20 }) : t('detail.eventsOnly')}
                </span>
                <Link to="/activity" className="link flex min-h-touch items-center gap-0.5">
                  {t('detail.viewAllActivity')} <ArrowRight size={12} />
                </Link>
              </div>
            </>
          )}
        </Panel>
      </div>
    </>
  )
}
