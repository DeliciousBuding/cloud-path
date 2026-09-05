// 新建用户表单（docs/api.md §3.2 POST /api/users）。
// 角色默认最小权限（viewer），admin 需要显式选择；错误一律展示服务端人话，不做本地伪判。
import { useState } from 'react'
import type { FormEvent } from 'react'
import { Button, TextField } from '@/components/ui'
import { ErrorNote } from './ErrorNote'
import { SelectField } from './fields'
import { useCreateUser } from '@/hooks/useAdmin'
import { adminErrorMessage, DEFAULT_ROLE, ROLE_OPTIONS, roleOption } from '@/lib/admin'
import { roleLabel } from '@/lib/format'
import { toast } from '@/store/toast'
import type { Role } from '@/lib/types'

const ROLE_FIELD_OPTIONS = ROLE_OPTIONS.map((r) => ({ value: r.value, label: r.label }))

export function CreateUserForm({ onDone }: { onDone: () => void }) {
  const create = useCreateUser()
  const [username, setUsername] = useState('')
  const [name, setName] = useState('')
  const [role, setRole] = useState<Role>(DEFAULT_ROLE)
  const [password, setPassword] = useState('')
  const [usernameErr, setUsernameErr] = useState('')
  const [passwordErr, setPasswordErr] = useState('')

  const submit = (ev: FormEvent) => {
    ev.preventDefault()
    const u = username.trim()
    let bad = false
    setUsernameErr(u ? '' : '请输入用户名')
    setPasswordErr(password ? '' : '请输入初始密码')
    if (!u || !password) bad = true
    if (bad) return
    create.mutate(
      { username: u, role, password, ...(name.trim() ? { name: name.trim() } : {}) },
      {
        onSuccess: (r) => {
          toast.ok('用户已创建', `${r.user.username} · ${roleLabel(r.user.role)}`)
          onDone()
        },
      },
    )
  }

  return (
    <form onSubmit={submit} aria-label="新建用户" className="mb-4 border-b border-hairline pb-4">
      <div className="grid gap-3 sm:grid-cols-2">
        <TextField
          label="用户名" value={username} error={usernameErr} autoComplete="off"
          hint="登录名，租户内唯一"
          onChange={(ev) => setUsername(ev.target.value)}
        />
        <TextField
          label="名称" value={name} autoComplete="off"
          hint="显示名；留空则与用户名相同"
          onChange={(ev) => setName(ev.target.value)}
        />
        <TextField
          label="密码" type="password" value={password} error={passwordErr} autoComplete="new-password"
          hint="初始密码，创建后建议让用户自行修改"
          onChange={(ev) => setPassword(ev.target.value)}
        />
        <SelectField
          label="角色" value={role} options={ROLE_FIELD_OPTIONS}
          hint={roleOption(role)?.hint}
          onChange={(v) => setRole(v as Role)}
        />
      </div>
      {create.isError && (
        <ErrorNote className="mt-3" message={adminErrorMessage(create.error)} />
      )}
      <div className="mt-3 flex flex-wrap gap-2">
        <Button type="submit" disabled={create.isPending}>
          {create.isPending ? '创建中…' : '创建用户'}
        </Button>
        <Button type="button" variant="ghost" onClick={onDone}>取消</Button>
      </div>
    </form>
  )
}