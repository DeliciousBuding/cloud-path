// 二次确认对话框：危险 Command / 删除 / 权限扩大都必须经过这里。
//
// 设计约束：
//   - 默认焦点落在「取消」上（危险操作不做「回车即执行」的顺手确认）；
//   - Esc 与点遮罩都是取消，不是确认；
//   - `requireAck` 给出必须勾选的确认句（不可逆操作 / 权限扩大），未勾选时确认键禁用；
//   - busy 期间两个按钮都禁用，避免重复提交产生第二个 revision。
// 焦点陷阱、Esc、遮罩关闭和焦点恢复由 Radix Dialog 负责；业务只保留确认语义。
import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import '@/i18n'
import type { ReactNode } from 'react'
import { AlertTriangle, Info } from 'lucide-react'
import { cn } from '@/lib/cn'
import { Button, Checkbox } from './ui'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogTitle,
} from './ui/dialog'

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
  open, title, body, confirmLabel, cancelLabel, tone = 'danger',
  busy = false, requireAck, extra, onConfirm, onCancel,
}: ConfirmDialogProps) {
  const { t } = useTranslation()
  const cancelText = cancelLabel ?? t('actions.cancel')
  const cancelRef = useRef<HTMLButtonElement>(null)
  const contentRef = useRef<HTMLDivElement>(null)
  const previousFocusRef = useRef<HTMLElement | null>(null)
  const [acked, setAcked] = useState(false)

  // 每次打开都重置勾选，并记录触发点；关闭后把焦点还给用户。
  // Radix 只自动管理 Dialog.Trigger 的焦点，CloudPath 的调用方是受控 open，故这里显式收口。
  useEffect(() => {
    if (open) {
      previousFocusRef.current = document.activeElement instanceof HTMLElement ? document.activeElement : null
      setAcked(false)
      return
    }
    previousFocusRef.current?.focus({ preventScroll: true })
    previousFocusRef.current = null
  }, [open])

  const blocked = busy || (Boolean(requireAck) && !acked)
  const Icon = tone === 'info' ? Info : AlertTriangle
  const iconCls = tone === 'danger' ? 'bg-bad/10 text-bad'
    : tone === 'warn' ? 'bg-warn/12 text-warn' : 'bg-accent/10 text-accent'

  return (
    <Dialog open={open} onOpenChange={(next) => { if (!next && !busy) onCancel() }}>
      <DialogContent
        ref={contentRef}
        showClose={false}
        aria-busy={busy}
        className="max-w-md"
        onOpenAutoFocus={(event) => {
          event.preventDefault()
          if (busy) contentRef.current?.focus()
          else cancelRef.current?.focus()
        }}
      >
        <div className="flex items-start gap-3.5">
          <span className={cn('flex h-10 w-10 shrink-0 items-center justify-center rounded-pill', iconCls)}>
            <Icon aria-hidden="true" size={18} />
          </span>
          <div className="min-w-0 flex-1">
            <DialogTitle className="break-words">{title}</DialogTitle>
            <DialogDescription asChild>
              <div className="mt-1.5 break-words">{body}</div>
            </DialogDescription>
          </div>
        </div>

        {extra && <div className="mt-4">{extra}</div>}

        {requireAck && (
          <label className="mt-4 flex cursor-pointer items-start gap-2.5 rounded-tile bg-surface-2 p-3.5">
            <Checkbox
              checked={acked} disabled={busy}
              onChange={(e) => setAcked(e.target.checked)}
              className="mt-0.5"
            />
            <span className="min-w-0 text-compact leading-relaxed break-words">{requireAck}</span>
          </label>
        )}

        <DialogFooter className="flex-col-reverse sm:flex-row">
          <Button ref={cancelRef} variant="ghost" disabled={busy} onClick={onCancel}>
            {cancelText}
          </Button>
          <Button
            variant={tone === 'danger' ? 'danger' : 'primary'}
            loading={busy}
            disabled={blocked}
            onClick={onConfirm}
          >
            {confirmLabel}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
