import { useMemo, useState } from 'react'
import { Link } from 'react-router'
import { Network, PowerOff, WifiOff } from 'lucide-react'
import { Badge, EmptyState, ErrorState, PageHeader, Panel, Segmented, StatusDot } from '@/components/ui'
import { RowSkeleton } from '@/components/Skeleton'
import { useDevices } from '@/hooks/useDevices'
import { useEdges } from '@/hooks/useEdges'
import { usePageTitle } from '@/hooks/usePageTitle'
import { deviceLabel, edgeFacts, filterEdgeFacts, sortEdgeFacts, type EdgeFacts, type EdgeFilter } from '@/lib/edges'
import { fmtDateTime } from '@/lib/format'

/**
 * 边缘节点列表：一眼看出「哪台电脑在线、哪台掉线」。
 *
 * 关键行为：掉线的 Edge 依然完整渲染（版本、最后在线、所辖设备都在），
 * 只是语义色转灰并给出「不影响其他节点」的系统级说明 —— 一台掉线不牵连其他台的呈现。
 */
export default function Edges() {
  usePageTitle('网关')

  const { list: edges, online, loading: edgeLoading, error, refetch } = useEdges()
  const { list: devices } = useDevices()
  const [filter, setFilter] = useState<EdgeFilter>('all')

  const facts = useMemo(() => sortEdgeFacts(edgeFacts(edges, devices)), [edges, devices])
  const shown = useMemo(() => filterEdgeFacts(facts, filter), [facts, filter])
  const offlineCount = facts.filter((f) => !f.edge.online).length

  return (
    <>
      <PageHeader
        title="网关"
        subtitle={edgeLoading ? '正在加载…'
          : edges.length === 0 ? '暂无网关'
            : `${online} 台在线 · 共 ${edges.length} 台`}
        actions={
          edges.length > 0 ? (
            <Segmented
              label="在线状态筛选"
              value={filter}
              onChange={setFilter}
              options={[
                { value: 'all', label: `全部 ${edges.length}` },
                { value: 'online', label: `在线 ${online}` },
                { value: 'offline', label: `离线 ${offlineCount}` },
              ]}
            />
          ) : undefined
        }
      />

      {/* 有节点掉线时给一次系统级说明（不在每张卡上重复，避免噪音） */}
      {offlineCount > 0 && (
        <div className="banner mb-5 rounded-lg fade-up" role="status">
          <PowerOff size={13} className="shrink-0" />
          <span className="min-w-0 break-words">
            {offlineCount} 个网关离线：这些网关上的设备暂停更新，已发送的操作排队等待重连；其他在线网关不受影响。
          </span>
        </div>
      )}

      {edgeLoading ? (
        <Panel><RowSkeleton rows={3} /></Panel>
      ) : error ? (
        <ErrorState icon={<WifiOff size={20} />} title="网关列表加载失败"
          hint="暂时无法加载网关列表。这不表示没有网关接入，请检查服务是否正常后重试。"
          onRetry={refetch} />
      ) : edges.length === 0 ? (
        <EmptyState icon={<Network size={24} />} title="还没有网关"
          hint="启动网关后，网关会自动出现在这里；离线网关也会保留记录。" />
      ) : shown.length === 0 ? (
        <EmptyState icon={<Network size={24} />}
          title={filter === 'online' ? '当前没有在线的网关' : '当前没有离线的网关'}
          hint={filter === 'online'
            ? '全部网关都已离线。检查各网关和网络后会自动重连。'
            : '所有网关都在线。'} />
      ) : (
        <>
        {/* 全宽行而非卡片网格：节点少时卡片会把内容困在窄轨里留下大片空白
            （Vercel：不要 strand content in a narrow track）；与设备舰队行同一语言 */}
        <ul className="m-0 list-none p-0">
          <EdgeRowHead />
          {shown.map((f) => <EdgeRow key={f.edge.edge_id} f={f} />)}
        </ul>
        </>
      )}
    </>
  )
}

const ROW_COLS = 'lg:grid-cols-[minmax(0,1.4fr)_4.5rem_5rem_minmax(0,1.7fr)_10.5rem_10.5rem]'

/** 列表表头（仅桌面；窄屏每行自带列名） */
function EdgeRowHead() {
  return (
    <li aria-hidden className={`hidden gap-x-4 px-4 pb-2 text-[11px] font-medium text-ink-3 lg:grid ${ROW_COLS}`}>
      <span>网关</span><span>状态</span><span>版本</span><span>设备</span>
      <span className="text-right">连接于</span><span className="text-right">最近更新</span>
    </li>
  )
}

function EdgeRow({ f }: { f: EdgeFacts }) {
  const e = f.edge
  return (
    <li className={`grid gap-x-4 gap-y-1.5 border-b border-hairline px-4 py-2.5 last:border-b-0 ${ROW_COLS}`}>
      {/* 节点 ID 是运维标识：mono；点击进详情 */}
      <div className="flex min-w-0 items-center gap-2">
        <StatusDot online={e.online} />
        <Link to={`/edges/${encodeURIComponent(e.edge_id)}`}
          className="min-w-0 truncate font-mono text-[13px] font-medium no-underline hover:text-accent"
          title={`${e.edge_id} · 查看详情`}>
          {e.edge_id}
        </Link>
      </div>
      <div className="hidden lg:block">
        <Badge tone={e.online ? 'ok' : 'idle'}>{e.online ? '在线' : '离线'}</Badge>
      </div>
      <div className="hidden min-w-0 truncate font-mono text-[11px] text-ink-2 lg:block" title={`版本 ${e.version || '未知'}`}>
        {e.version || '未知'}
      </div>
      <div className="flex min-w-0 flex-wrap items-center gap-1.5">
        <span className="shrink-0 lg:hidden">
          <Badge tone={e.online ? 'ok' : 'idle'}>{e.online ? '在线' : '离线'}</Badge>
        </span>
        {f.devices.length === 0 ? (
          <span className="text-[12px] text-ink-3">还没有接入设备</span>
        ) : (
          <>
            <span className="num shrink-0 text-[12px] text-ink-2"
              title={`${f.devices.length} 台 · ${f.onlineDevices} 台在线${e.online ? '' : '（暂停更新）'}`}>
              {f.devices.length} 台 · {f.onlineDevices} 在线
            </span>
            {f.devices.slice(0, 2).map((d) => {
              const dev = d.id.split('/').pop() ?? d.id
              return (
                <Link key={d.id}
                  to={`/devices/${encodeURIComponent(e.edge_id)}/${encodeURIComponent(dev)}`}
                  className={`badge max-w-[9rem] border transition-colors hover:bg-accent/10 hover:text-accent ${d.online ? 'border-hairline bg-ink-3/10 text-ink-2' : 'border-hairline bg-surface-2 text-ink-3'}`}
                  title={`${deviceLabel(d)} · ${d.online ? '在线' : '离线'}`}>
                  <StatusDot online={d.online} />
                  <span className="min-w-0 truncate">{deviceLabel(d)}</span>
                </Link>
              )
            })}
            {f.devices.length > 2 && (
              <Link to={`/edges/${encodeURIComponent(e.edge_id)}`}
                aria-label={`查看其余 ${f.devices.length - 2} 台设备`}
                title={`查看其余 ${f.devices.length - 2} 台设备`}
                className="badge max-w-full bg-ink-3/10 text-ink-2 hover:text-accent">
                +{f.devices.length - 2}
              </Link>
            )}
          </>
        )}
      </div>
      <div className="num min-w-0 truncate text-left font-mono text-[11px] text-ink-3 lg:text-right"
        title={e.connected_at ? fmtDateTime(e.connected_at) : '未连接'}>
        <span className="lg:hidden">{e.online ? '连接于 ' : '最后在线 '}</span>
        {e.connected_at ? fmtDateTime(e.connected_at) : '—'}
      </div>
      <div className="num min-w-0 truncate text-left font-mono text-[11px] text-ink-3 lg:text-right"
        title={f.lastReport ? fmtDateTime(f.lastReport) : '从未更新'}>
        <span className="lg:hidden">最近更新 </span>
        {f.lastReport ? fmtDateTime(f.lastReport) : '从未更新'}
      </div>
    </li>
  )
}
