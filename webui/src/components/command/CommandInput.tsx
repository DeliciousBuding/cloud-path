import { CommandButton } from '../CommandButton'
import { SchemaActionInput } from './SchemaActionInput'
import { commandArgsError } from '@/lib/command-schema'
import type { CommandAction } from '@/lib/descriptor'

/** Device commands retain their existing transport limits and ACK handling. */
export function CommandInput({ deviceId, action }: { deviceId: string; action: CommandAction }) {
  return <SchemaActionInput
    action={{ ...action, inputPlaceholder: action.inputPlaceholder ?? '按设备要求填写参数' }}
    validate={(args) => commandArgsError(args, action.inputSchema, action.inputMaxLength)}
    description="按设备要求填写参数。"
    emptyHint="填写参数后即可执行。" validationSource="设备端"
    renderSubmit={(args, error) => <CommandButton deviceId={deviceId} action={action} args={args}
      disabled={!!error} className="shrink-0" />} />
}
