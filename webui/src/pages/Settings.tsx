import { useState } from 'react'
import { Link, useNavigate } from 'react-router'
import { useQuery } from '@tanstack/react-query'
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

function InlineError({ title, hint, onRetry, retrying }: {
  title: string
  hint: string
  onRetry?: () => void
  retrying?: boolean
}) {
  return (
    <div role="alert" className="rounded-tile bg-bad/10 px-3.5 py-3 text-bad">
      <p className="flex items-start gap-2 text-compact font-semibold">
        <AlertCircle size={14} className="mt-0.5 shrink-0" />
        <span>{title}</span>
      </p>
      <p className="mt-1 text-meta leading-relaxed opacity-90">{hint}</p>
      {onRetry && (
        <button type="button" className="btn btn-ghost mt-2.5" onClick={onRetry} disabled={retrying}>
          {retrying ? '重试中…' : '重试'}
        </button>
      )}
    </div>
  )
}

function roleSummary(role: string): string {
  if (role === 'admin') return '可以管理成员、权限和访问令牌。'
  if (role === 'operator') return '可以查看设备并执行设备操作。'
  return '可以查看设备状态和运行记录。'
}

function metricNumber(value: number | undefined, fallback = '未提供'): string {
  return typeof value === 'number' && Number.isFinite(value) ? String(value) : fallback
}

export default function Settings() {
  usePageTitle('设置')

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
  const [tokenError, setTokenError] = useState('')
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
      toast.ok(previous ? '访问令牌已清除' : '没有保存访问令牌')
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
        setTokenError(res.status === 401 || res.status === 403
          ? '访问令牌无效、已吊销或权限不足。请确认后重试。'
          : '暂时无法验证访问令牌，请稍后重试。')
        return
      }
      const verified = await res.json().catch(() => null) as { user?: { role?: string } } | null
      if (!verified?.user || !UI_ROLES.has(verified.user.role ?? '')) {
        setToken(previous)
        setTok(previous)
        setHasStoredToken(Boolean(previous))
        setTokenError('这个访问令牌只能用于网关接入，不能登录平台。请使用具备查看、操作或管理权限的令牌。')
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
        setTokenError('访问令牌已验证，但登录状态没有保存成功，请重试。')
        return
      }
      setSaved(true)
      setTimeout(() => setSaved(false), 1500)
      reconnectLive()
      toast.ok('访问令牌已验证并保存', '页面会使用新的访问令牌重新连接')
    } catch {
      setToken(previous)
      setTok(previous)
      setHasStoredToken(Boolean(previous))
      setTokenError('暂时无法验证访问令牌，请检查网络后重试。')
    } finally {
      setTokenSaving(false)
    }
  }

  const changeTheme = (mode: ThemeMode) => {
    setTheme(mode)
    setThemeMode(mode)
  }

  const liveLabel = status === 'open' ? '已连接' : status === 'connecting' ? '连接中' : '已断开'
  const liveHint = status === 'open'
    ? '页面会自动更新状态和运行记录。'
    : status === 'connecting'
      ? '正在建立实时更新；期间仍会定时刷新。'
      : '实时更新暂时断开；页面仍会定时刷新，也可以手动重连。'

  const hasDeviceMetric = typeof health?.devices_online === 'number'
    && Number.isFinite(health.devices_online)
    && typeof health?.devices_total === 'number'
    && Number.isFinite(health.devices_total)

  return (
    <>
      <PageHeader title="设置" subtitle="账号、外观和访问令牌" />

      <p className="mb-5 max-w-[62ch] text-body leading-relaxed text-ink-2">
        {authStatus === 'in'
          ? '查看当前账号、外观和高级诊断。'
          : '查看当前账号、保存访问令牌和高级诊断。'}
      </p>

      <div className="grid items-start gap-5 lg:grid-cols-2">
        <Panel title={<span className="flex items-center gap-1.5"><UserRound size={14} />账号</span>}
          right={authStatus === 'in'
            ? <Badge tone="ok">已登录</Badge>
            : authStatus === 'open'
              ? <Badge tone="idle">无需登录</Badge>
              : <Badge tone="warn">未登录</Badge>}>
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
                {roleSummary(user.role)}
              </p>

              <details className="mt-4 border-t border-hairline pt-3 text-meta text-ink-2">
                <summary className="flex min-h-touch cursor-pointer select-none items-center">账号详情</summary>
                <dl className="mt-2.5 space-y-2.5">
                  <KeyValue k="登录账号" v={<span className="font-mono">{user.username}</span>} />
                  {isAdmin && (
                    <>
                      <KeyValue k="所属组织" v={user.tenant_slug || '未设置'} mono />
                      <KeyValue k="账号 ID" v={user.id} mono />
                    </>
                  )}
                </dl>
              </details>

              <p className="mt-3 text-meta leading-relaxed text-ink-3">
                登录状态保存在这台设备。共用电脑请记得退出登录。
              </p>
              <div className="mt-3 flex flex-wrap gap-2">
                <button type="button" className="btn btn-danger-ghost" disabled={signingOut}
                  onClick={() => void signOut()}>
                  <LogOut size={13} /> {signingOut ? '退出中…' : '退出登录'}
                </button>
                {user.role === 'admin' && (
                  <Link to="/admin" className="btn btn-ghost no-underline">管理成员与访问令牌</Link>
                )}
              </div>
            </>
          ) : (
            <p className="text-meta leading-relaxed text-ink-2">
              {authStatus === 'open'
                ? '当前无需登录即可查看。需要修改设置时，请使用本机操作或联系管理员。'
                : '尚未登录。请先到登录页用账号密码登录。'}
            </p>
          )}
        </Panel>

        <Panel title={<span className="flex items-center gap-1.5"><Sun size={14} />外观</span>}>
          <p className="text-meta leading-relaxed text-ink-2">
            选择你习惯的显示方式。外观偏好只保存在这台设备。
          </p>
          <div className="mt-4">
            <Segmented<ThemeMode>
              label="外观"
              value={themeMode}
              onChange={changeTheme}
              options={[
                { value: 'system', label: '跟随系统', icon: <Monitor size={12} /> },
                { value: 'light', label: '浅色', icon: <Sun size={12} /> },
                { value: 'dark', label: '深色', icon: <Moon size={12} /> },
              ]}
            />
          </div>
        </Panel>

        <Panel title={<span className="flex items-center gap-1.5"><KeyRound size={14} />访问令牌</span>}
          className="lg:col-span-2"
          right={authStatus === 'open' && hasStoredToken ? <Badge tone="ok">已保存</Badge> : undefined}>
          {authStatus === 'in' ? (
            <p className="text-meta leading-relaxed text-ink-2">
              当前已登录，不需要再保存访问令牌。访问令牌用于自动化工具；如需切换身份，请先退出登录。
            </p>
          ) : (
            <>
              {hasStoredToken && (
                <p className="mb-3 text-meta leading-relaxed text-ink-2">
                  本机已保存访问令牌，页面会优先使用它连接平台。
                </p>
              )}
              <div className="grid gap-3 lg:grid-cols-[minmax(0,1fr)_auto] lg:items-end">
                <TextField
                  label="访问令牌"
                  type="password"
                  value={tok}
                  onChange={(e) => { setTok(e.target.value); setTokenError('') }}
                  placeholder="收到令牌或使用自动化工具时填写"
                  autoComplete="off"
                  error={tokenError}
                  hint="保存前会先验证令牌；令牌只保存在这台设备。"
                />
                <button type="button" className="btn btn-primary lg:mb-[1.625rem]" disabled={tokenSaving} onClick={() => void saveToken()}>
                  {saved && <Check size={14} />}{tokenSaving ? '验证中…' : saved ? '已保存' : '保存令牌'}
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
          <span className="flex items-center gap-1.5"><Activity size={14} />高级诊断</span>
          <span className="hidden text-meta font-normal text-ink-3 sm:inline">连接、记录与接入方式</span>
          <span className="ml-auto flex items-center gap-1 text-meta font-normal text-ink-3">
            {diagnosticsOpen ? '收起' : '展开查看'}
            <ChevronDown size={14} className={diagnosticsOpen ? 'rotate-180 transition-transform' : 'transition-transform'} />
          </span>
        </summary>
        <p className="mt-1 max-w-[62ch] text-meta leading-relaxed text-ink-3">
          这里用于排查运行和接入问题。无法读取时会明确显示失败，不会用 0 或假加载代替。
        </p>

        {diagnosticsOpen && (
          <div className="mt-5 space-y-6 fade-up">
            {healthError ? (
              <InlineError
                title="无法读取运行状态"
                hint="连接可能暂时不可用。账号、外观和访问令牌设置不受影响。"
                onRetry={() => { void refetchHealth(); reconnectLive() }}
                retrying={healthFetching}
              />
            ) : healthPending && !health ? (
              <p className="py-4 text-center text-body text-ink-3">正在读取运行状态…</p>
            ) : (
              <div className="grid grid-cols-2 gap-4 lg:grid-cols-4">
                <StatTile icon={<Server size={13} />} label="平台版本"
                  value={<span className="font-mono font-medium tracking-normal break-words">{health?.version || '未提供'}</span>} />
                <StatTile icon={<Activity size={13} />} label="已运行"
                  value={typeof health?.uptime_s === 'number' && Number.isFinite(health.uptime_s) ? fmtUptime(health.uptime_s) : '未提供'} />
                <StatTile icon={<Cpu size={13} />} label="设备在线"
                  value={hasDeviceMetric
                    ? <>{health.devices_online}<span className="text-ink-3">/{health.devices_total}</span></>
                    : '未提供'} />
                <StatTile icon={<Network size={13} />} label="网关在线"
                  value={metricNumber(health?.edges_online)} />
              </div>
            )}

            <div className="grid items-start gap-5 lg:grid-cols-2">
              <Panel title={<span className="flex items-center gap-1.5"><Wifi size={14} />实时更新</span>}
                right={<Badge tone={status === 'open' ? 'ok' : status === 'connecting' ? 'warn' : 'bad'}>
                  {liveLabel}
                </Badge>}>
                <dl className="space-y-2.5">
                  <KeyValue k="更新状态" v={liveLabel} />
                  <KeyValue k="平台状态"
                    v={healthError ? '暂时无法读取' : health?.ok === true ? '正常' : health?.ok === false ? '需要检查' : '未提供'} />
                  <KeyValue k="使用权限"
                    v={statsError ? '暂时无法读取' : stats ? authModeLabel(stats.auth_mode) : statsPending ? '正在读取…' : '未提供'} />
                </dl>
                <p className="mt-3 border-t border-hairline pt-3 text-meta leading-relaxed text-ink-3">{liveHint}</p>
                <button type="button" className="btn btn-ghost mt-4"
                  onClick={() => { reconnectLive(); void refetchHealth(); toast.info('正在重新连接…') }}>
                  重新连接
                </button>
              </Panel>

              <Panel title={<span className="flex items-center gap-1.5"><Database size={14} />记录与保留</span>}
                right={<span className="text-meta text-ink-3">自动清理</span>}>
                {statsError ? (
                  <InlineError title="无法读取记录统计" hint="已经保存的记录不会因此删除。请稍后重试。"
                    onRetry={() => void refetchStats()} retrying={statsFetching} />
                ) : statsPending && !stats ? (
                  <p className="py-4 text-center text-body text-ink-3">正在读取记录统计…</p>
                ) : stats ? (
                  <>
                    <dl className="space-y-2.5">
                      <KeyValue k="运行记录总数" v={<span className="num">{stats.events}</span>} />
                      <KeyValue k="操作记录总数" v={<span className="num">{stats.commands}</span>} />
                      <KeyValue k="已接入设备" v={<span className="num">{stats.devices}</span>} />
                      <KeyValue k="最早运行记录" v={stats.oldest_event ? fmtDateTime(stats.oldest_event) : '尚无记录'} />
                      <KeyValue k="自动保留" v={`${stats.retention_days} 天`} />
                    </dl>
                    <div className="mt-3 flex flex-wrap items-center justify-between gap-2 border-t border-hairline pt-3">
                      <Link to="/activity" className="link text-meta">查看运行记录</Link>
                      {isAdmin && (
                        <details className="text-meta text-ink-3">
                          <summary className="cursor-pointer">技术详情</summary>
                          <p className="mt-1">记录格式版本：v{stats.schema_version}</p>
                        </details>
                      )}
                    </div>
                  </>
                ) : (
                  <p className="py-4 text-center text-body text-ink-3">记录统计未提供。</p>
                )}
              </Panel>

              <Panel title={<span className="flex items-center gap-1.5"><Plug size={14} />设备接入</span>}
                right={<span className="text-meta text-ink-3">{adapters ? `${adapters.adapters.length} ${isAdmin ? '个已登记' : '种接入方式'}` : '—'}</span>}>
                {adaptersError ? (
                  <InlineError title="无法读取设备接入信息" hint="已经接入的设备不会受影响。请稍后重试。"
                    onRetry={() => void refetchAdapters()} retrying={adaptersFetching} />
                ) : adaptersPending && !adapters ? (
                  <p className="py-4 text-center text-body text-ink-3">正在读取设备接入信息…</p>
                ) : adapters ? (
                  adapters.adapters.length === 0 ? (
                    <p className="py-4 text-center text-body text-ink-3">还没有设备接入方式。设备连接后会显示在这里。</p>
                  ) : isAdmin ? (
                    <div className="space-y-4">
                      {adapters.adapters.map((a) => (
                        <div key={a.name}>
                          <div className="flex min-w-0 items-center gap-2">
                            <span className="num min-w-0 truncate font-mono text-compact font-semibold" title={a.name}>{a.name}</span>
                            <span className="shrink-0 text-meta text-ink-3">{a.commands.length} 个操作</span>
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
                          这里显示设备可以执行的操作。没有权限时，操作会被拒绝。
                        </p>
                        <Link to="/devices" className="link text-meta">查看已接入设备</Link>
                      </div>
                    </div>
                  ) : (
                    <div className="space-y-3">
                      <p className="text-compact leading-relaxed text-ink-2">
                        已登记 {adapters.adapters.length} 种设备接入方式。设备连接后，平台会在这里显示可用的操作类别。
                      </p>
                      <div className="border-t border-hairline pt-3">
                        <Link to="/devices" className="link text-meta">查看已接入设备</Link>
                      </div>
                    </div>
                  )
                ) : (
                  <p className="py-4 text-center text-body text-ink-3">设备接入信息未提供。</p>
                )}
              </Panel>
            </div>
          </div>
        )}
      </details>
    </>
  )
}
