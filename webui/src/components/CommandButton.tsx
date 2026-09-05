import { useEffect, useRef, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { Loader2 } from 'lucide-react'
import { api } from '@/lib/api'
import { useLive } from '@/store/ws'
import { toast } from '@/store/toast'
import { cn } from '@/lib/cn'
import { ConfirmDialog } from './ConfirmDialog'
import { commandErrorCopy } from '@/lib/format'
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
 *
 * 危险命令的二次确认走设计系统里的 ConfirmDialog（不用 window.confirm 这类默认浏览器样式）：
 * 确认文案逐字取自声明的 `confirmation`，`variant==='danger'` 时还必须显式勾选才允许执行。
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
  const qc = useQueryClient()
  // 下发与 ack 结算后立即刷新历史与事件，不让用户等 5s 轮询
  const refreshHistory = () => {
    void qc.invalidateQueries({ queryKey: ['device-commands', deviceId] })
    void qc.invalidateQueries({ queryKey: ['device-events', deviceId] })
  }
  const [busy, setBusy] = useState(false)
  const [confirming, setConfirming] = useState(false)
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
    refreshHistory()
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

  /** 点击入口：声明了确认文案的先开对话框，其余直接下发 */
  const onClick = () => {
    if (busy) return
    if (action.confirmText) { setConfirming(true); return }
    void send()
  }

  const send = async () => {
    if (busy) return
    const [edgeId, devId] = deviceId.split('/')
    setBusy(true)
    try {
      const cv = await api.sendCommand(
        edgeId ?? '', devId ?? '', action.cmd,
        args === undefined ? undefined : sanitizeArgs(args, maxLen),
      )
      setPendingId(cv.id)
      refreshHistory()
    } catch (e) {
      setBusy(false)
      // 按 HTTP 状态说人话（权限不足 / 节点离线 / 限流 …），不把服务端原文甩给用户
      toast.bad(`${label}未下发`, commandErrorCopy(e))
    }
  }

  const title = [
    action.hint,
    action.capability ? `Capability ${action.capability}` : '',
    action.entityLabel ? `Entity ${action.entityLabel}` : '',
    `cmd=${action.cmd}`,
  ].filter(Boolean).join(' · ')

  return (
    <>
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
    <ConfirmDialog
      open={confirming}
      tone={action.variant === 'danger' ? 'danger' : 'warn'}
      title={`确认执行「${label}」？`}
      body={
        <>
          <p>{action.confirmText}</p>
          <p className="num mt-2 text-xs text-ink-3">
            目标设备 <span className="break-all">{deviceId}</span> · 命令 <span className="font-mono">{action.cmd}</span>
            {args ? <> · 参数 <span className="font-mono break-all">{args}</span></> : null}
          </p>
        </>
      }
      confirmLabel={label}
      busy={busy}
      requireAck={action.variant === 'danger'
        ? '我已确认该操作会作用于真实设备，且可能无法撤销。'
        : undefined}
      onCancel={() => setConfirming(false)}
      onConfirm={() => { setConfirming(false); void send() }}
    />
    </>
  )
}