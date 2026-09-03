// REST 客户端：TanStack Query 的 queryFn 全走这里。
// 鉴权接缝（docs/api.md §2）：会话 cookie 同源自动携带（fetch credentials:'same-origin'）
// + 可选 Bearer 服务令牌（localStorage）。登录态事实源 = GET /api/auth/me（200 已登录 / 401 未登录）。
// 任何受保护端点返回 401 → markUnauthenticated() 全局收敛（store/auth.ts → 路由守卫跳 /login）。
import type {
  AdapterView, CommandView, DeviceView, EdgeView, EventView, HealthView, MeResponse, StatsView, UserView,
} from './types'
import { markUnauthenticated } from '@/store/auth'

const TOKEN_KEY = 'cloudpath.token'

export function getToken(): string {
  try {
    return localStorage.getItem(TOKEN_KEY) ?? ''
  } catch {
    return '' // 隐私模式下 localStorage 可能抛错
  }
}

export function setToken(v: string) {
  try {
    if (v) localStorage.setItem(TOKEN_KEY, v)
    else localStorage.removeItem(TOKEN_KEY)
  } catch { /* 忽略：令牌只影响鉴权，不影响本地展示 */ }
}

/** 带 HTTP 状态码的请求错误（401→全局登出收敛；429→Retry-After 提示） */
export class ApiError extends Error {
  readonly status: number
  readonly retryAfter?: number
  constructor(status: number, message: string, retryAfter?: number) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.retryAfter = retryAfter
  }
}

interface ReqOptions {
  /** 公开端点（healthz 与 /api/auth/* 族）：401 属正常语义，不触发全局登出收敛 */
  public?: boolean
  /** 204 无响应体 */
  allowEmpty?: boolean
}

function parseRetryAfter(res: Response): number | undefined {
  const v = Number(res.headers.get('Retry-After'))
  return Number.isFinite(v) && v > 0 ? v : undefined
}

async function req<T>(path: string, init?: RequestInit, opts?: ReqOptions): Promise<T> {
  const headers: Record<string, string> = {
    'Content-Type': 'application/json',
    ...(init?.headers as Record<string, string> | undefined),
  }
  const t = getToken()
  if (t) headers['Authorization'] = `Bearer ${t}`
  let res: Response
  try {
    // 会话 cookie（cp_session）由浏览器同源自动携带；显式声明以固定语义
    res = await fetch(path, { ...init, headers, credentials: 'same-origin' })
  } catch {
    throw new Error('无法连接 server（服务未启动或网络不可达）')
  }
  if (!res.ok) {
    let msg = `${res.status} ${res.statusText}`
    try {
      const j = await res.json()
      if (j?.error) msg = j.error
    } catch { /* 保持状态码信息 */ }
    if (res.status === 401 && !opts?.public) markUnauthenticated()
    throw new ApiError(res.status, msg, parseRetryAfter(res))
  }
  if (opts?.allowEmpty || res.status === 204) return undefined as T
  return res.json() as Promise<T>
}

function qs(params: Record<string, string | number | undefined>): string {
  const q = new URLSearchParams()
  for (const [k, v] of Object.entries(params)) {
    if (v === undefined || v === '' || v === null) continue
    q.set(k, String(v))
  }
  const s = q.toString()
  return s ? `?${s}` : ''
}

export const api = {
  // 认证族（docs/api.md §2.2）：公开端点，401/409/429 是页面语义，不触发全局收敛
  me: () => req<MeResponse>('/api/auth/me', undefined, { public: true }),
  login: (username: string, password: string) =>
    req<{ user: UserView }>('/api/auth/login', {
      method: 'POST',
      body: JSON.stringify({ username, password }),
    }, { public: true }),
  setup: (username: string, password: string) =>
    req<{ user: UserView }>('/api/auth/setup', {
      method: 'POST',
      body: JSON.stringify({ username, password }),
    }, { public: true }),
  logout: () => req<void>('/api/auth/logout', { method: 'POST' }, { public: true, allowEmpty: true }),

  health: () => req<HealthView>('/healthz', undefined, { public: true }),
  stats: () => req<StatsView>('/api/stats'),
  adapters: () => req<{ adapters: AdapterView[] }>('/api/adapters'),
  devices: () => req<{ devices: DeviceView[] }>('/api/devices'),
  device: (edgeId: string, devId: string) =>
    req<DeviceView>(`/api/devices/${encodeURIComponent(edgeId)}/${encodeURIComponent(devId)}`),
  events: (params?: { device?: string; since?: number; limit?: number }) =>
    req<{ events: EventView[] }>(`/api/events${qs(params ?? {})}`),
  edges: () => req<{ edges: EdgeView[] }>('/api/edges'),
  commands: (params?: { device?: string; status?: string; limit?: number }) =>
    req<{ commands: CommandView[] }>(`/api/commands${qs(params ?? {})}`),
  sendCommand: (edgeId: string, devId: string, cmd: string, args?: string) =>
    req<CommandView>(
      `/api/devices/${encodeURIComponent(edgeId)}/${encodeURIComponent(devId)}/commands`,
      { method: 'POST', body: JSON.stringify({ cmd, args: args ?? '' }) },
    ),
}

export function wsUrl(): string {
  const proto = location.protocol === 'https:' ? 'wss' : 'ws'
  const t = getToken()
  return `${proto}://${location.host}/ws${t ? `?token=${encodeURIComponent(t)}` : ''}`
}