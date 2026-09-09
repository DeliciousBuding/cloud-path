// 运行实例的其余事实面：Version / 网关 / Trust / Permissions / Health / Revision / Last ACK，
// 以及 secret handle、非敏感配置与错误码呈现。
//
// 安全边界（control-plane-sync.md 不变量 6、任务书 §6.5）：
//   - secret 只显示 **handle 名**，明文永不出现在 DOM；
//   - 不呈现本机绝对路径（目录视图的 source 字段可能是路径，一律不渲染）；
//   - 不呈现插件 stdout/stderr 原文（observed.detail 是 server 限长脱敏后的摘要）。
import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { Braces, KeyRound, Lock, ShieldCheck, ShieldAlert } from 'lucide-react'
import { Badge, KeyValue } from '@/components/ui'
import {
  permissionGroups, permissionItemLabel, pluginDisplayName, pluginErrorCopy, safeConfigEntries,
  secretHandleName, shortDigest, trustMeta,
} from '@/lib/plugins'
import { fmtDateTime } from '@/lib/format'
import { resolveUIFieldLabel, resolveUIFieldValue } from '@/lib/plugin-ui'
import type {
  PluginCatalogView, PluginInstanceView, PluginPermissionsData, PluginUIField,
} from '@/lib/types'

/** 错误码 → 设计过的提示块（按稳定码呈现，不复述服务端文本） */
export function PluginErrorNote({ error, className }: { error: unknown; className?: string }) {
  const { t } = useTranslation('plugin')
  const copy = pluginErrorCopy(error)
  const box = copy.tone === 'bad' ? 'bg-bad/10 text-bad'
    : copy.tone === 'warn' ? 'bg-warn/12 text-warn' : 'bg-ink-3/10 text-ink-2'
  return (
    <div role="alert" className={`rounded-tile px-3.5 py-3 ${box} ${className ?? ''}`}>
      <p className="text-compact font-semibold break-words">{copy.title}</p>
      <p className="mt-0.5 text-meta leading-relaxed break-words opacity-90">{copy.hint}</p>
      {copy.code && (
        <details className="mt-2 min-w-0">
          <summary className="flex min-h-touch cursor-pointer items-center text-meta opacity-80">{t('facts.technicalDetails')}</summary>
          <p className="num mt-1 break-all text-meta opacity-70">{t('facts.errorCode')} {copy.code}</p>
        </details>
      )}
    </div>
  )
}

/** 权限声明清单：按硬件/网络/文件系统/secret 分组，未声明的组不出现 */
export function PermissionList({ permissions, emptyHint }: {
  permissions: PluginPermissionsData | undefined
  emptyHint?: string
}) {
  const { t } = useTranslation('plugin')
  const groups = permissionGroups(permissions)
  if (groups.length === 0) {
    return <p className="py-2 text-meta text-ink-3">{emptyHint ?? t('facts.noPermissionsDeclared')}</p>
  }
  return (
    <div className="space-y-2.5">
      {groups.map((g) => (
        <div key={g.key} className="min-w-0">
          <p className="mb-1 flex items-center gap-1.5 text-meta font-medium text-ink-2">
            {g.key === 'secrets'
              ? <KeyRound size={11} className="shrink-0" />
              : g.key === 'network'
                ? <ShieldAlert size={11} className="shrink-0" />
                : <ShieldCheck size={11} className="shrink-0" />}
            {g.group}
            <span className="num text-ink-3">{g.items.length}</span>
          </p>
          <ul className="m-0 flex list-none flex-wrap gap-1.5 p-0">
            {g.items.map((item) => (
              <li key={item} className="min-w-0 max-w-full">
                <Badge tone={g.tone} className="max-w-full">
                  <span className="min-w-0 truncate break-all">{permissionItemLabel(g.key, item)}</span>
                </Badge>
              </li>
            ))}
          </ul>
        </div>
      ))}
    </div>
  )
}

/** secret handle 清单：只有名字，并明确说明明文不在这里 */
export function SecretRefList({ refs }: { refs: string[] | undefined }) {
  const { t } = useTranslation('plugin')
  // 只取 handle 名；去重后排序，避免同一 handle 重复占位
  const entries = [...new Set((refs ?? []).map(secretHandleName))].sort()
  if (entries.length === 0) {
    return <p className="py-1 text-meta text-ink-3">{t('facts.noSecrets')}</p>
  }
  return (
    <div>
      <ul className="m-0 flex list-none flex-wrap gap-1.5 p-0">
        {entries.map((name) => (
          <li key={name} className="min-w-0 max-w-full">
            <span className="badge max-w-full bg-ink-3/10 text-ink-2" title={t('facts.secretTitle', { name })}>
              <Lock size={10} className="shrink-0" />
              <span className="min-w-0 truncate break-all">{name}</span>
            </span>
          </li>
        ))}
      </ul>
      <p className="mt-1.5 text-meta leading-relaxed text-ink-3">
        {t('facts.secretsHint')}
      </p>
    </div>
  )
}

/** app_config 路径读取：与配置表单保持同一套嵌套语义。 */
function isRecord(value: unknown): value is Record<string, unknown> {
  return Boolean(value) && typeof value === 'object' && !Array.isArray(value)
}

function parseConfigRoot(config: Record<string, string>, root: string): Record<string, unknown> | null {
  const raw = config[root]
  if (typeof raw !== 'string' || !raw.trim()) return null
  try {
    const value: unknown = JSON.parse(raw)
    return isRecord(value) ? value : null
  } catch { return null }
}

function getConfigPath(config: Record<string, string>, key: string): unknown {
  if (!key.includes('.')) return config[key]
  const [root, ...parts] = key.split('.')
  let current: unknown = parseConfigRoot(config, root)
  for (const part of parts) {
    if (!isRecord(current)) return undefined
    current = current[part]
  }
  return current
}

interface ConfigFieldGroup {
  key: string
  title?: string
  description?: string
  fields: PluginUIField[]
}

/** 只读取插件声明的配置字段；机器字段名和原始 JSON 不进入普通设置区。 */
function declaredConfigGroups(catalog?: PluginCatalogView): ConfigFieldGroup[] {
  const contributions = [
    ...(catalog?.contributes.applications ?? []),
    ...(catalog?.contributes.drivers ?? []),
  ]
  const groups: ConfigFieldGroup[] = []
  for (const contribution of contributions) {
    for (const page of contribution.ui?.pages ?? []) {
      for (const [index, section] of page.sections.entries()) {
        if (section.type !== 'form' || section.source !== 'config' || !section.fields?.length) continue
        groups.push({
          key: `${contribution.id}:${page.id}:${index}`,
          title: section.title || page.title,
          description: section.description,
          fields: section.fields,
        })
      }
    }
  }
  return groups
}

function scalarValue(field: PluginUIField, value: unknown, t: (key: string, options?: Record<string, unknown>) => string): string {
  const mapped = resolveUIFieldValue(field, value)
  if (mapped) return mapped
  if (field.type === 'boolean' || typeof value === 'boolean') {
    return value === true || value === 'true' ? t('sections.yes') : t('sections.no')
  }
  if (field.type === 'number' || field.type === 'integer') {
    const number = Number(value)
    if (Number.isFinite(number)) {
      const formatted = field.precision === undefined ? String(number) : String(Number(number.toFixed(field.precision)))
      const suffix = field.format === 'percent' && !field.unit?.includes('%') ? '%' : ''
      return `${formatted}${suffix}${field.unit ? ` ${field.unit}` : ''}`
    }
  }
  return `${String(value)}${field.unit ? ` ${field.unit}` : ''}`
}

function FieldValue({ field, value }: { field: PluginUIField; value: unknown }) {
  const { t } = useTranslation('plugin')
  if (field.type === 'array' || Array.isArray(value)) {
    const items = Array.isArray(value) ? value : []
    if (items.length === 0) return <span className="text-meta text-ink-3">{t('facts.noItems')}</span>
    return (
      <div className="space-y-2">
        {items.map((item, index) => {
          const itemTitle = t('facts.arrayItem', { number: index + 1 })
          if (!isRecord(item) || !field.itemFields?.length) {
            const display = isRecord(item) ? t('facts.structuredValue') : scalarValue(field, item, t)
            return (
              <div key={`${itemTitle}-${index}`} className="rounded-tile bg-surface-2 px-3 py-2">
                <p className="text-meta text-ink-3">{itemTitle}</p>
                <p className="mt-0.5 text-body text-ink-2">{display}</p>
              </div>
            )
          }
          return (
            <div key={`${itemTitle}-${index}`} className="rounded-tile bg-surface-2 px-3 py-2.5">
              <p className="mb-2 text-meta font-medium text-ink-2">{itemTitle}</p>
              <dl className="m-0 space-y-1.5">
                {field.itemFields.map((itemField) => (
                  <KeyValue
                    key={itemField.key}
                    k={resolveUIFieldLabel(itemField) || t('facts.settingFallback')}
                    v={<FieldValue field={itemField} value={item[itemField.key]} />}
                  />
                ))}
              </dl>
            </div>
          )
        })}
      </div>
    )
  }
  if (value === undefined || value === null || value === '') {
    if (field.default !== undefined && !Array.isArray(field.default) && !isRecord(field.default)) {
      return <span className="text-ink-2">{t('facts.usingDefault', { value: scalarValue(field, field.default, t) })}</span>
    }
    return <span className="text-meta text-ink-3">{t('facts.notConfigured')}</span>
  }
  return <span className="break-words">{scalarValue(field, value, t)}</span>
}
/** 非敏感配置：优先按 manifest 的 form 字段结构化展示，原始配置只留在技术详情。 */
export function ConfigTable({ config, catalog }: {
  config: Record<string, string> | undefined
  catalog?: PluginCatalogView
}) {
  const { t } = useTranslation('plugin')
  const groups = declaredConfigGroups(catalog)
  const rows = safeConfigEntries(config)
  if (groups.length === 0 && rows.length === 0) {
    return <p className="py-1 text-meta text-ink-3">{t('facts.noConfig')}</p>
  }
  return (
    <div className="min-w-0">
      {groups.length === 0 ? (
        <p className="py-1 text-meta leading-relaxed text-ink-3">{t('facts.settingsUnavailable')}</p>
      ) : (
        <div className="space-y-4">
          {groups.map((group) => (
            <section key={group.key} className="min-w-0">
              {group.title && <h3 className="text-compact font-medium text-ink-2">{group.title}</h3>}
              {group.description && <p className="mt-0.5 text-meta leading-relaxed text-ink-3">{group.description}</p>}
              <dl className="m-0 mt-2 space-y-2.5">
                {group.fields.map((field) => (
                  <KeyValue
                    key={field.key}
                    k={resolveUIFieldLabel(field) || t('facts.settingFallback')}
                    v={<FieldValue field={field} value={getConfigPath(config ?? {}, field.key)} />}
                  />
                ))}
              </dl>
            </section>
          ))}
        </div>
      )}
      {rows.length > 0 && (
        <details className="mt-3 min-w-0 border-t border-hairline pt-3 text-meta text-ink-2">
          <summary className="flex min-h-touch cursor-pointer items-center gap-1.5">
            <Braces size={12} />{t('facts.rawSettings')}
          </summary>
          <p className="mt-2 leading-relaxed text-ink-3">{t('facts.rawSettingsHint')}</p>
          <dl className="m-0 mt-2 space-y-1 rounded-tile bg-surface-2 p-3">
            {rows.map((r) => (
              <div key={r.key} className="flex min-w-0 justify-between gap-3">
                <dt className="shrink-0 font-mono">{r.key}</dt>
                <dd className="num min-w-0 break-all text-right font-mono" title={r.value}>
                  {r.isSecret ? t('facts.secretHidden') : r.value}
                </dd>
              </div>
            ))}
          </dl>
        </details>
      )}
    </div>
  )
}

/** 基本信息与技术详情一览 */
export function InstanceFacts({ v, catalog }: { v: PluginInstanceView; catalog?: PluginCatalogView }) {
  const { t } = useTranslation('plugin')
  const trust = catalog ? trustMeta(undefined, catalog.verified) : null
  const rows: { k: string; node: ReactNode }[] = [
    { k: t('facts.plugin'), node: <span className="min-w-0 truncate" title={pluginDisplayName(catalog)}>{pluginDisplayName(catalog)}</span> },
    ...(v.edge_id === 'server' ? [{ k: t('facts.location'), node: t('host.server') }] : [
      { k: t('facts.location'), node: <span className="num min-w-0 truncate font-mono" title={v.edge_id}>{t('location.edge', { id: v.edge_id || '—' })}</span> },
      { k: t('facts.edgeStatus'), node: v.edge_online ? t('common.online') : t('common.offline') },
    ]),
    { k: t('facts.expectedVersion'), node: v.desired.version || '—' },
    { k: t('facts.actualVersion'), node: v.has_observed ? (v.observed?.version || t('desired.versionNotProvided')) : t('common.notReported') },
  ]
  return (
    <dl className="m-0 space-y-2.5">
      {rows.map((r) => <KeyValue key={r.k} k={r.k} v={r.node} />)}
      <div className="flex min-w-0 items-baseline justify-between gap-2 border-t border-hairline pt-2.5">
        <dt className="shrink-0 text-compact text-ink-2">{t('facts.sourceVerification')}</dt>
        <dd className="min-w-0 truncate text-right">
          {trust
            ? <Badge tone={trust.tone}>{trust.label}</Badge>
            : <span className="text-meta text-ink-3">{t('common.notProvided')}</span>}
        </dd>
      </div>
      <details className="min-w-0 border-t border-hairline pt-2.5 text-meta text-ink-2">
        <summary className="flex min-h-touch cursor-pointer items-center">{t('facts.technicalDetails')}</summary>
        <div className="mt-2 space-y-1.5">
          <div className="flex min-w-0 items-baseline justify-between gap-2">
            <dt className="shrink-0">{t('facts.instanceId')}</dt>
            <dd className="num min-w-0 truncate text-right font-mono" title={v.id}>{v.id}</dd>
          </div>
          <div className="flex min-w-0 items-baseline justify-between gap-2">
            <dt className="shrink-0">{t('facts.pluginId')}</dt>
            <dd className="num min-w-0 truncate text-right font-mono" title={v.desired.plugin_id}>{v.desired.plugin_id || '—'}</dd>
          </div>
          <div className="flex min-w-0 items-baseline justify-between gap-2">
            <dt className="shrink-0">{t('facts.expectedVersion')}</dt>
            <dd className="num min-w-0 truncate text-right">{v.desired_revision}</dd>
          </div>
          <div className="flex min-w-0 items-baseline justify-between gap-2">
            <dt className="shrink-0">{t('facts.actualVersion')}</dt>
            <dd className="num min-w-0 truncate text-right">{v.applied_revision}</dd>
          </div>
          <div className="flex min-w-0 items-baseline justify-between gap-2">
            <dt className="shrink-0">{t('facts.lastUpdated')}</dt>
            <dd className="num min-w-0 truncate text-right">{v.last_ack_at ? fmtDateTime(v.last_ack_at) : t('facts.notSynced')}</dd>
          </div>
          {catalog && (
            <div className="flex min-w-0 items-baseline justify-between gap-2">
              <dt className="shrink-0">{t('facts.installDigest')}</dt>
              <dd className="num min-w-0 truncate text-right" title={catalog.digest}>{shortDigest(catalog.digest)}</dd>
            </div>
          )}
          {catalog?.compatibility && (
            <div className="flex min-w-0 items-baseline justify-between gap-2">
              <dt className="shrink-0">{t('facts.compatibility')}</dt>
              <dd className="min-w-0 truncate text-right" title={catalog.compatibility}>{catalog.compatibility}</dd>
            </div>
          )}
        </div>
      </details>
    </dl>
  )
}
