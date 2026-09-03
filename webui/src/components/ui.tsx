// 基础 UI 原语（Apple 极简）：Badge / StatusDot / Panel / PageHeader / StatTile / EmptyState /
// Segmented / KeyValue / Spinner / Button / TextField / ThemeToggle / AuthCard
// 颜色一律走 index.css token（Tailwind 主题类或 .btn/.input/.card 基类），组件内禁止裸色值。
import { useId, useState } from 'react'
import type { ButtonHTMLAttributes, InputHTMLAttributes, ReactNode } from 'react'
import { Moon, Sun } from 'lucide-react'
import { cn } from '@/lib/cn'
import { setTheme } from '@/lib/theme'
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
      className={cn('inline-block h-2 w-2 shrink-0 rounded-full',
        online ? 'bg-ok dot-online' : 'bg-idle/50', className)}
    />
  )
}

export function Panel({ title, right, className, children }: {
  title?: ReactNode; right?: ReactNode; className?: string; children: ReactNode
}) {
  return (
    <section className={cn('card p-5', className)}>
      {(title || right) && (
        <div className="mb-4 flex items-center justify-between gap-3">
          <h2 className="text-[15px] font-semibold tracking-tight">{title}</h2>
          {right}
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
    <header className="mb-8 flex flex-wrap items-end justify-between gap-4 fade-up">
      <div>
        <h1 className="text-[28px] font-bold tracking-tight leading-tight">{title}</h1>
        {subtitle && <p className="mt-1 text-sm text-ink-2">{subtitle}</p>}
      </div>
      {actions && <div className="flex items-center gap-2">{actions}</div>}
    </header>
  )
}

export function StatTile({ icon, label, value, unit, sub }: {
  icon?: ReactNode; label: string; value: ReactNode; unit?: string; sub?: ReactNode
}) {
  return (
    <div className="card p-5 fade-up">
      <div className="flex items-center gap-1.5 text-xs font-medium text-ink-2">
        {icon}{label}
      </div>
      <div className="num mt-2 text-3xl font-semibold tracking-tight">
        {value}
        {unit && <span className="ml-1 text-base font-normal text-ink-3">{unit}</span>}
      </div>
      {sub && <div className="mt-1 text-xs text-ink-3">{sub}</div>}
    </div>
  )
}

export function EmptyState({ icon, title, hint }: { icon?: ReactNode; title: string; hint?: string }) {
  return (
    <div className="card flex flex-col items-center justify-center px-6 py-16 text-center fade-up">
      <div className="flex h-14 w-14 items-center justify-center rounded-full bg-accent/10 text-accent">
        {icon}
      </div>
      <p className="mt-4 text-[15px] font-semibold">{title}</p>
      {hint && <p className="mt-1 max-w-sm text-sm text-ink-2">{hint}</p>}
    </div>
  )
}

export function Segmented<T extends string>({ options, value, onChange }: {
  options: { value: T; label: string; icon?: ReactNode }[]
  value: T
  onChange: (v: T) => void
}) {
  return (
    <div className="inline-flex rounded-full bg-ink-3/10 p-0.5">
      {options.map((o) => (
        <button
          key={o.value}
          type="button"
          onClick={() => onChange(o.value)}
          className={cn(
            'inline-flex items-center gap-1 rounded-full px-3 py-1 text-xs font-medium transition-all',
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

/** 键值行（详情页/系统页的定义列表项） */
export function KeyValue({ k, v, mono }: { k: ReactNode; v: ReactNode; mono?: boolean }) {
  return (
    <div className="kv">
      <dt>{k}</dt>
      <dd className={cn('min-w-0 truncate', mono && 'num font-mono text-xs text-ink-2')}>{v}</dd>
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


/** 通用按钮：variant 映射 .btn-* 基类；尺寸 lg 用于认证页主操作 */
export function Button({ variant = 'primary', lg, className, ...rest }: {
  variant?: 'primary' | 'ghost'
  lg?: boolean
} & ButtonHTMLAttributes<HTMLButtonElement>) {
  return (
    <button
      className={cn('btn', variant === 'primary' ? 'btn-primary' : 'btn-ghost', lg && 'btn-lg', className)}
      {...rest}
    />
  )
}

/** 带标签/提示/错误的表单输入行（error 优先于 hint 展示） */
export function TextField({ label, hint, error, className, ...rest }: {
  label: string
  hint?: string
  error?: string
} & InputHTMLAttributes<HTMLInputElement>) {
  const id = useId()
  const desc = error || hint ? `${id}-desc` : undefined
  return (
    <div className={className}>
      <label htmlFor={id} className="mb-1.5 block text-[13px] font-medium text-ink-2">{label}</label>
      <input
        id={id}
        aria-invalid={error ? true : undefined}
        aria-describedby={desc}
        className={cn('input', error && 'input-error')}
        {...rest}
      />
      {desc && (
        <p id={desc} className={cn('mt-1.5 text-xs', error ? 'text-bad' : 'text-ink-3')}>
          {error ?? hint}
        </p>
      )}
    </div>
  )
}

/** 主题快速切换：供 Login/Setup 等脱离 Layout 侧栏的独立页使用 */
export function ThemeToggle({ className }: { className?: string }) {
  const [dark, setDark] = useState(() => document.documentElement.classList.contains('dark'))
  const label = dark ? '切换为浅色外观' : '切换为深色外观'
  return (
    <button
      type="button"
      title={label}
      aria-label={label}
      onClick={() => {
        const next: ThemeMode = dark ? 'light' : 'dark'
        setTheme(next)
        setDark(!dark)
      }}
      className={cn(
        'flex h-8 w-8 items-center justify-center rounded-full border border-hairline',
        'bg-surface/70 text-ink-2 transition-colors hover:text-ink',
        className,
      )}
    >
      {dark ? <Sun size={15} strokeWidth={2} /> : <Moon size={15} strokeWidth={2} />}
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
          <span className="flex h-14 w-14 items-center justify-center rounded-[16px] bg-accent/10 text-accent">
            <Logo size={30} />
          </span>
          <h1 className="mt-4 text-[24px] font-bold leading-tight tracking-tight">{title}</h1>
          {subtitle && <p className="mt-1.5 text-sm text-ink-2">{subtitle}</p>}
        </div>
        <div className="card p-6">{children}</div>
        {footer && <div className="mt-5 text-center text-xs text-ink-3">{footer}</div>}
      </div>
    </div>
  )
}
