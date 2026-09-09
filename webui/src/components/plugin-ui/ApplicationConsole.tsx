// Generic application console rendered from a plugin UI page.
//
// It reuses the existing Application Plane reads/actions; the UI page only
// decides which white-listed sections are shown and in what order.
import { Link } from 'react-router'
import { Panel } from '@/components/ui'
import { useApplicationPlane } from '@/hooks/useApplicationPlane'
import { applicationRunningState } from '@/lib/application-plane'
import { ApplicationSection } from './ApplicationSections'
import type { PluginCatalogView, PluginInstanceView, PluginUIPage } from '@/lib/types'

export function ApplicationConsole({ instance, catalog, page, readOnly, lifecycleKey }: {
  instance: PluginInstanceView
  catalog: PluginCatalogView
  page: PluginUIPage
  readOnly: boolean
  lifecycleKey: string
}) {
  const { records, bindings, jobs, presentation, status, running, canRead } = useApplicationPlane(
    instance.desired.instance_id, 0, '', lifecycleKey,
  )
  const runningState = applicationRunningState(running,
    !instance.has_observed || instance.stale ? 'unknown' : instance.observed?.state)
  const actionRunning = runningState === 'running' ? instance.desired.enabled
    : runningState === 'unknown' ? undefined : false

  if (!canRead) {
    return <Panel title="应用数据">
      <p className="text-body text-ink-2">登录后可查看当前组织的应用数据、设置和操作。</p>
      <Link to="/login" className="btn btn-ghost mt-3">前往登录</Link>
    </Panel>
  }

  return <div className="space-y-5" aria-label={page.title}>
    {!instance.desired.enabled && <div role="status" className="rounded-tile bg-warn/12 px-3.5 py-3 text-body text-warn">
      这个应用已停用。你仍可查看历史记录和设置；操作按钮不会执行。
    </div>}
    {instance.stale && <div role="status" className="rounded-tile bg-warn/12 px-3.5 py-3 text-body text-warn">
      当前运行状态已过期，页面以最近一次收到的状态为准。
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
      {status === 'open' ? '实时更新已连接' : status === 'connecting' ? '正在连接实时更新，暂以定时同步为准' : '实时更新已断开，暂以定时同步为准'}
    </p>
  </div>
}
