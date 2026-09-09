import { useCallback, useEffect, useRef, useState } from 'react'
import type { ReactNode } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { Loader2 } from 'lucide-react'
import { api } from '@/lib/api'
import { useAuth } from '@/store/auth'
import { useLive } from '@/store/ws'
import { toast } from '@/store/toast'
import { cn } from '@/lib/cn'
import { ConfirmDialog } from './ConfirmDialog'
import { commandScope } from './command/scope'
import { commandArgsError, commandArgsErrorCopy, commandHasInput } from '@/lib/command-schema'
import { commandErrorCopy } from '@/lib/format'
import type { CommandAction } from '@/lib/descriptor'

/** ACK detail 可能是设备内部 key=value/JSON；轻提示只给人话，原文留在操作记录。 */
function ackDetailCopy(detail?: string): string | undefined {
  const text = detail?.trim()
  if (!text) return undefined
  if (/^[\[{]/.test(text) || /(?:^|\s)[a-z][a-z0-9_]*\s*=/.test(text)) {
    return '设备已返回确认，结果请在操作记录中查看。'
  }
  return text
}

const ACK_TIMEOUT_MS = 15000

interface CommandButtonProps {
  /** "<edge>/<dev>" */
  deviceId: string
  action: CommandAction
  /** 保留受控参数原文；undefined 表示不带 args 下发。 */
  args?: string
  /** 危险确认优先显示设备名；缺失时回落内部设备键。 */
  targetLabel?: string
  /** 历史重试等场景可覆盖按钮可见文案，无障碍名称单独给出。 */
  buttonLabel?: ReactNode
  buttonAriaLabel?: string
  className?: string
  disabled?: boolean
}

/** 独立使用按钮也受同一权限边界保护；身份/设备/声明变化会卸载旧确认与回执状态。 */
export function CommandButton(props: CommandButtonProps) {
  const scope = useAuth((s) => commandScope(s, props.deviceId))
  if (!scope) return null
  return <ScopedCommandButton key={JSON.stringify([scope, props.action])} {...props} scope={scope} />
}

/** POST → WS ACK → 历史刷新/超时；危险确认只取声明，不认识设备或具体操作名。 */
function ScopedCommandButton({ deviceId, targetLabel, action, args, buttonLabel, buttonAriaLabel, className, disabled, scope }: CommandButtonProps & { scope: string }) {
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
  const displayTarget = targetLabel || deviceId
  const inputSchema = action.inputSchema && commandHasInput(action.inputSchema) ? action.inputSchema : undefined
  const error = commandArgsError(args ?? '', inputSchema, action.inputMaxLength)
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
    if (ack.status === 'ok') toast.ok(label + '已完成', ackDetailCopy(ack.detail))
    else toast.bad(label + '失败', '设备返回失败，请在操作记录中查看结果。')
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
      toast.info(label + '仍在等待确认', '已下发，设备暂未返回结果；请到操作记录查看，避免重复执行。')
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
      toast.bad(label + '没有执行', commandErrorCopy(e))
    }
  }

  const onClick = () => {
    if (sending.current || blocked || !current()) return
    if (action.confirmText || action.variant === 'danger') { setConfirming({ args }); return }
    void send()
  }
  const title = commandArgsErrorCopy(error)

  return (
    <>
      <button type="button" onClick={onClick} disabled={busy || blocked} title={title}
        aria-busy={busy} aria-label={buttonAriaLabel ?? label}
        className={cn('btn min-w-0', {
          'btn-primary': action.variant === 'primary',
          'btn-ghost': !action.variant || action.variant === 'ghost',
          'bg-bad/10 text-bad hover:bg-bad/16': action.variant === 'danger',
        }, className)}>
        {busy && <Loader2 size={14} className="shrink-0 animate-spin" />}
        <span className="truncate">{busy ? '正在执行…' : (buttonLabel ?? label)}</span>
      </button>
      <ConfirmDialog open={confirming !== null && confirming.args === args && !blocked}
        tone={action.variant === 'danger' ? 'danger' : 'warn'} title={'确认执行「' + label + '」？'}
        body={<>
          <p>{action.confirmText ?? '请确认要执行此操作。'}</p>
          <p className="num mt-2 text-xs text-ink-3">
            目标设备 <span className="break-all">{displayTarget}</span>
          </p>
        </>}
        confirmLabel={label} busy={busy}
        requireAck={action.variant === 'danger' ? '我已确认操作目标，并知悉此操作可能无法撤销。' : undefined}
        onCancel={() => setConfirming(null)}
        onConfirm={() => {
          if (!confirming || confirming.args !== args || blocked || !current()) return
          setConfirming(null)
          void send()
        }} />
    </>
  )
}
