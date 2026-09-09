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
import { Trans, useTranslation } from 'react-i18next'
import {
  ArrowLeft, ArrowRight, Check, Eye, EyeOff, LogIn, PartyPopper, RefreshCw, ShieldAlert,
} from 'lucide-react'
import '@/i18n'
import { AuthCard, Button, Spinner, TextField } from '@/components/ui'
import { ApiError, api } from '@/lib/api'
import { setupErrorCopy } from '@/lib/authErrors'
import { confirmSession } from '@/store/auth'
import { cn } from '@/lib/cn'
import type { HealthView } from '@/lib/types'
import { usePageTitle } from '@/hooks/usePageTitle'

type Phase = 'checking' | 'ok' | 'fail'

const STEPS = [
  { label: 'setup.steps.connect', short: 'setup.steps.connectShort' },
  { label: 'setup.steps.createAdmin', short: 'setup.steps.createShort' },
  { label: 'setup.steps.complete', short: 'setup.steps.completeShort' },
]

/** 与服务端一致的上限（本地先拦一次，省一个来回；最终判定仍在服务端） */
const MAX_USERNAME = 64
const MAX_PASSWORD = 256

export default function Setup() {
  const { t } = useTranslation('auth')
  usePageTitle(t('pageTitle.setup'))

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
      setProbeError(t('setup.probe.error'))
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
   * 所以这能提示：启用账号验证后，未携带有效网关令牌的已接入网关会断开；已配置令牌的共享网关不受影响。
   * 全新安装为 false，完成页不插这段与本实例无关的警告。
   */
  const hasConnectedFleet = (health?.edges_online ?? 0) > 0 || (health?.devices_online ?? 0) > 0

  function setupErrorMessage(err: unknown, copy: ReturnType<typeof setupErrorCopy>): string {
    if (!(err instanceof ApiError)) return t('setup.errors.network')
    switch (err.status) {
      case 403: return t('setup.errors.forbidden')
      case 409: return t('setup.errors.alreadySetup')
      case 400: return t('setup.errors.badRequest')
      case 429: return copy.retryAfter
        ? t('setup.errors.rateLimitedAfter', { seconds: copy.retryAfter })
        : t('setup.errors.rateLimited')
      case 503: return t('setup.errors.unavailable')
      default: return t('setup.errors.failed')
    }
  }

  async function onCreate(e: FormEvent<HTMLFormElement>) {
    e.preventDefault()
    const u = username.trim()
    const next: typeof fieldError = {}
    if (!u) next.user = t('setup.create.validation.usernameRequired')
    else if (u.length > MAX_USERNAME) next.user = t('setup.create.validation.usernameTooLong', { max: MAX_USERNAME })
    if (!password) next.pass = t('setup.create.validation.passwordRequired')
    else if (password.length > MAX_PASSWORD) next.pass = t('setup.create.validation.passwordTooLong', { max: MAX_PASSWORD })
    if (password !== confirm) next.confirm = t('setup.create.validation.passwordMismatch')
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
        setFormError(t('setup.sessionNotEstablished'))
        setRedirectToLogin(true)
      } else {
        const copy = setupErrorCopy(err)
        setFormError(setupErrorMessage(err, copy))
        if (copy.alreadySetup) setRedirectToLogin(true)
      }
      setBusy(false)
    }
  }

  return (
    <AuthCard
      title={t('setup.title')}
      subtitle={t('setup.subtitle')}
      footer={
        step === 2
          ? <Link to="/login" className="link">{t('setup.footer.switchAccount')}</Link>
          : <Link to="/login" className="link">{t('setup.footer.existingAccount')}</Link>
      }
    >
      {/* 步骤指示器：移动端用短标签，避免 390px 溢出 */}
      <ol className="mb-6 flex items-center" aria-label={t('setup.progressAria')}>
        {STEPS.map((item, i) => (
          <li key={t(item.label)} className={cn('flex items-center', i < STEPS.length - 1 && 'flex-1')}>
            <span
              aria-current={i === step ? 'step' : undefined}
              className={cn(
                'flex h-6 w-6 shrink-0 items-center justify-center rounded-pill text-meta font-semibold',
                i < step ? 'bg-accent text-accent-ink'
                  : i === step ? 'bg-accent/15 text-accent'
                    : 'bg-ink-3/12 text-ink-3',
              )}
            >
              {i < step ? <Check size={13} strokeWidth={2.5} /> : i + 1}
            </span>
            <span className={cn(
              'ml-2 text-meta sm:hidden',
              i === step ? 'font-medium text-ink' : 'text-ink-3',
            )}>
              {t(item.short)}
            </span>
            <span className={cn(
              'ml-2 hidden text-meta sm:block',
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
            <p className="flex items-center gap-2 text-body text-ink-2">
              <Spinner /> {t('setup.probe.checking')}
            </p>
          )}
          {phase === 'ok' && health && (
            <div className="rounded-tile bg-ok/10 p-4">
              <p className="flex items-center gap-2 text-body font-medium text-ok">
                <Check size={15} strokeWidth={2.5} /> {t('setup.probe.ready')}
              </p>
              <p className="mt-1 text-meta break-words text-ink-2">
                <Trans i18nKey="setup.probe.version" ns="auth" values={{ version: health.version }} components={{ version: <span className="font-mono" /> }} />
              </p>
              {alreadyIn && (
                <p className="mt-2 text-meta leading-relaxed text-ink-2">
                  {t('setup.probe.alreadyIn')}
                </p>
              )}
            </div>
          )}
          {phase === 'fail' && (
            <div className="rounded-tile bg-bad/10 p-4">
              <p className="text-body font-medium text-bad">{t('setup.probe.failedTitle')}</p>
              <p className="mt-1 break-words text-meta text-ink-2">{probeError}</p>
              <p className="mt-1 text-meta text-ink-3">{t('setup.probe.failedHint')}</p>
            </div>
          )}
          <div className="flex gap-2">
            <Button variant="ghost" lg disabled={phase === 'checking'} onClick={() => void probe()} className="shrink-0">
              <RefreshCw size={14} /> {phase === 'checking' ? t('setup.probe.checkingAction') : phase === 'ok' ? t('setup.probe.recheck') : t('setup.probe.retry')}
            </Button>
            {alreadyIn ? (
              <Button lg onClick={() => navigate('/', { replace: true })} className="min-w-0 flex-1">
                {t('setup.probe.enter')} <ArrowRight size={14} />
              </Button>
            ) : (
              <Button lg disabled={phase !== 'ok'} onClick={() => setStep(1)} className="min-w-0 flex-1">
                {t('setup.probe.next')} <ArrowRight size={14} />
              </Button>
            )}
          </div>
        </div>
      )}

      {step === 1 && (
        redirectToLogin ? (
          <div className="space-y-4">
            <div role="alert" className="rounded-tile bg-warn/12 p-3.5 text-compact leading-relaxed break-words text-warn">
              {formError}
              <Link to="/login" className="link mt-2 flex items-center gap-1 text-compact">
                <LogIn size={13} /> {t('setup.create.goLogin')}
              </Link>
            </div>
            <div className="flex gap-2">
              <Button type="button" variant="ghost" lg onClick={() => { setRedirectToLogin(false); setFormError(''); setStep(0) }} className="shrink-0">
                <ArrowLeft size={14} /> {t('setup.create.back')}
              </Button>
              <Button type="button" lg className="min-w-0 flex-1" onClick={() => navigate('/login', { replace: true })}>
                {t('setup.create.goLogin')} <ArrowRight size={14} />
              </Button>
            </div>
          </div>
        ) : (
        <form onSubmit={onCreate} noValidate className="space-y-4">
          <div className="rounded-tile bg-surface-2 p-3.5">
            <div className="flex items-start gap-2">
              <ShieldAlert size={14} className="mt-0.5 shrink-0 text-warn" />
              <div className="min-w-0 text-meta leading-relaxed text-ink-2">
                <p><Trans i18nKey="setup.create.intro" ns="auth" components={{ strong: <span className="font-semibold text-ink" /> }} /></p>
                <ul className="mt-2 list-disc space-y-1 pl-4">
                  <li>{t('setup.create.onlyMembers')}</li>
                  <li>{t('setup.create.addMembers')}</li>
                </ul>
                <p className="mt-2">{t('setup.create.contactAdmin')}</p>
              </div>
            </div>
          </div>

          <TextField
            label={t('setup.create.username.label')}
            name="username"
            type="text"
            placeholder={t('setup.create.username.placeholder')}
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
            label={t('setup.create.password.label')}
            name="password"
            type={reveal ? 'text' : 'password'}
            placeholder={t('setup.create.password.placeholder')}
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
                aria-label={reveal ? t('login.fields.password.hide') : t('login.fields.password.show')}
                title={reveal ? t('login.fields.password.hide') : t('login.fields.password.show')}
                aria-pressed={reveal}
                className="flex h-7 w-7 items-center justify-center rounded-pill text-ink-3 transition-colors hover:text-ink"
              >
                {reveal ? <EyeOff size={15} /> : <Eye size={15} />}
              </button>
            }
          />
          <TextField
            label={t('setup.create.confirm.label')}
            name="confirm-password"
            type={reveal ? 'text' : 'password'}
            placeholder={t('setup.create.confirm.placeholder')}
            autoComplete="new-password"
            maxLength={MAX_PASSWORD}
            value={confirm}
            error={fieldError.confirm}
            disabled={busy}
            onChange={(e) => { setConfirm(e.target.value); setFieldError((f) => ({ ...f, confirm: undefined })) }}
          />

          {formError && (
            <div role="alert" className="rounded-tile bg-bad/10 p-3.5 text-compact leading-relaxed break-words text-bad">
              {formError}
            </div>
          )}

          <div className="flex gap-2">
            <Button type="button" variant="ghost" lg disabled={busy} onClick={() => { setRedirectToLogin(false); setFormError(''); setStep(0) }} className="shrink-0">
              <ArrowLeft size={14} /> {t('setup.create.back')}
            </Button>
            <Button type="submit" lg disabled={busy} className="min-w-0 flex-1">
              {busy && <Spinner size={14} />}
              {busy ? t('setup.create.submitting') : t('setup.create.submit')}
            </Button>
          </div>
        </form>
      )
      )}

      {step === 2 && (
        <div className="space-y-4 text-center">
          <span className="text-ok"><PartyPopper size={22} /></span>
          <div>
            <p className="text-lead font-semibold">{t('setup.complete.title')}</p>
            <p className="mt-1 text-compact leading-relaxed break-words text-ink-2">
              <Trans i18nKey="setup.complete.account" ns="auth" values={{ username: createdUser || username }} components={{ name: <span className="font-mono font-medium text-ink" /> }} />
            </p>
          </div>

          {/* 全鉴权会掐断未携带有效令牌的边缘：账号模式下 edge 的 WS 握手不带租户令牌就被拒
              （internal/server/ws.go），设备随即全部离线，而 server 只留一条 WARN，
              界面上没有任何地方告诉操作员这是怎么回事、怎么恢复。
              这不是故障，是账号模式的既定语义（docs/security.md §5），但向导只报喜不说这一步，
              人就会以为自己刚把部署弄坏了。

              只在**真的有边缘/设备接入过**时才说：全新安装（步骤 1 探到 0 边缘 0 设备）
              没有这个后果，此时插一段警告只是噪音。判据取步骤 1 的 /healthz 快照——
              那时还没进账号模式，边缘能连上；未携带有效令牌的连接会在启用账号验证后断开。 */}
          {hasConnectedFleet && (
            <div className="rounded-tile bg-surface-2 p-3.5 text-left">
              <div className="flex items-start gap-2 text-meta leading-relaxed text-ink-2">
                <ShieldAlert size={14} className="mt-0.5 shrink-0 text-warn" />
                <div className="min-w-0">
                  <Trans i18nKey="setup.complete.fleetWarning" ns="auth" components={{ strong: <span className="font-semibold text-ink" /> }} />
                  <details className="mt-2">
                    <summary className="cursor-pointer select-none font-medium text-ink-2">
                      {t('setup.complete.fleetDetails')}
                    </summary>
                    <p className="mt-1.5">
                      <Trans i18nKey="setup.complete.fleetRecovery" ns="auth" components={{ code: <span className="num font-mono" /> }} />
                    </p>
                  </details>
                </div>
              </div>
            </div>
          )}
          <Button lg className="w-full" onClick={() => navigate('/', { replace: true })}>
            {t('setup.complete.enter')}
          </Button>
        </div>
      )}
    </AuthCard>
  )
}
