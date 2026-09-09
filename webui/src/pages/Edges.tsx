import { useMemo, useState } from 'react'
import { Link } from 'react-router'
import { Network, WifiOff } from 'lucide-react'
import { Badge, EmptyState, ErrorState, PageHeader, Panel, Segmented } from '@/components/ui'
import { RowSkeleton } from '@/components/Skeleton'
import { useDevices } from '@/hooks/useDevices'
import { useEdges } from '@/hooks/useEdges'
import { usePageTitle } from '@/hooks/usePageTitle'
import { edgeFacts, filterEdgeFacts, sortEdgeFacts, type EdgeFacts, type EdgeFilter } from '@/lib/edges'
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
  const subtitle = edgeLoading ? '正在加载网关状态…'
    : error ? '网关状态暂不可用'
      : edges.length === 0 ? '还没有网关注册'
        : offlineCount > 0 ? `${online} 台在线 · ${offlineCount} 台离线（设备暂停更新）`
          : `${online} 台在线 · 全部在线`

  return (
    <>
      <PageHeader
        title="网关"
        subtitle={subtitle}
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

const ROW_COLS = 'lg:grid-cols-[minmax(0,1.4fr)_4.5rem_5rem_minmax(0,1.2fr)_10.5rem_2rem]'

/** 列表表头（仅桌面；窄屏每行自带列名） */
function EdgeRowHead() {
  return (
    <li aria-hidden className={`hidden gap-x-4 px-4 pb-2 text-micro font-medium text-ink-3 lg:grid ${ROW_COLS}`}>
      <span>网关</span><span>状态</span><span>版本</span><span>设备</span>
      <span className="text-right">最近上报</span><span />
    </li>
  )
}

function EdgeRow({ f }: { f: EdgeFacts }) {
  const e = f.edge
  return (
    <li className={`grid gap-x-4 gap-y-1.5 border-b border-hairline px-4 py-2.5 last:border-b-0 ${ROW_COLS}`}>
      {/* 节点 ID 是运维标识：mono；点击进详情 */}
      <div className="flex min-w-0 items-center gap-2">
        <Link to={`/edges/${encodeURIComponent(e.edge_id)}`}
          className="inline-flex min-h-touch min-w-0 items-center truncate font-mono text-compact font-medium no-underline hover:text-accent sm:min-h-0"
          title={`${e.edge_id} · 查看详情`}>
          {e.edge_id}
        </Link>
      </div>
      <div className="hidden lg:block">
        <Badge tone={e.online ? 'ok' : 'idle'}>{e.online ? '在线' : '离线'}</Badge>
      </div>
      <div className="hidden min-w-0 truncate font-mono text-micro text-ink-2 lg:block" title={`版本 ${e.version || '未知'}`}>
        {e.version || '未知'}
      </div>
      <div className="flex min-w-0 flex-wrap items-center gap-1.5">
        <span className="shrink-0 lg:hidden">
          <Badge tone={e.online ? 'ok' : 'idle'}>{e.online ? '在线' : '离线'}</Badge>
        </span>
        {f.devices.length === 0 ? (
          <span className="text-meta text-ink-3">还没有接入设备</span>
        ) : (
          <span className="num shrink-0 text-meta text-ink-2"
            title={`${f.devices.length} 台设备 · ${f.onlineDevices} 台在线${e.online ? '' : '（网关离线，设备暂停更新）'}`}>
            {f.devices.length} 台设备 · {f.onlineDevices} 在线
            {f.devices.length > f.onlineDevices && (
              <span className="text-warn"> · {f.devices.length - f.onlineDevices} 台离线</span>
            )}
          </span>
        )}
      </div>
      <div className="num min-w-0 truncate text-left font-mono text-micro text-ink-3 lg:text-right"
        title={f.lastReport ? fmtDateTime(f.lastReport) : '从未更新'}>
        <span className="lg:hidden">最近上报 </span>
        {f.lastReport ? fmtDateTime(f.lastReport) : '从未更新'}
      </div>
      <Link to={`/edges/${encodeURIComponent(e.edge_id)}`}
        className="link hidden justify-self-end text-meta lg:block"
        aria-label={`查看网关 ${e.edge_id}`}>
        查看
      </Link>
    </li>
  )
}
