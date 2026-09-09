import { useId, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import type { ReactNode } from 'react'
import { i18n } from '@/i18n'
import { cn } from '@/lib/cn'
import { Select } from '@/components/ui'
import { commandArgsErrorCopy, commandForm, unsupportedSchemaKeywords } from '@/lib/command-schema'
import type { CommandField } from '@/lib/command-schema'

interface SchemaActionInputProps {
  action: { label: string; hint?: string; inputSchema?: Record<string, unknown>; inputPlaceholder?: string }
  validate: (args: string) => string | undefined
  description: ReactNode
  emptyHint: string
  validationSource: string
  /** 上层已经提供操作选择器时，用 sr-only 保留语义但不再重复标题。 */
  showTitle?: boolean
  /** Empty object is an explicit no-arguments request, never schema defaults. */
  emptyArgs?: string
  disabled?: boolean
  onEdit?: () => void
  renderSubmit: (args: string, error?: string) => ReactNode
}

type Draft = Record<string, string>

function tr(key: string, options?: Record<string, unknown>): string {
  return String(i18n.t(`commandInput.${key}`, { ns: 'common', ...options }))
}

function parseFieldValue(field: CommandField, raw: string): { value?: unknown; error?: string } {
  if (field.type === 'number' || field.type === 'integer') {
    const value = Number(raw)
    if (!Number.isFinite(value) || (field.type === 'integer' && !Number.isInteger(value))) {
      return { error: tr('error.number', { label: field.label }) }
    }
    return { value }
  }
  if (field.type === 'boolean') return { value: raw === 'true' }
  if (field.type === 'enum') return { value: field.choices?.[Number(raw)] }
  return { value: raw }
}

function parseRows(field: CommandField, raw: string): { value?: unknown[]; error?: string } {
  const nested = field.fields ?? []
  const lines = raw.split(/\r?\n/).map((line) => line.trim()).filter(Boolean)
  if (field.minItems != null && lines.length < field.minItems) {
    return { error: tr('error.minItems', { label: field.label, count: field.minItems }) }
  }
  if (field.maxItems != null && lines.length > field.maxItems) {
    return { error: tr('error.maxItems', { label: field.label, count: field.maxItems }) }
  }
  const rows: Record<string, unknown>[] = []
  for (const [lineIndex, line] of lines.entries()) {
    const parts = nested.length === 1 ? [line] : line.split(/\s*[,，;；\t]\s*/).filter(Boolean)
    if (parts.length !== nested.length) {
      return { error: tr('error.rowsCount', { label: field.label, line: lineIndex + 1, count: nested.length, fields: nested.map((item) => item.label).join(tr('listSeparator')) }) }
    }
    const row: Record<string, unknown> = {}
    for (const [partIndex, nestedField] of nested.entries()) {
      const parsed = parseFieldValue(nestedField, parts[partIndex] ?? '')
      if (parsed.error) return { error: tr('error.row', { label: field.label, line: lineIndex + 1, error: parsed.error }) }
      row[nestedField.key] = parsed.value
    }
    rows.push(row)
  }
  return { value: rows }
}

function rowsText(value: unknown, fields: CommandField[]): string | null {
  if (!Array.isArray(value)) return null
  const lines: string[] = []
  for (const item of value) {
    if (!item || typeof item !== 'object' || Array.isArray(item)) return null
    const row = item as Record<string, unknown>
    const parts: string[] = []
    for (const field of fields) {
      const raw = row[field.key]
      if (field.type === 'enum') {
        const index = field.choices?.findIndex((choice) => choice === raw) ?? -1
        if (index < 0) return null
        parts.push(String(index))
      } else if (field.type === 'number' || field.type === 'integer') {
        if (typeof raw !== 'number' || !Number.isFinite(raw)) return null
        parts.push(String(raw))
      } else if (field.type === 'boolean') {
        if (typeof raw !== 'boolean') return null
        parts.push(String(raw))
      } else if (field.type === 'string') {
        if (typeof raw !== 'string') return null
        parts.push(raw)
      } else {
        return null
      }
    }
    lines.push(parts.join(', '))
  }
  return lines.join('\n')
}

function arrayText(value: unknown, itemType?: CommandField['itemType']): string | null {
  if (!Array.isArray(value)) return null
  const valid = value.every((item) => itemType === 'string'
    ? typeof item === 'string'
    : typeof item === 'number' && Number.isFinite(item) && (itemType !== 'integer' || Number.isInteger(item)))
  return valid ? value.join(', ') : null
}

function choiceLabel(choice: unknown): string {
  if (typeof choice === 'boolean') return choice ? tr('yes') : tr('no')
  if (choice === null) return tr('none')
  if (typeof choice === 'string' && choice) return choice
  return JSON.stringify(choice)
}

function arrayParts(field: CommandField, raw: string): string[] {
  const text = raw.trim()
  const parts = text.split(/[\s,]+/).filter(Boolean)
  if (parts.length > 1) return parts
  if ((field.itemType === 'number' || field.itemType === 'integer')
    && field.minItems != null && field.minItems > 1
    && /^\d+$/.test(text)
    && text.length >= field.minItems
    && (field.maxItems == null || text.length <= field.maxItems)) return [...text]
  return parts
}

function exampleValue(example: unknown): unknown {
  if (example && typeof example === 'object' && !Array.isArray(example) && Object.hasOwn(example, 'value')) {
    return (example as Record<string, unknown>).value
  }
  return example
}

function exampleLabel(example: unknown): string | undefined {
  if (example && typeof example === 'object' && !Array.isArray(example)) {
    const row = example as Record<string, unknown>
    if (typeof row.title === 'string' && row.title.trim()) return row.title
    if (typeof row.label === 'string' && row.label.trim()) return row.label
  }
  return undefined
}

function exampleText(field: CommandField, example: unknown, index: number): { value: string; label: string } | null {
  const raw = exampleValue(example)
  let value: string | null = null
  let fallback = tr('preset', { number: index + 1 })
  if (field.type === 'array') {
    value = arrayText(raw, field.itemType)
    if (value !== null) fallback = value
  } else if (field.type === 'object-rows') {
    value = rowsText(raw, field.fields ?? [])
  } else if (field.type === 'enum') {
    const choice = field.choices?.findIndex((item) => item === raw) ?? -1
    if (choice >= 0) { value = String(choice); fallback = String(raw) }
  } else if (field.type === 'boolean' && typeof raw === 'boolean') {
    value = String(raw)
    fallback = raw ? tr('yes') : tr('no')
  } else if ((field.type === 'number' || field.type === 'integer') && typeof raw === 'number' && Number.isFinite(raw)) {
    value = String(raw)
    fallback = String(raw)
  } else if (field.type === 'string' && typeof raw === 'string') {
    value = raw
    fallback = raw
  }
  return value === null ? null : { value, label: exampleLabel(example) ?? fallback }
}

function fieldArgs(fields: CommandField[], values: Draft): { args: string; error?: string } {
  const entries: [string, unknown][] = []
  for (const field of fields) {
    const raw = Object.hasOwn(values, field.key) ? values[field.key] : undefined
    if (raw === undefined || (raw.trim() === '' && field.type !== 'string')) continue
    if (field.type === 'array') {
      const parts = arrayParts(field, raw)
      if (field.minItems != null && parts.length < field.minItems) {
        return { args: '', error: tr('error.minItems', { label: field.label, count: field.minItems }) }
      }
      if (field.maxItems != null && parts.length > field.maxItems) {
        return { args: '', error: tr('error.maxItems', { label: field.label, count: field.maxItems }) }
      }
      const parsed: unknown[] = []
      for (const part of parts) {
        if (field.itemType === 'number' || field.itemType === 'integer') {
          const n = Number(part)
          if (!Number.isFinite(n) || (field.itemType === 'integer' && !Number.isInteger(n))) {
            return { args: '', error: tr('error.numeric', { label: field.label }) }
          }
          parsed.push(n)
        } else {
          parsed.push(part)
        }
      }
      entries.push([field.key, parsed])
      continue
    }
    if (field.type === 'object-rows') {
      const parsed = parseRows(field, raw)
      if (parsed.error) return { args: '', error: parsed.error }
      entries.push([field.key, parsed.value])
      continue
    }
    const parsed = parseFieldValue(field, raw)
    if (parsed.error) return { args: '', error: parsed.error }
    entries.push([field.key, parsed.value])
  }
  return { args: JSON.stringify(Object.fromEntries(entries)) }
}

/** JSON → 字段必须无损；未知键/复杂值保留在 JSON 中，不能在切换时悄悄丢掉。 */
function fieldDraft(fields: CommandField[], args: string): Draft | null {
  if (!args.trim()) return {}
  let value: unknown
  try { value = JSON.parse(args) } catch { return null }
  if (!value || typeof value !== 'object' || Array.isArray(value)) return null
  const entries: [string, string][] = []
  for (const [key, v] of Object.entries(value)) {
    const field = fields.find((f) => f.key === key)
    if (!field) return null
    if (field.type === 'array') {
      if (!Array.isArray(v) || !v.every((item) => field.itemType === 'string'
        ? typeof item === 'string'
        : typeof item === 'number' && Number.isFinite(item) && (field.itemType !== 'integer' || Number.isInteger(item)))) return null
      entries.push([key, v.join(', ')])
    } else if (field.type === 'object-rows') {
      const text = rowsText(v, field.fields ?? [])
      if (text === null) return null
      entries.push([key, text])
    } else if (field.type === 'enum') {
      const index = field.choices?.findIndex((choice) => choice === v) ?? -1
      if (index < 0) return null
      entries.push([key, String(index)])
    } else {
      const type = field.type === 'integer' ? 'number' : field.type
      if (typeof v !== type || (type === 'number' && !Number.isFinite(v))) return null
      entries.push([key, String(v)])
    }
  }
  return Object.fromEntries(entries)
}

export function SchemaActionInput({ action, validate, description, emptyHint, validationSource,
  showTitle = true, emptyArgs = '', disabled, onEdit, renderSubmit }: SchemaActionInputProps) {
  const id = useId()
  const { t } = useTranslation('common')
  const form = useMemo(() => action.inputSchema ? commandForm(action.inputSchema) : null, [action.inputSchema])
  const unsupported = useMemo(() => action.inputSchema ? unsupportedSchemaKeywords(action.inputSchema) : [], [action.inputSchema])
  const [branch, setBranch] = useState(0)
  const [values, setValues] = useState<Draft>({})
  const [edited, setEdited] = useState(false)
  const [json, setJSON] = useState<string | null>(form ? null : emptyArgs)
  const activeFields = form?.choices ? form.choices[branch]?.fields ?? [] : form?.fields ?? []
  const built = fieldArgs(activeFields, values)
  const args = json ?? (edited ? built.args : emptyArgs)
  const error = json === null && !edited && !emptyArgs ? t('commandInput.error.required')
    : (json === null ? built.error : undefined) ?? validate(args)
  const backToFields = json !== null && activeFields.length > 0 ? fieldDraft(activeFields, json) : null
  const descriptionId = description ? id + '-hint' : undefined
  const errorId = id + '-error'
  const shownError = edited ? commandArgsErrorCopy(error) : undefined
  const describedBy = [descriptionId, shownError ? errorId : undefined].filter(Boolean).join(' ') || undefined
  const setField = (key: string, value: string) => {
    onEdit?.()
    setValues((previous) => ({ ...previous, [key]: value }))
    setEdited(true)
  }
  const chooseBranch = (index: number) => {
    onEdit?.()
    setBranch(index)
    setEdited(false)
  }

  const fieldsUI = activeFields.length > 0 ? (
    <div className="grid min-w-0 gap-2 sm:grid-cols-2 sm:gap-3">
      {activeFields.map((field, index) => {
        const fieldId = id + '-field-' + index
        const fieldDescription = field.description ? fieldId + '-description' : undefined
        const value = Object.hasOwn(values, field.key) ? values[field.key] : ''
        const fieldExamples = (field.examples ?? [])
          .map((example, exampleIndex) => exampleText(field, example, exampleIndex))
          .filter((item): item is { value: string; label: string } => item !== null)
        return (
          <div key={field.key} className={cn('min-w-0', field.type === 'object-rows' && 'sm:col-span-2')}>
            <label htmlFor={fieldId} className="mb-1 block break-words text-meta text-ink-2">
              {field.label}{field.required && <span aria-hidden="true" className="ml-1 text-ink-3">{t('commandInput.required')}</span>}
            </label>
            {fieldExamples.length > 0 && (
              <div className="mb-2 flex flex-wrap gap-1.5">
                {fieldExamples.map((example, exampleIndex) => (
                  <button key={exampleIndex} type="button" className="btn btn-ghost btn-sm"
                    onClick={() => setField(field.key, example.value)}>{example.label}</button>
                ))}
              </div>
            )}
            {field.type === 'object-rows' ? (
              <div className="min-w-0">
                <p className="mb-1 text-meta text-ink-3">
                  {t('commandInput.rowsHint', { fields: (field.fields ?? []).map((nested) => nested.label).join(t('commandInput.listSeparator')) })}
                </p>
                <textarea id={fieldId} rows={4} value={value} required={field.required}
                  placeholder={(field.fields ?? []).map((nested) => nested.label).join(', ')}
                  aria-describedby={[fieldDescription, describedBy].filter(Boolean).join(' ')}
                  onChange={(e) => setField(field.key, e.target.value)}
                  className="input input-sm min-w-0 font-mono" />
              </div>
            ) : field.type === 'enum' || field.type === 'boolean' ? (
              <Select id={fieldId} compact value={value} required={field.required}
                aria-describedby={[fieldDescription, describedBy].filter(Boolean).join(' ')}
                className="min-w-0"
                onChange={(e) => setField(field.key, e.target.value)}>
                <option value="">{t('commandInput.select')}</option>
                {field.type === 'boolean' ? <><option value="true">{t('commandInput.yes')}</option><option value="false">{t('commandInput.no')}</option></>
                  : field.choices?.map((choice, i) => <option key={i} value={String(i)}>{field.choiceLabels?.[i] ?? choiceLabel(choice)}</option>)}
              </Select>
            ) : (
              <input id={fieldId} type={field.type === 'string' || field.type === 'array' ? 'text' : 'number'}
                value={value} required={field.required}
                step={field.type === 'integer' ? 1 : field.type === 'number' ? 'any' : undefined}
                min={typeof field.schema.minimum === 'number' ? field.schema.minimum : undefined}
                max={typeof field.schema.maximum === 'number' ? field.schema.maximum : undefined}
                placeholder={field.type === 'array'
                  ? (field.minItems != null && field.minItems > 1
                    ? t('commandInput.arrayPlaceholderWithMin', { count: field.minItems })
                    : t('commandInput.arrayPlaceholder'))
                  : undefined}
                aria-describedby={[fieldDescription, describedBy].filter(Boolean).join(' ')}
                onChange={(e) => setField(field.key, e.target.value)} className="min-w-0" />
            )}
            {field.description && <p id={fieldDescription} className="mt-1 break-words text-meta text-ink-3">{field.description}</p>}
          </div>
        )
      })}
    </div>
  ) : null

  return (
    <fieldset className={cn('min-w-0', showTitle && 'border-t border-hairline pt-3')} aria-describedby={describedBy} disabled={disabled}>
      <legend className={cn('max-w-full break-words text-compact text-ink-2 [overflow-wrap:anywhere]',
        showTitle ? 'mb-1.5' : 'sr-only')}>
        {action.label}
      </legend>
      {descriptionId && <p id={descriptionId} className="mb-2 text-meta leading-relaxed text-ink-3">{description}</p>}

      {form && json === null ? (
        <>
          {form.choices && form.choices.length > 0 && (
            <div className="mb-3">
              <p className="mb-1.5 text-meta text-ink-2">{t('commandInput.settingMethod')}</p>
              <div role="radiogroup" aria-label={t('commandInput.settingMethod')} className="grid gap-2 sm:grid-cols-2">
                {form.choices.map((choice, index) => (
                  <label key={choice.key} className={cn(
                    'flex min-w-0 cursor-pointer items-start gap-2 rounded-tile border px-3 py-2 text-meta transition-colors',
                    branch === index ? 'border-accent/60 bg-accent/8 text-ink' : 'border-hairline text-ink-2 hover:bg-ink-3/5',
                  )}>
                    <input type="radio" name={id + '-branch'} checked={branch === index}
                      onChange={() => chooseBranch(index)} className="mt-0.5 shrink-0 accent-accent" />
                    <span className="min-w-0 break-words">{choice.label}</span>
                  </label>
                ))}
              </div>
            </div>
          )}
          {fieldsUI}
        </>
      ) : (
        <div>
          <label htmlFor={id + '-args'} className="mb-1 block text-meta text-ink-2">
            {action.inputSchema ? t('commandInput.technicalParams') : t('commandInput.params')}
          </label>
          <textarea id={id + '-args'} rows={form ? 3 : 2} spellCheck={false}
            aria-label={action.inputSchema ? t('commandInput.technicalParamsAria', { label: action.label }) : t('commandInput.paramsAria', { label: action.label })}
            aria-invalid={shownError ? true : undefined} aria-describedby={describedBy}
            value={json ?? args} onChange={(e) => { onEdit?.(); setJSON(e.target.value); setEdited(true) }}
            placeholder={action.inputPlaceholder ?? (form ? t('commandInput.parameterPlaceholder') : t('commandInput.params'))}
            className={cn('input input-sm min-w-0 font-mono', shownError && 'input-error')} />
        </div>
      )}

      {shownError && <p id={errorId} role="alert" className="mt-2 break-words text-meta text-bad">{shownError}</p>}
      {!edited && error && <p className="sr-only text-meta text-ink-3 sm:not-sr-only sm:mt-2 sm:block">{emptyHint}</p>}
      {unsupported.length > 0 && (json !== null || !form) && (
        <details className="mt-2 text-meta text-ink-3">
          <summary className="flex min-h-touch cursor-pointer select-none items-center transition-colors hover:text-ink-2 sm:min-h-0">{t('commandInput.unsupportedSummary', { source: validationSource })}</summary>
          <p className="mt-1 break-words">{t('commandInput.unsupportedDetail', { unsupported: unsupported.join(t('commandInput.listSeparator')), source: validationSource })}</p>
        </details>
      )}
      <div className="mt-2 flex flex-wrap items-center justify-end gap-2">
        {renderSubmit(args, error)}
      </div>
      {form && json === null && activeFields.length > 0 && (
        <div className="mt-2 flex justify-end">
          <button type="button" className="link inline-flex min-h-touch items-center text-meta text-ink-3 sm:min-h-0" aria-label={t('commandInput.technicalOptionsAria', { label: action.label })}
            onClick={() => { onEdit?.(); setJSON(args); setEdited(true) }}>
            {t('commandInput.technicalOptions')}
          </button>
        </div>
      )}
      {form && json !== null && backToFields === null && <p className="mt-2 text-meta text-ink-3">{t('commandInput.cannotConvert')}</p>}
      {form && json !== null && (
        <div className="mt-2 flex flex-wrap items-center justify-end gap-2 text-meta">
          <button type="button" className="link inline-flex min-h-touch items-center sm:min-h-0" disabled={backToFields === null} aria-label={t('commandInput.useFormAria', { label: action.label })}
            onClick={() => { if (backToFields) { setValues(backToFields); setJSON(null); setEdited(true) } }}>
            {t('commandInput.useForm')}
          </button>
        </div>
      )}
    </fieldset>
  )
}
