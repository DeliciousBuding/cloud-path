import type { AppBindingView, AppDomainRecordView, AppScheduledJobView, PluginInstanceView, UserView } from '@/lib/types'
import { stubResponse } from './http'

export const appUser: UserView = {
  id: 7, tenant_id: 1, username: 'viewer', name: '查看者', role: 'viewer', tenant_slug: 'example',
}
export const appBinding: AppBindingView = {
  requirement_id: 'input-source', capability: 'example.dev/capability/input@1', entity_id: 'input-1',
}
export const appSchedule: AppScheduledJobView = {
  schedule_id: 'recurring-check', cron: '30 8 * * *', timezone: 'Asia/Shanghai', missed_policy: 'skip',
  next_run_at: 1_800_000_000, last_run_at: 1_799_913_600, state: 'active', revision: 1,
}
export function appRecord(id = 'saved-1', data: unknown = { count: 2, custom_key: { message: '已保存内容', enabled: false } }): AppDomainRecordView {
  return { record_type: 'sample', record_id: id, data_json: JSON.stringify(data), version: '1', updated_at: 1_800_000_000 }
}
export function appInstance(instanceID = 'app-a', controlID = 'server/' + instanceID): PluginInstanceView {
  return {
    id: controlID, tenant_id: 1, edge_id: 'server',
    desired: { instance_id: instanceID, plugin_id: 'example.app', enabled: true, version: 'v1', isolation: 'shared', revision: 1, updated_at: 1_800_000_000, config: { app_config: '{"example_input":"input-1"}' } },
    has_observed: true, observed: { state: 'running', version: 'v1', health: 'HEALTHY', restart_count: 0 },
    edge_online: false, desired_revision: 1, applied_revision: 1, drift: false, stale: false,
  }
}
export const appPresentation = {
  descriptors: [{
    device_id: 'node-a/device-a', external_id: 'device-a', status: 'online', entities: [{
      entity_id: 'input-1', unique_key: 'input-1', category: 'sensor', name: '入口信号', capabilities: [appBinding.capability],
    }],
  }],
  capabilities: [{ metadata: { id: appBinding.capability, version: 1, title: '输入信号' }, spec: {} }],
}

/** 无行业语义的后端 HTTP 契约夹具，不接触任何真实服务。 */
export function appResponse(url: string, options: {
  records?: AppDomainRecordView[]; running?: boolean; scheduled?: AppScheduledJobView[]; instance?: PluginInstanceView
} = {}) {
  const path = new URL(url, 'http://localhost')
  const instance = options.instance ?? appInstance()
  const running = options.running ?? true
  const id = decodeURIComponent(path.pathname.split('/')[3] ?? '')
  if (path.pathname.endsWith('/records')) return stubResponse(200, {
    instance_id: id, records: options.records ?? [appRecord()], limit: 20,
    offset: Number(path.searchParams.get('offset') ?? 0), record_type: path.searchParams.get('record_type') || undefined,
  })
  if (path.pathname.endsWith('/bindings')) return stubResponse(200, { instance_id: id, running, bindings: running ? [appBinding] : [] })
  if (path.pathname.endsWith('/jobs')) return stubResponse(200, {
    instance_id: id, running, jobs: running ? ['minute-check'] : [], scheduled: options.scheduled ?? [appSchedule],
  })
  if (url === '/api/descriptors') return stubResponse(200, appPresentation)
  if (url === '/api/plugins') return stubResponse(200, { plugins: [{
    id: 'example.app', kind: 'application', version: 'v1', source: '', digest: '', verified: true,
    protocol: 1, permissions: {}, contributes: { applications: [{ id: 'example.app', title: '示例应用' }] },
  }] })
  if (url === '/api/plugin-instances') return stubResponse(200, { instances: [instance] })
  if (url === '/api/plugin-instances/' + encodeURIComponent(instance.id)) return stubResponse(200, instance)
  if (url === '/api/edges') return stubResponse(200, { edges: [] })
  return stubResponse(404, {})
}
