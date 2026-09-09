import { useQuery } from '@tanstack/react-query'
import { api } from '@/lib/api'
import { useLive } from '@/store/ws'
import { authIdentity, useAuth } from '@/store/auth'
import type { DeviceView } from '@/lib/types'

export interface DevicesResult {
  list: DeviceView[]
  online: number
  /** 首帧仍在加载（WS 快照与 REST 都还没到） */
  loading: boolean
  /**
   * REST 数据源失败。必须与「真的没有设备」区分开：
   * 接口 500 时渲染「还没有设备接入」是一种假空态（用户会以为集群是空的）。
   * WS 快照已经给出设备时不算失败（实时通道仍是有效数据源）。
   */
  error: unknown
  refetch: () => void
}

/**
 * 设备列表统一来源：WS 实时快照优先，REST 轮询兜底。
 * 这样即使实时通道断开（或页面刚打开、快照未到），面板也不会空白。
 */
export function useDevices(): DevicesResult {
  const identity = useAuth(authIdentity)
  const live = useLive((s) => s.devices)
  const status = useLive((s) => s.status)
  const connectionEpoch = useLive((s) => s.connectionEpoch)
  const snapshotEpoch = useLive((s) => s.snapshotEpoch)
  const { data, isLoading, error, refetch } = useQuery({
    queryKey: ['devices', identity],
    queryFn: api.devices,
    refetchInterval: status === 'open' ? 30000 : 10000,
    retry: false,
  })

  // WS 只有处于 open 时才是当前权威；断开后 REST 轮询必须能纠正遗留的实时缓存。
  // REST 尚未成功时仍用 live 兜底，避免断网期间把最后已知状态清空。
  // open 只表示握手成功；必须等本次连接的 snapshot 落地后才把空集合当成权威事实。
  const liveAuthoritative = status === 'open' &&
    (connectionEpoch === 0 || snapshotEpoch === connectionEpoch)
  const merged: Record<string, DeviceView> = {}
  if (liveAuthoritative) {
    // 本次连接的 snapshot 是权威集合；REST 里只可能更旧，不能把已删/不属于当前
    // 租户的键补回来。
    for (const [k, d] of Object.entries(live)) merged[k] = d
  } else if (data !== undefined) {
    // REST 已成功返回时它就是权威集合；不能把已断开的旧 live 键补回来。
    for (const d of data.devices) merged[d.id] = d
  } else {
    // REST 尚未成功时仍用 live 兜底，避免断网期间把最后已知状态清空。
    for (const [k, d] of Object.entries(live)) merged[k] = d
  }
  const list = Object.values(merged).sort((a, b) => a.id.localeCompare(b.id))

  return {
    list,
    online: list.filter((d) => d.online).length,
    loading: list.length === 0 && !liveAuthoritative && (isLoading || status !== 'closed'),
    // WS open 时空快照也是权威事实；只有 REST 才是唯一来源且失败时才报错误态。
    error: list.length === 0 && !liveAuthoritative && !isLoading ? error : null,
    refetch: () => { void refetch() },
  }
}
