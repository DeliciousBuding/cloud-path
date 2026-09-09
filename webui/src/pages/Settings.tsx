import { useState } from 'react'
import { Link, useNavigate } from 'react-router'
import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import {
  Activity, AlertCircle, Check, ChevronDown, Cpu, Database, KeyRound, LogOut, Monitor, Moon, Network, Plug,
  Server, Sun, UserRound, Wifi,
} from 'lucide-react'
import { Badge, KeyValue, PageHeader, Panel, Segmented, StatTile, TextField } from '@/components/ui'
import { api, getToken, setToken } from '@/lib/api'
import { authModeLabel, cmdMeta, fmtDateTime, fmtUptime, roleLabel } from '@/lib/format'
import { getTheme, setTheme, type ThemeMode } from '@/lib/theme'
import { reconnectLive, useLive } from '@/store/ws'
import { usePageTitle } from '@/hooks/usePageTitle'
import { confirmSession, logout, useAuth } from '@/store/auth'
import { toast } from '@/store/toast'

const UI_ROLES = new Set(['admin', 'operator', 'viewer'])

type TokenErrorKey = 'invalid' | 'verifyFailed' | 'edgeOnly' | 'sessionFailed' | 'network'

function InlineError({ title, hint, onRetry, retrying }: {
  title: string
  hint: string
  onRetry?: () => void
  retrying?: boolean
}) {
  const { t } = useTranslation('settings')
  return (
    <div role="alert" className="rounded-tile bg-bad/10 px-3.5 py-3 text-bad">
      <p className="flex items-start gap-2 text-compact font-semibold">
        <AlertCircle size={14} className="mt-0.5 shrink-0" />
        <span>{title}</span>
      </p>
      <p className="mt-1 text-meta leading-relaxed opacity-90">{hint}</p>
      {onRetry && (
        <button type="button" className="btn btn-ghost mt-2.5" onClick={onRetry} disabled={retrying}>
          {retrying ? t('retry.busy') : t('retry.idle')}
        </button>
      )}
    </div>
  )
}

function roleSummaryKey(role: string): string {
  if (role === 'admin') return 'roles.adminSummary'
  if (role === 'operator') return 'roles.operatorSummary'
  return 'roles.viewerSummary'
}

function metricNumber(value: number | undefined, fallback: string): string {
  return typeof value === 'number' && Number.isFinite(value) ? String(value) : fallback
}

export default function Settings() {
  const { t } = useTranslation('settings')
  usePageTitle(t('page.title'))

  const {
    data: health, isFetching: healthFetching, isPending: healthPending,
    isError: healthError, refetch: refetchHealth,
  } = useQuery({ queryKey: ['health'], queryFn: api.health, refetchInterval: 10000, retry: false })
  const {
    data: stats, isFetching: statsFetching, isPending: statsPending,
    isError: statsError, refetch: refetchStats,
  } = useQuery({ queryKey: ['stats'], queryFn: api.stats, refetchInterval: 15000, retry: false })
  const {
    data: adapters, isFetching: adaptersFetching, isPending: adaptersPending,
    isError: adaptersError, refetch: refetchAdapters,
  } = useQuery({ queryKey: ['adapters'], queryFn: api.adapters, staleTime: 5 * 60_000, retry: false })
  const status = useLive((s) => s.status)
  const authStatus = useAuth((s) => s.status)
  const user = useAuth((s) => s.user)
  const isAdmin = user?.role === 'admin'
  const navigate = useNavigate()
  const [tok, setTok] = useState(getToken)
  const [hasStoredToken, setHasStoredToken] = useState(Boolean(getToken()))
  const [saved, setSaved] = useState(false)
  const [tokenSaving, setTokenSaving] = useState(false)
  const [tokenError, setTokenError] = useState<TokenErrorKey | ''>('')
  const [signingOut, setSigningOut] = useState(false)
  const [diagnosticsOpen, setDiagnosticsOpen] = useState(false)
  const [themeMode, setThemeMode] = useState<ThemeMode>(getTheme)

  async function signOut() {
    setSigningOut(true)
    await logout()
    navigate('/login', { replace: true })
  }

  const saveToken = async () => {
    const next = tok.trim()
    const previous = getToken()
    if (!next) {
      setToken('')
      setTok('')
      setHasStoredToken(false)
      setTokenError('')
      reconnectLive()
      toast.ok(previous ? t('token.toast.cleared') : t('token.toast.nothingStored'))
      return
    }

    setTokenSaving(true)
    setTokenError('')
    try {
      // 只带候选令牌、明确省略 cookie：避免已登录会话把无效令牌“验证成成功”。
      const res = await fetch('/api/auth/me', {
        headers: { Authorization: `Bearer ${next}` },
        credentials: 'omit',
      })
      if (!res.ok) {
        setToken(previous)
        setTok(previous)
        setHasStoredToken(Boolean(previous))
        setTokenError(res.status === 401 || res.status === 403 ? 'invalid' : 'verifyFailed')
        return
      }
      const verified = await res.json().catch(() => null) as { user?: { role?: string } } | null
      if (!verified?.user || !UI_ROLES.has(verified.user.role ?? '')) {
        setToken(previous)
        setTok(previous)
        setHasStoredToken(Boolean(previous))
        setTokenError('edgeOnly')
        return
      }
      setToken(next)
      setTok(next)
      setHasStoredToken(true)
      try {
        // 令牌一旦保存，后续 REST 请求会优先带 Bearer；同步刷新身份，
        // 不能继续显示旧的开放访问/账号状态。
        await confirmSession()
      } catch {
        setToken(previous)
        setTok(previous)
        setHasStoredToken(Boolean(previous))
        setTokenError('sessionFailed')
        return
      }
      setSaved(true)
      setTimeout(() => setSaved(false), 1500)
      reconnectLive()
      toast.ok(t('token.toast.verified'), t('token.toast.reconnecting'))
    } catch {
      setToken(previous)
      setTok(previous)
      setHasStoredToken(Boolean(previous))
      setTokenError('network')
    } finally {
      setTokenSaving(false)
    }
  }

  const changeTheme = (mode: ThemeMode) => {
    setTheme(mode)
    setThemeMode(mode)
  }

  const liveLabel = t(status === 'open' ? 'live.status.open' : status === 'connecting' ? 'live.status.connecting' : 'live.status.closed')
  const liveHint = t(status === 'open' ? 'live.hint.open' : status === 'connecting' ? 'live.hint.connecting' : 'live.hint.closed')

  const hasDeviceMetric = typeof health?.devices_online === 'number'
    && Number.isFinite(health.devices_online)
    && typeof health?.devices_total === 'number'
    && Number.isFinite(health.devices_total)

  return (
    <>
      <PageHeader title={t('page.title')} subtitle={t('page.subtitle')} />

      <p className="mb-5 max-w-[62ch] text-body leading-relaxed text-ink-2">
        {authStatus === 'in' ? t('page.introSignedIn') : t('page.introSignedOut')}
      </p>

      <div className="grid items-start gap-5 lg:grid-cols-2">
        <Panel title={<span className="flex items-center gap-1.5"><UserRound size={14} />{t('account.title')}</span>}
          right={authStatus === 'in'
            ? <Badge tone="ok">{t('account.badges.signedIn')}</Badge>
            : authStatus === 'open'
              ? <Badge tone="idle">{t('account.badges.open')}</Badge>
              : <Badge tone="warn">{t('account.badges.signedOut')}</Badge>}>
          {authStatus === 'in' && user ? (
            <>
              <div className="flex min-w-0 items-center gap-3">
                <span className="flex h-10 w-10 shrink-0 items-center justify-center rounded-pill bg-accent/10 text-accent">
                  <UserRound size={18} />
                </span>
                <div className="min-w-0 flex-1">
                  <p className="truncate text-lead font-semibold" title={user.name || user.username}>
                    {user.name || user.username}
                  </p>
                  <p className="num mt-0.5 truncate font-mono text-meta text-ink-3" title={user.username}>
                    {user.username}
                  </p>
                </div>
                <Badge tone={user.role === 'admin' ? 'accent' : user.role === 'operator' ? 'ok' : 'idle'}>
                  {roleLabel(user.role)}
                </Badge>
              </div>

              <p className="mt-4 rounded-tile bg-surface-2 px-3.5 py-3 text-compact leading-relaxed text-ink-2">
                {t(roleSummaryKey(user.role))}
              </p>

              <details className="mt-4 border-t border-hairline pt-3 text-meta text-ink-2">
                <summary className="flex min-h-touch cursor-pointer select-none items-center">{t('account.details')}</summary>
                <dl className="mt-2.5 space-y-2.5">
                  <KeyValue k={t('account.fields.username')} v={<span className="font-mono">{user.username}</span>} />
                  {isAdmin && (
                    <>
                      <KeyValue k={t('account.fields.tenant')} v={user.tenant_slug || t('account.unset')} mono />
                      <KeyValue k={t('account.fields.id')} v={user.id} mono />
                    </>
                  )}
                </dl>
              </details>

              <p className="mt-3 text-meta leading-relaxed text-ink-3">
                {t('account.persisted')}
              </p>
              <div className="mt-3 flex flex-wrap gap-2">
                <button type="button" className="btn btn-danger-ghost" disabled={signingOut}
                  onClick={() => void signOut()}>
                  <LogOut size={13} /> {signingOut ? t('account.signingOut') : t('account.signOut')}
                </button>
                {user.role === 'admin' && (
                  <Link to="/admin" className="btn btn-ghost no-underline">{t('account.manage')}</Link>
                )}
              </div>
            </>
          ) : (
            <p className="text-meta leading-relaxed text-ink-2">
              {authStatus === 'open' ? t('account.open') : t('account.signedOut')}
            </p>
          )}
        </Panel>

        <Panel title={<span className="flex items-center gap-1.5"><Sun size={14} />{t('appearance.title')}</span>}>
          <p className="text-meta leading-relaxed text-ink-2">
            {t('appearance.hint')}
          </p>
          <div className="mt-4">
            <Segmented<ThemeMode>
              label={t('appearance.label')}
              value={themeMode}
              onChange={changeTheme}
              options={[
                { value: 'system', label: t('appearance.system'), icon: <Monitor size={12} /> },
                { value: 'light', label: t('appearance.light'), icon: <Sun size={12} /> },
                { value: 'dark', label: t('appearance.dark'), icon: <Moon size={12} /> },
              ]}
            />
          </div>
        </Panel>

        <Panel title={<span className="flex items-center gap-1.5"><KeyRound size={14} />{t('token.title')}</span>}
          className="lg:col-span-2"
          right={authStatus === 'open' && hasStoredToken ? <Badge tone="ok">{t('token.saved')}</Badge> : undefined}>
          {authStatus === 'in' ? (
            <p className="text-meta leading-relaxed text-ink-2">
              {t('token.signedIn')}
            </p>
          ) : (
            <>
              {hasStoredToken && (
                <p className="mb-3 text-meta leading-relaxed text-ink-2">
                  {t('token.stored')}
                </p>
              )}
              <div className="grid gap-3 lg:grid-cols-[minmax(0,1fr)_auto] lg:items-end">
                <TextField
                  label={t('token.fieldLabel')}
                  type="password"
                  value={tok}
                  onChange={(e) => { setTok(e.target.value); setTokenError('') }}
                  placeholder={t('token.placeholder')}
                  autoComplete="off"
                  error={tokenError ? t(`token.errors.${tokenError}`) : undefined}
                  hint={t('token.hint')}
                />
                <button type="button" className="btn btn-primary lg:mb-[1.625rem]" disabled={tokenSaving} onClick={() => void saveToken()}>
                  {saved && <Check size={14} />}{tokenSaving ? t('token.saving') : saved ? t('token.saved') : t('token.save')}
                </button>
              </div>
            </>
          )}
        </Panel>
      </div>

      <details
        className="mt-6 rounded-card border border-hairline bg-surface p-4"
        onToggle={(e) => setDiagnosticsOpen(e.currentTarget.open)}
      >
        <summary className="flex min-h-touch cursor-pointer select-none flex-wrap items-center gap-x-2 gap-y-1 text-body font-medium text-ink-2">
          <span className="flex items-center gap-1.5"><Activity size={14} />{t('diagnostics.title')}</span>
          <span className="hidden text-meta font-normal text-ink-3 sm:inline">{t('diagnostics.subtitle')}</span>
          <span className="ml-auto flex items-center gap-1 text-meta font-normal text-ink-3">
            {diagnosticsOpen ? t('diagnostics.close') : t('diagnostics.open')}
            <ChevronDown size={14} className={diagnosticsOpen ? 'rotate-180 transition-transform' : 'transition-transform'} />
          </span>
        </summary>
        <p className="mt-1 max-w-[62ch] text-meta leading-relaxed text-ink-3">
          {t('diagnostics.hint')}
        </p>

        {diagnosticsOpen && (
          <div className="mt-5 space-y-6 fade-up">
            {healthError ? (
              <InlineError
                title={t('health.errorTitle')}
                hint={t('health.errorHint')}
                onRetry={() => { void refetchHealth(); reconnectLive() }}
                retrying={healthFetching}
              />
            ) : healthPending && !health ? (
              <p className="py-4 text-center text-body text-ink-3">{t('health.loading')}</p>
            ) : (
              <div className="grid grid-cols-2 gap-4 lg:grid-cols-4">
                <StatTile icon={<Server size={13} />} label={t('health.fields.version')}
                  value={<span className="font-mono font-medium tracking-normal break-words">{health?.version || t('metrics.unavailable')}</span>} />
                <StatTile icon={<Activity size={13} />} label={t('health.fields.uptime')}
                  value={typeof health?.uptime_s === 'number' && Number.isFinite(health.uptime_s) ? fmtUptime(health.uptime_s) : t('metrics.unavailable')} />
                <StatTile icon={<Cpu size={13} />} label={t('health.fields.devices')}
                  value={hasDeviceMetric
                    ? <>{health.devices_online}<span className="text-ink-3">/{health.devices_total}</span></>
                    : t('metrics.unavailable')} />
                <StatTile icon={<Network size={13} />} label={t('health.fields.edges')}
                  value={metricNumber(health?.edges_online, t('metrics.unavailable'))} />
              </div>
            )}

            <div className="grid items-start gap-5 lg:grid-cols-2">
              <Panel title={<span className="flex items-center gap-1.5"><Wifi size={14} />{t('live.title')}</span>}
                right={<Badge tone={status === 'open' ? 'ok' : status === 'connecting' ? 'warn' : 'bad'}>
                  {liveLabel}
                </Badge>}>
                <dl className="space-y-2.5">
                  <KeyValue k={t('live.rows.updateStatus')} v={liveLabel} />
                  <KeyValue k={t('live.rows.platformStatus')}
                    v={healthError ? t('live.platform.unavailable') : health?.ok === true ? t('live.platform.ok') : health?.ok === false ? t('live.platform.check') : t('live.platform.unknown')} />
                  <KeyValue k={t('live.rows.access')}
                    v={statsError ? t('live.platform.unavailable') : stats ? authModeLabel(stats.auth_mode) : statsPending ? t('live.accessLoading') : t('live.platform.unknown')} />
                </dl>
                <p className="mt-3 border-t border-hairline pt-3 text-meta leading-relaxed text-ink-3">{liveHint}</p>
                <button type="button" className="btn btn-ghost mt-4"
                  onClick={() => { reconnectLive(); void refetchHealth(); toast.info(t('live.reconnecting')) }}>
                  {t('live.reconnect')}
                </button>
              </Panel>

              <Panel title={<span className="flex items-center gap-1.5"><Database size={14} />{t('records.title')}</span>}
                right={<span className="text-meta text-ink-3">{t('records.autoCleanup')}</span>}>
                {statsError ? (
                  <InlineError title={t('records.errorTitle')} hint={t('records.errorHint')}
                    onRetry={() => void refetchStats()} retrying={statsFetching} />
                ) : statsPending && !stats ? (
                  <p className="py-4 text-center text-body text-ink-3">{t('records.loading')}</p>
                ) : stats ? (
                  <>
                    <dl className="space-y-2.5">
                      <KeyValue k={t('records.fields.events')} v={<span className="num">{stats.events}</span>} />
                      <KeyValue k={t('records.fields.commands')} v={<span className="num">{stats.commands}</span>} />
                      <KeyValue k={t('records.fields.devices')} v={<span className="num">{stats.devices}</span>} />
                      <KeyValue k={t('records.fields.oldest')} v={stats.oldest_event ? fmtDateTime(stats.oldest_event) : t('records.noRecords')} />
                      <KeyValue k={t('records.fields.retention')} v={t('records.retentionDays', { count: stats.retention_days })} />
                    </dl>
                    <div className="mt-3 flex flex-wrap items-center justify-between gap-2 border-t border-hairline pt-3">
                      <Link to="/activity" className="link text-meta">{t('records.view')}</Link>
                      {isAdmin && (
                        <details className="text-meta text-ink-3">
                          <summary className="cursor-pointer">{t('records.technical')}</summary>
                          <p className="mt-1">{t('records.schemaVersion', { version: stats.schema_version })}</p>
                        </details>
                      )}
                    </div>
                  </>
                ) : (
                  <p className="py-4 text-center text-body text-ink-3">{t('records.unavailable')}</p>
                )}
              </Panel>

              <Panel title={<span className="flex items-center gap-1.5"><Plug size={14} />{t('adapters.title')}</span>}
                right={<span className="text-meta text-ink-3">{adapters
                  ? t(isAdmin ? 'adapters.countRegistered' : 'adapters.countKinds', { count: adapters.adapters.length })
                  : '—'}</span>}>
                {adaptersError ? (
                  <InlineError title={t('adapters.errorTitle')} hint={t('adapters.errorHint')}
                    onRetry={() => void refetchAdapters()} retrying={adaptersFetching} />
                ) : adaptersPending && !adapters ? (
                  <p className="py-4 text-center text-body text-ink-3">{t('adapters.loading')}</p>
                ) : adapters ? (
                  adapters.adapters.length === 0 ? (
                    <p className="py-4 text-center text-body text-ink-3">{t('adapters.empty')}</p>
                  ) : isAdmin ? (
                    <div className="space-y-4">
                      {adapters.adapters.map((a) => (
                        <div key={a.name}>
                          <div className="flex min-w-0 items-center gap-2">
                            <span className="num min-w-0 truncate font-mono text-compact font-semibold" title={a.name}>{a.name}</span>
                            <span className="shrink-0 text-meta text-ink-3">{t('adapters.commands', { count: a.commands.length })}</span>
                          </div>
                          <div className="mt-2 flex flex-wrap gap-1.5">
                            {a.commands.map((c) => (
                              <span key={c} className="badge max-w-full bg-ink-3/10 text-ink-2" title={c}>
                                <span className="min-w-0 truncate">{cmdMeta(c).label}</span>
                              </span>
                            ))}
                          </div>
                        </div>
                      ))}
                      <div className="flex flex-wrap items-center justify-between gap-2 border-t border-hairline pt-3">
                        <p className="max-w-[42ch] text-meta leading-relaxed text-ink-3">
                          {t('adapters.adminHint')}
                        </p>
                        <Link to="/devices" className="link text-meta">{t('adapters.view')}</Link>
                      </div>
                    </div>
                  ) : (
                    <div className="space-y-3">
                      <p className="text-compact leading-relaxed text-ink-2">
                        {t('adapters.guestHint', { count: adapters.adapters.length })}
                      </p>
                      <div className="border-t border-hairline pt-3">
                        <Link to="/devices" className="link text-meta">{t('adapters.view')}</Link>
                      </div>
                    </div>
                  )
                ) : (
                  <p className="py-4 text-center text-body text-ink-3">{t('adapters.unavailable')}</p>
                )}
              </Panel>
            </div>
          </div>
        )}
      </details>
    </>
  )
}
