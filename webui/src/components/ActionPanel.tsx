// 设备操作：只展示设备当前支持的动作；参数操作先选功能，再填参数。
import { useId, useState } from 'react'
import { Command, SlidersHorizontal } from 'lucide-react'
import { Panel } from './ui'
import { CommandButton } from './CommandButton'
import { CommandInput } from './command/CommandInput'
import { commandScope } from './command/scope'
import { useAuth } from '@/store/auth'
import { cn } from '@/lib/cn'
import { argsError } from '@/lib/format'
import { commandHasInput } from '@/lib/command-schema'
import type { CommandSet } from '@/lib/descriptor'

/** 整棵可写子树按身份/租户/设备卸载，不能在回来时复活参数或确认框。 */
function WritableActions({ deviceId, targetLabel, set }: { deviceId: string; targetLabel?: string; set: CommandSet }) {
  const id = useId()
  const [selectedCmd, setSelectedCmd] = useState('')
  const [advCmd, setAdvCmd] = useState('')
  const [advArgs, setAdvArgs] = useState('')
  const advErr = argsError(advArgs)
  const hasSchema = (a: CommandSet['actions'][number]) => !!a.inputSchema && commandHasInput(a.inputSchema)
  const emptySchema = (a: CommandSet['actions'][number]) => a.inputSchema !== undefined && !commandHasInput(a.inputSchema)
  const parameterized = set.actions.filter(hasSchema)
  const manualAction = (a: CommandSet['actions'][number]) => !hasSchema(a)
    && !emptySchema(a) && (set.source === 'adapter' || (!a.inputSchema && !!a.needsInput))
  const manual = set.actions.filter(manualAction)
  const simple = set.actions.filter((a) => !hasSchema(a) && !manualAction(a))
    .sort((a, b) => Number(a.variant === 'danger') - Number(b.variant === 'danger'))
  const selected = parameterized.find((a) => a.cmd === selectedCmd) ?? parameterized[0]
  const advAction = manual.find((a) => a.cmd === advCmd)

  return (
    <>
      {simple.length > 0 && (
        <div>
          <p className="mb-2 text-[12px] font-medium text-ink-3">快捷操作</p>
          <div className="grid grid-cols-2 gap-2 sm:gap-3">
            {simple.map((a) => <div key={a.cmd} className={cn('min-w-0', a.variant === 'danger' && 'col-span-2')}>
              <CommandButton deviceId={deviceId} targetLabel={targetLabel} action={a} className="min-h-11 w-full sm:min-h-0" />
              {a.hint && <p className="sr-only min-w-0 text-[12px] leading-relaxed text-ink-3 sm:not-sr-only sm:mt-1.5 sm:block" title={a.hint}>{a.hint}</p>}
            </div>)}
          </div>
        </div>
      )}

      {parameterized.length > 0 && (
        <div className={simple.length > 0 ? 'mt-3 border-t border-hairline pt-3 sm:mt-4 sm:pt-4' : ''}>
          <label htmlFor={id + '-parameter-action'} className="mb-1.5 block text-[12px] font-medium text-ink-3">
            选择要设置的功能
          </label>
          <select
            id={id + '-parameter-action'}
            aria-label="选择参数操作"
            value={selected?.cmd ?? ''}
            onChange={(e) => setSelectedCmd(e.target.value)}
            className="input input-sm min-h-11 min-w-0 max-w-md sm:min-h-0"
          >
            {parameterized.map((a) => <option key={a.cmd} value={a.cmd}>{a.label}</option>)}
          </select>
          {selected && <div className="mt-3 sm:mt-4"><CommandInput key={selected.cmd} deviceId={deviceId} targetLabel={targetLabel} action={selected} /></div>}
        </div>
      )}

      {manual.length > 0 && (
        <details className={simple.length > 0 || parameterized.length > 0 ? 'mt-4 border-t border-hairline pt-4' : ''}>
          <summary className="flex min-h-11 cursor-pointer select-none items-center gap-1.5 text-[12px] font-medium text-ink-3 transition-colors hover:text-ink-2 sm:min-h-0">
            <SlidersHorizontal size={12} />高级：手动输入参数
          </summary>
          <div className="mt-2 flex flex-wrap gap-2">
            <label className="sr-only" htmlFor={id + '-cmd'}>选择操作</label>
            <select id={id + '-cmd'} value={advCmd}
              onChange={(e) => { setAdvCmd(e.target.value); setAdvArgs('') }}
              className="input input-sm min-h-11 min-w-0 flex-1 sm:min-h-0">
              <option value="">选择操作</option>
              {manual.map((a) => <option key={a.cmd} value={a.cmd}>{a.label}</option>)}
            </select>
            <label className="sr-only" htmlFor={id + '-args'}>操作参数</label>
            <input id={id + '-args'} value={advAction ? advArgs : ''} disabled={!advAction}
              aria-invalid={advErr ? true : undefined} aria-describedby={advErr ? id + '-error' : undefined}
              onChange={(e) => setAdvArgs(e.target.value)} placeholder={advAction?.inputPlaceholder ?? '参数（可空）'}
              className={cn('input input-sm min-h-11 min-w-0 flex-1 disabled:opacity-50 sm:min-h-0', advErr && 'input-error')} />
            {advAction && <CommandButton deviceId={deviceId} targetLabel={targetLabel} action={advAction} args={advArgs} disabled={!!advErr} className="min-h-11 w-full sm:min-h-0 sm:w-auto" />}
          </div>
          {advErr && <p id={id + '-error'} role="alert" className="mt-1 text-[12px] text-bad">{advErr}</p>}
        </details>
      )}
    </>
  )
}

export function ActionPanel({ deviceId, targetLabel, set, className }: {
  deviceId: string
  targetLabel?: string
  set: CommandSet
  className?: string
}) {
  const scope = useAuth((s) => commandScope(s, deviceId))
  return (
    <Panel className={className}
      title={<span className="flex items-center gap-1.5"><Command size={14} />设备操作</span>}>
      {set.actions.length === 0 ? <p className="py-4 text-center text-sm text-ink-3">
        这台设备暂时没有可执行的操作
      </p> : scope ? <WritableActions key={JSON.stringify([scope, targetLabel, set])} deviceId={deviceId} targetLabel={targetLabel} set={set} /> : <p className="py-3 text-sm text-ink-3">
        当前账号没有操作权限。
      </p>}
    </Panel>
  )
}
