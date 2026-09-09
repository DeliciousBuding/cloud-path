// 基础 UI 原语（Apple 极简）：布局/状态、导航、表单、动作和反馈。
// 组件只消费 index.css 的语义 token（Tailwind 主题类或 .btn/.input/.card 基类），禁止裸色值。
// 页面和领域组件不得直写 input/textarea/select；统一走这里的 primitive。
import { Children, Fragment, isValidElement, useEffect, useId, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import '@/i18n'
import type { ChangeEvent, ComponentPropsWithRef, FocusEvent as ReactFocusEvent, InputHTMLAttributes, KeyboardEvent as ReactKeyboardEvent, ReactNode, SelectHTMLAttributes, TextareaHTMLAttributes } from 'react'
import { ArrowLeft, Check, ChevronDown, Monitor, Moon, RefreshCw, Sun } from 'lucide-react'
import { Link } from 'react-router'
import type { LinkProps } from 'react-router'
import { cn } from '@/lib/cn'
import { getTheme, setTheme } from '@/lib/theme'
import type { ThemeMode } from '@/lib/theme'
import { Logo } from './Logo'

export type Tone = 'ok' | 'warn' | 'bad' | 'accent' | 'idle'

/** 语义色 → 胶囊底色（导出给 SchemaRenderer 等复用，避免各处重复调色板） */
export const TONE_CLS: Record<Tone, string> = {
  ok: 'bg-ok/12 text-ok',
  warn: 'bg-warn/14 text-warn',
  bad: 'bg-bad/12 text-bad',
  accent: 'bg-accent/10 text-accent',
  idle: 'bg-ink-3/10 text-ink-2',
}

/** 语义色 → 前景文字色 */
export const TONE_TEXT_CLS: Record<Tone, string> = {
  ok: 'text-ok', warn: 'text-warn', bad: 'text-bad', accent: 'text-accent', idle: 'text-ink-3',
}

export function Badge({ tone = 'idle', children, className }: { tone?: Tone; children: ReactNode; className?: string }) {
  return <span className={cn('badge', TONE_CLS[tone], className)}>{children}</span>
}

export function StatusDot({ online, className }: { online: boolean; className?: string }) {
  return (
    <span
      aria-hidden="true"
      className={cn('inline-block h-2 w-2 shrink-0 rounded-pill',
        online ? 'bg-ok' : 'bg-idle/50', className)}
    />
  )
}

export function Panel({ title, right, className, children }: {
  title?: ReactNode; right?: ReactNode; className?: string; children: ReactNode
}) {
  return (
    <section className={cn('card p-4 sm:p-5', className)}>
      {(title || right) && (
        <div className="mb-4 flex items-center justify-between gap-3">
          {title && <h2 className="text-lead font-semibold tracking-[-0.01em]">{title}</h2>}
          {right && <div className={cn(!title && 'ml-auto')}>{right}</div>}
        </div>
      )}
      {children}
    </section>
  )
}

export function PageHeader({ title, subtitle, actions }: {
  title: string; subtitle?: ReactNode; actions?: ReactNode
}) {
  return (
    <header className="mb-6 flex flex-wrap items-start justify-between gap-x-4 gap-y-3 sm:mb-8 sm:items-end">
      <div className="min-w-0">
        {/* -0.025em 档负字距是拉丁刻度；中文标题字面全角，超过 -0.01em 会挤，故用 CJK 安全值 */}
        <h1 className="text-title font-semibold leading-tight tracking-[-0.01em] sm:text-page-title">{title}</h1>
        {subtitle && <p className="mt-1.5 max-w-[62ch] text-body text-ink-2">{subtitle}</p>}
      </div>
      {actions && <div className="flex shrink-0 items-center gap-2">{actions}</div>}
    </header>
  )
}

export function StatTile({ icon, label, value, unit, sub }: {
  icon?: ReactNode; label: string; value: ReactNode; unit?: string; sub?: ReactNode
}) {
  return (
    // 390px：两列统计瓦片内宽仅约 8rem，长版本号等不可断字符串必须换行，否则撑出横向滚动
    <div className="card min-w-0 p-3.5 sm:p-4">
      <div className="flex min-w-0 items-center gap-1.5 text-meta font-medium text-ink-2">
        {icon}<span className="truncate">{label}</span>
      </div>
      <div className="metric num mt-1.5 break-words text-title font-semibold leading-none sm:text-display">
        {value}
        {unit && <span className="ml-1 text-body font-normal text-ink-3">{unit}</span>}
      </div>
      {/* sub 行恒预留：peer 瓦片共享 label→value→detail 内部行（Vercel 节奏纪律），高度结构一致不互撑 */}
      <div className="mt-0.5 min-h-4 text-meta text-ink-3 break-words">{sub}</div>
    </div>
  )
}

export function EmptyState({ icon, title, hint, action, compact, plain }: {
  icon?: ReactNode; title: string; hint?: string; action?: ReactNode; compact?: boolean; plain?: boolean
}) {
  return (
    <div className={cn(
      'flex flex-col items-center justify-center px-6 text-center',
      !plain && 'card',
      compact ? 'py-8' : 'py-12',
    )}>
      <span aria-hidden="true" className="text-ink-3">{icon}</span>
      <p className="mt-3 text-lead font-semibold">{title}</p>
      {hint && <p className="mt-1 max-w-sm text-body text-ink-2">{hint}</p>}
      {action && <div className="mt-4">{action}</div>}
    </div>
  )
}

/**
 * 错误态：说清「拿不到什么」+ 一键重试。
 * 不复述服务端技术细节，也不把错误渲染成空白（空白 = 用户以为没数据）。
 */
export function ErrorState({ icon, title, hint, onRetry, retrying, compact, plain, retryLabel }: {
  icon?: ReactNode; title: string; hint?: ReactNode
  onRetry?: () => void; retrying?: boolean; compact?: boolean; plain?: boolean; retryLabel?: string
}) {
  const { t } = useTranslation()
  const retryText = retryLabel ?? t('ui.reload')
  return (
    <div
      role="alert"
      className={cn('flex flex-col items-center justify-center px-6 text-center',
        !plain && 'card', compact ? 'py-8' : 'py-12')}
    >
      <span aria-hidden="true" className="text-bad">{icon ?? <RefreshCw size={22} />}</span>
      <p className="mt-3 text-lead font-semibold">{title}</p>
      {hint && <p className="mt-1 max-w-md text-body break-words text-ink-2">{hint}</p>}
      {onRetry && (
        <Button className="mt-5" loading={retrying} onClick={onRetry}>
          {!retrying && <RefreshCw size={13} />} {retryText}
        </Button>
      )}
    </div>
  )
}

export function Segmented<T extends string>({ options, value, onChange, label }: {
  options: { value: T; label: string; icon?: ReactNode }[]
  value: T
  onChange: (v: T) => void
  /** 分组可读名称（读屏用户需要知道这组按钮在切换什么） */
  label?: string
}) {
  const { t } = useTranslation()
  const groupLabel = label ?? t('ui.viewSwitch')
  return (
    <div className="inline-flex max-w-full rounded-pill bg-ink-3/10 p-0.5" role="group" aria-label={groupLabel}>
      {options.map((o) => (
        <button
          key={o.value}
          type="button"
          aria-pressed={value === o.value}
          onClick={() => onChange(o.value)}
          className={cn(
            'inline-flex min-h-touch items-center gap-1 rounded-pill px-3 py-1 text-meta font-medium transition-colors sm:min-h-0',
            value === o.value
              ? 'bg-surface text-ink shadow-sm'
              : 'text-ink-2 hover:text-ink',
          )}
        >
          {o.icon}{o.label}
        </button>
      ))}
    </div>
  )
}

/* ------------------------------------------------------------------ *
 * Tabs：详情页的分区切换（真实 tablist 语义 + roving tabindex + 方向键）
 * 与 Segmented 的区别：Segmented 是同一批数据的视图切换，Tabs 是不同内容分区。
 * ------------------------------------------------------------------ */

export interface TabItem<T extends string> {
  value: T
  label: string
  icon?: ReactNode
  /** 可选计数（事件数 / 能力数）；0 也显示，因为「0 条」本身是事实 */
  count?: number
}

export function TabBar<T extends string>({ items, value, onChange, label }: {
  items: TabItem<T>[]
  value: T
  onChange: (v: T) => void
  label?: string
}) {
  const { t } = useTranslation()
  const tablistLabel = label ?? t('ui.tabSwitch')
  const idx = Math.max(0, items.findIndex((i) => i.value === value))

  const move = (delta: number) => {
    if (items.length === 0) return
    const next = (idx + delta + items.length) % items.length
    const item = items[next]
    if (item) onChange(item.value)
  }

  return (
    // 390px：标签条在自身容器内横向滚动，不把溢出推给 body（与移动端主导航同一手法）
    <div className="-mx-1 overflow-x-auto px-1 pb-1">
      <div
        role="tablist" aria-label={tablistLabel}
        className="flex min-w-max gap-5 border-b border-hairline"
        onKeyDown={(e) => {
          if (e.key === 'ArrowRight') { e.preventDefault(); move(1) }
          else if (e.key === 'ArrowLeft') { e.preventDefault(); move(-1) }
          else if (e.key === 'Home') { e.preventDefault(); if (items[0]) onChange(items[0].value) }
          else if (e.key === 'End') {
            e.preventDefault()
            const last = items[items.length - 1]
            if (last) onChange(last.value)
          }
        }}
      >
        {items.map((it) => {
          const selected = it.value === value
          return (
            <button
              key={it.value}
              type="button" role="tab"
              id={`tab-${it.value}`}
              aria-selected={selected}
              aria-controls={`tabpanel-${it.value}`}
              tabIndex={selected ? 0 : -1}
              onClick={() => onChange(it.value)}
              className={cn(
                '-mb-px inline-flex min-h-touch items-center gap-1.5 border-b-2 px-0.5 pb-2 text-compact font-medium whitespace-nowrap transition-colors sm:min-h-0',
                selected ? 'border-ink text-ink' : 'border-transparent text-ink-3 hover:text-ink-2',
              )}
            >
              {it.icon}
              {it.label}
              {typeof it.count === 'number' && (
                <span className="num text-meta text-ink-3">
                  {it.count}
                </span>
              )}
            </button>
          )
        })}
      </div>
    </div>
  )
}

/** 与 TabBar 配对的tabpanel 外壳（id/aria-labelledby 必须成对，否则读屏找不到归属） */
export function TabPanel<T extends string>({ value, children, className }: {
  value: T; children: ReactNode; className?: string
}) {
  return (
    <div
      role="tabpanel" id={`tabpanel-${value}`} aria-labelledby={`tab-${value}`}
      tabIndex={0}
      className={cn('min-w-0 outline-none fade-up', className)}
    >
      {children}
    </div>
  )
}

/** 键值行（详情页/系统页的定义列表项） */
export function KeyValue({ k, v, mono, wrap }: { k: ReactNode; v: ReactNode; mono?: boolean; wrap?: boolean }) {
  return (
    <div className="kv">
      <dt>{k}</dt>
      <dd className={cn('min-w-0', wrap ? 'break-words' : 'truncate', mono && 'num font-mono text-meta text-ink-2')}>{v}</dd>
    </div>
  )
}

/** 细线加载指示（行内使用） */
export function Spinner({ size = 14, className }: { size?: number; className?: string }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" className={cn('animate-spin', className)} aria-hidden>
      <circle cx="12" cy="12" r="9" stroke="currentColor" strokeWidth="2.5" opacity=".2" fill="none" />
      <path d="M21 12a9 9 0 0 0-9-9" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" fill="none" />
    </svg>
  )
}


/** 通用按钮：variant/size 只映射设计系统基类，调用方不再自行拼 btn-*。 */
export type ButtonVariant = 'primary' | 'ghost' | 'quiet' | 'bare' | 'danger' | 'danger-ghost'
export type ButtonSize = 'sm' | 'md' | 'lg'

const BUTTON_VARIANT_CLS: Record<ButtonVariant, string> = {
  primary: 'btn-primary',
  ghost: 'btn-ghost',
  quiet: 'btn-quiet',
  bare: 'btn-bare',
  danger: 'btn-danger',
  'danger-ghost': 'btn-danger-ghost',
}

export function Button({
  variant = 'primary', size = 'md', lg, loading = false,
  className, children, disabled, type = 'button', ...rest
}: {
  variant?: ButtonVariant
  size?: ButtonSize
  /** 兼容旧调用；新代码用 size="lg"。 */
  lg?: boolean
  loading?: boolean
} & ComponentPropsWithRef<'button'>) {
  const resolvedSize: ButtonSize = lg ? 'lg' : size
  return (
    <button
      type={type}
      className={cn(
        'btn', BUTTON_VARIANT_CLS[variant],
        resolvedSize === 'sm' && 'btn-sm',
        resolvedSize === 'lg' && 'btn-lg',
        className,
      )}
      disabled={disabled || loading}
      aria-busy={loading || undefined}
      {...rest}
    >
      {loading && <Spinner size={13} />}
      {children}
    </button>
  )
}

/** 图标按钮：可读名称必填，视觉只保留图标，触控目标仍由 token 管理。 */
export function IconButton({
  label, variant = 'bare', size = 'md', className, children, type = 'button', ...rest
}: {
  label: string
  variant?: ButtonVariant
  size?: 'sm' | 'md'
} & Omit<ComponentPropsWithRef<'button'>, 'aria-label'>) {
  return (
    <Button
      type={type}
      variant={variant}
      aria-label={label}
      className={cn('btn-icon', size === 'sm' && 'btn-icon-sm', className)}
      {...rest}
    >
      {children}
    </Button>
  )
}

/** 链接按钮：保留 Link 的导航语义，视觉复用 Button 的 variant/size。 */
export function ButtonLink({
  variant = 'primary', size = 'md', className, children, ...rest
}: {
  variant?: ButtonVariant
  size?: ButtonSize
} & LinkProps) {
  return (
    <Link
      className={cn(
        'btn', BUTTON_VARIANT_CLS[variant],
        size === 'sm' && 'btn-sm',
        size === 'lg' && 'btn-lg',
        className,
      )}
      {...rest}
    >
      {children}
    </Link>
  )
}

/** 单行文本输入：只负责 .input 语义类和尺寸，标签/提示由 TextField 或领域组件负责。 */
export function Input({
  compact, error, className, ...rest
}: { compact?: boolean; error?: boolean } & InputHTMLAttributes<HTMLInputElement>) {
  return (
    <input
      className={cn('input', compact && 'input-sm', error && 'input-error', className)}
      {...rest}
    />
  )
}

/** 多行文本输入：与 Input 共用 token、边框、聚焦环和错误态。 */
export function Textarea({ compact, error, className, ...rest }: {
  compact?: boolean
  error?: boolean
} & TextareaHTMLAttributes<HTMLTextAreaElement>) {
  return <textarea className={cn('input', compact && 'input-sm', error && 'input-error', className)} {...rest} />
}

/** 复选框：视觉走 .checkbox，业务标签由调用方用 label 关联。 */
export function Checkbox({ className, ...rest }: Omit<InputHTMLAttributes<HTMLInputElement>, 'type'>) {
  return <input type="checkbox" className={cn('checkbox', className)} {...rest} />
}

/** 单选框：视觉走 .radio，业务标签由调用方用 label 关联。 */
export function Radio({ className, ...rest }: Omit<InputHTMLAttributes<HTMLInputElement>, 'type'>) {
  return <input type="radio" className={cn('radio', className)} {...rest} />
}

type SelectOptionItem = {
  value: string
  label: ReactNode
  text: string
  disabled: boolean
  group?: string
}

function optionText(label: ReactNode): string {
  return typeof label === 'string' || typeof label === 'number' ? String(label) : ''
}

function optionsFromChildren(children: ReactNode): SelectOptionItem[] {
  const options: SelectOptionItem[] = []
  const appendOption = (child: React.ReactElement, group?: string) => {
    if (child.type !== 'option') return
    const props = child.props as {
      value?: string | number
      label?: string
      disabled?: boolean
      children?: ReactNode
    }
    const value = props.value === undefined ? String(props.children ?? '') : String(props.value)
    const label = props.label ?? props.children ?? value
    options.push({ value, label, text: optionText(label), disabled: Boolean(props.disabled), group })
  }
  Children.forEach(children, (child) => {
    if (!isValidElement(child)) return
    if (child.type === 'optgroup') {
      const props = child.props as { label?: ReactNode; children?: ReactNode }
      const group = optionText(props.label)
      Children.forEach(props.children, (nested) => {
        if (isValidElement(nested)) appendOption(nested, group || undefined)
      })
      return
    }
    appendOption(child)
  })
  return options
}

/**
 * 可主题化下拉框。
 *
 * 视觉交互由 DOM 中的 combobox + listbox 承担，因此弹层也能完整使用设计 token；
 * 视觉隐藏的原生 select 保留表单语义、读屏兼容和现有测试 API，但不负责弹层绘制。
 */
export function Select({
  pill, compact, className, children, value, defaultValue, onChange,
  disabled, id, name, required, onBlur, ...rest
}: {
  pill?: boolean
  compact?: boolean
  children?: ReactNode
} & Omit<SelectHTMLAttributes<HTMLSelectElement>, 'children'>) {
  const options = useMemo(() => optionsFromChildren(children), [children])
  const controlled = value !== undefined
  const initialValue = controlled ? value : defaultValue
  const [internalValue, setInternalValue] = useState(() => {
    if (initialValue !== undefined && initialValue !== null) return String(initialValue)
    return options[0]?.value ?? ''
  })
  const currentValue = controlled ? String(value) : internalValue
  const selectedIndex = options.findIndex((option) => option.value === currentValue)
  const selected = selectedIndex >= 0 ? options[selectedIndex] : options[0]
  const [open, setOpen] = useState(false)
  const [activeIndex, setActiveIndex] = useState(0)
  const rootRef = useRef<HTMLDivElement>(null)
  const selectRef = useRef<HTMLSelectElement>(null)
  const listboxId = useId()
  const optionId = (index: number) => `${listboxId}-option-${index}`

  useEffect(() => {
    if (!controlled && options.length > 0 && !options.some((option) => option.value === internalValue)) {
      setInternalValue(options[0].value)
    }
  }, [controlled, internalValue, options])

  useEffect(() => {
    if (!open) return
    const next = selectedIndex >= 0 ? selectedIndex : options.findIndex((option) => !option.disabled)
    setActiveIndex(next >= 0 ? next : 0)
  }, [open, options, selectedIndex])

  useEffect(() => {
    if (!open) return
    const onPointerDown = (event: globalThis.PointerEvent) => {
      if (!rootRef.current?.contains(event.target as Node)) setOpen(false)
    }
    const onKeyDown = (event: globalThis.KeyboardEvent) => {
      if (event.key === 'Escape') {
        setOpen(false)
        selectRef.current?.focus()
      }
    }
    document.addEventListener('pointerdown', onPointerDown)
    document.addEventListener('keydown', onKeyDown)
    return () => {
      document.removeEventListener('pointerdown', onPointerDown)
      document.removeEventListener('keydown', onKeyDown)
    }
  }, [open])

  function emitChange(nextValue: string) {
    if (!controlled) setInternalValue(nextValue)
    const target = selectRef.current
    if (!target) return
    target.value = nextValue
    onChange?.({ target, currentTarget: target } as ChangeEvent<HTMLSelectElement>)
  }

  function choose(option: SelectOptionItem) {
    if (option.disabled) return
    emitChange(option.value)
    setOpen(false)
    selectRef.current?.focus()
  }

  function firstEnabledIndex(): number {
    return options.findIndex((option) => !option.disabled)
  }

  function lastEnabledIndex(): number {
    for (let index = options.length - 1; index >= 0; index -= 1) {
      if (!options[index].disabled) return index
    }
    return -1
  }

  function moveActive(delta: 1 | -1) {
    if (options.length === 0) return
    let next = activeIndex
    for (let step = 0; step < options.length; step += 1) {
      next = (next + delta + options.length) % options.length
      if (!options[next].disabled) {
        setActiveIndex(next)
        return
      }
    }
  }

  function handleKeyDown(event: ReactKeyboardEvent<HTMLSelectElement>) {
    if (disabled) return
    if (event.key === 'Tab') {
      setOpen(false)
      return
    }
    if (event.key === 'Escape') {
      if (open) event.preventDefault()
      setOpen(false)
      return
    }
    if (!open && ['ArrowDown', 'ArrowUp', 'Enter', ' '].includes(event.key)) {
      event.preventDefault()
      setOpen(true)
      return
    }
    if (!open) return
    if (event.key === 'ArrowDown') {
      event.preventDefault()
      moveActive(1)
    } else if (event.key === 'ArrowUp') {
      event.preventDefault()
      moveActive(-1)
    } else if (event.key === 'Home') {
      event.preventDefault()
      const next = firstEnabledIndex()
      if (next >= 0) setActiveIndex(next)
    } else if (event.key === 'End') {
      event.preventDefault()
      const next = lastEnabledIndex()
      if (next >= 0) setActiveIndex(next)
    } else if (event.key === 'Enter' || event.key === ' ') {
      event.preventDefault()
      const option = options[activeIndex]
      if (option) choose(option)
    }
  }

  function handleNativeChange(event: ChangeEvent<HTMLSelectElement>) {
    if (!controlled) setInternalValue(event.target.value)
    onChange?.(event)
  }

  function handleBlur(event: ReactFocusEvent<HTMLSelectElement>) {
    if (!rootRef.current?.contains(event.relatedTarget as Node)) setOpen(false)
    onBlur?.(event)
  }

  return (
    <div ref={rootRef} className={cn('select-wrap', pill && 'select-wrap-pill', className)}>
      <select
        {...rest}
        ref={selectRef}
        id={id}
        name={name}
        required={required}
        disabled={disabled}
        value={currentValue}
        onChange={handleNativeChange}
        onKeyDown={handleKeyDown}
        onBlur={handleBlur}
        aria-expanded={open}
        aria-controls={open ? listboxId : undefined}
        aria-activedescendant={open && activeIndex >= 0 ? optionId(activeIndex) : undefined}
        className="sr-only"
      >
        {children}
      </select>
      <button
        type="button"
        tabIndex={-1}
        aria-hidden="true"
        disabled={disabled}
        onClick={() => setOpen((current) => !current)}
        className={cn('select select-trigger', pill && 'select-pill', compact && 'select-sm')}
      >
        <span className="min-w-0 truncate" title={selected?.text || undefined}>{selected?.label ?? ''}</span>
        <ChevronDown size={14} className={cn('shrink-0 text-ink-3 transition-transform', open && 'rotate-180')} />
      </button>
      {open && (
        <div className="select-menu">
          <ul id={listboxId} role="listbox" aria-label={rest['aria-label']} className="select-list">
            {options.map((option, index) => (
              <Fragment key={option.value}>
                {option.group && (index === 0 || options[index - 1]?.group !== option.group) && (
                  <li role="presentation" className="select-group">{option.group}</li>
                )}
                <li
                  id={optionId(index)}
                  role="option"
                  aria-selected={option.value === currentValue}
                  aria-disabled={option.disabled || undefined}
                  data-active={index === activeIndex ? 'true' : undefined}
                  className="select-option"
                  onMouseDown={(event) => event.preventDefault()}
                  onMouseEnter={() => { if (!option.disabled) setActiveIndex(index) }}
                  onClick={() => choose(option)}
                >
                  <span className="min-w-0 truncate" title={option.text || undefined}>{option.label}</span>
                  {option.value === currentValue && <Check size={14} className="shrink-0" aria-hidden="true" />}
                </li>
              </Fragment>
            ))}
          </ul>
        </div>
      )}
    </div>
  )
}

/** 带标签/提示/错误的表单输入行（error 优先于 hint 展示）。
 *  `suffix` 挂在输入框右侧（如密码显示/隐藏切换）：输入框自动留出右内边距，不与文字重叠。 */
export function TextField({ label, hint, error, className, suffix, ...rest }: {
  label: string
  hint?: string
  error?: string
  suffix?: ReactNode
} & InputHTMLAttributes<HTMLInputElement>) {
  const id = useId()
  // error 为空串时视为「无错误」，必须回落到 hint：
  // 旧写法 `error ?? hint` 会让空串吃掉提示文案（登录页的令牌说明因此既不显示也读不到）。
  const message = error || hint
  const desc = message ? `${id}-desc` : undefined
  return (
    <div className={className}>
      <label htmlFor={id} className="mb-1.5 block text-compact font-medium text-ink-2">{label}</label>
      <div className="relative">
        <Input
          id={id}
          aria-invalid={error ? true : undefined}
          aria-describedby={desc}
          error={Boolean(error)}
          className={suffix ? 'pr-11' : undefined}
          {...rest}
        />
        {suffix && <div className="absolute inset-y-0 right-1.5 flex items-center">{suffix}</div>}
      </div>
      {desc && (
        <p id={desc} className={cn('mt-1.5 text-meta', error ? 'text-bad' : 'text-ink-3')}>
          {message}
        </p>
      )}
    </div>
  )
}

/** 主题快速切换：供 Login/Setup 等脱离 Layout 侧栏的独立页使用。
 *  与侧栏 ThemeControl 同一三态语义（浅色 → 深色 → 跟随系统）循环，
 *  图标反映当前模式、文案预告下一模式——不让「跟随系统」在独立页被悄悄丢掉。 */
export function ThemeToggle({ className }: { className?: string }) {
  const { t } = useTranslation()
  const [mode, setMode] = useState<ThemeMode>(() => getTheme())
  const META: Record<ThemeMode, { icon: typeof Sun; label: string }> = {
    light: { icon: Sun, label: t('theme.light') },
    dark: { icon: Moon, label: t('theme.dark') },
    system: { icon: Monitor, label: t('theme.system') },
  }
  const next: ThemeMode = mode === 'light' ? 'dark' : mode === 'dark' ? 'system' : 'light'
  const Cur = META[mode].icon
  return (
    <button
      type="button"
      title={t('theme.switchTitle', { current: META[mode].label, next: META[next].label })}
      aria-label={t('theme.switchTo', { label: META[next].label })}
      onClick={() => { setTheme(next); setMode(next) }}
      className={cn(
        'flex h-touch w-touch items-center justify-center rounded-pill border border-hairline sm:h-8 sm:w-8',
        'bg-surface/70 text-ink-2 transition-colors hover:text-ink',
        className,
      )}
    >
      <Cur aria-hidden="true" size={15} strokeWidth={2} />
    </button>
  )
}

/** 认证/引导页外壳：品牌区 + 居中卡片 + 右上角主题切换（390px 无横向溢出） */
export function AuthCard({ title, subtitle, children, footer }: {
  title: string
  subtitle?: ReactNode
  children: ReactNode
  footer?: ReactNode
}) {
  return (
    <div className="relative flex min-h-dvh items-center justify-center overflow-x-hidden px-4 py-10">
      <ThemeToggle className="absolute right-4 top-4" />
      <div className="w-full max-w-sm fade-up">
        <div className="mb-8 flex flex-col items-center text-center">
          <span aria-hidden="true" className="flex h-14 w-14 items-center justify-center rounded-card bg-accent/10 text-accent">
            <Logo size={30} />
          </span>
          <h1 className="mt-4 text-title font-semibold leading-tight tracking-[-0.01em]">{title}</h1>
          {subtitle && <p className="mt-1.5 text-body text-ink-2">{subtitle}</p>}
        </div>
        <div className="card p-6">{children}</div>
        {footer && <div className="mt-5 text-center text-meta text-ink-3">{footer}</div>}
      </div>
    </div>
  )
}

/** 详情页统一返回链（fade-in + hover 强调色）；to/label 由调用页声明 */
export function BackLink({ to, label }: { to: string; label: string }) {
  return (
    <Link to={to}
      className="mb-5 inline-flex min-h-touch items-center gap-1 text-body text-ink-2 transition-colors hover:text-accent fade-up sm:min-h-0">
      <ArrowLeft size={15} /> {label}
    </Link>
  )
}
