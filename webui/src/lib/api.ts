// REST 客户端：TanStack Query 的 queryFn 全走这里。
// 鉴权接缝（docs/api.md §2）：会话 cookie 同源自动携带（fetch credentials:'same-origin'）
// + 可选 Bearer 服务令牌（localStorage）。登录态事实源 = GET /api/auth/me（200 已登录 / 401 未登录）。
// 任何受保护端点返回 401 → markUnauthenticated() 全局收敛（store/auth.ts → 路由守卫跳 /login）。
import type {
  AdapterView, CommandView, CreateTokenInput, CreatedToken, CreateUserInput, DeviceView, EdgeView,
  EventView, HealthView, MeResponse, OverviewView, PluginCatalogListResponse, PluginCatalogView,
  PluginInstanceActionRequest, PluginInstanceCreateRequest, PluginInstanceDeleteRequest,
  PluginInstanceListResponse, PluginInstanceUpdateRequest, PluginInstanceView,
  PluginInstanceWriteResponse, StatsView, TokenView, UpdateUserInput, UserView,
} from './types'
import { PLUGIN_ERR_CODES } from './types'
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
  /**
   * 服务端给的**稳定错误码**（如 api.PluginErr*）。UI 按码呈现文案，不解析 message 文本。
   * 取值顺序：响应体 `code` 字段 → `error` 字段恰好等于某个已知稳定码 → undefined。
   * 只对闭合枚举做精确匹配，因此不构成「解析错误文本」。
   */
  readonly code?: string
  constructor(status: number, message: string, retryAfter?: number, code?: string) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.retryAfter = retryAfter
    this.code = code
  }
}

/**
 * 资源明确不存在（HTTP 404）。
 * UI 语义：给「未注册 / 不存在」空态，而不是「加载失败」错误态——
 * 后者会让用户去查 server，而真相是这台设备根本没接入。
 */
export function isNotFound(e: unknown): boolean {
  return e instanceof ApiError && e.status === 404
}

/** 从错误响应体提取稳定码（闭合枚举精确匹配，不做自然语言解析） */
function extractErrorCode(body: unknown): string | undefined {
  if (!body || typeof body !== 'object') return undefined
  const o = body as Record<string, unknown>
  if (typeof o.code === 'string' && o.code) return o.code
  if (typeof o.error === 'string' && PLUGIN_ERR_CODES.includes(o.error)) return o.error
  return undefined
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
    let code: string | undefined
    try {
      const j = await res.json()
      if (j?.error) msg = j.error
      code = extractErrorCode(j)
    } catch { /* 保持状态码信息 */ }
    if (res.status === 401 && !opts?.public) markUnauthenticated()
    throw new ApiError(res.status, msg, parseRetryAfter(res), code)
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

/** Wave2 契约探测：Descriptor/Capability 端点由后端 A1 落地（路径以 A1 实现为准）。
 *  缺席（404/405/501）或网络不可达时返回 null —— UI 走通用回落，而不是抛错刷屏。 */
const ABSENT = new Set([404, 405, 501, 502])

async function reqOrNull<T>(path: string): Promise<T | null> {
  try {
    return await req<T>(path)
  } catch (e) {
    if (!(e instanceof ApiError) || ABSENT.has(e.status)) return null
    throw e
  }
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

  // ---- 管理面（docs/api.md §3.2-3.3）：admin-only，凭据沿用同一层（会话 cookie + 可选 Bearer）----
  // 401 走全局登出收敛；403/409/400 由调用方按 lib/admin.ts 的映射展示人话，不在前端复述业务规则。
  users: () => req<{ users: UserView[] }>('/api/users'),
  createUser: (input: CreateUserInput) =>
    req<{ user: UserView }>('/api/users', { method: 'POST', body: JSON.stringify(input) }),
  updateUser: (id: number, patch: UpdateUserInput) =>
    req<{ user: UserView }>(`/api/users/${id}`, { method: 'PATCH', body: JSON.stringify(patch) }),
  tokens: () => req<{ tokens: TokenView[] }>('/api/tokens'),
  /** 明文只在本次返回值里出现一次：调用方只能在组件内存中持有，禁止写 storage/URL/console/toast */
  createToken: (input: CreateTokenInput) =>
    req<CreatedToken>('/api/tokens', { method: 'POST', body: JSON.stringify(input) }),
  revokeToken: (id: number) =>
    req<void>(`/api/tokens/${id}`, { method: 'DELETE' }, { allowEmpty: true }),

  // ---- Overview 聚合读面（GET /api/overview → api.OverviewView）----
  /** 概览页唯一数据源：计数/离线设备/失败命令/近期事件全部由 server 聚合，前端不自算 */
  overview: () => req<OverviewView>('/api/overview'),

  // ---- 插件目录（GET /api/plugins）----
  plugins: () => req<PluginCatalogListResponse>('/api/plugins'),
  plugin: (pluginId: string) =>
    req<PluginCatalogView>(`/api/plugins/${encodeURIComponent(pluginId)}`),

  // ---- 插件实例管理（冻结端点，路径不得改；错误按 PluginErr* 稳定码呈现）----
  pluginInstances: () => req<PluginInstanceListResponse>('/api/plugin-instances'),
  pluginInstance: (id: string) =>
    req<PluginInstanceView>(`/api/plugin-instances/${encodeURIComponent(id)}`),
  createPluginInstance: (body: PluginInstanceCreateRequest) =>
    req<PluginInstanceWriteResponse>('/api/plugin-instances',
      { method: 'POST', body: JSON.stringify(body) }),
  updatePluginInstance: (id: string, body: PluginInstanceUpdateRequest) =>
    req<PluginInstanceWriteResponse>(`/api/plugin-instances/${encodeURIComponent(id)}`,
      { method: 'PATCH', body: JSON.stringify(body) }),
  deletePluginInstance: (id: string, body?: PluginInstanceDeleteRequest) =>
    req<PluginInstanceWriteResponse>(`/api/plugin-instances/${encodeURIComponent(id)}`,
      { method: 'DELETE', body: JSON.stringify(body ?? {}) }),
  reconcilePluginInstance: (id: string, body?: PluginInstanceActionRequest) =>
    req<PluginInstanceWriteResponse>(`/api/plugin-instances/${encodeURIComponent(id)}/reconcile`,
      { method: 'POST', body: JSON.stringify(body ?? {}) }),

  // ---- Wave2 Schema 面（返回 unknown，由 lib/descriptor.ts 宽容归一化）----
  /** 批量 Descriptor（列表页优先，避免每设备一次请求） */
  descriptors: () => reqOrNull<unknown>('/api/descriptors'),
  /** 单设备 Descriptor */
  deviceDescriptor: (edgeId: string, devId: string) =>
    reqOrNull<unknown>(`/api/devices/${encodeURIComponent(edgeId)}/${encodeURIComponent(devId)}/descriptor`),
  /** Capability catalog：presentation / actions 的事实源 */
  capabilities: () => reqOrNull<unknown>('/api/capabilities'),
}

export function wsUrl(): string {
  const proto = location.protocol === 'https:' ? 'wss' : 'ws'
  const t = getToken()
  return `${proto}://${location.host}/ws${t ? `?token=${encodeURIComponent(t)}` : ''}`
}