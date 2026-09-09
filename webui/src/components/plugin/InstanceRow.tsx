// 实例列表行：主路径只回答「是否运行、在哪里、是否异常、下一步做什么」，
// 版本号、状态原值等工程字段收进折叠的技术详情。
import { Link } from 'react-router'
import { ArrowRight, Boxes, Server } from 'lucide-react'
import { Badge, StatusDot } from '@/components/ui'
import { DesiredObserved, SyncBanner } from './DesiredObserved'
import { InstanceControls } from './InstanceControls'
import {
  healthMeta, instanceLocationLabel, instanceStatus, isolationLabel, pluginDisplayName, stateMeta,
} from '@/lib/plugins'
import { fmtDateTime } from '@/lib/format'
import type { PluginCatalogView, PluginInstanceView } from '@/lib/types'

export function InstanceRow({ v, catalog, onEdit }: {
  v: PluginInstanceView
  catalog?: PluginCatalogView
  onEdit?: () => void
}) {
  const status = instanceStatus(v)
  const st = stateMeta(v.observed?.state)
  const hl = healthMeta(v.observed?.health)
  const serverHosted = v.edge_id === 'server'
  const hostLocation = instanceLocationLabel(v)

  return (
    <section className="card p-4 fade-up sm:p-5">
      <div className="flex min-w-0 flex-wrap items-center gap-2">
        <StatusDot online={status.key === 'normal'} />
        <Link to={`/plugins/${encodeURIComponent(v.id)}`}
          className="num min-w-0 max-w-full truncate text-[14px] font-semibold tracking-[-0.01em] no-underline hover:text-accent"
          title={`${v.id} · 查看详情`}>
          {v.desired.instance_id || v.id}
        </Link>
        <span className="flex min-w-0 items-center gap-1 text-[12px] text-ink-3"
          title={pluginDisplayName(catalog)}>
          <Boxes size={11} className="shrink-0" />
          <span className="min-w-0 truncate">{pluginDisplayName(catalog)}</span>
        </span>
        <span className="ml-auto shrink-0"><Badge tone={status.tone}>{status.label}</Badge></span>
      </div>

      <div className="mt-2 flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 text-[12px] text-ink-3">
        <span className="flex min-w-0 items-center gap-1">
          <Server size={11} className="shrink-0" />
          {serverHosted ? <span>中心服务</span> : (
            <Link to={`/edges/${encodeURIComponent(v.edge_id)}`}
              className="min-w-0 truncate no-underline transition-colors hover:text-accent"
              title={hostLocation}>{hostLocation}</Link>
          )}
        </span>
        {v.last_ack_at && <><span aria-hidden="true">·</span><span>更新于 {fmtDateTime(v.last_ack_at)}</span></>}
      </div>

      <div className="mt-3 grid grid-cols-2 gap-2.5">
        <div className="min-w-0 rounded-lg bg-surface-2 px-3 py-2.5">
          <p className="text-[12px] font-medium text-ink-3">保存的设置</p>
          <p className="mt-1 flex min-w-0 items-baseline gap-1 text-[12px] font-medium"
            title={`${v.desired.enabled ? '已启用' : '已停用'} · ${v.desired.version}`}>
            <span className="shrink-0">{v.desired.enabled ? '已启用' : '已停用'}</span>
            <span className="shrink-0 text-ink-3">·</span>
            <span className="num min-w-0 truncate">{v.desired.version || '—'}</span>
          </p>
        </div>
        <div className="min-w-0 rounded-lg bg-surface-2 px-3 py-2.5">
          <p className="text-[12px] font-medium text-ink-3">当前运行情况</p>
          {v.has_observed ? (
            <>
              <p className={`mt-1 flex min-w-0 items-baseline gap-1 text-[12px] font-medium ${
                status.tone === 'ok' ? 'text-ok' : status.tone === 'bad' ? 'text-bad'
                  : status.tone === 'warn' ? 'text-warn' : ''}`}
                title={`${st.label} · ${v.observed?.version ?? '未给出版本'}`}>
                <span className="min-w-0 truncate">{status.summary}</span>
              </p>
              <p className="mt-0.5 truncate text-[12px] text-ink-3">
                {v.observed?.health ? '健康：' + hl.label : '健康状态未上报'}
                {v.stale ? ' · 状态可能不是最新' : ''}
              </p>
            </>
          ) : (
            <>
              <p className="mt-1 truncate text-[12px] font-medium text-ink-2">状态待确认</p>
              <p className="mt-0.5 min-w-0 truncate text-[12px] text-ink-3">
                {serverHosted ? '还没有收到中心服务的运行状态' : v.edge_online ? '网关在线，还没有收到运行状态' : '网关离线'}
              </p>
            </>
          )}
        </div>
      </div>

      {status.needsAttention && (
        <div className="mt-3 rounded-lg bg-warn/10 px-3 py-2.5 text-[12px] leading-relaxed">
          <p className="font-medium text-warn">{status.label}</p>
          {status.next && <p className="mt-1 text-ink-2">下一步：{status.next}</p>}
        </div>
      )}

      <details className="mt-3 min-w-0 text-xs text-ink-2">
        <summary className="cursor-pointer">技术详情</summary>
        <dl className="mt-2 space-y-1 rounded-lg bg-surface-2 px-3 py-2.5">
          <div><dt className="inline">运行位置：</dt><dd className="inline">{hostLocation}</dd></div>
          <div><dt className="inline">运行方式：</dt><dd className="inline">{isolationLabel(v.desired.isolation)}</dd></div>
          <div><dt className="inline">最近更新：</dt><dd className="num inline">{v.last_ack_at ? fmtDateTime(v.last_ack_at) : '尚未更新'}</dd></div>
          <div><dt className="inline">插件标识：</dt><dd className="num inline break-all">{v.desired.plugin_id || '—'}</dd></div>
          <div><dt className="inline">当前版本：</dt><dd className="num inline">{v.has_observed ? (v.observed?.version || '未给出') : '未上报'}</dd></div>
          <div><dt className="inline">设置版本：</dt><dd className="num inline">{v.desired_revision}</dd></div>
          <div><dt className="inline">运行状态版本：</dt><dd className="num inline">{v.applied_revision}</dd></div>
          <div><dt className="inline">运行状态原值：</dt><dd className="num inline break-all">{v.observed?.state || '—'}</dd></div>
          <div><dt className="inline">健康状态原值：</dt><dd className="num inline break-all">{v.observed?.health || '—'}</dd></div>
        </dl>
      </details>

      <div className="mt-3 flex flex-wrap items-center gap-2 border-t border-hairline pt-3">
        <Link to={`/plugins/${encodeURIComponent(v.id)}`} className="btn btn-primary">
          查看详情 <ArrowRight size={13} />
        </Link>
        <InstanceControls v={v} catalog={catalog} onEdit={onEdit} variant="list" />
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
