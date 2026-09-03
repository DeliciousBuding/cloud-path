import { useQuery } from '@tanstack/react-query'
import { api } from '@/lib/api'
import { useLive } from '@/store/ws'
import type { DeviceView } from '@/lib/types'

export interface DevicesResult {
  list: DeviceView[]
  online: number
  /** 首帧仍在加载（WS 快照与 REST 都还没到） */
  loading: boolean
}

/**
 * 设备列表统一来源：WS 实时快照优先，REST 轮询兜底。
 * 这样即使实时通道断开（或页面刚打开、快照未到），面板也不会空白。
 */
export function useDevices(): DevicesResult {
  const live = useLive((s) => s.devices)
  const status = useLive((s) => s.status)
  const { data, isLoading } = useQuery({
    queryKey: ['devices'],
    queryFn: api.devices,
    refetchInterval: status === 'open' ? 30000 : 10000,
  })

  const merged: Record<string, DeviceView> = {}
  for (const d of data?.devices ?? []) merged[d.id] = d
  for (const [k, d] of Object.entries(live)) merged[k] = { ...merged[k], ...d }
  const list = Object.values(merged).sort((a, b) => a.id.localeCompare(b.id))

  return {
    list,
    online: list.filter((d) => d.online).length,
    loading: list.length === 0 && (isLoading || status === 'connecting'),
  }
}
