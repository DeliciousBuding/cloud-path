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
import { permissionCount, secretHandleName } from '@/lib/plugins'
import type {
  PluginCatalogView, PluginInstanceCreateRequest, PluginInstanceUpdateRequest, PluginInstanceView,
} from '@/lib/types'

const ISOLATIONS = [
  { value: 'process', label: '独立进程（process）' },
  { value: 'container', label: '容器（container）' },
  { value: 'none', label: '无隔离（none）' },
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

  const [edgeId, setEdgeId] = useState(instance?.edge_id ?? edges[0]?.edge_id ?? '')
  const [instanceId, setInstanceId] = useState(d?.instance_id ?? '')
  const [pluginId, setPluginId] = useState(d?.plugin_id ?? catalog[0]?.id ?? '')
  const [version, setVersion] = useState(d?.version ?? '')
  const [enabled, setEnabled] = useState(d?.enabled ?? true)
  const [isolation, setIsolation] = useState(d?.isolation || 'process')
  const [rows, setRows] = useState<ConfigRow[]>(rowsFromConfig(d?.config))
  const [refsText, setRefsText] = useState(refsToText(d?.secret_refs))
  const [permAcked, setPermAcked] = useState(false)
  const [error, setError] = useState<unknown>(null)

  const selected = useMemo(() => catalog.find((p) => p.id === pluginId), [catalog, pluginId])
  const perms = permissionCount(selected?.permissions)

  // Edge 列表异步到达时补一个默认值（不覆盖用户已选）
  const effectiveEdge = edgeId || edges[0]?.edge_id || ''

  const secretRefs = useMemo(
    () => refsText.split(/[\n,]/).map((s) => s.trim()).filter(Boolean).map(secretHandleName),
    [refsText],
  )

  const missing: string[] = []
  if (mode === 'create' && !effectiveEdge) missing.push('目标 Edge')
  if (mode === 'create' && !instanceId.trim()) missing.push('实例 ID')
  if (!pluginId.trim()) missing.push('插件')
  if (!version.trim()) missing.push('版本')
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
              <label htmlFor="pi-edge" className="mb-1.5 block text-[13px] font-medium text-ink-2">目标 Edge</label>
              <select id="pi-edge" className={SELECT_CLS} value={effectiveEdge} onChange={(e) => setEdgeId(e.target.value)}>
                {edges.length === 0 && <option value="">（还没有边缘节点）</option>}
                {edges.map((ed) => (
                  <option key={ed.edge_id} value={ed.edge_id}>
                    {optionLabel(`${ed.edge_id}${ed.online ? '' : '（离线）'}`, 40)}
                  </option>
                ))}
              </select>
              <p className="mt-1.5 text-xs text-ink-3">
                离线的 Edge 也可以写期望态；它重连后会自动应用最新完整快照。
              </p>
            </div>
            <TextField
              label="实例 ID" value={instanceId} placeholder="例如 compartment-main"
              autoComplete="off" spellCheck={false}
              hint="同一 Edge 内唯一；创建后不可改"
              onChange={(e) => setInstanceId(e.target.value)}
            />
          </>
        ) : (
          <div className="sm:col-span-2">
            <p className="num rounded-xl bg-surface-2 px-3.5 py-2.5 text-xs break-all text-ink-2">
              {instance?.edge_id} · {d?.instance_id}
            </p>
          </div>
        )}

        <div>
          <label htmlFor="pi-plugin" className="mb-1.5 block text-[13px] font-medium text-ink-2">插件</label>
          {catalog.length > 0 ? (
            <select id="pi-plugin" className={SELECT_CLS} value={pluginId} onChange={(e) => setPluginId(e.target.value)}>
              {catalog.map((p) => (
                <option key={p.id} value={p.id}>
                  {optionLabel(`${p.id}${p.version ? ` · ${p.version}` : ''}${p.verified ? '' : '（未验证）'}`, 40)}
                </option>
              ))}
            </select>
          ) : (
            <input id="pi-plugin" className="input text-[13px]" value={pluginId} placeholder="插件 ID"
              autoComplete="off" spellCheck={false} onChange={(e) => setPluginId(e.target.value)} />
          )}
          <p className="mt-1.5 text-xs text-ink-3">
            {catalog.length > 0 ? '候选来自插件目录（GET /api/plugins）' : '目录为空，需手动填写插件 ID'}
          </p>
        </div>

        <TextField
          label="版本" value={version} placeholder="例如 v1.2.0"
          autoComplete="off" spellCheck={false}
          hint={selected?.version ? `目录里当前是 ${selected.version}` : '固定版本，Edge 只会安装这个版本'}
          onChange={(e) => setVersion(e.target.value)}
        />

        <div>
          <label htmlFor="pi-iso" className="mb-1.5 block text-[13px] font-medium text-ink-2">隔离级别</label>
          <select id="pi-iso" className={SELECT_CLS} value={isolation} onChange={(e) => setIsolation(e.target.value)}>
            {ISOLATIONS.map((o) => <option key={o.value} value={o.value}>{o.label}</option>)}
          </select>
        </div>

        <div className="flex items-end pb-1">
          <label className="flex cursor-pointer items-center gap-2.5 text-[13px]">
            <input type="checkbox" checked={enabled} onChange={(e) => setEnabled(e.target.checked)}
              className="h-4 w-4 shrink-0 accent-accent" />
            创建后立即启用（期望态）
          </label>
        </div>
      </div>

      {/* ---- 权限确认 ---- */}
      <div className="rounded-xl bg-surface-2 p-3.5">
        <p className="mb-2 text-[13px] font-medium">该插件声明的权限</p>
        <PermissionList
          permissions={selected?.permissions}
          emptyHint={selected ? '没有声明任何权限' : '目录里没有这个插件，无法核对权限声明'}
        />
        {perms > 0 && (
          <label className="mt-3 flex cursor-pointer items-start gap-2.5 border-t border-hairline pt-3">
            <input type="checkbox" checked={permAcked} onChange={(e) => setPermAcked(e.target.checked)}
              className="mt-0.5 h-4 w-4 shrink-0 accent-accent" />
            <span className="min-w-0 text-[12.5px] leading-relaxed">
              我已核对上述 {perms} 项权限，同意授予。提交时会带上 confirm_permissions。
            </span>
          </label>
        )}
      </div>

      {/* ---- 非敏感配置 ---- */}
      <div>
        <div className="mb-2 flex items-center justify-between gap-2">
          <p className="text-[13px] font-medium">配置项（非敏感）</p>
          <Button type="button" variant="ghost" onClick={() => setRows((r) => [...r, { key: '', value: '' }])}>
            <Plus size={13} /> 添加
          </Button>
        </div>
        {rows.length === 0 ? (
          <p className="text-xs text-ink-3">没有配置项。需要敏感值请改用下面的 secret handle。</p>
        ) : (
          <div className="space-y-2">
            {rows.map((r, i) => (
              <div key={i} className="flex min-w-0 gap-2">
                <label className="sr-only" htmlFor={`cfg-k-${i}`}>配置键</label>
                <input id={`cfg-k-${i}`} className="input num min-w-0 flex-1 font-mono text-[12.5px]"
                  placeholder="key" value={r.key} autoComplete="off" spellCheck={false}
                  onChange={(e) => setRows((prev) => prev.map((x, j) => (j === i ? { ...x, key: e.target.value } : x)))} />
                <label className="sr-only" htmlFor={`cfg-v-${i}`}>配置值</label>
                <input id={`cfg-v-${i}`} className="input num min-w-0 flex-1 font-mono text-[12.5px]"
                  placeholder="value" value={r.value} autoComplete="off" spellCheck={false}
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
          <KeyRound size={13} className="shrink-0" /> Secret handle（一行一个）
        </label>
        <textarea
          id="pi-refs" rows={3} value={refsText} autoComplete="off" spellCheck={false}
          placeholder={'db-password\nsmtp-token'}
          onChange={(e) => setRefsText(e.target.value)}
          className="input num resize-y font-mono text-[12.5px]"
        />
        <p className="mt-1.5 text-[11.5px] leading-relaxed text-ink-3">
          只填 handle 名（<span className="num">secret://</span> 前缀可省略）。明文只存在于 Edge 本地 provider
          与插件进程内存中，Server 与浏览器都不会看到，也不会被记录。
        </p>
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
        <p className="text-[11.5px] text-ink-3">还需要填写：{missing.join('、')}</p>
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
              plugin_id: pluginId.trim(),
              version: version.trim(),
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
              version: version.trim() || undefined,
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
      {busy ? '提交中…' : mode === 'create' ? '创建实例' : '保存变更'}
    </Button>
  )
}