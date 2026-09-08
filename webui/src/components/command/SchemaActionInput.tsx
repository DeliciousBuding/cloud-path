import { useId, useMemo, useState } from 'react'
import type { ReactNode } from 'react'
import { cn } from '@/lib/cn'
import { commandFields, unsupportedSchemaKeywords } from '@/lib/command-schema'
import type { CommandField } from '@/lib/command-schema'
interface SchemaActionInputProps {
  action: { label: string; hint?: string; inputSchema?: Record<string, unknown>; inputPlaceholder?: string }
  validate: (args: string) => string | undefined
  description: ReactNode
  emptyHint: string
  validationSource: string
  /** Empty object is an explicit no-arguments request, never schema defaults. */
  emptyArgs?: string
  disabled?: boolean
  onEdit?: () => void
  renderSubmit: (args: string, error?: string) => ReactNode
}

type Draft = Record<string, string>

function fieldArgs(fields: CommandField[], values: Draft): { args: string; error?: string } {
  const entries: [string, unknown][] = []
  for (const field of fields) {
    const raw = Object.hasOwn(values, field.key) ? values[field.key] : undefined
    if (raw === undefined || (raw === '' && field.type !== 'string')) continue
    let value: unknown = raw
    if (field.type === 'number' || field.type === 'integer') {
      value = Number(raw)
      if (!Number.isFinite(value)) return { args: '', error: field.label + '：请输入有限数值' }
    } else if (field.type === 'boolean') value = raw === 'true'
    else if (field.type === 'enum') value = field.choices?.[Number(raw)]
    entries.push([field.key, value])
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
    if (field.type === 'enum') {
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
  emptyArgs = '', disabled, onEdit, renderSubmit }: SchemaActionInputProps) {
  const id = useId()
  const fields = useMemo(() => action.inputSchema ? commandFields(action.inputSchema) : null, [action.inputSchema])
  const unsupported = useMemo(() => action.inputSchema ? unsupportedSchemaKeywords(action.inputSchema) : [], [action.inputSchema])
  const [values, setValues] = useState<Draft>({})
  const [edited, setEdited] = useState(false)
  const [json, setJSON] = useState<string | null>(fields ? null : emptyArgs)
  const built = fieldArgs(fields ?? [], values)
  const args = json ?? (edited ? built.args : emptyArgs)
  const error = json === null && !edited && !emptyArgs ? '请填写参数'
    : (json === null ? built.error : undefined) ?? validate(args)
  const backToFields = fields && json !== null ? fieldDraft(fields, json) : null
  const descriptionId = id + '-hint'
  const errorId = id + '-error'
  const shownError = edited ? error : undefined
  const describedBy = descriptionId + (shownError ? ' ' + errorId : '')
  const setField = (key: string, value: string) => {
    onEdit?.()
    setValues((previous) => ({ ...previous, [key]: value }))
    setEdited(true)
  }

  return (
    <fieldset className="min-w-0 border-t border-hairline pt-3" aria-describedby={describedBy} disabled={disabled}>
      <legend className="mb-1.5 max-w-full break-words text-[13px] text-ink-2 [overflow-wrap:anywhere]">
        {action.label}{action.hint ? ' · ' + action.hint : ''}
      </legend>
      <p id={descriptionId} className="mb-2 text-[12px] leading-relaxed text-ink-3">
        {description}
        {fields ? '未输入的字段不发送，默认值不自动代入。' : action.inputSchema ? '此声明使用 JSON 输入。' : ''}
      </p>
      {fields && json === null ? (
        <div className="grid min-w-0 gap-3 sm:grid-cols-2">
          {fields.map((field, index) => {
            const fieldId = id + '-field-' + index
            const fieldDescription = field.description ? fieldId + '-description' : undefined
            return (
              <div key={field.key} className="min-w-0">
                <label htmlFor={fieldId} className="mb-1 block break-words text-[12px] text-ink-2">
                  {field.label}{field.required && <span aria-hidden="true" className="ml-1 text-ink-3">必填</span>}
                </label>
                {field.type === 'enum' || field.type === 'boolean' ? (
                  <select id={fieldId} value={Object.hasOwn(values, field.key) ? values[field.key] : ''} required={field.required}
                    aria-describedby={[fieldDescription, describedBy].filter(Boolean).join(' ')}
                    className="input input-sm min-w-0"
                    onChange={(e) => setField(field.key, e.target.value)}>
                    <option value="">请选择</option>
                    {field.type === 'boolean' ? <><option value="true">是</option><option value="false">否</option></>
                      : field.choices?.map((value, i) => <option key={i} value={String(i)}>{typeof value === 'string' && value ? value : JSON.stringify(value)}</option>)}
                  </select>
                ) : (
                  <input id={fieldId} type={field.type === 'string' ? 'text' : 'number'}
                    value={Object.hasOwn(values, field.key) ? values[field.key] : ''} required={field.required}
                    step={field.type === 'integer' ? 1 : field.type === 'number' ? 'any' : undefined}
                    min={typeof field.schema.minimum === 'number' ? field.schema.minimum : undefined}
                    max={typeof field.schema.maximum === 'number' ? field.schema.maximum : undefined}
                    aria-describedby={[fieldDescription, describedBy].filter(Boolean).join(' ')}
                    onChange={(e) => setField(field.key, e.target.value)} className="input input-sm min-w-0" />
                )}
                {field.description && <p id={fieldDescription} className="mt-1 break-words text-[12px] text-ink-3">{field.description}</p>}
              </div>
            )
          })}
        </div>
      ) : (
        <div>
          <label htmlFor={id + '-args'} className="mb-1 block text-[12px] text-ink-2">
            {action.inputSchema ? 'JSON 参数' : '参数'}
          </label>
          <textarea id={id + '-args'} rows={action.inputSchema ? 3 : 2} spellCheck={false}
            aria-label={action.label + (action.inputSchema ? ' JSON 参数' : ' 参数')}
            aria-invalid={shownError ? true : undefined} aria-describedby={describedBy}
            value={args} onChange={(e) => { onEdit?.(); setJSON(e.target.value); setEdited(true) }}
            placeholder={action.inputPlaceholder ?? (action.inputSchema ? '填写单行 JSON' : '参数')}
            className={cn('input input-sm min-w-0 font-mono', shownError && 'input-error')} />
        </div>
      )}
      {shownError && <p id={errorId} role="alert" className="mt-2 break-words text-[12px] text-bad">{shownError}</p>}
      {!edited && error && <p className="mt-2 text-[12px] text-ink-3">{emptyHint}</p>}
      {unsupported.length > 0 && <p className="mt-2 break-words text-[12px] text-ink-3">
        本地仅检查 JSON 与已支持的约束；未校验：{unsupported.join('、')}。完整校验以{validationSource}为准。
      </p>}
      {fields && json !== null && backToFields === null && <p className="mt-2 text-[12px] text-ink-3">
        当前 JSON 无法无损转为字段，请继续在 JSON 中编辑。
      </p>}
      <div className="mt-2 flex flex-wrap items-center justify-end gap-2">
        {fields && <button type="button" className="btn btn-ghost" disabled={json !== null && backToFields === null}
          aria-label={action.label + (json === null ? ' 编辑 JSON' : ' 使用字段')}
          onClick={() => {
            if (json === null) setJSON(args)
            else if (backToFields) { setValues(backToFields); setEdited(Boolean(json.trim())); setJSON(null) }
          }}>{json === null ? '编辑 JSON' : '使用字段'}</button>}
        {renderSubmit(args, error)}
      </div>
    </fieldset>
  )
}
