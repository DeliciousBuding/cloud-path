import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Link, useParams } from 'react-router'
import {
  Boxes, KeyRound, Puzzle, Server, Settings2, ShieldCheck, SlidersHorizontal,
} from 'lucide-react'
import { BackLink, Badge, EmptyState, ErrorState, Panel } from '@/components/ui'
import { RowSkeleton } from '@/components/Skeleton'
import { ApplicationPlane } from '@/components/plugin/ApplicationPlane'
import { InstanceSplit } from '@/components/plugin/InstanceRow'
import { InstanceStatusSummary } from '@/components/plugin/DesiredObserved'
import { InstanceControls } from '@/components/plugin/InstanceControls'
import { InstanceForm } from '@/components/plugin/InstanceForm'
import { ConfigTable, InstanceFacts, PermissionList, SecretRefList } from '@/components/plugin/PluginFacts'
import { usePluginCatalog, usePluginInstance } from '@/hooks/usePlugins'
import { instanceStatus, pluginDisplayName } from '@/lib/plugins'
import { usePageTitle } from '@/hooks/usePageTitle'
import { useAuth } from '@/store/auth'

/**
 * 插件实例详情：期望状态与实际状态**分离**呈现，外加 Version / Edge / Trust / Permissions /
 * Health / Revision / Last ACK。写操作（启停 / reconcile / 编辑 / 删除）都在这一页可完成，
 * 390px 下同样可操作。
 */
export default function PluginInstanceDetail() {
  const { t } = useTranslation('plugins')
  const { id = '' } = useParams()
  const key = decodeURIComponent(id)
  const { instance, loading, error } = usePluginInstance(key)
  usePageTitle(instance ? t('detail.title', { id: instance.id }) : t('detail.titleFallback'))

  const { plugins } = usePluginCatalog()
  const [editing, setEditing] = useState(false)
  const readOnly = useAuth((s) => s.status === 'in' && s.user?.role === 'viewer')

  const catalog = useMemo(
    () => (instance ? plugins.find((p) => p.id === instance.desired.plugin_id) : undefined),
    [plugins, instance],
  )

  if (loading) {
    return (
      <>
        <BackLink to="/plugins" label={t('detail.back')} />
        <Panel><RowSkeleton rows={5} /></Panel>
      </>
    )
  }

  if (!instance) {
    return (
      <>
        <BackLink to="/plugins" label={t('detail.back')} />
        {error ? (
          <ErrorState icon={<Boxes size={20} />} title={t('detail.loadFailed')}
            hint={t('detail.notFoundHint', { id: key })} />
        ) : (
          <EmptyState icon={<Boxes size={24} />} title={t('detail.notFound')}
            hint={t('detail.notFoundHint', { id: key })} />
        )}
      </>
    )
  }

  const status = instanceStatus(instance)
  const serverHosted = instance.edge_id === 'server'
  const isApplication = serverHosted || catalog?.kind === 'application'
  const lifecycleKey = JSON.stringify([instance.desired.revision, instance.desired.enabled,
    instance.has_observed, instance.observed?.state, instance.observed?.restart_count, instance.stale])

  const facts = (
    <div className="grid items-start gap-5 lg:grid-cols-3">
      <Panel className="lg:col-span-2"
        title={<span className="flex items-center gap-1.5"><Boxes size={14} />{t('detail.savedAndObserved')}</span>}>
        <InstanceSplit v={instance} />
      </Panel>

      <Panel title={<span className="flex items-center gap-1.5"><Settings2 size={14} />{t('detail.basicInfo')}</span>}>
        <InstanceFacts v={instance} catalog={catalog} />
      </Panel>

      <Panel title={<span className="flex items-center gap-1.5"><ShieldCheck size={14} />{t('detail.permissions')}</span>}
        right={catalog ? undefined : <Badge tone="idle">{t('detail.sourceUnavailable')}</Badge>}>
        <PermissionList
          permissions={catalog?.permissions}
          emptyHint={catalog
            ? t('detail.noExtraPermissions')
            : t('detail.permissionsUnavailable')}
        />
        {catalog?.verified && (
          <p className="mt-3 border-t border-hairline pt-3 text-meta leading-relaxed text-ink-3">
            {t('detail.verifiedPermissions')}
          </p>
        )}
      </Panel>

      <Panel title={<span className="flex items-center gap-1.5"><KeyRound size={14} />{t('detail.secrets')}</span>}>
        <SecretRefList refs={instance.desired.secret_refs} />
      </Panel>

      <Panel title={<span className="flex items-center gap-1.5"><SlidersHorizontal size={14} />{t('detail.settings')}</span>}>
        <ConfigTable config={instance.desired.config} />
        <p className="mt-3 border-t border-hairline pt-3 text-meta leading-relaxed text-ink-3">
          {t('detail.settingsHint')}
        </p>
      </Panel>
    </div>
  )

  return (
    <>
      <BackLink to="/plugins" label={t('detail.back')} />

      <header className="mb-5 flex flex-wrap items-center gap-2.5 fade-up">
        <h1 className="metric num min-w-0 max-w-full break-all text-hero font-semibold sm:truncate" title={instance.id}>
          {instance.desired.instance_id || instance.id}
        </h1>
        {/* 插件/节点 ID 是机器标识：mono 文本，不用胶囊（状态才配胶囊） */}
        <span className="flex min-w-0 items-center gap-1 text-meta text-ink-3"
          title={pluginDisplayName(catalog)}>
          <Puzzle size={11} className="shrink-0" />
          <span className="min-w-0 truncate">{pluginDisplayName(catalog)}</span>
        </span>
        {serverHosted ? <span className="flex items-center gap-1 text-meta text-ink-2"><Server size={13} />{t('detail.server')}</span> : <>
          <Link to={`/edges/${encodeURIComponent(instance.edge_id)}`}
            className="flex min-w-0 max-w-full items-center gap-1 font-mono text-micro text-ink-3 no-underline transition-colors hover:text-accent"
            title={t('detail.edgeTitle', { id: instance.edge_id })}>
            <Server size={11} className="shrink-0" />
            <span className="min-w-0 truncate">{t('detail.edge', { id: instance.edge_id || '—' })}</span>
          </Link>
          <Badge tone={instance.edge_online ? 'ok' : 'idle'}>
            {instance.edge_online ? t('detail.edgeOnline') : t('detail.edgeOffline')}
          </Badge>
        </>}
        <span className="ml-auto shrink-0"><Badge tone={status.tone}>{status.label}</Badge></span>
      </header>

      {editing && !readOnly ? (
        <Panel title={<span className="flex items-center gap-1.5"><SlidersHorizontal size={14} />{t('detail.editSettings')}</span>}
          className="mb-5">
          <InstanceForm mode="edit" instance={instance} catalog={plugins} onDone={() => setEditing(false)} />
        </Panel>
      ) : (
        <>
          <Panel className="mb-5" title={t('detail.currentStatus')}>
            <InstanceStatusSummary v={instance} />
          </Panel>

          <Panel className="mb-5" title={t('detail.actions')}>
            <InstanceControls v={instance} catalog={catalog} showEdit={false}
              onEdit={() => setEditing(true)} />
            {!readOnly && <button type="button" className="btn btn-ghost mt-3" onClick={() => setEditing(true)}>
              <SlidersHorizontal size={13} /> {t('detail.editSettings')}
            </button>}
          </Panel>

          {isApplication && instance.desired.instance_id && (
            <ApplicationPlane key={instance.id} instanceID={instance.desired.instance_id} lifecycleKey={lifecycleKey}
              desiredEnabled={instance.desired.enabled}
              runtimeState={!instance.has_observed || instance.stale
                ? 'unknown' : instance.observed?.state ?? 'unknown'} />
          )}

          <details className="min-w-0">
            <summary className="mb-4 flex min-h-touch cursor-pointer items-center text-body text-ink-2">{t('detail.showDetails')}</summary>
            {facts}
          </details>
        </>
      )}
    </>
  )
}
