import { useEffect, useRef, useState } from 'react'
import { Loader2 } from 'lucide-react'
import { api } from '@/lib/api'
import { useLive } from '@/store/ws'
import { toast } from '@/store/toast'
import { cn } from '@/lib/cn'
import type { CommandAction } from '@/lib/descriptor'

const ACK_TIMEOUT_MS = 15000

/** args 卫生：后端限制长度且不含换行/NUL（docs/api.md），前端先收敛一次 */
export function sanitizeArgs(s: string, max = 64): string {
  return s.replace(/[\r\n\0]/g, '').slice(0, max)
}

/**
 * 命令按钮：POST 下发 → 记录 command_id → 订阅 WS ack → 轻提示反馈 + 超时兜底。
 *
 * 文案、危险确认、是否需要参数全部来自 `action`（由 lib/descriptor.ts 从 Capability actions /
 * Descriptor commands / 适配器白名单推导）。本组件不认识任何具体命令名。
 */
export function CommandButton({ deviceId, action, args, className }: {
  /** "<edge>/<dev>" */
  deviceId: string
  action: CommandAction
  /** 受控参数（带输入框的动作用）；undefined 表示不带 args 下发 */
  args?: string
  className?: string
}) {
  const acks = useLive((s) => s.acks)
  const [busy, setBusy] = useState(false)
  const [pendingId, setPendingId] = useState<number | null>(null)
  const settled = useRef<Set<number>>(new Set())
  const label = action.label
  const maxLen = action.inputMaxLength ?? 64

  // ack 到达 → 结算
  useEffect(() => {
    if (pendingId == null || settled.current.has(pendingId)) return
    const ack = acks[pendingId]
    if (!ack) return
    settled.current.add(pendingId)
    setBusy(false)
    setPendingId(null)
    if (ack.status === 'ok') toast.ok(`${label}已执行`, ack.detail || undefined)
    else toast.bad(`${label}失败`, ack.detail || ack.status)
  }, [acks, pendingId, label])

  // 超时兜底（edge 未回执）
  useEffect(() => {
    if (pendingId == null) return
    const t = setTimeout(() => {
      if (settled.current.has(pendingId)) return
      settled.current.add(pendingId)
      setBusy(false)
      setPendingId(null)
      toast.bad(`${label}超时`, '边缘节点未回执（设备可能离线或通道忙）')
    }, ACK_TIMEOUT_MS)
    return () => clearTimeout(t)
  }, [pendingId, label])

  const onClick = async () => {
    if (busy) return
    if (action.confirmText && !window.confirm(action.confirmText)) return
    const [edgeId, devId] = deviceId.split('/')
    setBusy(true)
    try {
      const cv = await api.sendCommand(
        edgeId ?? '', devId ?? '', action.cmd,
        args === undefined ? undefined : sanitizeArgs(args, maxLen),
      )
      setPendingId(cv.id)
    } catch (e) {
      setBusy(false)
      toast.bad(`${label}下发失败`, e instanceof Error ? e.message : String(e))
    }
  }

  const title = [
    action.hint,
    action.capability ? `Capability ${action.capability}` : '',
    action.entityLabel ? `Entity ${action.entityLabel}` : '',
    `cmd=${action.cmd}`,
  ].filter(Boolean).join(' · ')

  return (
    <button
      type="button"
      onClick={onClick}
      disabled={busy}
      title={title}
      aria-busy={busy}
      aria-label={label}
      className={cn('btn min-w-0', {
        'btn-primary': action.variant === 'primary',
        'btn-ghost': !action.variant || action.variant === 'ghost',
        'bg-bad/10 text-bad hover:bg-bad/16': action.variant === 'danger',
      }, className)}
    >
      {busy && <Loader2 size={14} className="shrink-0 animate-spin" />}
      <span className="truncate">{label}</span>
    </button>
  )
}