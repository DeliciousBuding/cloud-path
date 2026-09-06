import { useCallback, useEffect, useRef, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { Loader2 } from 'lucide-react'
import { api } from '@/lib/api'
import { useAuth } from '@/store/auth'
import { useLive } from '@/store/ws'
import { toast } from '@/store/toast'
import { cn } from '@/lib/cn'
import { ConfirmDialog } from './ConfirmDialog'
import { commandScope } from './command/scope'
import { commandArgsError } from '@/lib/command-schema'
import { commandErrorCopy } from '@/lib/format'
import type { CommandAction } from '@/lib/descriptor'

const ACK_TIMEOUT_MS = 15000

interface CommandButtonProps {
  /** "<edge>/<dev>" */
  deviceId: string
  action: CommandAction
  /** 保留受控参数原文；undefined 表示不带 args 下发。 */
  args?: string
  className?: string
  disabled?: boolean
}

/** 独立使用按钮也受同一权限边界保护；身份/设备/声明变化会卸载旧确认与回执状态。 */
export function CommandButton(props: CommandButtonProps) {
  const scope = useAuth((s) => commandScope(s, props.deviceId))
  if (!scope) return null
  return <ScopedCommandButton key={JSON.stringify([scope, props.action])} {...props} scope={scope} />
}

/** POST → WS ACK → 历史刷新/超时；危险确认只取声明，不认识设备或具体命令名。 */
function ScopedCommandButton({ deviceId, action, args, className, disabled, scope }: CommandButtonProps & { scope: string }) {
  const acks = useLive((s) => s.acks)
  const qc = useQueryClient()
  const refreshHistory = useCallback(() => {
    void qc.invalidateQueries({ queryKey: ['device-commands', deviceId] })
    void qc.invalidateQueries({ queryKey: ['device-events', deviceId] })
  }, [qc, deviceId])
  const [busy, setBusy] = useState(false)
  const [confirming, setConfirming] = useState<{ args?: string } | null>(null)
  const [pendingId, setPendingId] = useState<number | null>(null)
  const settled = useRef<Set<number>>(new Set())
  const active = useRef(true)
  const sending = useRef(false)
  const label = action.label
  const error = commandArgsError(args ?? '', action.inputSchema, action.inputMaxLength)
  const blocked = !!disabled || !!error
  const current = useCallback(() => active.current && commandScope(useAuth.getState(), deviceId) === scope, [deviceId, scope])

  useEffect(() => {
    active.current = true
    return () => { active.current = false }
  }, [])
  // 参数或有效性变化后必须重新确认；即使后来改回原参数，也不能复活已勾选的确认。
  useEffect(() => { setConfirming(null) }, [args, blocked])

  useEffect(() => {
    if (pendingId == null || settled.current.has(pendingId) || !current()) return
    const ack = acks[pendingId]
    if (!ack) return
    settled.current.add(pendingId)
    sending.current = false
    setBusy(false)
    setPendingId(null)
    refreshHistory()
    if (ack.status === 'ok') toast.ok(label + '已执行', ack.detail || undefined)
    else toast.bad(label + '失败', ack.detail || '边缘节点返回失败但未附原因，可在命令历史查看原始回执')
  }, [acks, pendingId, label, current, refreshHistory])

  useEffect(() => {
    if (pendingId == null) return
    const t = setTimeout(() => {
      if (settled.current.has(pendingId) || !current()) return
      settled.current.add(pendingId)
      sending.current = false
      setBusy(false)
      setPendingId(null)
      refreshHistory()
      toast.bad(label + '超时', '边缘节点未回执（设备可能离线或通道忙）')
    }, ACK_TIMEOUT_MS)
    return () => clearTimeout(t)
  }, [pendingId, label, current, refreshHistory])

  const send = async () => {
    // DOM 的 disabled 不是最后一道门：确认回调与异步响应也必须属于当前身份。
    if (sending.current || blocked || !current()) return
    const [edgeId, devId] = deviceId.split('/')
    sending.current = true
    setBusy(true)
    try {
      const cv = await api.sendCommand(edgeId ?? '', devId ?? '', action.cmd, args)
      if (!current()) return
      setPendingId(cv.id)
      refreshHistory()
    } catch (e) {
      if (!current()) return
      sending.current = false
      setBusy(false)
      toast.bad(label + '未下发', commandErrorCopy(e))
    }
  }

  const onClick = () => {
    if (sending.current || blocked || !current()) return
    if (action.confirmText || action.variant === 'danger') { setConfirming({ args }); return }
    void send()
  }
  const title = [
    error, action.hint,
    action.capability ? 'Capability ' + action.capability : '',
    action.entityLabel ? 'Entity ' + action.entityLabel : '',
    'cmd=' + action.cmd,
  ].filter(Boolean).join(' · ')

  return (
    <>
      <button type="button" onClick={onClick} disabled={busy || blocked} title={title}
        aria-busy={busy} aria-label={label}
        className={cn('btn min-w-0', {
          'btn-primary': action.variant === 'primary',
          'btn-ghost': !action.variant || action.variant === 'ghost',
          'bg-bad/10 text-bad hover:bg-bad/16': action.variant === 'danger',
        }, className)}>
        {busy && <Loader2 size={14} className="shrink-0 animate-spin" />}
        <span className="truncate">{label}</span>
      </button>
      <ConfirmDialog open={confirming !== null && confirming.args === args && !blocked}
        tone={action.variant === 'danger' ? 'danger' : 'warn'} title={'确认执行「' + label + '」？'}
        body={<>
          <p>{action.confirmText ?? '请确认要向该设备执行此命令。'}</p>
          <p className="num mt-2 text-xs text-ink-3">
            目标设备 <span className="break-all">{deviceId}</span> · 命令 <span className="font-mono">{action.cmd}</span>
            {args ? <> · 参数 <span className="font-mono break-all">{args}</span></> : null}
          </p>
        </>}
        confirmLabel={label} busy={busy}
        requireAck={action.variant === 'danger' ? '我已确认该操作会作用于真实设备，且可能无法撤销。' : undefined}
        onCancel={() => setConfirming(null)}
        onConfirm={() => {
          if (!confirming || confirming.args !== args || blocked || !current()) return
          setConfirming(null)
          void send()
        }} />
    </>
  )
}
