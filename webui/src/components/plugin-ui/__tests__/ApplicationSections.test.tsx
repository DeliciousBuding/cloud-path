import { screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { ApplicationSection } from '@/components/plugin-ui/ApplicationSections'
import { renderWithProviders } from '@/test/render'
import type { AppDomainRecordView, AppDomainRecordsView, PluginCatalogView, PluginInstanceView, PluginUISection } from '@/lib/types'

const instance: PluginInstanceView = {
  id: 'server/app-a', tenant_id: 1, edge_id: 'server',
  desired: { instance_id: 'app-a', plugin_id: 'example.app', version: 'v1', enabled: true, isolation: 'shared', revision: 1, updated_at: 1 },
  has_observed: true, observed: { state: 'running', health: 'HEALTHY', restart_count: 0 },
  edge_online: false, desired_revision: 1, applied_revision: 1, drift: false, stale: false,
}
const catalog: PluginCatalogView = {
  id: 'example.app', kind: 'application', version: 'v1', source: '', digest: '', verified: true,
  protocol: 1, permissions: {}, contributes: { applications: [{ id: 'example.app', title: '示例应用' }] },
}
function record(type: string, id: string, data: unknown, updated_at: number): AppDomainRecordView {
  return { record_type: type, record_id: id, data_json: JSON.stringify(data), updated_at }
}
function query<T>(data: T) {
  return { data, isPending: false, isError: false, error: null, isFetching: false, refetch: () => undefined }
}
function renderSection(section: PluginUISection, records: AppDomainRecordView[]) {
  const recordsData: AppDomainRecordsView = { instance_id: 'app-a', records, limit: 20, offset: 0 }
  return renderWithProviders(<ApplicationSection
    instance={instance} catalog={catalog} section={section}
    records={query(recordsData)} bindings={query({ instance_id: 'app-a', running: true, bindings: [] })}
    jobs={query({ instance_id: 'app-a', running: true, jobs: [], scheduled: [], job_descriptors: [] })}
    readOnly={false} lifecycleKey="1"
  />)
}

describe('Application section recordType isolation', () => {
  it('records/timeline only renders the declared recordType', () => {
    renderSection({ type: 'timeline', recordType: 'service_call' }, [
      record('press', 'press-1', { title: '按键事件' }, 3),
      record('service_call', 'call-1', { title: '工位呼叫' }, 2),
      record('heartbeat', 'heartbeat-1', { title: '心跳事件' }, 1),
    ])
    expect(screen.getByText('工位呼叫')).toBeInTheDocument()
    expect(screen.queryByText('按键事件')).not.toBeInTheDocument()
    expect(screen.queryByText('心跳事件')).not.toBeInTheDocument()
  })

  it('custom sections use the catalog version before the instance version', () => {
    const versionedCatalog = { ...catalog, version: 'v2.0.0' }
    renderWithProviders(<ApplicationSection
      instance={instance} catalog={versionedCatalog} section={{ type: 'custom', entry: 'ui/index.html', scopes: [] }}
      records={query({ instance_id: 'app-a', records: [], limit: 20, offset: 0 })}
      bindings={query({ instance_id: 'app-a', running: true, bindings: [] })}
      jobs={query({ instance_id: 'app-a', running: true, jobs: [], scheduled: [], job_descriptors: [] })}
      readOnly lifecycleKey="1"
    />)
    expect(screen.getByTitle('插件自定义界面')).toHaveAttribute('src', '/api/plugin-ui/assets/example.app/v2.0.0/ui/index.html')
  })

  it('actions only render manual-only jobs', () => {
    renderWithProviders(<ApplicationSection
      instance={instance} catalog={catalog} section={{ type: 'actions', source: 'manual-jobs' }}
      records={query({ instance_id: 'app-a', records: [], limit: 20, offset: 0 })}
      bindings={query({ instance_id: 'app-a', running: true, bindings: [] })}
      jobs={query({ instance_id: 'app-a', running: true, jobs: ['manual-job', 'auto-job'], scheduled: [], job_descriptors: [
        { id: 'manual-job', title: '手动执行', input_schema_json: '{}', manual_only: true },
        { id: 'auto-job', title: '自动任务', input_schema_json: '{}', manual_only: false },
      ] })}
      readOnly={false} lifecycleKey="1"
    />)
    expect(screen.getByText('手动执行')).toBeInTheDocument()
    expect(screen.queryByText('自动任务')).not.toBeInTheDocument()
  })

  it('metrics filters by recordType before taking the latest record', () => {
    renderSection({ type: 'metrics', recordType: 'sensor' }, [
      record('other', 'other-1', { temperature_c: 999 }, 3),
      record('sensor', 'sensor-1', { temperature_c: 21 }, 2),
    ])
    expect(screen.getByText('21')).toBeInTheDocument()
    expect(screen.queryByText('999')).not.toBeInTheDocument()
  })
})
