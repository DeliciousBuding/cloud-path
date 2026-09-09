// 新建用户表单（docs/api.md §3.2 POST /api/users）。
// 角色默认最小权限（viewer），admin 需要显式选择；错误一律展示服务端人话，不做本地伪判。
import { useState } from 'react'
import type { FormEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { Button, TextField } from '@/components/ui'
import { ErrorNote } from './ErrorNote'
import { SelectField } from './fields'
import { useCreateUser } from '@/hooks/useAdmin'
import { adminErrorMessage, DEFAULT_ROLE, ROLE_OPTIONS } from '@/lib/admin'
import { roleLabel } from '@/lib/format'
import { toast } from '@/store/toast'
import type { Role } from '@/lib/types'

export function CreateUserForm({ onDone }: { onDone: () => void }) {
  const { t } = useTranslation('admin')
  const create = useCreateUser()
  const [username, setUsername] = useState('')
  const [name, setName] = useState('')
  const [role, setRole] = useState<Role>(DEFAULT_ROLE)
  const [password, setPassword] = useState('')
  const [usernameErr, setUsernameErr] = useState('')
  const [passwordErr, setPasswordErr] = useState('')
  const roleOptions = ROLE_OPTIONS.map((r) => ({ value: r.value, label: roleLabel(r.value) }))

  const submit = (ev: FormEvent) => {
    ev.preventDefault()
    const u = username.trim()
    let bad = false
    setUsernameErr(u ? '' : t('createUser.username.required'))
    setPasswordErr(password ? '' : t('createUser.password.required'))
    if (!u || !password) bad = true
    if (bad) return
    create.mutate(
      { username: u, role, password, ...(name.trim() ? { name: name.trim() } : {}) },
      {
        onSuccess: (r) => {
          toast.ok(t('createUser.toast.successTitle'), t('createUser.toast.successMessage', {
            username: r.user.username,
            role: roleLabel(r.user.role),
          }))
          onDone()
        },
      },
    )
  }

  return (
    <form onSubmit={submit} aria-label={t('createUser.formAria')} className="mb-4 border-b border-hairline pb-4">
      <div className="grid gap-3 sm:grid-cols-2">
        <TextField
          label={t('createUser.username.label')} value={username} error={usernameErr} autoComplete="off"
          hint={t('createUser.username.hint')}
          onChange={(ev) => setUsername(ev.target.value)}
        />
        <TextField
          label={t('createUser.name.label')} value={name} autoComplete="off"
          hint={t('createUser.name.hint')}
          onChange={(ev) => setName(ev.target.value)}
        />
        <TextField
          label={t('createUser.password.label')} type="password" value={password} error={passwordErr} autoComplete="new-password"
          hint={t('createUser.password.hint')}
          onChange={(ev) => setPassword(ev.target.value)}
        />
        <SelectField
          label={t('createUser.role.label')} value={role} options={roleOptions}
          hint={t(`roleHints.${role}`)}
          onChange={(v) => setRole(v as Role)}
        />
      </div>
      {create.isError && (
        <ErrorNote className="mt-3" message={adminErrorMessage(create.error)} />
      )}
      <div className="mt-3 flex flex-wrap gap-2">
        <Button type="submit" disabled={create.isPending}>
          {create.isPending ? t('createUser.submitting') : t('createUser.submit')}
        </Button>
        <Button type="button" variant="ghost" onClick={onDone}>{t('actions.cancel')}</Button>
      </div>
    </form>
  )
}
