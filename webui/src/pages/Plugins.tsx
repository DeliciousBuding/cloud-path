import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import {
  Ban, Boxes, Layers, PackagePlus, PackageOpen, Plus, Puzzle, Server, ShieldCheck,
} from 'lucide-react'
import {
  Badge, Button, EmptyState, ErrorState, PageHeader, Panel, Select, TabBar, TabPanel,
} from '@/components/ui'
import { RowSkeleton } from '@/components/Skeleton'
import { InstanceForm } from '@/components/plugin/InstanceForm'
import { InstanceRow } from '@/components/plugin/InstanceRow'
import { PermissionList } from '@/components/plugin/PluginFacts'
import {
  usePluginCatalog, usePluginInstances,
} from '@/hooks/usePlugins'
import {
  indexCatalog, instanceStatus, normalizePluginKind, pluginDisplayName, shortDigest, trustMeta,
} from '@/lib/plugins'
import type { TabItem } from '@/components/ui'
import type { PluginInstanceView } from '@/lib/types'
import { usePageTitle } from '@/hooks/usePageTitle'
import { useAuth } from '@/store/auth'

type Tab = 'catalog' | 'instances'

/** 单个分区最多渲染多少条：实例/插件可能很多，超出部分如实说明而不是静默截断 */
const LIST_CAP = 200

function StatusPill({ label, count, tone }: { label: string; count: number; tone: 'ok' | 'warn' | 'idle' }) {
  return <span className="inline-flex min-w-0 items-center gap-1.5">
    <Badge tone={tone}>{label}</Badge>
    <span className="num text-compact font-semibold">{count}</span>
  </span>
}

/**
 * 插件面二分：
 *   目录 Catalog   = 插件声明事实（GET /api/plugins）：kind/version/digest/verified/permissions/contributes
 *   实例 Instances  = 期望态与实际态**分离**呈现 + 写操作（POST/PATCH/DELETE/reconcile）
 *
 * 不渲染目录里的 source 字段：它可能是安装来源的本机路径，属于不得外泄的信息。
 */
export default function Plugins() {
  const { t } = useTranslation('plugins')
  usePageTitle(t('page.title'))

  function kindLabel(kind: string | undefined): string {
    const key = kind === 'application' ? 'page.kindApplication'
      : kind === 'driver' ? 'page.kindDriver'
        : kind === 'connector' ? 'page.kindConnector' : undefined
    return key ? t(key) : kind || t('page.kindUnknown')
  }

  const [tab, setTab] = useState<Tab>('instances')
  const [edgeFilter, setEdgeFilter] = useState('all')
  const [creating, setCreating] = useState(false)
  const [creatingPluginId, setCreatingPluginId] = useState<string | null>(null)
  const [editing, setEditing] = useState<PluginInstanceView | null>(null)
  const readOnly = useAuth((s) => s.status === 'in' && s.user?.role === 'viewer')

  useEffect(() => {
    if (readOnly) {
      setCreating(false)
      setCreatingPluginId(null)
      setEditing(null)
    }
  }, [readOnly])

  const { plugins, loading: catLoading, error: catError, refetch: refetchCat } = usePluginCatalog()
  const canCreateInstance = plugins.some((p) => ['application', 'driver'].includes(normalizePluginKind(p.kind)))
  const { instances, loading: insLoading, error: insError, refetch: refetchIns } = usePluginInstances()
  const catalogIndex = useMemo(() => indexCatalog(plugins), [plugins])
  const edgeOptions = useMemo(
    () => [...new Set(instances.map((v) => v.edge_id))].sort((a, b) => a.localeCompare(b)),
    [instances],
  )
  const statusCounts = useMemo(() => ({
    normal: instances.filter((v) => instanceStatus(v).key === 'normal').length,
    attention: instances.filter((v) => instanceStatus(v).key === 'attention').length,
    unknown: instances.filter((v) => instanceStatus(v).key === 'unknown').length,
    stopped: instances.filter((v) => instanceStatus(v).key === 'stopped').length,
  }), [instances])
  const visibleInstances = useMemo(() => {
    const filtered = edgeFilter === 'all' ? instances : instances.filter((v) => v.edge_id === edgeFilter)
    return [...filtered].sort((a, b) => {
      const byPriority = instanceStatus(a).priority - instanceStatus(b).priority
      return byPriority || (a.desired.instance_id || a.id).localeCompare(b.desired.instance_id || b.id)
    })
  }, [edgeFilter, instances])

  useEffect(() => {
    if (edgeFilter !== 'all' && !edgeOptions.includes(edgeFilter)) setEdgeFilter('all')
  }, [edgeFilter, edgeOptions])

  const tabs: TabItem<Tab>[] = [
    { value: 'instances', label: t('page.tabInstances'), icon: <Boxes size={13} />, count: instances.length },
    { value: 'catalog', label: t('page.tabCatalog'), icon: <Puzzle size={13} />, count: plugins.length },
  ]

  function startCreate(pluginId?: string) {
    setCreatingPluginId(pluginId ?? null)
    setCreating(true)
    setEditing(null)
    setTab('instances')
  }

  function stopCreate() {
    setCreating(false)
    setCreatingPluginId(null)
  }

  return (
    <>
      <PageHeader
        title={t('page.title')}
        subtitle={
          catLoading || insLoading
            ? t('page.loading')
            : instances.length === 0
              ? t('page.pluginCount', { count: plugins.length })
              : t('page.instanceSummary', { total: instances.length, normal: statusCounts.normal, attention: statusCounts.attention })
        }
        actions={!readOnly && canCreateInstance && (
          <Button onClick={() => startCreate()}>
            <Plus size={13} /> {t('page.createInstance')}
          </Button>
        )}
      />

      <div className="mb-5">
        <TabBar items={tabs} value={tab} onChange={setTab} label={t('page.tabAria')} />
      </div>

      {tab === 'catalog' && (
        <TabPanel value={tab}>
          {catError ? (
            <ErrorState icon={<Puzzle size={20} />} title={t('page.catalogLoadFailed')}
              hint={t('page.catalogLoadFailedHint')}
              onRetry={refetchCat} />
          ) : catLoading ? (
            <Panel><RowSkeleton rows={4} /></Panel>
          ) : plugins.length === 0 ? (
            <EmptyState icon={<PackageOpen size={24} />} title={t('page.noPlugins')}
              hint={t('page.noPluginsHint')} />
          ) : (
            <div className="grid gap-4 lg:grid-cols-2">
              {plugins.slice(0, LIST_CAP).map((p) => {
                const trust = trustMeta(undefined, p.verified)
                const kind = normalizePluginKind(p.kind)
                const canCreate = kind === 'application' || kind === 'driver'
                const contributes = [
                  ...(p.contributes?.drivers ?? []).map((x) => ({ kind: 'Driver', label: t('page.contributionDriver'), ...x })),
                  ...(p.contributes?.applications ?? []).map((x) => ({ kind: 'Application', label: t('page.contributionApplication'), ...x })),
                  ...(p.contributes?.connectors ?? []).map((x) => ({ kind: 'Connector', label: t('page.contributionConnector'), ...x })),
                ]
                return (
                  <Panel key={p.id} className="fade-up">
                    <div className="flex min-w-0 flex-wrap items-center gap-2">
                      <span className="min-w-0 max-w-full truncate text-lead font-semibold tracking-[-0.01em]" title={pluginDisplayName(p)}>
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
                      <div className="kv"><dt>{t('page.version')}</dt>
                        <dd className="num min-w-0 truncate">{p.version || '—'}</dd></div>
                    </dl>

                    <div className="mt-3.5 border-t border-hairline pt-3">
                      <p className="mb-2 text-meta font-medium text-ink-3">{t('page.permissionsRequired')}</p>
                      <PermissionList permissions={p.permissions} emptyHint={t('page.noExtraPermissions')} />
                    </div>

                    <details className="mt-3.5 min-w-0 border-t border-hairline pt-3 text-meta text-ink-2">
                      <summary className="flex min-h-touch cursor-pointer items-center">{t('page.technicalDetails')}</summary>
                      <dl className="mt-2 space-y-1.5">
                        <div className="kv"><dt>{t('page.pluginId')}</dt>
                          <dd className="num min-w-0 truncate font-mono" title={p.id}>{p.id}</dd></div>
                        <div className="kv"><dt>{t('page.pluginType')}</dt>
                          <dd className="num min-w-0 truncate">{p.kind || t('page.notProvided')}</dd></div>
                        <div className="kv"><dt>{t('page.protocolVersion')}</dt>
                          <dd className="num min-w-0 truncate">{p.protocol || 0}</dd></div>
                        <div className="kv"><dt>{t('page.installDigest')}</dt>
                          <dd className="num min-w-0 truncate" title={p.digest}>{shortDigest(p.digest)}</dd></div>
                        {p.compatibility && (
                          <div className="kv"><dt>{t('page.compatibility')}</dt>
                            <dd className="min-w-0 truncate" title={p.compatibility}>{p.compatibility}</dd></div>
                        )}
                        {contributes.length > 0 && (
                          <div className="min-w-0 pt-1">
                            <dt className="mb-1 text-ink-3">{t('page.contributions')}</dt>
                            <dd className="flex min-w-0 flex-wrap gap-1.5">
                              {contributes.map((c) => <span key={`${c.kind}-${c.id}-technical`}
                                className="num max-w-full truncate rounded bg-surface-2 px-1.5 py-0.5 font-mono"
                                title={`${c.kind} · ${c.id}`}>{c.kind} · {c.id}</span>)}
                            </dd>
                          </div>
                        )}
                      </dl>
                    </details>

                    <div className="mt-3.5 flex min-w-0 flex-wrap items-center gap-2 border-t border-hairline pt-3">
                      {canCreate ? (
                        <Button
                          aria-label={t('page.createInstanceAria', { name: pluginDisplayName(p) })}
                          onClick={() => startCreate(p.id)}>
                          <Plus size={13} /> {t('page.createInstanceButton')}
                        </Button>
                      ) : (
                        <span className="inline-flex items-center gap-1.5 text-meta font-medium text-ink-2">
                          <Ban size={13} className="shrink-0" /> {t('page.connectorNoInstance')}
                        </span>
                      )}
                      <span className="min-w-0 text-meta leading-relaxed text-ink-3">
                        {t('page.availableHint')}
                      </span>
                    </div>
                  </Panel>
                )
              })}
            </div>
          )}
          {plugins.length > LIST_CAP && (
            <p className="mt-4 text-center text-meta text-ink-3">
              {t('page.listCapPlugins', { cap: LIST_CAP, count: plugins.length })}
            </p>
          )}
        </TabPanel>
      )}

      {tab === 'instances' && (
        <TabPanel value={tab}>
          {!readOnly && creating ? (
            <Panel title={<span className="flex items-center gap-1.5"><PackagePlus size={14} />{t('page.newInstance')}</span>}>
              <InstanceForm mode="create" catalog={plugins} initialPluginId={creatingPluginId ?? undefined} onDone={stopCreate} />
            </Panel>
          ) : !readOnly && editing ? (
            <Panel title={<span className="flex items-center gap-1.5"><PackagePlus size={14} />{t('page.editInstance')}</span>}>
              <InstanceForm mode="edit" instance={editing} catalog={plugins} onDone={() => setEditing(null)} />
            </Panel>
          ) : insError ? (
            <ErrorState icon={<Boxes size={20} />} title={t('page.instanceLoadFailed')}
              hint={t('page.instanceLoadFailedHint')}
              onRetry={refetchIns} />
          ) : insLoading ? (
            <div className="grid gap-4"><RowSkeleton rows={3} /></div>
          ) : instances.length === 0 ? (
            <div>
              <EmptyState icon={<Layers size={24} />} title={t('page.noInstances')}
                hint={canCreateInstance
                  ? t('page.noInstancesCreateHint')
                  : t('page.noInstancesInstallHint')} />
              {!readOnly && canCreateInstance && <div className="-mt-3 flex justify-center">
                <Button onClick={() => startCreate()}>
                  <Plus size={13} /> {t('page.createFirstInstance')}
                </Button>
              </div>}
            </div>
          ) : (
            <div className="grid gap-4">
              <div className="flex min-w-0 flex-wrap items-center gap-x-4 gap-y-2 rounded-tile bg-surface-2 px-3.5 py-2.5">
                <span className="text-meta font-medium text-ink-3">{t('page.statusOverview')}</span>
                <StatusPill label={t('page.statusNormal')} count={statusCounts.normal} tone="ok" />
                <StatusPill label={t('page.statusAttention')} count={statusCounts.attention} tone="warn" />
                <StatusPill label={t('page.statusUnknown')} count={statusCounts.unknown} tone="idle" />
                <StatusPill label={t('page.statusStopped')} count={statusCounts.stopped} tone="idle" />
              </div>
              <div className="flex min-w-0 flex-col gap-2 rounded-tile bg-surface-2 px-3.5 py-3 sm:flex-row sm:items-center sm:justify-between">
                <label htmlFor="instance-location" className="flex min-w-0 shrink-0 items-center gap-2 whitespace-nowrap text-compact font-medium text-ink-2">
                  <Server size={14} className="shrink-0" /> {t('page.location')}
                </label>
                <Select id="instance-location" compact className="max-w-full sm:w-72"
                  value={edgeFilter} onChange={(e) => setEdgeFilter(e.target.value)}>
                  <option value="all">{t('page.allLocations', { count: instances.length })}</option>
                  {edgeOptions.map((edge) => (
                    <option key={edge} value={edge}>
                      {edge === 'server' ? t('page.server') : edge ? t('page.edge', { id: edge }) : t('page.edgeUnknown')}（{instances.filter((v) => v.edge_id === edge).length}）
                    </option>
                  ))}
                </Select>
              </div>

              {visibleInstances.length === 0 ? (
                <EmptyState icon={<Layers size={24} />} title={t('page.noInstancesAtLocation')}
                  hint={t('page.noInstancesAtLocationHint')} />
              ) : (
                <>
                  {visibleInstances.slice(0, LIST_CAP).map((v) => (
                    <InstanceRow key={v.id} v={v} catalog={catalogIndex.get(v.desired.plugin_id)}
                      onEdit={() => setEditing(v)} />
                  ))}
                  {visibleInstances.length > LIST_CAP && (
                    <p className="text-center text-meta text-ink-3">
                      {t('page.listCapInstances', { cap: LIST_CAP, count: visibleInstances.length })}
                    </p>
                  )}
                </>
              )}

              <p className="text-meta leading-relaxed text-ink-3">
                {t('page.listHint')}
              </p>
            </div>
          )}
        </TabPanel>
      )}
    </>
  )
}
