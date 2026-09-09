import { Link } from 'react-router'
import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { History, RefreshCw } from 'lucide-react'
import { Badge, Button, ErrorState, Panel } from '@/components/ui'
import { RowSkeleton } from '@/components/Skeleton'
import { CommandButton } from '@/components/CommandButton'
import { commandDisplayMeta, commandFailureInfo, commandStatusLabel } from '@/components/EventFeed'
import { api } from '@/lib/api'
import { commandArgsError } from '@/lib/command-schema'
import { cmdStatusMeta, fmtTime, fmtDateTime } from '@/lib/format'
import type { ReactNode } from 'react'
import type { CommandAction } from '@/lib/descriptor'

function isFailure(status: string): boolean {
  return status === 'failed' || status === 'timeout'
}

function isInProgress(status: string): boolean {
  return status === 'pending' || status === 'sent'
}

function controlPath(deviceId: string): string {
  const [edgeId = '', devId = ''] = deviceId.split('/')
  return `/devices/${encodeURIComponent(edgeId)}/${encodeURIComponent(devId)}?tab=controls`
}

/** 操作记录：REST 轮询该设备的操作与执行结果（含超时/失败原因）。
 *  普通用户只看人话结果；状态码、原始返回与参数只放在「技术详情」里。 */
export function CommandHistory({ deviceId, targetLabel, actions, limit, footer, online = true }: {
  deviceId: string; targetLabel?: string; actions?: CommandAction[];
  /** 展示上限（概览首屏用：右栏不该拉到 20 行把左栏踢出空洞）；缺省全显 */
  limit?: number;
  /** 被截断时的出口（如「到控制页看全部」） */
  footer?: ReactNode
  /** 设备离线时禁止从历史记录重试；缺省保持既有行为。 */
  online?: boolean
}) {
  const { t } = useTranslation('activity')
  const { data, isLoading, error, refetch, isFetching } = useQuery({
    queryKey: ['device-commands', deviceId],
    queryFn: () => api.commands({ device: deviceId, limit: 20 }),
    refetchInterval: 5000,
  })
  const rows = data?.commands ?? []
  const shown = limit != null ? rows.slice(0, limit) : rows

  return (
    <Panel
      title={<span className="flex items-center gap-1.5"><History size={14} />{t('tabs.commands')}</span>}
      right={!isLoading && !error ? (
        <span className="flex items-center gap-2 text-meta text-ink-3">
          <span className="num">{t('command.count', { count: shown.length })}</span>
          <Button variant="ghost" size="sm" className="h-7 min-h-touch px-2 sm:min-h-0" disabled={isFetching}
            aria-label={t('command.refreshAria')} onClick={() => { void refetch() }}>
            <RefreshCw size={12} className={isFetching ? 'animate-spin' : undefined} />
            {t('refresh')}
          </Button>
        </span>
      ) : undefined}
    >
      {isLoading ? (
        <RowSkeleton rows={3} />
      ) : error ? (
        <ErrorState compact title={t('command.loadErrorTitle')}
          hint={t('command.loadErrorHint')}
          onRetry={() => { void refetch() }} retrying={isFetching} />
      ) : rows.length === 0 ? (
        <p className="py-4 text-center text-body text-ink-3">{t('command.empty')}</p>
      ) : (
        <>
        <ul className="divide-y divide-hairline">
          {shown.map((c) => {
            const st = cmdStatusMeta(c.status)
            const statusLabel = commandStatusLabel(c.status)
            const meta = commandDisplayMeta(c.cmd, undefined, actions)
            const action = actions?.find((a) => a.cmd === c.cmd)
            const failure = isFailure(c.status)
            const progress = isInProgress(c.status)
            const unknown = !failure && !progress && statusLabel === t('command.statusUnknown')
            const info = failure ? commandFailureInfo(c.result, c.status) : null
            const retryError = action ? commandArgsError(c.args ?? '', action.inputSchema, action.inputMaxLength) : undefined
            const progressCopy = c.status === 'pending' ? t('command.progressPending') : t('command.progressSent')
            return (
              <li key={c.id} className="grid min-w-0 grid-cols-[auto_minmax(0,1fr)_auto] items-center gap-x-3 gap-y-1 py-3">
                <Badge tone={st.tone} className="shrink-0">{statusLabel}</Badge>
                <span className="min-w-0 truncate text-meta font-medium" title={meta.hint || meta.label}>
                  {meta.label}
                </span>
                <time className="num shrink-0 font-mono text-micro text-ink-3"
                  title={c.acked_at ? t('command.completedAt', { time: fmtDateTime(c.acked_at) }) : fmtDateTime(c.created_at)}>
                  {fmtTime(c.created_at)}
                </time>

                {progress && (
                  <p className="col-span-3 min-w-0 text-meta text-ink-3">{progressCopy}{t('command.progressSuffix')}</p>
                )}

                {failure && info && (
                  <div className="col-span-3 flex min-w-0 flex-wrap items-center gap-x-2 gap-y-2 rounded-tile bg-bad/8 px-2.5 py-2 text-meta">
                    <span className="min-w-0 flex-1 break-words text-ink-2">
                      <span className="font-medium text-bad">{info.message}</span>
                      <span className="text-ink-3"> · {info.next}</span>
                    </span>
                    {!online ? (
                      <span className="shrink-0 text-ink-3">{t('command.offlineRetry')}</span>
                    ) : action && !retryError ? (
                      <CommandButton deviceId={deviceId} targetLabel={targetLabel} action={action} args={c.args ?? ''}
                        buttonLabel={t('command.retry')} buttonAriaLabel={t('command.retryAria', { label: meta.label })}
                        className="btn-sm min-h-touch w-full sm:min-h-0 sm:w-auto" />
                    ) : (
                      <Link to={controlPath(deviceId)} className="link inline-flex min-h-touch shrink-0 items-center sm:min-h-0">
                        {t('command.goRetry')}
                      </Link>
                    )}
                  </div>
                )}

                {unknown && (
                  <p className="col-span-3 min-w-0 text-meta text-ink-3">
                    {t('command.unknown')}
                  </p>
                )}

                <details className="col-span-3 min-w-0 text-meta">
                  <summary className="flex min-h-touch cursor-pointer select-none items-center text-ink-3 sm:min-h-0">
                    {t('command.technicalDetails')}
                  </summary>
                  <dl className="mt-1.5 grid gap-1 rounded-tile bg-ink-3/5 p-2 text-ink-2 sm:grid-cols-2">
                    <div className="min-w-0"><dt className="inline text-ink-3">{t('command.statusCode')}</dt><dd className="inline break-all font-mono">{c.status}</dd></div>
                    <div className="min-w-0"><dt className="inline text-ink-3">{t('command.recordId')}</dt><dd className="num inline font-mono">{c.id}</dd></div>
                    <div className="min-w-0 sm:col-span-2"><dt className="inline text-ink-3">{t('command.argsLabel')}</dt><dd className="inline break-all font-mono">{c.args || '—'}</dd></div>
                    <div className="min-w-0 sm:col-span-2"><dt className="inline text-ink-3">{t('command.rawResult')}</dt><dd className="inline break-words font-mono">{c.result || '—'}</dd></div>
                  </dl>
                </details>
              </li>
            )
          })}
        </ul>
        {footer && rows.length > shown.length && <div className="mt-2 border-t border-hairline pt-2">{footer}</div>}
        </>
      )}
    </Panel>
  )
}
