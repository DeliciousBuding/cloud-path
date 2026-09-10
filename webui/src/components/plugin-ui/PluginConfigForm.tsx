// Schema/field-driven application configuration form.
//
// The main view never renders raw JSON. JSON remains available only in an
// advanced details block as a fallback for diagnostics.
import { useEffect, useId, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import type { ComponentProps, FormEvent } from 'react'
import { Braces, Plus, Save, X } from 'lucide-react'
import { Button, Checkbox, IconButton, Input, Select, Textarea, TextField } from '@/components/ui'
import { PluginErrorNote } from '@/components/plugin/PluginFacts'
import { useUpdateInstance } from '@/hooks/usePlugins'
import { safeConfigEntries } from '@/lib/plugins'
import { resolveUIFieldDescription, resolveUIFieldLabel, resolveUIFieldValue } from '@/lib/plugin-ui'
import type { PluginInstanceView, PluginUIField, PluginUISection } from '@/lib/types'

export function isRecord(value: unknown): value is Record<string, unknown> {
  return Boolean(value) && typeof value === 'object' && !Array.isArray(value)
}

export function parseConfigRoot(config: Record<string, string>, root: string): Record<string, unknown> | null {
  const raw = config[root]
  if (typeof raw !== 'string' || !raw.trim()) return null
  try {
    const value: unknown = JSON.parse(raw)
    return isRecord(value) ? value : null
  } catch { return null }
}

export function getConfigPath(config: Record<string, string>, key: string): unknown {
  if (!key.includes('.')) return config[key]
  const [root, ...parts] = key.split('.')
  let current: unknown = parseConfigRoot(config, root)
  for (const part of parts) {
    if (!isRecord(current)) return undefined
    current = current[part]
  }
  return current
}

export function typedConfigFieldValue(field: PluginUIField, value: unknown): unknown {
  if (field.type === 'array') return Array.isArray(value) ? value : []
  const text = typeof value === 'string' ? value : value === undefined || value === null ? '' : String(value)
  if (text === '') return undefined
  if (field.type === 'boolean') return text === 'true'
  if (field.type === 'number' || field.type === 'integer') return Number(text)
  if (field.enum) return field.enum.find((option) => String(option) === text) ?? text
  return text
}

/**
 * Write a field back without stringifying nested JSON values.
 * `app_config.timezone` / `app_config.reminder.freq` / `app_config.beep_on_press`
 * therefore preserve string, number and boolean types in the persisted JSON.
 */
export function setConfigPath(config: Record<string, string>, key: string, value: unknown, t: (key: string, options?: Record<string, unknown>) => string): Record<string, string> {
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
    : parseConfigRoot(config, root)
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

export function configFieldValue(config: Record<string, string>, field: PluginUIField): unknown {
  const value = getConfigPath(config, field.key)
  if (value === undefined || value === null) return field.default
  return value
}

export type ConfigTranslator = (key: string, options?: Record<string, unknown>) => string

/** Read the declared fields from a config map without exposing raw JSON to the caller. */
export function valuesFromConfigFields(fields: PluginUIField[], config: Record<string, string>): Record<string, unknown> {
  const values: Record<string, unknown> = {}
  for (const field of fields) values[field.key] = configFieldValue(config, field)
  return values
}

/** Validate the current UI values and return errors keyed by field key. */
export function validateConfigFields(
  fields: PluginUIField[], values: Record<string, unknown>, t: ConfigTranslator,
): Record<string, string> {
  const errors: Record<string, string> = {}
  for (const field of fields) {
    const message = validateConfigField(field, values[field.key], t)
    if (message) errors[field.key] = message
  }
  return errors
}

/** Apply only declared fields to an existing config map, preserving undeclared keys. */
export function applyConfigFields(
  config: Record<string, string>, fields: PluginUIField[], values: Record<string, unknown>, t: ConfigTranslator,
): Record<string, string> {
  let next = { ...config }
  for (const field of fields) next = setConfigPath(next, field.key, typedConfigFieldValue(field, values[field.key]), t)
  return next
}

export function validateConfigField(field: PluginUIField, value: unknown, t: (key: string, options?: Record<string, unknown>) => string): string | undefined {
  if (field.type === 'array') {
    const items = Array.isArray(value) ? value : []
    if (field.required && items.length === 0) return t('config.required')
    if (field.minItems !== undefined && items.length < field.minItems) return t('config.minItems', { value: field.minItems })
    if (field.maxItems !== undefined && items.length > field.maxItems) return t('config.maxItems', { value: field.maxItems })
    for (const item of items) {
      const record = isRecord(item) ? item : {}
      for (const itemField of field.itemFields ?? []) {
        const itemError = validateConfigField(itemField, record[itemField.key], t)
        if (itemError) return `${resolveUIFieldLabel(itemField) || itemField.key}：${itemError}`
      }
    }
    return undefined
  }
  const textValue = typeof value === 'string' ? value : value === undefined || value === null ? '' : String(value)
  if (field.required && textValue.trim() === '') return t('config.required')
  if (textValue === '') return undefined
  if (field.type === 'number' || field.type === 'integer') {
    const number = Number(textValue)
    if (!Number.isFinite(number)) return t('config.invalidNumber')
    if (field.type === 'integer' && !Number.isInteger(number)) return t('config.invalidInteger')
    if (field.minimum !== undefined && number < field.minimum) return t('config.minimum', { value: field.minimum })
    if (field.maximum !== undefined && number > field.maximum) return t('config.maximum', { value: field.maximum })
  }
  if (field.pattern) {
    try { if (!new RegExp(field.pattern).test(textValue)) return t('config.invalidPattern') }
    catch { return t('config.invalidPatternRule') }
  }
  if (field.enum && !field.enum.some((option) => String(option) === textValue)) return t('config.invalidOption')
  return undefined
}

function TextareaField({ label, hint, error, ...rest }: {
  label: string
  hint?: string
  error?: string
} & Omit<ComponentProps<typeof Textarea>, 'error'>) {
  const id = useId()
  const message = error || hint
  const messageId = message ? `${id}-message` : undefined
  return <div className="min-w-0">
    <label htmlFor={id} className="mb-1.5 block text-compact font-medium text-ink-2">{label}</label>
    <Textarea id={id} error={Boolean(error)} aria-invalid={error ? true : undefined}
      aria-describedby={messageId} {...rest} />
    {message && <p id={messageId} className={error ? 'mt-1.5 text-meta text-bad' : 'mt-1.5 text-meta leading-relaxed text-ink-3'}>{message}</p>}
  </div>
}

function emptyArrayItem(fields: PluginUIField[]): Record<string, unknown> {
  return Object.fromEntries(fields.map((field) => [field.key, field.default ?? (field.type === 'boolean' ? false : '')]))
}

function ArrayField({ field, value, disabled, error, onChange }: {
  field: PluginUIField
  value: unknown
  disabled: boolean
  error?: string
  onChange: (value: unknown[]) => void
}) {
  const { t } = useTranslation('plugin')
  const items = Array.isArray(value) ? value : []
  const itemFields = field.itemFields ?? []
  const message = error || resolveUIFieldDescription(field)
  const updateItem = (index: number, key: string, next: unknown) => {
    onChange(items.map((item, current) => current === index && isRecord(item) ? { ...item, [key]: next } : item))
  }
  return <div className="min-w-0 sm:col-span-2">
    <div className="mb-2 flex items-center justify-between gap-3">
      <label className="text-compact font-medium text-ink-2">{resolveUIFieldLabel(field) || field.key}{field.required ? ' *' : ''}</label>
      <Button type="button" variant="ghost" size="sm" disabled={disabled}
        onClick={() => onChange([...items, emptyArrayItem(itemFields)])}><Plus size={13} />{t('config.addItem')}</Button>
    </div>
    {message && <p className={error ? 'mb-2 text-meta text-bad' : 'mb-2 text-meta leading-relaxed text-ink-3'}>{message}</p>}
    {items.length === 0
      ? <p className="rounded-tile bg-surface-2 px-3.5 py-3 text-meta text-ink-3">{t('config.noItems')}</p>
      : <div className="space-y-3">{items.map((item, index) => {
        const record = isRecord(item) ? item : {}
        return <div key={index} className="rounded-tile border border-hairline p-3.5">
          <div className="mb-3 flex items-center justify-between gap-2">
            <p className="text-meta font-medium text-ink-2">{t('config.item', { index: index + 1 })}</p>
            <IconButton type="button" label={t('config.removeItem', { index: index + 1 })} size="sm" disabled={disabled}
              onClick={() => onChange(items.filter((_, current) => current !== index))}><X size={13} /></IconButton>
          </div>
          <div className="grid gap-3 sm:grid-cols-2">
            {itemFields.map((itemField) => {
              const itemValue = record[itemField.key]
              const textValue = typeof itemValue === 'string' ? itemValue : itemValue === undefined || itemValue === null ? '' : String(itemValue)
              if (itemField.type === 'boolean') return <label key={itemField.key} className="flex min-h-touch items-center gap-2 self-end text-body text-ink-2">
                <Checkbox checked={itemValue === true || itemValue === 'true'} disabled={disabled}
                  onChange={(event) => updateItem(index, itemField.key, event.target.checked)} />
                <span>{resolveUIFieldLabel(itemField) || itemField.key}</span>
              </label>
              if (itemField.enum?.length || itemField.type === 'select') return <label key={itemField.key} className="min-w-0 text-compact font-medium text-ink-2">
                <span className="mb-1.5 block">{resolveUIFieldLabel(itemField) || itemField.key}{itemField.required ? ' *' : ''}</span>
                <Select className="w-full" value={textValue} disabled={disabled} required={itemField.required}
                  onChange={(event) => updateItem(index, itemField.key, event.target.value)}>
                  <option value="">{t('config.select')}</option>
                  {(itemField.enum ?? []).map((option) => <option key={String(option)} value={String(option)}>{resolveUIFieldValue(itemField, option) ?? String(option)}</option>)}
                </Select>
              </label>
              return <label key={itemField.key} className="min-w-0 text-compact font-medium text-ink-2">
                <span className="mb-1.5 block">{resolveUIFieldLabel(itemField) || itemField.key}{itemField.required ? ' *' : ''}</span>
                <Input className="w-full" disabled={disabled} required={itemField.required}
                  type={itemField.type === 'number' || itemField.type === 'integer' ? 'number' : 'text'}
                  value={textValue} placeholder={itemField.placeholder}
                  onChange={(event) => updateItem(index, itemField.key, event.target.value)} />
              </label>
            })}
          </div>
        </div>
      })}</div>}
  </div>
}

export function PluginConfigFields({ fields, values, errors, disabled, onChange }: {
  fields: PluginUIField[]
  values: Record<string, unknown>
  errors: Record<string, string>
  disabled: boolean
  onChange: (key: string, value: unknown) => void
}) {
  const { t } = useTranslation('plugin')
  return <div className="grid gap-4 sm:grid-cols-2">
    {fields.map((field) => {
      const value = values[field.key]
      const textValue = typeof value === 'string' ? value : value === undefined || value === null ? '' : String(value)
      const common = {
        label: resolveUIFieldLabel(field) || field.key,
        hint: resolveUIFieldDescription(field),
        error: errors[field.key],
        disabled,
      }
      const message = common.error || resolveUIFieldDescription(field)
      const messageId = message ? `${field.key}-message` : undefined
      if (field.type === 'array') {
        return <ArrayField key={field.key} field={field} value={value} disabled={common.disabled} error={common.error}
          onChange={(next) => onChange(field.key, next)} />
      }
      if (field.type === 'boolean') {
        return <div key={field.key} className="min-w-0 self-end">
          <label className="flex min-h-touch items-center gap-2 text-body text-ink-2">
            <Checkbox checked={value === true || value === 'true'} disabled={common.disabled} aria-describedby={messageId}
              onChange={(event) => onChange(field.key, String(event.target.checked))} />
            <span>{common.label}</span>
          </label>
          {message && <p id={messageId} className={common.error ? 'mt-1.5 text-meta text-bad' : 'mt-1.5 text-meta leading-relaxed text-ink-3'}>{message}</p>}
        </div>
      }
      if (field.enum?.length || field.type === 'select') {
        return <label key={field.key} className="min-w-0 text-compact font-medium text-ink-2">
          <span className="mb-1.5 block">{common.label}{field.required ? ' *' : ''}</span>
          <Select className="w-full" value={textValue} disabled={common.disabled} required={field.required}
            aria-invalid={common.error ? true : undefined} aria-describedby={messageId}
            onChange={(event) => onChange(field.key, event.target.value)}>
            <option value="">{t('config.select')}</option>
            {(field.enum ?? []).map((option) => <option key={String(option)} value={String(option)}>{resolveUIFieldValue(field, option) ?? String(option)}</option>)}
          </Select>
          {message && <p id={messageId} className={common.error ? 'mt-1.5 text-meta text-bad' : 'mt-1.5 text-meta leading-relaxed text-ink-3'}>{message}</p>}
        </label>
      }
      if (field.type === 'textarea') {
        return <TextareaField key={field.key} label={common.label} hint={common.hint} error={common.error}
          disabled={common.disabled} required={field.required} value={textValue} rows={4}
          autoComplete="off" spellCheck={false}
          placeholder={field.secret ? t('config.secretPlaceholder') : field.placeholder}
          onChange={(event) => onChange(field.key, event.target.value)} />
      }
      return <TextField key={field.key} {...common} required={field.required}
        type={field.type === 'number' || field.type === 'integer' ? 'number' : 'text'}
        step={field.type === 'integer' ? 1 : 'any'} value={textValue}
        autoComplete="off" spellCheck={false}
        placeholder={field.secret ? t('config.secretPlaceholder') : field.placeholder}
        onChange={(event) => onChange(field.key, event.target.value)} />
    })}
  </div>
}

export function PluginConfigForm({ instance, section, readOnly }: {
  instance: PluginInstanceView
  section: PluginUISection
  readOnly: boolean
}) {
  const { t } = useTranslation('plugin')
  const fields = useMemo(() => section.fields ?? [], [section.fields])
  const [values, setValues] = useState<Record<string, unknown>>({})
  const [errors, setErrors] = useState<Record<string, string>>({})
  const [error, setError] = useState<unknown>(null)
  const update = useUpdateInstance()
  const config = useMemo(() => instance.desired.config ?? {}, [instance.desired.config])

  useEffect(() => {
    setValues(valuesFromConfigFields(fields, config))
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
    const nextErrors = validateConfigFields(fields, values, t)
    setErrors(nextErrors)
    if (Object.keys(nextErrors).length > 0) return
    try {
      const nextConfig = applyConfigFields(config, fields, values, t)
      setError(null)
      await update.mutateAsync({ id: instance.id, body: { config: nextConfig } })
    } catch (cause) {
      setError(cause)
    }
  }

  return <form className="space-y-4" onSubmit={(event) => void submit(event)}>
    {readOnly && <p className="rounded-tile bg-ink-3/10 px-3.5 py-3 text-body text-ink-2">{t('config.readOnly')}</p>}
    <PluginConfigFields fields={fields} values={values} errors={errors}
      disabled={readOnly || update.isPending}
      onChange={(key, value) => setValues((current) => ({ ...current, [key]: value }))} />
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
