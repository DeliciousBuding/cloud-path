import { useState } from 'react'
import { Link, useNavigate } from 'react-router'
import { useQuery } from '@tanstack/react-query'
import {
  Activity, AlertCircle, Boxes, Check, Cpu, Database, KeyRound, LogOut, Network, Plug, Server,
  UserRound, Wifi,
} from 'lucide-react'
import { PageHeader, Panel, StatTile, Badge, KeyValue } from '@/components/ui'
import { api, getToken, setToken } from '@/lib/api'
import { authModeLabel, cmdMeta, fmtDateTime, fmtUptime, roleLabel } from '@/lib/format'
import { useLive, reconnectLive } from '@/store/ws'
import { usePageTitle } from '@/hooks/usePageTitle'
import { logout, useAuth } from '@/store/auth'
import { toast } from '@/store/toast'

function InlineError({ title, hint, onRetry, retrying }: {
  title: string
  hint: string
  onRetry?: () => void
  retrying?: boolean
}) {
  return (
    <div role="alert" className="rounded-lg bg-bad/10 px-3.5 py-3 text-bad">
      <p className="flex items-start gap-2 text-[13px] font-semibold">
        <AlertCircle size={14} className="mt-0.5 shrink-0" />
        <span>{title}</span>
      </p>
      <p className="mt-1 text-[12px] leading-relaxed opacity-90">{hint}</p>
      {onRetry && (
        <button type="button" className="btn btn-ghost mt-2.5" onClick={onRetry} disabled={retrying}>
          {retrying ? '重试中…' : '重试'}
        </button>
      )}
    </div>
  )
}

export default function Settings() {
  usePageTitle('设置')

  const {
    data: health, isFetching, isError: healthError, refetch: refetchHealth,
  } = useQuery({ queryKey: ['health'], queryFn: api.health, refetchInterval: 10000 })
  const { data: stats, isError: statsError, refetch: refetchStats } = useQuery({
    queryKey: ['stats'], queryFn: api.stats, refetchInterval: 15000,
  })
  const { data: adapters, isError: adaptersError, refetch: refetchAdapters } = useQuery({
    queryKey: ['adapters'], queryFn: api.adapters, staleTime: 5 * 60_000,
  })
  const status = useLive((s) => s.status)
  const authStatus = useAuth((s) => s.status)
  const user = useAuth((s) => s.user)
  const navigate = useNavigate()
  const [tok, setTok] = useState(getToken)
  const [saved, setSaved] = useState(false)
  const [signingOut, setSigningOut] = useState(false)

  async function signOut() {
    setSigningOut(true)
    await logout()
    navigate('/login', { replace: true })
  }

  const saveToken = () => {
    setToken(tok.trim())
    setSaved(true)
    setTimeout(() => setSaved(false), 1500)
    reconnectLive()
    toast.ok('访问令牌已保存', '页面会使用新的访问令牌重新连接')
  }

  const liveLabel = status === 'open' ? '已连接' : status === 'connecting' ? '连接中' : '已断开'
  const liveHint = status === 'open'
    ? '页面会自动更新状态和运行记录。'
    : status === 'connecting'
      ? '正在建立实时更新；期间仍会定时刷新。'
      : '实时更新暂时断开；页面仍会定时刷新，也可以手动重连。'

  return (
    <>
      <PageHeader title="设置" subtitle="账号、访问令牌和高级诊断" />

      <p className="mb-5 max-w-[62ch] text-sm leading-relaxed text-ink-2">
        账号和访问令牌可以直接在这里设置。平台运行情况、记录统计和设备连接方式在下方「高级诊断」中查看。
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
              <dl className="space-y-2.5">
                <KeyValue k="用户名" v={<span className="min-w-0 truncate font-mono" title={user.username}>{user.username}</span>} />
                <KeyValue k="姓名" v={<span className="min-w-0 truncate" title={user.name}>{user.name || '—'}</span>} />
                <KeyValue k="角色" v={roleLabel(user.role)} />
                <KeyValue k="组织" v={<span className="min-w-0 truncate font-mono" title={user.tenant_slug}>{user.tenant_slug || '—'}</span>} />
              </dl>
              <p className="mt-3 border-t border-hairline pt-3 text-[12px] leading-relaxed text-ink-3">
                登录状态保存在这台设备的浏览器中。共用电脑请记得退出登录。
              </p>
              <div className="mt-3 flex flex-wrap gap-2">
                <button type="button" className="btn btn-danger-ghost" disabled={signingOut}
                  onClick={() => void signOut()}>
                  <LogOut size={13} /> {signingOut ? '退出中…' : '退出登录'}
                </button>
                {user.role === 'admin' && <Link to="/admin" className="btn btn-ghost no-underline">管理用户和访问令牌</Link>}
              </div>
            </>
          ) : (
            <p className="text-xs leading-relaxed text-ink-2">
              {authStatus === 'open'
                ? '当前无需登录即可查看。需要修改设置时，请使用本机操作或联系管理员。'
                : '尚未登录。请先到登录页用账号密码登录。'}
            </p>
          )}
        </Panel>

        <Panel title={<span className="flex items-center gap-1.5"><KeyRound size={14} />访问令牌（可选）</span>}>
          <p className="mb-3 text-xs leading-relaxed text-ink-2">
            账号登录时通常<strong>不需要填写</strong>。只有在你收到访问令牌，或自动化工具需要连接时再填写。
            令牌只保存在这台设备的浏览器中。
          </p>
          <div className="flex flex-col gap-2 sm:flex-row">
            <label className="sr-only" htmlFor="token">访问令牌</label>
            <input
              id="token"
              type="password"
              value={tok}
              onChange={(e) => setTok(e.target.value)}
              placeholder="留空即使用当前登录状态"
              autoComplete="off"
              className="min-w-0 flex-1 rounded-full border border-hairline bg-surface-2 px-3.5 py-2 text-sm outline-none transition-colors focus:border-accent"
            />
            <button type="button" className="btn btn-primary shrink-0" onClick={saveToken}>
              {saved && <Check size={14} />}{saved ? '已保存' : '保存'}
            </button>
          </div>
        </Panel>

        <Panel title={<span className="flex items-center gap-1.5"><Boxes size={14} />关于</span>} className="lg:col-span-2">
          <p className="max-w-[62ch] text-sm leading-relaxed text-ink-2">
            <span className="font-semibold text-ink">Cloudpath（云径）</span> 是设备接入与管理平台。
            网关负责连接本地设备，平台集中显示状态、执行操作并保存记录。
            平台不绑定具体硬件或行业，设备功能由应用或插件提供。
          </p>
          <p className="mt-4 border-t border-hairline pt-4 text-xs leading-relaxed text-ink-3">
            Cloudpath（云径） · 设备接入与管理平台
          </p>
        </Panel>
      </div>

      <details className="mt-6 rounded-xl border border-hairline bg-surface p-4">
        <summary className="cursor-pointer text-sm font-medium text-ink-2">高级诊断</summary>
        <p className="mt-1 max-w-[62ch] text-xs leading-relaxed text-ink-3">
          平台运行情况、记录统计和设备连接方式。普通使用不需要修改这里。
        </p>

        <div className="mt-5 space-y-6">
          <div className="grid grid-cols-2 gap-4 lg:grid-cols-4">
            <StatTile icon={<Server size={13} />} label="平台版本"
              value={<span className="font-mono font-medium tracking-normal break-words">{health?.version ?? '—'}</span>} />
            <StatTile icon={<Activity size={13} />} label="已运行"
              value={health ? fmtUptime(health.uptime_s) : '—'} />
            <StatTile icon={<Cpu size={13} />} label="设备在线"
              value={health ? <>{health.devices_online}<span className="text-ink-3">/{health.devices_total}</span></> : '—'} />
            <StatTile icon={<Network size={13} />} label="网关在线" value={health ? health.edges_online : '—'} />
          </div>

          <div className="grid items-start gap-5 lg:grid-cols-2">
            <Panel title={<span className="flex items-center gap-1.5"><Wifi size={14} />实时更新</span>}
              right={<Badge tone={status === 'open' ? 'ok' : status === 'connecting' ? 'warn' : 'bad'}>
                {liveLabel}
              </Badge>}>
              {healthError ? (
                <InlineError title="平台状态暂时不可用"
                  hint="页面其他内容仍可使用。请稍后重试，或重新连接实时更新。"
                  onRetry={() => { void refetchHealth(); reconnectLive() }} retrying={isFetching} />
              ) : (
                <>
                  <dl className="space-y-2.5">
                    <KeyValue k="更新状态" v={liveLabel} />
                    <KeyValue k="平台状态" v={isFetching && !health ? '检查中…' : health?.ok ? '正常' : '需要检查'} />
                    <KeyValue k="使用权限" v={stats ? authModeLabel(stats.auth_mode) : '正在读取…'} />
                  </dl>
                  <p className="mt-3 border-t border-hairline pt-3 text-[12px] leading-relaxed text-ink-3">{liveHint}</p>
                </>
              )}
              <button type="button" className="btn btn-ghost mt-4"
                onClick={() => { reconnectLive(); void refetchHealth(); toast.info('正在重新连接…') }}>
                重新连接
              </button>
            </Panel>

            <Panel title={<span className="flex items-center gap-1.5"><Database size={14} />记录与保留</span>}
              right={<span className="text-[12px] text-ink-3">自动清理</span>}>
              {statsError ? (
                <InlineError title="记录统计暂时不可用" hint="请稍后重试；已经保存的记录不会因此删除。"
                  onRetry={() => void refetchStats()} />
              ) : !stats ? (
                <p className="py-4 text-center text-sm text-ink-3">正在读取记录统计…</p>
              ) : (
                <>
                  <dl className="space-y-2.5">
                    <KeyValue k="运行记录总数" v={<span className="num">{stats.events}</span>} />
                    <KeyValue k="操作记录总数" v={<span className="num">{stats.commands}</span>} />
                    <KeyValue k="已接入设备" v={<span className="num">{stats.devices}</span>} />
                    <KeyValue k="最早运行记录" v={stats.oldest_event ? fmtDateTime(stats.oldest_event) : '尚无记录'} />
                    <KeyValue k="自动保留" v={`${stats.retention_days} 天`} />
                  </dl>
                  <div className="mt-3 flex flex-wrap items-center justify-between gap-2 border-t border-hairline pt-3">
                    <Link to="/activity" className="link text-[12px]">查看运行记录</Link>
                    <details className="text-[12px] text-ink-3">
                      <summary className="cursor-pointer">技术详情</summary>
                      <p className="mt-1">记录格式版本：v{stats.schema_version}</p>
                    </details>
                  </div>
                </>
              )}
            </Panel>

            <Panel title={<span className="flex items-center gap-1.5"><Plug size={14} />设备接入</span>}
              right={<span className="text-[12px] text-ink-3">{adapters ? `${adapters.adapters.length} 个已登记` : '—'}</span>}>
              {adaptersError ? (
                <InlineError title="设备接入信息暂时不可用" hint="请稍后重试；已经接入的设备不会受影响。"
                  onRetry={() => void refetchAdapters()} />
              ) : !adapters ? (
                <p className="py-4 text-center text-sm text-ink-3">正在加载设备接入信息…</p>
              ) : adapters.adapters.length === 0 ? (
                <p className="py-4 text-center text-sm text-ink-3">还没有设备接入方式。设备连接后会显示在这里。</p>
              ) : (
                <div className="space-y-4">
                  {adapters.adapters.map((a) => (
                    <div key={a.name}>
                      <div className="flex min-w-0 items-center gap-2">
                        <span className="num min-w-0 truncate font-mono text-[13px] font-semibold" title={a.name}>{a.name}</span>
                        <span className="shrink-0 text-[12px] text-ink-3">{a.commands.length} 个操作</span>
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
                    <p className="max-w-[42ch] text-[12px] leading-relaxed text-ink-3">
                      这里显示设备可以执行的操作。没有权限时，操作会被拒绝。
                    </p>
                    <Link to="/devices" className="link text-[12px]">查看已接入设备</Link>
                  </div>
                </div>
              )}
            </Panel>
          </div>
        </div>
      </details>
    </>
  )
}
