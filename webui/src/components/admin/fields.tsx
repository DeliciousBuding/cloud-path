// 管理面表单原语：带标签的下拉与复选行。
// 提示文案一律放在 label 之外、用 aria-describedby 关联 —— 这样控件的可读名称只含标签本身，
// 不会被长提示污染（TextField 在 components/ui.tsx 里已经是同一套做法）。
import { useId } from 'react'
import { cn } from '@/lib/cn'

export interface FieldOption { value: string; label: string }

export function SelectField({ label, hint, value, onChange, options, className }: {
  label: string
  hint?: string
  value: string
  onChange: (v: string) => void
  options: FieldOption[]
  className?: string
}) {
  const id = useId()
  const desc = hint ? `${id}-desc` : undefined
  return (
    <div className={className}>
      <label htmlFor={id} className="mb-1.5 block text-[13px] font-medium text-ink-2">{label}</label>
      <select
        id={id}
        value={value}
        aria-describedby={desc}
        onChange={(ev) => onChange(ev.target.value)}
        className="input"
      >
        {options.map((o) => <option key={o.value} value={o.value}>{o.label}</option>)}
      </select>
      {desc && <p id={desc} className="mt-1.5 text-xs leading-relaxed text-ink-3 break-words">{hint}</p>}
    </div>
  )
}

export function CheckRow({ label, hint, checked, onChange, tone = 'plain', className }: {
  label: string
  hint?: string
  checked: boolean
  onChange: (v: boolean) => void
  /** danger：高风险范围/不可逆操作，标签用警示色 */
  tone?: 'plain' | 'danger'
  className?: string
}) {
  const id = useId()
  const desc = hint ? `${id}-desc` : undefined
  return (
    <div className={cn('flex items-start gap-2.5', className)}>
      <input
        id={id}
        type="checkbox"
        checked={checked}
        aria-describedby={desc}
        onChange={(ev) => onChange(ev.target.checked)}
        className="mt-0.5 h-4 w-4 shrink-0 accent-[var(--color-accent)]"
      />
      <div className="min-w-0">
        <label
          htmlFor={id}
          className={cn('num block text-[13px] font-medium leading-relaxed break-words',
            tone === 'danger' ? 'text-warn' : 'text-ink')}
        >
          {label}
        </label>
        {desc && <p id={desc} className="mt-0.5 text-xs leading-relaxed text-ink-3 break-words">{hint}</p>}
      </div>
    </div>
  )
}