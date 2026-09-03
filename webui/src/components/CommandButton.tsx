import { useEffect, useRef, useState, type ReactNode } from 'react'
import { Loader2 } from 'lucide-react'
import { api } from '@/lib/api'
import { useLive } from '@/store/ws'
import { toast } from '@/store/toast'
import { cn } from '@/lib/cn'

const ACK_TIMEOUT_MS = 15000

/**
 * 命令按钮：POST 下发 → 记录 command_id → 订阅 WS ack → 轻提示反馈 + 超时兜底。
 * 按钮可用性由适配器命令白名单决定（页面按 /api/adapters 过滤后再渲染）。
 */
export function CommandButton({ deviceId, cmd, args, label, icon, variant = 'ghost', confirmText, okText, title, className }: {
  deviceId: string          // "<edge>/<dev>"
  cmd: string               // sync|dump|trigger|open|isp|raw
  args?: string             // 命令参数（如 raw 的原始串、sync 的 HHMM）
  label: string
  icon?: ReactNode
  variant?: 'primary' | 'ghost' | 'danger'
  confirmText?: string      // 需要二次确认的危险命令
  okText?: string           // ack ok 时的提示语
  title?: string
  className?: string
}) {
  const acks = useLive((s) => s.acks)
  const [busy, setBusy] = useState(false)
  const [pendingId, setPendingId] = useState<number | null>(null)
  const settled = useRef<Set<number>>(new Set())

  // ack 到达 → 结算
  useEffect(() => {
    if (pendingId == null || settled.current.has(pendingId)) return
    const ack = acks[pendingId]
    if (!ack) return
    settled.current.add(pendingId)
    setBusy(false)
    setPendingId(null)
    if (ack.status === 'ok') toast.ok(okText ?? `${label}已执行`, ack.detail || undefined)
    else toast.bad(`${label}失败`, ack.detail || ack.status)
  }, [acks, pendingId, label, okText])

  // 超时兜底（edge 未回执）
  useEffect(() => {
    if (pendingId == null) return
    const t = setTimeout(() => {
      if (settled.current.has(pendingId)) return
      settled.current.add(pendingId)
      setBusy(false)
      setPendingId(null)
      toast.bad(`${label}超时`, '边缘节点未回执（设备可能离线或串口忙）')
    }, ACK_TIMEOUT_MS)
    return () => clearTimeout(t)
  }, [pendingId, label])

  const onClick = async () => {
    if (busy) return
    if (confirmText && !window.confirm(confirmText)) return
    const [edgeId, devId] = deviceId.split('/')
    setBusy(true)
    try {
      const cv = await api.sendCommand(edgeId ?? '', devId ?? '', cmd, args)
      setPendingId(cv.id)
    } catch (e) {
      setBusy(false)
      toast.bad(`${label}下发失败`, e instanceof Error ? e.message : String(e))
    }
  }

  return (
    <button
      type="button"
      onClick={onClick}
      disabled={busy}
      title={title}
      aria-busy={busy}
      className={cn('btn', {
        'btn-primary': variant === 'primary',
        'btn-ghost': variant === 'ghost',
        'bg-bad/10 text-bad hover:bg-bad/16': variant === 'danger',
      }, className)}
    >
      {busy ? <Loader2 size={14} className="animate-spin" /> : icon}
      {label}
    </button>
  )
}
