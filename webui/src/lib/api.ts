// REST 客户端：TanStack Query 的 queryFn 全走这里
import type {
  AdapterView, CommandView, DeviceView, EdgeView, EventView, HealthView, StatsView,
} from './types'

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

async function req<T>(path: string, init?: RequestInit): Promise<T> {
  const headers: Record<string, string> = {
    'Content-Type': 'application/json',
    ...(init?.headers as Record<string, string> | undefined),
  }
  const t = getToken()
  if (t) headers['Authorization'] = `Bearer ${t}`
  let res: Response
  try {
    res = await fetch(path, { ...init, headers })
  } catch {
    throw new Error('无法连接 server（服务未启动或网络不可达）')
  }
  if (!res.ok) {
    let msg = `${res.status} ${res.statusText}`
    try {
      const j = await res.json()
      if (j?.error) msg = j.error
    } catch { /* 保持状态码信息 */ }
    throw new Error(msg)
  }
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
  health: () => req<HealthView>('/healthz'),
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
