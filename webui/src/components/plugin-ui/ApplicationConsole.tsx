// Generic application console rendered from a plugin UI page.
//
// It reuses the existing Application Plane reads/actions; the UI page only
// decides which white-listed sections are shown and in what order.
import { useTranslation } from 'react-i18next'
import { ButtonLink, Panel } from '@/components/ui'
import { useApplicationPlane } from '@/hooks/useApplicationPlane'
import { applicationRunningState } from '@/lib/application-plane'
import { resolveLocalizedText } from '@/i18n/pluginText'
import { ApplicationSection, ApplicationStatusStrip } from './ApplicationSections'
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
      <ButtonLink to="/login" variant="ghost" className="mt-3">{t('console.login')}</ButtonLink>
    </Panel>
  }

  // 页面顶部只留一条权威状态。声明里的 `status` 区块不再单独渲染，
  // 否则同一件事会出现两遍，而且互相矛盾（例如「运行中」和「尚未收到」同时出现）。
  const visible = page.sections
    .map((section, index) => ({ section, index }))
    .filter((item) => item.section.type !== 'status')
  const isAdvanced = ({ type, source }: typeof page.sections[number]) => type === 'form' || type === 'diagnostics'
    || (type === 'table' && source === 'bindings')
  const main = visible.filter((item) => !isAdvanced(item.section))
  const advanced = visible.filter((item) => isAdvanced(item.section))
  const renderSection = ({ section, index }: typeof visible[number]) => <ApplicationSection
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
  />

  return <div className="space-y-5" aria-label={pageTitle}>
    <ApplicationStatusStrip instance={instance} />
    {main.map(renderSection)}
    {advanced.length > 0 && <details className="min-w-0 rounded-panel border border-hairline bg-surface px-4 py-3.5">
      <summary className="flex min-h-touch cursor-pointer list-none items-center justify-between gap-3 text-compact font-medium">
        <span>{t('console.advanced')}</span>
        <span className="text-meta font-normal text-ink-3">{t('console.advancedHint')}</span>
      </summary>
      <div className="mt-4 space-y-5">{advanced.map(renderSection)}</div>
    </details>}
    <p role="status" className="text-meta text-ink-3">
      {status === 'open' ? t('console.realtimeOpen') : status === 'connecting' ? t('console.realtimeConnecting') : t('console.realtimeClosed')}
    </p>
  </div>
}
