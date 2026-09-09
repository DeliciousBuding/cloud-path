// 插件实例的写操作面：启停 / 编辑 / 重新应用 / 删除，以及权限扩大的显式确认。
//
// 主路径只展示常用动作；重新应用只在状态需要处理时出现，删除收进详情页的「更多操作」。
// 写完只失效查询，由服务端投影决定新事实 —— 绝不因为按钮点了、请求 200 就把保存的设置当作已运行。
import { useState } from 'react'
import { useAuth } from '@/store/auth'
import { Pencil, Power, RefreshCw, Trash2 } from 'lucide-react'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { PermissionList, PluginErrorNote } from './PluginFacts'
import { pluginErrorCopy } from '@/lib/plugins'
import {
  useDeleteInstance, useReconcileInstance, useUpdateInstance,
} from '@/hooks/usePlugins'
import type { PluginCatalogView, PluginInstanceUpdateRequest, PluginInstanceView } from '@/lib/types'

export function InstanceControls({ v, catalog, onEdit, showEdit = true, variant = 'detail' }: {
  v: PluginInstanceView
  /** 目录里的插件声明（用于权限扩大确认时列出将要授予的权限） */
  catalog?: PluginCatalogView
  onEdit?: () => void
  showEdit?: boolean
  /** list 只保留高频动作；detail 才展示重新应用和删除 */
  variant?: 'list' | 'detail'
}) {
  const readOnly = useAuth((s) => s.status === 'in' && s.user?.role === 'viewer')
  const host = v.edge_id === 'server' ? '中心服务' : '网关'
  const update = useUpdateInstance()
  const remove = useDeleteInstance()
  const reconcile = useReconcileInstance()

  const [deleteOpen, setDeleteOpen] = useState(false)
  const [purge, setPurge] = useState(false)
  const [reconcileOpen, setReconcileOpen] = useState(false)
  /** 因权限扩大被拒后，待用户确认再重发的 payload */
  const [pendingPerm, setPendingPerm] = useState<PluginInstanceUpdateRequest | null>(null)

  const busy = update.isPending || remove.isPending || reconcile.isPending
  // 三个 mutation 的错误合并呈现（同一时刻只会有一个在飞）
  const error = update.error ?? remove.error ?? reconcile.error

  async function patch(body: PluginInstanceUpdateRequest) {
    try {
      await update.mutateAsync({ id: v.id, body })
      setPendingPerm(null)
    } catch (e) {
      // 权限扩大：把同一份 payload 记下来，等用户显式确认后带 confirm_permissions 重发
      if (pluginErrorCopy(e).needsPermissionConfirm) setPendingPerm(body)
    }
  }

  if (readOnly) return <p className="text-sm text-ink-3">当前账号只能查看，不能修改这个项目。</p>

  const toggleLabel = v.desired.enabled ? '停用' : '启用'
  const needsReapply = v.drift || v.stale || !v.has_observed

  return (
    <div className="min-w-0">
      <div className="flex flex-wrap items-center gap-2">
        <button
          type="button" className="btn btn-ghost" disabled={busy}
          onClick={() => void patch({ enabled: !v.desired.enabled })}
          title={`将保存的设置改为${toggleLabel}；${host}应用后才真正生效`}
        >
          <Power size={13} className="shrink-0" />
          {update.isPending ? '提交中…' : toggleLabel}
        </button>

        {variant === 'detail' && needsReapply && (
          <button
            type="button" className="btn btn-ghost" disabled={busy}
            onClick={() => setReconcileOpen(true)}
            title={'让' + host + '重新应用最新设置'}
          >
            <RefreshCw size={13} className="shrink-0" />
            {reconcile.isPending ? '正在应用…' : '重新应用设置'}
          </button>
        )}

        {showEdit && onEdit && (
          <button type="button" className="btn btn-ghost" disabled={busy} onClick={onEdit}>
            <Pencil size={13} className="shrink-0" /> 编辑
          </button>
        )}
      </div>

      {variant === 'detail' && (
        <details className="mt-3 border-t border-hairline pt-3">
          <summary className="cursor-pointer text-xs text-ink-2">更多操作</summary>
          <button
            type="button" className="btn btn-danger-ghost mt-2" disabled={busy}
            onClick={() => { setPurge(false); setDeleteOpen(true) }}
          >
            <Trash2 size={13} className="shrink-0" /> 删除实例
          </button>
        </details>
      )}

      {error ? <PluginErrorNote error={error} className="mt-3" /> : null}

      {/* ---- 权限扩大确认 ---- */}
      <ConfirmDialog
        open={pendingPerm !== null}
        tone="warn"
        title="这次修改会增加插件权限"
        body={
          <>
            <p>请先核对下面新增的权限，确认后再保存：</p>
            <div className="mt-3 rounded-lg bg-surface-2 p-3">
              <PermissionList
                permissions={catalog?.permissions}
                emptyHint="可用插件列表中没有这个插件的权限信息，无法核对，建议先确认插件来源再重试。"
              />
            </div>
          </>
        }
        confirmLabel="确认并保存"
        requireAck="我已核对上述权限，同意授予该插件这些权限。"
        busy={update.isPending}
        onCancel={() => setPendingPerm(null)}
        onConfirm={() => {
          const body = pendingPerm
          setPendingPerm(null)
          if (body) void patch({ ...body, confirm_permissions: true })
        }}
      />

      {/* ---- 重新应用确认：可能重启实例，必须显式确认 ---- */}
      <ConfirmDialog
        open={reconcileOpen}
        tone="warn"
        title="重新应用最新设置？"
        body={
          <>
            <p>
              {!v.has_observed
                ? `还没有收到${host}的运行状态。重新应用可能会重启这个项目，请确认后再继续。`
                : `${host}还没有应用最新设置。重新应用可能会重启这个项目，请确认后再继续。`}
            </p>
            <p className="mt-2 text-xs text-ink-2">
              {v.edge_id !== 'server' && !v.edge_online && '注意：该网关当前离线，重新连接后才会应用。'}
              {v.edge_id !== 'server' && v.edge_online && '该网关在线，通常会立即开始同步。'}
              {v.edge_id === 'server' && '由中心服务处理；请以更新后的运行状态为准。'}
            </p>
          </>
        }
        confirmLabel="重新应用"
        busy={reconcile.isPending}
        extra={
          <label className="flex cursor-pointer items-start gap-2.5 rounded-lg bg-surface-2 p-3">
            <input type="checkbox" checked={purge} onChange={(e) => setPurge(e.target.checked)}
              className="mt-0.5 h-4 w-4 shrink-0 accent-accent" />
            <span className="min-w-0 text-[12px] leading-relaxed">
              强制重新应用：即使{host}认为当前设置已经生效，也再执行一次
            </span>
          </label>
        }
        onCancel={() => { setReconcileOpen(false); setPurge(false) }}
        onConfirm={() => {
          setReconcileOpen(false)
          void reconcile.mutateAsync({ id: v.id, body: { force: purge } })
            .catch(() => { /* 错误由下方 PluginErrorNote 呈现 */ })
            .finally(() => setPurge(false))
        }}
      />

      {/* ---- 删除确认（不可逆，必须勾选） ---- */}
      <ConfirmDialog
        open={deleteOpen}
        tone="danger"
        title={`删除实例 ${v.desired.instance_id}？`}
        body={
          <>
            <p>
              删除后，{host}会在下一次更新时停止这个项目。操作会留下记录。
            </p>
            <p className="mt-2 text-xs text-ink-3">
              运行位置：{v.edge_id === 'server' ? '中心服务' : `网关 ${v.edge_id || '—'}`} · 版本：{v.desired.version || '—'}
            </p>
          </>
        }
        confirmLabel="删除实例"
        busy={remove.isPending}
        requireAck="我确认要删除这个运行实例。"
        extra={
          <label className="flex cursor-pointer items-start gap-2.5 rounded-lg bg-surface-2 p-3">
            <input type="checkbox" checked={purge} onChange={(e) => setPurge(e.target.checked)}
              className="mt-0.5 h-4 w-4 shrink-0 accent-accent" />
            <span className="min-w-0 text-[12px] leading-relaxed">
              同时删除运行数据：该应用保存的数据会一并删除，<span className="font-semibold text-bad">且无法恢复</span>
            </span>
          </label>
        }
        onCancel={() => { setDeleteOpen(false); setPurge(false) }}
        onConfirm={() => {
          setDeleteOpen(false)
          void remove.mutateAsync({ id: v.id, body: { purge } })
            .catch(() => { /* 错误由下方 PluginErrorNote 呈现 */ })
            .finally(() => setPurge(false))
        }}
      />
    </div>
  )
}
