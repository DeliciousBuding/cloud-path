import { useMemo } from 'react'
import { Link } from 'react-router'
import { useQuery } from '@tanstack/react-query'
import { Activity, Boxes, Cpu, Network, ArrowRight, Inbox } from 'lucide-react'
import { PageHeader, Panel, StatTile, EmptyState, Badge } from '@/components/ui'
import { DeviceCard } from '@/components/DeviceCard'
import { DeviceCardSkeleton, RowSkeleton, StatSkeleton } from '@/components/Skeleton'
import { EventFeed } from '@/components/EventFeed'
import { api } from '@/lib/api'
import { useLive } from '@/store/ws'
import { useDevices } from '@/hooks/useDevices'
import { useEdges } from '@/hooks/useEdges'
import { useNow } from '@/hooks/useNow'
import { fmtUptime, mergeEvents } from '@/lib/format'

function midnightTs(): number {
  const d = new Date()
  d.setHours(0, 0, 0, 0)
  return Math.floor(d.getTime() / 1000)
}

export default function Dashboard() {
  const { list, online, loading } = useDevices()
  const { list: edgeList, online: edgeOnline } = useEdges()
  const liveEvents = useLive((s) => s.events)
  const status = useLive((s) => s.status)
  const now = useNow(30_000)

  const { data: health } = useQuery({ queryKey: ['health'], queryFn: api.health, refetchInterval: 15000 })
  const { data: today, isLoading: todayLoading } = useQuery({
    queryKey: ['events-today'],
    queryFn: () => api.events({ since: midnightTs(), limit: 1000 }),
    refetchInterval: 30000,
  })
  // 近期事件（REST）与 WS 实时事件合并：刚打开页面也有内容，不必等下一条上报
  const { data: recent, isLoading: recentLoading } = useQuery({
    queryKey: ['events-recent'],
    queryFn: () => api.events({ limit: 30 }),
    refetchInterval: 15000,
  })
  const feed = useMemo(
    () => mergeEvents(liveEvents, recent?.events ?? []).slice(0, 14),
    [liveEvents, recent],
  )

  return (
    <>
      <PageHeader
        title="概览"
        subtitle={now.toLocaleDateString('zh-CN', { year: 'numeric', month: 'long', day: 'numeric', weekday: 'long' })}
        actions={
          <Badge tone={status === 'open' ? 'ok' : status === 'connecting' ? 'warn' : 'bad'}>
            {status === 'open' ? '实时连接正常' : status === 'connecting' ? '连接中…' : '连接断开'}
          </Badge>
        }
      />

      <div className="grid grid-cols-2 gap-4 lg:grid-cols-4">
        {loading ? (
          <>
            <StatSkeleton /><StatSkeleton /><StatSkeleton /><StatSkeleton />
          </>
        ) : (
          <>
            <StatTile icon={<Cpu size={13} />} label="在线设备"
              value={<>{online}<span className="text-ink-3">/{list.length}</span></>}
              sub={list.length === 0 ? '等待边缘节点接入' : undefined} />
            <StatTile icon={<Network size={13} />} label="边缘节点"
              value={<>{edgeOnline}<span className="text-ink-3">/{edgeList.length}</span></>}
              sub={edgeList.length === 0 ? '尚未有节点注册' : undefined} />
            <StatTile icon={<Activity size={13} />} label="今日事件"
              value={today ? (today.events.length >= 1000 ? '1000+' : today.events.length) : '—'}
              sub={todayLoading ? undefined : `自 ${now.toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit', hour12: false })} 起统计当日`} />
            <StatTile icon={<Boxes size={13} />} label="服务运行"
              value={health ? fmtUptime(health.uptime_s) : '—'}
              sub={health ? `server ${health.version}` : undefined} />
          </>
        )}
      </div>

      <div className="mt-7 grid items-start gap-6 xl:grid-cols-[minmax(0,1fr)_320px]">
        <div>
          <div className="mb-3 flex items-center justify-between px-1">
            <h2 className="text-[15px] font-semibold tracking-tight">设备</h2>
            <Link to="/devices" className="link flex items-center gap-0.5 text-xs">
              全部设备 <ArrowRight size={12} />
            </Link>
          </div>
          {loading ? (
            <div className="grid gap-4 sm:grid-cols-2 2xl:grid-cols-3">
              <DeviceCardSkeleton /><DeviceCardSkeleton />
            </div>
          ) : list.length === 0 ? (
            <EmptyState
              icon={<Inbox size={24} />}
              title="还没有设备接入"
              hint="在边缘主机上复制 edge.example.yaml 为 edge.yaml，填好串口后启动 cloudpath-edge，设备会自动出现在这里。"
            />
          ) : (
            <div className="grid gap-4 sm:grid-cols-2 2xl:grid-cols-3">
              {list.map((d) => <DeviceCard key={d.id} d={d} />)}
            </div>
          )}
        </div>

        <Panel
          title="实时事件"
          right={
            <Link to="/events" className="link flex items-center gap-0.5 text-xs">
              全部 <ArrowRight size={12} />
            </Link>
          }
          className="xl:sticky xl:top-8"
        >
          {recentLoading && feed.length === 0 ? <RowSkeleton rows={6} /> : <EventFeed events={feed} limit={14} />}
        </Panel>
      </div>
    </>
  )
}
