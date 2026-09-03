import { useMemo, useState } from 'react'
import { Link } from 'react-router'
import { ArrowRight, Cpu, Network, PowerOff, Server } from 'lucide-react'
import { Badge, EmptyState, KeyValue, PageHeader, Panel, Segmented, StatusDot } from '@/components/ui'
import { RowSkeleton } from '@/components/Skeleton'
import { useDevices } from '@/hooks/useDevices'
import { useEdges } from '@/hooks/useEdges'
import { deviceLabel, edgeFacts, filterEdgeFacts, sortEdgeFacts, type EdgeFilter } from '@/lib/edges'
import { fmtDateTime } from '@/lib/format'

/**
 * 边缘节点列表：一眼看出「哪台电脑在线、哪台掉线」。
 *
 * 关键行为：掉线的 Edge 依然完整渲染（版本、最后在线、所辖设备都在），
 * 只是语义色转灰并给出「不影响其他节点」的系统级说明 —— 一台掉线不牵连其他台的呈现。
 */
export default function Edges() {
  const { list: edges, online, loading: edgeLoading } = useEdges()
  const { list: devices } = useDevices()
  const [filter, setFilter] = useState<EdgeFilter>('all')

  const facts = useMemo(() => sortEdgeFacts(edgeFacts(edges, devices)), [edges, devices])
  const shown = useMemo(() => filterEdgeFacts(facts, filter), [facts, filter])
  const offlineCount = facts.filter((f) => !f.edge.online).length

  return (
    <>
      <PageHeader
        title="边缘节点"
        subtitle={edgeLoading ? '正在加载…' : `${online}/${edges.length} 台在线 · 分布在 ${edges.length} 台主机上`}
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
        <div className="banner mb-5 rounded-xl fade-up" role="status">
          <PowerOff size={13} className="shrink-0" />
          <span className="min-w-0 break-words">
            {offlineCount} 台边缘节点离线：这些节点上的设备暂停上报，已下发命令排队等重连；其余在线节点不受影响。
          </span>
        </div>
      )}

      {edgeLoading ? (
        <Panel><RowSkeleton rows={3} /></Panel>
      ) : edges.length === 0 ? (
        <EmptyState icon={<Network size={24} />} title="没有边缘节点"
          hint="在接入主机上启动 cloudpath-edge（读取 edge.yaml）即会自动注册到这里；离线节点也会保留记录。" />
      ) : shown.length === 0 ? (
        <EmptyState icon={<Network size={24} />}
          title={filter === 'online' ? '当前没有在线的边缘节点' : '当前没有离线的边缘节点'}
          hint={filter === 'online'
            ? '全部节点都已离线。检查各主机的 cloudpath-edge 进程与网络后会自动重连。'
            : '所有节点都在线。'} />
      ) : (
        <div className="grid gap-4 lg:grid-cols-2">
          {shown.map((f) => {
            const e = f.edge
            return (
              <Panel key={e.edge_id} className="fade-up">
                {/* 390px：edge_id 由后端给定，长度不可控 —— 必须可截断，否则撑出横向滚动 */}
                <div className="flex min-w-0 items-center gap-2">
                  <StatusDot online={e.online} />
                  <Link to={`/edges/${encodeURIComponent(e.edge_id)}`}
                    className="num min-w-0 flex-1 truncate text-[15px] font-semibold tracking-tight no-underline hover:text-accent"
                    title={`${e.edge_id} · 查看详情`}>
                    {e.edge_id}
                  </Link>
                  <span className="shrink-0">
                    <Badge tone={e.online ? 'ok' : 'idle'}>{e.online ? '在线' : '离线'}</Badge>
                  </span>
                </div>

                <dl className="mt-4 space-y-2.5">
                  <KeyValue k="版本" v={e.version || '未知'} mono />
                  <KeyValue
                    k={e.online ? '连接于' : '最后在线'}
                    v={<span className="num">{e.connected_at ? fmtDateTime(e.connected_at) : '—'}</span>}
                  />
                  <KeyValue k="所辖设备"
                    v={e.online
                      ? `${f.devices.length} 台 · ${f.onlineDevices} 台在线`
                      : `${f.devices.length} 台（暂停上报）`} />
                  <KeyValue k="最近上报"
                    v={<span className="num">{f.lastReport ? fmtDateTime(f.lastReport) : '从未上报'}</span>} />
                </dl>

                <div className="mt-4 border-t border-hairline pt-3">
                  <p className="mb-2 flex items-center gap-1 text-[11px] text-ink-3">
                    <Cpu size={11} /> 设备
                  </p>
                  {f.devices.length === 0 ? (
                    <span className="text-xs text-ink-3">该节点还没有注册设备</span>
                  ) : (
                    <div className="flex flex-wrap gap-1.5">
                      {f.devices.slice(0, 8).map((d) => {
                        const dev = d.id.split('/').pop() ?? d.id
                        return (
                          // 设备键长度由后端决定：胶囊自身限宽，内部文本截断，状态点不收缩
                          <Link key={d.id}
                            to={`/devices/${encodeURIComponent(e.edge_id)}/${encodeURIComponent(dev)}`}
                            className={`badge max-w-full border transition-colors hover:bg-accent/10 hover:text-accent ${d.online ? 'border-hairline bg-ink-3/10 text-ink-2' : 'border-hairline bg-surface-2 text-ink-3'}`}
                            title={`${deviceLabel(d)} · ${d.online ? '在线' : '离线'}`}>
                            <StatusDot online={d.online} />
                            <span className="min-w-0 truncate">{deviceLabel(d)}</span>
                            <ArrowRight size={10} className="shrink-0" />
                          </Link>
                        )
                      })}
                      {f.devices.length > 8 && (
                        <Link to={`/edges/${encodeURIComponent(e.edge_id)}`}
                          className="badge max-w-full bg-ink-3/10 text-ink-2 hover:text-accent">
                          另有 {f.devices.length - 8} 台
                        </Link>
                      )}
                    </div>
                  )}
                </div>

                <Link to={`/edges/${encodeURIComponent(e.edge_id)}`}
                  className="link mt-4 flex items-center gap-0.5 border-t border-hairline pt-3 text-xs">
                  <Server size={11} /> 节点详情 <ArrowRight size={12} />
                </Link>
              </Panel>
            )
          })}
        </div>
      )}
    </>
  )
}