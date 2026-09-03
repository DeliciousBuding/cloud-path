// Login：按现有契约保存访问令牌（lib/api.ts 的 TOKEN_KEY）并校验 server 连通性。
// 后端暂未提供专用认证端点；就绪后只需替换 onSubmit 内的校验调用，页面结构不变。
// 路由约定：/login（App.tsx 由 FE-UX lane 接线，default export 名固定为 Login）。
import { useState } from 'react'
import type { FormEvent } from 'react'
import { Link, useNavigate } from 'react-router'
import { Eye, EyeOff } from 'lucide-react'
import { AuthCard, Button, Spinner, TextField } from '@/components/ui'
import { api, getToken, setToken } from '@/lib/api'

export default function Login() {
  const navigate = useNavigate()
  const [token, setTokenInput] = useState(getToken)
  const [reveal, setReveal] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  async function onSubmit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault()
    const v = token.trim()
    if (!v) { setError('请输入访问令牌'); return }
    setBusy(true)
    setError('')
    setToken(v)
    try {
      await api.health()
      navigate('/', { replace: true })
    } catch (err) {
      setToken('') // 校验失败回滚，避免留下无效令牌
      setError(err instanceof Error ? err.message : '无法连接 server，请重试')
      setBusy(false)
    }
  }

  return (
    <AuthCard
      title="登录 Cloudpath"
      subtitle="设备无关的 IoT 接入与管理平台"
      footer={<Link to="/setup" className="link">首次部署？运行设置向导</Link>}
    >
      <form onSubmit={onSubmit} noValidate className="space-y-4">
        <TextField
          label="访问令牌"
          type={reveal ? 'text' : 'password'}
          placeholder="粘贴 server 签发的令牌"
          autoComplete="off"
          spellCheck={false}
          value={token}
          error={error}
          hint="令牌仅保存在本机浏览器，用于 API 与实时通道鉴权"
          onChange={(e) => { setTokenInput(e.target.value); setError('') }}
        />
        <div className="flex items-stretch gap-2">
          <Button type="submit" lg disabled={busy} className="min-w-0 flex-1">
            {busy && <Spinner size={14} />}
            {busy ? '验证中…' : '登录'}
          </Button>
          <Button
            type="button"
            variant="ghost"
            lg
            onClick={() => setReveal(!reveal)}
            aria-label={reveal ? '隐藏令牌' : '显示令牌'}
            title={reveal ? '隐藏令牌' : '显示令牌'}
            className="shrink-0 px-3.5"
          >
            {reveal ? <EyeOff size={15} /> : <Eye size={15} />}
          </Button>
        </div>
      </form>
    </AuthCard>
  )
}
