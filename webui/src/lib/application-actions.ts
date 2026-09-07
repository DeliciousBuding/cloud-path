import { ApiError } from './api'
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
  if (typeof json !== 'string') return { error: '操作参数声明无效，暂不能执行。' }
  if (!json.trim()) return { schema: { type: 'object' } }
  try {
    const schema: unknown = JSON.parse(json)
    if (typeof schema === 'boolean') return { schema: { allOf: [schema] } }
    if (schema && typeof schema === 'object' && !Array.isArray(schema)) return { schema: schema as Record<string, unknown> }
  } catch { /* Invalid declarations must not become permissive forms. */ }
  return { error: '操作参数声明无效，暂不能执行。' }
}

/** Applications accept JSON objects, not the 64-byte/single-line device command wire format. */
export function appJobArgsError(args: string, schema: Record<string, unknown>): string | undefined {
  const bytes = new TextEncoder().encode(args).length
  if (bytes > APP_ACTION_MAX_BYTES) return '参数 ' + bytes + ' UTF-8 字节，超过 ' + APP_ACTION_MAX_BYTES + ' 字节上限'
  return schemaArgsError(args, { type: 'object', allOf: [schema] })
}

export function appActionError(error: unknown): string {
  if (error instanceof ApiError) {
    switch (error.status) {
      case 400: return '参数未被接受，请核对输入与插件返回的说明。'
      case 401: return '登录已失效，请重新登录后查看应用记录。'
      case 403: return '没有执行权限，请核对账号角色与访问权限。'
      case 404: return '应用操作或目标不存在，声明可能已变更。'
      case 409: return '操作发生冲突，请先核对应用记录与当前状态。'
      case 429: return error.retryAfter ? '操作过于频繁，请 ' + error.retryAfter + ' 秒后再试。' : '操作过于频繁，请稍后再试。'
      case 503: return '执行结果待确认，请先查看应用记录。'
    }
  }
  return error instanceof Error && error.name === 'TimeoutError'
    ? '请求超时，执行结果待确认。' : '未取得可信的执行结果，请先查看应用记录。'
}
