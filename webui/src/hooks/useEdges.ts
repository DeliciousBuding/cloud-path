import { useQuery } from '@tanstack/react-query'
import { api } from '@/lib/api'
import { useLive } from '@/store/ws'
import type { EdgeView } from '@/lib/types'

export interface EdgesResult {
  list: EdgeView[]
  online: number
  loading: boolean
  /** REST 失败且没有任何数据：不得渲染成「没有边缘节点」的假空态 */
  error: unknown
  refetch: () => void
}

/** 边缘节点列表：WS 实时状态覆盖 REST 兜底（离线节点只在 REST 里可见）。 */
export function useEdges(): EdgesResult {
  const live = useLive((s) => s.edges)
  const status = useLive((s) => s.status)
  const connectionEpoch = useLive((s) => s.connectionEpoch)
  const snapshotEpoch = useLive((s) => s.snapshotEpoch)
  const { data, isLoading, error, refetch } = useQuery({
    queryKey: ['edges'], queryFn: api.edges, refetchInterval: 10000, retry: false,
  })

  // WS 只有 open 时是当前权威；断开后 REST 轮询是兜底权威，避免旧在线态粘住。
  // open 只表示握手成功；必须等本次连接的 snapshot 落地后才把空集合当成权威事实。
  const liveAuthoritative = status === 'open' &&
    (connectionEpoch === 0 || snapshotEpoch === connectionEpoch)
  const merged: Record<string, EdgeView> = {}
  if (liveAuthoritative || data === undefined) {
    for (const [id, e] of Object.entries(live)) merged[id] = e
    for (const e of data?.edges ?? []) if (!merged[e.edge_id]) merged[e.edge_id] = e
  } else {
    // REST 已成功返回时它就是权威集合；不能把已断开的旧 live 键补回来。
    for (const e of data.edges) merged[e.edge_id] = e
  }
  const list = Object.values(merged).sort((a, b) => a.edge_id.localeCompare(b.edge_id))

  return {
    list,
    online: list.filter((e) => e.online).length,
    loading: list.length === 0 && !liveAuthoritative && isLoading,
    // WS open 时空快照也是权威事实；只有 REST 才是唯一来源且失败时才报错误态。
    error: list.length === 0 && !liveAuthoritative && !isLoading ? error : null,
    refetch: () => { void refetch() },
  }
}
