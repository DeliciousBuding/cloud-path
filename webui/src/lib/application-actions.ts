import { ApiError } from './api'
import { i18n } from '@/i18n'
import { schemaArgsError } from './command-schema'
import type { AuthState } from '@/store/auth'
import type { AppJobView } from './types'

export const APP_ACTION_MAX_BYTES = 4096
export const APP_ACTION_TIMEOUT_MS = 15_000

/** No open-mode or viewer fallback: application actions require a tenant-scoped operator. */
export function appActionScope({ status, user }: AuthState, instanceID: string): string | null {
  if (status !== 'in' || !user || user.disabled || !instanceID
    || !Number.isInteger(user.id) || user.id < 0 || !Number.isInteger(user.tenant_id) || user.tenant_id <= 0
    || (user.role !== 'operator' && user.role !== 'admin')) return null
  return JSON.stringify([user.tenant_id, user.tenant_slug, user.id, user.username, user.role, instanceID])
}

export function manualAppJobs(jobs: AppJobView[] | undefined): AppJobView[] {
  return Array.isArray(jobs) ? jobs.filter((job) => job?.manual_only === true && typeof job.id === 'string' && job.id.trim()) : []
}

export function appJobSchema(json: string): { schema?: Record<string, unknown>; error?: string } {
  if (typeof json !== 'string') return { error: i18n.t('plugin:actions.invalidSchema') }
  if (!json.trim()) return { schema: { type: 'object' } }
  try {
    const schema: unknown = JSON.parse(json)
    if (typeof schema === 'boolean') return { schema: { allOf: [schema] } }
    if (schema && typeof schema === 'object' && !Array.isArray(schema)) return { schema: schema as Record<string, unknown> }
  } catch { /* Invalid declarations must not become permissive forms. */ }
  return { error: i18n.t('plugin:actions.invalidSchema') }
}

/** Applications accept JSON objects, not the 64-byte/single-line device command wire format. */
export function appJobArgsError(args: string, schema: Record<string, unknown>): string | undefined {
  const bytes = new TextEncoder().encode(args).length
  if (bytes > APP_ACTION_MAX_BYTES) return i18n.t('plugin:actions.argsTooLong', { bytes, limit: APP_ACTION_MAX_BYTES })
  return schemaArgsError(args, { type: 'object', allOf: [schema] })
}

export function appActionError(error: unknown): string {
  if (error instanceof ApiError) {
    switch (error.status) {
      case 400: return i18n.t('plugin:actions.errors.badRequest')
      case 401: return i18n.t('plugin:actions.errors.unauthorized')
      case 403: return i18n.t('plugin:actions.errors.forbidden')
      case 404: return i18n.t('plugin:actions.errors.notFound')
      case 409: return i18n.t('plugin:actions.errors.conflict')
      case 429: return error.retryAfter
        ? i18n.t('plugin:actions.errors.rateLimitedAfter', { seconds: error.retryAfter })
        : i18n.t('plugin:actions.errors.rateLimited')
      case 503: return i18n.t('plugin:actions.errors.unavailable')
    }
  }
  return error instanceof Error && error.name === 'TimeoutError'
    ? i18n.t('plugin:actions.errors.timeout') : i18n.t('plugin:actions.errors.unknown')
}
