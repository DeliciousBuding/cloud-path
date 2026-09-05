// 单个用户行：查看 + 改 name/role/disabled + 重置密码（docs/api.md §3.2 PATCH）。
// 「最后一个 admin」这类规则由 server 判定，前端只把 409 的人话原样展示。
import { useState } from 'react'
import type { FormEvent } from 'react'
import { Badge, Button, KeyValue, TextField } from '@/components/ui'
import { ErrorNote } from './ErrorNote'
import { CheckRow, SelectField } from './fields'
import { useUpdateUser } from '@/hooks/useAdmin'
import { adminErrorMessage, ROLE_OPTIONS, roleOption } from '@/lib/admin'
import { roleLabel } from '@/lib/format'
import { toast } from '@/store/toast'
import type { Role, UserView } from '@/lib/types'

const ROLE_FIELD_OPTIONS = ROLE_OPTIONS.map((r) => ({ value: r.value, label: r.label }))

type Mode = 'idle' | 'edit' | 'reset'

export function UserRow({ user: u }: { user: UserView }) {
  const update = useUpdateUser()
  const [mode, setMode] = useState<Mode>('idle')
  const [name, setName] = useState(u.name)
  const [role, setRole] = useState<Role>(u.role)
  const [disabled, setDisabled] = useState(Boolean(u.disabled))
  const [nameErr, setNameErr] = useState('')
  const [password, setPassword] = useState('')
  const [passwordErr, setPasswordErr] = useState('')
  const [confirmed, setConfirmed] = useState(false)

  const openEdit = () => {
    setName(u.name); setRole(u.role); setDisabled(Boolean(u.disabled)); setNameErr('')
    setMode('edit')
  }
  const openReset = () => {
    setPassword(''); setPasswordErr(''); setConfirmed(false)
    setMode('reset')
  }

  const saveEdit = (ev: FormEvent) => {
    ev.preventDefault()
    const n = name.trim()
    setNameErr(n ? '' : '名称不能为空')
    if (!n) return
    update.mutate(
      { id: u.id, patch: { name: n, role, disabled } },
      { onSuccess: () => { toast.ok('用户已更新', `${u.username} · ${roleLabel(role)}`); setMode('idle') } },
    )
  }

  const saveReset = (ev: FormEvent) => {
    ev.preventDefault()
    setPasswordErr(password ? '' : '请输入新密码')
    if (!password) return
    update.mutate(
      { id: u.id, patch: { password } },
      { onSuccess: () => { toast.ok('密码已重置', `${u.username} 的全部会话已撤销`); setMode('idle') } },
    )
  }

  return (
    <li className="py-4 first:pt-0">
      <div className="flex min-w-0 items-start gap-2">
        {/* 用户名/显示名由管理员填写，长度不可控：必须各自截断 */}
        <div className="min-w-0 flex-1">
          <p className="num truncate text-sm font-semibold" title={u.username}>{u.username}</p>
          {/* 显示名与用户名相同（服务端回落）时不重复占一行 */}
          {u.name && u.name !== u.username && (
            <p className="mt-0.5 truncate text-xs text-ink-2" title={u.name}>{u.name}</p>
          )}
        </div>
        <div className="flex shrink-0 flex-wrap justify-end gap-1.5">
          <Badge tone={u.role === 'admin' ? 'accent' : u.role === 'operator' ? 'ok' : 'idle'}>
            {roleLabel(u.role)}
          </Badge>
          {u.disabled && <Badge tone="bad">已禁用</Badge>}
        </div>
      </div>

      <dl className="mt-3 space-y-2">
        <KeyValue k="用户 ID" v={<span className="font-mono">{u.id}</span>} />
        <KeyValue k="租户" v={u.tenant_slug} mono />
      </dl>

      {mode === 'idle' && (
        <div className="mt-3 flex flex-wrap gap-2 border-t border-hairline pt-3">
          <Button variant="ghost" onClick={openEdit} aria-label={`编辑用户 ${u.username}`}>编辑</Button>
          <Button variant="ghost" onClick={openReset} aria-label={`重置密码：${u.username}`}>重置密码</Button>
        </div>
      )}

      {mode === 'edit' && (
        <form onSubmit={saveEdit} aria-label={`编辑用户 ${u.username}`}
          className="mt-3 space-y-3 border-t border-hairline pt-3">
          <TextField label="名称" value={name} error={nameErr} autoComplete="off"
            onChange={(ev) => setName(ev.target.value)} />
          <SelectField label="角色" value={role} options={ROLE_FIELD_OPTIONS}
            hint={roleOption(role)?.hint} onChange={(v) => setRole(v as Role)} />
          <CheckRow label="禁用该账号" tone="danger" checked={disabled} onChange={setDisabled}
            hint="禁用后立即撤销该用户的全部会话；最后一个可用 admin 不允许被禁用或降级（服务端会拒绝）" />
          {update.isError && <ErrorNote message={adminErrorMessage(update.error)} />}
          <div className="flex flex-wrap gap-2">
            <Button type="submit" disabled={update.isPending} aria-label={`保存用户 ${u.username} 的修改`}>
              {update.isPending ? '保存中…' : '保存修改'}
            </Button>
            <Button type="button" variant="ghost" onClick={() => setMode('idle')}
              aria-label={`取消编辑 ${u.username}`}>取消</Button>
          </div>
        </form>
      )}

      {mode === 'reset' && (
        <form onSubmit={saveReset} aria-label={`重置 ${u.username} 的密码`}
          className="mt-3 space-y-3 border-t border-hairline pt-3">
          <p className="text-xs leading-relaxed text-warn break-words">
            重置后该用户的全部会话会被撤销，必须用新密码重新登录。此操作不可撤销。
          </p>
          <TextField label="新密码" type="password" value={password} error={passwordErr}
            autoComplete="new-password" hint="新密码只发往服务端，不会保存在本机浏览器"
            onChange={(ev) => setPassword(ev.target.value)} />
          <CheckRow label={`确认重置 ${u.username} 的密码`} tone="danger" checked={confirmed}
            onChange={setConfirmed} hint="勾选后「确认重置密码」才可提交" />
          {update.isError && <ErrorNote message={adminErrorMessage(update.error)} />}
          <div className="flex flex-wrap gap-2">
            <Button type="submit" disabled={!confirmed || update.isPending}
              aria-label={`确认重置 ${u.username} 的密码`}>
              {update.isPending ? '重置中…' : '确认重置密码'}
            </Button>
            <Button type="button" variant="ghost" onClick={() => setMode('idle')}
              aria-label={`取消重置 ${u.username}`}>取消</Button>
          </div>
        </form>
      )}
    </li>
  )
}