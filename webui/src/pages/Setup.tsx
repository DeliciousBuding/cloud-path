// Setup：首次运行向导 —— ① 连通 server ② 管理令牌（可选） ③ 完成。
// 只调用现有只读端点 /healthz 与 lib/api.ts 的令牌契约，不假设新的后端接口。
// 路由约定：/setup（App.tsx 由 FE-UX lane 接线，default export 名固定为 Setup）。
import { useCallback, useEffect, useState } from 'react'
import { Link, useNavigate } from 'react-router'
import { ArrowLeft, ArrowRight, Check, PartyPopper, RefreshCw } from 'lucide-react'
import { AuthCard, Button, Spinner, TextField } from '@/components/ui'
import { api, setToken } from '@/lib/api'
import { cn } from '@/lib/cn'
import type { HealthView } from '@/lib/types'

type Phase = 'checking' | 'ok' | 'fail'

const STEPS = ['连接 server', '管理令牌', '完成']

export default function Setup() {
  const navigate = useNavigate()
  const [step, setStep] = useState(0)

  // 步骤 1：连通性探测
  const [phase, setPhase] = useState<Phase>('checking')
  const [health, setHealth] = useState<HealthView | null>(null)
  const [probeError, setProbeError] = useState('')

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
    }
  }, [])

  useEffect(() => { void probe() }, [probe])

  // 步骤 2：管理令牌（可选，可跳过）
  const [token, setTokenInput] = useState('')

  function saveTokenAndFinish() {
    const v = token.trim()
    if (v) setToken(v)
    setStep(2)
  }

  return (
    <AuthCard
      title="设置 Cloudpath"
      subtitle="三步完成首次配置"
      footer={
        step === 2
          ? <Link to="/login" className="link">重新配置令牌？去登录页</Link>
          : <Link to="/login" className="link">跳过向导，直接登录</Link>
      }
    >
      {/* 步骤指示器：移动端只留圆点，避免 390px 溢出 */}
      <ol className="mb-6 flex items-center" aria-label="设置进度">
        {STEPS.map((label, i) => (
          <li key={label} className={cn('flex items-center', i < STEPS.length - 1 && 'flex-1')}>
            <span
              aria-current={i === step ? 'step' : undefined}
              className={cn(
                'flex h-6 w-6 shrink-0 items-center justify-center rounded-full text-[11px] font-semibold',
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
            <div className="rounded-xl bg-ok/10 p-4">
              <p className="flex items-center gap-2 text-sm font-medium text-ok">
                <Check size={15} strokeWidth={2.5} /> server 已连接
              </p>
              <p className="num mt-1 text-xs text-ink-2">版本 {health.version}</p>
            </div>
          )}
          {phase === 'fail' && (
            <div className="rounded-xl bg-bad/10 p-4">
              <p className="text-sm font-medium text-bad">无法连接 server</p>
              <p className="mt-1 break-words text-xs text-ink-2">{probeError}</p>
              <p className="mt-1 text-xs text-ink-3">请确认中心服务已启动，且本页面与 server 同源。</p>
            </div>
          )}
          <div className="flex gap-2">
            <Button variant="ghost" lg disabled={phase === 'checking'} onClick={() => void probe()} className="shrink-0">
              <RefreshCw size={14} /> 重试
            </Button>
            <Button lg disabled={phase !== 'ok'} onClick={() => setStep(1)} className="min-w-0 flex-1">
              下一步 <ArrowRight size={14} />
            </Button>
          </div>
        </div>
      )}

      {step === 1 && (
        <div className="space-y-4">
          <TextField
            label="管理令牌"
            type="password"
            placeholder="可选：粘贴 server 管理令牌"
            autoComplete="off"
            spellCheck={false}
            hint="令牌用于 API 与实时通道鉴权，可跳过并在登录页填写"
            value={token}
            onChange={(e) => setTokenInput(e.target.value)}
          />
          <div className="flex gap-2">
            <Button variant="ghost" lg onClick={() => setStep(0)} className="shrink-0">
              <ArrowLeft size={14} /> 上一步
            </Button>
            <Button lg onClick={saveTokenAndFinish} className="min-w-0 flex-1">
              保存并继续 <ArrowRight size={14} />
            </Button>
          </div>
        </div>
      )}

      {step === 2 && (
        <div className="space-y-4 text-center">
          <span className="mx-auto flex h-12 w-12 items-center justify-center rounded-full bg-ok/12 text-ok">
            <PartyPopper size={22} />
          </span>
          <div>
            <p className="text-[15px] font-semibold">设置完成</p>
            <p className="mt-1 text-[13px] text-ink-2">
              {token.trim() ? '管理令牌已保存到本机浏览器。' : '尚未配置令牌，可随时在登录页补充。'}
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
