import { Inbox } from 'lucide-react'
import { PageHeader, EmptyState } from '@/components/ui'
import { DeviceCard } from '@/components/DeviceCard'
import { DeviceCardSkeleton } from '@/components/Skeleton'
import { useDevices } from '@/hooks/useDevices'

export default function Devices() {
  const { list, online, loading } = useDevices()

  return (
    <>
      <PageHeader title="设备"
        subtitle={loading ? '正在加载设备…' : `共 ${list.length} 台 · ${online} 台在线`} />
      {loading ? (
        <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
          <DeviceCardSkeleton /><DeviceCardSkeleton /><DeviceCardSkeleton />
        </div>
      ) : list.length === 0 ? (
        <EmptyState icon={<Inbox size={24} />} title="还没有设备接入"
          hint="在边缘主机上配置 edge.yaml 并启动 cloudpath-edge，设备会自动注册到这里。" />
      ) : (
        <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
          {list.map((d) => <DeviceCard key={d.id} d={d} />)}
        </div>
      )}
    </>
  )
}
