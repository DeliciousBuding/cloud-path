// 实例列表行：主路径只回答「是否运行、在哪里、是否异常、下一步做什么」，
// 版本号、状态原值等工程字段收进折叠的技术详情。
import { Link } from 'react-router'
import { useTranslation } from 'react-i18next'
import { ArrowRight, Boxes, Server } from 'lucide-react'
import { Badge, StatusDot } from '@/components/ui'
import { DesiredObserved, SyncBanner } from './DesiredObserved'
import { InstanceControls } from './InstanceControls'
import {
  healthMeta, instanceLocationLabel, instanceStatus, isolationLabel, pluginDisplayName, stateMeta,
} from '@/lib/plugins'
import { fmtDateTime } from '@/lib/format'
import type { PluginCatalogView, PluginInstanceView } from '@/lib/types'

export function InstanceRow({ v, catalog, onEdit }: {
  v: PluginInstanceView
  catalog?: PluginCatalogView
  onEdit?: () => void
}) {
  const { t } = useTranslation('plugin')
  const status = instanceStatus(v)
  const st = stateMeta(v.observed?.state)
  const hl = healthMeta(v.observed?.health)
  const serverHosted = v.edge_id === 'server'
  const hostLocation = instanceLocationLabel(v)

  return (
    <section className="card p-4 fade-up sm:p-5">
      <div className="flex min-w-0 flex-wrap items-center gap-2">
        <StatusDot online={status.key === 'normal'} />
        <Link to={`/plugins/${encodeURIComponent(v.id)}`}
          className="num inline-flex min-h-touch min-w-0 max-w-full items-center truncate text-body font-semibold tracking-[-0.01em] no-underline hover:text-accent sm:min-h-0"
          title={t('instance.rowTitle', { id: v.id })}>
          {v.desired.instance_id || v.id}
        </Link>
        <span className="flex min-w-0 items-center gap-1 text-meta text-ink-3"
          title={pluginDisplayName(catalog)}>
          <Boxes size={11} className="shrink-0" />
          <span className="min-w-0 truncate">{pluginDisplayName(catalog)}</span>
        </span>
        <span className="ml-auto shrink-0"><Badge tone={status.tone}>{status.label}</Badge></span>
      </div>

      <div className="mt-2 flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 text-meta text-ink-3">
        <span className="flex min-w-0 items-center gap-1">
          <Server size={11} className="shrink-0" />
          {serverHosted ? <span>{t('host.server')}</span> : (
            <Link to={`/edges/${encodeURIComponent(v.edge_id)}`}
              className="min-w-0 truncate no-underline transition-colors hover:text-accent"
              title={hostLocation}>{hostLocation}</Link>
          )}
        </span>
        {v.last_ack_at && <><span aria-hidden="true">·</span><span>{t('plane.updatedAt')} {fmtDateTime(v.last_ack_at)}</span></>}
      </div>

      <div className="mt-3 grid grid-cols-2 gap-2.5">
        <div className="min-w-0 rounded-tile bg-surface-2 px-3 py-2.5">
          <p className="text-meta font-medium text-ink-3">{t('desired.desired')}</p>
          <p className="mt-1 flex min-w-0 items-baseline gap-1 text-meta font-medium"
            title={`${v.desired.enabled ? t('desired.enabledValue') : t('desired.disabledValue')} · ${v.desired.version}`}>
            <span className="shrink-0">{v.desired.enabled ? t('desired.enabledValue') : t('desired.disabledValue')}</span>
            <span className="shrink-0 text-ink-3">·</span>
            <span className="num min-w-0 truncate">{v.desired.version || '—'}</span>
          </p>
        </div>
        <div className="min-w-0 rounded-tile bg-surface-2 px-3 py-2.5">
          <p className="text-meta font-medium text-ink-3">{t('desired.observed')}</p>
          {v.has_observed ? (
            <>
              <p className={`mt-1 break-words text-meta font-medium ${
                status.tone === 'ok' ? 'text-ok' : status.tone === 'bad' ? 'text-bad'
                  : status.tone === 'warn' ? 'text-warn' : ''}`}
                title={`${st.label} · ${v.observed?.version ?? t('instance.versionNotProvided')}`}>
                {status.summary}
              </p>
              <p className="mt-0.5 break-words text-meta text-ink-3">
                {v.observed?.health
                  ? (hl.tone === 'idle' ? t('instance.healthUnavailable') : t('instance.health', { label: hl.label }))
                  : t('instance.healthNotReported')}
                {v.stale ? t('instance.mayBeStale') : ''}
              </p>
            </>
          ) : (
            <>
              <p className="mt-1 truncate text-meta font-medium text-ink-2">{t('common.statusUnknown')}</p>
              <p className="mt-0.5 min-w-0 truncate text-meta text-ink-3">
                {serverHosted ? t('instance.noServerStatus') : v.edge_online ? t('instance.noEdgeStatusOnline') : t('instance.edgeOffline')}
              </p>
            </>
          )}
        </div>
      </div>

      {status.needsAttention && status.next && (
        <div className="mt-3 rounded-tile bg-warn/10 px-3 py-2.5 text-meta leading-relaxed text-ink-2">
          {t('desired.next', { text: status.next })}
        </div>
      )}

      <details className="mt-3 min-w-0 text-meta text-ink-2">
        <summary className="flex min-h-touch cursor-pointer items-center">{t('common.technicalDetails')}</summary>
        <dl className="mt-2 space-y-1 rounded-tile bg-surface-2 px-3 py-2.5">
          <div><dt className="inline">{t('facts.location')}：</dt><dd className="inline">{hostLocation}</dd></div>
          <div><dt className="inline">{t('facts.isolation')}：</dt><dd className="inline">{isolationLabel(v.desired.isolation)}</dd></div>
          <div><dt className="inline">{t('facts.lastUpdated')}：</dt><dd className="num inline">{v.last_ack_at ? fmtDateTime(v.last_ack_at) : t('instance.notUpdated')}</dd></div>
          <div><dt className="inline">{t('facts.pluginId')}：</dt><dd className="num inline break-all">{v.desired.plugin_id || '—'}</dd></div>
          <div><dt className="inline">{t('instance.currentVersion')}：</dt><dd className="num inline">{v.has_observed ? (v.observed?.version || t('instance.versionNotProvided')) : t('common.notReported')}</dd></div>
          <div><dt className="inline">{t('facts.expectedVersion')}</dt><dd className="num inline">{v.desired_revision}</dd></div>
          <div><dt className="inline">{t('facts.actualVersion')}</dt><dd className="num inline">{v.applied_revision}</dd></div>
          <div><dt className="inline">{t('plane.stateRaw')}</dt><dd className="num inline break-all">{v.observed?.state || '—'}</dd></div>
          <div><dt className="inline">{t('instance.healthRaw')}</dt><dd className="num inline break-all">{v.observed?.health || '—'}</dd></div>
        </dl>
      </details>

      <div className="mt-3 flex flex-wrap items-center gap-2 border-t border-hairline pt-3">
        <Link to={`/plugins/${encodeURIComponent(v.id)}`} className="btn btn-primary">
          {t('instance.viewDetails')} <ArrowRight size={13} />
        </Link>
        <InstanceControls v={v} catalog={catalog} onEdit={onEdit} variant="list" />
      </div>
    </section>
  )
}

/** 详情页用的完整分离视图（顶部同步条 + 双栏全字段） */
export function InstanceSplit({ v }: { v: PluginInstanceView }) {
  return (
    <div className="space-y-4">
      <SyncBanner v={v} />
      <DesiredObserved v={v} />
    </div>
  )
}
