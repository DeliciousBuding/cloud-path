// Sandboxed custom plugin UI host.
//
// The iframe is same-origin in URL, but sandbox="allow-scripts" intentionally
// omits allow-same-origin. It therefore has an opaque origin and cannot read
// CloudPath cookies, localStorage or the parent DOM. All data access goes
// through this postMessage bridge, where scopes and tenant/RBAC are checked.
import { useEffect, useId, useRef, useState } from 'react'
import { ExternalLink, ShieldAlert } from 'lucide-react'
import { api } from '@/lib/api'
import { appJobArgsError, appJobSchema } from '@/lib/application-actions'
import { pluginUIAssetURL } from '@/lib/plugin-ui'
import { Panel } from '@/components/ui'
import type { PluginInstanceView, PluginUISection } from '@/lib/types'

interface BridgeRequest {
  type: 'cloudpath:request'
  id: string
  method: string
  params?: unknown
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return Boolean(value) && typeof value === 'object' && !Array.isArray(value)
}

function requestOf(value: unknown): BridgeRequest | null {
  if (!isRecord(value) || value.type !== 'cloudpath:request') return null
  if (typeof value.id !== 'string' || value.id.length === 0 || value.id.length > 120) return null
  if (typeof value.method !== 'string' || value.method.length === 0 || value.method.length > 80) return null
  return { type: 'cloudpath:request', id: value.id, method: value.method, params: value.params }
}

function safeIdempotencyKey(): string {
  if (typeof crypto !== 'undefined' && typeof crypto.getRandomValues === 'function') {
    return Array.from(crypto.getRandomValues(new Uint8Array(16)), (byte) => byte.toString(16).padStart(2, '0')).join('')
  }
  return `ui-${Date.now()}-${Math.random().toString(16).slice(2)}`
}

function bridgeError(code: string, message: string): Error & { code?: string } {
  const error = new Error(message) as Error & { code?: string }
  error.code = code
  return error
}

function configPayload(value: unknown): Record<string, string> {
  if (!isRecord(value)) throw bridgeError('invalid_params', '配置格式无效')
  const out: Record<string, string> = {}
  for (const [key, raw] of Object.entries(value)) {
    if (!key || key.length > 160 || typeof raw !== 'string' || raw.length > 16_384) {
      throw bridgeError('invalid_params', '配置项格式无效')
    }
    out[key] = raw
  }
  return out
}

export function PluginUIBridge({ pluginId, version, instance, section }: {
  pluginId: string
  /** Canonical catalog version; callers fall back to the desired instance version only when absent. */
  version?: string
  instance: PluginInstanceView
  section: PluginUISection
}) {
  const frame = useRef<HTMLIFrameElement>(null)
  const [state, setState] = useState<'loading' | 'ready' | 'error'>('loading')
  const titleId = useId()
  const src = pluginUIAssetURL(pluginId, version?.trim() || instance.desired.version, section.entry)
  const scopes = new Set(section.scopes ?? [])
  const dataInstanceID = instance.desired.instance_id

  useEffect(() => {
    if (!src || !frame.current) return
    const onMessage = (event: MessageEvent) => {
      if (event.source !== frame.current?.contentWindow) return
      const request = requestOf(event.data)
      if (!request) return
      const reply = (ok: boolean, data?: unknown, error?: { code: string; message: string }) => {
        frame.current?.contentWindow?.postMessage({
          type: 'cloudpath:response', id: request.id, ok, data, error,
        }, '*')
      }
      const deny = (message: string) => reply(false, undefined, { code: 'forbidden_scope', message })
      const run = async () => {
        const params = isRecord(request.params) ? request.params : {}
        switch (request.method) {
          case 'instance.get':
            if (!scopes.has('instance.read')) return deny('当前自定义界面没有读取实例信息的权限。')
            return reply(true, await api.pluginInstance(instance.id))
          case 'bindings.list':
            if (!scopes.has('bindings.read')) return deny('当前自定义界面没有读取设备绑定的权限。')
            return reply(true, await api.appBindings(dataInstanceID))
          case 'records.list':
            if (!scopes.has('records.read')) return deny('当前自定义界面没有读取记录的权限。')
            return reply(true, await api.appRecords(dataInstanceID, {
              recordType: typeof params.recordType === 'string' ? params.recordType.slice(0, 64) : undefined,
              offset: typeof params.offset === 'number' && params.offset >= 0 ? Math.floor(params.offset) : 0,
              limit: typeof params.limit === 'number' && params.limit > 0 ? Math.min(Math.floor(params.limit), 100) : 20,
            }))
          case 'jobs.list':
            if (!scopes.has('jobs.read')) return deny('当前自定义界面没有读取操作的权限。')
            return reply(true, await api.appJobs(dataInstanceID))
          case 'jobs.run': {
            if (!scopes.has('jobs.run')) return deny('当前自定义界面没有执行操作的权限。')
            const jobID = typeof params.job_id === 'string' ? params.job_id.trim() : ''
            const args = typeof params.args_json === 'string' ? params.args_json : '{}'
            if (!jobID || jobID.length > 120 || new TextEncoder().encode(args).length > 4096) {
              return reply(false, undefined, { code: 'invalid_params', message: '操作参数无效。' })
            }
            const jobs = await api.appJobs(dataInstanceID)
            const descriptor = jobs.job_descriptors.find((job) => job.id === jobID)
            if (!descriptor || descriptor.manual_only !== true) {
              return reply(false, undefined, { code: 'not_allowed', message: '这个操作不能由自定义界面手动执行。' })
            }
            const { schema, error } = appJobSchema(descriptor.input_schema_json)
            if (!schema || error || appJobArgsError(args, schema)) {
              return reply(false, undefined, { code: 'invalid_params', message: '操作参数无效。' })
            }
            const idempotencyKey = typeof params.idempotency_key === 'string' && params.idempotency_key.length <= 120
              ? params.idempotency_key : safeIdempotencyKey()
            return reply(true, await api.runAppJob(dataInstanceID, jobID, { args_json: args, idempotency_key: idempotencyKey }))
          }
          case 'config.update': {
            if (!scopes.has('config.write')) return deny('当前自定义界面没有修改设置的权限。')
            const config = configPayload(params.config)
            return reply(true, await api.updatePluginInstance(instance.id, { config }))
          }
          default:
            return reply(false, undefined, { code: 'unknown_method', message: '不支持这个自定义界面请求。' })
        }
      }
      void run().catch((error: unknown) => {
        const code = isRecord(error) && typeof error.code === 'string' ? error.code : 'upstream_error'
        reply(false, undefined, { code, message: code === 'invalid_params' ? '请求参数无效。' : '请求没有完成，请稍后重试。' })
      })
    }
    window.addEventListener('message', onMessage)
    return () => window.removeEventListener('message', onMessage)
  }, [dataInstanceID, instance.id, scopes, src])

  const onLoad = () => {
    setState('ready')
    frame.current?.contentWindow?.postMessage({
      type: 'cloudpath:init', apiVersion: 1,
      instance: { id: dataInstanceID, plugin_id: instance.desired.plugin_id },
      scopes: section.scopes ?? [],
    }, '*')
  }

  if (!src) {
    return <div role="alert" className="rounded-lg bg-bad/10 px-4 py-3 text-sm text-bad">
      <p className="flex items-center gap-2 font-medium"><ShieldAlert size={15} /> 自定义界面不可用</p>
      <p className="mt-1 text-xs leading-relaxed opacity-90">插件没有声明有效的页面入口。请联系插件维护者修正后重试。</p>
    </div>
  }

  return <Panel title={<span className="flex items-center gap-1.5"><ExternalLink size={14} /> 自定义界面</span>}>
    <p id={titleId} className="mb-3 text-xs leading-relaxed text-ink-3">
      页面在隔离环境中运行，只能通过平台提供的受控接口读取数据或执行操作。
    </p>
    <div className="relative min-h-64 overflow-hidden rounded-lg border border-hairline bg-surface-2">
      {state === 'loading' && <p role="status" className="absolute inset-0 z-10 flex items-center justify-center text-sm text-ink-3">正在加载自定义界面…</p>}
      {state === 'error' && <div role="alert" className="absolute inset-0 z-10 flex items-center justify-center px-6 text-center text-sm text-bad">自定义界面加载失败，请返回应用页面或联系插件维护者。</div>}
      <iframe
        ref={frame}
        src={src}
        title="插件自定义界面"
        aria-labelledby={titleId}
        sandbox="allow-scripts"
        referrerPolicy="no-referrer"
        onLoad={onLoad}
        onError={() => setState('error')}
        className="h-64 w-full border-0 bg-surface"
      />
    </div>
  </Panel>
}
