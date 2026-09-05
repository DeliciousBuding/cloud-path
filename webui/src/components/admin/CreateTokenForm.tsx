// 新建服务令牌表单（docs/api.md §3.3 POST /api/tokens）。
// scope 默认最小权限（只勾 read），admin/edge 不预选且带显式风险说明。
// 明文由父组件（TokenManager）在 onCreated 里接管，本组件不落任何持久化通道。
import { useState } from 'react'
import type { FormEvent } from 'react'
import { Button, TextField } from '@/components/ui'
import { ErrorNote } from './ErrorNote'
import { CheckRow, SelectField } from './fields'
import { api } from '@/lib/api'
import {
  adminErrorMessage, DEFAULT_EXPIRY, DEFAULT_SCOPES, EXPIRY_OPTIONS, expiryToUnix, SCOPE_OPTIONS,
} from '@/lib/admin'
import type { CreatedToken, TokenScope } from '@/lib/types'

export function CreateTokenForm({ onCreated, onCancel }: {
  onCreated: (t: CreatedToken) => void
  onCancel: () => void
}) {
  const [name, setName] = useState('')
  const [scopes, setScopes] = useState<TokenScope[]>(DEFAULT_SCOPES)
  const [expiry, setExpiry] = useState(DEFAULT_EXPIRY)
  const [busy, setBusy] = useState(false)
  const [nameErr, setNameErr] = useState('')
  const [scopeErr, setScopeErr] = useState('')
  const [formErr, setFormErr] = useState('')

  const toggle = (s: TokenScope, on: boolean) => {
    setScopes((prev) => (on ? [...prev.filter((x) => x !== s), s] : prev.filter((x) => x !== s)))
    setScopeErr('')
  }

  const submit = async (ev: FormEvent) => {
    ev.preventDefault()
    const n = name.trim()
    setNameErr(n ? '' : '请输入令牌名称')
    if (!n) return
    if (scopes.length === 0) {
      setScopeErr('至少选择一个权限范围（服务端要求 scopes 非空）')
      return
    }
    setScopeErr('')
    setFormErr('')
    setBusy(true)
    try {
      const created = await api.createToken({ name: n, scopes, expires_at: expiryToUnix(expiry) })
      onCreated(created)
    } catch (e) {
      setFormErr(adminErrorMessage(e))
    } finally {
      setBusy(false)
    }
  }

  return (
    <form onSubmit={submit} aria-label="新建服务令牌" className="mb-4 border-b border-hairline pb-4">
      <TextField
        label="令牌名称" value={name} error={nameErr} autoComplete="off"
        hint="用于识别用途，例如「CI 部署」「边缘机房 A」；这里填的是名字，不是令牌明文"
        onChange={(ev) => setName(ev.target.value)}
      />

      <fieldset className="mt-4">
        <legend className="mb-2 text-[13px] font-medium text-ink-2">权限范围（默认最小权限）</legend>
        <div className="space-y-2.5">
          {SCOPE_OPTIONS.map((o) => (
            <CheckRow
              key={o.value}
              label={o.label}
              hint={o.hint}
              tone={o.danger ? 'danger' : 'plain'}
              checked={scopes.includes(o.value)}
              onChange={(on) => toggle(o.value, on)}
            />
          ))}
        </div>
      </fieldset>
      {scopeErr && <ErrorNote className="mt-3" message={scopeErr} />}

      <div className="mt-4">
        <SelectField
          label="有效期" value={expiry} options={EXPIRY_OPTIONS}
          hint="过期后令牌自动失效；也可以随时吊销。默认 30 天以缩小暴露窗口"
          onChange={setExpiry}
        />
      </div>

      {formErr && <ErrorNote className="mt-3" message={formErr} />}

      <div className="mt-4 flex flex-wrap gap-2">
        <Button type="submit" disabled={busy}>{busy ? '创建中…' : '创建令牌'}</Button>
        <Button type="button" variant="ghost" onClick={onCancel}>取消</Button>
      </div>
    </form>
  )
}