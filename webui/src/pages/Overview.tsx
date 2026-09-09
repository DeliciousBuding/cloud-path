import { useMemo } from 'react'
import { Link } from 'react-router'
import {
  Activity, AlertTriangle, ArrowRight, Boxes, CheckCircle2, Cpu, History, Inbox, Network,
  WifiOff, XCircle,
} from 'lucide-react'
import { Badge, EmptyState, ErrorState, Panel, PageHeader, StatTile } from '@/components/ui'
import { DeviceRow, DeviceRowHead } from '@/components/DeviceRow'
import { RowSkeleton, StatSkeleton } from '@/components/Skeleton'
import { EventFeed } from '@/components/EventFeed'
import { api } from '@/lib/api'
import { useQuery } from '@tanstack/react-query'
import { deviceShortName, overviewAlerts, overviewStats, type OverviewAlert, type OverviewStat } from '@/lib/overview'
import { fmtDateTime, mergeEvents, timeAgo } from '@/lib/format'
import { cn } from '@/lib/cn'
import { useNow } from '@/hooks/useNow'
import { usePageTitle } from '@/hooks/usePageTitle'
import { useDevices } from '@/hooks/useDevices'
import { useEdges } from '@/hooks/useEdges'
import { useOverview } from '@/hooks/useOverview'
import { useLive } from '@/store/ws'

/**
 * 概览（产品级首屏）：一屏回答四件事——系统健康吗 / 多少设备在线 / 有什么要我处理 / 最近发生了什么。
 *
 * 数据来源边界（禁止假数据）：
 *   - 计数 / 离线设备 / 失败命令 / 近期运行记录 → GET /api/overview 的服务端聚合（SSOT）；
 *   - 汇总数据缺席或失败（老版本 server 无该端点）→ 降级为设备/边缘列表通道实时计算，
 *     并显式标注来源；两条通道都拿不到才渲染 Error 态，绝不塞占位数字。
 *   - 设备 fleet → useDevices（WS 快照优先，REST 轮询兜底）；事件流 → 聚合 + WS 去重合并。
 */
export default function Overview() {
  usePageTitle('概览')

  useNow() // 页头「更新于 X 前」每秒走字（相对时间不靠轮询刷新）
  const { data, loading, isFetching, refetch } = useOverview()
  const { list: devices, loading: devLoading, error: devError, refetch: refetchDevices } = useDevices()
  const edges = useEdges()
  const liveEvents = useLive((s) => s.events)
  const status = useLive((s) => s.status)
  // 失败命令的展示名走声明（catalog 已由同页 EventFeed 拉取，命中同一 query key，不额外发请求）
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
      label: s.key === 'edges' ? '在线网关'
        : s.key === 'commands' ? '近24小时失败操作' : s.label,
      emptyHint: s.key === 'devices' ? '等待网关接入设备'
        : s.key === 'edges' ? '尚未有网关注册'
          : s.key === 'commands' ? '近24小时没有失败或超时的操作' : s.emptyHint,
    }))
    : null
  // 降级统计按通道独立落地：哪条列表通道可用就算哪一块，不编分母
  // （插件/失败命令无列表来源 → 不出现在降级态）；两条都失败才进 Error 态。
  const devOk = !devError
  const edgesOk = !edges.error
  const fallbackStats: OverviewStat[] = !serverOk ? [
    ...(devOk ? [{
      key: 'devices' as const, label: '在线设备',
      online: devices.filter((d) => d.online).length, total: devices.length,
      emptyHint: '等待网关接入设备',
      tone: (devices.length === 0 ? 'idle' : devices.some((d) => d.online) ? 'ok' : 'bad') as OverviewStat['tone'],
    }] : []),
    ...(edgesOk ? [{
      key: 'edges' as const, label: '在线网关',
      online: edges.online, total: edges.list.length,
      emptyHint: '尚未有网关注册',
      tone: (edges.list.length === 0 ? 'idle' : edges.online === 0 ? 'bad' : 'ok') as OverviewStat['tone'],
    }] : []),
  ] : []
  const shownStats: OverviewStat[] | null = stats ?? (fallbackStats.length ? fallbackStats : null)

  const alerts: OverviewAlert[] = data
    ? overviewAlerts(data).map((a) => ({
      ...a,
      title: a.id === 'edges-offline' ? a.title.replace('边缘节点', '网关')
        : a.id === 'commands-failed' ? a.title.replace('命令', '操作') : a.title,
      hint: a.id === 'edges-offline'
        ? '离线网关上的设备不会更新状态；已发送的操作会排队等待重连。其他在线网关不受影响。'
        : a.id === 'devices-offline' ? '这些设备最近一次更新后未再变化，点开可查看最后在线时间和运行记录。'
          : a.id === 'commands-failed' ? '查看失败或超时的操作及结果，核对原因和发生时间；运行记录页保留全部历史。'
            : a.id === 'plugins-gap' ? '变更已提交，但设备或应用还没有确认。它可能仍在处理中，也可能没有成功应用。'
              : a.hint,
    }))
    : []
  const offline = data?.offline_devices ?? []
  // 降级态的关注项：离线设备 + 离线边缘（列表通道真实字段）
  const fallbackAlerts: OverviewAlert[] = !serverOk ? [
    ...devices.filter((d) => !d.online).map((d): OverviewAlert => {
      const [edgeId, devId] = d.id.split('/')
      return {
        id: `dev-offline-${d.id}`, tone: 'warn', count: 1,
        to: `/devices/${encodeURIComponent(edgeId ?? '')}/${encodeURIComponent(devId ?? '')}`,
        title: `${deviceShortName(d)} 离线`, hint: '点开可查看最后在线时间和运行记录。',
      }
    }),
    ...edges.list.filter((e) => !e.online).map((e): OverviewAlert => ({
      id: `edge-offline-${e.edge_id}`, tone: 'bad', count: 1,
      to: `/edges/${encodeURIComponent(e.edge_id)}`,
      title: `网关 ${e.edge_id} 离线`, hint: '离线网关上的设备不会更新状态。',
    })),
  ] : []
  const attentionRows = [...alerts, ...fallbackAlerts]
  const attention = serverOk
    ? alerts.reduce((n, a) => n + a.count, 0)
    : fallbackAlerts.length

  return (
    <>
      <PageHeader
        title="概览"
        subtitle={
          data?.server_time
            ? <span title={fmtDateTime(data.server_time)}>更新于 {timeAgo(data.server_time)}</span>
            : health
              ? <>服务已运行 <span className="num">{Math.floor(health.uptime_s / 60)}</span> 分钟</>
              : '设备、网关与插件的实时总览'
        }
        actions={status === 'open'
          ? <Badge tone="ok">实时连接正常</Badge>
          : undefined}
      />

      {/* ---- KPI：服务端聚合优先；缺席降级列表通道并标注来源 ---- */}
      {loading && !data ? (
        <div className="grid grid-cols-2 gap-2.5 lg:grid-cols-4">
          <StatSkeleton /><StatSkeleton /><StatSkeleton /><StatSkeleton />
        </div>
      ) : shownStats ? (
        <>
          <div className="grid grid-cols-2 gap-2.5 lg:grid-cols-4">
            {shownStats.map((s) => (
              <StatTile
                key={s.key}
                icon={s.key === 'devices' ? <Cpu size={13} />
                  : s.key === 'edges' ? <Network size={13} />
                    : s.key === 'plugins' ? <Boxes size={13} /> : <XCircle size={13} />}
                label={s.label}
                value={s.total < 0 ? s.online : <>{s.online}<span className="text-ink-3">/{s.total}</span></>}
                sub={s.total === 0 || (s.total < 0 && s.online === 0) ? s.emptyHint : undefined}
              />
            ))}
          </div>
          {!serverOk && (
            <p className="mt-2 flex items-center gap-1 px-0.5 text-[12px] text-ink-3">
              <AlertTriangle size={11} className="shrink-0" />
              汇总数据暂不可用，以上数据来自设备和网关列表
              <button type="button" className="link ml-1 text-[12px]" onClick={() => void refetch()}>重新加载</button>
            </p>
          )}
        </>
      ) : (
        <ErrorState
          icon={<AlertTriangle size={20} />}
          title="概览数据加载失败"
          hint="汇总数据与设备/网关列表暂时都不可用。下面的实时连接状态仍独立维护，请检查服务是否正常后重试。"
          onRetry={() => { void refetch(); void refetchDevices() }}
          retrying={isFetching}
        />
      )}

      {/* ---- 主体：fleet + 关注并排；事件条通栏在下（宽屏不留死角） ---- */}
      {/* 单列 field：live 数据下任何双列 split 都会被两栏高差踢出画布空洞（空洞随数据搬家），
          reflow 成单列结构消灭；fleet 表格占有全证据宽（Vercel：tables own the full evidence width） */}
      <div className="mt-7 space-y-5">
        {attentionRows.length > 0 && (
        <Panel
          title={<span className="flex items-center gap-1.5"><AlertTriangle size={14} className="text-warn" />需要关注</span>}
          right={attention > 0
            ? <Badge tone="warn">{attention} 项</Badge>
            : <Badge tone="ok"><CheckCircle2 size={11} />无异常</Badge>}>
          {attentionRows.length === 0 ? (
            <p className="py-6 text-center text-sm text-ink-3">
              {serverOk || !loading ? '暂无异常' : '正在检查…'}
            </p>
          ) : (
            <ul className="divide-y divide-hairline">
              {attentionRows.map((a) => (
                <li key={a.id} className="py-2">
                  <Link to={a.to} className="flex min-w-0 items-center gap-2 no-underline transition-colors hover:text-accent"
                    title={a.hint}>
                    {/* 计数已在标题人话里（「N 条命令执行失败」）：行首只留语义色点，不重复数字 */}
                    <span className={cn('h-1.5 w-1.5 shrink-0 rounded-full',
                      a.tone === 'bad' ? 'bg-bad' : a.tone === 'warn' ? 'bg-warn' : 'bg-ink-3')} />
                    <span className="min-w-0 flex-1 truncate text-[12px] font-medium">{a.title}</span>
                    <ArrowRight size={12} className="shrink-0 text-ink-3" />
                  </Link>
                </li>
              ))}
            </ul>
          )}
          {serverOk && offline.length > 0 && (
            <Link to="/devices" className="link mt-3 flex items-center gap-0.5 border-t border-hairline pt-3 text-xs">
              {offline.length} 台离线设备明细 <ArrowRight size={12} />
            </Link>
          )}
        </Panel>
        )}
        {attentionRows.length === 0 && serverOk && !loading && (
          <p className="flex items-center gap-1.5 px-0.5 text-[12px] text-ink-3">
            <CheckCircle2 size={12} className="text-ok" />暂无异常
          </p>
        )}
        <Panel
          title={<span className="flex items-center gap-1.5"><Cpu size={14} />设备</span>}
          right={
            <Link to="/devices" className="link flex items-center gap-0.5 text-xs">
              全部设备 <ArrowRight size={12} />
            </Link>
          }>
          {devLoading ? (
            <RowSkeleton rows={3} />
          ) : devError ? (
            <ErrorState icon={<WifiOff size={20} />} title="设备状态加载失败"
              hint="暂时无法加载设备列表。概览统计与设备列表分开加载，上面的统计可能仍然可用。"
              onRetry={refetchDevices} compact />
          ) : devices.length === 0 ? (
            <EmptyState
              icon={<Inbox size={24} />}
              title="还没有设备接入"
              hint="启动网关并完成设备接入后，设备会自动出现在这里。"
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


        <Panel
          title={<span className="flex items-center gap-1.5"><Activity size={14} />近期运行记录</span>}
          right={
            <Link to="/activity" className="link flex items-center gap-0.5 text-xs">
              全部 <ArrowRight size={12} />
            </Link>
          }
        >
          {loading && feed.length === 0
            ? <RowSkeleton rows={6} />
            : feed.length === 0
              ? (serverOk
                ? <p className="flex flex-col items-center gap-2 py-10 text-center text-sm text-ink-3">
                  <History size={20} /> 暂无运行记录
                  <span className="text-[12px]">设备状态变化或操作结果会显示在这里。</span>
                </p>
                : <ErrorState icon={<History size={20} />} title="运行记录暂不可用"
                  hint="暂时无法加载历史运行记录；实时连接收到的新记录仍会显示。"
                  onRetry={() => void refetch()} compact />)
              : <EventFeed events={feed} limit={10} />}
        </Panel>
      </div>
    </>
  )
}
