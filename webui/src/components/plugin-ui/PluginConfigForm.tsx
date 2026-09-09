// Schema/field-driven application configuration form.
//
// The main view never renders raw JSON. JSON remains available only in an
// advanced details block as a fallback for diagnostics.
import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import type { FormEvent } from 'react'
import { Braces, Save } from 'lucide-react'
import { Button, Checkbox, Select, TextField } from '@/components/ui'
import { PluginErrorNote } from '@/components/plugin/PluginFacts'
import { useUpdateInstance } from '@/hooks/usePlugins'
import { safeConfigEntries } from '@/lib/plugins'
import type { PluginInstanceView, PluginUIField, PluginUISection } from '@/lib/types'

function isRecord(value: unknown): value is Record<string, unknown> {
  return Boolean(value) && typeof value === 'object' && !Array.isArray(value)
}

function parseRoot(config: Record<string, string>, root: string): Record<string, unknown> | null {
  const raw = config[root]
  if (typeof raw !== 'string' || !raw.trim()) return null
  try {
    const value: unknown = JSON.parse(raw)
    return isRecord(value) ? value : null
  } catch { return null }
}

function getPath(config: Record<string, string>, key: string): unknown {
  if (!key.includes('.')) return config[key]
  const [root, ...parts] = key.split('.')
  let current: unknown = parseRoot(config, root)
  for (const part of parts) {
    if (!isRecord(current)) return undefined
    current = current[part]
  }
  return current
}

function typedFieldValue(field: PluginUIField, value: string): unknown {
  if (value === '') return undefined
  if (field.type === 'boolean') return value === 'true'
  if (field.type === 'number' || field.type === 'integer') return Number(value)
  if (field.enum) return field.enum.find((option) => String(option) === value) ?? value
  return value
}

/**
 * Write a field back without stringifying nested JSON values.
 * `app_config.timezone` / `app_config.reminder.freq` / `app_config.beep_on_press`
 * therefore preserve string, number and boolean types in the persisted JSON.
 */
function setPath(config: Record<string, string>, key: string, value: unknown, t: (key: string, options?: Record<string, unknown>) => string): Record<string, string> {
  if (!key.includes('.')) {
    const next = { ...config }
    if (value === undefined) delete next[key]
    else next[key] = typeof value === 'string' ? value : JSON.stringify(value)
    return next
  }
  const [root, ...parts] = key.split('.')
  const existing = config[root]
  const rootValue = existing === undefined || existing === ''
    ? {}
    : parseRoot(config, root)
  if (!rootValue) throw new Error(t('config.invalidJson'))
  let current: Record<string, unknown> = rootValue
  for (const part of parts.slice(0, -1)) {
    const next = current[part]
    current[part] = isRecord(next) ? { ...next } : {}
    current = current[part] as Record<string, unknown>
  }
  const leaf = parts[parts.length - 1]
  if (!leaf) return config
  if (value === undefined) delete current[leaf]
  else current[leaf] = value
  return { ...config, [root]: JSON.stringify(rootValue) }
}

function fieldValue(config: Record<string, string>, field: PluginUIField): string {
  const value = getPath(config, field.key)
  if (value === undefined || value === null) return field.default === undefined ? '' : String(field.default)
  return String(value)
}

function validateField(field: PluginUIField, value: string, t: (key: string, options?: Record<string, unknown>) => string): string | undefined {
  if (field.required && value.trim() === '') return t('config.required')
  if (value === '') return undefined
  if (field.type === 'number' || field.type === 'integer') {
    const number = Number(value)
    if (!Number.isFinite(number)) return t('config.invalidNumber')
    if (field.type === 'integer' && !Number.isInteger(number)) return t('config.invalidInteger')
    if (field.minimum !== undefined && number < field.minimum) return t('config.minimum', { value: field.minimum })
    if (field.maximum !== undefined && number > field.maximum) return t('config.maximum', { value: field.maximum })
  }
  if (field.pattern) {
    try { if (!new RegExp(field.pattern).test(value)) return t('config.invalidPattern') }
    catch { return t('config.invalidPatternRule') }
  }
  if (field.enum && !field.enum.some((option) => String(option) === value)) return t('config.invalidOption')
  return undefined
}

export function PluginConfigForm({ instance, section, readOnly }: {
  instance: PluginInstanceView
  section: PluginUISection
  readOnly: boolean
}) {
  const { t } = useTranslation('plugin')
  const fields = useMemo(() => section.fields ?? [], [section.fields])
  const [values, setValues] = useState<Record<string, string>>({})
  const [errors, setErrors] = useState<Record<string, string>>({})
  const [error, setError] = useState<unknown>(null)
  const update = useUpdateInstance()
  const config = useMemo(() => instance.desired.config ?? {}, [instance.desired.config])

  useEffect(() => {
    const next: Record<string, string> = {}
    for (const field of fields) next[field.key] = fieldValue(config, field)
    setValues(next)
    setErrors({})
  }, [instance.id, instance.desired.revision, fields, config])

  if (fields.length === 0) {
    return <div className="space-y-3">
      <div role="alert" className="rounded-tile bg-warn/12 px-3.5 py-3 text-body text-warn">
        <p className="font-medium">{t('config.unavailable')}</p>
        <p className="mt-1 text-meta leading-relaxed opacity-90">{t('config.unavailableHint')}</p>
      </div>
      <AdvancedConfig config={config} />
    </div>
  }

  async function submit(event: FormEvent) {
    event.preventDefault()
    const nextErrors: Record<string, string> = {}
    for (const field of fields) {
      const message = validateField(field, values[field.key] ?? '', t)
      if (message) nextErrors[field.key] = message
    }
    setErrors(nextErrors)
    if (Object.keys(nextErrors).length > 0) return
    let nextConfig = { ...config }
    try {
      for (const field of fields) nextConfig = setPath(nextConfig, field.key, typedFieldValue(field, values[field.key] ?? ''), t)
      setError(null)
      await update.mutateAsync({ id: instance.id, body: { config: nextConfig } })
    } catch (cause) {
      setError(cause)
    }
  }

  return <form className="space-y-4" onSubmit={(event) => void submit(event)}>
    {readOnly && <p className="rounded-tile bg-ink-3/10 px-3.5 py-3 text-body text-ink-2">{t('config.readOnly')}</p>}
    <div className="grid gap-4 sm:grid-cols-2">
      {fields.map((field) => {
        const value = values[field.key] ?? ''
        const common = {
          label: field.label || field.key,
          hint: field.description,
          error: errors[field.key],
          disabled: readOnly || update.isPending,
        }
        if (field.type === 'boolean') {
          return <label key={field.key} className="flex min-h-touch items-center gap-2 self-end text-body text-ink-2">
            <Checkbox checked={value === 'true'} disabled={common.disabled}
              onChange={(event) => setValues((current) => ({ ...current, [field.key]: String(event.target.checked) }))} />
            <span>{common.label}</span>
          </label>
        }
        if (field.enum?.length || field.type === 'select') {
          return <label key={field.key} className="min-w-0 text-compact font-medium text-ink-2">
            <span className="mb-1.5 block">{common.label}{field.required ? ' *' : ''}</span>
            <Select className="w-full" value={value} disabled={common.disabled}
              aria-invalid={common.error ? true : undefined}
              onChange={(event) => setValues((current) => ({ ...current, [field.key]: event.target.value }))}>
              <option value="">{t('config.select')}</option>
              {(field.enum ?? []).map((option) => <option key={String(option)} value={String(option)}>{String(option)}</option>)}
            </Select>
            {common.error && <span className="mt-1.5 block text-meta text-bad">{common.error}</span>}
          </label>
        }
        return <TextField key={field.key} {...common} type={field.type === 'number' || field.type === 'integer' ? 'number' : 'text'}
          step={field.type === 'integer' ? 1 : 'any'} value={value}
          autoComplete="off" spellCheck={false}
          placeholder={field.secret ? t('config.secretPlaceholder') : field.placeholder}
          onChange={(event) => setValues((current) => ({ ...current, [field.key]: event.target.value }))} />
      })}
    </div>
    {error ? <PluginErrorNote error={error} /> : null}
    {!readOnly && <div className="flex justify-end border-t border-hairline pt-4">
      <Button type="submit" disabled={update.isPending}><Save size={14} />{update.isPending ? t('config.saving') : t('config.save')}</Button>
    </div>}
    <AdvancedConfig config={config} />
  </form>
}

function AdvancedConfig({ config }: { config: Record<string, string> }) {
  const { t } = useTranslation('plugin')
  const entries = safeConfigEntries(config)
  return <details className="min-w-0 text-meta text-ink-2">
    <summary className="flex min-h-touch cursor-pointer items-center gap-1.5"><Braces size={12} />{t('config.advanced')}</summary>
    <p className="mt-2 leading-relaxed text-ink-3">{t('config.advancedHint')}</p>
    <dl className="mt-2 space-y-1 rounded-tile bg-surface-2 p-3">
      {entries.map((entry) => <div key={entry.key} className="flex min-w-0 justify-between gap-3">
        <dt className="shrink-0">{entry.key}</dt>
        <dd className="num min-w-0 break-all text-right font-mono" title={entry.value}>{entry.isSecret ? t('config.secretHidden') : entry.value}</dd>
      </div>)}
      {entries.length === 0 && <p className="text-ink-3">{t('config.empty')}</p>}
    </dl>
  </details>
}
