// 运行实例的其余事实面：Version / 网关 / Trust / Permissions / Health / Revision / Last ACK，
// 以及 secret handle、非敏感配置与错误码呈现。
//
// 安全边界（control-plane-sync.md 不变量 6、任务书 §6.5）：
//   - secret 只显示 **handle 名**，明文永不出现在 DOM；
//   - 不呈现本机绝对路径（目录视图的 source 字段可能是路径，一律不渲染）；
//   - 不呈现插件 stdout/stderr 原文（observed.detail 是 server 限长脱敏后的摘要）。
import type { ReactNode } from 'react'
import { KeyRound, Lock, ShieldCheck, ShieldAlert } from 'lucide-react'
import { Badge, KeyValue } from '@/components/ui'
import {
  permissionGroups, permissionItemLabel, pluginDisplayName, pluginErrorCopy, safeConfigEntries,
  secretHandleName, shortDigest, trustMeta,
} from '@/lib/plugins'
import { fmtDateTime } from '@/lib/format'
import type { PluginCatalogView, PluginInstanceView, PluginPermissionsData } from '@/lib/types'

/** 错误码 → 设计过的提示块（按稳定码呈现，不复述服务端文本） */
export function PluginErrorNote({ error, className }: { error: unknown; className?: string }) {
  const copy = pluginErrorCopy(error)
  const box = copy.tone === 'bad' ? 'bg-bad/10 text-bad'
    : copy.tone === 'warn' ? 'bg-warn/12 text-warn' : 'bg-ink-3/10 text-ink-2'
  return (
    <div role="alert" className={`rounded-lg px-3.5 py-3 ${box} ${className ?? ''}`}>
      <p className="text-[13px] font-semibold break-words">{copy.title}</p>
      <p className="mt-0.5 text-[12px] leading-relaxed break-words opacity-90">{copy.hint}</p>
      {copy.code && (
        <details className="mt-2 min-w-0">
          <summary className="flex min-h-11 cursor-pointer items-center text-[12px] opacity-80">技术详情</summary>
          <p className="num mt-1 break-all text-[12px] opacity-70">错误码 {copy.code}</p>
        </details>
      )}
    </div>
  )
}

/** 权限声明清单：按硬件/网络/文件系统/secret 分组，未声明的组不出现 */
export function PermissionList({ permissions, emptyHint }: {
  permissions: PluginPermissionsData | undefined
  emptyHint?: string
}) {
  const groups = permissionGroups(permissions)
  if (groups.length === 0) {
    return <p className="py-2 text-[12px] text-ink-3">{emptyHint ?? '该插件没有声明任何权限'}</p>
  }
  return (
    <div className="space-y-2.5">
      {groups.map((g) => (
        <div key={g.key} className="min-w-0">
          <p className="mb-1 flex items-center gap-1.5 text-[12px] font-medium text-ink-2">
            {g.key === 'secrets'
              ? <KeyRound size={11} className="shrink-0" />
              : g.key === 'network'
                ? <ShieldAlert size={11} className="shrink-0" />
                : <ShieldCheck size={11} className="shrink-0" />}
            {g.group}
            <span className="num text-ink-3">{g.items.length}</span>
          </p>
          <ul className="m-0 flex list-none flex-wrap gap-1.5 p-0">
            {g.items.map((item) => (
              <li key={item} className="min-w-0 max-w-full">
                <Badge tone={g.tone} className="max-w-full">
                  <span className="min-w-0 truncate break-all">{permissionItemLabel(g.key, item)}</span>
                </Badge>
              </li>
            ))}
          </ul>
        </div>
      ))}
    </div>
  )
}

/** secret handle 清单：只有名字，并明确说明明文不在这里 */
export function SecretRefList({ refs }: { refs: string[] | undefined }) {
  // 只取 handle 名；去重后排序，避免同一 handle 重复占位
  const entries = [...new Set((refs ?? []).map(secretHandleName))].sort()
  if (entries.length === 0) {
    return <p className="py-1 text-[12px] text-ink-3">未引用任何密钥</p>
  }
  return (
    <div>
      <ul className="m-0 flex list-none flex-wrap gap-1.5 p-0">
        {entries.map((name) => (
          <li key={name} className="min-w-0 max-w-full">
            <span className="badge max-w-full bg-ink-3/10 text-ink-2" title={`密钥引用：${name}`}>
              <Lock size={10} className="shrink-0" />
              <span className="min-w-0 truncate break-all">{name}</span>
            </span>
          </li>
        ))}
      </ul>
      <p className="mt-1.5 text-[12px] leading-relaxed text-ink-3">
        这里只显示密钥名称，不会显示密钥内容。
      </p>
    </div>
  )
}

/** 非敏感配置：secret:// 值自动折叠成 handle 名 */
export function ConfigTable({ config }: { config: Record<string, string> | undefined }) {
  const rows = safeConfigEntries(config)
  if (rows.length === 0) return <p className="py-1 text-[12px] text-ink-3">没有配置项</p>
  return (
    <dl className="m-0">
      {rows.map((r) => (
        <KeyValue
          key={r.key}
          k={<span className="num min-w-0 truncate font-mono text-[12px]" title={r.key}>{r.key}</span>}
          v={r.isSecret
            ? <span className="flex min-w-0 items-center justify-end gap-1"><Lock size={10} className="shrink-0" /><span className="truncate">{r.value}</span></span>
            : <span className="num min-w-0 truncate font-mono" title={r.value}>{r.value}</span>}
        />
      ))}
    </dl>
  )
}

/** 基本信息与技术详情一览 */
export function InstanceFacts({ v, catalog }: { v: PluginInstanceView; catalog?: PluginCatalogView }) {
  const trust = catalog ? trustMeta(undefined, catalog.verified) : null
  const rows: { k: string; node: ReactNode }[] = [
    { k: '插件', node: <span className="min-w-0 truncate" title={pluginDisplayName(catalog)}>{pluginDisplayName(catalog)}</span> },
    ...(v.edge_id === 'server' ? [{ k: '运行位置', node: '中心服务' }] : [
      { k: '运行位置', node: <span className="num min-w-0 truncate font-mono" title={v.edge_id}>网关 {v.edge_id || '—'}</span> },
      { k: '网关状态', node: v.edge_online ? '在线' : '离线' },
    ]),
    { k: '期望版本', node: v.desired.version || '—' },
    { k: '实际版本', node: v.has_observed ? (v.observed?.version || '未给出') : '未上报' },
  ]
  return (
    <dl className="m-0 space-y-2.5">
      {rows.map((r) => <KeyValue key={r.k} k={r.k} v={r.node} />)}
      <div className="flex min-w-0 items-baseline justify-between gap-2 border-t border-hairline pt-2.5">
        <dt className="shrink-0 text-[13px] text-ink-2">来源验证</dt>
        <dd className="min-w-0 truncate text-right">
          {trust
            ? <Badge tone={trust.tone}>{trust.label}</Badge>
            : <span className="text-[12px] text-ink-3">未提供</span>}
        </dd>
      </div>
      <details className="min-w-0 border-t border-hairline pt-2.5 text-xs text-ink-2">
        <summary className="flex min-h-11 cursor-pointer items-center">技术详情</summary>
        <div className="mt-2 space-y-1.5">
          <div className="flex min-w-0 items-baseline justify-between gap-2">
            <dt className="shrink-0">实例 ID</dt>
            <dd className="num min-w-0 truncate text-right font-mono" title={v.id}>{v.id}</dd>
          </div>
          <div className="flex min-w-0 items-baseline justify-between gap-2">
            <dt className="shrink-0">插件标识</dt>
            <dd className="num min-w-0 truncate text-right font-mono" title={v.desired.plugin_id}>{v.desired.plugin_id || '—'}</dd>
          </div>
          <div className="flex min-w-0 items-baseline justify-between gap-2">
            <dt className="shrink-0">设置版本</dt>
            <dd className="num min-w-0 truncate text-right">{v.desired_revision}</dd>
          </div>
          <div className="flex min-w-0 items-baseline justify-between gap-2">
            <dt className="shrink-0">运行状态版本</dt>
            <dd className="num min-w-0 truncate text-right">{v.applied_revision}</dd>
          </div>
          <div className="flex min-w-0 items-baseline justify-between gap-2">
            <dt className="shrink-0">最近更新</dt>
            <dd className="num min-w-0 truncate text-right">{v.last_ack_at ? fmtDateTime(v.last_ack_at) : '尚未同步'}</dd>
          </div>
          {catalog && (
            <div className="flex min-w-0 items-baseline justify-between gap-2">
              <dt className="shrink-0">安装摘要</dt>
              <dd className="num min-w-0 truncate text-right" title={catalog.digest}>{shortDigest(catalog.digest)}</dd>
            </div>
          )}
          {catalog?.compatibility && (
            <div className="flex min-w-0 items-baseline justify-between gap-2">
              <dt className="shrink-0">兼容性</dt>
              <dd className="min-w-0 truncate text-right" title={catalog.compatibility}>{catalog.compatibility}</dd>
            </div>
          )}
        </div>
      </details>
    </dl>
  )
}
