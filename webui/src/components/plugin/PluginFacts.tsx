// 插件实例的其余事实面：Version / Edge / Trust / Permissions / Health / Revision / Last ACK，
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
  permissionGroups, pluginErrorCopy, safeConfigEntries, secretHandleName, shortDigest, trustMeta,
} from '@/lib/plugins'
import { fmtDateTime } from '@/lib/format'
import type { PluginCatalogView, PluginInstanceView, PluginPermissionsData } from '@/lib/types'

/** 错误码 → 设计过的提示块（按稳定码呈现，不复述服务端文本） */
export function PluginErrorNote({ error, className }: { error: unknown; className?: string }) {
  const copy = pluginErrorCopy(error)
  const box = copy.tone === 'bad' ? 'bg-bad/10 text-bad'
    : copy.tone === 'warn' ? 'bg-warn/12 text-warn' : 'bg-ink-3/10 text-ink-2'
  return (
    <div role="alert" className={`rounded-xl px-3.5 py-3 ${box} ${className ?? ''}`}>
      <p className="text-[13px] font-semibold break-words">{copy.title}</p>
      <p className="mt-0.5 text-[12px] leading-relaxed break-words opacity-90">{copy.hint}</p>
      {copy.code && (
        <p className="num mt-1.5 text-[10px] opacity-70">错误码 {copy.code}</p>
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
          <p className="mb-1 flex items-center gap-1.5 text-[11px] font-medium text-ink-2">
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
                  <span className="min-w-0 truncate break-all">{item}</span>
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
      <p className="mt-1.5 text-[11px] leading-relaxed text-ink-3">
        只显示 handle 名。明文只在 Edge 本地的 secret provider 与插件进程内存中，Server 与浏览器都拿不到。
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

/** Version / Edge / Trust / Health / Revision / Last ACK 一览 */
export function InstanceFacts({ v, catalog }: { v: PluginInstanceView; catalog?: PluginCatalogView }) {
  const trust = catalog ? trustMeta(undefined, catalog.verified) : null
  const rows: { k: string; node: ReactNode }[] = [
    { k: '实例 ID', node: <span className="num min-w-0 truncate font-mono" title={v.id}>{v.id}</span> },
    { k: '插件', node: <span className="num min-w-0 truncate font-mono" title={v.desired.plugin_id}>{v.desired.plugin_id || '—'}</span> },
    { k: '边缘节点', node: <span className="num min-w-0 truncate font-mono" title={v.edge_id}>{v.edge_id || '—'}</span> },
    { k: '边缘节点在线', node: v.edge_online ? '是' : '否' },
    { k: '期望版本', node: v.desired.version || '—' },
    { k: '实际版本', node: v.has_observed ? (v.observed?.version || '未给出') : '未上报' },
    { k: '期望修订版', node: String(v.desired_revision) },
    { k: '已应用修订版', node: String(v.applied_revision) },
    { k: '最后回执', node: v.last_ack_at ? fmtDateTime(v.last_ack_at) : '尚无回执' },
  ]
  return (
    <dl className="m-0 space-y-2.5">
      {rows.map((r) => <KeyValue key={r.k} k={r.k} v={r.node} />)}
      <div className="flex min-w-0 items-baseline justify-between gap-2 border-t border-hairline pt-2.5">
        <dt className="shrink-0 text-[13px] text-ink-2">信任目录</dt>
        <dd className="min-w-0 truncate text-right">
          {trust
            ? <Badge tone={trust.tone}>{trust.label}</Badge>
            : <span className="text-[12px] text-ink-3">目录未提供</span>}
        </dd>
      </div>
      {catalog && (
        <div className="flex min-w-0 items-baseline justify-between gap-2">
          <dt className="shrink-0 text-[13px] text-ink-2">Digest</dt>
          <dd className="num min-w-0 truncate text-right text-[12px] text-ink-2" title={catalog.digest}>
            {shortDigest(catalog.digest)}
          </dd>
        </div>
      )}
      {catalog?.compatibility && (
        <div className="flex min-w-0 items-baseline justify-between gap-2">
          <dt className="shrink-0 text-[13px] text-ink-2">兼容性</dt>
          <dd className="min-w-0 truncate text-right text-[12px]" title={catalog.compatibility}>
            {catalog.compatibility}
          </dd>
        </div>
      )}
    </dl>
  )
}