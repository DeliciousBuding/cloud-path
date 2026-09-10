import { lazy, Suspense, useEffect, useId, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Box, Maximize2, Minimize2 } from 'lucide-react'
import { Badge, IconButton, Panel } from '@/components/ui'
import { cn } from '@/lib/cn'
import { useDeviceTwinActivity } from './activity'
import { useNow } from '@/hooks/useNow'
import type { DeviceDescriptor, DeviceView } from '@/lib/types'
import { resolveDeviceTwin, resolveDeviceTwinHighlight } from './device-twin'

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
    if (Date.now() / 1000 - activity.at > 8) {
      setVisibleActivity(undefined)
      return
    }
    setVisibleActivity(activity)
    const timer = window.setTimeout(() => setVisibleActivity(undefined), 2400)
    return () => window.clearTimeout(timer)
  }, [activity])

  if (!resolution) return null

  const viewerHeight = 'h-72 sm:h-80'
  const highlight = useMemo(() => {
    if (!visibleActivity) return undefined
    const resolved = resolveDeviceTwinHighlight(device, visibleActivity)
    return resolved ? { id: visibleActivity.id, ...resolved } : undefined
  }, [device, visibleActivity])

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
        {twinReady ? (
          <Suspense fallback={<p className="grid h-full place-items-center px-6 text-center text-body text-ink-2">{t('twin.loading')}</p>}>
            <StcbBoardTwin cacheKey={device.id} state={resolution.visualState} label={t('twin.aria')} highlight={highlight} />
          </Suspense>
        ) : (
          <p className="grid h-full place-items-center px-6 text-center text-body text-ink-2">{t('twin.loading')}</p>
        )}
      </div>
    </Panel>
  )
}
