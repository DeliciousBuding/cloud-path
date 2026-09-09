import { useEffect, useMemo, useState } from 'react'
import {
  Boxes, Layers, PackagePlus, PackageOpen, Plus, Puzzle, Server, ShieldCheck,
} from 'lucide-react'
import {
  Badge, EmptyState, ErrorState, PageHeader, Panel, TabBar, TabPanel,
} from '@/components/ui'
import { RowSkeleton } from '@/components/Skeleton'
import { InstanceForm } from '@/components/plugin/InstanceForm'
import { InstanceRow } from '@/components/plugin/InstanceRow'
import { PermissionList } from '@/components/plugin/PluginFacts'
import {
  usePluginCatalog, usePluginInstances,
} from '@/hooks/usePlugins'
import { indexCatalog, pluginDisplayName, shortDigest, trustMeta } from '@/lib/plugins'
import type { TabItem } from '@/components/ui'
import type { PluginInstanceView } from '@/lib/types'
import { usePageTitle } from '@/hooks/usePageTitle'
import { useAuth } from '@/store/auth'

type Tab = 'catalog' | 'instances'

/** 单个分区最多渲染多少条：实例/插件可能很多，超出部分如实说明而不是静默截断 */
const LIST_CAP = 200

const KIND_LABEL: Record<string, string> = {
  application: '应用',
  driver: '设备驱动',
  connector: '连接器',
}

function kindLabel(kind: string | undefined): string {
  return kind ? (KIND_LABEL[kind] ?? kind) : '未知类型'
}

/**
 * 插件面三分：
 *   目录 Catalog   = 插件声明事实（GET /api/plugins）：kind/version/digest/verified/permissions/contributes
 *   已安装 Installed = 每台 Edge 上实际装了什么、跑成什么样（只取 observed 投影）
 *   实例 Instances  = 期望态与实际态**分离**呈现 + 写操作（POST/PATCH/DELETE/reconcile）
 *
 * 不渲染目录里的 source 字段：它可能是安装来源的本机路径，属于不得外泄的信息。
 */
export default function Plugins() {
  usePageTitle('应用与插件')

  const [tab, setTab] = useState<Tab>('instances')
  const [edgeFilter, setEdgeFilter] = useState('all')
  const [creating, setCreating] = useState(false)
  const [editing, setEditing] = useState<PluginInstanceView | null>(null)
  const readOnly = useAuth((s) => s.status === 'in' && s.user?.role === 'viewer')

  useEffect(() => {
    if (readOnly) {
      setCreating(false)
      setEditing(null)
    }
  }, [readOnly])

  const { plugins, loading: catLoading, error: catError, refetch: refetchCat } = usePluginCatalog()
  const { instances, loading: insLoading, error: insError, refetch: refetchIns } = usePluginInstances()
  const catalogIndex = useMemo(() => indexCatalog(plugins), [plugins])
  const edgeOptions = useMemo(
    () => [...new Set(instances.map((v) => v.edge_id))].sort((a, b) => a.localeCompare(b)),
    [instances],
  )
  const visibleInstances = useMemo(
    () => edgeFilter === 'all' ? instances : instances.filter((v) => v.edge_id === edgeFilter),
    [edgeFilter, instances],
  )

  useEffect(() => {
    if (edgeFilter !== 'all' && !edgeOptions.includes(edgeFilter)) setEdgeFilter('all')
  }, [edgeFilter, edgeOptions])

  const tabs: TabItem<Tab>[] = [
    { value: 'instances', label: '运行实例', icon: <Boxes size={13} />, count: instances.length },
    { value: 'catalog', label: '可用插件', icon: <Puzzle size={13} />, count: plugins.length },
  ]

  return (
    <>
      <PageHeader
        title="应用与插件"
        subtitle={
          catLoading || insLoading
            ? '正在加载…'
            : `${instances.length} 个运行实例 · ${plugins.length} 个可用插件`
        }
        actions={!readOnly && (
          <button type="button" className="btn btn-primary" onClick={() => { setCreating(true); setTab('instances') }}>
            <Plus size={13} /> 新建实例
          </button>
        )}
      />

      <div className="mb-5">
        <TabBar items={tabs} value={tab} onChange={setTab} label="应用与插件分区" />
      </div>

      {tab === 'catalog' && (
        <TabPanel value={tab}>
          {catError ? (
            <ErrorState icon={<Puzzle size={20} />} title="可用插件加载失败"
              hint="暂时无法加载可用插件。已经添加的项目不会受影响，请稍后重试。"
              onRetry={refetchCat} />
          ) : catLoading ? (
            <Panel><RowSkeleton rows={4} /></Panel>
          ) : plugins.length === 0 ? (
            <EmptyState icon={<PackageOpen size={24} />} title="没有可用插件"
              hint="插件同步后会显示在这里。列表为空不影响已经添加的项目。" />
          ) : (
            <div className="grid gap-4 lg:grid-cols-2">
              {plugins.slice(0, LIST_CAP).map((p) => {
                const trust = trustMeta(undefined, p.verified)
                const contributes = [
                  ...(p.contributes?.drivers ?? []).map((x) => ({ kind: 'Driver', label: '设备驱动', ...x })),
                  ...(p.contributes?.applications ?? []).map((x) => ({ kind: 'Application', label: '应用', ...x })),
                  ...(p.contributes?.connectors ?? []).map((x) => ({ kind: 'Connector', label: '连接器', ...x })),
                ]
                return (
                  <Panel key={p.id} className="fade-up">
                    <div className="flex min-w-0 flex-wrap items-center gap-2">
                      <span className="min-w-0 max-w-full truncate text-[15px] font-semibold tracking-[-0.01em]" title={pluginDisplayName(p)}>
                        {pluginDisplayName(p)}
                      </span>
                      <span className="ml-auto flex shrink-0 items-center gap-1.5">
                        <Badge tone="idle" className="max-w-full">
                          <span className="min-w-0 truncate">{kindLabel(p.kind)}</span>
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
                    </dl>

                    <div className="mt-3.5 border-t border-hairline pt-3">
                      <p className="mb-2 text-[12px] font-medium text-ink-3">需要的权限</p>
                      <PermissionList permissions={p.permissions} emptyHint="不需要额外权限" />
                    </div>

                    {contributes.length > 0 && (
                      <div className="mt-3.5 border-t border-hairline pt-3">
                        <p className="mb-2 text-[12px] font-medium text-ink-3">提供的功能</p>
                        <ul className="m-0 flex list-none flex-wrap gap-1.5 p-0">
                          {contributes.map((c) => (
                            <li key={`${c.kind}-${c.id}`} className="min-w-0 max-w-full">
                              <span className="badge max-w-full bg-ink-3/10 text-ink-2" title={`${c.kind} · ${c.id}`}>
                                <span className="shrink-0 text-ink-3">{c.label}</span>
                                <span className="min-w-0 truncate">{c.title || c.id}</span>
                              </span>
                            </li>
                          ))}
                        </ul>
                      </div>
                    )}

                    <details className="mt-3.5 min-w-0 border-t border-hairline pt-3 text-xs text-ink-2">
                      <summary className="cursor-pointer">技术详情</summary>
                      <dl className="mt-2 space-y-1.5">
                        <div className="kv"><dt>插件标识</dt>
                          <dd className="num min-w-0 truncate font-mono" title={p.id}>{p.id}</dd></div>
                        <div className="kv"><dt>插件类型</dt>
                          <dd className="num min-w-0 truncate">{p.kind || '未提供'}</dd></div>
                        <div className="kv"><dt>协议版本</dt>
                          <dd className="num min-w-0 truncate">{p.protocol || 0}</dd></div>
                        <div className="kv"><dt>安装摘要</dt>
                          <dd className="num min-w-0 truncate" title={p.digest}>{shortDigest(p.digest)}</dd></div>
                        {p.compatibility && (
                          <div className="kv"><dt>兼容性</dt>
                            <dd className="min-w-0 truncate" title={p.compatibility}>{p.compatibility}</dd></div>
                        )}
                        {contributes.length > 0 && (
                          <div className="min-w-0 pt-1">
                            <dt className="mb-1 text-ink-3">功能标识</dt>
                            <dd className="flex min-w-0 flex-wrap gap-1.5">
                              {contributes.map((c) => <span key={`${c.kind}-${c.id}-technical`}
                                className="num max-w-full truncate rounded bg-surface-2 px-1.5 py-0.5 font-mono"
                                title={`${c.kind} · ${c.id}`}>{c.kind} · {c.id}</span>)}
                            </dd>
                          </div>
                        )}
                      </dl>
                    </details>

                    <p className="mt-3 border-t border-hairline pt-2.5 text-[12px] leading-relaxed text-ink-3">
                      这里只表示插件可以使用，不代表已经运行。请到「运行实例」查看。
                    </p>
                  </Panel>
                )
              })}
            </div>
          )}
          {plugins.length > LIST_CAP && (
            <p className="mt-4 text-center text-[12px] text-ink-3">
              仅显示前 {LIST_CAP} 个插件（共 {plugins.length} 个）
            </p>
          )}
        </TabPanel>
      )}

      {tab === 'instances' && (
        <TabPanel value={tab}>
          {!readOnly && creating ? (
            <Panel title={<span className="flex items-center gap-1.5"><PackagePlus size={14} />新建运行实例</span>}>
              <InstanceForm mode="create" catalog={plugins} onDone={() => setCreating(false)} />
            </Panel>
          ) : !readOnly && editing ? (
            <Panel title={<span className="flex items-center gap-1.5"><PackagePlus size={14} />编辑运行实例</span>}>
              <InstanceForm mode="edit" instance={editing} catalog={plugins} onDone={() => setEditing(null)} />
            </Panel>
          ) : insError ? (
            <ErrorState icon={<Boxes size={20} />} title="运行实例加载失败"
              hint="暂时无法加载运行实例。请重试；如果仍然失败，请联系管理员。"
              onRetry={refetchIns} />
          ) : insLoading ? (
            <div className="grid gap-4"><RowSkeleton rows={3} /></div>
          ) : instances.length === 0 ? (
            <EmptyState icon={<Layers size={24} />} title="还没有运行实例"
              hint="添加项目后选择网关和版本。保存后，网关应用设置时这里会显示运行情况。" />
          ) : (
            <div className="grid gap-4">
              <div className="flex min-w-0 flex-col gap-2 rounded-lg bg-surface-2 px-3.5 py-3 sm:flex-row sm:items-center sm:justify-between">
                <label htmlFor="instance-location" className="flex min-w-0 shrink-0 items-center gap-2 whitespace-nowrap text-[13px] font-medium text-ink-2">
                  <Server size={14} className="shrink-0" /> 运行位置
                </label>
                <select id="instance-location" className="input max-w-full text-[13px] sm:w-72"
                  value={edgeFilter} onChange={(e) => setEdgeFilter(e.target.value)}>
                  <option value="all">全部运行位置（{instances.length}）</option>
                  {edgeOptions.map((edge) => (
                    <option key={edge} value={edge}>
                      {edge === 'server' ? '中心服务' : `网关 ${edge || '未知'}`}（{instances.filter((v) => v.edge_id === edge).length}）
                    </option>
                  ))}
                </select>
              </div>

              {visibleInstances.length === 0 ? (
                <EmptyState icon={<Layers size={24} />} title="这个运行位置还没有实例"
                  hint="选择其他运行位置，或在上方添加项目。" />
              ) : (
                <>
                  {visibleInstances.slice(0, LIST_CAP).map((v) => (
                    <InstanceRow key={v.id} v={v} catalog={catalogIndex.get(v.desired.plugin_id)}
                      onEdit={() => setEditing(v)} />
                  ))}
                  {visibleInstances.length > LIST_CAP && (
                    <p className="text-center text-[12px] text-ink-3">
                      仅显示前 {LIST_CAP} 个实例（共 {visibleInstances.length} 个）
                    </p>
                  )}
                </>
              )}

              <p className="text-[12px] leading-relaxed text-ink-3">
                每行分开显示保存的设置和当前运行情况。保存为启用，不代表它已经运行。
              </p>
            </div>
          )}
        </TabPanel>
      )}
    </>
  )
}
