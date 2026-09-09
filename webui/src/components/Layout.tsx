import { useEffect, useMemo, useRef, useState } from 'react'
import { NavLink, Outlet, useLocation, useNavigate } from 'react-router'
import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import {
  LayoutDashboard, Cpu, Activity, LogOut, Network, Settings, Monitor, Puzzle, ShieldCheck, Sun, Moon,
  ChevronDown, UserRound, WifiOff, Boxes, Bell, Music, Thermometer, Megaphone, Pill, AppWindow,
} from 'lucide-react'
import type { LucideIcon } from 'lucide-react'
import { Logo } from './Logo'
import { LocaleSwitcher } from './LocaleSwitcher'
import { StatusDot } from './ui'
import { ToastViewport } from './Toast'
import { api } from '@/lib/api'
import { cn } from '@/lib/cn'
import { useLive } from '@/store/ws'
import { getTheme, setTheme, type ThemeMode } from '@/lib/theme'
import { roleLabel } from '@/lib/format'
import { logout, useAuth, useIsAdmin } from '@/store/auth'
import { usePluginCatalog, usePluginInstances } from '@/hooks/usePlugins'
import { applicationUIReadable, buildApplicationNavigation } from '@/lib/plugin-ui'
import { resolveLocalizedText } from '@/i18n/pluginText'

/**
 * 产品级信息架构（固定顺序）：
 *   Overview / Edges / Devices / Plugins / Activity / Administration / Settings
 * Administration 排在 Settings 之前，且只对 admin 出现（docs/api.md §3.1）：
 * 入口本身就是敏感信息，非 admin 连链接都不给（Admin 页自身另有门禁与空态）。
 */
const CORE_NAV = [
  { to: '/', labelKey: 'overview', icon: LayoutDashboard, end: true },
  { to: '/devices', labelKey: 'devices', icon: Cpu, end: false },
  { to: '/edges', labelKey: 'edges', icon: Network, end: false },
  { to: '/activity', labelKey: 'activity', icon: Activity, end: false },
]

const APPS_NAV = [{ to: '/plugins', labelKey: 'plugins', icon: Puzzle, end: false }]

const ADMIN_NAV = { to: '/admin', labelKey: 'admin', icon: ShieldCheck, end: false }

const TAIL_NAV = [{ to: '/settings', labelKey: 'settings', icon: Settings, end: false }]

const APP_ICONS: Record<string, LucideIcon> = {
  pill: Pill, pillbox: Pill, bell: Bell, music: Music, thermometer: Thermometer,
  megaphone: Megaphone, 'service-desk': Megaphone, environment: Thermometer,
  box: Boxes, application: AppWindow,
}

function appIcon(name: string | undefined): LucideIcon {
  return name ? APP_ICONS[name] ?? Puzzle : Puzzle
}

function navCls(active: boolean): string {
  return cn(
    'flex items-center gap-2.5 rounded-tile px-3 py-2 text-body font-medium transition-colors',
    active ? 'bg-accent/10 text-accent' : 'text-ink-2 hover:bg-ink-3/8 hover:text-ink',
  )
}

function ThemeControl() {
  const { t } = useTranslation('common')
  const [mode, setMode] = useState<ThemeMode>(getTheme())
  const opts: { value: ThemeMode; icon: typeof Sun; labelKey: string }[] = [
    { value: 'light', icon: Sun, labelKey: 'theme.light' },
    { value: 'dark', icon: Moon, labelKey: 'theme.dark' },
    { value: 'system', icon: Monitor, labelKey: 'theme.system' },
  ]
  return (
    <div className="flex rounded-pill bg-ink-3/10 p-0.5" role="group" aria-label={t('theme.label')}>
      {opts.map(({ value, icon: Icon, labelKey }) => (
        <button
          key={value}
          type="button"
          title={t(labelKey)}
          aria-label={t(labelKey)}
          aria-pressed={mode === value}
          onClick={() => { setMode(value); setTheme(value) }}
          className={cn(
            'flex h-touch w-touch items-center justify-center rounded-pill transition-colors sm:h-6 sm:w-7',
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
  const { t } = useTranslation('common')
  const status = useLive((s) => s.status)
  const text = status === 'open' ? t('status.connected') : status === 'connecting' ? t('status.connecting') : t('status.disconnected')
  return (
    <span className="flex items-center gap-1.5 text-meta text-ink-2" title={t('layout.connectionTitle', { status: text })}>
      <StatusDot online={status === 'open'} />
      {text}
    </span>
  )
}

function Brand() {
  const { t } = useTranslation('common')
  return (
    <NavLink to="/" className="flex min-h-touch items-center gap-2.5 px-1 text-accent sm:min-h-0" aria-label={t('app.overviewAria')}>
      <Logo size={26} />
      <span className="leading-tight">
        <span className="block text-lead font-semibold tracking-[-0.01em] text-ink">CloudPath</span>
        <span className="hidden text-meta text-ink-3 sm:block">{t('app.tagline')}</span>
      </span>
    </NavLink>
  )
}

/** 当前登录账号 + 登出（只在账号模式已登录时出现；开放访问/未登录不渲染） */
function AccountPill() {
  const { t } = useTranslation('common')
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
      <span className="flex h-6 w-6 shrink-0 items-center justify-center rounded-pill bg-accent/10 text-accent">
        <UserRound size={12} />
      </span>
      <span className="min-w-0 flex-1">
        <span className="block truncate text-meta font-medium" title={user.name || user.username}>
          {user.name || user.username}
        </span>
        <span className="block truncate text-meta text-ink-3" title={`${user.username} · ${roleLabel(user.role)}`}>
          {roleLabel(user.role)}
        </span>
      </span>
      <button
        type="button" onClick={() => void signOut()} disabled={busy}
        aria-label={t('actions.logout')} title={t('actions.logoutTitle')}
        className="flex h-7 w-7 shrink-0 items-center justify-center rounded-pill text-ink-3 transition-colors hover:text-bad disabled:opacity-disabled"
      >
        <LogOut size={13} />
      </button>
    </div>
  )
}

function SidebarFooter() {
  const { t } = useTranslation('common')
  const { data } = useQuery({ queryKey: ['health-sidebar'], queryFn: api.health, refetchInterval: 30000 })
  return (
    <div className="mt-auto space-y-3 border-t border-hairline px-3 pt-3 pb-1">
      <AccountPill />
      <div className="flex items-center justify-between gap-2">
        <ConnPill />
        <ThemeControl />
      </div>
      <LocaleSwitcher />
      {data && <p className="num font-mono text-micro text-ink-3">{t('layout.version', { version: data.version })}</p>}
    </div>
  )
}

/** 实时通道断开时的系统级提示条（重连由 store 自动进行）。
 *  连续失败要如实说出来：账号模式下 /ws 靠会话 cookie 鉴权，会话失效时页面若照常渲染
 *  就会变成「看着正常但没有实时数据」的假数据，因此这里给出失败次数并说明正在重新检查登录状态。
 *  层叠位置：桌面侧栏是 fixed z-nav w-60，横幅若全宽 sticky 会被侧栏盖住（且旧版
 *  lg:pl-64 与侧栏 w-60 不等宽，视觉错位）；故横幅排在侧栏/移动顶栏之后的文档流里，
 *  桌面用 lg:ml-60 让出侧栏宽度、lg:sticky 吸顶，移动端随内容流不吸顶。 */
function OfflineBanner() {
  const { t } = useTranslation('common')
  const status = useLive((s) => s.status)
  const failures = useLive((s) => s.failures)
  if (status === 'open') return null
  return (
    <div className="banner z-sticky lg:sticky lg:top-0 lg:ml-60" role="status">
      <WifiOff size={13} className="shrink-0" />
      <span className="min-w-0 break-words">
        {status === 'connecting' ? t('layout.offlineConnecting') : t('layout.offlineDisconnected')}
      </span>
      {failures >= 3 && (
        <span className="num ml-auto shrink-0">
          {t('layout.offlineFailures', { count: failures })}{failures >= 5 ? t('layout.offlineRechecking') : ''}
        </span>
      )}
    </div>
  )
}

export default function Layout() {
  const location = useLocation()
  const isAdmin = useIsAdmin()
  const authStatus = useAuth((state) => state.status)
  const user = useAuth((state) => state.user)
  const { t, i18n } = useTranslation('common')
  const { t: tNav } = useTranslation('nav')
  const locale = i18n.resolvedLanguage ?? i18n.language
  const [moreOpen, setMoreOpen] = useState(false)
  const moreRef = useRef<HTMLDivElement>(null)
  const { plugins } = usePluginCatalog()
  const { instances } = usePluginInstances()
  const readable = applicationUIReadable(user, authStatus)
  const coreNav = useMemo(() => CORE_NAV.map((item) => ({ ...item, label: tNav(item.labelKey) })), [tNav])
  const appNav = useMemo(() => buildApplicationNavigation(plugins, instances, readable).map((item) => ({
    to: item.to,
    label: resolveLocalizedText(item.navigation, 'title', locale) ?? item.label,
    icon: appIcon(item.icon),
    end: false,
  })), [instances, locale, plugins, readable])
  const appsNav = useMemo(() => APPS_NAV.map((item) => ({ ...item, label: tNav(item.labelKey) })), [tNav])
  const adminNav = useMemo(() => ({ ...ADMIN_NAV, label: tNav(ADMIN_NAV.labelKey) }), [tNav])
  const tailNav = useMemo(() => TAIL_NAV.map((item) => ({ ...item, label: tNav(item.labelKey) })), [tNav])
  const moreNav = isAdmin ? [...appNav, ...appsNav, adminNav, ...tailNav] : [...appNav, ...appsNav, ...tailNav]
  const nav = [...coreNav, ...moreNav]
  const moreActive = moreNav.some(({ to }) => location.pathname === to || location.pathname.startsWith(`${to}/`))

  useEffect(() => {
    setMoreOpen(false)
  }, [location.pathname])

  useEffect(() => {
    if (!moreOpen) return
    const closeOutside = (event: MouseEvent) => {
      if (!moreRef.current?.contains(event.target as Node)) setMoreOpen(false)
    }
    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key === 'Escape') setMoreOpen(false)
    }
    document.addEventListener('mousedown', closeOutside)
    document.addEventListener('keydown', closeOnEscape)
    return () => {
      document.removeEventListener('mousedown', closeOutside)
      document.removeEventListener('keydown', closeOnEscape)
    }
  }, [moreOpen])

  return (
    <div className="min-h-screen">
      <a href="#main"
        className="sr-only focus:not-sr-only focus:absolute focus:left-4 focus:top-3 focus:z-overlay focus:rounded-pill focus:bg-accent focus:px-4 focus:py-2 focus:text-meta focus:text-accent-ink">
        {t('layout.skipToContent')}
      </a>

      {/* 桌面侧栏 */}
      <aside className="bg-surface fixed inset-y-0 left-0 z-nav hidden w-60 flex-col border-r border-hairline px-3 py-5 lg:flex">
        <div className="px-1">
          <Brand />
        </div>
        <nav className="mt-7 space-y-0.5" aria-label={t('layout.mainNavigation')}>
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
      <header className="bg-surface sticky top-0 z-nav border-b border-hairline px-4 py-2.5 lg:hidden">
        <div className="flex items-center justify-between gap-3">
          <Brand />
          <div className="flex items-center gap-2">
            <ConnPill />
            <div ref={moreRef} className="relative">
              <button
                type="button"
                aria-label={t('layout.moreNavAria')}
                aria-expanded={moreOpen}
                aria-controls="mobile-more-menu"
                onClick={() => setMoreOpen((open) => !open)}
                className={cn(
                  'btn btn-ghost min-h-touch',
                  (moreOpen || moreActive) && 'border-accent/30 bg-accent/8 text-accent',
                )}
              >
                {t('layout.more')} <ChevronDown size={14} className={cn('transition-transform', moreOpen && 'rotate-180')} />
              </button>
              {moreOpen && <div id="mobile-more-menu" className="absolute right-0 z-overlay mt-2 w-[min(18rem,calc(100vw-2rem))] rounded-card border border-hairline bg-surface p-3 shadow-lift">
                <p className="px-2 pb-1 text-micro font-medium text-ink-3">{t('layout.morePages')}</p>
                <AccountPill />
                {moreNav.length > 0 && (
                  <nav className="mt-2 border-t border-hairline pt-3" aria-label={t('layout.moreNavigation')}>
                    <div className="space-y-0.5">
                      {moreNav.map(({ to, label, icon: Icon, end }) => (
                        <NavLink key={to} to={to} end={end} title={label} onClick={() => setMoreOpen(false)}
                          className={({ isActive }) => navCls(isActive)}>
                          <Icon size={15} strokeWidth={1.9} />
                          {label}
                        </NavLink>
                      ))}
                    </div>
                  </nav>
                )}
                <div className="mt-3 flex items-center justify-between border-t border-hairline pt-3">
                  <span className="text-meta text-ink-2">{t('layout.appearance')}</span>
                  <ThemeControl />
                </div>
                <LocaleSwitcher className="mt-3 border-t border-hairline pt-3" />
              </div>}
            </div>
          </div>
        </div>
        <nav className="mt-2 grid grid-cols-4 gap-1" aria-label={t('layout.mainNavigation')}>
          {coreNav.map(({ to, label, icon: Icon, end }) => (
            <NavLink key={to} to={to} end={end} title={label}
              className={({ isActive }) => cn(
                'flex min-h-touch min-w-0 flex-col items-center justify-center gap-0.5 rounded-tile px-1 py-1 text-micro font-medium transition-colors',
                isActive ? 'bg-accent/10 text-accent' : 'text-ink-2 hover:bg-ink-3/8 hover:text-ink',
              )}>
              <Icon size={15} strokeWidth={1.9} />
              <span className="max-w-full truncate">{label}</span>
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
