import { useMemo, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Activity, RefreshCw, FilterX } from 'lucide-react'
import { PageHeader, Panel, EmptyState, Spinner } from '@/components/ui'
import { RowSkeleton } from '@/components/Skeleton'
import { EventFeed } from '@/components/EventFeed'
import { api } from '@/lib/api'
import { useLive } from '@/store/ws'
import { useDevices } from '@/hooks/useDevices'
import { mergeEvents, eventLabel } from '@/lib/format'
import { useCapabilityIndex } from '@/hooks/useDescriptor'
import { cn } from '@/lib/cn'

const PAGE_LIMIT = 500

export default function Events() {
  const liveEvents = useLive((s) => s.events)
  const { list: devices } = useDevices()
  const [device, setDevice] = useState('')
  const [types, setTypes] = useState<Set<string>>(new Set())
  const index = useCapabilityIndex()

  const { data, isFetching, isLoading, refetch } = useQuery({
    queryKey: ['events', device],
    queryFn: () => api.events({ device: device || undefined, limit: PAGE_LIMIT }),
    refetchInterval: 5000,
  })

  const merged = useMemo(() => {
    const live = device ? liveEvents.filter((e) => e.device_id === device) : liveEvents
    return mergeEvents(live, data?.events ?? [])
      .filter((e) => types.size === 0 || types.has(e.type))
  }, [liveEvents, data, device, types])

  // 过滤选项由当前数据里出现过的事件类型动态生成——前端不维护事件类型枚举
  const typeOptions = useMemo(() => {
    const set = new Set<string>()
    for (const e of merged) set.add(e.type)
    return [...set].sort()
  }, [merged])

  const toggleType = (t: string) =>
    setTypes((prev) => {
      const next = new Set(prev)
      if (next.has(t)) next.delete(t)
      else next.add(t)
      return next
    })

  const atLimit = (data?.events.length ?? 0) >= PAGE_LIMIT

  return (
    <>
      <PageHeader
        title="事件"
        subtitle={`全部设备的上报事件 · 当前视图 ${merged.length} 条`}
        actions={
          <button type="button" className="btn btn-ghost" onClick={() => refetch()} title="立即刷新">
            {isFetching ? <Spinner size={13} /> : <RefreshCw size={13} />} 刷新
          </button>
        }
      />

      <Panel className="mb-5">
        <div className="flex flex-wrap items-center gap-2">
          <label className="sr-only" htmlFor="ev-device">按设备筛选</label>
          <select
            id="ev-device"
            value={device}
            onChange={(e) => setDevice(e.target.value)}
            className="rounded-full border border-hairline bg-surface px-3 py-1.5 text-xs font-medium outline-none focus:border-accent"
          >
            <option value="">全部设备</option>
            {devices.map((d) => (
              <option key={d.id} value={d.id}>{d.name ? `${d.name}（${d.id}）` : d.id}</option>
            ))}
          </select>
          <span className="mx-1 h-4 w-px bg-hairline" aria-hidden />
          {typeOptions.map((t) => (
            <button
              key={t}
              type="button"
              onClick={() => toggleType(t)}
              aria-pressed={types.has(t)}
              title={t}
              className={cn(
                'rounded-full px-3 py-1 text-[11px] font-medium transition-colors',
                types.has(t) ? 'bg-accent text-accent-ink' : 'bg-ink-3/10 text-ink-2 hover:bg-ink-3/16',
              )}
            >
              {eventLabel(t, index)}
            </button>
          ))}
          {(types.size > 0 || device) && (
            <button type="button" onClick={() => { setTypes(new Set()); setDevice('') }}
              className="link flex items-center gap-0.5 text-[11px]" title="清除全部筛选">
              <FilterX size={11} /> 清除筛选
            </button>
          )}
        </div>
      </Panel>

      <Panel title={<span className="flex items-center gap-1.5"><Activity size={14} />事件流</span>}
        right={isFetching ? <Spinner size={12} className="text-ink-3" /> : undefined}>
        {isLoading ? (
          <RowSkeleton rows={8} />
        ) : merged.length === 0 ? (
          <EmptyState icon={<Activity size={24} />} title="没有匹配的事件"
            hint="调整筛选条件，或等待设备上报。" />
        ) : (
          <>
            <EventFeed events={merged} limit={200} />
            {atLimit && (
              <p className="mt-3 border-t border-hairline pt-3 text-center text-[11px] text-ink-3">
                仅显示最近 {PAGE_LIMIT} 条（更早的历史仍在数据库中，可按设备筛选查看）
              </p>
            )}
          </>
        )}
      </Panel>
    </>
  )
}
