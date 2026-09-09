import { CommandButton } from '../CommandButton'
import { SchemaActionInput } from './SchemaActionInput'
import { commandArgsError } from '@/lib/command-schema'
import type { CommandAction } from '@/lib/descriptor'

/** Device commands retain their existing transport limits and ACK handling. */
export function CommandInput({ deviceId, targetLabel, action }: { deviceId: string; targetLabel?: string; action: CommandAction }) {
  return <SchemaActionInput
    action={{ ...action, inputPlaceholder: action.inputPlaceholder ?? '按设备要求填写参数' }}
    validate={(args) => commandArgsError(args, action.inputSchema, action.inputMaxLength)}
    description={action.hint ?? ''}
    emptyHint="填写参数后即可执行。" validationSource="设备端" showTitle={false}
    renderSubmit={(args, error) => <CommandButton deviceId={deviceId} targetLabel={targetLabel} action={action} args={args}
      disabled={!!error} className="min-h-11 w-full sm:min-h-0 sm:w-auto" />} />
}
