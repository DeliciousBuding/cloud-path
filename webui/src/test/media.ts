// 可控的 matchMedia 替身：jsdom 的 matchMedia 永远 matches=false 且不会派发 change，
// 无法测「减少动效偏好」「跟随系统主题」这类实时跟随行为。
// 测试用 setMediaMatches(query, bool) 改状态并触发监听，afterEach 由 setup.ts 清空登记。
type Listener = (ev: { matches: boolean; media: string }) => void

interface Entry { matches: boolean; listeners: Set<Listener> }

const registry = new Map<string, Entry>()

function entry(media: string): Entry {
  let e = registry.get(media)
  if (!e) { e = { matches: false, listeners: new Set() }; registry.set(media, e) }
  return e
}

class MediaQueryListStub {
  readonly media: string
  onchange: Listener | null = null
  constructor(media: string) { this.media = media }
  get matches(): boolean { return entry(this.media).matches }
  addEventListener(_type: string, listener: Listener): void { entry(this.media).listeners.add(listener) }
  removeEventListener(_type: string, listener: Listener): void { entry(this.media).listeners.delete(listener) }
  /** 旧式 API（部分库仍在用） */
  addListener(listener: Listener): void { entry(this.media).listeners.add(listener) }
  removeListener(listener: Listener): void { entry(this.media).listeners.delete(listener) }
  dispatchEvent(): boolean { return false }
}

/** 安装替身（setup.ts 调用一次） */
export function installMatchMediaStub(): void {
  Object.defineProperty(window, 'matchMedia', {
    writable: true,
    configurable: true,
    value: (query: string) => new MediaQueryListStub(query),
  })
}

/** 设置某个媒体查询的匹配状态，并通知已订阅的组件 */
export function setMediaMatches(query: string, matches: boolean): void {
  const e = entry(query)
  e.matches = matches
  for (const l of [...e.listeners]) l({ matches, media: query })
}

export function getMediaMatches(query: string): boolean {
  return entry(query).matches
}

/** 当前订阅数（验证卸载时真的摘掉了监听，避免泄漏） */
export function mediaListenerCount(query: string): number {
  return registry.get(query)?.listeners.size ?? 0
}

export function resetMediaQueries(): void {
  registry.clear()
}

export const REDUCED_MOTION_QUERY = '(prefers-reduced-motion: reduce)'
export const DARK_SCHEME_QUERY = '(prefers-color-scheme: dark)'