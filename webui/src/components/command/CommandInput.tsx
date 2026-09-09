import { useTranslation } from 'react-i18next'
import { CommandButton } from '../CommandButton'
import { SchemaActionInput } from './SchemaActionInput'
import { commandArgsError } from '@/lib/command-schema'
import type { CommandAction } from '@/lib/descriptor'

/** Device commands retain their existing transport limits and ACK handling. */
export function CommandInput({ deviceId, targetLabel, action }: { deviceId: string; targetLabel?: string; action: CommandAction }) {
  const { t } = useTranslation('common')
  return <SchemaActionInput
    action={{ ...action, inputPlaceholder: action.inputPlaceholder ?? t('commandInput.parameterPlaceholder') }}
    validate={(args) => commandArgsError(args, action.inputSchema, action.inputMaxLength)}
    description={action.hint ?? ''}
    emptyHint={t('commandInput.emptyHint')} validationSource={t('commandInput.validationSource')} showTitle={false}
    renderSubmit={(args, error) => <CommandButton deviceId={deviceId} targetLabel={targetLabel} action={action} args={args}
      disabled={!!error} className="min-h-touch w-full sm:min-h-0 sm:w-auto" />} />
}
