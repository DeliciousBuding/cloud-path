// 基础 UI 原语（Apple 极简）：Badge / StatusDot / Panel / PageHeader / StatTile / EmptyState /
// Segmented / KeyValue / Spinner / Button / TextField / ThemeToggle / AuthCard
// 颜色一律走 index.css token（Tailwind 主题类或 .btn/.input/.card 基类），组件内禁止裸色值。
import { useId, useState } from 'react'
import type { ButtonHTMLAttributes, InputHTMLAttributes, ReactNode } from 'react'
import { ArrowLeft, Monitor, Moon, RefreshCw, Sun } from 'lucide-react'
import { Link } from 'react-router'
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
      className={cn('inline-block h-2 w-2 shrink-0 rounded-full',
        online ? 'bg-ok' : 'bg-idle/50', className)}
    />
  )
}

export function Panel({ title, right, className, children }: {
  title?: ReactNode; right?: ReactNode; className?: string; children: ReactNode
}) {
  return (
    <section className={cn('card p-4', className)}>
      {(title || right) && (
        <div className="mb-4 flex items-center justify-between gap-3">
          <h2 className="text-[15px] font-semibold tracking-[-0.01em]">{title}</h2>
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
        {/* -0.025em 档负字距是拉丁刻度；中文标题字面全角，超过 -0.01em 会挤，故用 CJK 安全值 */}
        <h1 className="text-[28px] font-semibold tracking-[-0.01em] leading-tight">{title}</h1>
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
    // 390px：两列统计瓦片内宽仅约 8rem，长版本号等不可断字符串必须换行，否则撑出横向滚动
    <div className="card min-w-0 p-4 fade-up">
      <div className="flex min-w-0 items-center gap-1.5 text-xs font-medium text-ink-2">
        {icon}<span className="truncate">{label}</span>
      </div>
      <div className="metric num mt-1.5 text-[30px] font-semibold leading-none break-words">
        {value}
        {unit && <span className="ml-1 text-base font-normal text-ink-3">{unit}</span>}
      </div>
      {sub && <div className="mt-0.5 text-xs text-ink-3 break-words">{sub}</div>}
    </div>
  )
}

export function EmptyState({ icon, title, hint }: { icon?: ReactNode; title: string; hint?: string }) {
  return (
    <div className="card flex flex-col items-center justify-center px-6 py-16 text-center fade-up">
      <span className="text-ink-3">{icon}</span>
      <p className="mt-3 text-[15px] font-semibold">{title}</p>
      {hint && <p className="mt-1 max-w-sm text-sm text-ink-2">{hint}</p>}
    </div>
  )
}

/**
 * 错误态：说清「拿不到什么」+ 一键重试。
 * 不复述服务端技术细节，也不把错误渲染成空白（空白 = 用户以为没数据）。
 */
export function ErrorState({ icon, title, hint, onRetry, retrying, compact }: {
  icon?: ReactNode; title: string; hint?: ReactNode
  onRetry?: () => void; retrying?: boolean; compact?: boolean
}) {
  return (
    <div
      role="alert"
      className={cn('card flex flex-col items-center justify-center px-6 text-center fade-up',
        compact ? 'py-9' : 'py-14')}
    >
      <span className="text-bad">{icon ?? <RefreshCw size={22} />}</span>
      <p className="mt-3 text-[15px] font-semibold">{title}</p>
      {hint && <p className="mt-1 max-w-md text-sm break-words text-ink-2">{hint}</p>}
      {onRetry && (
        <button type="button" className="btn btn-ghost mt-5" onClick={onRetry} disabled={retrying}>
          {retrying ? <Spinner size={13} /> : <RefreshCw size={13} />} 重试
        </button>
      )}
    </div>
  )
}

/** 页内分区标题（比 Panel title 更轻，用于把一组卡片归到一个语义段落下） */
export function SectionTitle({ icon, children, right }: {
  icon?: ReactNode; children: ReactNode; right?: ReactNode
}) {
  return (
    <div className="mb-3 flex min-w-0 items-center justify-between gap-3 px-1">
      <h2 className="flex min-w-0 items-center gap-1.5 text-[15px] font-semibold tracking-[-0.01em]">
        {icon}<span className="truncate">{children}</span>
      </h2>
      {right}
    </div>
  )
}

export function Segmented<T extends string>({ options, value, onChange, label = '视图切换' }: {
  options: { value: T; label: string; icon?: ReactNode }[]
  value: T
  onChange: (v: T) => void
  /** 分组可读名称（读屏用户需要知道这组按钮在切换什么） */
  label?: string
}) {
  return (
    <div className="inline-flex max-w-full rounded-full bg-ink-3/10 p-0.5" role="group" aria-label={label}>
      {options.map((o) => (
        <button
          key={o.value}
          type="button"
          aria-pressed={value === o.value}
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

export function TabBar<T extends string>({ items, value, onChange, label = '分页切换' }: {
  items: TabItem<T>[]
  value: T
  onChange: (v: T) => void
  label?: string
}) {
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
        role="tablist" aria-label={label}
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
                '-mb-px inline-flex items-center gap-1.5 border-b-2 px-0.5 pb-2 text-[13px] font-medium whitespace-nowrap transition-colors',
                selected ? 'border-ink text-ink' : 'border-transparent text-ink-3 hover:text-ink-2',
              )}
            >
              {it.icon}
              {it.label}
              {typeof it.count === 'number' && (
                <span className="num text-[12px] text-ink-3">
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
      <label htmlFor={id} className="mb-1.5 block text-[13px] font-medium text-ink-2">{label}</label>
      <div className="relative">
      <input
        id={id}
        aria-invalid={error ? true : undefined}
        aria-describedby={desc}
        className={cn('input', error ? 'input-error' : undefined, suffix ? 'pr-11' : undefined)}
        {...rest}
      />
      {suffix && <div className="absolute inset-y-0 right-1.5 flex items-center">{suffix}</div>}
      </div>
      {desc && (
        <p id={desc} className={cn('mt-1.5 text-xs', error ? 'text-bad' : 'text-ink-3')}>
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
  const [mode, setMode] = useState<ThemeMode>(() => getTheme())
  const META: Record<ThemeMode, { icon: typeof Sun; label: string }> = {
    light: { icon: Sun, label: '浅色外观' },
    dark: { icon: Moon, label: '深色外观' },
    system: { icon: Monitor, label: '跟随系统' },
  }
  const next: ThemeMode = mode === 'light' ? 'dark' : mode === 'dark' ? 'system' : 'light'
  const Cur = META[mode].icon
  return (
    <button
      type="button"
      title={`当前：${META[mode].label} · 点击切换为${META[next].label}`}
      aria-label={`切换为${META[next].label}`}
      onClick={() => { setTheme(next); setMode(next) }}
      className={cn(
        'flex h-8 w-8 items-center justify-center rounded-full border border-hairline',
        'bg-surface/70 text-ink-2 transition-colors hover:text-ink',
        className,
      )}
    >
      <Cur size={15} strokeWidth={2} />
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
          <span className="flex h-14 w-14 items-center justify-center rounded-xl bg-accent/10 text-accent">
            <Logo size={30} />
          </span>
          <h1 className="mt-4 text-[26px] font-semibold leading-tight tracking-[-0.01em]">{title}</h1>
          {subtitle && <p className="mt-1.5 text-sm text-ink-2">{subtitle}</p>}
        </div>
        <div className="card p-6">{children}</div>
        {footer && <div className="mt-5 text-center text-xs text-ink-3">{footer}</div>}
      </div>
    </div>
  )
}

/** 详情页统一返回链（fade-in + hover 强调色）；to/label 由调用页声明 */
export function BackLink({ to, label }: { to: string; label: string }) {
  return (
    <Link to={to}
      className="mb-5 inline-flex items-center gap-1 text-sm text-ink-2 transition-colors hover:text-accent fade-up">
      <ArrowLeft size={15} /> {label}
    </Link>
  )
}
