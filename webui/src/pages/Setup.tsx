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
import { setupErrorCopy } from '@/lib/authErrors'
import { confirmSession } from '@/store/auth'
import { cn } from '@/lib/cn'
import type { HealthView } from '@/lib/types'
import { usePageTitle } from '@/hooks/usePageTitle'

type Phase = 'checking' | 'ok' | 'fail'

const STEPS = ['连接 server', '创建管理员账号', '完成']

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
    } catch (err) {
      setProbeError(err instanceof Error ? err.message : '无法连接 server')
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
    try {
      const r = await api.setup(u, password)
      const user = await confirmSession(r?.user ?? null)
      setCreatedUser(user?.username || u)
      setStep(2)
    } catch (err) {
      const copy = setupErrorCopy(err)
      setFormError(copy.message)
      if (copy.alreadySetup) setRedirectToLogin(true)
      setBusy(false)
    }
  }

  return (
    <AuthCard
      title="设置 Cloudpath"
      subtitle="三步完成首次配置"
      footer={
        step === 2
          ? <Link to="/login" className="link">换个账号？去登录页</Link>
          : <Link to="/login" className="link">已有账号？直接登录</Link>
      }
    >
      {/* 步骤指示器：移动端只留圆点，避免 390px 溢出 */}
      <ol className="mb-6 flex items-center" aria-label="设置进度">
        {STEPS.map((label, i) => (
          <li key={label} className={cn('flex items-center', i < STEPS.length - 1 && 'flex-1')}>
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
              'ml-2 hidden text-xs sm:block',
              i === step ? 'font-medium text-ink' : 'text-ink-3',
            )}>
              {label}
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
              <Spinner /> 正在检测 server 连接…
            </p>
          )}
          {phase === 'ok' && health && (
            <div className="rounded-lg bg-ok/10 p-4">
              <p className="flex items-center gap-2 text-sm font-medium text-ok">
                <Check size={15} strokeWidth={2.5} /> server 已连接
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
              <p className="text-sm font-medium text-bad">无法连接 server</p>
              <p className="mt-1 break-words text-xs text-ink-2">{probeError}</p>
              <p className="mt-1 text-xs text-ink-3">请确认中心服务已启动，且本页面与 server 同源。</p>
            </div>
          )}
          <div className="flex gap-2">
            <Button variant="ghost" lg disabled={phase === 'checking'} onClick={() => void probe()} className="shrink-0">
              <RefreshCw size={14} /> 重试
            </Button>
            {alreadyIn ? (
              <Button lg onClick={() => navigate('/', { replace: true })} className="min-w-0 flex-1">
                进入管理台 <ArrowRight size={14} />
              </Button>
            ) : (
              <Button lg disabled={phase !== 'ok'} onClick={() => setStep(1)} className="min-w-0 flex-1">
                下一步 <ArrowRight size={14} />
              </Button>
            )}
          </div>
        </div>
      )}

      {step === 1 && (
        <form onSubmit={onCreate} noValidate className="space-y-4">
          <div className="rounded-lg bg-surface-2 p-3.5">
            <p className="flex items-start gap-2 text-[12px] leading-relaxed text-ink-2">
              <ShieldAlert size={14} className="mt-0.5 shrink-0 text-warn" />
              <span>
                这里创建的是<span className="font-semibold text-ink">首个管理员账号</span>，创建成功后实例立即进入全鉴权模式，其他账号需由管理员在
                「管理 → 用户」中创建。首次设置只允许从服务器本机（回环地址）进行，或携带一次性 setup token。
              </span>
            </p>
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
            <div role="alert"
              className={cn('rounded-lg p-3.5 text-[13px] leading-relaxed break-words',
                redirectToLogin ? 'bg-warn/12 text-warn' : 'bg-bad/10 text-bad')}>
              {formError}
              {redirectToLogin && (
                <Link to="/login" className="link mt-2 flex items-center gap-1 text-[13px]">
                  <LogIn size={13} /> 去登录页
                </Link>
              )}
            </div>
          )}

          <div className="flex gap-2">
            <Button type="button" variant="ghost" lg disabled={busy} onClick={() => setStep(0)} className="shrink-0">
              <ArrowLeft size={14} /> 上一步
            </Button>
            <Button type="submit" lg disabled={busy} className="min-w-0 flex-1">
              {busy && <Spinner size={14} />}
              {busy ? '创建中…' : '创建账号并继续'}
            </Button>
          </div>
        </form>
      )}

      {step === 2 && (
        <div className="space-y-4 text-center">
          <span className="text-ok"><PartyPopper size={22} /></span>
          <div>
            <p className="text-[15px] font-semibold">设置完成</p>
            <p className="mt-1 text-[13px] leading-relaxed break-words text-ink-2">
              管理员账号 <span className="font-mono font-medium text-ink">{createdUser || username}</span> 已创建，
              并且你已经登录。实例现在处于全鉴权模式。
            </p>
          </div>
          <Button lg className="w-full" onClick={() => navigate('/', { replace: true })}>
            进入管理台
          </Button>
        </div>
      )}
    </AuthCard>
  )
}