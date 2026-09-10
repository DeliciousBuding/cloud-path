import { lazy, Suspense, useEffect, useId, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Box, Maximize2, Minimize2 } from 'lucide-react'
import { Badge, IconButton, Panel } from '@/components/ui'
import { cn } from '@/lib/cn'
import { useNow } from '@/hooks/useNow'
import type { DeviceDescriptor, DeviceView } from '@/lib/types'
import { resolveDeviceTwin } from './device-twin'

const loadStcbBoardTwin = () => import('./StcbBoardTwin')
const StcbBoardTwin = lazy(loadStcbBoardTwin)

/**
 * 设备详情先完成首屏绘制，再利用浏览器空闲时间预热 3D 模块。
 * 模块本身仍由 React.lazy 加载，既不阻塞路由首屏，也不在列表/首页强制下载 three.js。
 */
function useIdleReady(enabled: boolean) {
  const [ready, setReady] = useState(false)
  useEffect(() => {
    if (!enabled) return
    let active = true
    let idleID: number | undefined
    let timeoutID: number | undefined
    const warm = () => {
      if (!active) return
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
  if (!resolution) return null

  const viewerHeight = 'h-72 sm:h-80'

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
            <StcbBoardTwin state={resolution.visualState} label={t('twin.aria')} />
          </Suspense>
        ) : (
          <p className="grid h-full place-items-center px-6 text-center text-body text-ink-2">{t('twin.loading')}</p>
        )}
      </div>
    </Panel>
  )
}
