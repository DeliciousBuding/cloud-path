import { useMemo } from 'react'
import { Link } from 'react-router'
import {
  Activity, AlertTriangle, ArrowRight, Boxes, CheckCircle2, Cpu, History, Inbox, Network,
  PowerOff, XCircle,
} from 'lucide-react'
import { Badge, EmptyState, ErrorState, Panel, PageHeader, SectionTitle, StatTile } from '@/components/ui'
import { DeviceCard } from '@/components/DeviceCard'
import { DeviceCardSkeleton, RowSkeleton, StatSkeleton } from '@/components/Skeleton'
import { EventFeed } from '@/components/EventFeed'
import { api } from '@/lib/api'
import { useQuery } from '@tanstack/react-query'
import { deviceShortName, overviewAlerts, overviewStats } from '@/lib/overview'
import { cmdStatusMeta, fmtDateTime, mergeEvents } from '@/lib/format'
import { useDevices } from '@/hooks/useDevices'
import { useOverview } from '@/hooks/useOverview'
import { useLive } from '@/store/ws'

/**
 * 概览（产品级首屏）：一屏回答「我的设备现在怎么样、有什么要我处理」。
 *
 * 数据来源边界（禁止假数据）：
 *   - 计数 / 离线设备 / 失败命令 / 近期事件 → GET /api/overview 的服务端聚合结果；
 *   - 实时设备卡片 → useDevices（WS 快照优先，REST 轮询兜底）；
 *   - 事件流 → overview.recent_events 与 WS 实时事件按 设备+时间+类型 去重合并。
 * 任何一块拿不到就渲染设计过的 Empty / Error 态，绝不塞占位数字或 demo 卡片。
 */
export default function Overview() {
  const { data, loading, error, isFetching, refetch } = useOverview()
  const { list: devices, loading: devLoading } = useDevices()
  const liveEvents = useLive((s) => s.events)
  const status = useLive((s) => s.status)
  const { data: health } = useQuery({
    queryKey: ['health'], queryFn: api.health, refetchInterval: 30_000,
  })

  const feed = useMemo(
    () => mergeEvents(liveEvents, data?.recent_events ?? []).slice(0, 12),
    [liveEvents, data],
  )

  const stats = data ? overviewStats(data) : []
  const alerts = data ? overviewAlerts(data) : []
  // 有专属面板的两类不进 alert 行（避免同一件事说两遍）
  const bannerAlerts = alerts.filter((a) => a.id !== 'devices-offline' && a.id !== 'commands-failed')
  const offline = data?.offline_devices ?? []
  const failed = data?.failed_commands ?? []
  const attention = alerts.reduce((n, a) => n + a.count, 0)

  return (
    <>
      <PageHeader
        title="概览"
        subtitle={
          data?.server_time
            ? <>服务端聚合于 <span className="num">{fmtDateTime(data.server_time)}</span></>
            : health
              ? <>server {health.version} · <span className="num">运行 {Math.floor(health.uptime_s / 60)} 分钟</span></>
              : '设备、边缘节点与插件的实时总览'
        }
        actions={
          <>
            {attention > 0 && (
              <Badge tone="warn"><AlertTriangle size={11} />需要关注 {attention} 项</Badge>
            )}
            <Badge tone={status === 'open' ? 'ok' : status === 'connecting' ? 'warn' : 'bad'}>
              {status === 'open' ? '实时连接正常' : status === 'connecting' ? '连接中…' : '实时连接断开'}
            </Badge>
          </>
        }
      />

      {/* ---- 统计瓦片 ---- */}
      {error && !data ? (
        <ErrorState
          icon={<AlertTriangle size={20} />}
          title="概览数据加载失败"
          hint="拿不到服务端聚合结果（GET /api/overview）。下面的设备实时状态仍来自独立通道，可继续查看与控制。"
          onRetry={refetch}
          retrying={isFetching}
        />
      ) : loading ? (
        <div className="grid grid-cols-2 gap-4 lg:grid-cols-4">
          <StatSkeleton /><StatSkeleton /><StatSkeleton /><StatSkeleton />
        </div>
      ) : (
        <div className="grid grid-cols-2 gap-4 lg:grid-cols-4">
          {stats.map((s) => (
            <StatTile
              key={s.key}
              icon={s.key === 'devices' ? <Cpu size={13} />
                : s.key === 'edges' ? <Network size={13} />
                  : s.key === 'plugins' ? <Boxes size={13} /> : <XCircle size={13} />}
              label={s.label}
              // total=-1 表示「只有计数、没有分母」（失败命令）
              value={s.total < 0 ? s.online : <>{s.online}<span className="text-ink-3">/{s.total}</span></>}
              sub={s.total === 0 || (s.total < 0 && s.online === 0) ? s.emptyHint : undefined}
            />
          ))}
        </div>
      )}

      {/* ---- 需要关注（只在真有问题时出现，不占常驻版面） ---- */}
      {bannerAlerts.length > 0 && (
        <section className="mt-7">
          <SectionTitle icon={<AlertTriangle size={14} className="text-warn" />}>需要关注</SectionTitle>
          <div className="space-y-2.5">
            {bannerAlerts.map((a) => (
              <Link key={a.id} to={a.to}
                className="card card-lift flex min-w-0 items-start gap-3 p-4 no-underline fade-up">
                <span className={`mt-0.5 shrink-0 ${a.tone === 'bad' ? 'text-bad' : 'text-warn'}`}>
                  {a.tone === 'bad' ? <PowerOff size={16} /> : <AlertTriangle size={16} />}
                </span>
                <span className="min-w-0 flex-1">
                  <span className="block text-[14px] font-semibold">{a.title}</span>
                  <span className="mt-0.5 block text-xs leading-relaxed break-words text-ink-2">{a.hint}</span>
                </span>
                <ArrowRight size={14} className="mt-1 shrink-0 text-ink-3" />
              </Link>
            ))}
          </div>
        </section>
      )}

      {/* ---- 离线设备 / 失败命令 ---- */}
      {data && (
        <div className="mt-7 grid items-start gap-5 lg:grid-cols-2">
          <Panel
            title={<span className="flex items-center gap-1.5"><PowerOff size={14} />离线设备</span>}
            right={offline.length > 0
              ? <Badge tone="warn">{offline.length}</Badge>
              : <Badge tone="ok"><CheckCircle2 size={11} />全部在线</Badge>}
          >
            {offline.length === 0 ? (
              <p className="py-6 text-center text-sm text-ink-3">
                {data.devices_total === 0 ? '还没有设备接入' : '所有已注册设备都在上报状态'}
              </p>
            ) : (
              <ul className="divide-y divide-hairline">
                {offline.slice(0, 8).map((d) => (
                  <li key={d.id} className="flex min-w-0 items-center gap-3 py-2.5">
                    <Link to={`/devices/${encodeURIComponent(d.edge_id)}/${encodeURIComponent(d.id.split('/').pop() ?? '')}`}
                      className="min-w-0 flex-1 no-underline">
                      <span className="block truncate text-[13px] font-medium hover:text-accent" title={d.name || d.id}>
                        {deviceShortName(d)}
                      </span>
                      <span className="num block truncate text-[11px] text-ink-3" title={d.id}>
                        {d.edge_id} · 最后见 {d.last_seen ? fmtDateTime(d.last_seen) : '—'}
                      </span>
                    </Link>
                  </li>
                ))}
              </ul>
            )}
            {offline.length > 8 && (
              <Link to="/devices" className="link mt-3 flex items-center gap-0.5 border-t border-hairline pt-3 text-xs">
                另有 {offline.length - 8} 台 · 查看全部 <ArrowRight size={12} />
              </Link>
            )}
          </Panel>

          <Panel
            title={<span className="flex items-center gap-1.5"><XCircle size={14} />失败命令</span>}
            right={failed.length > 0
              ? <Badge tone="bad">{failed.length}</Badge>
              : <Badge tone="ok"><CheckCircle2 size={11} />无失败</Badge>}
          >
            {failed.length === 0 ? (
              <p className="py-6 text-center text-sm text-ink-3">
                {data.commands_failed === 0 ? '最近的命令都执行成功了' : '暂无失败命令明细'}
              </p>
            ) : (
              <ul className="divide-y divide-hairline">
                {failed.slice(0, 8).map((c) => {
                  const meta = cmdStatusMeta(c.status)
                  return (
                    <li key={c.id} className="flex min-w-0 items-center gap-3 py-2.5">
                      <span className="min-w-0 flex-1">
                        <span className="num block truncate font-mono text-[12px] font-medium" title={`${c.cmd} ${c.args}`}>
                          {c.cmd}{c.args ? ` ${c.args}` : ''}
                        </span>
                        <span className="num block truncate text-[11px] text-ink-3" title={c.device_id}>
                          {c.device_id} · {c.created_at ? fmtDateTime(c.created_at) : '—'}
                        </span>
                      </span>
                      <Badge tone={meta.tone} className="shrink-0">{meta.label}</Badge>
                    </li>
                  )
                })}
              </ul>
            )}
            {failed.length > 0 && (
              <Link to="/activity" className="link mt-3 flex items-center gap-0.5 border-t border-hairline pt-3 text-xs">
                命令历史 <ArrowRight size={12} />
              </Link>
            )}
          </Panel>
        </div>
      )}

      {/* ---- 实时设备状态 + 近期事件 ---- */}
      <div className="mt-7 grid items-start gap-6 xl:grid-cols-[minmax(0,1fr)_320px]">
        <div>
          <SectionTitle
            icon={<Cpu size={14} />}
            right={
              <Link to="/devices" className="link flex items-center gap-0.5 text-xs">
                全部设备 <ArrowRight size={12} />
              </Link>
            }
          >
            实时设备状态
          </SectionTitle>
          {devLoading ? (
            <div className="grid gap-4 sm:grid-cols-2 2xl:grid-cols-3">
              <DeviceCardSkeleton /><DeviceCardSkeleton />
            </div>
          ) : devices.length === 0 ? (
            <EmptyState
              icon={<Inbox size={24} />}
              title="还没有设备接入"
              hint="在边缘主机上复制 edge.example.yaml 为 edge.yaml，填好串口后启动 cloudpath-edge，设备会自动出现在这里。"
            />
          ) : (
            <div className="grid gap-4 sm:grid-cols-2 2xl:grid-cols-3">
              {devices.slice(0, 9).map((d) => <DeviceCard key={d.id} d={d} />)}
            </div>
          )}
          {!devLoading && devices.length > 9 && (
            <Link to="/devices" className="link mt-3 flex items-center gap-0.5 text-xs">
              另有 {devices.length - 9} 台设备 · 查看全部 <ArrowRight size={12} />
            </Link>
          )}
        </div>

        <Panel
          title={<span className="flex items-center gap-1.5"><Activity size={14} />近期事件</span>}
          right={
            <Link to="/activity" className="link flex items-center gap-0.5 text-xs">
              全部 <ArrowRight size={12} />
            </Link>
          }
          className="xl:sticky xl:top-8"
        >
          {loading && feed.length === 0
            ? <RowSkeleton rows={6} />
            : feed.length === 0
              ? <p className="flex flex-col items-center gap-2 py-10 text-center text-sm text-ink-3">
                <History size={20} /> 还没有事件上报
              </p>
              : <EventFeed events={feed} limit={12} />}
        </Panel>
      </div>
    </>
  )
}