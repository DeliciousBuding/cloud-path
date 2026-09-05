// 实例列表行：紧凑形态下也要把「期望」与「实际」分成两块写清楚，
// 不能合成一句「运行中 v1.2.0」—— 那正是把 desired 当 observed 的写法。
import { Link } from 'react-router'
import { Boxes } from 'lucide-react'
import { Badge, StatusDot } from '@/components/ui'
import { DesiredObserved, SyncBanner } from './DesiredObserved'
import { InstanceControls } from './InstanceControls'
import { healthMeta, isolationLabel, stateMeta, syncState } from '@/lib/plugins'
import { fmtDateTime } from '@/lib/format'
import type { PluginCatalogView, PluginInstanceView } from '@/lib/types'

export function InstanceRow({ v, catalog, onEdit }: {
  v: PluginInstanceView
  catalog?: PluginCatalogView
  onEdit?: () => void
}) {
  const s = syncState(v)
  const st = stateMeta(v.observed?.state)
  const hl = healthMeta(v.observed?.health)

  return (
    <section className="card p-4 fade-up sm:p-5">
      <div className="flex min-w-0 flex-wrap items-center gap-2">
        <StatusDot online={v.edge_online && v.has_observed && st.tone === 'ok'} />
        <Link to={`/plugins/${encodeURIComponent(v.id)}`}
          className="num min-w-0 max-w-full truncate text-[14px] font-semibold tracking-[-0.01em] no-underline hover:text-accent"
          title={`${v.id} · 查看详情`}>
          {v.desired.instance_id || v.id}
        </Link>
        <span className="flex min-w-0 items-center gap-1 font-mono text-[11px] text-ink-3"
          title={`插件 ${v.desired.plugin_id || '未知'}`}>
          <Boxes size={11} className="shrink-0" />
          <span className="min-w-0 truncate">{v.desired.plugin_id || '未知插件'}</span>
        </span>
        <span className="ml-auto shrink-0"><Badge tone={s.tone}>{s.label}</Badge></span>
      </div>

      {/* 期望 / 实际 两栏（紧凑版）：390px 也保持并排，因为对照本身就是信息 */}
      <div className="mt-3 grid grid-cols-2 gap-2.5">
        <div className="min-w-0 rounded-xl bg-surface-2 px-3 py-2.5">
          <p className="text-[12px] font-medium text-ink-3">期望态</p>
          <p className="mt-1 flex min-w-0 items-baseline gap-1 text-[12px] font-medium"
            title={`${v.desired.enabled ? '已启用' : '已停用'} · ${v.desired.version} · 修订版 ${v.desired_revision}`}>
            <span className="shrink-0">{v.desired.enabled ? '已启用' : '已停用'}</span>
            <span className="shrink-0 text-ink-3">·</span>
            <span className="num min-w-0 truncate">{v.desired.version || '—'}</span>
          </p>
          <p className="num mt-0.5 truncate text-[12px] text-ink-3">
            修订 {v.desired_revision} · {isolationLabel(v.desired.isolation)}
          </p>
        </div>
        <div className="min-w-0 rounded-xl bg-surface-2 px-3 py-2.5">
          <p className="text-[12px] font-medium text-ink-3">实际态</p>
          {v.has_observed ? (
            <>
              <p className={`mt-1 flex min-w-0 items-baseline gap-1 text-[12px] font-medium ${
                st.tone === 'ok' ? 'text-ok' : st.tone === 'bad' ? 'text-bad'
                  : st.tone === 'warn' ? 'text-warn' : ''}`}
                title={`${st.label} · ${v.observed?.version ?? '未给出版本'} · 已应用 ${v.applied_revision}`}>
                <span className="min-w-0 truncate">{st.label}</span>
                <span className="shrink-0 text-ink-3">·</span>
                <span className="num min-w-0 truncate">{v.observed?.version || '未给出'}</span>
              </p>
              <p className="num mt-0.5 truncate text-[12px] text-ink-3">
                已应用 {v.applied_revision} · {hl.label}
                {v.stale ? ' · stale' : ''}
              </p>
            </>
          ) : (
            <>
              <p className="mt-1 truncate text-[12px] font-medium text-ink-2">边缘节点未上报</p>
              <p className="mt-0.5 min-w-0 truncate text-[12px] text-ink-3">
                {v.edge_online ? '节点在线但还没回过' : '节点离线'}
              </p>
            </>
          )}
        </div>
      </div>

      <p className="num mt-2.5 truncate text-[12px] text-ink-3"
        title={`边缘节点 ${v.edge_id} · 最后回执 ${v.last_ack_at ? fmtDateTime(v.last_ack_at) : '尚无回执'}`}>
        边缘节点 {v.edge_id || '—'} · 最后回执 {v.last_ack_at ? fmtDateTime(v.last_ack_at) : '尚无回执'}
      </p>

      <div className="mt-3 border-t border-hairline pt-3">
        <InstanceControls v={v} catalog={catalog} onEdit={onEdit} />
      </div>
    </section>
  )
}

/** 详情页用的完整分离视图（顶部同步条 + 双栏全字段） */
export function InstanceSplit({ v }: { v: PluginInstanceView }) {
  return (
    <div className="space-y-4">
      <SyncBanner v={v} />
      <DesiredObserved v={v} />
    </div>
  )
}