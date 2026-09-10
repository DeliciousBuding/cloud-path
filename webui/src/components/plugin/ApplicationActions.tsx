import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'
import { ChevronRight } from 'lucide-react'
import { SchemaActionInput } from '@/components/command/SchemaActionInput'
import { Button } from '@/components/ui'
import { useApplicationAction } from '@/hooks/useApplicationAction'
import { applicationResultSummary } from '@/lib/application-plane'
import { appActionError, appActionScope, appJobArgsError, appJobSchema, manualAppJobs } from '@/lib/application-actions'
import { commandHasMeaningfulInput } from '@/lib/command-schema'
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
      ? <p className="break-words text-body leading-relaxed text-ink-2 [overflow-wrap:anywhere]">{
        summary.text || t('actions.resultFallback')
      }</p>
      : <p className="text-body text-ink-2">{json
        ? t('actions.resultInvalid') : t('actions.resultMissing')}</p>}
    {json && <details className="min-w-0">
      <summary className="flex min-h-touch cursor-pointer items-center text-meta text-ink-2">{t('actions.viewRaw')}</summary>
      <pre tabIndex={0} role="group" aria-label={t('actions.rawAria')}
        className="num mt-2 max-h-64 overflow-auto whitespace-pre-wrap break-all rounded-tile bg-surface-2 p-3 font-mono text-meta">{json}</pre>
    </details>}
  </div>
}

function ActionForm({ instanceID, job, scope, schema, enabled, compact = false, submitLabel }: {
  instanceID: string; job: AppJobView; scope: string; schema: Record<string, unknown>; enabled: boolean
  compact?: boolean; submitLabel?: string
}) {
  const { t } = useTranslation('plugin')
  const { mutation, request, run, edit } = useApplicationAction(instanceID, job, schema, scope, enabled)
  const title = job.title?.trim() || job.id
  const idleLabel = submitLabel ?? t('actions.run')
  return <div className="min-w-0">
    <SchemaActionInput action={{ label: title, inputSchema: schema, inputPlaceholder: t('actions.inputPlaceholder') }}
      validate={(args) => appJobArgsError(args, schema)} emptyArgs="{}" showTitle={false}
      description={compact ? t('actions.noInputDescription') : t('actions.inputDescription')}
      emptyHint={t('actions.noInputHint')}
      validationSource={t('actions.validationSource')} disabled={!enabled || mutation.isPending} onEdit={edit}
      renderSubmit={(args, error) => <Button className="shrink-0" loading={mutation.isPending}
        aria-label={mutation.isError ? t('actions.retryAria', { title }) : mutation.isSuccess ? t('actions.againAria', { title }) : t('actions.runAria', { title })}
        disabled={!enabled || Boolean(error)} onClick={() => run(args)}>
        {submitLabel ? idleLabel : mutation.isPending ? t('actions.waiting') : mutation.isError ? t('actions.retry') : mutation.isSuccess ? t('actions.again') : t('actions.run')}
      </Button>} />
    {mutation.isError && <div className="mt-3 min-w-0 space-y-2 rounded-tile border border-hairline p-3">
      <p role="alert" className="break-words text-body text-bad">{appActionError(mutation.error)}</p>
      <p className="text-meta leading-relaxed text-ink-2">{t('actions.noAutoRetry')}</p>
      {mutation.error instanceof Error && mutation.error.message && <details className="min-w-0">
        <summary className="cursor-pointer text-meta text-ink-2">{t('actions.errorDetails')}</summary>
        <p className="mt-2 whitespace-pre-wrap break-words text-meta text-ink-2 [overflow-wrap:anywhere]">{mutation.error.message}</p>
      </details>}
    </div>}
    {mutation.isSuccess && <div className="mt-3 min-w-0 rounded-tile border border-hairline p-4">
      <p role="status" className="text-body font-medium">{t('actions.accepted')}</p>
      <p className="mt-1 text-meta leading-relaxed text-ink-3">{t('actions.acceptedHint')}</p>
      <h4 className="mt-4 text-body font-medium">{t('actions.result')}</h4>
      <ActionResult json={mutation.data.result_json} />
    </div>}
    {request && <details className="mt-3 min-w-0">
      <summary className="flex min-h-touch cursor-pointer items-center text-meta text-ink-2">{t('actions.requestDetails')}</summary>
      <p className="mt-2 break-all text-meta text-ink-2">{t('actions.requestId')}<code>{request.idempotency_key}</code></p>
      <pre tabIndex={0} role="group" aria-label={t('actions.requestArgsAria')}
        className="num mt-2 max-h-48 overflow-auto whitespace-pre-wrap break-all rounded-tile bg-surface-2 p-3 font-mono text-meta">{request.args_json}</pre>
    </details>}
  </div>
}

function Action({ instanceID, job, scope, enabled }: {
  instanceID: string; job: AppJobView; scope: string | null; enabled: boolean
}) {
  const { t } = useTranslation('plugin')
  const { schema, error } = useMemo(() => appJobSchema(job.input_schema_json), [job.input_schema_json])
  const needsInput = useMemo(() => schema ? commandHasMeaningfulInput(schema) : false, [schema])
  const title = job.title?.trim() || job.id
  if (!scope || !schema) return <article className="min-w-0 py-4 first:pt-0 last:pb-0">
    <h3 className="break-words text-body font-medium [overflow-wrap:anywhere]">{title}</h3>
    {error && <p role="alert" className="mt-2 text-meta text-bad">{error}</p>}
  </article>
  // 无需填写参数的操作：按钮本身就是操作名，不再额外挂一个同名标题。
  if (!needsInput) return <article aria-label={title} className="min-w-0 py-3 first:pt-0 last:pb-0">
    <ActionForm instanceID={instanceID} job={job} scope={scope} schema={schema} enabled={enabled}
      submitLabel={title} compact />
    {error && <p role="alert" className="mt-2 text-meta text-bad">{error}</p>}
  </article>
  return <article aria-label={title} className="min-w-0 py-4 first:pt-0 last:pb-0">
    <h3 className="break-words text-body font-medium [overflow-wrap:anywhere]">{title}</h3>
    <p className="mt-1 text-meta text-ink-3">{t('actions.parametersNeeded')}</p>
    <details className="group mt-3 min-w-0 rounded-tile border border-hairline bg-surface-2/60">
      <summary className="flex min-h-touch cursor-pointer list-none items-center gap-1.5 px-3.5 py-2.5 text-meta font-medium text-ink-2">
        <ChevronRight size={13} className="shrink-0 transition-transform group-open:rotate-90" />
        {t('actions.openParameters')}
      </summary>
      <div className="border-t border-hairline px-3.5 py-3">
        <ActionForm instanceID={instanceID} job={job} scope={scope} schema={schema} enabled={enabled} submitLabel={title} />
      </div>
    </details>
    {error && <p role="alert" className="mt-2 text-meta text-bad">{error}</p>}
  </article>
}

export function ApplicationActions({ instanceID, jobs, emptyText, running, conflict = false, desiredEnabled = true, lifecycleKey }: {
  instanceID: string; jobs: AppJobsView | undefined; emptyText?: string; running: boolean | undefined; conflict?: boolean; desiredEnabled?: boolean; lifecycleKey?: string
}) {
  const { t } = useTranslation('plugin')
  const scope = useAuth((state) => appActionScope(state, instanceID))
  const actions = manualAppJobs(jobs?.instance_id === instanceID ? jobs.job_descriptors : undefined)
  return <div className="min-w-0">
    <p className="mb-3 text-meta leading-relaxed text-ink-3">{t('actions.intro')}</p>
    {!scope && <p className="mb-3 text-body text-ink-2">{t('actions.readOnly')}</p>}
    {running !== true && <p className="mb-3 text-body text-ink-2">{conflict
      ? t('actions.conflict')
      : running === false
        ? desiredEnabled ? t('actions.stopped') : t('actions.disabled')
        : t('actions.unknown')}</p>}
    {actions.length ? <div className="divide-y divide-hairline">{actions.map((job) => <Action
      key={JSON.stringify([scope, lifecycleKey, running === false, job])} instanceID={instanceID} job={job} scope={scope} enabled={running === true} />)}</div>
      : jobs && <p className="py-3 text-body text-ink-3">{emptyText?.trim() || t('actions.empty')}</p>}
  </div>
}
