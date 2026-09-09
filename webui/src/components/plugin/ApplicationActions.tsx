import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'
import { Loader2 } from 'lucide-react'
import { SchemaActionInput } from '@/components/command/SchemaActionInput'
import { useApplicationAction } from '@/hooks/useApplicationAction'
import { applicationResultSummary } from '@/lib/application-plane'
import { appActionError, appActionScope, appJobArgsError, appJobSchema, manualAppJobs } from '@/lib/application-actions'
import { commandHasInput } from '@/lib/command-schema'
import type { AppJobsView, AppJobView } from '@/lib/types'
import { useAuth } from '@/store/auth'

function ActionResult({ json }: { json: string }) {
  const { t } = useTranslation('plugin')
  let value: unknown
  let valid = true
  try { value = JSON.parse(json) } catch { valid = false }
  const summary = valid ? applicationResultSummary(value) : { text: undefined }
  return <div className="mt-3 min-w-0 space-y-3">
    {valid
      ? <p className="break-words text-sm leading-relaxed text-ink-2 [overflow-wrap:anywhere]">{
        summary.text || t('actions.resultFallback')
      }</p>
      : <p className="text-sm text-ink-2">{json
        ? t('actions.resultInvalid') : t('actions.resultMissing')}</p>}
    {json && <details className="min-w-0">
      <summary className="flex min-h-11 cursor-pointer items-center text-xs text-ink-2">{t('actions.viewRaw')}</summary>
      <pre tabIndex={0} role="group" aria-label={t('actions.rawAria')}
        className="num mt-2 max-h-64 overflow-auto whitespace-pre-wrap break-all rounded-lg bg-surface-2 p-3 font-mono text-xs">{json}</pre>
    </details>}
  </div>
}

function ActionForm({ instanceID, job, scope, schema, enabled, hasInput }: {
  instanceID: string; job: AppJobView; scope: string; schema: Record<string, unknown>; enabled: boolean; hasInput: boolean
}) {
  const { t } = useTranslation('plugin')
  const { mutation, request, run, edit } = useApplicationAction(instanceID, job, schema, scope, enabled)
  const title = job.title?.trim() || job.id
  return <div className="min-w-0">
    <SchemaActionInput action={{ label: title, inputSchema: schema, inputPlaceholder: t('actions.inputPlaceholder') }}
      validate={(args) => appJobArgsError(args, schema)} emptyArgs="{}"
      description={hasInput ? t('actions.inputDescription') : t('actions.noInputDescription')}
      emptyHint={hasInput ? t('actions.inputHint') : t('actions.noInputHint')}
      validationSource={t('actions.validationSource')} disabled={!enabled || mutation.isPending} onEdit={edit}
      renderSubmit={(args, error) => <button type="button" className="btn btn-primary shrink-0"
        aria-label={mutation.isError ? t('actions.retryAria', { title }) : mutation.isSuccess ? t('actions.againAria', { title }) : t('actions.runAria', { title })}
        aria-busy={mutation.isPending} disabled={!enabled || Boolean(error) || mutation.isPending} onClick={() => run(args)}>
        {mutation.isPending && <Loader2 size={14} className="animate-spin" />}
        {mutation.isPending ? t('actions.waiting') : mutation.isError ? t('actions.retry') : mutation.isSuccess ? t('actions.again') : t('actions.run')}
      </button>} />
    {mutation.isError && <div className="mt-3 min-w-0 space-y-2 rounded-lg border border-hairline p-3">
      <p role="alert" className="break-words text-sm text-bad">{appActionError(mutation.error)}</p>
      <p className="text-xs leading-relaxed text-ink-2">{t('actions.noAutoRetry')}</p>
      {mutation.error instanceof Error && mutation.error.message && <details className="min-w-0">
        <summary className="cursor-pointer text-xs text-ink-2">{t('actions.errorDetails')}</summary>
        <p className="mt-2 whitespace-pre-wrap break-words text-xs text-ink-2 [overflow-wrap:anywhere]">{mutation.error.message}</p>
      </details>}
    </div>}
    {mutation.isSuccess && <div className="mt-3 min-w-0 rounded-lg border border-hairline p-4">
      <p role="status" className="text-sm font-medium">{t('actions.accepted')}</p>
      <p className="mt-1 text-xs leading-relaxed text-ink-3">{t('actions.acceptedHint')}</p>
      <h4 className="mt-4 text-sm font-medium">{t('actions.result')}</h4>
      <ActionResult json={mutation.data.result_json} />
    </div>}
    {request && <details className="mt-3 min-w-0">
      <summary className="flex min-h-11 cursor-pointer items-center text-xs text-ink-2">{t('actions.requestDetails')}</summary>
      <p className="mt-2 break-all text-xs text-ink-2">{t('actions.requestId')}<code>{request.idempotency_key}</code></p>
      <pre tabIndex={0} role="group" aria-label={t('actions.requestArgsAria')}
        className="num mt-2 max-h-48 overflow-auto whitespace-pre-wrap break-all rounded-lg bg-surface-2 p-3 font-mono text-xs">{request.args_json}</pre>
    </details>}
  </div>
}

function Action({ instanceID, job, scope, enabled }: {
  instanceID: string; job: AppJobView; scope: string | null; enabled: boolean
}) {
  const { schema, error } = useMemo(() => appJobSchema(job.input_schema_json), [job.input_schema_json])
  const hasInput = useMemo(() => schema ? commandHasInput(schema) : false, [schema])
  return <article aria-label={job.title?.trim() || job.id} className="min-w-0 py-4 first:pt-0 last:pb-0">
    {scope && schema ? <ActionForm instanceID={instanceID} job={job} scope={scope} schema={schema} enabled={enabled} hasInput={hasInput} />
      : <h3 className="break-words text-sm font-medium [overflow-wrap:anywhere]">{job.title?.trim() || job.id}</h3>}
    {error && <p role="alert" className="mt-2 text-xs text-bad">{error}</p>}
  </article>
}

export function ApplicationActions({ instanceID, jobs, running, conflict = false, desiredEnabled = true, lifecycleKey }: {
  instanceID: string; jobs: AppJobsView | undefined; running: boolean | undefined; conflict?: boolean; desiredEnabled?: boolean; lifecycleKey?: string
}) {
  const { t } = useTranslation('plugin')
  const scope = useAuth((state) => appActionScope(state, instanceID))
  const actions = manualAppJobs(jobs?.instance_id === instanceID ? jobs.job_descriptors : undefined)
  return <div className="min-w-0">
    <p className="mb-3 text-xs leading-relaxed text-ink-3">{t('actions.intro')}</p>
    {!scope && <p className="mb-3 text-sm text-ink-2">{t('actions.readOnly')}</p>}
    {running !== true && <p className="mb-3 text-sm text-ink-2">{conflict
      ? t('actions.conflict')
      : running === false
        ? desiredEnabled ? t('actions.stopped') : t('actions.disabled')
        : t('actions.unknown')}</p>}
    {actions.length ? <div className="divide-y divide-hairline">{actions.map((job) => <Action
      key={JSON.stringify([scope, lifecycleKey, running === false, job])} instanceID={instanceID} job={job} scope={scope} enabled={running === true} />)}</div>
      : jobs && <p className="py-3 text-sm text-ink-3">{t('actions.empty')}</p>}
  </div>
}
