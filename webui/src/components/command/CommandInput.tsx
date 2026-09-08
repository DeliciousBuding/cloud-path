import { CommandButton } from '../CommandButton'
import { SchemaActionInput } from './SchemaActionInput'
import { argsMaxBytes } from '@/lib/format'
import { commandArgsError } from '@/lib/command-schema'
import type { CommandAction } from '@/lib/descriptor'

/** Device commands retain their existing transport limits and ACK handling. */
export function CommandInput({ deviceId, action }: { deviceId: string; action: CommandAction }) {
  return <SchemaActionInput
    action={{ ...action, inputPlaceholder: action.inputSchema ? '填写单行 JSON' : action.inputPlaceholder }}
    validate={(args) => commandArgsError(args, action.inputSchema, action.inputMaxLength)}
    description={<>≤{argsMaxBytes(action.inputMaxLength)} UTF-8 字节，不含换行/NUL。</>}
    emptyHint="填写参数后可下发。" validationSource="设备端"
    renderSubmit={(args, error) => <CommandButton deviceId={deviceId} action={action} args={args}
      disabled={!!error} className="shrink-0" />} />
}
