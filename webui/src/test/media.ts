// jsdom 的最小 matchMedia 替身：只提供组件初始化所需的稳定 matches=false。
type Listener = (ev: { matches: boolean; media: string }) => void

class MediaQueryListStub {
  readonly media: string
  readonly matches = false
  onchange: Listener | null = null
  constructor(media: string) { this.media = media }
  addEventListener(_type: string, _listener: Listener): void {}
  removeEventListener(_type: string, _listener: Listener): void {}
  addListener(_listener: Listener): void {}
  removeListener(_listener: Listener): void {}
  dispatchEvent(): boolean { return false }
}

/** 安装 jsdom 缺失的 matchMedia API；不维护媒体查询动态状态。 */
export function installMatchMediaStub(): void {
  Object.defineProperty(window, 'matchMedia', {
    writable: true,
    configurable: true,
    value: (query: string) => new MediaQueryListStub(query) as unknown as MediaQueryList,
  })
}