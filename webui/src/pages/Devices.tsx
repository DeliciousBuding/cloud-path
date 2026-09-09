import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Cpu, Inbox, Search, SearchX, WifiOff } from 'lucide-react'
import { Button, EmptyState, ErrorState, Input, PageHeader, Panel, Segmented } from '@/components/ui'
import { RowSkeleton } from '@/components/Skeleton'
import { DeviceRow, DeviceRowHead } from '@/components/DeviceRow'
import { useDevices } from '@/hooks/useDevices'
import { usePageTitle } from '@/hooks/usePageTitle'
import type { DeviceView } from '@/lib/types'

type Filter = 'all' | 'online' | 'offline'

function matches(d: DeviceView, q: string): boolean {
  if (!q) return true
  const hay = `${d.name ?? ''} ${d.id} ${d.edge_id} ${d.adapter} ${d.port ?? ''}`.toLowerCase()
  return q.split(/\s+/).filter(Boolean).every((w) => hay.includes(w))
}

/**
 * 设备列表：Name / 网关 / Capabilities / Online-Offline / Last Seen。
 * 设备可能很多 —— 因此是紧凑列表（不是卡片栅格），并给出搜索与在线状态过滤，
 * 过滤后无结果与「一台都没有」是两种不同的空态。
 */
export default function Devices() {
  const { t } = useTranslation('devices')
  usePageTitle(t('page.title'))

  const { list, online, loading, error, refetch } = useDevices()
  const [filter, setFilter] = useState<Filter>('all')
  const [q, setQ] = useState('')

  const shown = useMemo(() => {
    const byStatus = filter === 'online' ? list.filter((d) => d.online)
      : filter === 'offline' ? list.filter((d) => !d.online) : list
    return byStatus
      .filter((d) => matches(d, q.trim().toLowerCase()))
      .sort((a, b) => {
        // 列表默认把需要处理的离线设备放在前面；同状态按最近上报倒序，避免每次轮询跳位。
        if (a.online !== b.online) return a.online ? 1 : -1
        const at = Math.max(a.updated_at ?? 0, a.last_seen ?? 0)
        const bt = Math.max(b.updated_at ?? 0, b.last_seen ?? 0)
        if (at !== bt) return bt - at
        return a.id.localeCompare(b.id)
      })
  }, [list, filter, q])

  const offline = list.length - online
  const subtitle = loading ? t('list.subtitle.loading')
    : error ? t('list.subtitle.error')
      : list.length === 0 ? t('list.subtitle.empty')
        : offline > 0 ? t('list.subtitle.mixed', { online, offline })
          : t('list.subtitle.allOnline', { count: online })

  return (
    <>
      <PageHeader
        title={t('page.title')}
        subtitle={!loading && !error && list.length === 0 ? undefined : subtitle}
        actions={
          list.length > 0 ? (
            <Segmented
              label={t('list.filter.label')}
              value={filter}
              onChange={setFilter}
              options={[
                { value: 'all', label: t('list.filter.all', { count: list.length }) },
                { value: 'online', label: t('list.filter.online', { count: online }) },
                { value: 'offline', label: t('list.filter.offline', { count: offline }) },
              ]}
            />
          ) : undefined
        }
      />

      {list.length > 0 && (
        <div className="mb-4 flex items-center gap-2">
          <label className="sr-only" htmlFor="dev-search">{t('list.search.label')}</label>
          <span className="relative min-w-0 flex-1">
            <Search size={14} className="pointer-events-none absolute left-3.5 top-1/2 -translate-y-1/2 text-ink-3" />
            <Input
              id="dev-search" type="search" value={q} placeholder={t('list.search.placeholder')}
              onChange={(e) => setQ(e.target.value)}
              className="input-search max-w-full"
            />
          </span>
          {q && (
            <Button variant="ghost" className="shrink-0" onClick={() => setQ('')}>{t('list.search.clear')}</Button>
          )}
        </div>
      )}

      {loading ? (
        <Panel><RowSkeleton rows={6} /></Panel>
      ) : error ? (
        // 接口失败 ≠ 没有设备：必须分开说，否则用户会以为集群是空的
        <ErrorState icon={<WifiOff size={20} />} title={t('list.error.title')}
          hint={t('list.error.hint')}
          onRetry={refetch} />
      ) : list.length === 0 ? (
        <EmptyState icon={<Inbox size={24} />} title={t('list.empty.title')}
          hint={t('list.empty.hint')} />
      ) : shown.length === 0 ? (
        <EmptyState icon={<SearchX size={24} />} title={t('list.noMatch.title')}
          hint={q ? t('list.noMatch.query', { query: q }) : t('list.noMatch.filter')} />
      ) : (
        <Panel className="overflow-hidden p-0">
          <ul className="m-0 list-none p-0">
            <DeviceRowHead />
            {shown.slice(0, 300).map((d) => <DeviceRow key={d.id} d={d} />)}
          </ul>
          {shown.length > 300 && (
            <p className="border-t border-hairline px-4 py-3 text-center text-meta text-ink-3">
              {t('list.limit', { count: shown.length })}
            </p>
          )}
          <p className="flex items-center gap-1.5 border-t border-hairline px-4 py-2.5 text-meta text-ink-3">
            <Cpu size={11} className="shrink-0" />
            {t('list.note')}
          </p>
        </Panel>
      )}
    </>
  )
}
