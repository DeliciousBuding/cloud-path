import { useState } from 'react'
import { useNavigate } from 'react-router'
import { useQuery } from '@tanstack/react-query'
import {
  Activity, Boxes, Check, Cpu, Database, KeyRound, LogOut, Network, Plug, Server, UserRound, Wifi,
} from 'lucide-react'
import { PageHeader, Panel, StatTile, Badge, KeyValue } from '@/components/ui'
import { api, getToken, setToken, wsUrl } from '@/lib/api'
import { cmdMeta, fmtDateTime, fmtUptime, roleLabel } from '@/lib/format'
import { useLive, reconnectLive } from '@/store/ws'
import { logout, useAuth } from '@/store/auth'
import { toast } from '@/store/toast'

export default function Settings() {
  const { data: health, isFetching } = useQuery({ queryKey: ['health'], queryFn: api.health, refetchInterval: 10000 })
  const { data: stats } = useQuery({ queryKey: ['stats'], queryFn: api.stats, refetchInterval: 15000 })
  const { data: adapters } = useQuery({ queryKey: ['adapters'], queryFn: api.adapters, staleTime: 5 * 60_000 })
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
    reconnectLive() // 令牌变更立即重连生效
    toast.ok('令牌已保存', '实时通道已用新令牌重连')
  }

  return (
    <>
      <PageHeader title="系统" subtitle="服务状态、账号、存储、适配器与本机令牌" />

      <div className="grid grid-cols-2 gap-4 lg:grid-cols-4">
        <StatTile icon={<Server size={13} />} label="服务版本" value={health?.version ?? '—'} />
        <StatTile icon={<Activity size={13} />} label="运行时长" value={health ? fmtUptime(health.uptime_s) : '—'} />
        <StatTile icon={<Cpu size={13} />} label="设备在线"
          value={<>{health?.devices_online ?? 0}<span className="text-ink-3">/{health?.devices_total ?? 0}</span></>} />
        <StatTile icon={<Network size={13} />} label="边缘在线" value={health?.edges_online ?? 0} />
      </div>

      <div className="mt-6 grid items-start gap-5 lg:grid-cols-2">
        <Panel title={<span className="flex items-center gap-1.5"><Wifi size={14} />实时连接</span>}
          right={<Badge tone={status === 'open' ? 'ok' : status === 'connecting' ? 'warn' : 'bad'}>
            {status === 'open' ? '已连接' : status === 'connecting' ? '连接中' : '已断开'}
          </Badge>}>
          <dl className="space-y-2.5">
            <KeyValue k="WS 端点" v={wsUrl()} mono />
            <KeyValue k="服务健康" v={isFetching ? '检查中…' : health?.ok ? '正常' : '异常'} />
            <KeyValue k="自动重连" v="断线后自动重试（1–15 秒）" />
            <KeyValue k="鉴权" v={stats?.auth_enabled ? '已启用账号鉴权' : '未启用（本机模式）'} />
          </dl>
          <button type="button" className="btn btn-ghost mt-4"
            onClick={() => { reconnectLive(); toast.info('正在重连…') }}>
            重新连接
          </button>
        </Panel>

        <Panel title={<span className="flex items-center gap-1.5"><UserRound size={14} />账号</span>}
          right={authStatus === 'in'
            ? <Badge tone="ok">已登录</Badge>
            : authStatus === 'open'
              ? <Badge tone="idle">开放访问</Badge>
              : <Badge tone="warn">未登录</Badge>}>
          {authStatus === 'in' && user ? (
            <>
              <dl className="space-y-2.5">
                <KeyValue k="用户名" v={<span className="num min-w-0 truncate" title={user.username}>{user.username}</span>} />
                <KeyValue k="姓名" v={<span className="min-w-0 truncate" title={user.name}>{user.name || '—'}</span>} />
                <KeyValue k="角色" v={roleLabel(user.role)} />
                <KeyValue k="租户" v={<span className="num min-w-0 truncate" title={user.tenant_slug}>{user.tenant_slug || '—'}</span>} />
              </dl>
              <p className="mt-3 border-t border-hairline pt-3 text-[11px] leading-relaxed text-ink-3">
                登录态由浏览器会话承载；共用机器请记得登出。
              </p>
              <button type="button" className="btn btn-danger-ghost mt-3" disabled={signingOut}
                onClick={() => void signOut()}>
                <LogOut size={13} /> {signingOut ? '登出中…' : '登出'}
              </button>
            </>
          ) : (
            <p className="text-xs leading-relaxed text-ink-2">
              {authStatus === 'open'
                ? '当前是开放访问（GET /api/auth/me 不可用），说明 server 还没启用账号鉴权或尚未初始化。启用后这里会显示登录账号。'
                : '尚未登录。受保护的数据接口会返回 401，请先到登录页用账号密码登录。'}
            </p>
          )}
        </Panel>

        <Panel title={<span className="flex items-center gap-1.5"><KeyRound size={14} />本机令牌（可选）</span>}>
          <p className="mb-3 text-xs leading-relaxed text-ink-2">
            账号模式下浏览器靠<strong>会话 cookie</strong> 鉴权，这里通常<strong>不需要填任何东西</strong>。
            仅两种情况需要本机令牌：server 以 legacy 共享令牌
            （<code className="rounded bg-ink-3/10 px-1 font-mono">CLOUDPATH_TOKEN</code>）运行时，
            或你在用服务令牌做 API / 机器客户端接入。令牌只保存在本机浏览器 localStorage。
          </p>
          <div className="flex gap-2">
            <label className="sr-only" htmlFor="token">接入令牌</label>
            <input
              id="token"
              type="password"
              value={tok}
              onChange={(e) => setTok(e.target.value)}
              placeholder="留空 = 用会话 cookie（账号模式默认）"
              autoComplete="off"
              className="min-w-0 flex-1 rounded-full border border-hairline bg-surface-2 px-3.5 py-2 text-sm outline-none transition-colors focus:border-accent"
            />
            <button type="button" className="btn btn-primary shrink-0" onClick={saveToken}>
              {saved && <Check size={14} />}{saved ? '已保存' : '保存'}
            </button>
          </div>
        </Panel>

        <Panel title={<span className="flex items-center gap-1.5"><Database size={14} />存储</span>}
          right={<span className="text-[11px] text-ink-3">SQLite</span>}>
          <dl className="space-y-2.5">
            <KeyValue k="事件总数" v={<span className="num">{stats?.events ?? '—'}</span>} />
            <KeyValue k="命令总数" v={<span className="num">{stats?.commands ?? '—'}</span>} />
            <KeyValue k="注册设备" v={<span className="num">{stats?.devices ?? '—'}</span>} />
            <KeyValue k="最早事件" v={stats?.oldest_event ? fmtDateTime(stats.oldest_event) : '—'} />
            <KeyValue k="保留期" v={`${stats?.retention_days ?? '—'} 天（超期自动清理）`} />
            <KeyValue k="Schema 版本" v={<span className="num">v{stats?.schema_version ?? '—'}</span>} />
          </dl>
        </Panel>

        <Panel title={<span className="flex items-center gap-1.5"><Plug size={14} />设备适配器</span>}
          right={<span className="text-[11px] text-ink-3">{adapters?.adapters.length ?? 0} 个已注册</span>}>
          {!(adapters?.adapters.length) ? (
            <p className="py-4 text-center text-sm text-ink-3">加载适配器清单…</p>
          ) : (
            <div className="space-y-4">
              {adapters.adapters.map((a) => (
                <div key={a.name}>
                  {/* 390px：适配器名来自后端注册，长名字必须可截断 */}
                  <div className="flex min-w-0 items-center gap-2">
                    <span className="num min-w-0 truncate font-mono text-[13px] font-semibold" title={a.name}>{a.name}</span>
                    <Badge tone="idle" className="shrink-0">{a.commands.length} 条命令</Badge>
                  </div>
                  <div className="mt-2 flex flex-wrap gap-1.5">
                    {a.commands.map((c) => (
                      <span key={c} className="badge max-w-full bg-ink-3/10 text-ink-2" title={`${c} → ${cmdMeta(c).hint || '—'}`}>
                        <span className="min-w-0 truncate">{cmdMeta(c).label}</span>
                        <span className="num ml-1 min-w-0 truncate font-mono text-[10px] text-ink-3">{c}</span>
                      </span>
                    ))}
                  </div>
                </div>
              ))}
              <p className="border-t border-hairline pt-3 text-[11px] leading-relaxed text-ink-3">
                命令白名单由适配器声明，server 拒绝白名单外的命令；前端命令面板自动跟随。
              </p>
            </div>
          )}
        </Panel>

        <Panel title={<span className="flex items-center gap-1.5"><Boxes size={14} />关于</span>} className="lg:col-span-2">
          <p className="text-sm leading-relaxed text-ink-2">
            <span className="font-semibold text-ink">Cloudpath（云径）</span> 是通用 IoT 接入与管理平台：
            边缘代理把本地串口设备聚合上云，中心服务统一监控、下发命令并持久化事件，
            管理台通过 WebSocket 实时可视化。核心不绑定任何具体硬件或行业语义——
            设备语义由适配器插件提供。
          </p>
          <div className="mt-4 flex flex-wrap gap-2 border-t border-hairline pt-4 text-xs text-ink-3">
            <span className="badge bg-ink-3/10">Go · chi · WebSocket</span>
            <span className="badge bg-ink-3/10">SQLite（纯 Go，零 CGO）</span>
            <span className="badge bg-ink-3/10">React 19 · Vite · Tailwind 4</span>
            <span className="badge bg-ink-3/10">单二进制发布（前端内嵌）</span>
          </div>
        </Panel>
      </div>
    </>
  )
}
