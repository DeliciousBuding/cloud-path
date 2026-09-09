// Sandboxed custom plugin UI host.
//
// The iframe is same-origin in URL, but sandbox="allow-scripts" intentionally
// omits allow-same-origin. It therefore has an opaque origin and cannot read
// CloudPath cookies, localStorage or the parent DOM. All data access goes
// through this postMessage bridge, where scopes and tenant/RBAC are checked.
import { useEffect, useId, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import '@/i18n'
import { ExternalLink, ShieldAlert } from 'lucide-react'
import { api } from '@/lib/api'
import { appJobArgsError, appJobSchema } from '@/lib/application-actions'
import { pluginUIAssetURL } from '@/lib/plugin-ui'
import { Panel } from '@/components/ui'
import type { PluginInstanceView, PluginUISection } from '@/lib/types'

interface BridgeReady {
  type: 'cloudpath:ready'
  nonce: string
}

interface BridgeRequest {
  type: 'cloudpath:request'
  nonce: string
  id: string
  method: string
  params?: unknown
}

interface BridgeSession {
  nonce: string
  ready: boolean
  targetOrigin: string
}

interface BridgeErrorPayload {
  code: string
  message: string
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return Boolean(value) && typeof value === 'object' && !Array.isArray(value)
}

function nonceOf(value: unknown): string | null {
  return typeof value === 'string' && value.length > 0 && value.length <= 128 ? value : null
}

function readyOf(value: unknown): BridgeReady | null {
  if (!isRecord(value) || value.type !== 'cloudpath:ready') return null
  const nonce = nonceOf(value.nonce)
  return nonce ? { type: 'cloudpath:ready', nonce } : null
}

function requestOf(value: unknown): BridgeRequest | null {
  if (!isRecord(value) || value.type !== 'cloudpath:request') return null
  const nonce = nonceOf(value.nonce)
  if (!nonce) return null
  if (typeof value.id !== 'string' || value.id.length === 0 || value.id.length > 120) return null
  if (typeof value.method !== 'string' || value.method.length === 0 || value.method.length > 80) return null
  return { type: 'cloudpath:request', nonce, id: value.id, method: value.method, params: value.params }
}

function newNonce(): string {
  if (typeof crypto !== 'undefined') {
    if (typeof crypto.randomUUID === 'function') return crypto.randomUUID()
    if (typeof crypto.getRandomValues === 'function') {
      return Array.from(crypto.getRandomValues(new Uint8Array(16)), (byte) => byte.toString(16).padStart(2, '0')).join('')
    }
  }
  return `ui-${Date.now()}-${Math.random().toString(16).slice(2)}`
}

function safeIdempotencyKey(): string {
  if (typeof crypto !== 'undefined' && typeof crypto.getRandomValues === 'function') {
    return Array.from(crypto.getRandomValues(new Uint8Array(16)), (byte) => byte.toString(16).padStart(2, '0')).join('')
  }
  return `ui-${Date.now()}-${Math.random().toString(16).slice(2)}`
}

function safeTargetOrigin(origin: string): string {
  // sandbox="allow-scripts" gives the iframe an opaque origin. The browser
  // requires "*" for that origin; authorization remains the exact source
  // window plus the live nonce. Same-origin frames can use the exact origin.
  return origin === window.location.origin ? origin : '*'
}

function bridgeError(code: string, message: string): Error & { code?: string } {
  const error = new Error(message) as Error & { code?: string }
  error.code = code
  return error
}

function configPayload(value: unknown, t: (key: string, options?: Record<string, unknown>) => string): Record<string, string> {
  if (!isRecord(value)) throw bridgeError('invalid_params', t('bridge.invalidConfig'))
  const out: Record<string, string> = {}
  for (const [key, raw] of Object.entries(value)) {
    if (!key || key.length > 160 || typeof raw !== 'string' || raw.length > 16_384) {
      throw bridgeError('invalid_params', t('bridge.invalidConfigItem'))
    }
    out[key] = raw
  }
  return out
}

export function PluginUIBridge({ pluginId, version, instance, section, readOnly = false }: {
  pluginId: string
  /** Canonical catalog version; callers fall back to the desired instance version only when absent. */
  version?: string
  instance: PluginInstanceView
  section: PluginUISection
  readOnly?: boolean
}) {
  const { t } = useTranslation('plugin')
  const frame = useRef<HTMLIFrameElement>(null)
  const session = useRef<BridgeSession | null>(null)
  const [state, setState] = useState<'loading' | 'ready' | 'error'>('loading')
  const titleId = useId()
  const src = pluginUIAssetURL(pluginId, version?.trim() || instance.desired.version, section.entry)
  const scopes = new Set(section.scopes ?? [])
  const dataInstanceID = instance.desired.instance_id

  useEffect(() => {
    if (!src) return
    const onMessage = (event: MessageEvent) => {
      const sourceWindow = frame.current?.contentWindow
      const active = session.current
      if (!sourceWindow || !active || event.source !== sourceWindow) return

      const ready = readyOf(event.data)
      if (ready) {
        if (ready.nonce !== active.nonce || active.ready) return
        active.ready = true
        active.targetOrigin = safeTargetOrigin(event.origin)
        setState('ready')
        sourceWindow.postMessage({
          type: 'cloudpath:init',
          nonce: active.nonce,
          apiVersion: 1,
          instance: { id: dataInstanceID, plugin_id: instance.desired.plugin_id },
          scopes: section.scopes ?? [],
        }, active.targetOrigin)
        return
      }

      const request = requestOf(event.data)
      if (!request || !active.ready || request.nonce !== active.nonce) return

      const reply = (ok: boolean, data?: unknown, error?: BridgeErrorPayload) => {
        const current = session.current
        const target = frame.current?.contentWindow
        // A navigation/reload replaces the session object; never deliver a
        // response from an old request to the new document.
        if (!current || current !== active || !current.ready || current.nonce !== request.nonce || !target) return
        target.postMessage({
          type: 'cloudpath:response', nonce: request.nonce, id: request.id, ok, data, error,
        }, current.targetOrigin)
      }
      const deny = (message: string) => reply(false, undefined, { code: 'forbidden_scope', message })
      const run = async () => {
        if (readOnly && (request.method === 'jobs.run' || request.method === 'config.update')) {
          const message = request.method === 'jobs.run' ? t('bridge.denyJobsRun') : t('bridge.denyConfigWrite')
          return reply(false, undefined, { code: 'forbidden_readonly', message })
        }
        const params = isRecord(request.params) ? request.params : {}
        switch (request.method) {
          case 'instance.get':
            if (!scopes.has('instance.read')) return deny(t('bridge.denyInstanceRead'))
            return reply(true, await api.pluginInstance(instance.id))
          case 'bindings.list':
            if (!scopes.has('bindings.read')) return deny(t('bridge.denyBindingsRead'))
            return reply(true, await api.appBindings(dataInstanceID))
          case 'records.list':
            if (!scopes.has('records.read')) return deny(t('bridge.denyRecordsRead'))
            return reply(true, await api.appRecords(dataInstanceID, {
              recordType: typeof params.recordType === 'string' ? params.recordType.slice(0, 64) : undefined,
              offset: typeof params.offset === 'number' && params.offset >= 0 ? Math.floor(params.offset) : 0,
              limit: typeof params.limit === 'number' && params.limit > 0 ? Math.min(Math.floor(params.limit), 100) : 20,
            }))
          case 'jobs.list':
            if (!scopes.has('jobs.read')) return deny(t('bridge.denyJobsRead'))
            return reply(true, await api.appJobs(dataInstanceID))
          case 'jobs.run': {
            if (!scopes.has('jobs.run')) return deny(t('bridge.denyJobsRun'))
            const jobID = typeof params.job_id === 'string' ? params.job_id.trim() : ''
            const args = typeof params.args_json === 'string' ? params.args_json : '{}'
            if (!jobID || jobID.length > 120 || new TextEncoder().encode(args).length > 4096) {
              return reply(false, undefined, { code: 'invalid_params', message: t('bridge.invalidJob') })
            }
            const jobs = await api.appJobs(dataInstanceID)
            const descriptor = jobs.job_descriptors.find((job) => job.id === jobID)
            if (!descriptor || descriptor.manual_only !== true) {
              return reply(false, undefined, { code: 'not_allowed', message: t('bridge.jobNotManual') })
            }
            const { schema, error } = appJobSchema(descriptor.input_schema_json)
            if (!schema || error || appJobArgsError(args, schema)) {
              return reply(false, undefined, { code: 'invalid_params', message: t('bridge.invalidJob') })
            }
            const idempotencyKey = typeof params.idempotency_key === 'string' && params.idempotency_key.length <= 120
              ? params.idempotency_key : safeIdempotencyKey()
            return reply(true, await api.runAppJob(dataInstanceID, jobID, { args_json: args, idempotency_key: idempotencyKey }))
          }
          case 'config.update': {
            if (!scopes.has('config.write')) return deny(t('bridge.denyConfigWrite'))
            const config = configPayload(params.config, t)
            return reply(true, await api.updatePluginInstance(instance.id, { config }))
          }
          default:
            return reply(false, undefined, { code: 'unknown_method', message: t('bridge.unknownMethod') })
        }
      }
      void run().catch((error: unknown) => {
        const code = isRecord(error) && typeof error.code === 'string' ? error.code : 'upstream_error'
        reply(false, undefined, { code, message: code === 'invalid_params' ? t('bridge.invalidParams') : t('bridge.requestFailed') })
      })
    }
    window.addEventListener('message', onMessage)
    return () => window.removeEventListener('message', onMessage)
  }, [dataInstanceID, instance.desired.plugin_id, instance.id, readOnly, section.scopes, src])

  const onLoad = () => {
    const nonce = newNonce()
    session.current = { nonce, ready: false, targetOrigin: '*' }
    setState('loading')
    // This bootstrap deliberately contains no scopes or instance data. It
    // only gives the newly loaded document the nonce for the ready handshake.
    frame.current?.contentWindow?.postMessage({ type: 'cloudpath:hello', nonce }, '*')
  }

  const onError = () => {
    session.current = null
    setState('error')
  }

  if (!src) {
    return <div role="alert" className="rounded-tile bg-bad/10 px-4 py-3 text-body text-bad">
      <p className="flex items-center gap-2 font-medium"><ShieldAlert size={15} /> {t('bridge.unavailable')}</p>
      <p className="mt-1 text-meta leading-relaxed opacity-90">{t('bridge.unavailableHint')}</p>
    </div>
  }

  return <Panel title={<span className="flex items-center gap-1.5"><ExternalLink size={14} /> {t('bridge.title')}</span>}>
    <p id={titleId} className="mb-3 text-meta leading-relaxed text-ink-3">
      {t('bridge.hint')}
    </p>
    <div className="relative min-h-64 overflow-hidden rounded-tile border border-hairline bg-surface-2">
      {state === 'loading' && <p role="status" className="absolute inset-0 z-local flex items-center justify-center text-body text-ink-3">{t('bridge.loading')}</p>}
      {state === 'error' && <div role="alert" className="absolute inset-0 z-local flex items-center justify-center px-6 text-center text-body text-bad">{t('bridge.loadFailed')}</div>}
      <iframe
        ref={frame}
        src={src}
        title={t('bridge.iframeTitle')}
        aria-labelledby={titleId}
        sandbox="allow-scripts"
        referrerPolicy="no-referrer"
        onLoad={onLoad}
        onError={onError}
        className="h-64 w-full border-0 bg-surface"
      />
    </div>
  </Panel>
}
