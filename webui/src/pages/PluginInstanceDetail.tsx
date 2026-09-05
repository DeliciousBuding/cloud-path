import { useMemo, useState } from 'react'
import { Link, useParams } from 'react-router'
import {
  Boxes, KeyRound, Puzzle, Server, Settings2, ShieldCheck, SlidersHorizontal,
} from 'lucide-react'
import { BackLink, Badge, EmptyState, ErrorState, Panel } from '@/components/ui'
import { RowSkeleton } from '@/components/Skeleton'
import { InstanceSplit } from '@/components/plugin/InstanceRow'
import { InstanceControls } from '@/components/plugin/InstanceControls'
import { InstanceForm } from '@/components/plugin/InstanceForm'
import { ConfigTable, InstanceFacts, PermissionList, SecretRefList } from '@/components/plugin/PluginFacts'
import { usePluginCatalog, usePluginInstance } from '@/hooks/usePlugins'
import { syncState } from '@/lib/plugins'

/**
 * 插件实例详情：期望态与实际态**分离**呈现，外加 Version / Edge / Trust / Permissions /
 * Health / Revision / Last ACK。写操作（启停 / reconcile / 编辑 / 删除）都在这一页可完成，
 * 390px 下同样可操作。
 */
export default function PluginInstanceDetail() {
  const { id = '' } = useParams()
  const key = decodeURIComponent(id)
  const { instance, loading, error } = usePluginInstance(key)
  const { plugins } = usePluginCatalog()
  const [editing, setEditing] = useState(false)

  const catalog = useMemo(
    () => (instance ? plugins.find((p) => p.id === instance.desired.plugin_id) : undefined),
    [plugins, instance],
  )

  if (loading) {
    return (
      <>
        <BackLink to="/plugins" label="插件" />
        <Panel><RowSkeleton rows={5} /></Panel>
      </>
    )
  }

  if (!instance) {
    return (
      <>
        <BackLink to="/plugins" label="插件" />
        {error ? (
          <ErrorState icon={<Boxes size={20} />} title="实例加载失败"
            hint={`拿不到 ${key} 的实例投影。可能实例已删除、不属于当前租户，或 server 不可达。`} />
        ) : (
          <EmptyState icon={<Boxes size={24} />} title="实例不存在"
            hint={`没有找到 ${key}。实例可能已被删除，或不属于当前租户。`} />
        )}
      </>
    )
  }

  const s = syncState(instance)

  return (
    <>
      <BackLink to="/plugins" label="插件" />

      <header className="mb-5 flex flex-wrap items-center gap-2.5 fade-up">
        <h1 className="num min-w-0 max-w-full truncate text-[22px] font-semibold tracking-tight" title={instance.id}>
          {instance.desired.instance_id || instance.id}
        </h1>
        <Badge tone="idle" className="max-w-full">
          <Puzzle size={11} className="shrink-0" />
          <span className="num min-w-0 truncate">{instance.desired.plugin_id || '未知插件'}</span>
        </Badge>
        <Link to={`/edges/${encodeURIComponent(instance.edge_id)}`}
          className="badge max-w-full bg-ink-3/10 text-ink-2 no-underline transition-colors hover:bg-accent/10 hover:text-accent"
          title={`边缘节点 ${instance.edge_id}`}>
          <Server size={11} className="shrink-0" />
          <span className="num min-w-0 truncate">{instance.edge_id || '—'}</span>
        </Link>
        <Badge tone={instance.edge_online ? 'ok' : 'idle'}>
          {instance.edge_online ? 'Edge 在线' : 'Edge 离线'}
        </Badge>
        <span className="ml-auto shrink-0"><Badge tone={s.tone}>{s.label}</Badge></span>
      </header>

      {editing ? (
        <Panel title={<span className="flex items-center gap-1.5"><SlidersHorizontal size={14} />编辑期望态</span>}
          className="mb-5">
          <InstanceForm mode="edit" instance={instance} catalog={plugins} onDone={() => setEditing(false)} />
        </Panel>
      ) : (
        <>
          <div className="grid items-start gap-5 lg:grid-cols-3">
            <Panel className="lg:col-span-2"
              title={<span className="flex items-center gap-1.5"><Boxes size={14} />期望态与实际态</span>}>
              <InstanceSplit v={instance} />
            </Panel>

            <Panel title={<span className="flex items-center gap-1.5"><Settings2 size={14} />事实一览</span>}>
              <InstanceFacts v={instance} catalog={catalog} />
            </Panel>

            <Panel title={<span className="flex items-center gap-1.5"><ShieldCheck size={14} />权限</span>}
              right={catalog ? undefined : <Badge tone="idle">目录未提供</Badge>}>
              <PermissionList
                permissions={catalog?.permissions}
                emptyHint={catalog
                  ? '该插件没有声明任何权限'
                  : '目录里没有这个插件的声明，无法核对权限。期望态与实际态不受影响。'}
              />
              {catalog?.verified && (
                <p className="mt-3 border-t border-hairline pt-3 text-[11px] leading-relaxed text-ink-3">
                  该插件的安装物已通过验证（digest 一致），权限声明来自其 manifest。
                </p>
              )}
            </Panel>

            <Panel title={<span className="flex items-center gap-1.5"><KeyRound size={14} />Secret handle</span>}>
              <SecretRefList refs={instance.desired.secret_refs} />
            </Panel>

            <Panel title={<span className="flex items-center gap-1.5"><SlidersHorizontal size={14} />配置（非敏感）</span>}>
              <ConfigTable config={instance.desired.config} />
              <p className="mt-3 border-t border-hairline pt-3 text-[10.5px] leading-relaxed text-ink-3">
                配置里若出现 secret:// 引用，这里只显示 handle 名。插件的运行日志与本机路径不会出现在这一页。
              </p>
            </Panel>
          </div>

          <Panel className="mt-5" title="操作">
            <InstanceControls v={instance} catalog={catalog} showEdit={false}
              onEdit={() => setEditing(true)} />
            <button type="button" className="btn btn-ghost mt-3" onClick={() => setEditing(true)}>
              <SlidersHorizontal size={13} /> 编辑期望态（版本 / 隔离 / 配置 / secret handle）
            </button>
          </Panel>
        </>
      )}
    </>
  )
}
