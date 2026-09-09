// 运行实例的 **期望态 | 实际态** 分离呈现（control-plane-sync.md 不变量 5）。
//
// 这一块是整个插件面最重要的一处诚实性：
//   - 左栏只放 Server 权威的 desired（enabled / version / revision / isolation / updated_at）；
//   - 右栏只放 网关上报的 observed（state / version / applied / health / reported_at / restart）；
//   - `has_observed=false` 时右栏不是空格子，而是一个明确的「网关未上报」块，
//     并说明原因（网关离线 vs 在线但还没回过）——绝不让 desired.enabled 冒充「运行中」；
//   - `stale` / `drift` 各有独立视觉状态，且都写在右栏或顶部同步条上。
import type { ReactNode } from 'react'
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
  return (
    <div className="flex flex-col items-center justify-center rounded-lg bg-surface px-3 py-6 text-center">
      <span className="flex h-9 w-9 items-center justify-center rounded-full bg-ink-3/12 text-ink-2">
        {v.edge_online ? <RadioTower size={16} /> : <CloudOff size={16} />}
      </span>
      <p className="mt-2.5 text-[13px] font-semibold">{v.edge_id === 'server' ? '还没有运行状态' : '还没有收到运行状态'}</p>
      <p className="mt-1 text-[12px] leading-relaxed break-words text-ink-2">
        {v.edge_id === 'server' ? '保存的启用设置不代表它正在运行，请查看应用数据。' : v.edge_online
          ? '网关在线，但还没有收到它的运行状态，暂时不能判断是否正在运行。'
          : '网关当前离线，因此没有最新运行状态。重新连接后会自动更新。'}
      </p>
    </div>
  )
}

/** 详情页主路径摘要：只显示用户能判断和行动的状态、位置与下一步。 */
export function InstanceStatusSummary({ v }: { v: PluginInstanceView }) {
  const status = instanceStatus(v)
  const state = stateMeta(v.observed?.state)
  const health = healthMeta(v.observed?.health)
  const Icon = status.key === 'normal' ? TimerReset : status.key === 'stopped' ? Power
    : status.key === 'attention' ? AlertTriangle : CloudOff
  const next = (v.drift || v.stale) ? '确认保存的设置无误后，点击「重新应用设置」。' : status.next
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
        {next && <p className="mt-1 break-words text-[12px] leading-relaxed text-ink-2">下一步：{next}</p>}
      </div>
    </div>
    <dl className="grid gap-3 border-t border-hairline pt-4 sm:grid-cols-3">
      <div className="min-w-0">
        <dt className="text-[12px] text-ink-3">运行位置</dt>
        <dd className="mt-1 break-words text-[13px] font-medium">{instanceLocationLabel(v)}</dd>
      </div>
      <div className="min-w-0">
        <dt className="text-[12px] text-ink-3">当前状态</dt>
        <dd className="mt-1 break-words text-[13px] font-medium">{v.has_observed ? state.label : '状态待确认'}</dd>
      </div>
      <div className="min-w-0">
        <dt className="text-[12px] text-ink-3">健康情况</dt>
        <dd className="mt-1 break-words text-[13px] font-medium">{v.has_observed ? health.label : '尚未收到'}</dd>
      </div>
    </dl>
  </div>
}

/**
 * 期望状态 | 实际状态对照。移动端上下堆叠，桌面双栏并排，保留字段顺序与同步/差异信息。
 */
export function DesiredObserved({ v }: { v: PluginInstanceView }) {
  const st = stateMeta(v.observed?.state)
  const hl = healthMeta(v.observed?.health)
  const reportedAt = v.observed?.reported_at ?? 0

  return (
    <div className="space-y-2">
    <div className="grid grid-cols-1 gap-2.5 sm:grid-cols-2 sm:gap-4">
      <div className="min-w-0 rounded-lg bg-surface-2 p-3 sm:p-3.5">
        <ColumnHead note={v.edge_id === 'server' ? '中心服务会按它应用' : '网关会按它应用'}>保存的设置</ColumnHead>
        <dl className="m-0">
          <Row k="启用" v={v.desired.enabled ? '已启用' : '已停用'}
            tone={v.desired.enabled ? 'accent' : 'idle'} />
          <Row k="版本" v={v.desired.version || '—'} />
          <Row k="运行方式" v={isolationLabel(v.desired.isolation)} />
          <Row wrap k="更新于" v={v.desired.updated_at ? fmtDateTime(v.desired.updated_at) : '—'} />
        </dl>
        <p className="mt-2 text-[12px] leading-relaxed text-ink-3">
          这是保存的设置，不代表已经生效。
        </p>
      </div>

      <div className="min-w-0 rounded-lg bg-surface-2 p-3 sm:p-3.5">
        <ColumnHead note={v.edge_id === 'server' ? '最近一次收到' : '网关最近一次收到'}>当前运行情况</ColumnHead>
        {v.has_observed ? (
          <>
            <dl className="m-0">
              <Row k="运行情况" v={st.label} tone={st.tone === 'idle' ? undefined : st.tone} />
              <Row k="版本" v={v.observed?.version || '未给出'} />
              <Row k="健康情况" v={hl.label} tone={hl.tone === 'idle' ? undefined : hl.tone} />
              <Row k="重启次数" v={`${v.observed?.restart_count ?? 0} 次`}
                tone={(v.observed?.restart_count ?? 0) > 0 ? 'warn' : undefined} />
              <Row wrap k="更新时间" v={reportedAt ? fmtDateTime(reportedAt) : '—'} />
            </dl>
            {v.stale && (
              <p className="mt-2 flex items-start gap-1.5 rounded-lg bg-warn/12 px-2.5 py-2 text-[12px] leading-relaxed text-warn">
                <AlertTriangle size={12} className="mt-0.5 shrink-0" />
                <span className="min-w-0 break-words">
                  运行状态已过期 · 更新于 {reportedAt ? fmtDateTime(reportedAt) : '未知时间'}
                  {reportedAt ? `（${timeAgo(reportedAt)}）` : ''}
                </span>
              </p>
            )}
            {v.observed?.detail && hostDetailLabel(v.observed.detail) !== v.observed.detail && (
              <p className="mt-2 truncate text-[12px] text-ink-3" title={v.observed.detail}>
                说明：{hostDetailLabel(v.observed.detail)}
              </p>
            )}
          </>
        ) : (
          <UnreportedBlock v={v} />
        )}
      </div>
    </div>
    <details className="min-w-0 text-xs text-ink-2">
      <summary className="flex min-h-11 cursor-pointer items-center">技术详情</summary>
      <dl className="mt-2 space-y-1 rounded-lg bg-surface-2 px-3 py-2.5">
        <div><dt className="inline">设置版本：</dt><dd className="num inline">{v.desired_revision}</dd></div>
        <div><dt className="inline">运行状态版本：</dt><dd className="num inline">{v.applied_revision}</dd></div>
        <div><dt className="inline">运行状态原值：</dt><dd className="num inline break-all">{v.observed?.state || '—'}</dd></div>
        <div><dt className="inline">运行情况原值：</dt><dd className="num inline break-all">{v.observed?.health || '—'}</dd></div>
        {v.observed?.detail && <div><dt className="inline">状态说明：</dt><dd className="inline break-all">{v.observed.detail}</dd></div>}
      </dl>
    </details>
    </div>
  )
}
