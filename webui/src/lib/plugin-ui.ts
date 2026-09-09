// 插件 UI contribution 的纯函数层：归一化、动态导航与路由解析。
//
// 安全边界（docs/architecture/plugin-ui.md）：
//   - 插件只声明 route/slug、白名单 section 和 package-relative custom entry；
//   - 任意远程 URL、绝对路径、`..`、未知 section、未知 scope 一律丢弃或 fail-closed；
//   - 这里不读取 cookie/localStorage，也不把插件字段拼成 HTML。
import type {
  PluginApplicationContributionData, PluginCatalogDriverView, PluginCatalogView, PluginInstanceView, PluginUIContribution,
  PluginUIField, PluginUIFieldType, PluginUINavigation, PluginUIPage, PluginUIPresentation,
  PluginUISection, PluginUISectionType, PluginUISource, PluginUIVisibility, UserView,
} from './types'

export const PLUGIN_UI_API_VERSION = 1 as const
export const PLUGIN_UI_SECTION_TYPES: readonly PluginUISectionType[] = [
  'status', 'metrics', 'form', 'actions', 'records', 'timeline', 'table', 'schedule',
  'chart', 'markdown', 'diagnostics', 'custom',
]
export const PLUGIN_UI_SOURCES: readonly PluginUISource[] = [
  'instance', 'bindings', 'jobs', 'manual-jobs', 'records', 'config', 'device',
  'device-actions', 'diagnostics', 'state', 'events',
]
export const PLUGIN_UI_PRESENTATIONS: readonly PluginUIPresentation[] = ['list', 'timeline', 'table', 'cards']
export const PLUGIN_UI_FIELD_TYPES: readonly PluginUIFieldType[] = ['string', 'number', 'integer', 'boolean', 'select', 'textarea']
export const PLUGIN_UI_FIELD_FORMATS = ['text', 'time', 'number', 'percent', 'duration'] as const
/** Driver 设备详情只接受这三种 section；来源必须与契约一一对应。 */
export const PLUGIN_UI_DEVICE_SECTION_TYPES = ['status', 'actions', 'diagnostics'] as const
/** 自定义 iframe 只能通过 Core 的 bridge 调用这些收窄能力。 */
export const PLUGIN_UI_SCOPE_ALLOWLIST = [
  'instance.read', 'bindings.read', 'jobs.read', 'records.read', 'jobs.run', 'config.write',
] as const

export type PluginUIScope = typeof PLUGIN_UI_SCOPE_ALLOWLIST[number]

const SECTION_SET = new Set<string>(PLUGIN_UI_SECTION_TYPES)
const SOURCE_SET = new Set<string>(PLUGIN_UI_SOURCES)
const PRESENTATION_SET = new Set<string>(PLUGIN_UI_PRESENTATIONS)
const FIELD_TYPE_SET = new Set<string>(PLUGIN_UI_FIELD_TYPES)
const FIELD_FORMAT_SET = new Set<string>(PLUGIN_UI_FIELD_FORMATS)
const SCOPE_SET = new Set<string>(PLUGIN_UI_SCOPE_ALLOWLIST)
const DEVICE_SECTION_TYPE_SET = new Set<string>(PLUGIN_UI_DEVICE_SECTION_TYPES)
const DEVICE_SECTION_SOURCE: Record<typeof PLUGIN_UI_DEVICE_SECTION_TYPES[number], PluginUISource> = {
  status: 'device',
  actions: 'device-actions',
  diagnostics: 'diagnostics',
}
const ROUTE_RE = /^[a-z0-9][a-z0-9-]{0,62}$/

function record(value: unknown): value is Record<string, unknown> {
  return Boolean(value) && typeof value === 'object' && !Array.isArray(value)
}

function text(value: unknown): string | undefined {
  return typeof value === 'string' && value.trim() ? value.trim() : undefined
}

function normalizeI18n(value: unknown): Record<string, string> | undefined {
  if (!record(value)) return undefined
  const entries: [string, string][] = []
  for (const [rawKey, rawText] of Object.entries(value)) {
    if (typeof rawText !== 'string') continue
    const key = rawKey.trim()
    const label = rawText.trim()
    if (key && label) entries.push([key, label])
  }
  return entries.length > 0 ? Object.fromEntries(entries) : undefined
}

function finite(value: unknown): number | undefined {
  return typeof value === 'number' && Number.isFinite(value) ? value : undefined
}

function safeEntry(value: unknown): string | undefined {
  const raw = text(value)
  if (!raw || raw.length > 240 || raw.startsWith('/') || raw.includes('\\') || raw.includes('..')) return undefined
  if (/^[a-z][a-z0-9+.-]*:/i.test(raw)) return undefined
  const parts = raw.split('/')
  if (parts.some((part) => !part || part === '.' || part === '..' || !/^[A-Za-z0-9._~-]+$/.test(part))) return undefined
  return parts.join('/')
}

function normalizeField(raw: unknown): PluginUIField | null {
  if (!record(raw)) return null
  const key = text(raw.key)
  if (!key || key.length > 120) return null
  const type = text(raw.type)
  const format = text(raw.format)
  const precision = finite(raw.precision)
  const field: PluginUIField = {
    key,
    label: text(raw.label),
    type: type && FIELD_TYPE_SET.has(type) ? type as PluginUIFieldType : undefined,
    description: text(raw.description),
    placeholder: text(raw.placeholder),
    required: raw.required === true ? true : undefined,
    minimum: finite(raw.minimum),
    maximum: finite(raw.maximum),
    pattern: text(raw.pattern),
    secret: raw.secret === true ? true : undefined,
    unit: text(raw.unit)?.slice(0, 16),
    precision: precision !== undefined && Number.isInteger(precision) && precision >= 0 && precision <= 6 ? precision : undefined,
    format: format && FIELD_FORMAT_SET.has(format) ? format as PluginUIField['format'] : undefined,
    primary: raw.primary === true ? true : undefined,
    hideWhenEmpty: raw.hideWhenEmpty === true ? true : undefined,
  }
  if (record(raw.values)) {
    const values: Record<string, string> = {}
    for (const [rawKey, rawValue] of Object.entries(raw.values)) {
      const keyLabel = text(rawKey)
      const valueLabel = text(rawValue)
      if (keyLabel && valueLabel && keyLabel.length <= 80 && valueLabel.length <= 80) values[keyLabel] = valueLabel
      if (Object.keys(values).length >= 64) break
    }
    if (Object.keys(values).length > 0) field.values = values
  }
  if (Array.isArray(raw.enum)) {
    const options = raw.enum.filter((item): item is string | number | boolean =>
      typeof item === 'string' || typeof item === 'number' || typeof item === 'boolean')
    if (options.length > 0) field.enum = options.slice(0, 64)
  }
  if ('default' in raw) field.default = raw.default
  return field
}

function normalizeSection(raw: unknown): PluginUISection | null {
  if (!record(raw)) return null
  const type = text(raw.type)
  if (!type || !SECTION_SET.has(type)) return null
  const source = text(raw.source)
  const presentation = text(raw.presentation)
  const section: PluginUISection = {
    type: type as PluginUISectionType,
    title: text(raw.title)?.slice(0, 80),
    description: text(raw.description)?.slice(0, 240),
    emptyText: text(raw.emptyText)?.slice(0, 160),
    source: source && SOURCE_SET.has(source) ? source as PluginUISource : undefined,
    recordType: text(raw.recordType ?? raw.record_type)?.slice(0, 64),
    presentation: presentation && PRESENTATION_SET.has(presentation)
      ? presentation as PluginUIPresentation : undefined,
    text: typeof raw.text === 'string' ? raw.text.slice(0, 4000) : undefined,
  }
  if (type === 'custom') section.entry = safeEntry(raw.entry)
  if (Array.isArray(raw.scopes)) {
    section.scopes = [...new Set(raw.scopes
      .filter((item): item is string => typeof item === 'string')
      .filter((item) => SCOPE_SET.has(item)))]
      .slice(0, 16)
  }
  if (Array.isArray(raw.fields)) {
    section.fields = raw.fields.slice(0, 64).map(normalizeField)
      .filter((item): item is PluginUIField => item !== null)
  }
  return section
}

function normalizeNavigation(raw: unknown): PluginUINavigation | undefined {
  if (!record(raw)) return undefined
  const title = text(raw.title)
  const route = text(raw.route)
  if (!title || !route || !ROUTE_RE.test(route)) return undefined
  const order = finite(raw.order)
  const visibility = text(raw.visibility)
  return {
    title: title.slice(0, 40),
    i18n: normalizeI18n(raw.i18n),
    route,
    icon: text(raw.icon)?.slice(0, 40),
    order: order !== undefined && Number.isInteger(order) && order >= 0 && order <= 999 ? order : undefined,
    visibility: visibility === 'always' || visibility === 'instance-enabled'
      ? visibility as PluginUIVisibility : undefined,
  }
}

function normalizePage(raw: unknown): PluginUIPage | null {
  if (!record(raw)) return null
  const id = text(raw.id)
  const title = text(raw.title)
  if (!id || !ROUTE_RE.test(id) || !title || !Array.isArray(raw.sections)) return null
  const sections = raw.sections.slice(0, 32).map(normalizeSection)
    .filter((item): item is PluginUISection => item !== null)
  if (sections.length === 0) return null
  return { id, title: title.slice(0, 80), description: text(raw.description)?.slice(0, 240), i18n: normalizeI18n(raw.i18n), sections }
}

/** 宽容归一化 UI contribution；非法字段安全丢弃，未知结构不会进入渲染树。 */
export function normalizePluginUI(raw: unknown): PluginUIContribution | undefined {
  if (!record(raw) || raw.apiVersion !== PLUGIN_UI_API_VERSION) return undefined
  const navigation = normalizeNavigation(raw.navigation)
  const pages = Array.isArray(raw.pages)
    ? raw.pages.slice(0, 8).map(normalizePage).filter((item): item is PluginUIPage => item !== null)
    : undefined
  const deviceRaw = record(raw.device) ? raw.device : undefined
  const deviceSections = deviceRaw && Array.isArray(deviceRaw.sections)
    ? deviceRaw.sections.slice(0, 24).map(normalizeSection)
      .filter((item): item is PluginUISection => item !== null)
    : undefined
  const device = deviceSections && deviceSections.length > 0 ? { sections: deviceSections } : undefined
  if (!navigation && (!pages || pages.length === 0) && !device) return undefined
  return {
    apiVersion: PLUGIN_UI_API_VERSION,
    navigation,
    pages: pages && pages.length > 0 ? pages : undefined,
    device,
  }
}

export type ApplicationUIReadableStatus = 'loading' | 'in' | 'out' | 'open'

/**
 * Read-only UI visibility. `open` is the existing L0/auth-probe-unavailable
 * state: reads may be open, but writes still require an authenticated
 * operator/admin through appActionScope/API RBAC.
 */
export function applicationUIReadable(
  user: UserView | null | undefined,
  status: ApplicationUIReadableStatus = 'in',
): boolean {
  if (status === 'open') return true
  return status === 'in' && Boolean(user && !user.disabled
    && Number.isInteger(user.tenant_id) && user.tenant_id > 0)
}

export interface ApplicationNavigationItem {
  key: string
  to: string
  route: string
  label: string
  icon?: string
  order: number
  plugin: PluginCatalogView
  contribution: PluginApplicationContributionData
  navigation: PluginUINavigation
  instances: PluginInstanceView[]
  primaryInstance: PluginInstanceView
}

export interface ApplicationRouteReady {
  kind: 'ready'
  plugin: PluginCatalogView
  contribution: PluginApplicationContributionData
  ui: PluginUIContribution
  navigation: PluginUINavigation
  page: PluginUIPage
  instances: PluginInstanceView[]
  instance: PluginInstanceView
}

export type ApplicationRouteResolution =
  | ApplicationRouteReady
  | { kind: 'auth-required' }
  | { kind: 'not-found'; route: string }
  | { kind: 'route-conflict'; route: string }
  | { kind: 'unverified'; route: string }
  | { kind: 'no-instance'; route: string }
  | { kind: 'disabled'; route: string }
  | { kind: 'no-page'; route: string }
  | { kind: 'page-not-found'; route: string; pageId: string }

function applicationContributions(plugin: PluginCatalogView): PluginApplicationContributionData[] {
  return plugin.kind === 'application' && Array.isArray(plugin.contributes?.applications)
    ? plugin.contributes.applications : []
}

function hasEnabled(instances: PluginInstanceView[]): boolean {
  return instances.some((instance) => instance.desired.enabled)
}

function primaryInstance(instances: PluginInstanceView[]): PluginInstanceView {
  return instances.find((instance) => instance.desired.enabled) ?? instances[0]!
}

/**
 * 只返回可见的业务导航项。route 冲突时全部 fail-closed，而不是静默选一个覆盖。
 * 未登录、未验证、无实例、或默认 visibility 下全部停用，都不会出现在主导航。
 */
export function buildApplicationNavigation(
  plugins: PluginCatalogView[], instances: PluginInstanceView[], readable: boolean,
): ApplicationNavigationItem[] {
  if (!readable) return []
  const candidates: Omit<ApplicationNavigationItem, 'to'>[] = []
  for (const plugin of plugins) {
    if (!plugin.verified) continue
    for (const contribution of applicationContributions(plugin)) {
      const navigation = contribution.ui?.navigation
      if (!navigation) continue
      const related = instances.filter((instance) => instance.desired.plugin_id === plugin.id)
      if (related.length === 0) continue
      const visibility = navigation.visibility ?? 'instance-enabled'
      if (visibility !== 'always' && !hasEnabled(related)) continue
      candidates.push({
        key: `${plugin.id}:${contribution.id}:${navigation.route}`,
        route: navigation.route,
        label: navigation.title,
        icon: navigation.icon,
        order: navigation.order ?? 100,
        plugin,
        contribution,
        navigation,
        instances: related,
        primaryInstance: primaryInstance(related),
      })
    }
  }
  const routeCounts = new Map<string, number>()
  for (const item of candidates) routeCounts.set(item.route, (routeCounts.get(item.route) ?? 0) + 1)
  return candidates
    .filter((item) => routeCounts.get(item.route) === 1)
    .sort((a, b) => a.order - b.order || a.label.localeCompare(b.label))
    .map((item) => ({ ...item, to: `/apps/${item.route}` }))
}

/** 解析深链接；返回明确的 fail-closed 原因，页面据此给用户可读状态。 */
export function resolveApplicationRoute(
  plugins: PluginCatalogView[], instances: PluginInstanceView[], route: string,
  pageId: string | undefined, requestedInstanceId: string | undefined, readable: boolean,
): ApplicationRouteResolution {
  if (!readable) return { kind: 'auth-required' }
  const matches = plugins.flatMap((plugin) => applicationContributions(plugin)
    .filter((contribution) => contribution.ui?.navigation?.route === route)
    .map((contribution) => ({ plugin, contribution, navigation: contribution.ui!.navigation! })))
  if (matches.length === 0) return { kind: 'not-found', route }
  if (matches.length > 1) return { kind: 'route-conflict', route }
  const match = matches[0]!
  if (!match.plugin.verified) return { kind: 'unverified', route }
  const related = instances.filter((instance) => instance.desired.plugin_id === match.plugin.id)
  if (related.length === 0) return { kind: 'no-instance', route }
  const visibility = match.navigation.visibility ?? 'instance-enabled'
  if (visibility !== 'always' && !hasEnabled(related)) return { kind: 'disabled', route }
  const pages = match.contribution.ui?.pages ?? []
  if (pages.length === 0) return { kind: 'no-page', route }
  const page = pageId ? pages.find((candidate) => candidate.id === pageId) : pages[0]
  if (!page) return { kind: 'page-not-found', route, pageId: pageId! }
  const instance = requestedInstanceId
    ? related.find((candidate) => candidate.desired.instance_id === requestedInstanceId || candidate.id === requestedInstanceId)
      ?? primaryInstance(related)
    : primaryInstance(related)
  return {
    kind: 'ready',
    plugin: match.plugin,
    contribution: match.contribution,
    ui: match.contribution.ui!,
    navigation: match.navigation,
    page,
    instances: related,
    instance,
  }
}

/** Same-origin asset path for a sandboxed custom page; never accepts a remote URL. */
export function pluginUIAssetURL(pluginId: string, version: string | undefined, entry: string | undefined): string | undefined {
  const safe = safeEntry(entry)
  const canonicalVersion = version?.trim()
  if (!safe || !canonicalVersion) return undefined
  const encoded = safe.split('/').map((part) => encodeURIComponent(part)).join('/')
  return `/api/plugin-ui/assets/${encodeURIComponent(pluginId)}/${encodeURIComponent(canonicalVersion)}/${encoded}`
}

export interface DriverDeviceUIResolution {
  plugin: PluginCatalogView
  contribution: PluginCatalogDriverView
  sections: PluginUISection[]
}

function isDriverDeviceSection(section: PluginUISection): boolean {
  if (!DEVICE_SECTION_TYPE_SET.has(section.type)) return false
  return section.source === DEVICE_SECTION_SOURCE[section.type as typeof PLUGIN_UI_DEVICE_SECTION_TYPES[number]]
}

/**
 * Resolve a device adapter to its installed, verified Driver contribution.
 *
 * `DeviceView.adapter` is the backend's registered adapter fact; the manifest contract
 * requires `contributes.drivers[].id` to equal that adapter name. Ambiguous matches fail
 * closed instead of guessing which plugin should own the device page.
 */
export function resolveDriverDeviceUI(
  plugins: PluginCatalogView[], adapter: string,
): DriverDeviceUIResolution | undefined {
  const adapterID = adapter.trim()
  if (!adapterID) return undefined
  const matches: DriverDeviceUIResolution[] = []
  for (const plugin of plugins) {
    if (plugin.kind !== 'driver' || !plugin.verified) continue
    for (const contribution of plugin.contributes.drivers ?? []) {
      if (contribution.id !== adapterID) continue
      const sections = (contribution.ui?.device?.sections ?? []).filter(isDriverDeviceSection)
      if (sections.length > 0) matches.push({ plugin, contribution, sections })
    }
  }
  return matches.length === 1 ? matches[0] : undefined
}
