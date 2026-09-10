import { lazy, Suspense, useEffect, useId, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Activity as ActivityIcon, Box, Maximize2, Minimize2 } from 'lucide-react'
import { Badge, IconButton, Panel } from '@/components/ui'
import { cn } from '@/lib/cn'
import { useDeviceTwinActivity } from './activity'
import { useNow } from '@/hooks/useNow'
import type { DeviceDescriptor, DeviceView } from '@/lib/types'
import { resolveDeviceTwin } from './device-twin'

const loadStcbBoardTwin = () => import('./StcbBoardTwin')
const StcbBoardTwin = lazy(loadStcbBoardTwin)

/** Keep the idle warm-up result for the lifetime of the device page module. */
let stcbBoardTwinWarm = false

/**
 * 设备详情先完成首屏绘制，再利用浏览器空闲时间预热 3D 模块。
 * 模块本身仍由 React.lazy 加载，既不阻塞路由首屏，也不在列表/首页强制下载 three.js。
 */
function useIdleReady(enabled: boolean) {
  const [ready, setReady] = useState(stcbBoardTwinWarm)
  useEffect(() => {
    if (!enabled) return
    if (stcbBoardTwinWarm) {
      setReady(true)
      return
    }
    let active = true
    let idleID: number | undefined
    let timeoutID: number | undefined
    const warm = () => {
      if (!active) return
      stcbBoardTwinWarm = true
      void loadStcbBoardTwin().catch(() => undefined)
      setReady(true)
    }
    if (typeof window.requestIdleCallback === 'function') {
      idleID = window.requestIdleCallback(warm, { timeout: 1200 })
    } else {
      timeoutID = window.setTimeout(warm, 250)
    }
    return () => {
      active = false
      if (idleID !== undefined) window.cancelIdleCallback(idleID)
      if (timeoutID !== undefined) window.clearTimeout(timeoutID)
    }
  }, [enabled])
  return ready
}

export function DeviceTwinPanel({ device, descriptor, full = false, onToggle, className }: {
  device: DeviceView
  descriptor: DeviceDescriptor | null
  full?: boolean
  onToggle?: () => void
  className?: string
}) {
  const { t } = useTranslation('devices')
  const now = useNow()
  const nowSeconds = Math.floor(now.getTime() / 1000)
  const resolution = useMemo(() => resolveDeviceTwin(device, descriptor, nowSeconds), [device, descriptor, nowSeconds])
  const viewerId = useId()
  const twinReady = useIdleReady(resolution !== undefined)
  const activity = useDeviceTwinActivity((state) => state.byDevice[device.id])
  const [visibleActivity, setVisibleActivity] = useState<typeof activity>(undefined)

  useEffect(() => {
    if (!activity) {
      setVisibleActivity(undefined)
      return
    }
    if (Date.now() / 1000 - activity.at > 8) return
    setVisibleActivity(activity)
    const timer = window.setTimeout(() => setVisibleActivity(undefined), 5200)
    return () => window.clearTimeout(timer)
  }, [activity])

  if (!resolution) return null

  const viewerHeight = 'h-72 sm:h-80'
  const activityColor = visibleActivity
    ? { ok: 'var(--color-ok)', warn: 'var(--color-warn)', bad: 'var(--color-bad)', accent: 'var(--color-accent)', idle: 'var(--color-ink-3)' }[visibleActivity.tone]
    : undefined

  return (
    <Panel
      className={cn('overflow-hidden', className)}
      title={<span className="flex items-center gap-2"><Box size={16} aria-hidden="true" />{t('twin.title')}</span>}
      right={
        <div className="flex items-center gap-2">
          <Badge tone={device.online ? 'ok' : 'idle'}>{device.online ? t('twin.live') : t('twin.offline')}</Badge>
          {onToggle && (
            <IconButton
              variant="ghost"
              size="sm"
              label={t(full ? 'twin.collapse' : 'twin.expand')}
              aria-expanded={full}
              aria-controls={viewerId}
              onClick={onToggle}
            >
              {full ? <Minimize2 size={14} aria-hidden="true" /> : <Maximize2 size={14} aria-hidden="true" />}
            </IconButton>
          )}
        </div>
      }
    >
      <div
        id={viewerId}
        className={`relative overflow-hidden rounded-tile bg-surface-2 ring-1 ring-hairline transition-[height] duration-300 ${viewerHeight}`}
      >
        {visibleActivity && (
          <>
            <div className="twin-event-ring pointer-events-none absolute inset-0 z-10 rounded-tile" style={{ boxShadow: `inset 0 0 0 2px ${activityColor}` }} />
            <div key={visibleActivity.id} role="status" aria-live="polite" className="twin-event-in pointer-events-none absolute inset-x-3 top-3 z-20 rounded-tile border border-hairline/80 bg-surface/95 px-3 py-2 shadow-lift backdrop-blur-sm">
              <div className="flex min-w-0 items-center gap-2">
                <span className="h-2 w-2 shrink-0 rounded-pill" style={{ backgroundColor: activityColor }} aria-hidden="true" />
                <ActivityIcon size={13} className="shrink-0 text-ink-3" aria-hidden="true" />
                <span className="min-w-0 truncate text-meta font-semibold text-ink">{visibleActivity.label}</span>
              </div>
              {visibleActivity.detail && <p className="mt-1 truncate pl-[30px] text-micro text-ink-3">{visibleActivity.detail}</p>}
            </div>
          </>
        )}
        {twinReady ? (
          <Suspense fallback={<p className="grid h-full place-items-center px-6 text-center text-body text-ink-2">{t('twin.loading')}</p>}>
            <StcbBoardTwin cacheKey={device.id} state={resolution.visualState} label={t('twin.aria')} />
          </Suspense>
        ) : (
          <p className="grid h-full place-items-center px-6 text-center text-body text-ink-2">{t('twin.loading')}</p>
        )}
      </div>
    </Panel>
  )
}
