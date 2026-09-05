// 插件实例的写操作面：启停 / reconcile / 删除，以及**权限扩大的显式确认**。
//
// 三条硬约束：
//   ① 写完只失效查询，由服务端投影决定新事实 —— 绝不因为「按钮点了、请求 200」
//      就把 desired.enabled 渲染成 observed 运行中；
//   ② 错误按 PluginErr* 稳定码呈现（lib/plugins.ts pluginErrorCopy），不解析服务端文本；
//   ③ 收到 plugin_permission_confirmation_required 时弹出权限清单要求显式确认，
//      用户勾选后才带 confirm_permissions:true 重发同一份 payload。
import { useState } from 'react'
import { Link } from 'react-router'
import { Pencil, Power, RefreshCw, Trash2 } from 'lucide-react'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { PermissionList, PluginErrorNote } from './PluginFacts'
import { pluginErrorCopy } from '@/lib/plugins'
import {
  useDeleteInstance, useReconcileInstance, useUpdateInstance,
} from '@/hooks/usePlugins'
import type { PluginCatalogView, PluginInstanceUpdateRequest, PluginInstanceView } from '@/lib/types'

export function InstanceControls({ v, catalog, onEdit, showEdit = true }: {
  v: PluginInstanceView
  /** 目录里的插件声明（用于权限扩大确认时列出将要授予的权限） */
  catalog?: PluginCatalogView
  onEdit?: () => void
  showEdit?: boolean
}) {
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

  const toggleLabel = v.desired.enabled ? '停用' : '启用'

  return (
    <div className="min-w-0">
      <div className="flex flex-wrap items-center gap-2">
        <button
          type="button" className="btn btn-ghost" disabled={busy}
          onClick={() => void patch({ enabled: !v.desired.enabled })}
          title={`把期望态改成${toggleLabel}；Edge 应用后才真正生效`}
        >
          <Power size={13} className="shrink-0" />
          {update.isPending ? '提交中…' : toggleLabel}
        </button>

        <button
          type="button" className="btn btn-ghost" disabled={busy}
          // 不一致或过期时值得问一下（可能强制重启实例），一致时直接下发
          onClick={() => (v.drift || v.stale ? setReconcileOpen(true) : void reconcile.mutateAsync({ id: v.id }).catch(() => {}))}
          title="让 Edge 重新收敛到最新期望快照"
        >
          <RefreshCw size={13} className="shrink-0" />
          {reconcile.isPending ? '下发中…' : '重新下发'}
        </button>

        {showEdit && onEdit && (
          <button type="button" className="btn btn-ghost" disabled={busy} onClick={onEdit}>
            <Pencil size={13} className="shrink-0" /> 编辑
          </button>
        )}

        <Link to={`/plugins/${encodeURIComponent(v.id)}`} className="btn btn-ghost no-underline">
          详情
        </Link>

        <button
          type="button" className="btn btn-danger-ghost ml-auto" disabled={busy}
          onClick={() => { setPurge(false); setDeleteOpen(true) }}
        >
          <Trash2 size={13} className="shrink-0" /> 删除
        </button>
      </div>

      {error ? <PluginErrorNote error={error} className="mt-3" /> : null}

      {/* ---- 权限扩大确认 ---- */}
      <ConfirmDialog
        open={pendingPerm !== null}
        tone="warn"
        title="这次变更会扩大插件权限"
        body={
          <>
            <p>服务端要求显式确认后才接受这次写入。下面是该插件声明的权限，请逐项核对：</p>
            <div className="mt-3 rounded-xl bg-surface-2 p-3">
              <PermissionList
                permissions={catalog?.permissions}
                emptyHint="目录里没有这个插件的权限声明，无法核对，建议先确认插件来源再重试。"
              />
            </div>
          </>
        }
        confirmLabel="确认并重新提交"
        requireAck="我已核对上述权限，同意授予该插件这些权限。"
        busy={update.isPending}
        onCancel={() => setPendingPerm(null)}
        onConfirm={() => {
          const body = pendingPerm
          setPendingPerm(null)
          if (body) void patch({ ...body, confirm_permissions: true })
        }}
      />

      {/* ---- reconcile 确认（仅在 drift/stale 时出现） ---- */}
      <ConfirmDialog
        open={reconcileOpen}
        tone="warn"
        title="重新下发期望快照？"
        body={
          <>
            <p>
              当前期望修订版 {v.desired_revision}，边缘节点已应用 {v.applied_revision}。
              重新下发会让边缘节点重新收敛到最新完整快照。
            </p>
            <p className="mt-2 text-xs text-ink-2">
              {!v.edge_online && '注意：该边缘节点当前离线，快照会在它重连后才被应用。'}
              {v.edge_online && '该 Edge 在线，通常会立即开始应用。'}
            </p>
          </>
        }
        confirmLabel="下发"
        busy={reconcile.isPending}
        extra={
          <label className="flex cursor-pointer items-start gap-2.5 rounded-xl bg-surface-2 p-3">
            <input type="checkbox" checked={purge} onChange={(e) => setPurge(e.target.checked)}
              className="mt-0.5 h-4 w-4 shrink-0 accent-accent" />
            <span className="min-w-0 text-[12px] leading-relaxed">
              强制（force）：即使 Edge 认为已应用同一 revision 也重新执行一次
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
              期望态会被移除，Edge 在下一次快照同步时停止该实例。这是一次写操作，会记入审计。
            </p>
            <p className="num mt-2 text-xs text-ink-3 break-all">
              {v.edge_id} · {v.desired.plugin_id} · {v.desired.version}
            </p>
          </>
        }
        confirmLabel="删除实例"
        busy={remove.isPending}
        requireAck="我确认要删除这个插件实例。"
        extra={
          <label className="flex cursor-pointer items-start gap-2.5 rounded-xl bg-surface-2 p-3">
            <input type="checkbox" checked={purge} onChange={(e) => setPurge(e.target.checked)}
              className="mt-0.5 h-4 w-4 shrink-0 accent-accent" />
            <span className="min-w-0 text-[12px] leading-relaxed">
              同时清除本地数据（purge）：插件在 Edge 上产生的数据一并删除，**不可恢复**
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