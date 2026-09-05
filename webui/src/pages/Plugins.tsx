import { useMemo, useState } from 'react'
import { Link } from 'react-router'
import {
  Boxes, CloudOff, Layers, PackagePlus, PackageOpen, Plus, Puzzle, Server, ShieldCheck,
} from 'lucide-react'
import {
  Badge, EmptyState, ErrorState, PageHeader, Panel, StatusDot, TabBar, TabPanel,
} from '@/components/ui'
import { RowSkeleton } from '@/components/Skeleton'
import { InstanceForm } from '@/components/plugin/InstanceForm'
import { InstanceRow } from '@/components/plugin/InstanceRow'
import { PermissionList } from '@/components/plugin/PluginFacts'
import {
  usePluginCatalog, usePluginInstances,
} from '@/hooks/usePlugins'
import { groupByEdge, healthMeta, indexCatalog, shortDigest, stateMeta, trustMeta } from '@/lib/plugins'
import { fmtDateTime } from '@/lib/format'
import type { TabItem } from '@/components/ui'
import type { PluginInstanceView } from '@/lib/types'

type Tab = 'catalog' | 'installed' | 'instances'

/** 单个分区最多渲染多少条：实例/插件可能很多，超出部分如实说明而不是静默截断 */
const LIST_CAP = 200

/**
 * 插件面三分：
 *   目录 Catalog   = 插件声明事实（GET /api/plugins）：kind/version/digest/verified/permissions/contributes
 *   已安装 Installed = 每台 Edge 上实际装了什么、跑成什么样（只取 observed 投影）
 *   实例 Instances  = 期望态与实际态**分离**呈现 + 写操作（POST/PATCH/DELETE/reconcile）
 *
 * 不渲染目录里的 source 字段：它可能是安装来源的本机路径，属于不得外泄的信息。
 */
export default function Plugins() {
  const [tab, setTab] = useState<Tab>('catalog')
  const [creating, setCreating] = useState(false)
  const [editing, setEditing] = useState<PluginInstanceView | null>(null)

  const { plugins, loading: catLoading, error: catError, refetch: refetchCat } = usePluginCatalog()
  const { instances, loading: insLoading, error: insError, refetch: refetchIns } = usePluginInstances()
  const catalogIndex = useMemo(() => indexCatalog(plugins), [plugins])
  const byEdge = useMemo(() => groupByEdge(instances), [instances])

  const tabs: TabItem<Tab>[] = [
    { value: 'catalog', label: '目录', icon: <Puzzle size={13} />, count: plugins.length },
    { value: 'installed', label: '已安装', icon: <Server size={13} />, count: byEdge.size },
    { value: 'instances', label: '实例', icon: <Boxes size={13} />, count: instances.length },
  ]

  return (
    <>
      <PageHeader
        title="插件"
        subtitle={
          catLoading || insLoading
            ? '正在加载插件面…'
            : `目录 ${plugins.length} 个 · 运行实例 ${instances.length} 个`
        }
        actions={
          <button type="button" className="btn btn-primary" onClick={() => { setCreating(true); setTab('instances') }}>
            <Plus size={13} /> 新建实例
          </button>
        }
      />

      <div className="mb-5">
        <TabBar items={tabs} value={tab} onChange={setTab} label="插件面分区" />
      </div>

      {tab === 'catalog' && (
        <TabPanel value={tab}>
          {catError ? (
            <ErrorState icon={<Puzzle size={20} />} title="插件目录加载失败"
              hint="拿不到 GET /api/plugins。目录缺席不影响实例的期望态与实际态呈现。"
              onRetry={refetchCat} />
          ) : catLoading ? (
            <Panel><RowSkeleton rows={4} /></Panel>
          ) : plugins.length === 0 ? (
            <EmptyState icon={<PackageOpen size={24} />} title="插件目录为空"
              hint="还没有已安装的插件被登记到目录里。Edge 安装插件并上报后，这里会出现它的声明事实（版本、摘要、权限、贡献）。" />
          ) : (
            <div className="grid gap-4 lg:grid-cols-2">
              {plugins.slice(0, LIST_CAP).map((p) => {
                const trust = trustMeta(undefined, p.verified)
                const contributes = [
                  ...(p.contributes?.drivers ?? []).map((x) => ({ kind: 'Driver', ...x })),
                  ...(p.contributes?.applications ?? []).map((x) => ({ kind: 'Application', ...x })),
                  ...(p.contributes?.connectors ?? []).map((x) => ({ kind: 'Connector', ...x })),
                ]
                return (
                  <Panel key={p.id} className="fade-up">
                    <div className="flex min-w-0 flex-wrap items-center gap-2">
                      <span className="num min-w-0 max-w-full truncate text-[15px] font-semibold tracking-[-0.01em]" title={p.id}>
                        {p.id}
                      </span>
                      <span className="ml-auto flex shrink-0 items-center gap-1.5">
                        <Badge tone="idle" className="max-w-full">
                          <span className="min-w-0 truncate">{p.kind || '未知类型'}</span>
                        </Badge>
                        <Badge tone={trust.tone}>
                          {p.verified ? <ShieldCheck size={11} className="shrink-0" /> : null}
                          {trust.label}
                        </Badge>
                      </span>
                    </div>

                    <dl className="mt-3 space-y-1.5">
                      <div className="kv"><dt>版本</dt>
                        <dd className="num min-w-0 truncate">{p.version || '—'}</dd></div>
                      <div className="kv"><dt>协议</dt>
                        <dd className="num min-w-0 truncate">{p.protocol || 0}</dd></div>
                      <div className="kv"><dt>摘要</dt>
                        <dd className="num min-w-0 truncate text-ink-2" title={p.digest}>
                          {shortDigest(p.digest)}
                        </dd></div>
                      {p.compatibility && (
                        <div className="kv"><dt>兼容性</dt>
                          <dd className="min-w-0 truncate" title={p.compatibility}>{p.compatibility}</dd></div>
                      )}
                    </dl>

                    <div className="mt-3.5 border-t border-hairline pt-3">
                      <p className="mb-2 text-[12px] font-medium text-ink-3">声明权限</p>
                      <PermissionList permissions={p.permissions} />
                    </div>

                    {contributes.length > 0 && (
                      <div className="mt-3.5 border-t border-hairline pt-3">
                        <p className="mb-2 text-[12px] font-medium text-ink-3">贡献</p>
                        <ul className="m-0 flex list-none flex-wrap gap-1.5 p-0">
                          {contributes.map((c) => (
                            <li key={`${c.kind}-${c.id}`} className="min-w-0 max-w-full">
                              <span className="badge max-w-full bg-ink-3/10 text-ink-2" title={`${c.kind} · ${c.id}`}>
                                <span className="shrink-0 text-ink-3">{c.kind}</span>
                                <span className="min-w-0 truncate">{c.title || c.id}</span>
                              </span>
                            </li>
                          ))}
                        </ul>
                      </div>
                    )}

                    <p className="mt-3 border-t border-hairline pt-2.5 text-[12px] leading-relaxed text-ink-3">
                      以上是插件的**声明**，不代表任何 Edge 上正在运行；运行事实见「已安装」与「实例」分区。
                    </p>
                  </Panel>
                )
              })}
            </div>
          )}
          {plugins.length > LIST_CAP && (
            <p className="mt-4 text-center text-[12px] text-ink-3">
              仅显示前 {LIST_CAP} 个插件（目录共 {plugins.length} 个）
            </p>
          )}
        </TabPanel>
      )}

      {tab === 'installed' && (
        <TabPanel value={tab}>
          {insError ? (
            <ErrorState icon={<Server size={20} />} title="已安装信息加载失败"
              hint="拿不到 GET /api/plugin-instances 的实际态投影。" onRetry={refetchIns} />
          ) : insLoading ? (
            <Panel><RowSkeleton rows={4} /></Panel>
          ) : instances.length === 0 ? (
            <EmptyState icon={<Server size={24} />} title="还没有插件实例"
              hint="在「实例」分区新建一个实例，指定目标 Edge 与插件版本；Edge 应用快照并上报后，这里会显示它实际装了什么。" />
          ) : (
            <div className="space-y-5">
              {[...byEdge.entries()].map(([edgeId, list]) => {
                const edgeOnline = list[0]?.edge_online ?? false
                return (
                  <Panel key={edgeId}
                    title={
                      <span className="flex min-w-0 items-center gap-2">
                        <StatusDot online={edgeOnline} />
                        <span className="num min-w-0 truncate" title={edgeId}>{edgeId || '未知 Edge'}</span>
                      </span>
                    }
                    right={
                      <Badge tone={edgeOnline ? 'ok' : 'idle'}>{edgeOnline ? '边缘节点在线' : '边缘节点离线'}</Badge>
                    }>
                    {!edgeOnline && (
                      <p className="mb-3 flex items-start gap-1.5 rounded-lg bg-ink-3/10 px-3 py-2.5 text-[12px] leading-relaxed text-ink-2">
                        <CloudOff size={12} className="mt-0.5 shrink-0" />
                        <span className="min-w-0">该边缘节点离线：下面是它最后一次上报的实际态，不代表当前运行状况。其他节点不受影响。</span>
                      </p>
                    )}
                    <ul className="m-0 list-none divide-y divide-hairline p-0">
                      {list.slice(0, LIST_CAP).map((v) => {
                        const st = stateMeta(v.observed?.state)
                        const hl = healthMeta(v.observed?.health)
                        return (
                          <li key={v.id} className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 py-2.5">
                            <Link to={`/plugins/${encodeURIComponent(v.id)}`}
                              className="num min-w-0 max-w-full truncate text-[13px] font-medium no-underline hover:text-accent"
                              title={v.id}>
                              {v.desired.instance_id || v.id}
                            </Link>
                            <span className="num min-w-0 max-w-full truncate text-[12px] text-ink-3" title={v.desired.plugin_id}>
                              {v.desired.plugin_id}
                            </span>
                            {v.has_observed ? (
                              <>
                                <Badge tone={st.tone === 'idle' ? 'idle' : st.tone} className="max-w-full">
                                  <span className="min-w-0 truncate">{st.label}</span>
                                </Badge>
                                <span className="num truncate text-[12px] text-ink-2">{v.observed?.version || '未给出版本'}</span>
                                <span className="truncate text-[12px] text-ink-3">健康 {hl.label}</span>
                                <span className="num truncate text-[12px] text-ink-3">
                                  重启 {v.observed?.restart_count ?? 0} 次
                                </span>
                                {v.stale && <Badge tone="warn" className="shrink-0">stale</Badge>}
                                <span className="num ml-auto shrink-0 text-[12px] text-ink-3"
                                  title={v.observed?.reported_at ? fmtDateTime(v.observed.reported_at) : '未给出上报时间'}>
                                  {v.observed?.reported_at ? fmtDateTime(v.observed.reported_at) : '—'}
                                </span>
                              </>
                            ) : (
                              <>
                                <Badge tone="idle">边缘节点未上报</Badge>
                                <span className="ml-auto shrink-0 text-[12px] text-ink-3">
                                  {v.edge_online ? '节点在线但还没回过' : '节点离线'}
                                </span>
                              </>
                            )}
                          </li>
                        )
                      })}
                    </ul>
                  </Panel>
                )
              })}
              <p className="text-[12px] leading-relaxed text-ink-3">
                「已安装」只呈现 Edge 上报的实际态；没有上报就写「Edge 未上报」，不用期望态顶替。
              </p>
            </div>
          )}
        </TabPanel>
      )}

      {tab === 'instances' && (
        <TabPanel value={tab}>
          {creating ? (
            <Panel title={<span className="flex items-center gap-1.5"><PackagePlus size={14} />新建插件实例</span>}>
              <InstanceForm mode="create" catalog={plugins} onDone={() => setCreating(false)} />
            </Panel>
          ) : editing ? (
            <Panel title={<span className="flex items-center gap-1.5"><PackagePlus size={14} />编辑插件实例</span>}>
              <InstanceForm mode="edit" instance={editing} catalog={plugins} onDone={() => setEditing(null)} />
            </Panel>
          ) : insError ? (
            <ErrorState icon={<Boxes size={20} />} title="插件实例加载失败"
              hint="拿不到 GET /api/plugin-instances。期望态与实际态都无法呈现，请重试或检查 server。"
              onRetry={refetchIns} />
          ) : insLoading ? (
            <div className="grid gap-4"><RowSkeleton rows={3} /></div>
          ) : instances.length === 0 ? (
            <EmptyState icon={<Layers size={24} />} title="还没有插件实例"
              hint="新建一个实例：指定目标 Edge、插件与固定版本。提交后 Server 生成期望态与单调递增的 revision，Edge 应用并上报后才会出现实际态。" />
          ) : (
            <div className="grid gap-4">
              {instances.slice(0, LIST_CAP).map((v) => (
                <InstanceRow key={v.id} v={v} catalog={catalogIndex.get(v.desired.plugin_id)}
                  onEdit={() => setEditing(v)} />
              ))}
              {instances.length > LIST_CAP && (
                <p className="text-center text-[12px] text-ink-3">
                  仅显示前 {LIST_CAP} 个实例（共 {instances.length} 个）
                </p>
              )}
              <p className="text-[12px] leading-relaxed text-ink-3">
                每行都分开写「期望态」与「实际态」：期望已启用不等于 Edge 已经运行。实际态缺席时明确显示「Edge 未上报」。
              </p>
            </div>
          )}
        </TabPanel>
      )}
    </>
  )
}