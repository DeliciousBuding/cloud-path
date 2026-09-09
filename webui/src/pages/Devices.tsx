import { useMemo, useState } from 'react'
import { AlertTriangle, Cpu, Inbox, Search, SearchX, WifiOff } from 'lucide-react'
import { EmptyState, ErrorState, PageHeader, Panel, Segmented } from '@/components/ui'
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
 * 设备列表：Name / Edge / Capabilities / Online-Offline / Last Seen。
 * 设备可能很多 —— 因此是紧凑列表（不是卡片栅格），并给出搜索与在线状态过滤，
 * 过滤后无结果与「一台都没有」是两种不同的空态。
 */
export default function Devices() {
  usePageTitle('设备')

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
  const subtitle = loading ? '正在加载设备状态…'
    : error ? '设备状态暂不可用'
      : list.length === 0 ? '还没有设备接入'
        : offline > 0 ? `${online} 台在线 · ${offline} 台离线需关注`
          : `${online} 台在线 · 全部在线`

  return (
    <>
      <PageHeader
        title="设备"
        subtitle={subtitle}
        actions={
          list.length > 0 ? (
            <Segmented
              label="在线状态筛选"
              value={filter}
              onChange={setFilter}
              options={[
                { value: 'all', label: `全部 ${list.length}` },
                { value: 'online', label: `在线 ${online}` },
                { value: 'offline', label: `离线 ${offline}` },
              ]}
            />
          ) : undefined
        }
      />

      {!loading && !error && offline > 0 && (
        <div className="banner mb-5 rounded-lg fade-up" role="status">
          <AlertTriangle size={13} className="shrink-0" />
          <span className="min-w-0 break-words">
            {offline} 台设备离线，先检查所属网关和网络；设备恢复上报后会自动回到在线。
          </span>
          <button type="button" className="link ml-auto shrink-0 text-[12px]" onClick={() => setFilter('offline')}>
            只看离线
          </button>
        </div>
      )}

      {list.length > 0 && (
        <div className="mb-4 flex items-center gap-2">
          <label className="sr-only" htmlFor="dev-search">搜索设备</label>
          <span className="relative min-w-0 flex-1">
            <Search size={14} className="pointer-events-none absolute left-3.5 top-1/2 -translate-y-1/2 text-ink-3" />
            <input
              id="dev-search" type="search" value={q} placeholder="按名称、编号、网关或设备类型搜索"
              onChange={(e) => setQ(e.target.value)}
              className="input input-search max-w-full"
            />
          </span>
          {q && (
            <button type="button" className="btn btn-ghost shrink-0" onClick={() => setQ('')}>清除</button>
          )}
        </div>
      )}

      {loading ? (
        <Panel><RowSkeleton rows={6} /></Panel>
      ) : error ? (
        // 接口失败 ≠ 没有设备：必须分开说，否则用户会以为集群是空的
        <ErrorState icon={<WifiOff size={20} />} title="设备列表加载失败"
          hint="暂时无法加载设备列表。这不表示没有设备已接入，请检查服务是否正常后重试。"
          onRetry={refetch} />
      ) : list.length === 0 ? (
        <EmptyState icon={<Inbox size={24} />} title="还没有设备接入"
          hint="启动网关并完成设备接入后，设备会自动出现在这里。" />
      ) : shown.length === 0 ? (
        <EmptyState icon={<SearchX size={24} />} title="没有匹配的设备"
          hint={q ? `没有设备匹配「${q}」。试试只搜名称的一部分，或清除筛选条件。` : '当前筛选条件下没有设备，换一个状态试试。'} />
      ) : (
        <Panel className="overflow-hidden p-0">
          <ul className="m-0 list-none p-0">
            <DeviceRowHead />
            {shown.slice(0, 300).map((d) => <DeviceRow key={d.id} d={d} />)}
          </ul>
          {shown.length > 300 && (
            <p className="border-t border-hairline px-4 py-3 text-center text-[12px] text-ink-3">
              仅显示前 300 台（共 {shown.length} 台匹配）；请用搜索或在线状态筛选缩小范围
            </p>
          )}
          <p className="flex items-center gap-1.5 border-t border-hairline px-4 py-2.5 text-[12px] text-ink-3">
            <Cpu size={11} className="shrink-0" />
            关键数据来自设备上报；「等待同步」表示设备还没有上报可显示的数据。
          </p>
        </Panel>
      )}
    </>
  )
}
