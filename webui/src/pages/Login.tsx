// Login：**账号密码**登录（POST /api/auth/login，docs/api.md §2.2）。
//
// 登录成功的唯一判据是 `api.login` 2xx **并且** `api.me()` 复核通过 ——
// 绝不是「某个公开端点可达」。旧实现把任意字符串塞进 localStorage 再打一次
// 无需鉴权的 /healthz 就跳首页，账号模式下随后所有 /api/* 都会 401，
// 用户看到的是「登录成功了但整站没数据」，即假登录（P0 缺陷 D3）。
//
// 服务令牌（Bearer）是给 API 客户端 / CLI 的合法路径，因此保留为**默认折叠的次要入口**，
// 且它的成功判据同样是 me 复核，而不是 healthz。
// 路由约定：/login（default export 名固定为 Login）。
import { useEffect, useState } from 'react'
import type { FormEvent } from 'react'
import { Link, useNavigate } from 'react-router'
import { ChevronDown, Eye, EyeOff, KeyRound } from 'lucide-react'
import { AuthCard, Button, Spinner, TextField } from '@/components/ui'
import { api, getToken, setToken } from '@/lib/api'
import { loginErrorCopy } from '@/lib/authErrors'
import { confirmSession } from '@/store/auth'
import { toast } from '@/store/toast'
import { cn } from '@/lib/cn'

export default function Login() {
  const navigate = useNavigate()

  // ---- 主路径：账号密码 ----
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [reveal, setReveal] = useState(false)
  const [busy, setBusy] = useState(false)
  const [fieldError, setFieldError] = useState<{ user?: string; pass?: string }>({})
  const [formError, setFormError] = useState('')
  const [cooldown, setCooldown] = useState(0)

  // ---- 次要路径：服务令牌（默认折叠） ----
  const [tokenOpen, setTokenOpen] = useState(false)
  const [token, setTokenInput] = useState('')
  const [tokenError, setTokenError] = useState('')
  const [tokenBusy, setTokenBusy] = useState(false)

  // 429 限流倒计时：只在服务端给了 Retry-After 时才启动，不自己编秒数
  useEffect(() => {
    if (cooldown <= 0) return
    const t = setInterval(() => setCooldown((c) => (c > 0 ? c - 1 : 0)), 1000)
    return () => clearInterval(t)
  }, [cooldown])

  const locked = busy || cooldown > 0

  async function onSubmit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault()
    const u = username.trim()
    const next: { user?: string; pass?: string } = {}
    if (!u) next.user = '请输入用户名'
    if (!password) next.pass = '请输入密码'
    setFieldError(next)
    if (next.user || next.pass) return

    setBusy(true)
    setFormError('')
    try {
      const r = await api.login(u, password)
      // 复核：会话 cookie 是否真的生效（这一步失败就不能算登录成功）
      const user = await confirmSession(r?.user ?? null)
      toast.ok('登录成功', user?.name || user?.username || undefined)
      navigate('/', { replace: true })
    } catch (err) {
      const copy = loginErrorCopy(err)
      setFormError(copy.message)
      if (copy.retryAfter) setCooldown(copy.retryAfter)
      if (copy.badCredentials) {
        // 密码错就清空密码（浏览器密码管理器仍会保留），并把焦点交回用户名
        setPassword('')
      }
      setBusy(false)
    }
  }

  async function onTokenSubmit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault()
    const v = token.trim()
    if (!v) { setTokenError('请输入服务令牌'); return }
    setTokenBusy(true)
    setTokenError('')
    setToken(v)
    try {
      // 令牌模式的判据同样是 me：任意字符串打 healthz 一律不算登录
      const user = await confirmSession()
      toast.ok('令牌已生效', user?.name || user?.username || undefined)
      navigate('/', { replace: true })
    } catch (err) {
      setToken('') // 复核失败即回滚，不在本机留下无效凭据
      const copy = loginErrorCopy(err)
      setTokenError(copy.unreachable
        ? copy.message
        : '令牌被拒绝：无效、已吊销或权限不足（服务令牌需由管理员在「管理 → 服务令牌」中签发）')
      setTokenBusy(false)
    }
  }

  return (
    <AuthCard
      title="登录 Cloudpath"
      subtitle="通用 IoT 设备接入与管理平台"
      footer={<Link to="/setup" className="link">首次部署？运行设置向导</Link>}
    >
      <form onSubmit={onSubmit} noValidate className="space-y-4">
        <TextField
          label="用户名"
          name="username"
          type="text"
          placeholder="你的账号"
          autoComplete="username"
          autoCapitalize="none"
          spellCheck={false}
          autoFocus
          value={username}
          error={fieldError.user}
          disabled={locked}
          onChange={(e) => { setUsername(e.target.value); setFieldError((f) => ({ ...f, user: undefined })) }}
        />
        <TextField
          label="密码"
          name="password"
          type={reveal ? 'text' : 'password'}
          placeholder="你的密码"
          autoComplete="current-password"
          value={password}
          error={fieldError.pass}
          disabled={locked}
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

        {/* 表单级错误：凭据错 / 限流 / 不可达。role=alert 让读屏立即播报 */}
        {formError && (
          <p role="alert" className="rounded-xl bg-bad/10 px-3.5 py-2.5 text-[13px] leading-relaxed break-words text-bad">
            {formError}
          </p>
        )}

        <Button type="submit" lg disabled={locked} className="w-full">
          {busy && <Spinner size={14} />}
          {busy ? '登录中…' : cooldown > 0 ? `请 ${cooldown} 秒后重试` : '登录'}
        </Button>
      </form>

      {/* 次要入口：服务令牌。默认折叠，避免用户把「随便一串字符」当账号密码之外的第二条假登录路径 */}
      <div className="mt-5 border-t border-hairline pt-4">
        <button
          type="button"
          onClick={() => setTokenOpen((v) => !v)}
          aria-expanded={tokenOpen}
          aria-controls="token-signin"
          className="flex w-full items-center gap-1.5 text-xs font-medium text-ink-2 transition-colors hover:text-ink"
        >
          <KeyRound size={13} className="shrink-0" />
          使用服务令牌登录（API / 机器客户端）
          <ChevronDown size={13} className={cn('ml-auto shrink-0 transition-transform', tokenOpen && 'rotate-180')} />
        </button>

        {tokenOpen && (
          <form id="token-signin" onSubmit={onTokenSubmit} noValidate className="mt-3.5 space-y-3 fade-up">
            <TextField
              label="服务令牌"
              type="password"
              placeholder="粘贴管理员签发的令牌"
              autoComplete="off"
              spellCheck={false}
              value={token}
              error={tokenError}
              hint="令牌仅保存在本机浏览器，用于接口与实时通道鉴权；明文不会被再次显示"
              disabled={tokenBusy}
              onChange={(e) => { setTokenInput(e.target.value); setTokenError('') }}
            />
            <Button type="submit" variant="ghost" lg disabled={tokenBusy} className="w-full">
              {tokenBusy && <Spinner size={14} />}
              {tokenBusy ? '校验中…' : '用令牌登录'}
            </Button>
            {getToken() && (
              <p className="text-[12px] text-ink-3">
                本机已保存一个令牌；提交新令牌会覆盖它。
              </p>
            )}
          </form>
        )}
      </div>
    </AuthCard>
  )
}