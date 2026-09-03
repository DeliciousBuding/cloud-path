import { useMemo, useState } from 'react'
import { Cpu, Inbox, Search, SearchX } from 'lucide-react'
import { EmptyState, PageHeader, Panel, Segmented } from '@/components/ui'
import { RowSkeleton } from '@/components/Skeleton'
import { DeviceRow, DeviceRowHead } from '@/components/DeviceRow'
import { useDevices } from '@/hooks/useDevices'
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
  const { list, online, loading } = useDevices()
  const [filter, setFilter] = useState<Filter>('all')
  const [q, setQ] = useState('')

  const shown = useMemo(() => {
    const byStatus = filter === 'online' ? list.filter((d) => d.online)
      : filter === 'offline' ? list.filter((d) => !d.online) : list
    return byStatus.filter((d) => matches(d, q.trim().toLowerCase()))
  }, [list, filter, q])

  const offline = list.length - online

  return (
    <>
      <PageHeader
        title="设备"
        subtitle={loading ? '正在加载设备…' : `共 ${list.length} 台 · ${online} 台在线 · ${offline} 台离线`}
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

      {list.length > 0 && (
        <div className="mb-4 flex items-center gap-2">
          <label className="sr-only" htmlFor="dev-search">搜索设备</label>
          <span className="relative min-w-0 flex-1">
            <Search size={14} className="pointer-events-none absolute left-3.5 top-1/2 -translate-y-1/2 text-ink-3" />
            <input
              id="dev-search" type="search" value={q} placeholder="按名称、设备键、边缘节点或适配器搜索"
              onChange={(e) => setQ(e.target.value)}
              className="input max-w-full pl-9 text-[13px]"
            />
          </span>
          {q && (
            <button type="button" className="btn btn-ghost shrink-0" onClick={() => setQ('')}>清除</button>
          )}
        </div>
      )}

      {loading ? (
        <Panel><RowSkeleton rows={6} /></Panel>
      ) : list.length === 0 ? (
        <EmptyState icon={<Inbox size={24} />} title="还没有设备接入"
          hint="在边缘主机上配置 edge.yaml 并启动 cloudpath-edge，设备会自动注册到这里。" />
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
            <p className="border-t border-hairline px-4 py-3 text-center text-[11px] text-ink-3">
              仅显示前 300 台（共 {shown.length} 台匹配）；请用搜索或在线状态筛选缩小范围
            </p>
          )}
          <p className="flex items-center gap-1.5 border-t border-hairline px-4 py-2.5 text-[11px] text-ink-3">
            <Cpu size={11} className="shrink-0" />
            能力来自设备上报的 Descriptor 声明；「能力未知」表示该设备还没有提供 Descriptor。
          </p>
        </Panel>
      )}
    </>
  )
}