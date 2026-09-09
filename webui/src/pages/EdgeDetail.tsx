import { useMemo } from 'react'
import { Link, useParams } from 'react-router'
import { useQuery } from '@tanstack/react-query'
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
 * 边缘节点详情：这台主机的连接事实 + 它名下所有设备的当前状态 + 相关事件。
 * 该节点离线时页面依然完整可读（历史事实与设备清单都在），不因为离线就变空白。
 */
export default function EdgeDetail() {
  const { edgeId = '' } = useParams()
  const id = decodeURIComponent(edgeId)
  const { list: edges, loading: edgeLoading, error: edgeError, refetch } = useEdges()
  const { list: devices } = useDevices()
  const liveEvents = useLive((s) => s.events)
  useNow() // 头部「连接于 X 前」每秒走字

  const facts = useMemo(() => edgeFacts(edges, devices), [edges, devices])
  const f = facts.find((x) => x.edge.edge_id === id)
  usePageTitle(f ? `网关 ${f.edge.edge_id}` : '网关')

  const { data: evHist, isLoading: evLoading, error: evError, refetch: refetchEvents } = useQuery({
    queryKey: ['edge-events', id],
    // /api/events 只接受 device 参数：按节点取最近事件后在前端按设备键前缀归属
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
        <BackLink to="/edges" label="网关" />
        <Panel><RowSkeleton rows={4} /></Panel>
      </>
    )
  }

  if (!f) {
    return (
      <>
        <BackLink to="/edges" label="网关" />
        {edgeError ? (
          <ErrorState icon={<Network size={20} />} title="网关信息加载失败"
            hint={`暂时无法加载网关列表，因此不能确认 ${id} 是否存在。请检查服务是否正常后重试。`}
            onRetry={refetch} />
        ) : (
        <EmptyState icon={<Network size={24} />} title="网关不存在"
          hint={`没有找到 ${id}。网关接入后会自动出现；若它曾长期离线且从未接入设备，可能不在记录里。`} />
        )}
      </>
    )
  }

  const e = f.edge
  return (
    <>
      <BackLink to="/edges" label="网关" />

      <header className="mb-7 flex flex-wrap items-center gap-3">
        <h1 className="min-w-0 max-w-full truncate font-mono text-[24px] font-semibold" title={e.edge_id}>
          {e.edge_id}
        </h1>
        <Badge tone={e.online ? 'ok' : 'idle'}>{e.online ? '在线' : '离线'}</Badge>
        {e.version && (
          <span className="min-w-0 truncate font-mono text-[11px] text-ink-3" title={`版本 ${e.version}`}>{e.version}</span>
        )}
        <span className="ml-auto text-xs text-ink-3"
          title={e.connected_at ? fmtDateTime(e.connected_at) : undefined}>
          {e.online ? '连接于 ' : '最后在线 '}
          {e.connected_at ? <span className="num">{timeAgo(e.connected_at)}</span> : '—'}
        </span>
      </header>

      {!e.online && (
        <div className="banner mb-5 rounded-lg" role="status">
          这个网关当前离线：下属设备暂停更新，网关离线期间不能下发操作，恢复连接后再试。其他在线网关不受影响。
        </div>
      )}

      <div className="grid items-start gap-5 lg:grid-cols-3">
        <Panel title={<span className="flex items-center gap-1.5"><Server size={14} />网关信息</span>}>
          <dl className="space-y-2.5">
            <KeyValue k="网关编号" v={e.edge_id} mono />
            <KeyValue k="版本" v={e.version || '未知'} mono />
            <KeyValue wrap k="最近更新"
              v={<span className="num font-mono">{f.lastReport ? fmtDateTime(f.lastReport) : '从未更新'}</span>} />
            <KeyValue k="接入设备" v={`${f.devices.length} 台 · ${f.onlineDevices} 台在线`} />
            {f.declared.length !== f.devices.length && <KeyValue k="已发现设备" v={`${f.declared.length} 台`} />}
          </dl>
          {f.declared.length !== f.devices.length && (
            <p className="mt-3 border-t border-hairline pt-3 text-[12px] leading-relaxed text-ink-3">
              已发现设备数与当前连接数不一致：可能设备在网关重启后未再被发现，或设备已移到其他网关。
            </p>
          )}
        </Panel>

        <Panel className="lg:col-span-2"
          title={<span className="flex items-center gap-1.5"><Cpu size={14} />接入设备</span>}
          right={<span className="text-[12px] text-ink-3">{f.onlineDevices}/{f.devices.length} 在线</span>}>
          {f.devices.length === 0 ? (
            <p className="py-8 text-center text-sm text-ink-3">该网关还没有接入设备。启动网关并接入设备后会自动显示在这里。</p>
          ) : (
            <ul className="divide-y divide-hairline">
              {f.devices.map((d) => {
                const dev = d.id.split('/').pop() ?? d.id
                return (
                  <li key={d.id} className="flex min-w-0 flex-col gap-2 py-3 sm:flex-row sm:items-center">
                    <Badge tone={d.online ? 'ok' : 'idle'} className="w-fit shrink-0">
                      {d.online ? '在线' : '离线'}
                    </Badge>
                    <Link to={`/devices/${encodeURIComponent(e.edge_id)}/${encodeURIComponent(dev)}`}
                      className="flex min-h-11 min-w-0 flex-1 flex-col justify-center no-underline">
                      <span className="block truncate text-[13px] font-medium hover:text-accent" title={deviceLabel(d)}>
                        <span className="sr-only">{d.online ? '在线，' : '离线，'}</span>
                        {deviceLabel(d)}
                      </span>
                      <span className="num block truncate font-mono text-[11px] text-ink-3" title={`${d.adapter || '未知设备类型'}${d.port ? ` · ${d.port}` : ''}`}>
                        {d.port ? `串口 ${d.port}` : '串口未报告'}
                      </span>
                    </Link>
                    <span className="num shrink-0 font-mono text-[11px] text-ink-3 sm:text-right"
                      title={d.online ? '最近更新' : '最后在线'}>
                      <span className="sm:hidden">最近上报 </span>
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
          title={<span className="flex items-center gap-1.5"><History size={14} />近期状态事件</span>}
          right={<span className="num text-[12px] text-ink-3">{events.length} 条</span>}>
          {evLoading && events.length === 0 ? (
            <RowSkeleton rows={5} />
          ) : evError && events.length === 0 ? (
            <ErrorState compact icon={<History size={20} />} title="状态事件加载失败"
              hint="暂时无法加载该网关的状态事件；设备状态仍以上方清单为准。请稍后重试。"
              onRetry={() => { void refetchEvents() }} />
          ) : events.length === 0 ? (
            <p className="py-8 text-center text-sm text-ink-3">该网关还没有状态事件。设备上报状态变化后会显示在这里；操作结果请在运行记录页查看。</p>
          ) : (
            <>
              <EventFeed events={events} limit={20} dayGrouped />
              <div className="mt-3 flex flex-wrap items-center justify-between gap-2 border-t border-hairline pt-3 text-xs">
                <span className="text-ink-3">
                  {events.length > 20 ? `另有 ${events.length - 20} 条` : '这里只展示状态事件'}
                </span>
                <Link to="/activity" className="link flex min-h-11 items-center gap-0.5">
                  查看全部运行记录 <ArrowRight size={12} />
                </Link>
              </div>
            </>
          )}
        </Panel>
      </div>
    </>
  )
}
