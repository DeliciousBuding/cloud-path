import { useMemo } from 'react'
import { Link, useParams } from 'react-router'
import { useQuery } from '@tanstack/react-query'
import { ArrowLeft, Cpu, History, Network, Server } from 'lucide-react'
import { Badge, EmptyState, KeyValue, Panel, StatusDot } from '@/components/ui'
import { RowSkeleton } from '@/components/Skeleton'
import { EventFeed } from '@/components/EventFeed'
import { api } from '@/lib/api'
import { useDevices } from '@/hooks/useDevices'
import { useEdges } from '@/hooks/useEdges'
import { useLive } from '@/store/ws'
import { deviceLabel, edgeFacts } from '@/lib/edges'
import { fmtDateTime, mergeEvents } from '@/lib/format'

/**
 * 边缘节点详情：这台主机的连接事实 + 它名下所有设备的当前状态 + 相关事件。
 * 该节点离线时页面依然完整可读（历史事实与设备清单都在），不因为离线就变空白。
 */
export default function EdgeDetail() {
  const { edgeId = '' } = useParams()
  const id = decodeURIComponent(edgeId)
  const { list: edges, loading: edgeLoading } = useEdges()
  const { list: devices } = useDevices()
  const liveEvents = useLive((s) => s.events)

  const facts = useMemo(() => edgeFacts(edges, devices), [edges, devices])
  const f = facts.find((x) => x.edge.edge_id === id)

  const { data: evHist, isLoading: evLoading } = useQuery({
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
        <BackLink />
        <Panel><RowSkeleton rows={4} /></Panel>
      </>
    )
  }

  if (!f) {
    return (
      <>
        <BackLink />
        <EmptyState icon={<Network size={24} />} title="边缘节点不存在"
          hint={`没有找到 ${id}。节点接入后会自动注册；若它曾长期离线且从未注册设备，可能不在记录里。`} />
      </>
    )
  }

  const e = f.edge
  return (
    <>
      <BackLink />

      <header className="mb-7 flex flex-wrap items-center gap-3 fade-up">
        <StatusDot online={e.online} />
        <h1 className="num min-w-0 max-w-full truncate text-[26px] font-bold tracking-tight" title={e.edge_id}>
          {e.edge_id}
        </h1>
        <Badge tone={e.online ? 'ok' : 'idle'}>{e.online ? '在线' : '离线'}</Badge>
        {e.version && <Badge tone="accent" className="max-w-full truncate">{e.version}</Badge>}
        <span className="num ml-auto text-xs text-ink-3">
          {e.online ? '连接于 ' : '最后在线 '}{e.connected_at ? fmtDateTime(e.connected_at) : '—'}
        </span>
      </header>

      {!e.online && (
        <div className="banner mb-5 rounded-xl fade-up" role="status">
          该节点当前离线：名下设备暂停上报，已下发命令会排队等它重连。其他在线节点不受影响。
        </div>
      )}

      <div className="grid items-start gap-5 lg:grid-cols-3">
        <Panel title={<span className="flex items-center gap-1.5"><Server size={14} />节点信息</span>}>
          <dl className="space-y-2.5">
            <KeyValue k="节点 ID" v={e.edge_id} mono />
            <KeyValue k="在线" v={e.online ? '是' : '否'} />
            <KeyValue k="版本" v={e.version || '未知'} mono />
            <KeyValue k={e.online ? '连接于' : '最后在线'}
              v={<span className="num">{e.connected_at ? fmtDateTime(e.connected_at) : '—'}</span>} />
            <KeyValue k="最近上报"
              v={<span className="num">{f.lastReport ? fmtDateTime(f.lastReport) : '从未上报'}</span>} />
            <KeyValue k="所辖设备" v={`${f.devices.length} 台 · ${f.onlineDevices} 台在线`} />
            <KeyValue k="接入时声明" v={`${f.declared.length} 台`} />
          </dl>
          {f.declared.length !== f.devices.length && (
            <p className="mt-3 border-t border-hairline pt-3 text-[11px] leading-relaxed text-ink-3">
              声明设备数与当前实际归属不一致：可能设备在节点重启后未再被发现，或有设备被移到别的节点。
            </p>
          )}
        </Panel>

        <Panel className="lg:col-span-2"
          title={<span className="flex items-center gap-1.5"><Cpu size={14} />所辖设备</span>}
          right={<span className="text-[11px] text-ink-3">{f.onlineDevices}/{f.devices.length} 在线</span>}>
          {f.devices.length === 0 ? (
            <p className="py-8 text-center text-sm text-ink-3">该节点还没有注册设备</p>
          ) : (
            <ul className="divide-y divide-hairline">
              {f.devices.map((d) => {
                const dev = d.id.split('/').pop() ?? d.id
                return (
                  <li key={d.id} className="flex min-w-0 items-center gap-3 py-2.5">
                    <StatusDot online={d.online} />
                    <Link to={`/devices/${encodeURIComponent(e.edge_id)}/${encodeURIComponent(dev)}`}
                      className="min-w-0 flex-1 no-underline">
                      <span className="block truncate text-[13px] font-medium hover:text-accent" title={deviceLabel(d)}>
                        {deviceLabel(d)}
                      </span>
                      <span className="num block truncate text-[11px] text-ink-3" title={d.id}>
                        {d.adapter || '未知适配器'}{d.port ? ` · ${d.port}` : ''}
                      </span>
                    </Link>
                    <span className="num shrink-0 text-[11px] text-ink-3"
                      title={d.online ? '最近更新' : '最后见'}>
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
          title={<span className="flex items-center gap-1.5"><History size={14} />该节点近期事件</span>}
          right={<span className="num text-[11px] text-ink-3">{events.length} 条</span>}>
          {evLoading && events.length === 0 ? (
            <RowSkeleton rows={5} />
          ) : events.length === 0 ? (
            <p className="py-8 text-center text-sm text-ink-3">该节点还没有事件记录</p>
          ) : (
            <EventFeed events={events} limit={60} fullTime />
          )}
        </Panel>
      </div>
    </>
  )
}

function BackLink() {
  return (
    <Link to="/edges"
      className="mb-5 inline-flex items-center gap-1 text-sm text-ink-2 transition-colors hover:text-accent fade-up">
      <ArrowLeft size={15} /> 边缘节点
    </Link>
  )
}