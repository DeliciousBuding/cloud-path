// 设备操作：只展示设备当前支持的动作；参数操作先选功能，再填参数。
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Command, SlidersHorizontal, WifiOff } from 'lucide-react'
import { Panel, Select } from './ui'
import { CommandButton } from './CommandButton'
import { CommandInput } from './command/CommandInput'
import { commandScope } from './command/scope'
import { useAuth } from '@/store/auth'
import { cn } from '@/lib/cn'
import { argsError } from '@/lib/format'
import { commandHasInput } from '@/lib/command-schema'
import type { CommandSet } from '@/lib/descriptor'

/** 整棵可写子树按身份/租户/设备卸载，不能在回来时复活参数或确认框。 */
function WritableActions({ deviceId, targetLabel, set, disabled = false }: {
  deviceId: string
  targetLabel?: string
  set: CommandSet
  disabled?: boolean
}) {
  const { t } = useTranslation('devices')
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
    <fieldset disabled={disabled} aria-disabled={disabled || undefined}
      className={cn('m-0 min-w-0 border-0 p-0', disabled && 'opacity-60')}>
      {simple.length > 0 && (
        <div>
          <p className="mb-2 text-meta font-medium text-ink-3">{t('commandPanel.quickActions')}</p>
          <div className="grid grid-cols-2 gap-2 sm:gap-3">
            {simple.map((a) => <div key={a.cmd} className={cn('min-w-0', a.variant === 'danger' && 'col-span-2')}>
              <CommandButton deviceId={deviceId} targetLabel={targetLabel} action={a} disabled={disabled} className="min-h-touch w-full sm:min-h-0" />
              {a.hint && <p className="sr-only min-w-0 text-meta leading-relaxed text-ink-3 sm:not-sr-only sm:mt-1.5 sm:block" title={a.hint}>{a.hint}</p>}
            </div>)}
          </div>
        </div>
      )}

      {parameterized.length > 0 && (
        <div className={simple.length > 0 ? 'mt-3 border-t border-hairline pt-3 sm:mt-4 sm:pt-4' : ''}>
          <label htmlFor={id + '-parameter-action'} className="mb-1.5 block text-meta font-medium text-ink-3">
            {t('commandPanel.parameterLabel')}
          </label>
          <Select
            id={id + '-parameter-action'}
            aria-label={t('commandPanel.parameterAria')}
            value={selected?.cmd ?? ''}
            onChange={(e) => setSelectedCmd(e.target.value)}
            className="min-w-0 max-w-md"
            compact
          >
            {parameterized.map((a) => <option key={a.cmd} value={a.cmd}>{a.label}</option>)}
          </Select>
          {selected && <div className="mt-3 sm:mt-4"><CommandInput key={selected.cmd} deviceId={deviceId} targetLabel={targetLabel} action={selected} /></div>}
        </div>
      )}

      {manual.length > 0 && (
        <details className={simple.length > 0 || parameterized.length > 0 ? 'mt-4 border-t border-hairline pt-4' : ''}>
          <summary className="flex min-h-touch cursor-pointer select-none items-center gap-1.5 text-meta font-medium text-ink-3 transition-colors hover:text-ink-2 sm:min-h-0">
            <SlidersHorizontal size={12} />{t('commandPanel.manualSummary')}
          </summary>
          <div className="mt-2 flex flex-wrap gap-2">
            <label className="sr-only" htmlFor={id + '-cmd'}>{t('commandPanel.actionPlaceholder')}</label>
            <Select id={id + '-cmd'} compact value={advCmd}
              onChange={(e) => { setAdvCmd(e.target.value); setAdvArgs('') }}
              className="min-w-0 flex-1">
              <option value="">{t('commandPanel.actionPlaceholder')}</option>
              {manual.map((a) => <option key={a.cmd} value={a.cmd}>{a.label}</option>)}
            </Select>
            <label className="sr-only" htmlFor={id + '-args'}>{t('commandPanel.argsLabel')}</label>
            <input id={id + '-args'} value={advAction ? advArgs : ''} disabled={!advAction}
              aria-invalid={advErr ? true : undefined} aria-describedby={advErr ? id + '-error' : undefined}
              onChange={(e) => setAdvArgs(e.target.value)} placeholder={advAction?.inputPlaceholder ?? t('commandPanel.argsPlaceholder')}
              className={cn('input input-sm min-w-0 flex-1', advErr && 'input-error')} />
            {advAction && <CommandButton deviceId={deviceId} targetLabel={targetLabel} action={advAction} args={advArgs} disabled={disabled || !!advErr} className="min-h-touch w-full sm:min-h-0 sm:w-auto" />}
          </div>
          {advErr && <p id={id + '-error'} role="alert" className="mt-1 text-meta text-bad">{advErr}</p>}
        </details>
      )}
    </fieldset>
  )
}

export function ActionPanel({ deviceId, targetLabel, set, className, online = true, offlineReason }: {
  deviceId: string
  targetLabel?: string
  set: CommandSet
  className?: string
  online?: boolean
  offlineReason?: string
}) {
  const { t } = useTranslation('devices')
  const scope = useAuth((s) => commandScope(s, deviceId))
  return (
    <Panel className={className}
      title={<span className="flex items-center gap-1.5"><Command size={14} />{t('commandPanel.title')}</span>}>
      {!online && offlineReason && (
        <p role="status" className="mb-3 flex items-start gap-2 rounded-tile bg-ink-3/8 px-3 py-2.5 text-meta leading-relaxed text-ink-2">
          <WifiOff size={14} className="mt-0.5 shrink-0 text-ink-3" />
          <span>{offlineReason}</span>
        </p>
      )}
      {set.actions.length === 0 ? <p className="py-4 text-center text-body text-ink-3">
        {t('commandPanel.empty')}
      </p> : scope ? <WritableActions key={JSON.stringify([scope, targetLabel, set])} deviceId={deviceId} targetLabel={targetLabel} set={set} disabled={!online} /> : <p className="py-3 text-body text-ink-3">
        {t('commandPanel.noPermission')}
      </p>}
    </Panel>
  )
}
