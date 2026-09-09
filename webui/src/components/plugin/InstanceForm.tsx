// 插件实例的创建 / 编辑表单。
//
// 创建路径先选「应用或驱动」，运行位置随类型确定：应用走中心服务，驱动选网关；
// 连接器不出现在创建候选中。版本和运行方式用下拉选择，低频的配置与密钥收进高级参数。
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
  { value: 'shared', label: '共享运行（多个项目共用运行资源）' },
  { value: 'per-instance', label: '独立运行（每个项目单独运行）' },
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

export function InstanceForm({ mode, instance, catalog, initialPluginId, onDone }: {
  mode: 'create' | 'edit'
  /** edit 模式下的当前实例（用于预填） */
  instance?: PluginInstanceView | null
  /** 目录（提供插件候选与权限声明） */
  catalog: PluginCatalogView[]
  /** 从「可用插件」卡片进入时预选插件；只接受可创建的应用/驱动 */
  initialPluginId?: string
  onDone: () => void
}) {
  const { list: edges } = useEdges()
  const d = instance?.desired

  const [edgeId, setEdgeId] = useState(instance?.edge_id ?? '')
  const [instanceId, setInstanceId] = useState(d?.instance_id ?? '')
  const [pluginId, setPluginId] = useState(d?.plugin_id ?? initialPluginId ?? '')
  const [version, setVersion] = useState(d?.version ?? '')
  const [enabled, setEnabled] = useState(d?.enabled ?? true)
  const [isolation, setIsolation] = useState<'shared' | 'per-instance'>(() => legalIsolation(d?.isolation))
  const [rows, setRows] = useState<ConfigRow[]>(rowsFromConfig(d?.config))
  const [refsText, setRefsText] = useState(refsToText(d?.secret_refs))
  const [permAcked, setPermAcked] = useState(false)
  const [error, setError] = useState<unknown>(null)

  const createOptions = useMemo(() => catalog.filter((p) => {
    const kind = normalizePluginKind(p.kind)
    return kind === 'application' || kind === 'driver'
  }), [catalog])
  const preferredCreate = useMemo(() => {
    const verified = createOptions.filter((p) => p.verified)
    return verified.find((p) => normalizePluginKind(p.kind) === 'application')
      ?? verified.find((p) => normalizePluginKind(p.kind) === 'driver')
      ?? createOptions.find((p) => normalizePluginKind(p.kind) === 'application')
      ?? createOptions.find((p) => normalizePluginKind(p.kind) === 'driver')
  }, [createOptions])
  const effectivePluginId = pluginId || (mode === 'create' ? preferredCreate?.id : catalog[0]?.id) || ''
  const selected = useMemo(() => catalog.find((p) => p.id === effectivePluginId), [catalog, effectivePluginId])
  const pluginKind = normalizePluginKind(selected?.kind)
  const perms = permissionCount(selected?.permissions)
  const effectiveVersion = version || selected?.version || ''
  const versionOptions = Array.from(new Set(
    [d?.version, selected?.version, effectiveVersion].filter((x): x is string => Boolean(x)),
  ))

  // 运行位置由插件类型决定：应用只能跑在中心服务，驱动只能跑在网关，连接器不创建运行实例。
  const preferredEdge = edges.find((edge) => edge.online)?.edge_id ?? edges[0]?.edge_id ?? ''
  const effectiveEdge = pluginKind === 'application' ? 'server'
    : pluginKind === 'driver' ? (edgeId || preferredEdge) : ''
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
  if (mode === 'create' && !instanceId.trim()) missing.push('实例名称')
  if (!effectivePluginId.trim()) missing.push('要运行的应用或驱动')
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
      <div className="rounded-lg bg-surface-2 p-3.5">
        <label htmlFor="pi-plugin" className="mb-1.5 block text-[13px] font-medium text-ink-2">
          要运行什么
        </label>
        {mode === 'edit' ? (
          <div id="pi-plugin" className="input flex min-w-0 items-center text-[13px] text-ink-2">
            <span className="min-w-0 truncate">{pluginDisplayName(selected)}</span>
          </div>
        ) : createOptions.length > 0 ? (
          <select id="pi-plugin" className={SELECT_CLS} value={effectivePluginId}
            onChange={(e) => {
              const nextID = e.target.value
              const next = catalog.find((p) => p.id === nextID)
              const nextKind = normalizePluginKind(next?.kind)
              setPluginId(nextID)
              setVersion(next?.version ?? '')
              setEdgeId(nextKind === 'application' ? 'server' : '')
            }}>
            <optgroup label="中心服务应用">
              {createOptions.filter((p) => normalizePluginKind(p.kind) === 'application').map((p) => (
                <option key={p.id} value={p.id}>
                  {optionLabel(`${pluginDisplayName(p)}${p.version ? ` · ${p.version}` : ''}${p.verified ? '' : '（未验证）'}`, 40)}
                </option>
              ))}
            </optgroup>
            <optgroup label="网关驱动">
              {createOptions.filter((p) => normalizePluginKind(p.kind) === 'driver').map((p) => (
                <option key={p.id} value={p.id}>
                  {optionLabel(`${pluginDisplayName(p)}${p.version ? ` · ${p.version}` : ''}${p.verified ? '' : '（未验证）'}`, 40)}
                </option>
              ))}
            </optgroup>
          </select>
        ) : (
          <p className="rounded-lg bg-surface px-3.5 py-2.5 text-[13px] text-ink-2">
            暂无可用于创建实例的应用或驱动。连接器用于连接服务，不会创建运行实例。
          </p>
        )}
        <p className="mt-1.5 text-xs leading-relaxed text-ink-3">
          {mode === 'edit' ? '创建后不能更换插件。' : '应用会自动运行在中心服务；驱动需要选择网关。连接器不会出现在这里。'}
        </p>
      </div>

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
                  {edges.map((edge) => (
                    <option key={edge.edge_id} value={edge.edge_id}>
                      {optionLabel(`${edge.edge_id}${edge.online ? '' : '（离线）'}`, 40)}
                    </option>
                  ))}
                </select>
              ) : (
                <p className="rounded-lg bg-surface-2 px-3.5 py-2.5 text-[13px] text-ink-2">
                  请先选择要运行的应用或驱动。
                </p>
              )}
              <p className="mt-1.5 text-xs text-ink-3">
                {pluginKind === 'application'
                  ? '应用由中心服务运行。'
                  : pluginKind === 'driver'
                    ? '驱动在网关运行；离线网关也可以先保存设置。'
                    : '运行位置会随插件类型自动确定。'}
              </p>
            </div>
            <TextField
              label="实例名称" value={instanceId} placeholder="例如 living-room"
              autoComplete="off" spellCheck={false}
              hint="同一条运行位置不能重名；建议使用英文和短横线，创建后不能修改。"
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
            {selected?.version ? `当前可用版本：${selected.version}` : '暂未取得可用版本，请稍后重试。'}
          </p>
        </div>

        <div>
          <label htmlFor="pi-iso" className="mb-1.5 block text-[13px] font-medium text-ink-2">运行方式</label>
          <select id="pi-iso" className={SELECT_CLS} value={isolation} onChange={(e) => setIsolation(e.target.value as 'shared' | 'per-instance')}>
            {ISOLATIONS.map((o) => <option key={o.value} value={o.value}>{o.label}</option>)}
          </select>
          <p className="mt-1.5 text-xs text-ink-3">不确定时保持共享运行即可。</p>
        </div>

        <div className="flex items-end pb-1">
          <label className="flex cursor-pointer items-center gap-2.5 text-[13px]">
            <input type="checkbox" checked={enabled} onChange={(e) => setEnabled(e.target.checked)}
              className="h-4 w-4 shrink-0 accent-accent" />
            {mode === 'create' ? '保存后立即启用' : '保存后启用'}
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

      <details className="rounded-lg border border-hairline p-3.5" open={mode === 'edit' && (rows.length > 0 || refsText.length > 0)}>
        <summary className="cursor-pointer text-[13px] font-medium">高级参数（可选）</summary>
        <div className="mt-4 space-y-4">
          <div>
            <div className="mb-2 flex items-center justify-between gap-2">
              <p className="text-[13px] font-medium">应用参数</p>
              <Button type="button" variant="ghost" onClick={() => setRows((r) => [...r, { key: '', value: '' }])}>
                <Plus size={13} /> 添加参数
              </Button>
            </div>
            {rows.length === 0 ? (
              <p className="text-xs text-ink-3">没有额外参数。需要密钥时请使用下面的密钥名称。</p>
            ) : (
              <div className="space-y-2">
                {rows.map((r, i) => (
                  <div key={i} className="flex min-w-0 gap-2">
                    <label className="sr-only" htmlFor={`cfg-k-${i}`}>参数名称</label>
                    <input id={`cfg-k-${i}`} className="input num min-w-0 flex-1 font-mono text-[12px]"
                      placeholder="参数名称" value={r.key} autoComplete="off" spellCheck={false}
                      onChange={(e) => setRows((prev) => prev.map((x, j) => (j === i ? { ...x, key: e.target.value } : x)))} />
                    <label className="sr-only" htmlFor={`cfg-v-${i}`}>参数值</label>
                    <input id={`cfg-v-${i}`} className="input num min-w-0 flex-1 font-mono text-[12px]"
                      placeholder="参数值" value={r.value} autoComplete="off" spellCheck={false}
                      onChange={(e) => setRows((prev) => prev.map((x, j) => (j === i ? { ...x, value: e.target.value } : x)))} />
                    <button type="button" aria-label={`删除参数 ${r.key || i + 1}`}
                      className="flex h-9 w-9 shrink-0 items-center justify-center rounded-full text-ink-3 transition-colors hover:text-bad"
                      onClick={() => setRows((prev) => prev.filter((_, j) => j !== i))}>
                      <X size={14} />
                    </button>
                  </div>
                ))}
              </div>
            )}
          </div>

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
        </div>
      </details>

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
