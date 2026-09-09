import { useEffect, useRef, useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import '@/i18n'
import { api } from '@/lib/api'
import { APP_ACTION_TIMEOUT_MS, appActionScope, appJobArgsError } from '@/lib/application-actions'
import type { AppJobRunRequest, AppJobView } from '@/lib/types'
import { useAuth } from '@/store/auth'

/** One mounted form owns one logical request; no retry, offline replay, or cross-scope result. */
export function useApplicationAction(instanceID: string, job: AppJobView, schema: Record<string, unknown>, scope: string, enabled: boolean) {
  const qc = useQueryClient()
  const { t } = useTranslation('plugin')
  const active = useRef(true)
  const sending = useRef(false)
  const controller = useRef<AbortController | null>(null)
  const logicalRequest = useRef<AppJobRunRequest | null>(null)
  const [request, setRequest] = useState<AppJobRunRequest | null>(null)
  const current = () => active.current && appActionScope(useAuth.getState(), instanceID) === scope

  useEffect(() => {
    active.current = true
    // Abort synchronously on identity changes so even a late 401 cannot sign out the next user.
    const unsubscribe = useAuth.subscribe((state) => {
      if (appActionScope(state, instanceID) !== scope) controller.current?.abort()
    })
    return () => { active.current = false; unsubscribe(); controller.current?.abort() }
  }, [instanceID, scope])

  const mutation = useMutation({
    retry: false, networkMode: 'always', gcTime: 0,
    mutationFn: async (body: AppJobRunRequest) => {
      if (!current()) throw new Error(t('actions.contextChanged'))
      const abort = new AbortController()
      controller.current = abort
      const timeout = new Error(t('actions.timeoutDetail'))
      timeout.name = 'TimeoutError'
      const timer = setTimeout(() => abort.abort(timeout), APP_ACTION_TIMEOUT_MS)
      try {
        const result = await api.runAppJob(instanceID, job.id, body, abort.signal)
        abort.signal.throwIfAborted()
        if (!result || result.instance_id !== instanceID || result.job_id !== job.id || typeof result.result_json !== 'string') {
          throw new Error(t('actions.responseMismatch'))
        }
        return result
      } finally {
        clearTimeout(timer)
        if (controller.current === abort) controller.current = null
      }
    },
    onSettled: () => {
      sending.current = false
      if (!current()) return
      const state = useAuth.getState()
      // Failure/timeout can still have produced records. REST remains their only source of truth.
      void qc.invalidateQueries({ queryKey: ['application-plane', state.status, state.user?.tenant_id ?? null, state.user?.id ?? null, instanceID] })
    },
  })

  const edit = () => {
    if (sending.current) return
    logicalRequest.current = null
    setRequest(null)
    mutation.reset()
  }
  const run = (args: string) => {
    if (!enabled || sending.current || !current() || !job.manual_only || appJobArgsError(args, schema)) return
    // View switches preserve the exact previous payload. Every actual edit, even edit-and-undo, resets it.
    const body = !mutation.isSuccess && logicalRequest.current ? logicalRequest.current
      : { args_json: args, idempotency_key: Array.from(crypto.getRandomValues(new Uint8Array(16)), (byte) => byte.toString(16).padStart(2, '0')).join('') }
    logicalRequest.current = body
    setRequest(body)
    sending.current = true
    mutation.mutate(body)
  }
  return { mutation, request, run, edit }
}
