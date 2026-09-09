// 运行实例的 **期望态 | 实际态** 分离呈现（control-plane-sync.md 不变量 5）。
//
// 这一块是整个插件面最重要的一处诚实性：
//   - 左栏只放 Server 权威的 desired（enabled / version / revision / isolation / updated_at）；
//   - 右栏只放 网关上报的 observed（state / version / applied / health / reported_at / restart）；
//   - `has_observed=false` 时右栏不是空格子，而是一个明确的「网关未上报」块，
//     并说明原因（网关离线 vs 在线但还没回过）——绝不让 desired.enabled 冒充「运行中」；
//   - `stale` / `drift` 各有独立视觉状态，且都写在右栏或顶部同步条上。
import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { AlertTriangle, CloudOff, Power, RadioTower, TimerReset } from 'lucide-react'
import { Badge } from '@/components/ui'
import {
  healthMeta, hostDetailLabel, instanceLocationLabel, instanceStatus, isolationLabel, stateMeta, syncState,
} from '@/lib/plugins'
import { fmtDateTime, timeAgo } from '@/lib/format'
import type { PluginInstanceView } from '@/lib/types'

/** 顶部同步条：一句话说明期望状态与实际状态的关系 */
export function SyncBanner({ v }: { v: PluginInstanceView }) {
  const s = syncState(v)
  const Icon = s.key === 'synced' ? TimerReset : s.key === 'unreported' ? CloudOff : AlertTriangle
  const boxCls = s.tone === 'ok' ? 'bg-ok/10' : s.tone === 'warn' ? 'bg-warn/12'
    : s.tone === 'accent' ? 'bg-accent/10' : 'bg-ink-3/10'
  const fgCls = s.tone === 'ok' ? 'text-ok' : s.tone === 'warn' ? 'text-warn'
    : s.tone === 'accent' ? 'text-accent' : 'text-ink-2'
  return (
    <div className={`flex min-w-0 items-start gap-2.5 rounded-lg px-3.5 py-3 ${boxCls}`} role="status">
      <span className={`mt-0.5 shrink-0 ${fgCls}`}>
        <Icon size={15} />
      </span>
      <span className="min-w-0 flex-1">
        <span className="block text-[13px] font-semibold break-words">{s.label}</span>
        <span className="mt-0.5 block text-[12px] leading-relaxed break-words text-ink-2">{s.hint}</span>
      </span>
    </div>
  )
}

/** 一行事实：左标签右值，值必须可截断（390px 两栏时每栏只有约 10rem） */
function Row({ k, v, tone, wrap }: { k: string; v: ReactNode; tone?: 'ok' | 'warn' | 'bad' | 'idle' | 'accent'; wrap?: boolean }) {
  const cls = tone === 'ok' ? 'text-ok' : tone === 'warn' ? 'text-warn'
    : tone === 'bad' ? 'text-bad' : tone === 'accent' ? 'text-accent' : undefined
  return (
    <div className="flex min-w-0 items-baseline justify-between gap-2 border-b border-hairline py-1.5 last:border-b-0">
      <dt className="shrink-0 text-[12px] text-ink-3">{k}</dt>
      <dd className={`num min-w-0 text-[12px] font-medium ${wrap ? 'break-words' : 'truncate'} ${cls ?? ''}`}
        title={typeof v === 'string' ? v : undefined}>
        {v}
      </dd>
    </div>
  )
}

function ColumnHead({ children, note }: { children: ReactNode; note: string }) {
  return (
    <div className="mb-1.5 flex min-w-0 items-baseline justify-between gap-2">
      <h3 className="min-w-0 truncate text-[12px] font-semibold tracking-[-0.01em]">{children}</h3>
      <span className="shrink-0 text-[12px] text-ink-3">{note}</span>
    </div>
  )
}

/** 右栏在「网关未上报」时的整块呈现（不是空格子） */
function UnreportedBlock({ v }: { v: PluginInstanceView }) {
  const { t } = useTranslation('plugin')
  return (
    <div className="flex flex-col items-center justify-center rounded-lg bg-surface px-3 py-6 text-center">
      <span className="flex h-9 w-9 items-center justify-center rounded-full bg-ink-3/12 text-ink-2">
        {v.edge_online ? <RadioTower size={16} /> : <CloudOff size={16} />}
      </span>
      <p className="mt-2.5 text-[13px] font-semibold">{v.edge_id === 'server' ? t('desired.noState') : t('desired.noObserved')}</p>
      <p className="mt-1 text-[12px] leading-relaxed break-words text-ink-2">
        {v.edge_id === 'server' ? t('desired.noStateServerHint') : v.edge_online
          ? t('desired.noStateEdgeOnlineHint')
          : t('desired.noStateEdgeOfflineHint')}
      </p>
    </div>
  )
}

/** 详情页主路径摘要：只显示用户能判断和行动的状态、位置与下一步。 */
export function InstanceStatusSummary({ v }: { v: PluginInstanceView }) {
  const { t } = useTranslation('plugin')
  const status = instanceStatus(v)
  const state = stateMeta(v.observed?.state)
  const health = healthMeta(v.observed?.health)
  const Icon = status.key === 'normal' ? TimerReset : status.key === 'stopped' ? Power
    : status.key === 'attention' ? AlertTriangle : CloudOff
  const next = (v.drift || v.stale) ? t('status.reapplyHint') : status.next
  const tone = status.tone === 'ok' ? 'text-ok bg-ok/10' : status.tone === 'bad' ? 'text-bad bg-bad/10'
    : status.tone === 'warn' ? 'text-warn bg-warn/12' : 'text-ink-2 bg-ink-3/10'

  return <div className="space-y-4">
    <div className="flex min-w-0 items-start gap-3">
      <span className={`flex h-9 w-9 shrink-0 items-center justify-center rounded-full ${tone}`}>
        <Icon size={16} />
      </span>
      <div className="min-w-0">
        <Badge tone={status.tone}>{status.label}</Badge>
        <p className="mt-2 break-words text-sm font-medium">{status.summary}</p>
        {next && <p className="mt-1 break-words text-[12px] leading-relaxed text-ink-2">{t('desired.next', { text: next })}</p>}
      </div>
    </div>
    <dl className="grid gap-3 border-t border-hairline pt-4 sm:grid-cols-3">
      <div className="min-w-0">
        <dt className="text-[12px] text-ink-3">{t('desired.summaryLocation')}</dt>
        <dd className="mt-1 break-words text-[13px] font-medium">{instanceLocationLabel(v)}</dd>
      </div>
      <div className="min-w-0">
        <dt className="text-[12px] text-ink-3">{t('desired.summaryState')}</dt>
        <dd className="mt-1 break-words text-[13px] font-medium">{v.has_observed ? state.label : t('common.statusUnknown')}</dd>
      </div>
      <div className="min-w-0">
        <dt className="text-[12px] text-ink-3">{t('desired.summaryHealth')}</dt>
        <dd className="mt-1 break-words text-[13px] font-medium">{v.has_observed ? health.label : t('desired.summaryNotReceived')}</dd>
      </div>
    </dl>
  </div>
}

/**
 * 期望状态 | 实际状态对照。移动端上下堆叠，桌面双栏并排，保留字段顺序与同步/差异信息。
 */
export function DesiredObserved({ v }: { v: PluginInstanceView }) {
  const { t } = useTranslation('plugin')
  const st = stateMeta(v.observed?.state)
  const hl = healthMeta(v.observed?.health)
  const reportedAt = v.observed?.reported_at ?? 0

  return (
    <div className="space-y-2">
    <div className="grid grid-cols-1 gap-2.5 sm:grid-cols-2 sm:gap-4">
      <div className="min-w-0 rounded-lg bg-surface-2 p-3 sm:p-3.5">
        <ColumnHead note={v.edge_id === 'server' ? t('desired.desiredNoteServer') : t('desired.desiredNoteEdge')}>{t('desired.desired')}</ColumnHead>
        <dl className="m-0">
          <Row k={t('desired.enabled')} v={v.desired.enabled ? t('desired.enabledValue') : t('desired.disabledValue')}
            tone={v.desired.enabled ? 'accent' : 'idle'} />
          <Row k={t('desired.version')} v={v.desired.version || '—'} />
          <Row k={t('desired.isolation')} v={isolationLabel(v.desired.isolation)} />
          <Row wrap k={t('desired.updatedAt')} v={v.desired.updated_at ? fmtDateTime(v.desired.updated_at) : '—'} />
        </dl>
        <p className="mt-2 text-[12px] leading-relaxed text-ink-3">
          {t('desired.desiredHint')}
        </p>
      </div>

      <div className="min-w-0 rounded-lg bg-surface-2 p-3 sm:p-3.5">
        <ColumnHead note={v.edge_id === 'server' ? t('desired.observedNoteServer') : t('desired.observedNoteEdge')}>{t('desired.observed')}</ColumnHead>
        {v.has_observed ? (
          <>
            <dl className="m-0">
              <Row k={t('desired.state')} v={st.label} tone={st.tone === 'idle' ? undefined : st.tone} />
              <Row k={t('desired.version')} v={v.observed?.version || t('desired.versionNotProvided')} />
              <Row k={t('desired.health')} v={hl.label} tone={hl.tone === 'idle' ? undefined : hl.tone} />
              <Row k={t('desired.restartCount')} v={t('desired.restartCountValue', { count: v.observed?.restart_count ?? 0 })}
                tone={(v.observed?.restart_count ?? 0) > 0 ? 'warn' : undefined} />
              <Row wrap k={t('desired.observedAt')} v={reportedAt ? fmtDateTime(reportedAt) : '—'} />
            </dl>
            {v.stale && (
              <p className="mt-2 flex items-start gap-1.5 rounded-lg bg-warn/12 px-2.5 py-2 text-[12px] leading-relaxed text-warn">
                <AlertTriangle size={12} className="mt-0.5 shrink-0" />
                <span className="min-w-0 break-words">
                  {t('desired.stale', { time: reportedAt ? fmtDateTime(reportedAt) : t('desired.unknownTime') })}
                  {reportedAt ? `（${timeAgo(reportedAt)}）` : ''}
                </span>
              </p>
            )}
            {v.observed?.detail && hostDetailLabel(v.observed.detail) !== v.observed.detail && (
              <p className="mt-2 truncate text-[12px] text-ink-3" title={v.observed.detail}>
                {t('desired.detail')}{hostDetailLabel(v.observed.detail)}
              </p>
            )}
          </>
        ) : (
          <UnreportedBlock v={v} />
        )}
      </div>
    </div>
    <details className="min-w-0 text-xs text-ink-2">
      <summary className="flex min-h-11 cursor-pointer items-center">{t('desired.technicalDetails')}</summary>
      <dl className="mt-2 space-y-1 rounded-lg bg-surface-2 px-3 py-2.5">
        <div><dt className="inline">{t('desired.desiredRevision')}</dt><dd className="num inline">{v.desired_revision}</dd></div>
        <div><dt className="inline">{t('desired.appliedRevision')}</dt><dd className="num inline">{v.applied_revision}</dd></div>
        <div><dt className="inline">{t('desired.stateRaw')}</dt><dd className="num inline break-all">{v.observed?.state || '—'}</dd></div>
        <div><dt className="inline">{t('desired.healthRaw')}</dt><dd className="num inline break-all">{v.observed?.health || '—'}</dd></div>
        {v.observed?.detail && <div><dt className="inline">{t('desired.detailRaw')}</dt><dd className="inline break-all">{v.observed.detail}</dd></div>}
      </dl>
    </details>
    </div>
  )
}
