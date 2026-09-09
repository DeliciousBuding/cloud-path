import { useMemo } from 'react'
import { Loader2 } from 'lucide-react'
import { SchemaActionInput } from '@/components/command/SchemaActionInput'
import { StructuredValue } from '@/components/StructuredValue'
import { useApplicationAction } from '@/hooks/useApplicationAction'
import { appActionError, appActionScope, appJobArgsError, appJobSchema, manualAppJobs } from '@/lib/application-actions'
import type { AppJobsView, AppJobView } from '@/lib/types'
import { useAuth } from '@/store/auth'

function ActionResult({ json }: { json: string }) {
  let value: unknown
  let valid = true
  try { value = JSON.parse(json) } catch { valid = false }
  return <div className="mt-3 min-w-0 space-y-3">
    {valid ? <StructuredValue value={value} /> : <p className="text-sm text-ink-2">{json
      ? '应用返回的内容无法直接展示，请查看原文。' : '应用未返回结果内容，请查看应用记录。'}</p>}
    {json && <details className="min-w-0">
      <summary className="flex min-h-11 cursor-pointer items-center text-xs text-ink-2">查看结果原文</summary>
      <pre tabIndex={0} role="group" aria-label="执行结果原文"
        className="num mt-2 max-h-64 overflow-auto whitespace-pre-wrap break-all rounded-lg bg-surface-2 p-3 font-mono text-xs">{json}</pre>
    </details>}
  </div>
}

function ActionForm({ instanceID, job, scope, schema, enabled }: {
  instanceID: string; job: AppJobView; scope: string; schema: Record<string, unknown>; enabled: boolean
}) {
  const { mutation, request, run, edit } = useApplicationAction(instanceID, job, schema, scope, enabled)
  const title = job.title?.trim() || job.id
  return <div className="min-w-0">
    <SchemaActionInput action={{ label: title, inputSchema: schema, inputPlaceholder: '按应用要求填写参数' }}
      validate={(args) => appJobArgsError(args, schema)} emptyArgs="{}"
      description="按应用要求填写参数。"
      emptyHint="填写参数后可执行。" validationSource="插件" disabled={!enabled || mutation.isPending} onEdit={edit}
      renderSubmit={(args, error) => <button type="button" className="btn btn-primary shrink-0"
        aria-label={(mutation.isError ? '重试' : mutation.isSuccess ? '再次执行' : '执行') + '「' + title + '」'}
        aria-busy={mutation.isPending} disabled={!enabled || Boolean(error) || mutation.isPending} onClick={() => run(args)}>
        {mutation.isPending && <Loader2 size={14} className="animate-spin" />}
        {mutation.isPending ? '等待执行结果…' : mutation.isError ? '再次尝试' : mutation.isSuccess ? '再次执行' : '执行操作'}
      </button>} />
    {mutation.isError && <div className="mt-3 min-w-0 space-y-2 rounded-lg border border-hairline p-3">
      <p role="alert" className="break-words text-sm text-bad">{appActionError(mutation.error)}</p>
      <p className="text-xs leading-relaxed text-ink-2">不会自动重试。请先查看应用记录；再次执行会沿用上次内容，修改参数后再执行会作为一次新的操作。</p>
      {mutation.error instanceof Error && mutation.error.message && <details className="min-w-0">
        <summary className="cursor-pointer text-xs text-ink-2">错误详情</summary>
        <p className="mt-2 whitespace-pre-wrap break-words text-xs text-ink-2 [overflow-wrap:anywhere]">{mutation.error.message}</p>
      </details>}
    </div>}
    {mutation.isSuccess && <div className="mt-3 min-w-0 rounded-lg border border-hairline p-4">
      <p role="status" className="text-sm font-medium">操作已受理</p>
      <p className="mt-1 text-xs leading-relaxed text-ink-3">以下为应用返回的结果，不一定代表设备已经执行。设备执行结果请在应用记录中核对。</p>
      <h4 className="mt-4 text-sm font-medium">执行结果</h4>
      <ActionResult json={mutation.data.result_json} />
    </div>}
    {request && <details className="mt-3 min-w-0">
      <summary className="flex min-h-11 cursor-pointer items-center text-xs text-ink-2">本次请求详情</summary>
      <p className="mt-2 break-all text-xs text-ink-2">请求标识：<code>{request.idempotency_key}</code></p>
      <pre tabIndex={0} role="group" aria-label="本次请求参数"
        className="num mt-2 max-h-48 overflow-auto whitespace-pre-wrap break-all rounded-lg bg-surface-2 p-3 font-mono text-xs">{request.args_json}</pre>
    </details>}
  </div>
}

function Action({ instanceID, job, scope, enabled }: {
  instanceID: string; job: AppJobView; scope: string | null; enabled: boolean
}) {
  const { schema, error } = useMemo(() => appJobSchema(job.input_schema_json), [job.input_schema_json])
  return <article aria-label={job.title?.trim() || job.id} className="min-w-0 py-4 first:pt-0 last:pb-0">
    {scope && schema ? <ActionForm instanceID={instanceID} job={job} scope={scope} schema={schema} enabled={enabled} />
      : <h3 className="break-words text-sm font-medium [overflow-wrap:anywhere]">{job.title?.trim() || job.id}</h3>}
    {error && <p role="alert" className="mt-2 text-xs text-bad">{error}</p>}
  </article>
}

export function ApplicationActions({ instanceID, jobs, running, conflict = false, desiredEnabled = true, lifecycleKey }: {
  instanceID: string; jobs: AppJobsView | undefined; running: boolean | undefined; conflict?: boolean; desiredEnabled?: boolean; lifecycleKey?: string
}) {
  const scope = useAuth((state) => appActionScope(state, instanceID))
  const actions = manualAppJobs(jobs?.instance_id === instanceID ? jobs.job_descriptors : undefined)
  return <div className="min-w-0">
    <p className="mb-3 text-xs leading-relaxed text-ink-3">这里显示可以手动执行的操作。后台定时任务会自动运行。</p>
    {!scope && <p className="mb-3 text-sm text-ink-2">当前账号只能查看，不能执行操作。</p>}
    {running !== true && <p className="mb-3 text-sm text-ink-2">{conflict
      ? '运行状态来源不一致，暂不能执行操作。'
      : running === false
        ? desiredEnabled ? '应用已停止，不能执行操作。' : '设置已停用，不能执行操作。'
        : '应用运行状态尚未确认，不能执行操作。'}</p>}
    {actions.length ? <div className="divide-y divide-hairline">{actions.map((job) => <Action
      key={JSON.stringify([scope, lifecycleKey, running === false, job])} instanceID={instanceID} job={job} scope={scope} enabled={running === true} />)}</div>
      : jobs && <p className="py-3 text-sm text-ink-3">暂无应用操作</p>}
  </div>
}
