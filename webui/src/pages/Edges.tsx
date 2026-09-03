import { Link } from 'react-router'
import { ArrowRight, Cpu, Network } from 'lucide-react'
import { PageHeader, Panel, EmptyState, Badge, StatusDot, KeyValue } from '@/components/ui'
import { RowSkeleton } from '@/components/Skeleton'
import { useEdges } from '@/hooks/useEdges'
import { timeAgo } from '@/lib/format'

export default function Edges() {
  const { list, online, loading } = useEdges()

  return (
    <>
      <PageHeader title="边缘节点"
        subtitle={loading ? '正在加载…' : `代理进程 · ${online}/${list.length} 在线`} />
      {loading ? (
        <Panel><RowSkeleton rows={3} /></Panel>
      ) : list.length === 0 ? (
        <EmptyState icon={<Network size={24} />} title="没有边缘节点"
          hint="在接入设备上启动 cloudpath-edge（读取 edge.yaml）即会自动注册到这里；离线节点也会保留记录。" />
      ) : (
        <div className="grid gap-4 md:grid-cols-2">
          {list.map((e) => (
            <Panel key={e.edge_id} className="fade-up">
              <div className="flex items-center gap-2">
                <StatusDot online={e.online} />
                <span className="num text-[15px] font-semibold tracking-tight">{e.edge_id}</span>
                <span className="ml-auto">
                  <Badge tone={e.online ? 'ok' : 'idle'}>{e.online ? '在线' : '离线'}</Badge>
                </span>
              </div>
              <dl className="mt-4 space-y-2.5">
                <KeyValue k="版本" v={e.version || '—'} mono />
                <KeyValue k={e.online ? '连接于' : '最后在线'} v={timeAgo(e.connected_at)} />
                <KeyValue k="所辖设备" v={`${e.devices?.length ?? 0} 台`} />
              </dl>
              <div className="mt-4 border-t border-hairline pt-3">
                <p className="mb-2 flex items-center gap-1 text-[11px] text-ink-3">
                  <Cpu size={11} /> 设备
                </p>
                <div className="flex flex-wrap gap-1.5">
                  {(e.devices ?? []).map((key) => {
                    const [eg, dev] = key.split('/')
                    return (
                      <Link key={key}
                        to={`/devices/${encodeURIComponent(eg ?? '')}/${encodeURIComponent(dev ?? '')}`}
                        className="badge bg-ink-3/10 text-ink-2 transition-colors hover:bg-accent/10 hover:text-accent"
                        title={key}>
                        {dev} <ArrowRight size={10} />
                      </Link>
                    )
                  })}
                  {!(e.devices ?? []).length && <span className="text-xs text-ink-3">无</span>}
                </div>
              </div>
            </Panel>
          ))}
        </div>
      )}
    </>
  )
}
