// 新建用户表单（docs/api.md §3.2 POST /api/users）。
// 角色默认最小权限（viewer），admin 需要显式选择；错误一律展示服务端人话，不做本地伪判。
import { useState } from 'react'
import type { FormEvent } from 'react'
import { Button, TextField } from '@/components/ui'
import { ErrorNote } from './ErrorNote'
import { SelectField } from './fields'
import { useCreateUser } from '@/hooks/useAdmin'
import { adminErrorMessage, DEFAULT_ROLE, ROLE_OPTIONS } from '@/lib/admin'
import { roleLabel } from '@/lib/format'
import { toast } from '@/store/toast'
import type { Role } from '@/lib/types'

const ROLE_FIELD_OPTIONS = ROLE_OPTIONS.map((r) => ({ value: r.value, label: r.label }))
const ROLE_HINTS: Record<Role, string> = {
  viewer: '只能查看设备状态和记录。',
  operator: '可以查看并执行设备操作。',
  admin: '可以管理成员和访问令牌。',
}

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
    setUsernameErr(u ? '' : '请输入登录账号')
    setPasswordErr(password ? '' : '请输入初始密码')
    if (!u || !password) bad = true
    if (bad) return
    create.mutate(
      { username: u, role, password, ...(name.trim() ? { name: name.trim() } : {}) },
      {
        onSuccess: (r) => {
          toast.ok('成员已添加', `${r.user.username} · ${roleLabel(r.user.role)}`)
          onDone()
        },
      },
    )
  }

  return (
    <form onSubmit={submit} aria-label="添加成员" className="mb-4 border-b border-hairline pb-4">
      <div className="grid gap-3 sm:grid-cols-2">
        <TextField
          label="登录账号" value={username} error={usernameErr} autoComplete="off"
          hint="成员登录时使用；本组织内不能重复"
          onChange={(ev) => setUsername(ev.target.value)}
        />
        <TextField
          label="显示名称" value={name} autoComplete="off"
          hint="留空则显示登录账号"
          onChange={(ev) => setName(ev.target.value)}
        />
        <TextField
          label="初始密码" type="password" value={password} error={passwordErr} autoComplete="new-password"
          hint="成员第一次登录时使用，之后建议尽快修改"
          onChange={(ev) => setPassword(ev.target.value)}
        />
        <SelectField
          label="角色" value={role} options={ROLE_FIELD_OPTIONS}
          hint={ROLE_HINTS[role]}
          onChange={(v) => setRole(v as Role)}
        />
      </div>
      {create.isError && (
        <ErrorNote className="mt-3" message={adminErrorMessage(create.error)} />
      )}
      <div className="mt-3 flex flex-wrap gap-2">
        <Button type="submit" disabled={create.isPending}>
          {create.isPending ? '添加中…' : '添加成员'}
        </Button>
        <Button type="button" variant="ghost" onClick={onDone}>取消</Button>
      </div>
    </form>
  )
}