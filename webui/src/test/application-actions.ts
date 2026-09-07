import type { AppJobView, AppJobRunRequest } from '@/lib/types'
import { appResponse } from './application-plane'
import { stubResponse } from './http'
import type { FetchStub } from './http'

export const fieldJob: AppJobView = {
  id: 'update-count', title: '更新计数', manual_only: true,
  input_schema_json: JSON.stringify({ type: 'object', additionalProperties: false,
    required: ['count'], properties: {
      count: { type: 'integer', title: '次数', minimum: 1, maximum: 8 },
      note: { type: 'string', title: '备注', default: '不会代填的默认值' },
    },
  }),
}
export const emptyJob: AppJobView = {
  id: 'refresh-records', title: '刷新记录', manual_only: true,
  input_schema_json: JSON.stringify({ type: 'object', additionalProperties: false, properties: {} }),
}
export function actionResponse(url: string, options: {
  descriptors?: AppJobView[] | null; running?: boolean; instanceID?: string
} = {}) {
  const pathname = new URL(url, 'http://localhost').pathname
  const instanceID = options.instanceID ?? decodeURIComponent(pathname.split('/')[3] ?? '')
  if (pathname.endsWith('/jobs')) return stubResponse(200, {
    instance_id: instanceID, running: options.running ?? true, jobs: ['background-check'], scheduled: [],
    job_descriptors: options.descriptors === null ? undefined : options.descriptors ?? [fieldJob],
  })
  return appResponse(url, { running: options.running })
}
export function actionRequests(http: FetchStub): AppJobRunRequest[] {
  return http.calls.filter((call) => call.method === 'POST' && call.url.endsWith('/run')).map((call) => call.body as AppJobRunRequest)
}
export function accepted(instanceID = 'app-a', jobID = fieldJob.id, result: unknown = { status: 'accepted', count: 2 }) {
  return stubResponse(200, { instance_id: instanceID, job_id: jobID, result_json: JSON.stringify(result) })
}
