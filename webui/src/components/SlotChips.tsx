import type { Slot } from '@/lib/types'
import { cn } from '@/lib/cn'

const SLOT_CLS = ['bg-ink-3/10 text-ink-2', 'bg-ok/12 text-ok', 'bg-bad/12 text-bad']

/**
 * 槽位状态胶囊：0 待确认 / 1 已确认 / 2 逾期。
 * 语义由适配器给出（label 字段），组件本身不假设行业含义。
 */
export function SlotChips({ slots, compact }: { slots?: Slot[]; compact?: boolean }) {
  if (!slots?.length) return null
  return (
    <div className="flex gap-1.5">
      {slots.map((s) => (
        <span
          key={s.index}
          className={cn(
            'rounded-full font-medium',
            compact ? 'px-2 py-0.5 text-[11px]' : 'flex-1 px-3 py-1.5 text-center text-xs',
            SLOT_CLS[s.code] ?? SLOT_CLS[0],
          )}
          title={`槽位 ${s.index + 1}：${s.label}`}
        >
          {compact ? s.label : `槽位 ${s.index + 1} · ${s.label}`}
        </span>
      ))}
    </div>
  )
}

/** 槽位图例（详情页用） */
export function SlotLegend() {
  const items = [
    { cls: 'bg-idle/60', label: '待确认' },
    { cls: 'bg-ok', label: '已确认' },
    { cls: 'bg-bad', label: '逾期' },
  ]
  return (
    <div className="mt-4 flex gap-4 border-t border-hairline pt-3 text-[11px] text-ink-3">
      {items.map((i) => (
        <span key={i.label} className="flex items-center gap-1">
          <i className={cn('h-2 w-2 rounded-full', i.cls)} />{i.label}
        </span>
      ))}
    </div>
  )
}
