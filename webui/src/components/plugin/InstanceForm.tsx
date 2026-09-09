// 运行实例的创建 / 编辑表单。
//
// 创建路径先选「应用或驱动」，运行位置随类型确定：应用走中心服务，驱动选网关；
// 连接器不出现在创建候选中。版本和运行方式用下拉选择，低频的配置与密钥收进高级参数。
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { KeyRound, Plus, X } from 'lucide-react'
import { Button, Select, TextField } from '@/components/ui'
import { PermissionList, PluginErrorNote } from './PluginFacts'
import { useEdges } from '@/hooks/useEdges'
import { useCreateInstance, useUpdateInstance } from '@/hooks/usePlugins'
import { optionLabel } from '@/lib/format'
import { normalizePluginKind, permissionCount, pluginDisplayName, secretHandleName } from '@/lib/plugins'
import type {
  PluginCatalogView, PluginInstanceCreateRequest, PluginInstanceUpdateRequest, PluginInstanceView,
} from '@/lib/types'

const ISOLATIONS = [
  { value: 'shared', labelKey: 'form.isolationShared' },
  { value: 'per-instance', labelKey: 'form.isolationIndependent' },
] as const


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
  const { t } = useTranslation('plugin')
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
  if (mode === 'create' && !instanceId.trim()) missing.push(t('form.missingInstanceName'))
  if (!effectivePluginId.trim()) missing.push(t('form.missingPlugin'))
  if (pluginKind === 'connector') missing.push(t('form.missingConnector'))
  if (pluginKind !== 'application' && pluginKind !== 'driver' && pluginKind !== 'connector') missing.push(t('form.missingPluginType'))
  if (mode === 'create' && pluginKind !== 'connector' && pluginKind !== 'unknown' && !effectiveEdge) missing.push(t('form.missingLocation'))
  if (hostMismatch) missing.push(t('form.missingHostMismatch'))
  if (!effectiveVersion.trim()) missing.push(t('form.missingVersion'))
  if (perms > 0 && !permAcked) missing.push(t('form.missingPermissions'))

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
      <div className="rounded-tile bg-surface-2 p-3.5">
        <label htmlFor="pi-plugin" className="mb-1.5 block text-compact font-medium text-ink-2">
          {t('form.whatToRun')}
        </label>
        {mode === 'edit' ? (
          <div id="pi-plugin" className="input flex min-w-0 items-center text-compact text-ink-2">
            <span className="min-w-0 truncate">{pluginDisplayName(selected)}</span>
          </div>
        ) : createOptions.length > 0 ? (
          <Select id="pi-plugin" className="overflow-hidden" compact value={effectivePluginId}
            onChange={(e) => {
              const nextID = e.target.value
              const next = catalog.find((p) => p.id === nextID)
              const nextKind = normalizePluginKind(next?.kind)
              setPluginId(nextID)
              setVersion(next?.version ?? '')
              setEdgeId(nextKind === 'application' ? 'server' : '')
            }}>
            <optgroup label={t('form.appGroup')}>
              {createOptions.filter((p) => normalizePluginKind(p.kind) === 'application').map((p) => (
                <option key={p.id} value={p.id}>
                  {optionLabel(`${pluginDisplayName(p)}${p.version ? ` · ${p.version}` : ''}${p.verified ? '' : t('form.unverified')}`, 40)}
                </option>
              ))}
            </optgroup>
            <optgroup label={t('form.driverGroup')}>
              {createOptions.filter((p) => normalizePluginKind(p.kind) === 'driver').map((p) => (
                <option key={p.id} value={p.id}>
                  {optionLabel(`${pluginDisplayName(p)}${p.version ? ` · ${p.version}` : ''}${p.verified ? '' : t('form.unverified')}`, 40)}
                </option>
              ))}
            </optgroup>
          </Select>
        ) : (
          <p className="rounded-tile bg-surface px-3.5 py-2.5 text-compact text-ink-2">
            {t('form.noCandidates')}
          </p>
        )}
        <p className="mt-1.5 text-meta leading-relaxed text-ink-3">
          {mode === 'edit' ? t('form.pluginFixed') : t('form.pluginHelp')}
        </p>
      </div>

      <div className="grid gap-4 sm:grid-cols-2">
        {mode === 'create' ? (
          <>
            <div>
              <label htmlFor="pi-edge" className="mb-1.5 block text-compact font-medium text-ink-2">{t('form.location')}</label>
              {pluginKind === 'connector' ? (
                <p className="rounded-tile bg-surface-2 px-3.5 py-2.5 text-compact text-ink-2">
                  {t('form.connectorNoInstance')}
                </p>
              ) : pluginKind === 'application' ? (
                <Select id="pi-edge" className="overflow-hidden" compact value="server" disabled>
                  <option value="server">{t('host.server')}</option>
                </Select>
              ) : pluginKind === 'driver' ? (
                <Select id="pi-edge" className="overflow-hidden" compact value={effectiveEdge} onChange={(e) => setEdgeId(e.target.value)}>
                  {edges.length === 0 && <option value="">{t('form.noEdge')}</option>}
                  {edges.map((edge) => (
                    <option key={edge.edge_id} value={edge.edge_id}>
                      {optionLabel(`${edge.edge_id}${edge.online ? '' : t('form.edgeOffline')}`, 40)}
                    </option>
                  ))}
                </Select>
              ) : (
                <p className="rounded-tile bg-surface-2 px-3.5 py-2.5 text-compact text-ink-2">
                  {t('form.choosePlugin')}
                </p>
              )}
              <p className="mt-1.5 text-meta text-ink-3">
                {pluginKind === 'application'
                  ? t('form.appLocationHint')
                  : pluginKind === 'driver'
                    ? t('form.driverLocationHint')
                    : t('form.locationAutoHint')}
              </p>
            </div>
            <TextField
              label={t('form.instanceName')} value={instanceId} placeholder={t('form.instancePlaceholder')}
              autoComplete="off" spellCheck={false}
              hint={t('form.instanceHint')}
              onChange={(e) => setInstanceId(e.target.value)}
            />
          </>
        ) : (
          <div className="sm:col-span-2">
            <p className="rounded-tile bg-surface-2 px-3.5 py-2.5 text-meta break-words text-ink-2">
              {t('form.editMeta', { location: instance?.edge_id === 'server' ? t('host.server') : t('location.edge', { id: instance?.edge_id || '—' }), name: d?.instance_id || '—' })}
            </p>
            {hostMismatch && (
              <p role="alert" className="mt-2 rounded-tile bg-warn/12 px-3 py-2 text-meta leading-relaxed text-warn">
                {t('form.hostMismatch')}
              </p>
            )}
          </div>
        )}

        <div>
          <label htmlFor="pi-version" className="mb-1.5 block text-compact font-medium text-ink-2">{t('form.version')}</label>
          {versionOptions.length > 0 ? (
            <Select id="pi-version" className="overflow-hidden" compact value={effectiveVersion}
              onChange={(e) => setVersion(e.target.value)}>
              {versionOptions.map((v) => <option key={v} value={v}>{v}</option>)}
            </Select>
          ) : (
            <input id="pi-version" className="input text-compact" value={version} placeholder={t('form.versionPlaceholder')}
              autoComplete="off" spellCheck={false} onChange={(e) => setVersion(e.target.value)} />
          )}
          <p className="mt-1.5 text-meta text-ink-3">
            {selected?.version ? t('form.currentVersion', { version: selected.version }) : t('form.versionUnavailable')}
          </p>
        </div>

        <div>
          <label htmlFor="pi-iso" className="mb-1.5 block text-compact font-medium text-ink-2">{t('form.isolation')}</label>
          <Select id="pi-iso" className="overflow-hidden" compact value={isolation} onChange={(e) => setIsolation(e.target.value as 'shared' | 'per-instance')}>
            {ISOLATIONS.map((o) => <option key={o.value} value={o.value}>{t(o.labelKey)}</option>)}
          </Select>
          <p className="mt-1.5 text-meta text-ink-3">{t('form.isolationHint')}</p>
        </div>

        <div className="flex items-end pb-1">
          <label className="flex cursor-pointer items-center gap-2.5 text-compact">
            <input type="checkbox" checked={enabled} onChange={(e) => setEnabled(e.target.checked)}
              className="h-4 w-4 shrink-0 accent-accent" />
            {mode === 'create' ? t('form.enableOnCreate') : t('form.enableOnUpdate')}
          </label>
        </div>
      </div>

      {/* ---- 权限确认 ---- */}
      <div className="rounded-tile bg-surface-2 p-3.5">
        <p className="mb-2 text-compact font-medium">{t('form.pluginPermissions')}</p>
        <PermissionList
          permissions={selected?.permissions}
          emptyHint={selected ? t('form.noPermissions') : t('form.pluginMissing')}
        />
        {perms > 0 && (
          <label className="mt-3 flex cursor-pointer items-start gap-2.5 border-t border-hairline pt-3">
            <input type="checkbox" checked={permAcked} onChange={(e) => setPermAcked(e.target.checked)}
              className="mt-0.5 h-4 w-4 shrink-0 accent-accent" />
            <span className="min-w-0 text-meta leading-relaxed">
              {t('form.permissionsAck', { count: perms })}
            </span>
          </label>
        )}
      </div>

      <details className="rounded-tile border border-hairline p-3.5" open={mode === 'edit' && (rows.length > 0 || refsText.length > 0)}>
        <summary className="cursor-pointer text-compact font-medium">{t('form.advanced')}</summary>
        <div className="mt-4 space-y-4">
          <div>
            <div className="mb-2 flex items-center justify-between gap-2">
              <p className="text-compact font-medium">{t('form.pluginConfig')}</p>
              <Button type="button" variant="ghost" onClick={() => setRows((r) => [...r, { key: '', value: '' }])}>
                <Plus size={13} /> {t('form.addParam')}
              </Button>
            </div>
            {rows.length === 0 ? (
              <p className="text-meta text-ink-3">{t('form.noParams')}</p>
            ) : (
              <div className="space-y-2">
                {rows.map((r, i) => (
                  <div key={i} className="flex min-w-0 gap-2">
                    <label className="sr-only" htmlFor={`cfg-k-${i}`}>{t('form.paramName')}</label>
                    <input id={`cfg-k-${i}`} className="input num min-w-0 flex-1 font-mono text-meta"
                      placeholder="{t('form.paramName')}" value={r.key} autoComplete="off" spellCheck={false}
                      onChange={(e) => setRows((prev) => prev.map((x, j) => (j === i ? { ...x, key: e.target.value } : x)))} />
                    <label className="sr-only" htmlFor={`cfg-v-${i}`}>{t('form.paramValue')}</label>
                    <input id={`cfg-v-${i}`} className="input num min-w-0 flex-1 font-mono text-meta"
                      placeholder="{t('form.paramValue')}" value={r.value} autoComplete="off" spellCheck={false}
                      onChange={(e) => setRows((prev) => prev.map((x, j) => (j === i ? { ...x, value: e.target.value } : x)))} />
                    <button type="button" aria-label={t('form.deleteParam', { name: r.key || i + 1 })}
                      className="flex h-9 w-9 shrink-0 items-center justify-center rounded-pill text-ink-3 transition-colors hover:text-bad"
                      onClick={() => setRows((prev) => prev.filter((_, j) => j !== i))}>
                      <X size={14} />
                    </button>
                  </div>
                ))}
              </div>
            )}
          </div>

          <div>
            <label htmlFor="pi-refs" className="mb-1.5 flex items-center gap-1.5 text-compact font-medium">
              <KeyRound size={13} className="shrink-0" /> {t('form.secretRefs')}
            </label>
            <textarea
              id="pi-refs" rows={3} value={refsText} autoComplete="off" spellCheck={false}
              placeholder={t('form.secretPlaceholder')}
              onChange={(e) => setRefsText(e.target.value)}
              className="input num resize-y font-mono text-meta"
            />
            <p className="mt-1.5 text-meta leading-relaxed text-ink-3">
              {t('form.secretHint')}
            </p>
            <details className="mt-1.5 text-meta text-ink-3">
              <summary className="cursor-pointer">{t('common.technicalDetails')}</summary>
              <p className="mt-1.5 leading-relaxed">{t('form.secretPrefixHint')}</p>
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
        <p className="text-meta text-ink-3">{t('form.missing', { items: missing.join('、') })}</p>
      )}
      {error ? <PluginErrorNote error={error} /> : null}

      <div className="flex flex-col-reverse gap-2.5 border-t border-hairline pt-4 sm:flex-row sm:justify-end">
        <Button type="button" variant="ghost" onClick={onDone}>{t('form.cancel')}</Button>
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
  const { t } = useTranslation('plugin')
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
      {busy ? t('form.submitting') : mode === 'create' ? t('form.create') : t('form.update')}
    </Button>
  )
}
