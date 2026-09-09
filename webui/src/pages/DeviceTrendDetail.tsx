import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useParams, useSearchParams } from 'react-router'
import { useQuery } from '@tanstack/react-query'
import { BarChart3, Database, Maximize2, RadioTower, RefreshCw, TrendingUp } from 'lucide-react'
import {
  BackLink, Badge, EmptyState, ErrorState, PageHeader, Panel, Segmented, StatTile, StatusDot,
} from '@/components/ui'
import { TimeSeriesChart, summarizeSeries } from '@/components/charts'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { api, isNotFound } from '@/lib/api'
import { fmtDateTime, fmtTime } from '@/lib/format'
import { formatValue } from '@/lib/descriptor'
import { seriesLabel, seriesUnit } from '@/lib/series'
import { useDeviceDescriptor } from '@/hooks/useDescriptor'
import { useDeviceSamples } from '@/hooks/useDeviceSamples'
import { RowSkeleton } from '@/components/Skeleton'
import { usePageTitle } from '@/hooks/usePageTitle'
import { useLive } from '@/store/ws'
import type { SeriesSampleView } from '@/lib/types'

type TrendRange = '1h' | '24h' | '7d' | 'all'
type TrendChartKind = 'area' | 'line'

const RANGE_SECONDS: Record<Exclude<TrendRange, 'all'>, number> = {
  '1h': 3600,
  '24h': 86_400,
  '7d': 7 * 86_400,
}

const PAGE_SIZE = 100
const QUALITY_KEYS: Record<string, string> = {
  good: 'quality.good',
  uncertain: 'quality.uncertain',
  bad: 'quality.bad',
  unavailable: 'quality.unavailable',
}

function parseRange(value: string | null): TrendRange {
  return value === '1h' || value === '7d' || value === 'all' ? value : '24h'
}

function parseKind(value: string | null): TrendChartKind {
  return value === 'line' ? 'line' : 'area'
}

function rangeStart(range: TrendRange): number | undefined {
  if (range === 'all') return undefined
  return Math.floor(Date.now() / 1000) - RANGE_SECONDS[range]
}

function mergeSamples(history: SeriesSampleView[], live: { t: number; v: number }[]): SeriesSampleView[] {
  const byTime = new Map<number, SeriesSampleView>()
  for (const sample of history) byTime.set(sample.ts, sample)
  for (const point of live) {
    byTime.set(point.t, { device_id: '', key: '', ts: point.t, value: point.v, quality: 'good' })
  }
  return [...byTime.values()].sort((a, b) => a.ts - b.ts)
}

export default function DeviceTrendDetail() {
  const { t } = useTranslation('devices')
  const { edgeId = '', deviceId = '', seriesKey = '' } = useParams()
  const decodedEdge = decodeURIComponent(edgeId)
  const decodedDevice = decodeURIComponent(deviceId)
  const decodedSeries = decodeURIComponent(seriesKey)
  const key = `${decodedEdge}/${decodedDevice}`
  const [searchParams, setSearchParams] = useSearchParams()
  const range = parseRange(searchParams.get('range'))
  const kind = parseKind(searchParams.get('kind'))
  const [page, setPage] = useState(0)

  const liveDevice = useLive((state) => state.devices[key])
  const livePoints = useLive((state) => state.series[key]?.[decodedSeries]) ?? []
  const deviceQuery = useQuery({
    queryKey: ['device', key], queryFn: () => api.device(decodedEdge, decodedDevice),
    refetchInterval: 10_000, retry: false,
  })
  const device = liveDevice ?? deviceQuery.data
  const { descriptor, capabilities } = useDeviceDescriptor(key, decodedEdge, decodedDevice, { device: device ?? null })
  const label = seriesLabel(decodedSeries, descriptor, capabilities)
  const unit = seriesUnit(decodedSeries, descriptor, capabilities)
  const from = useMemo(() => rangeStart(range), [range])
  const samplesQuery = useDeviceSamples(decodedEdge, decodedDevice, decodedSeries, { from, limit: 1000 })

  const history = useMemo(
    () => samplesQuery.data?.pages.flatMap((pageData) => pageData.samples) ?? [],
    [samplesQuery.data],
  )
  const liveInRange = useMemo(() => (from === undefined ? livePoints : livePoints.filter((point) => point.t >= from)), [from, livePoints])
  const samples = useMemo(() => mergeSamples(history, liveInRange), [history, liveInRange])
  const chartPoints = useMemo(() => samples.map((sample) => ({ t: sample.ts, v: sample.value })), [samples])
  const stats = summarizeSeries(chartPoints)
  const newestFirst = useMemo(() => [...samples].reverse(), [samples])
  const pageCount = Math.max(1, Math.ceil(newestFirst.length / PAGE_SIZE))
  const safePage = Math.min(page, pageCount - 1)
  const pageSamples = newestFirst.slice(safePage * PAGE_SIZE, (safePage + 1) * PAGE_SIZE)

  usePageTitle(device ? `${label} · ${device.name || decodedDevice}` : t('detail.trendDetail.title'))

  useEffect(() => { setPage(0) }, [decodedSeries, range])

  const updateSearch = (patch: { range?: TrendRange; kind?: TrendChartKind }) => {
    setSearchParams((previous) => {
      const next = new URLSearchParams(previous)
      if (patch.range) next.set('range', patch.range)
      if (patch.kind) next.set('kind', patch.kind)
      return next
    })
  }

  const backTo = `/devices/${encodeURIComponent(decodedEdge)}/${encodeURIComponent(decodedDevice)}?tab=overview&stateView=trend`
  const axisTick = range === '1h' || range === '24h' ? fmtTime : fmtDateTime
  const emptyLabel = t('detail.trendDetail.empty')

  if (!device) {
    return (
      <>
        <BackLink to="/devices" label={t('detail.back')} />
        {deviceQuery.isPending ? <Panel><RowSkeleton rows={5} /></Panel>
          : isNotFound(deviceQuery.error) ? (
            <EmptyState icon={<RadioTower size={24} />} title={t('error.notFoundTitle')}
              hint={t('error.notFoundHint', { key })} />
          ) : (
            <ErrorState icon={<RadioTower size={20} />} title={t('error.loadTitle')}
              hint={t('error.loadHint')} onRetry={() => { void deviceQuery.refetch() }} />
          )}
      </>
    )
  }

  if (samplesQuery.isError && samples.length === 0) {
    return (
      <>
        <BackLink to={backTo} label={t('detail.trendDetail.back')} />
        <ErrorState icon={<Database size={20} />} title={t('detail.trendDetail.loadTitle')}
          hint={t('detail.trendDetail.loadHint')} onRetry={() => { void samplesQuery.refetch() }} />
      </>
    )
  }

  return (
    <>
      <BackLink to={backTo} label={t('detail.trendDetail.back')} />
      <PageHeader
        title={label}
        subtitle={`${device.name || decodedDevice} · ${decodedEdge}/${decodedDevice}`}
        actions={<Badge tone={device.online ? 'ok' : 'idle'}><StatusDot online={device.online} />{device.online ? t('detail.state.live') : t('detail.state.offline')}</Badge>}
      />

      {!device.online && (
        <p className="mb-4 rounded-tile bg-ink-3/10 px-3 py-2 text-meta text-ink-2">
          {t('detail.trendDetail.offlineHint')}
        </p>
      )}

      <div className="mb-5 grid grid-cols-2 gap-2.5 lg:grid-cols-4">
        <StatTile label={t('detail.trendDetail.latest')} value={stats ? formatValue(stats.latest) : '—'} unit={unit}
          sub={stats ? fmtDateTime(stats.lastT) : t('detail.trendDetail.noSamples')} />
        <StatTile label={t('detail.trendDetail.min')} value={stats ? formatValue(stats.min) : '—'} unit={unit}
          sub={t('detail.trendDetail.rangeLabel')} />
        <StatTile label={t('detail.trendDetail.max')} value={stats ? formatValue(stats.max) : '—'} unit={unit}
          sub={t('detail.trendDetail.rangeLabel')} />
        <StatTile label={t('detail.trendDetail.average')} value={stats ? formatValue(stats.average) : '—'} unit={unit}
          sub={t('detail.trendDetail.samples', { count: samples.length })} />
      </div>

      <Panel title={<span className="flex items-center gap-1.5"><TrendingUp size={14} />{t('detail.trendDetail.chartTitle')}</span>}>
        <div className="mb-3 flex flex-wrap items-center gap-2">
          <Segmented
            label={t('detail.trendDetail.rangeLabel')}
            options={[
              { value: '1h', label: t('detail.trendDetail.range1h') },
              { value: '24h', label: t('detail.trendDetail.range24h') },
              { value: '7d', label: t('detail.trendDetail.range7d') },
              { value: 'all', label: t('detail.trendDetail.rangeAll') },
            ]}
            value={range}
            onChange={(value) => updateSearch({ range: value })}
          />
          <Segmented
            label={t('detail.trendDetail.chartLabel')}
            options={[
              { value: 'area', label: t('detail.state.area') },
              { value: 'line', label: t('detail.state.line') },
            ]}
            value={kind}
            onChange={(value) => updateSearch({ kind: value })}
          />
        </div>
        <TimeSeriesChart
          points={chartPoints}
          kind={kind}
          height={380}
          unit={unit}
          xTick={axisTick}
          tooltipTime={fmtDateTime}
          zoomable
          emptyLabel={emptyLabel}
          ariaLabel={t('detail.trendDetail.chartAria', { label })}
        />
        <p className="mt-3 border-t border-hairline pt-3 text-meta leading-relaxed text-ink-3">
          {t('detail.trendDetail.chartNote')}
        </p>
      </Panel>

      <div className="mt-5">
        <Panel
          title={<span className="flex items-center gap-1.5"><Database size={14} />{t('detail.trendDetail.tableTitle')}</span>}
          right={<span className="num text-meta text-ink-3">{t('detail.trendDetail.samples', { count: samples.length })}</span>}
        >
          {samplesQuery.isPending && samples.length === 0 ? <RowSkeleton rows={6} />
            : samples.length === 0 ? <EmptyState compact plain icon={<BarChart3 size={20} />}
              title={t('detail.trendDetail.noSamples')} hint={t('detail.trendDetail.noSamplesHint')} />
              : (
                <>
                  <div className="overflow-x-auto">
                    <Table aria-label={t('detail.trendDetail.tableTitle')} className="min-w-[34rem] text-meta">
                      <TableHeader className="border-0 bg-transparent">
                        <TableRow className="hover:bg-transparent">
                          <TableHead className="px-2 py-2">{t('detail.trendDetail.tableTime')}</TableHead>
                          <TableHead className="px-2 py-2 text-right">{t('detail.trendDetail.tableValue')}</TableHead>
                          <TableHead className="px-2 py-2">{t('detail.trendDetail.tableQuality')}</TableHead>
                        </TableRow>
                      </TableHeader>
                      <TableBody>
                        {pageSamples.map((sample) => (
                          <TableRow key={sample.ts}>
                            <TableCell className="num whitespace-nowrap px-2 py-1.5 text-ink-2">{fmtDateTime(sample.ts)}</TableCell>
                            <TableCell className="num px-2 py-1.5 text-right font-medium">
                              {formatValue(sample.value)}{unit && <span className="ml-1 font-normal text-ink-3">{unit}</span>}
                            </TableCell>
                            <TableCell className="px-2 py-1.5 text-ink-2">
                              {sample.quality ? t(QUALITY_KEYS[sample.quality] ?? 'quality.good') : '—'}
                            </TableCell>
                          </TableRow>
                        ))}
                      </TableBody>
                    </Table>
                  </div>
                  <div className="mt-3 flex flex-wrap items-center justify-between gap-2 border-t border-hairline pt-3">
                    <div className="flex items-center gap-2">
                      <button type="button" className="btn btn-ghost" disabled={safePage === 0}
                        onClick={() => setPage((value) => Math.max(0, value - 1))}>
                        {t('detail.trendDetail.newer')}
                      </button>
                      <button type="button" className="btn btn-ghost" disabled={safePage >= pageCount - 1}
                        onClick={() => setPage((value) => Math.min(pageCount - 1, value + 1))}>
                        {t('detail.trendDetail.older')}
                      </button>
                      <span className="num text-meta text-ink-3">{safePage + 1} / {pageCount}</span>
                    </div>
                    {samplesQuery.hasNextPage && (
                      <button type="button" className="btn btn-ghost"
                        disabled={samplesQuery.isFetchingNextPage}
                        onClick={() => { void samplesQuery.fetchNextPage() }}>
                        {samplesQuery.isFetchingNextPage ? <RefreshCw size={13} className="animate-spin" /> : <Maximize2 size={13} />}
                        {t('detail.trendDetail.loadOlder')}
                      </button>
                    )}
                  </div>
                </>
              )}
        </Panel>
      </div>
    </>
  )
}
