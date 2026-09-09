// 插件实例的创建 / 编辑表单。
//
// 要点：
//   - Edge、插件、版本都是**选择**而不是自由发挥：Edge 取自 /api/edges，插件取自 /api/plugins，
//     避免用户手打一个不存在的 plugin_id 再吃一个 conflict；
//   - 选中插件若声明了权限，必须逐项核对并显式勾选后才允许提交（对应 confirm_permissions），
//     提交体里才带 confirm_permissions:true；
//   - 配置值可以是普通标量或 `secret://<name>` handle；界面上只呈现 handle 名，
//     并且明确说明明文不会经过 Server 与浏览器。
import { useMemo, useState } from 'react'
import { KeyRound, Plus, X } from 'lucide-react'
import { Button, TextField } from '@/components/ui'
import { PermissionList, PluginErrorNote } from './PluginFacts'
import { useEdges } from '@/hooks/useEdges'
import { useCreateInstance, useUpdateInstance } from '@/hooks/usePlugins'
import { optionLabel } from '@/lib/format'
import { normalizePluginKind, permissionCount, pluginDisplayName, secretHandleName } from '@/lib/plugins'
import type {
  PluginCatalogView, PluginInstanceCreateRequest, PluginInstanceUpdateRequest, PluginInstanceView,
} from '@/lib/types'

const ISOLATIONS = [
  { value: 'shared', label: '共享运行（默认）' },
  { value: 'per-instance', label: '每个实例独立运行' },
]

// 原生 select/option 不吃 CSS 截断：限宽 + overflow-hidden，option 文本另做收敛
const SELECT_CLS = 'input overflow-hidden text-[13px]'

interface ConfigRow { key: string; value: string }

function rowsFromConfig(config: Record<string, string> | undefined): ConfigRow[] {
  return Object.entries(config ?? {}).map(([key, value]) => ({ key, value: String(value ?? '') }))
}

/** 把 secret_refs 数组与输入框文本互转（一行一个 handle） */
function refsToText(refs: string[] | undefined): string {
  return (refs ?? []).map(secretHandleName).join('\n')
}

function legalIsolation(value: string | undefined): 'shared' | 'per-instance' {
  if (value === 'per-instance' || value === 'process' || value === 'container') return 'per-instance'
  return 'shared'
}

export function InstanceForm({ mode, instance, catalog, onDone }: {
  mode: 'create' | 'edit'
  /** edit 模式下的当前实例（用于预填） */
  instance?: PluginInstanceView | null
  /** 目录（提供插件候选与权限声明） */
  catalog: PluginCatalogView[]
  onDone: () => void
}) {
  const { list: edges } = useEdges()
  const d = instance?.desired

  const [edgeId, setEdgeId] = useState(instance?.edge_id ?? '')
  const [instanceId, setInstanceId] = useState(d?.instance_id ?? '')
  const [pluginId, setPluginId] = useState(d?.plugin_id ?? '')
  const [version, setVersion] = useState(d?.version ?? '')
  const [enabled, setEnabled] = useState(d?.enabled ?? true)
  const [isolation, setIsolation] = useState<'shared' | 'per-instance'>(() => legalIsolation(d?.isolation))
  const [rows, setRows] = useState<ConfigRow[]>(rowsFromConfig(d?.config))
  const [refsText, setRefsText] = useState(refsToText(d?.secret_refs))
  const [permAcked, setPermAcked] = useState(false)
  const [error, setError] = useState<unknown>(null)

  const effectivePluginId = pluginId || catalog[0]?.id || ''
  const selected = useMemo(() => catalog.find((p) => p.id === effectivePluginId), [catalog, effectivePluginId])
  const pluginKind = normalizePluginKind(selected?.kind)
  const perms = permissionCount(selected?.permissions)
  const effectiveVersion = version || selected?.version || ''
  const versionOptions = Array.from(new Set(
    [d?.version, selected?.version, effectiveVersion].filter((x): x is string => Boolean(x)),
  ))

  // 运行位置由插件类型决定：应用只能跑在中心服务，驱动只能跑在网关，连接器不创建运行实例。
  const effectiveEdge = edgeId || (pluginKind === 'application' ? 'server'
    : pluginKind === 'driver' ? edges[0]?.edge_id ?? '' : '')
  const hostMismatch = mode === 'edit' && (
    (pluginKind === 'application' && instance?.edge_id !== 'server')
    || (pluginKind === 'driver' && instance?.edge_id === 'server')
    || pluginKind === 'connector'
  )

  const secretRefs = useMemo(
    () => refsText.split(/[\n,]/).map((s) => s.trim()).filter(Boolean).map(secretHandleName),
    [refsText],
  )

  const missing: string[] = []
  if (mode === 'create' && !instanceId.trim()) missing.push('名称或标识')
  if (!effectivePluginId.trim()) missing.push('插件')
  if (pluginKind === 'connector') missing.push('连接器不能创建运行实例')
  if (pluginKind !== 'application' && pluginKind !== 'driver' && pluginKind !== 'connector') missing.push('插件类型')
  if (mode === 'create' && pluginKind !== 'connector' && pluginKind !== 'unknown' && !effectiveEdge) missing.push('运行位置')
  if (hostMismatch) missing.push('运行位置与插件类型不匹配')
  if (!effectiveVersion.trim()) missing.push('版本')
  if (perms > 0 && !permAcked) missing.push('权限确认')

  function buildConfig(): Record<string, string> | undefined {
    const out: Record<string, string> = {}
    let any = false
    for (const r of rows) {
      const k = r.key.trim()
      if (!k) continue
      out[k] = r.value
      any = true
    }
    return any ? out : undefined
  }

  return (
    <form
      className="space-y-4"
      noValidate
      onSubmit={(e) => { e.preventDefault() }}
    >
      <div className="grid gap-4 sm:grid-cols-2">
        {mode === 'create' ? (
          <>
            <div>
              <label htmlFor="pi-edge" className="mb-1.5 block text-[13px] font-medium text-ink-2">运行位置</label>
              {pluginKind === 'connector' ? (
                <p className="rounded-lg bg-surface-2 px-3.5 py-2.5 text-[13px] text-ink-2">
                  连接器只负责连接，不能创建运行实例。
                </p>
              ) : pluginKind === 'application' ? (
                <select id="pi-edge" className={SELECT_CLS} value="server" disabled>
                  <option value="server">中心服务</option>
                </select>
              ) : pluginKind === 'driver' ? (
                <select id="pi-edge" className={SELECT_CLS} value={effectiveEdge} onChange={(e) => setEdgeId(e.target.value)}>
                  {edges.length === 0 && <option value="">（还没有可用网关）</option>}
                  {edges.map((ed) => (
                    <option key={ed.edge_id} value={ed.edge_id}>
                      {optionLabel(`${ed.edge_id}${ed.online ? '' : '（离线）'}`, 40)}
                    </option>
                  ))}
                </select>
              ) : (
                <p className="rounded-lg bg-surface-2 px-3.5 py-2.5 text-[13px] text-ink-2">
                  无法判断插件类型，暂时不能选择运行位置。
                </p>
              )}
              <p className="mt-1.5 text-xs text-ink-3">
                {pluginKind === 'application'
                  ? '应用只能运行在中心服务。'
                  : pluginKind === 'driver'
                    ? '驱动只能运行在网关；离线网关也可以先保存设置。'
                    : '请选择类型明确的插件。'}
              </p>
            </div>
            <TextField
              label="名称或标识" value={instanceId} placeholder="例如 compartment-main"
              autoComplete="off" spellCheck={false}
              hint="同一网关内不能重复；创建后不能修改，建议使用英文和短横线"
              onChange={(e) => setInstanceId(e.target.value)}
            />
          </>
        ) : (
          <div className="sm:col-span-2">
            <p className="rounded-lg bg-surface-2 px-3.5 py-2.5 text-xs break-words text-ink-2">
              运行位置：{instance?.edge_id === 'server' ? '中心服务' : `网关 ${instance?.edge_id || '—'}`} · 名称：{d?.instance_id || '—'}
            </p>
            {hostMismatch && (
              <p role="alert" className="mt-2 rounded-lg bg-warn/12 px-3 py-2 text-[12px] leading-relaxed text-warn">
                当前运行位置与插件类型不匹配。为避免保存明显错误的设置，请先删除后按正确类型重新创建。
              </p>
            )}
          </div>
        )}

        <div>
          <label htmlFor="pi-plugin" className="mb-1.5 block text-[13px] font-medium text-ink-2">插件</label>
          {mode === 'edit' ? (
            <div id="pi-plugin" className="input flex min-w-0 items-center text-[13px] text-ink-2">
              <span className="min-w-0 truncate">{pluginDisplayName(selected)}</span>
            </div>
          ) : catalog.length > 0 ? (
            <select id="pi-plugin" className={SELECT_CLS} value={effectivePluginId}
              onChange={(e) => {
                const nextID = e.target.value
                const next = catalog.find((p) => p.id === nextID)
                const nextKind = normalizePluginKind(next?.kind)
                setPluginId(nextID)
                setVersion(next?.version ?? '')
                setEdgeId(nextKind === 'application' ? 'server'
                  : nextKind === 'driver' ? edges[0]?.edge_id ?? '' : '')
              }}>
              {catalog.map((p) => (
                <option key={p.id} value={p.id}>
                  {optionLabel(`${pluginDisplayName(p)}${p.version ? ` · ${p.version}` : ''}${p.verified ? '' : '（未验证）'}`, 40)}
                </option>
              ))}
            </select>
          ) : (
            <p className="rounded-lg bg-surface-2 px-3.5 py-2.5 text-[13px] text-ink-2">
              暂无可选插件。请先确认插件是否已经同步，再回来添加。
            </p>
          )}
          <p className="mt-1.5 text-xs text-ink-3">
            {mode === 'edit' ? '创建后不能更换插件' : catalog.length > 0 ? '从已同步的插件中选择' : '没有可用插件，暂时无法添加'}
          </p>
        </div>

        <div>
          <label htmlFor="pi-version" className="mb-1.5 block text-[13px] font-medium text-ink-2">版本</label>
          {versionOptions.length > 0 ? (
            <select id="pi-version" className={SELECT_CLS} value={effectiveVersion}
              onChange={(e) => setVersion(e.target.value)}>
              {versionOptions.map((v) => <option key={v} value={v}>{v}</option>)}
            </select>
          ) : (
            <input id="pi-version" className="input text-[13px]" value={version} placeholder="例如 v1.2.0"
              autoComplete="off" spellCheck={false} onChange={(e) => setVersion(e.target.value)} />
          )}
          <p className="mt-1.5 text-xs text-ink-3">
            {selected?.version ? `可用版本：${selected.version}` : '暂未取得可用版本，请稍后重试'}
          </p>
        </div>

        <div>
          <label htmlFor="pi-iso" className="mb-1.5 block text-[13px] font-medium text-ink-2">隔离级别</label>
          <select id="pi-iso" className={SELECT_CLS} value={isolation} onChange={(e) => setIsolation(e.target.value as 'shared' | 'per-instance')}>
            {ISOLATIONS.map((o) => <option key={o.value} value={o.value}>{o.label}</option>)}
          </select>
        </div>

        <div className="flex items-end pb-1">
          <label className="flex cursor-pointer items-center gap-2.5 text-[13px]">
            <input type="checkbox" checked={enabled} onChange={(e) => setEnabled(e.target.checked)}
              className="h-4 w-4 shrink-0 accent-accent" />
            创建后立即启用
          </label>
        </div>
      </div>

      {/* ---- 权限确认 ---- */}
      <div className="rounded-lg bg-surface-2 p-3.5">
        <p className="mb-2 text-[13px] font-medium">该插件需要的权限</p>
        <PermissionList
          permissions={selected?.permissions}
          emptyHint={selected ? '不需要额外权限' : '可用插件列表中没有这个插件，无法核对权限'}
        />
        {perms > 0 && (
          <label className="mt-3 flex cursor-pointer items-start gap-2.5 border-t border-hairline pt-3">
            <input type="checkbox" checked={permAcked} onChange={(e) => setPermAcked(e.target.checked)}
              className="mt-0.5 h-4 w-4 shrink-0 accent-accent" />
            <span className="min-w-0 text-[12px] leading-relaxed">
              我已核对上述 {perms} 项权限，同意授予。提交时会确认这些权限。
            </span>
          </label>
        )}
      </div>

      {/* ---- 非敏感配置 ---- */}
      <div>
        <div className="mb-2 flex items-center justify-between gap-2">
          <p className="text-[13px] font-medium">应用设置（可选）</p>
          <Button type="button" variant="ghost" onClick={() => setRows((r) => [...r, { key: '', value: '' }])}>
            <Plus size={13} /> 添加设置项
          </Button>
        </div>
        {rows.length === 0 ? (
          <p className="text-xs text-ink-3">没有额外设置。需要密钥时请使用下面的密钥名称。</p>
        ) : (
          <div className="space-y-2">
            {rows.map((r, i) => (
              <div key={i} className="flex min-w-0 gap-2">
                <label className="sr-only" htmlFor={`cfg-k-${i}`}>设置名称</label>
                <input id={`cfg-k-${i}`} className="input num min-w-0 flex-1 font-mono text-[12px]"
                  placeholder="设置名称" value={r.key} autoComplete="off" spellCheck={false}
                  onChange={(e) => setRows((prev) => prev.map((x, j) => (j === i ? { ...x, key: e.target.value } : x)))} />
                <label className="sr-only" htmlFor={`cfg-v-${i}`}>设置值</label>
                <input id={`cfg-v-${i}`} className="input num min-w-0 flex-1 font-mono text-[12px]"
                  placeholder="设置值" value={r.value} autoComplete="off" spellCheck={false}
                  onChange={(e) => setRows((prev) => prev.map((x, j) => (j === i ? { ...x, value: e.target.value } : x)))} />
                <button type="button" aria-label={`删除配置项 ${r.key || i + 1}`}
                  className="flex h-9 w-9 shrink-0 items-center justify-center rounded-full text-ink-3 transition-colors hover:text-bad"
                  onClick={() => setRows((prev) => prev.filter((_, j) => j !== i))}>
                  <X size={14} />
                </button>
              </div>
            ))}
          </div>
        )}
      </div>

      {/* ---- secret handle ---- */}
      <div>
        <label htmlFor="pi-refs" className="mb-1.5 flex items-center gap-1.5 text-[13px] font-medium">
          <KeyRound size={13} className="shrink-0" /> 使用的密钥（可选）
        </label>
        <textarea
          id="pi-refs" rows={3} value={refsText} autoComplete="off" spellCheck={false}
          placeholder={'例如：提醒服务密钥\n自动化密钥'}
          onChange={(e) => setRefsText(e.target.value)}
          className="input num resize-y font-mono text-[12px]"
        />
        <p className="mt-1.5 text-[12px] leading-relaxed text-ink-3">
          每行填一个密钥名称，不要填密钥内容。平台只会保存名称，不会显示密钥内容。
        </p>
        <details className="mt-1.5 text-[12px] text-ink-3">
          <summary className="cursor-pointer">技术详情</summary>
          <p className="mt-1.5 leading-relaxed">可填写 <span className="num">secret://</span> 前缀，也可以只填写名称；两种写法都会按名称保存。</p>
        </details>
        {secretRefs.length > 0 && (
          <ul className="mt-2 flex list-none flex-wrap gap-1.5 p-0">
            {secretRefs.map((n) => (
              <li key={n} className="badge max-w-full bg-ink-3/10 text-ink-2">
                <span className="min-w-0 truncate break-all">{n}</span>
              </li>
            ))}
          </ul>
        )}
      </div>

      {missing.length > 0 && (
        <p className="text-[12px] text-ink-3">还需要处理：{missing.join('、')}</p>
      )}
      {error ? <PluginErrorNote error={error} /> : null}

      <div className="flex flex-col-reverse gap-2.5 border-t border-hairline pt-4 sm:flex-row sm:justify-end">
        <Button type="button" variant="ghost" onClick={onDone}>取消</Button>
        <SubmitButton
          mode={mode} disabled={missing.length > 0}
          onCreate={() => {
            setError(null)
            const body: PluginInstanceCreateRequest = {
              edge_id: effectiveEdge,
              instance_id: instanceId.trim(),
              plugin_id: effectivePluginId.trim(),
              version: effectiveVersion.trim(),
              enabled,
              isolation,
              config: buildConfig(),
              secret_refs: secretRefs.length > 0 ? secretRefs : undefined,
              confirm_permissions: perms > 0 ? true : undefined,
            }
            return body
          }}
          onUpdate={() => {
            setError(null)
            const body: PluginInstanceUpdateRequest = {
              version: effectiveVersion.trim() || undefined,
              enabled,
              isolation,
              config: buildConfig(),
              secret_refs: secretRefs,
              confirm_permissions: perms > 0 ? true : undefined,
            }
            return body
          }}
          onError={setError}
          instanceId={instance?.id ?? ''}
          onDone={onDone}
        />
      </div>
    </form>
  )
}

/** 提交按钮：mutation 收在一处。成功后关闭表单 —— 界面上的新事实一律由服务端投影给出，
 *  前端不做乐观更新（否则就会出现「点了启用就当作设备已执行」的假象）。 */
function SubmitButton({ mode, disabled, onCreate, onUpdate, onError, instanceId, onDone }: {
  mode: 'create' | 'edit'
  disabled: boolean
  onCreate: () => PluginInstanceCreateRequest
  onUpdate: () => PluginInstanceUpdateRequest
  onError: (e: unknown) => void
  instanceId: string
  onDone: () => void
}) {
  const create = useCreateInstance()
  const update = useUpdateInstance()
  const busy = create.isPending || update.isPending

  async function run() {
    try {
      if (mode === 'create') await create.mutateAsync(onCreate())
      else await update.mutateAsync({ id: instanceId, body: onUpdate() })
      onDone()
    } catch (e) {
      onError(e) // 稳定码文案由 PluginErrorNote 呈现
    }
  }

  return (
    <Button type="button" disabled={disabled || busy} onClick={() => void run()}>
      {busy ? '提交中…' : mode === 'create' ? '创建并保存' : '保存修改'}
    </Button>
  )
}
