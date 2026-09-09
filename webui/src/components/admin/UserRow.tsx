// 单个用户行：查看 + 改 name/role/disabled + 重置密码（docs/api.md §3.2 PATCH）。
// 「最后一个 admin」这类规则由 server 判定，前端只把 409 的人话原样展示。
import { useState } from 'react'
import type { FormEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { Badge, Button, KeyValue, TextField } from '@/components/ui'
import { ErrorNote } from './ErrorNote'
import { CheckRow, SelectField } from './fields'
import { useUpdateUser } from '@/hooks/useAdmin'
import { adminErrorMessage, ROLE_OPTIONS } from '@/lib/admin'
import { roleLabel } from '@/lib/format'
import { toast } from '@/store/toast'
import type { Role, UserView } from '@/lib/types'

type Mode = 'idle' | 'edit' | 'reset'

export function UserRow({ user: u }: { user: UserView }) {
  const { t } = useTranslation('admin')
  const update = useUpdateUser()
  const [mode, setMode] = useState<Mode>('idle')
  const [name, setName] = useState(u.name)
  const [role, setRole] = useState<Role>(u.role)
  const [disabled, setDisabled] = useState(Boolean(u.disabled))
  const [password, setPassword] = useState('')
  const [passwordErr, setPasswordErr] = useState('')
  const [confirmed, setConfirmed] = useState(false)
  const roleOptions = ROLE_OPTIONS.map((r) => ({ value: r.value, label: roleLabel(r.value) }))

  const openEdit = () => {
    setName(u.name); setRole(u.role); setDisabled(Boolean(u.disabled))
    setMode('edit')
  }
  const openReset = () => {
    setPassword(''); setPasswordErr(''); setConfirmed(false)
    setMode('reset')
  }

  const saveEdit = (ev: FormEvent) => {
    ev.preventDefault()
    const n = name.trim()
    update.mutate(
      { id: u.id, patch: { role, disabled, ...(n ? { name: n } : {}) } },
      { onSuccess: () => {
        toast.ok(t('userRow.toast.updatedTitle'), t('userRow.toast.updatedMessage', {
          username: u.username,
          role: roleLabel(role),
        }))
        setMode('idle')
      } },
    )
  }

  const saveReset = (ev: FormEvent) => {
    ev.preventDefault()
    setPasswordErr(password ? '' : t('userRow.reset.required'))
    if (!password) return
    update.mutate(
      { id: u.id, patch: { password } },
      { onSuccess: () => {
        toast.ok(t('userRow.toast.resetTitle'), t('userRow.toast.resetMessage', { username: u.username }))
        setMode('idle')
      } },
    )
  }

  return (
    <li className="py-4 first:pt-0">
      <div className="flex min-w-0 items-start gap-2">
        {/* 用户名/显示名由管理员填写，长度不可控：必须各自截断 */}
        <div className="min-w-0 flex-1">
          <p className="truncate text-sm font-semibold" title={u.name || u.username}>{u.name || u.username}</p>
          {u.name && u.name !== u.username && (
            <p className="num mt-0.5 truncate font-mono text-xs text-ink-3" title={u.username}>{u.username}</p>
          )}
        </div>
        <div className="flex shrink-0 flex-wrap justify-end gap-1.5">
          <Badge tone={u.role === 'admin' ? 'accent' : u.role === 'operator' ? 'ok' : 'idle'}>
            {roleLabel(u.role)}
          </Badge>
          {u.disabled && <Badge tone="bad">{t('userRow.disabled')}</Badge>}
        </div>
      </div>

      <details className="mt-3 text-xs text-ink-2">
        <summary className="flex min-h-11 cursor-pointer items-center">{t('userRow.details')}</summary>
        <dl className="mt-2 space-y-2">
          <KeyValue k={t('userRow.fields.id')} v={<span className="font-mono">{u.id}</span>} />
          <KeyValue k={t('userRow.fields.username')} v={u.username} mono />
          <KeyValue k={t('userRow.fields.tenant')} v={u.tenant_slug} mono />
        </dl>
      </details>

      {mode === 'idle' && (
        <div className="mt-3 flex flex-wrap gap-2 border-t border-hairline pt-3">
          <Button variant="ghost" onClick={openEdit} aria-label={t('userRow.actions.editAria', { username: u.username })}>
            {t('userRow.actions.edit')}
          </Button>
          <Button variant="ghost" onClick={openReset} aria-label={t('userRow.actions.resetAria', { username: u.username })}>
            {t('userRow.actions.reset')}
          </Button>
        </div>
      )}

      {mode === 'edit' && (
        <form onSubmit={saveEdit} aria-label={t('userRow.edit.formAria', { username: u.username })}
          className="mt-3 space-y-3 border-t border-hairline pt-3">
          <TextField label={t('userRow.edit.displayName')} value={name} autoComplete="off"
            hint={t('userRow.edit.displayNameHint')} onChange={(ev) => setName(ev.target.value)} />
          <SelectField label={t('userRow.edit.role')} value={role} options={roleOptions}
            hint={t(`roleHints.${role}`)} onChange={(v) => setRole(v as Role)} />
          <CheckRow label={t('userRow.edit.disable')} tone="danger" checked={disabled} onChange={setDisabled}
            hint={t('userRow.edit.disableHint')} />
          {update.isError && <ErrorNote message={adminErrorMessage(update.error)} />}
          <div className="flex flex-wrap gap-2">
            <Button type="submit" disabled={update.isPending} aria-label={t('userRow.actions.saveAria', { username: u.username })}>
              {update.isPending ? t('userRow.actions.saving') : t('userRow.actions.save')}
            </Button>
            <Button type="button" variant="ghost" onClick={() => setMode('idle')}
              aria-label={t('userRow.actions.cancelEditAria', { username: u.username })}>
              {t('actions.cancel')}
            </Button>
          </div>
        </form>
      )}

      {mode === 'reset' && (
        <form onSubmit={saveReset} aria-label={t('userRow.reset.formAria', { username: u.username })}
          className="mt-3 space-y-3 border-t border-hairline pt-3">
          <p className="text-xs leading-relaxed text-warn break-words">
            {t('userRow.reset.warning')}
          </p>
          <TextField label={t('userRow.reset.newPassword')} type="password" value={password} error={passwordErr}
            autoComplete="new-password" hint={t('userRow.reset.newPasswordHint')}
            onChange={(ev) => setPassword(ev.target.value)} />
          <CheckRow label={t('userRow.reset.confirm', { username: u.username })} tone="danger" checked={confirmed}
            onChange={setConfirmed} hint={t('userRow.reset.confirmHint')} />
          {update.isError && <ErrorNote message={adminErrorMessage(update.error)} />}
          <div className="flex flex-wrap gap-2">
            <button type="submit" disabled={!confirmed || update.isPending}
              className="btn btn-danger" aria-label={t('userRow.reset.submitAria', { username: u.username })}>
              {update.isPending ? t('userRow.reset.submitting') : t('userRow.reset.submit')}
            </button>
            <Button type="button" variant="ghost" onClick={() => setMode('idle')}
              aria-label={t('userRow.actions.cancelResetAria', { username: u.username })}>
              {t('actions.cancel')}
            </Button>
          </div>
        </form>
      )}
    </li>
  )
}
