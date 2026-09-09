import { useMemo } from 'react'
import { Link } from 'react-router'
import { useQuery } from '@tanstack/react-query'
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

function fmtUptime(seconds: unknown): string | null {
  if (typeof seconds !== 'number' || !Number.isFinite(seconds) || seconds < 0) return null
  if (seconds < 60) return '服务刚刚启动'
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) return `服务已运行 ${minutes} 分钟`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `服务已运行 ${hours} 小时`
  return `服务已运行 ${Math.floor(hours / 24)} 天`
}

/**
 * 概览首屏按三段组织：现在怎样 → 需要关注 → 去哪里处理。
 *
 * 数据边界不变：计数、离线设备、失败操作和近期记录优先取聚合读面；聚合读面缺席时
 * 只使用设备和网关列表的真实字段降级，任何数字都不在前端编造。
 */
export default function Overview() {
  usePageTitle('概览')
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
      label: s.key === 'plugins' ? '应用正常' : s.key === 'commands' ? '失败操作' : s.label,
      emptyHint: s.key === 'devices' ? '等待网关接入设备'
        : s.key === 'edges' ? '尚未有网关注册'
          : s.key === 'plugins' ? '还没有应用'
            : s.key === 'commands' ? '24 小时内没有失败或超时' : s.emptyHint,
    }))
    : null

  const devOk = !devLoading && !devError
  const edgesOk = !edges.loading && !edges.error
  // 首帧仍在读取聚合状态时，不用空列表提前渲染 0/0；列表通道失败或聚合失败后才降级。
  const fallbackStats: OverviewStat[] = !serverOk && !loading ? [
    ...(devOk ? [{
      key: 'devices' as const, label: '在线设备',
      online: devices.filter((d) => d.online).length, total: devices.length,
      emptyHint: '等待网关接入设备',
      tone: (devices.length === 0 ? 'idle' : devices.some((d) => d.online) ? 'ok' : 'bad') as Tone,
    }] : []),
    ...(edgesOk ? [{
      key: 'edges' as const, label: '在线网关',
      online: edges.online, total: edges.list.length,
      emptyHint: '尚未有网关注册',
      tone: (edges.list.length === 0 ? 'idle' : edges.online === 0 ? 'bad' : 'ok') as Tone,
    }] : []),
  ] : []
  const shownStats: OverviewStat[] | null = stats ?? (fallbackStats.length ? fallbackStats : null)

  const alerts: OverviewAlert[] = data
    ? overviewAlerts(data).map((a) => ({
      ...a,
      title: a.id === 'edges-offline' ? '网关连接中断'
        : a.id === 'devices-offline' ? '部分设备离线'
          : a.id === 'commands-failed' ? '有操作未完成'
            : a.id === 'plugins-gap' ? '应用尚未就绪' : a.title,
      hint: a.id === 'edges-offline'
        ? '检查网关电源和网络；设备会在网关恢复后继续更新。'
        : a.id === 'devices-offline' ? '查看最后在线时间，确认设备供电和连接。'
          : a.id === 'commands-failed' ? '查看失败原因和发生时间，必要时重新操作。'
            : a.id === 'plugins-gap' ? '确认运行位置是否在线；应用会在条件恢复后继续应用设置。'
              : a.hint,
    }))
    : []

  const fallbackAlerts: OverviewAlert[] = !serverOk ? [
    ...devices.filter((d) => !d.online).map((d): OverviewAlert => {
      const [edgeId, devId] = d.id.split('/')
      return {
        id: `dev-offline-${d.id}`, tone: 'warn', count: 1,
        to: `/devices/${encodeURIComponent(edgeId ?? '')}/${encodeURIComponent(devId ?? '')}`,
        title: `${deviceShortName(d)} 离线`, hint: '查看最后在线时间，确认设备供电和连接。',
      }
    }),
    ...edges.list.filter((e) => !e.online).map((e): OverviewAlert => ({
      id: `edge-offline-${e.edge_id}`, tone: 'bad', count: 1,
      to: `/edges/${encodeURIComponent(e.edge_id)}`,
      title: `网关 ${e.edge_id} 离线`, hint: '检查网关电源和网络。',
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
  let nowTitle = '正在读取状态'
  let nowDetail = '正在获取设备与网关的最新状态。'
  let NowIcon = Activity

  if (!hasStats && !stillLoading) {
    nowTone = 'bad'
    nowTitle = '状态暂时不可用'
    nowDetail = '设备与网关状态没有加载出来。请重新加载；下方运行记录仍会独立加载。'
    NowIcon = WifiOff
  } else if (hasStats && attention > 0) {
    nowTone = attentionRows.some((a) => a.tone === 'bad') ? 'bad' : 'warn'
    nowTitle = `有 ${attention} 项需要处理`
    nowDetail = '先从下方「需要关注」开始，每一项都能直接前往对应页面。'
    NowIcon = AlertTriangle
  } else if (hasStats && (deviceStat?.total ?? 0) === 0) {
    nowTone = 'idle'
    nowTitle = '等待设备接入'
    nowDetail = '先接入并启动网关，设备上线后这里会显示最新状态。'
    NowIcon = Inbox
  } else if (hasStats) {
    nowTone = 'ok'
    nowTitle = '运行正常'
    nowDetail = '设备、网关和应用没有需要立即处理的异常。'
    NowIcon = CheckCircle2
  }

  const subtitle = data?.server_time
    ? `更新于 ${timeAgo(data.server_time)}`
    : fmtUptime(health?.uptime_s) ?? (health ? '服务状态正常' : '设备与网关的最新状态')

  return (
    <>
      <PageHeader
        title="概览"
        subtitle={subtitle}
        actions={
          <button
            type="button"
            className="btn btn-ghost"
            onClick={() => { void refetch(); void refetchDevices() }}
            disabled={isFetching}
          >
            {isFetching ? <Spinner size={13} /> : <RefreshCw size={13} />} 刷新
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
            <p className="text-[12px] font-medium text-ink-3">现在怎样</p>
            <div className="mt-3 flex items-start gap-3">
              <span className={cn('flex h-8 w-8 shrink-0 items-center justify-center rounded-full sm:h-9 sm:w-9', TONE_CLS[nowTone])}>
                <NowIcon size={18} strokeWidth={2} />
              </span>
              <div className="min-w-0">
                <h2 id="overview-now-title" className={cn('text-[20px] font-semibold leading-tight tracking-[-0.01em] sm:text-[22px]', TONE_TEXT_CLS[nowTone])}>
                  {nowTitle}
                </h2>
                <p className="mt-1.5 max-w-[58ch] text-sm text-ink-2">{nowDetail}</p>
              </div>
            </div>

            <div className="mt-4 flex flex-wrap items-center gap-2">
              {!hasStats && !stillLoading ? (
                <button type="button" className="btn btn-primary" onClick={() => { void refetch(); void refetchDevices() }} disabled={isFetching}>
                  {isFetching ? <Spinner size={13} /> : <RefreshCw size={13} />} 重新加载
                </button>
              ) : attention > 0 ? (
                <a href="#attention" className="btn btn-primary">查看需要处理 <ArrowDown size={14} /></a>
              ) : (deviceStat?.total ?? 0) === 0 && hasStats ? (
                <Link to="/edges" className="btn btn-primary">前往网关 <ArrowRight size={14} /></Link>
              ) : hasStats ? (
                <Link to="/devices" className="btn btn-primary">查看设备 <ArrowRight size={14} /></Link>
              ) : null}
            </div>

            {partial && (
              <p className="mt-4 flex flex-wrap items-center gap-1.5 text-[12px] text-ink-3">
                <AlertTriangle size={12} className="shrink-0 text-warn" />
                部分状态暂不可用，当前显示设备和网关的最新结果。
                <button type="button" className="link" onClick={() => void refetch()}>重新加载</button>
              </p>
            )}
          </div>

          {hasStats && (
            <div className="border-t border-hairline bg-surface-2/60 p-4 sm:p-5 lg:border-l lg:border-t-0">
              <p className="text-[12px] font-medium text-ink-3">关键状态</p>
              <div className="mt-3 grid grid-cols-2 gap-x-4 gap-y-3">
                {shownStats?.map((s) => {
                  const value = s.key === 'commands' ? s.online : `${s.online}/${s.total}`
                  const hint = s.key === 'devices' || s.key === 'edges'
                    ? s.total === 0 ? s.emptyHint : `${Math.max(0, s.total - s.online)} 台未在线`
                    : s.key === 'plugins'
                      ? s.total === 0 ? s.emptyHint : s.online === s.total ? '全部正常' : `${s.total - s.online} 个未运行`
                      : s.online === 0 ? '没有失败或超时' : '24 小时内需要查看'
                  return (
                    <div key={s.key} className="min-w-0 border-b border-hairline pb-2 last:border-b-0">
                      <div className="flex items-center justify-between gap-2">
                        <span className="min-w-0 truncate text-[12px] text-ink-3">{s.label}</span>
                        <span className={cn('num shrink-0 text-[18px] font-semibold leading-none', TONE_TEXT_CLS[s.tone])}>{value}</span>
                      </div>
                      <p className="mt-1 truncate text-[11px] text-ink-3" title={hint}>{hint}</p>
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
                  <p className="text-[12px] text-ink-3">下一步</p>
                  <h2 className="mt-0.5 flex items-center gap-1.5 text-[15px] font-semibold tracking-[-0.01em]">
                    <AlertTriangle size={14} className="text-warn" /> 需要关注
                  </h2>
                </div>
                {attention > 0 && <Badge tone="warn">{attentionCategories} 类 · {attention} 项</Badge>}
              </div>

              {attentionRows.length === 0 ? (
                !hasStats && !stillLoading ? (
                  <div className="flex flex-wrap items-center gap-x-3 gap-y-2 px-4 py-4 sm:px-5">
                    <AlertTriangle size={16} className="shrink-0 text-warn" />
                    <p className="min-w-0 flex-1 text-sm text-ink-2">状态不可用，暂时无法判断是否需要处理。</p>
                    <button type="button" className="link shrink-0 text-xs" onClick={() => { void refetch(); void refetchDevices() }}>
                      重新检查
                    </button>
                  </div>
                ) : (
                  <div className="flex flex-wrap items-center gap-x-3 gap-y-2 px-4 py-4 sm:px-5">
                    <CheckCircle2 size={16} className="shrink-0 text-ok" />
                    <p className="min-w-0 flex-1 text-sm text-ink-2">当前没有需要处理的异常。</p>
                    <Link to="/activity" className="link flex shrink-0 items-center gap-0.5 text-xs">
                      查看运行记录 <ArrowRight size={12} />
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
                        <span className={cn('h-2 w-2 shrink-0 rounded-full',
                          a.tone === 'bad' ? 'bg-bad' : a.tone === 'warn' ? 'bg-warn' : 'bg-ink-3')} />
                        <span className="min-w-0 flex-1">
                          <span className="block truncate text-sm font-medium">{a.title}{a.count > 1 ? ` · ${a.count} 项` : ''}</span>
                          <span className="mt-0.5 hidden truncate text-[12px] text-ink-3 sm:block">{a.hint}</span>
                        </span>
                        <span className="shrink-0 text-xs font-medium text-accent">去处理</span>
                        <ArrowRight size={13} className="shrink-0 text-ink-3 transition-transform group-hover:translate-x-0.5" />
                      </Link>
                    </li>
                  ))}
                </ul>
              )}
            </section>

            {hasStats && (
              <Panel
                title={<span className="flex items-center gap-1.5"><Cpu size={14} />设备状态</span>}
                right={
                  <Link to="/devices" className="link flex items-center gap-0.5 text-xs">
                    查看全部设备 <ArrowRight size={12} />
                  </Link>
                }
              >
                {devLoading ? (
                  <RowSkeleton rows={3} />
                ) : devError ? (
                  <ErrorState
                    plain compact
                    icon={<WifiOff size={20} />}
                    title="设备状态暂时不可用"
                    hint="上方状态仍然可用。请稍后重新加载设备列表。"
                    onRetry={refetchDevices}
                    retryLabel="重新加载设备"
                  />
                ) : devices.length === 0 ? (
                  <EmptyState
                    plain compact
                    icon={<Inbox size={24} />}
                    title="还没有设备接入"
                    hint="启动网关并完成设备接入后，设备会自动出现在这里。"
                    action={<Link to="/edges" className="btn btn-ghost">前往网关 <ArrowRight size={13} /></Link>}
                  />
                ) : (
                  <ul className="m-0 list-none p-0">
                    <DeviceRowHead />
                    {devices.slice(0, 8).map((d) => <DeviceRow key={d.id} d={d} />)}
                  </ul>
                )}
                {devices.length > 8 && (
                  <Link to="/devices" className="link mt-3 flex items-center gap-0.5 border-t border-hairline pt-3 text-xs">
                    另有 {devices.length - 8} 台 · 查看全部 <ArrowRight size={12} />
                  </Link>
                )}
              </Panel>
            )}

            <Panel
              title={<span className="flex items-center gap-1.5"><Activity size={14} />最近运行记录</span>}
              right={
                <Link to="/activity" className="link flex items-center gap-0.5 text-xs">
                  查看全部 <ArrowRight size={12} />
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
                    title="暂无运行记录"
                    hint="设备状态变化或操作结果会显示在这里。"
                    action={<Link to="/devices" className="btn btn-ghost">查看设备 <ArrowRight size={13} /></Link>}
                  />
                ) : (
                  <p className="py-8 text-center text-sm text-ink-3">运行记录暂时不可用，恢复后会自动出现。</p>
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
