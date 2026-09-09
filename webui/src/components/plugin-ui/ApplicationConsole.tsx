// Generic application console rendered from a plugin UI page.
//
// It reuses the existing Application Plane reads/actions; the UI page only
// decides which white-listed sections are shown and in what order.
import { Link } from 'react-router'
import { useTranslation } from 'react-i18next'
import { Panel } from '@/components/ui'
import { useApplicationPlane } from '@/hooks/useApplicationPlane'
import { applicationRunningState } from '@/lib/application-plane'
import { resolveLocalizedText } from '@/i18n/pluginText'
import { ApplicationSection } from './ApplicationSections'
import type { PluginCatalogView, PluginInstanceView, PluginUIPage } from '@/lib/types'

export function ApplicationConsole({ instance, catalog, page, readOnly, lifecycleKey }: {
  instance: PluginInstanceView
  catalog: PluginCatalogView
  page: PluginUIPage
  readOnly: boolean
  lifecycleKey: string
}) {
  const { t, i18n } = useTranslation('plugin')
  const { records, bindings, jobs, presentation, status, running, canRead } = useApplicationPlane(
    instance.desired.instance_id, 0, '', lifecycleKey,
  )
  const pageTitle = resolveLocalizedText(page, 'title', i18n.resolvedLanguage ?? i18n.language) ?? page.title
  const runningState = applicationRunningState(running,
    !instance.has_observed || instance.stale ? 'unknown' : instance.observed?.state)
  const actionRunning = runningState === 'running' ? instance.desired.enabled
    : runningState === 'unknown' ? undefined : false

  if (!canRead) {
    return <Panel title={t('console.appData')}>
      <p className="text-body text-ink-2">{t('console.loginHint')}</p>
      <Link to="/login" className="btn btn-ghost mt-3">{t('console.login')}</Link>
    </Panel>
  }

  return <div className="space-y-5" aria-label={pageTitle}>
    {!instance.desired.enabled && <div role="status" className="rounded-tile bg-warn/12 px-3.5 py-3 text-body text-warn">
      {t('console.disabled')}
    </div>}
    {instance.stale && <div role="status" className="rounded-tile bg-warn/12 px-3.5 py-3 text-body text-warn">
      {t('console.stale')}
    </div>}
    {page.sections.map((section, index) => <ApplicationSection
      key={`${section.type}:${index}`}
      instance={instance}
      catalog={catalog}
      section={section}
      records={records}
      bindings={bindings}
      jobs={jobs}
      presentation={presentation}
      running={actionRunning}
      readOnly={readOnly}
      lifecycleKey={lifecycleKey}
    />)}
    <p role="status" className="text-meta text-ink-3">
      {status === 'open' ? t('console.realtimeOpen') : status === 'connecting' ? t('console.realtimeConnecting') : t('console.realtimeClosed')}
    </p>
  </div>
}
