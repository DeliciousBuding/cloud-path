// fetch 替身：只实现 lib/api.ts 真正用到的 Response 面（ok/status/statusText/headers.get/json），
// 避免依赖 jsdom / undici 的 Response 实现差异，用例可精确断言请求路径与方法。
import { vi } from 'vitest'

export interface StubResponse {
  ok: boolean
  status: number
  statusText: string
  headers: { get(name: string): string | null }
  json(): Promise<unknown>
  text(): Promise<string>
}

export function stubResponse(status: number, body?: unknown, headers: Record<string, string> = {}): StubResponse {
  const lower: Record<string, string> = {}
  for (const [k, v] of Object.entries(headers)) lower[k.toLowerCase()] = v
  const text = body === undefined ? '' : JSON.stringify(body)
  return {
    ok: status >= 200 && status < 300,
    status,
    statusText: String(status),
    headers: { get: (name: string) => lower[name.toLowerCase()] ?? null },
    json: async () => body,
    text: async () => text,
  }
}

export type FetchRoute = (url: string, init?: RequestInit) => StubResponse | Promise<StubResponse>

export interface RecordedCall { url: string; method: string; body: unknown; headers: Record<string, string> }

export interface FetchStub {
  calls: RecordedCall[]
  /** 最近一次请求（断言 POST body / Authorization 头用） */
  last(): RecordedCall | undefined
  /** 命中某路径的请求 */
  to(fragment: string): RecordedCall[]
}

/**
 * 安装 fetch 替身。route 返回 undefined 时按 404 处理（模拟端点缺席），
 * 这正是 Wave2 Schema 面「后端未就绪」的默认现实。
 */
export function installFetch(route: FetchRoute): FetchStub {
  const calls: RecordedCall[] = []
  const fn = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = typeof input === 'string' ? input : input.toString()
    let body: unknown = undefined
    if (typeof init?.body === 'string') {
      try { body = JSON.parse(init.body) } catch { body = init.body }
    }
    calls.push({
      url,
      method: (init?.method ?? 'GET').toUpperCase(),
      body,
      headers: (init?.headers ?? {}) as Record<string, string>,
    })
    const r = await route(url, init)
    return (r ?? stubResponse(404, { error: 'not found' })) as unknown as Response
  })
  Object.defineProperty(globalThis, 'fetch', { writable: true, configurable: true, value: fn })
  const stub: FetchStub = {
    calls,
    last: () => calls[calls.length - 1],
    to: (fragment) => calls.filter((c) => c.url.includes(fragment)),
  }
  return stub
}

/** 全部端点 404（Schema 面缺席 / 后端未就绪的最小现实） */
export function installEmptyFetch(): FetchStub {
  return installFetch(() => stubResponse(404, { error: 'not found' }))
}