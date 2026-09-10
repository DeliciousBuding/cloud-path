import { lazy, Suspense, useMemo } from 'react'
import { useTranslation } from 'react-i18next'
import { Box } from 'lucide-react'
import { Badge, Panel } from '@/components/ui'
import { useNow } from '@/hooks/useNow'
import type { DeviceDescriptor, DeviceView } from '@/lib/types'
import { resolveDeviceTwin } from './device-twin'

const StcbBoardTwin = lazy(() => import('./StcbBoardTwin'))

const INDICATOR_LABEL: Record<'led' | 'mode' | 'page', string> = {
  led: 'twin.indicator.led',
  mode: 'twin.indicator.mode',
  page: 'twin.indicator.page',
}

export function DeviceTwinPanel({ device, descriptor }: {
  device: DeviceView
  descriptor: DeviceDescriptor | null
}) {
  const { t } = useTranslation('devices')
  const now = useNow()
  const nowSeconds = Math.floor(now.getTime() / 1000)
  const resolution = useMemo(() => resolveDeviceTwin(device, descriptor, nowSeconds), [device, descriptor, nowSeconds])
  if (!resolution) return null

  return (
    <Panel
      className="mb-5 overflow-hidden"
      title={<span className="flex items-center gap-2"><Box size={16} aria-hidden="true" />{t('twin.title')}</span>}
      right={<Badge tone={device.online ? 'ok' : 'idle'}>{device.online ? t('twin.live') : t('twin.offline')}</Badge>}
    >
      <p className="mb-3 max-w-[76ch] text-meta leading-relaxed text-ink-3">{t('twin.note')}</p>
      <div className="relative h-80 overflow-hidden rounded-tile bg-surface-2 lg:h-96">
        <Suspense fallback={<p className="grid h-full place-items-center px-6 text-center text-body text-ink-2">{t('twin.loading')}</p>}>
          <StcbBoardTwin state={resolution.visualState} label={t('twin.aria')} />
        </Suspense>
        <div className="pointer-events-none absolute left-3 top-3 flex max-w-[calc(100%-1.5rem)] flex-wrap gap-2">
          {resolution.indicators.map((indicator) => (
            <Badge key={indicator.kind} tone={indicator.tone === 'good' ? 'ok' : 'warn'}>
              {t(INDICATOR_LABEL[indicator.kind])} {indicator.value}
            </Badge>
          ))}
        </div>
      </div>
    </Panel>
  )
}
