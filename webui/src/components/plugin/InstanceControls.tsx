// 运行实例的写操作面：启停 / 编辑 / 重新应用 / 删除，以及权限扩大的显式确认。
//
// 主路径只展示常用动作；重新应用只在状态需要处理时出现，删除收进详情页的「更多操作」。
// 写完只失效查询，由服务端投影决定新事实 —— 绝不因为按钮点了、请求 200 就把保存的设置当作已运行。
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
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
  const { t } = useTranslation('plugin')
  const readOnly = useAuth((s) => s.status === 'in' && s.user?.role === 'viewer')
  const host = v.edge_id === 'server' ? t('host.server') : t('host.edge')
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

  if (readOnly) return <p className="text-body text-ink-3">{t('controls.readOnly')}</p>

  const toggleLabel = v.desired.enabled ? t('controls.disable') : t('controls.enable')
  const needsReapply = v.drift || v.stale || !v.has_observed

  return (
    <div className="min-w-0">
      <div className="flex flex-wrap items-center gap-2">
        <button
          type="button" className="btn btn-ghost" disabled={busy}
          onClick={() => void patch({ enabled: !v.desired.enabled })}
          title={t('controls.toggleTitle', { action: toggleLabel, host })}
        >
          <Power size={13} className="shrink-0" />
          {update.isPending ? t('controls.submitting') : toggleLabel}
        </button>

        {variant === 'detail' && needsReapply && (
          <button
            type="button" className="btn btn-ghost" disabled={busy}
            onClick={() => setReconcileOpen(true)}
            title={t('controls.reapplyTitle', { host })}
          >
            <RefreshCw size={13} className="shrink-0" />
            {reconcile.isPending ? t('controls.applying') : t('controls.reapply')}
          </button>
        )}

        {showEdit && onEdit && (
          <button type="button" className="btn btn-ghost" disabled={busy} onClick={onEdit}>
            <Pencil size={13} className="shrink-0" /> {t('common.edit')}
          </button>
        )}
      </div>

      {variant === 'detail' && (
        <details className="mt-3 border-t border-hairline pt-3">
          <summary className="flex min-h-touch cursor-pointer items-center text-meta text-ink-2">{t('controls.more')}</summary>
          <button
            type="button" className="btn btn-danger-ghost mt-2" disabled={busy}
            onClick={() => { setPurge(false); setDeleteOpen(true) }}
          >
            <Trash2 size={13} className="shrink-0" /> {t('controls.deleteInstance')}
          </button>
        </details>
      )}

      {error ? <PluginErrorNote error={error} className="mt-3" /> : null}

      {/* ---- 权限扩大确认 ---- */}
      <ConfirmDialog
        open={pendingPerm !== null}
        tone="warn"
        title={t('controls.permissionTitle')}
        body={
          <>
            <p>{t('controls.permissionBody')}</p>
            <div className="mt-3 rounded-tile bg-surface-2 p-3">
              <PermissionList
                permissions={catalog?.permissions}
                emptyHint={t('controls.permissionUnknown')}
              />
            </div>
          </>
        }
        confirmLabel={t('controls.confirmSave')}
        requireAck={t('controls.permissionAck')}
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
        title={t('controls.reconcileTitle')}
        body={
          <>
            <p>
              {!v.has_observed
                ? t('controls.reconcileUnreported', { host })
                : t('controls.reconcileDrift', { host })}
            </p>
            <p className="mt-2 text-meta text-ink-2">
              {v.edge_id !== 'server' && !v.edge_online && t('controls.reconcileOffline')}
              {v.edge_id !== 'server' && v.edge_online && t('controls.reconcileOnline')}
              {v.edge_id === 'server' && t('controls.reconcileServer')}
            </p>
          </>
        }
        confirmLabel={t('controls.reconcileConfirm')}
        busy={reconcile.isPending}
        extra={
          <label className="flex cursor-pointer items-start gap-2.5 rounded-tile bg-surface-2 p-3">
            <input type="checkbox" checked={purge} onChange={(e) => setPurge(e.target.checked)}
              className="mt-0.5 h-4 w-4 shrink-0 accent-accent" />
            <span className="min-w-0 text-meta leading-relaxed">
              {t('controls.forceReconcile', { host })}
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
        title={t('controls.deleteTitle', { id: v.desired.instance_id })}
        body={
          <>
            <p>
              {t('controls.deleteBody', { host })}
            </p>
            <p className="mt-2 text-meta text-ink-3">
              {t('controls.deleteMeta', { location: v.edge_id === 'server' ? t('host.server') : t('location.edge', { id: v.edge_id || '—' }), version: v.desired.version || '—' })}
            </p>
          </>
        }
        confirmLabel={t('controls.deleteInstance')}
        busy={remove.isPending}
        requireAck={t('controls.deleteAck')}
        extra={
          <label className="flex cursor-pointer items-start gap-2.5 rounded-tile bg-surface-2 p-3">
            <input type="checkbox" checked={purge} onChange={(e) => setPurge(e.target.checked)}
              className="mt-0.5 h-4 w-4 shrink-0 accent-accent" />
            <span className="min-w-0 text-meta leading-relaxed">
              {t('controls.purge')}
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
