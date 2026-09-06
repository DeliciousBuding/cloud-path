// 命令集、参数与危险确认来自声明；鉴权在公共面板边界，不依赖具体页面。
import { useId, useState } from 'react'
import { Command, SlidersHorizontal } from 'lucide-react'
import { Panel } from './ui'
import { CommandButton } from './CommandButton'
import { CommandInput } from './command/CommandInput'
import { commandScope } from './command/scope'
import { useAuth } from '@/store/auth'
import { cn } from '@/lib/cn'
import { argsError, optionLabel } from '@/lib/format'
import type { CommandSet } from '@/lib/descriptor'

const SOURCE_LABEL: Record<CommandSet['source'], string> = {
  descriptor: 'Schema 声明', adapter: '适配器白名单', none: '无声明',
}

/** 整棵可写子树按身份/租户/设备卸载，不能在回来时复活参数或确认框。 */
function WritableActions({ deviceId, set }: { deviceId: string; set: CommandSet }) {
  const id = useId()
  const [advCmd, setAdvCmd] = useState('')
  const [advArgs, setAdvArgs] = useState('')
  const advErr = argsError(advArgs)
  const simple = set.actions.filter((a) => !a.needsInput && !a.inputSchema)
    .sort((a, b) => Number(a.variant === 'danger') - Number(b.variant === 'danger'))
  const withInput = set.actions.filter((a) => a.needsInput || a.inputSchema)
  const advanced = set.source === 'adapter' ? set.actions.filter((a) => !a.inputSchema) : []
  const advAction = advanced.find((a) => a.cmd === advCmd)

  return (
    <>
      {simple.length > 0 && <div className="grid gap-3 sm:grid-cols-2">
        {simple.map((a) => <div key={a.cmd} className={cn('min-w-0', a.variant === 'danger' && 'sm:col-span-2')}>
          <CommandButton deviceId={deviceId} action={a} className="w-full" />
          {a.hint && <p className="mt-1.5 min-w-0 text-[12px] leading-relaxed text-ink-3" title={a.hint}>{a.hint}</p>}
        </div>)}
      </div>}
      {withInput.length > 0 && <div className={simple.length > 0 ? 'mt-3 space-y-3' : 'space-y-3'}>
        {withInput.map((a) => <CommandInput key={a.cmd} deviceId={deviceId} action={a} />)}
      </div>}
      {advanced.length > 0 && <div className="mt-3 border-t border-hairline pt-3">
        <p className="mb-2 flex items-center gap-1.5 text-[12px] text-ink-3">
          <SlidersHorizontal size={12} />带参数下发（≤64 UTF-8 字节，不含换行/NUL）
        </p>
        <div className="flex flex-wrap gap-2">
          <label className="sr-only" htmlFor={id + '-cmd'}>选择命令</label>
          <select id={id + '-cmd'} value={advCmd}
            onChange={(e) => { setAdvCmd(e.target.value); setAdvArgs('') }}
            className="input input-sm min-w-0 flex-1">
            <option value="">选择命令</option>
            {advanced.map((a) => <option key={a.cmd} value={a.cmd}>{optionLabel(a.cmd)}</option>)}
          </select>
          <label className="sr-only" htmlFor={id + '-args'}>命令参数</label>
          <input id={id + '-args'} value={advAction ? advArgs : ''} disabled={!advAction}
            aria-invalid={advErr ? true : undefined} aria-describedby={advErr ? id + '-error' : undefined}
            onChange={(e) => setAdvArgs(e.target.value)} placeholder={advAction?.inputPlaceholder ?? '参数（可空）'}
            className={cn('input input-sm min-w-0 flex-1 disabled:opacity-50', advErr && 'input-error')} />
          {advAction && <CommandButton deviceId={deviceId} action={advAction} args={advArgs} disabled={!!advErr} className="shrink-0" />}
        </div>
        {advErr && <p id={id + '-error'} role="alert" className="mt-1 text-[12px] text-bad">{advErr}</p>}
      </div>}
    </>
  )
}

export function ActionPanel({ deviceId, set, adapterName, className }: {
  deviceId: string
  set: CommandSet
  adapterName?: string
  className?: string
}) {
  const scope = useAuth((s) => commandScope(s, deviceId))
  return (
    <Panel className={className}
      title={<span className="flex items-center gap-1.5"><Command size={14} />命令</span>}
      right={<span className="flex min-w-0 items-center gap-1.5">
        <span className="shrink-0 text-[12px] text-ink-3">{SOURCE_LABEL[set.source]}</span>
        {adapterName && <span className="min-w-0 truncate text-[12px] text-ink-3" title={'适配器 ' + adapterName}>{adapterName} 适配器</span>}
      </span>}>
      {set.actions.length === 0 ? <p className="py-4 text-center text-sm text-ink-3">
        该设备未声明可下发命令（等待 Descriptor / Capability catalog）
      </p> : scope ? <WritableActions key={JSON.stringify([scope, set])} deviceId={deviceId} set={set} /> : <p className="py-3 text-sm text-ink-3">
        只读：需要 operator 或 admin 身份才能下发命令。
      </p>}
      <p className="mt-3 text-[12px] leading-relaxed text-ink-3">
        命令经 server → 边缘节点 → 设备下发，回执通过实时通道返回并落库。
      </p>
    </Panel>
  )
}
