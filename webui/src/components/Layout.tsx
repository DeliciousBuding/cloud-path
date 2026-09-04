import { useState } from 'react'
import { NavLink, Outlet, useNavigate } from 'react-router'
import { useQuery } from '@tanstack/react-query'
import {
  LayoutDashboard, Cpu, Activity, LogOut, Network, Settings, Monitor, Puzzle, Pill, ShieldCheck, Sun, Moon,
  UserRound, WifiOff,
} from 'lucide-react'
import { Logo } from './Logo'
import { StatusDot } from './ui'
import { ToastViewport } from './Toast'
import { api } from '@/lib/api'
import { cn } from '@/lib/cn'
import { useLive } from '@/store/ws'
import { getTheme, setTheme, type ThemeMode } from '@/lib/theme'
import { roleLabel } from '@/lib/format'
import { logout, useAuth, useIsAdmin } from '@/store/auth'

/**
 * 产品级信息架构（固定顺序）：
 *   Overview / Edges / Devices / Plugins / Activity / Administration / Settings
 * Administration 排在 Settings 之前，且只对 admin 出现（docs/api.md §3.1）：
 * 入口本身就是敏感信息，非 admin 连链接都不给（Admin 页自身另有门禁与空态）。
 */
const NAV = [
  { to: '/', label: '概览', icon: LayoutDashboard, end: true },
  { to: '/edges', label: '边缘节点', icon: Network, end: false },
  { to: '/devices', label: '设备', icon: Cpu, end: false },
  { to: '/pillbox', label: '药盒控制', icon: Pill, end: false },
  { to: '/plugins', label: '插件', icon: Puzzle, end: false },
  { to: '/activity', label: '活动', icon: Activity, end: false },
]

const ADMIN_NAV = { to: '/admin', label: '管理', icon: ShieldCheck, end: false }

const TAIL_NAV = [{ to: '/settings', label: '系统', icon: Settings, end: false }]

function navCls(active: boolean): string {
  return cn(
    'flex items-center gap-2.5 rounded-xl px-3 py-2 text-[13px] font-medium transition-colors',
    active ? 'bg-accent/10 text-accent' : 'text-ink-2 hover:bg-ink-3/8 hover:text-ink',
  )
}

function ThemeControl() {
  const [mode, setMode] = useState<ThemeMode>(getTheme())
  const opts: { value: ThemeMode; icon: typeof Sun; title: string }[] = [
    { value: 'light', icon: Sun, title: '浅色外观' },
    { value: 'dark', icon: Moon, title: '深色外观' },
    { value: 'system', icon: Monitor, title: '跟随系统' },
  ]
  return (
    <div className="flex rounded-full bg-ink-3/10 p-0.5" role="group" aria-label="外观主题">
      {opts.map(({ value, icon: Icon, title }) => (
        <button
          key={value}
          type="button"
          title={title}
          aria-label={title}
          aria-pressed={mode === value}
          onClick={() => { setMode(value); setTheme(value) }}
          className={cn(
            'flex h-6 w-7 items-center justify-center rounded-full transition-all',
            mode === value ? 'bg-surface text-ink shadow-sm' : 'text-ink-3 hover:text-ink',
          )}
        >
          <Icon size={13} strokeWidth={2} />
        </button>
      ))}
    </div>
  )
}

function ConnPill() {
  const status = useLive((s) => s.status)
  const text = status === 'open' ? '已连接' : status === 'connecting' ? '连接中' : '已断开'
  return (
    <span className="flex items-center gap-1.5 text-[11px] text-ink-2" title={`实时通道：${text}`}>
      <StatusDot online={status === 'open'} />
      {text}
    </span>
  )
}

function Brand() {
  return (
    <NavLink to="/" className="flex items-center gap-2.5 px-1 text-accent" aria-label="Cloudpath 概览">
      <Logo size={26} />
      <span className="leading-tight">
        <span className="block text-[15px] font-semibold tracking-tight text-ink">Cloudpath</span>
        <span className="block text-[11px] text-ink-3">云径 · 设备接入平台</span>
      </span>
    </NavLink>
  )
}

/** 当前登录账号 + 登出（只在账号模式已登录时出现；开放访问/未登录不渲染） */
function AccountPill() {
  const status = useAuth((s) => s.status)
  const user = useAuth((s) => s.user)
  const navigate = useNavigate()
  const [busy, setBusy] = useState(false)
  if (status !== 'in' || !user) return null

  async function signOut() {
    setBusy(true)
    await logout()
    navigate('/login', { replace: true })
  }

  return (
    <div className="flex min-w-0 items-center gap-2">
      <span className="flex h-6 w-6 shrink-0 items-center justify-center rounded-full bg-accent/10 text-accent">
        <UserRound size={12} />
      </span>
      <span className="min-w-0 flex-1">
        <span className="block truncate text-[12px] font-medium" title={user.name || user.username}>
          {user.name || user.username}
        </span>
        <span className="block truncate text-[10px] text-ink-3" title={`${user.username} · ${roleLabel(user.role)}`}>
          {roleLabel(user.role)}
        </span>
      </span>
      <button
        type="button" onClick={() => void signOut()} disabled={busy}
        aria-label="登出" title="登出当前账号"
        className="flex h-7 w-7 shrink-0 items-center justify-center rounded-full text-ink-3 transition-colors hover:text-bad disabled:opacity-50"
      >
        <LogOut size={13} />
      </button>
    </div>
  )
}

function SidebarFooter() {
  const { data } = useQuery({ queryKey: ['health-sidebar'], queryFn: api.health, refetchInterval: 30000 })
  return (
    <div className="mt-auto space-y-3 border-t border-hairline px-3 pt-3 pb-1">
      <AccountPill />
      <div className="flex items-center justify-between">
        <ConnPill />
        <ThemeControl />
      </div>
      {data && <p className="num text-[10px] text-ink-3">server {data.version}</p>}
    </div>
  )
}

/** 实时通道断开时的系统级提示条（重连由 store 自动进行）。
 *  连续失败要如实说出来：账号模式下 /ws 靠会话 cookie 鉴权，会话失效时页面若照常渲染
 *  就会变成「看着正常但没有实时数据」的假数据，因此这里给出失败次数并说明正在复核登录态。
 *  层叠位置：桌面侧栏是 fixed z-40 w-60，横幅若全宽 sticky 会被侧栏盖住（且旧版
 *  lg:pl-64 与侧栏 w-60 不等宽，视觉错位）；故横幅排在侧栏/移动顶栏之后的文档流里，
 *  桌面用 lg:ml-60 让出侧栏宽度、lg:sticky 吸顶，移动端随内容流不吸顶。 */
function OfflineBanner() {
  const status = useLive((s) => s.status)
  const failures = useLive((s) => s.failures)
  if (status === 'open') return null
  return (
    <div className="banner z-30 lg:sticky lg:top-0 lg:ml-60" role="status">
      <WifiOff size={13} className="shrink-0" />
      <span className="min-w-0 break-words">
        {status === 'connecting' ? '正在连接实时通道…' : '实时通道已断开，正在自动重连（页面数据仍会定时刷新）'}
      </span>
      {failures >= 3 && (
        <span className="num ml-auto shrink-0">
          已连续失败 {failures} 次{failures >= 5 ? ' · 正在复核登录态' : ''}
        </span>
      )}
    </div>
  )
}

export default function Layout() {
  const nav = useIsAdmin() ? [...NAV, ADMIN_NAV, ...TAIL_NAV] : [...NAV, ...TAIL_NAV]
  return (
    <div className="min-h-screen">
      <a href="#main"
        className="sr-only focus:not-sr-only focus:absolute focus:left-4 focus:top-3 focus:z-50 focus:rounded-full focus:bg-accent focus:px-4 focus:py-2 focus:text-xs focus:text-accent-ink">
        跳到主内容
      </a>

      {/* 桌面侧栏 */}
      <aside className="glass fixed inset-y-0 left-0 z-40 hidden w-60 flex-col border-r border-hairline px-3 py-5 lg:flex">
        <div className="px-1">
          <Brand />
        </div>
        <nav className="mt-7 space-y-0.5" aria-label="主导航">
          {nav.map(({ to, label, icon: Icon, end }) => (
            <NavLink key={to} to={to} end={end} title={label} className={({ isActive }) => navCls(isActive)}>
              <Icon size={16} strokeWidth={1.9} />
              {label}
            </NavLink>
          ))}
        </nav>
        <SidebarFooter />
      </aside>

      {/* 移动端顶栏 */}
      <header className="glass sticky top-0 z-40 border-b border-hairline px-4 py-3 lg:hidden">
        <div className="flex items-center justify-between">
          <Brand />
          <ConnPill />
        </div>
        <nav className="-mx-1 mt-3 flex gap-1 overflow-x-auto px-1 pb-0.5" aria-label="主导航">
          {nav.map(({ to, label, icon: Icon, end }) => (
            <NavLink key={to} to={to} end={end} title={label} className={({ isActive }) => navCls(isActive)}>
              <Icon size={15} strokeWidth={1.9} />
              <span className="whitespace-nowrap">{label}</span>
            </NavLink>
          ))}
        </nav>
      </header>

      <OfflineBanner />

      <main id="main" className="lg:pl-60">
        <div className="mx-auto max-w-[1360px] px-4 py-7 sm:px-6 lg:px-10 lg:py-9">
          <Outlet />
        </div>
      </main>
      <ToastViewport />
    </div>
  )
}
