// 二次确认对话框：危险 Command / 删除 / 权限扩大都必须经过这里。
//
// 设计约束：
//   - 默认焦点落在「取消」上（危险操作不做「回车即执行」的顺手确认）；
//   - Esc 与点遮罩都是取消，不是确认；
//   - `requireAck` 给出必须勾选的确认句（不可逆操作 / 权限扩大），未勾选时确认键禁用；
//   - busy 期间两个按钮都禁用，避免重复提交产生第二个 revision；
//   - 390px：宽度用 w-full + max-w，内边距相对单位，不写死像素宽。
import { useEffect, useId, useRef, useState } from 'react'
import type { ReactNode } from 'react'
import { AlertTriangle, Info } from 'lucide-react'
import { cn } from '@/lib/cn'
import { Spinner } from './ui'

export interface ConfirmDialogProps {
  open: boolean
  title: string
  /** 说清「会发生什么、影响谁、能不能撤销」 */
  body: ReactNode
  confirmLabel: string
  cancelLabel?: string
  /** danger = 不可逆或影响设备运行；warn = 需要知情但不危险 */
  tone?: 'danger' | 'warn' | 'info'
  busy?: boolean
  /** 必须勾选的确认句；给出后未勾选不可确认 */
  requireAck?: string
  /** 需要在对话框里额外呈现的结构化内容（如待确认的权限清单） */
  extra?: ReactNode
  onConfirm: () => void
  onCancel: () => void
}

export function ConfirmDialog({
  open, title, body, confirmLabel, cancelLabel = '取消', tone = 'danger',
  busy = false, requireAck, extra, onConfirm, onCancel,
}: ConfirmDialogProps) {
  const titleId = useId()
  const cancelRef = useRef<HTMLButtonElement>(null)
  const [acked, setAcked] = useState(false)

  // 每次打开都重置勾选：上一次的确认不得延续到下一次操作
  useEffect(() => { if (open) setAcked(false) }, [open])

  useEffect(() => {
    if (!open) return
    cancelRef.current?.focus()
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape' && !busy) onCancel()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [open, busy, onCancel])

  if (!open) return null

  const blocked = busy || (Boolean(requireAck) && !acked)
  const Icon = tone === 'info' ? Info : AlertTriangle
  const iconCls = tone === 'danger' ? 'bg-bad/10 text-bad'
    : tone === 'warn' ? 'bg-warn/12 text-warn' : 'bg-accent/10 text-accent'

  return (
    <div
      className="fixed inset-0 z-50 flex items-end justify-center overflow-y-auto px-4 py-6 sm:items-center"
      onMouseDown={(e) => { if (e.target === e.currentTarget && !busy) onCancel() }}
    >
      {/* 遮罩：颜色走 token 混色，不写死 rgba */}
      <div className="dialog-backdrop fixed inset-0 backdrop-blur-[2px]" aria-hidden />
      <div
        role="dialog" aria-modal="true" aria-labelledby={titleId}
        className="card dialog relative w-full max-w-md p-6 fade-up"
      >
        <div className="flex items-start gap-3.5">
          <span className={cn('flex h-10 w-10 shrink-0 items-center justify-center rounded-full', iconCls)}>
            <Icon size={18} />
          </span>
          <div className="min-w-0 flex-1">
            <h2 id={titleId} className="text-[15px] font-semibold tracking-[-0.01em] break-words">{title}</h2>
            <div className="mt-1.5 text-sm leading-relaxed break-words text-ink-2">{body}</div>
          </div>
        </div>

        {extra && <div className="mt-4">{extra}</div>}

        {requireAck && (
          <label className="mt-4 flex cursor-pointer items-start gap-2.5 rounded-xl bg-surface-2 p-3.5">
            <input
              type="checkbox" checked={acked} disabled={busy}
              onChange={(e) => setAcked(e.target.checked)}
              className="mt-0.5 h-4 w-4 shrink-0 accent-accent"
            />
            <span className="min-w-0 text-[13px] leading-relaxed break-words">{requireAck}</span>
          </label>
        )}

        <div className="mt-6 flex flex-col-reverse gap-2.5 sm:flex-row sm:justify-end">
          <button
            ref={cancelRef} type="button" className="btn btn-ghost" disabled={busy} onClick={onCancel}
          >
            {cancelLabel}
          </button>
          <button
            type="button"
            className={cn('btn', tone === 'danger' ? 'btn-danger' : 'btn-primary')}
            disabled={blocked}
            onClick={onConfirm}
          >
            {busy && <Spinner size={13} />}
            {confirmLabel}
          </button>
        </div>
      </div>
    </div>
  )
}