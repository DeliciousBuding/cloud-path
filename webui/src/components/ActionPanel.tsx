// 命令面板（Schema 驱动）：命令集、文案、危险确认全部来自后端声明
// （Capability spec.actions → Descriptor 扩展 commands → /api/adapters 白名单），
// 前端不再维护 CMD 白名单/文案/图标表。图标一律省略，避免又变成一张命令→外观映射表。
import { useState } from 'react'
import { Command, SlidersHorizontal } from 'lucide-react'
import { Badge, Panel } from './ui'
import { CommandButton } from './CommandButton'
import { cn } from '@/lib/cn'
import { optionLabel } from '@/lib/format'
import { humanize } from '@/lib/descriptor'
import type { CommandAction, CommandSet } from '@/lib/descriptor'

const SOURCE_LABEL: Record<CommandSet['source'], string> = {
  descriptor: 'Schema 声明',
  adapter: '适配器白名单',
  none: '无声明',
}

/** 声明了 inputSchema 的动作：参数输入 + 下发（占位符即声明生成的参数模板） */
function InputAction({ deviceId, action }: { deviceId: string; action: CommandAction }) {
  const [args, setArgs] = useState('')
  const max = action.inputMaxLength ?? 64
  const id = `args-${action.cmd}`
  return (
    <div className="border-t border-hairline pt-3">
      <label htmlFor={id} className="mb-1.5 block text-[11px] leading-relaxed text-ink-3">
        {action.label}
        {action.hint ? ` · ${action.hint}` : ''}
        <span className="num"> （≤{max} 字符，不含换行）</span>
      </label>
      <div className="flex gap-2">
        <input
          id={id} value={args} maxLength={max}
          onChange={(e) => setArgs(e.target.value.replace(/[\r\n\0]/g, ''))}
          placeholder={action.inputPlaceholder ?? '参数'}
          className="num min-w-0 flex-1 rounded-full border border-hairline bg-surface-2 px-3.5 py-1.5 font-mono text-xs outline-none transition-colors focus:border-accent"
        />
        <CommandButton deviceId={deviceId} action={action} args={args} className="shrink-0" />
      </div>
    </div>
  )
}

export function ActionPanel({ deviceId, set, adapterName, className }: {
  deviceId: string
  set: CommandSet
  adapterName?: string
  className?: string
}) {
  const [advCmd, setAdvCmd] = useState('')
  const [advArgs, setAdvArgs] = useState('')

  const simple = set.actions.filter((a) => !a.needsInput)
  const withInput = set.actions.filter((a) => a.needsInput)
  // 声明缺席时（只有白名单命令名），保留一个通用「带参数下发」入口，仍然不写死任何命令名
  const advanced = set.source === 'adapter' ? set.actions : []
  const advAction: CommandAction | null = advCmd
    ? (advanced.find((a) => a.cmd === advCmd) ?? { cmd: advCmd, label: humanize(advCmd) })
    : null

  return (
    <Panel
      className={className}
      title={<span className="flex items-center gap-1.5"><Command size={14} />命令</span>}
      right={
        <span className="flex min-w-0 items-center gap-1.5">
          <Badge tone={set.source === 'descriptor' ? 'accent' : 'idle'}>{SOURCE_LABEL[set.source]}</Badge>
          {adapterName && <span className="min-w-0 truncate text-[11px] text-ink-3" title={adapterName}>{adapterName}</span>}
        </span>
      }
    >
      {set.actions.length === 0 ? (
        <p className="py-4 text-center text-sm text-ink-3">
          该设备未声明可下发命令（等待 Descriptor / Capability catalog）
        </p>
      ) : (
        <>
          {simple.length > 0 && (
            <div className="grid gap-3 sm:grid-cols-2">
              {simple.map((a) => (
                <div key={a.cmd} className="min-w-0">
                  <CommandButton deviceId={deviceId} action={a}
                    className={cn('w-full', a.variant === 'danger' ? 'col-span-2' : '')} />
                  {a.hint && (
                    <p className="mt-1.5 min-w-0 text-[11px] leading-relaxed text-ink-3" title={a.hint}>
                      {a.hint}
                    </p>
                  )}
                </div>
              ))}
            </div>
          )}

          {withInput.length > 0 && (
            <div className={simple.length > 0 ? 'mt-3 space-y-3' : 'space-y-3'}>
              {withInput.map((a) => <InputAction key={a.cmd} deviceId={deviceId} action={a} />)}
            </div>
          )}

          {advanced.length > 0 && (
            <div className="mt-3 border-t border-hairline pt-3">
              <p className="mb-1.5 flex items-center gap-1 text-[11px] text-ink-3">
                <SlidersHorizontal size={11} /> 带参数下发（命令集来自后端白名单）
              </p>
              <div className="flex gap-2">
                <label className="sr-only" htmlFor="adv-cmd">选择命令</label>
                <select id="adv-cmd" value={advCmd} onChange={(e) => setAdvCmd(e.target.value)}
                  className="num min-w-0 shrink-0 rounded-full border border-hairline bg-surface-2 px-3 py-1.5 font-mono text-xs outline-none transition-colors focus:border-accent">
                  <option value="">选择命令</option>
                  {advanced.map((a) => <option key={a.cmd} value={a.cmd}>{optionLabel(a.cmd)}</option>)}
                </select>
                <label className="sr-only" htmlFor="adv-args">命令参数</label>
                <input id="adv-args" value={advArgs} maxLength={64} disabled={!advAction}
                  onChange={(e) => setAdvArgs(e.target.value.replace(/[\r\n\0]/g, ''))}
                  placeholder={advAction?.inputPlaceholder ?? '参数（可空）'}
                  className="num min-w-0 flex-1 rounded-full border border-hairline bg-surface-2 px-3.5 py-1.5 font-mono text-xs outline-none transition-colors focus:border-accent disabled:opacity-50"
                />
                {advAction && (
                  <CommandButton deviceId={deviceId} action={advAction} args={advArgs} className="shrink-0" />
                )}
              </div>
            </div>
          )}
        </>
      )}

      <p className="mt-3 text-[11px] leading-relaxed text-ink-3">
        命令经 server → 边缘节点 → 设备下发，回执通过实时通道返回并落库。
      </p>
    </Panel>
  )
}



