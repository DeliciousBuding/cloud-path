// 新建服务令牌表单（docs/api.md §3.3 POST /api/tokens）。
// scope 默认最小权限（只勾 read），admin/edge 不预选且带显式风险说明。
// 明文由父组件（TokenManager）在 onCreated 里接管，本组件不落任何持久化通道。
import { useState } from 'react'
import type { FormEvent } from 'react'
import { useTranslation } from 'react-i18next'
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
  const { t } = useTranslation('admin')
  const [name, setName] = useState('')
  const [scopes, setScopes] = useState<TokenScope[]>(DEFAULT_SCOPES)
  const [expiry, setExpiry] = useState(DEFAULT_EXPIRY)
  const [busy, setBusy] = useState(false)
  const [nameErr, setNameErr] = useState('')
  const [scopeErr, setScopeErr] = useState('')
  const [formErr, setFormErr] = useState('')
  const expiryOptions = EXPIRY_OPTIONS.map((o) => ({
    value: o.value,
    label: t(`createToken.expiry.options.${o.value}`),
  }))

  const toggle = (s: TokenScope, on: boolean) => {
    setScopes((prev) => (on ? [...prev.filter((x) => x !== s), s] : prev.filter((x) => x !== s)))
    setScopeErr('')
  }

  const submit = async (ev: FormEvent) => {
    ev.preventDefault()
    const n = name.trim()
    setNameErr(n ? '' : t('createToken.name.required'))
    if (!n) return
    if (scopes.length === 0) {
      setScopeErr(t('createToken.scopes.required'))
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
    <form onSubmit={submit} aria-label={t('createToken.formAria')} className="mb-4 border-b border-hairline pb-4">
      <TextField
        label={t('createToken.name.label')} value={name} error={nameErr} autoComplete="off"
        hint={t('createToken.name.hint')}
        onChange={(ev) => setName(ev.target.value)}
      />

      <fieldset className="mt-4">
        <legend className="mb-2 text-[13px] font-medium text-ink-2">{t('createToken.scopes.legend')}</legend>
        <div className="space-y-2.5">
          {SCOPE_OPTIONS.map((o) => (
            <CheckRow
              key={o.value}
              label={t(`createToken.scopes.options.${o.value}.label`)}
              hint={t(`createToken.scopes.options.${o.value}.hint`)}
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
          label={t('createToken.expiry.label')} value={expiry} options={expiryOptions}
          hint={t('createToken.expiry.hint')}
          onChange={setExpiry}
        />
      </div>

      {formErr && <ErrorNote className="mt-3" message={formErr} />}

      <div className="mt-4 flex flex-wrap gap-2">
        <Button type="submit" disabled={busy}>{busy ? t('createToken.submitting') : t('createToken.submit')}</Button>
        <Button type="button" variant="ghost" onClick={onCancel}>{t('actions.cancel')}</Button>
      </div>
    </form>
  )
}
