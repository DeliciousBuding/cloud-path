// Setup：首次运行向导 —— ① 连通 server ② 创建首个管理员账号 ③ 完成。
//
// 真实后端语义（docs/api.md §2、internal/server/auth_handlers.go）：
//   POST /api/auth/setup {username,password} → 200 {user}；
//   真实 TCP 回环永远放行，非回环需一次性 X-Cloudpath-Setup-Token；
//   **首个用户落库后立即进入全鉴权账号模式**。因此公网访问这里基本会 403，
//   已初始化过则 409 —— 两种都必须说成人话并把用户导流到登录页，不能白屏或甩原始错误。
// 路由约定：/setup（default export 名固定为 Setup）。
import { useCallback, useEffect, useState } from 'react'
import type { FormEvent } from 'react'
import { Link, useNavigate } from 'react-router'
import {
  ArrowLeft, ArrowRight, Check, Eye, EyeOff, LogIn, PartyPopper, RefreshCw, ShieldAlert,
} from 'lucide-react'
import { AuthCard, Button, Spinner, TextField } from '@/components/ui'
import { api } from '@/lib/api'
import { SESSION_NOT_ESTABLISHED, setupErrorCopy } from '@/lib/authErrors'
import { confirmSession } from '@/store/auth'
import { cn } from '@/lib/cn'
import type { HealthView } from '@/lib/types'
import { usePageTitle } from '@/hooks/usePageTitle'

type Phase = 'checking' | 'ok' | 'fail'

const STEPS = [
  { label: '连接 CloudPath', short: '连接服务' },
  { label: '创建管理员账号', short: '创建账号' },
  { label: '完成', short: '完成' },
]

/** 与服务端一致的上限（本地先拦一次，省一个来回；最终判定仍在服务端） */
const MAX_USERNAME = 64
const MAX_PASSWORD = 256

export default function Setup() {
  usePageTitle('初始化')

  const navigate = useNavigate()
  const [step, setStep] = useState(0)

  // ---- 步骤 1：连通性探测 ----
  const [phase, setPhase] = useState<Phase>('checking')
  const [health, setHealth] = useState<HealthView | null>(null)
  const [probeError, setProbeError] = useState('')
  /** me→200 说明已经登录过了，没必要再走初始化 */
  const [alreadyIn, setAlreadyIn] = useState(false)

  const probe = useCallback(async () => {
    setPhase('checking')
    setProbeError('')
    try {
      const h = await api.health()
      setHealth(h)
      setPhase('ok')
    } catch {
      setProbeError('暂时无法连接 CloudPath。')
      setPhase('fail')
      return
    }
    // 顺带看一眼是否已登录（失败无所谓，只是省掉一次注定 409 的提交）
    try {
      await api.me()
      setAlreadyIn(true)
    } catch {
      setAlreadyIn(false)
    }
  }, [])

  useEffect(() => { void probe() }, [probe])

  // ---- 步骤 2：创建首个管理员账号 ----
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [confirm, setConfirm] = useState('')
  const [reveal, setReveal] = useState(false)
  const [busy, setBusy] = useState(false)
  const [fieldError, setFieldError] = useState<{ user?: string; pass?: string; confirm?: string }>({})
  const [formError, setFormError] = useState('')
  /** 403/409：本实例不该再走 setup，UI 改成导流到登录页 */
  const [redirectToLogin, setRedirectToLogin] = useState(false)
  const [createdUser, setCreatedUser] = useState('')

  /**
   * 步骤 1 的 /healthz 快照里有没有已接入的边缘/设备。那时实例还没进账号模式、边缘连得上，
   * 所以这是「完成 setup 后会被断开」的准确信号。全新安装为 false，完成页就不插一段
   * 与本实例无关的警告——只在真的会咬人时才说。
   */
  const hasConnectedFleet = (health?.edges_online ?? 0) > 0 || (health?.devices_online ?? 0) > 0

  async function onCreate(e: FormEvent<HTMLFormElement>) {
    e.preventDefault()
    const u = username.trim()
    const next: typeof fieldError = {}
    if (!u) next.user = '请输入用户名'
    else if (u.length > MAX_USERNAME) next.user = `用户名不超过 ${MAX_USERNAME} 个字符`
    if (!password) next.pass = '请输入密码'
    else if (password.length > MAX_PASSWORD) next.pass = `密码不超过 ${MAX_PASSWORD} 个字符`
    if (password !== confirm) next.confirm = '两次输入的密码不一致'
    setFieldError(next)
    if (next.user || next.pass || next.confirm) return

    setBusy(true)
    setFormError('')
    // 两段语义必须分开：setup 调用本身失败 → 按状态码给权限/格式文案；
    // setup 已 2xx（账号不可逆落库、实例已进全鉴权）但会话复核失败 → 说真话并导流登录页，
    // 绝不能报「用户名或密码错误」，那会让人重输一套刚设定、且完全正确的凭据（重试只会 409）。
    let created = false
    try {
      const r = await api.setup(u, password)
      created = true
      const user = await confirmSession(r?.user ?? null)
      setCreatedUser(user?.username || u)
      setStep(2)
    } catch (err) {
      if (created) {
        setFormError(SESSION_NOT_ESTABLISHED.setup)
        setRedirectToLogin(true)
      } else {
        const copy = setupErrorCopy(err)
        setFormError(copy.message)
        if (copy.alreadySetup) setRedirectToLogin(true)
      }
      setBusy(false)
    }
  }

  return (
    <AuthCard
      title="设置 CloudPath"
      subtitle="三步完成首次配置"
      footer={
        step === 2
          ? <Link to="/login" className="link">换个账号？去登录页</Link>
          : <Link to="/login" className="link">已有账号？直接登录</Link>
      }
    >
      {/* 步骤指示器：移动端用短标签，避免 390px 溢出 */}
      <ol className="mb-6 flex items-center" aria-label="设置进度">
        {STEPS.map((item, i) => (
          <li key={item.label} className={cn('flex items-center', i < STEPS.length - 1 && 'flex-1')}>
            <span
              aria-current={i === step ? 'step' : undefined}
              className={cn(
                'flex h-6 w-6 shrink-0 items-center justify-center rounded-full text-[12px] font-semibold',
                i < step ? 'bg-accent text-accent-ink'
                  : i === step ? 'bg-accent/15 text-accent'
                    : 'bg-ink-3/12 text-ink-3',
              )}
            >
              {i < step ? <Check size={13} strokeWidth={2.5} /> : i + 1}
            </span>
            <span className={cn(
              'ml-2 text-xs sm:hidden',
              i === step ? 'font-medium text-ink' : 'text-ink-3',
            )}>
              {item.short}
            </span>
            <span className={cn(
              'ml-2 hidden text-xs sm:block',
              i === step ? 'font-medium text-ink' : 'text-ink-3',
            )}>
              {item.label}
            </span>
            {i < STEPS.length - 1 && (
              <span className={cn('mx-2 h-px flex-1 sm:mx-3', i < step ? 'bg-accent/40' : 'bg-hairline')} />
            )}
          </li>
        ))}
      </ol>

      {step === 0 && (
        <div className="space-y-4">
          {phase === 'checking' && (
            <p className="flex items-center gap-2 text-sm text-ink-2">
              <Spinner /> 正在连接 CloudPath…
            </p>
          )}
          {phase === 'ok' && health && (
            <div className="rounded-lg bg-ok/10 p-4">
              <p className="flex items-center gap-2 text-sm font-medium text-ok">
                <Check size={15} strokeWidth={2.5} /> CloudPath 已就绪
              </p>
              <p className="mt-1 text-xs break-words text-ink-2">版本 <span className="font-mono">{health.version}</span></p>
              {alreadyIn && (
                <p className="mt-2 text-xs leading-relaxed text-ink-2">
                  你已经登录了，无需再初始化。
                </p>
              )}
            </div>
          )}
          {phase === 'fail' && (
            <div className="rounded-lg bg-bad/10 p-4">
              <p className="text-sm font-medium text-bad">暂时无法连接 CloudPath</p>
              <p className="mt-1 break-words text-xs text-ink-2">{probeError}</p>
              <p className="mt-1 text-xs text-ink-3">请确认 CloudPath 正在运行，然后重试。</p>
            </div>
          )}
          <div className="flex gap-2">
            <Button variant="ghost" lg disabled={phase === 'checking'} onClick={() => void probe()} className="shrink-0">
              <RefreshCw size={14} /> {phase === 'checking' ? '检测中…' : phase === 'ok' ? '重新检测' : '重试'}
            </Button>
            {alreadyIn ? (
              <Button lg onClick={() => navigate('/', { replace: true })} className="min-w-0 flex-1">
                进入 CloudPath <ArrowRight size={14} />
              </Button>
            ) : (
              <Button lg disabled={phase !== 'ok'} onClick={() => setStep(1)} className="min-w-0 flex-1">
                下一步：创建账号 <ArrowRight size={14} />
              </Button>
            )}
          </div>
        </div>
      )}

      {step === 1 && (
        redirectToLogin ? (
          <div className="space-y-4">
            <div role="alert" className="rounded-lg bg-warn/12 p-3.5 text-[13px] leading-relaxed break-words text-warn">
              {formError}
              <Link to="/login" className="link mt-2 flex items-center gap-1 text-[13px]">
                <LogIn size={13} /> 去登录页
              </Link>
            </div>
            <div className="flex gap-2">
              <Button type="button" variant="ghost" lg onClick={() => { setRedirectToLogin(false); setFormError(''); setStep(0) }} className="shrink-0">
                <ArrowLeft size={14} /> 上一步
              </Button>
              <Button type="button" lg className="min-w-0 flex-1" onClick={() => navigate('/login', { replace: true })}>
                去登录页 <ArrowRight size={14} />
              </Button>
            </div>
          </div>
        ) : (
        <form onSubmit={onCreate} noValidate className="space-y-4">
          <div className="rounded-lg bg-surface-2 p-3.5">
            <div className="flex items-start gap-2">
              <ShieldAlert size={14} className="mt-0.5 shrink-0 text-warn" />
              <div className="min-w-0 text-[12px] leading-relaxed text-ink-2">
                <p>这里创建的是<span className="font-semibold text-ink">首个管理员账号</span>。完成后：</p>
                <ul className="mt-2 list-disc space-y-1 pl-4">
                  <li>只有已登录的成员可以进入平台。</li>
                  <li>其他成员由管理员在「管理 → 成员与访问权限」中添加。</li>
                </ul>
                <p className="mt-2">如果你不是这台设备的直接使用者，请先联系管理员。</p>
              </div>
            </div>
          </div>

          <TextField
            label="用户名"
            name="username"
            type="text"
            placeholder="例如 admin"
            autoComplete="username"
            autoCapitalize="none"
            spellCheck={false}
            maxLength={MAX_USERNAME}
            value={username}
            error={fieldError.user}
            disabled={busy}
            onChange={(e) => { setUsername(e.target.value); setFieldError((f) => ({ ...f, user: undefined })) }}
          />
          <TextField
            label="密码"
            name="password"
            type={reveal ? 'text' : 'password'}
            placeholder="给这个账号设一个密码"
            autoComplete="new-password"
            maxLength={MAX_PASSWORD}
            value={password}
            error={fieldError.pass}
            disabled={busy}
            onChange={(e) => { setPassword(e.target.value); setFieldError((f) => ({ ...f, pass: undefined })) }}
            suffix={
              <button
                type="button"
                onClick={() => setReveal(!reveal)}
                aria-label={reveal ? '隐藏密码' : '显示密码'}
                title={reveal ? '隐藏密码' : '显示密码'}
                aria-pressed={reveal}
                className="flex h-7 w-7 items-center justify-center rounded-full text-ink-3 transition-colors hover:text-ink"
              >
                {reveal ? <EyeOff size={15} /> : <Eye size={15} />}
              </button>
            }
          />
          <TextField
            label="确认密码"
            name="confirm-password"
            type={reveal ? 'text' : 'password'}
            placeholder="再输一次"
            autoComplete="new-password"
            maxLength={MAX_PASSWORD}
            value={confirm}
            error={fieldError.confirm}
            disabled={busy}
            onChange={(e) => { setConfirm(e.target.value); setFieldError((f) => ({ ...f, confirm: undefined })) }}
          />

          {formError && (
            <div role="alert" className="rounded-lg bg-bad/10 p-3.5 text-[13px] leading-relaxed break-words text-bad">
              {formError}
            </div>
          )}

          <div className="flex gap-2">
            <Button type="button" variant="ghost" lg disabled={busy} onClick={() => { setRedirectToLogin(false); setFormError(''); setStep(0) }} className="shrink-0">
              <ArrowLeft size={14} /> 上一步
            </Button>
            <Button type="submit" lg disabled={busy} className="min-w-0 flex-1">
              {busy && <Spinner size={14} />}
              {busy ? '创建中…' : '创建账号并继续'}
            </Button>
          </div>
        </form>
      )
      )}

      {step === 2 && (
        <div className="space-y-4 text-center">
          <span className="text-ok"><PartyPopper size={22} /></span>
          <div>
            <p className="text-[15px] font-semibold">设置完成</p>
            <p className="mt-1 text-[13px] leading-relaxed break-words text-ink-2">
              管理员账号 <span className="font-mono font-medium text-ink">{createdUser || username}</span> 已创建，
              并且你已经登录。现在只有登录后的成员可以访问平台。
            </p>
          </div>

          {/* 全鉴权会立刻掐断已接入的边缘：账号模式下 edge 的 WS 握手不带租户令牌就被拒
              （internal/server/ws.go），设备随即全部离线，而 server 只留一条 WARN，
              界面上没有任何地方告诉操作员这是怎么回事、怎么恢复。
              这不是故障，是账号模式的既定语义（docs/security.md §5），但向导只报喜不说这一步，
              人就会以为自己刚把部署弄坏了。

              只在**真的有边缘/设备接入过**时才说：全新安装（步骤 1 探到 0 边缘 0 设备）
              没有这个后果，此时插一段警告只是噪音。判据取步骤 1 的 /healthz 快照——
              那时还没进账号模式，边缘能连上，正是「会被断开」的准确信号。 */}
          {hasConnectedFleet && (
            <div className="rounded-lg bg-surface-2 p-3.5 text-left">
              <div className="flex items-start gap-2 text-[12px] leading-relaxed text-ink-2">
                <ShieldAlert size={14} className="mt-0.5 shrink-0 text-warn" />
                <div className="min-w-0">
                  已接入的<span className="font-semibold text-ink">网关现在会被断开</span>：启用账号验证后，网关
                  必须携带 <span className="font-semibold text-ink">「网关」范围的访问令牌</span>。
                  <details className="mt-2">
                    <summary className="cursor-pointer select-none font-medium text-ink-2">
                      查看网关恢复步骤（技术人员）
                    </summary>
                    <p className="mt-1.5">
                      进入「管理 → 访问令牌」创建一个勾选「网关接入」权限的令牌（完整内容只显示一次），
                      填进该网关配置的 <span className="num font-mono">token:</span> 字段，再重新启动网关。
                    </p>
                  </details>
                </div>
              </div>
            </div>
          )}
          <Button lg className="w-full" onClick={() => navigate('/', { replace: true })}>
            进入 CloudPath
          </Button>
        </div>
      )}
    </AuthCard>
  )
}
