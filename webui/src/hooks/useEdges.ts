import { useQuery } from '@tanstack/react-query'
import { api } from '@/lib/api'
import { useLive } from '@/store/ws'
import type { EdgeView } from '@/lib/types'

export interface EdgesResult {
  list: EdgeView[]
  online: number
  loading: boolean
}

/** 边缘节点列表：WS 实时状态覆盖 REST 兜底（离线节点只在 REST 里可见）。 */
export function useEdges(): EdgesResult {
  const live = useLive((s) => s.edges)
  const { data, isLoading } = useQuery({ queryKey: ['edges'], queryFn: api.edges, refetchInterval: 10000 })

  const merged: Record<string, EdgeView> = {}
  for (const e of data?.edges ?? []) merged[e.edge_id] = e
  for (const [id, e] of Object.entries(live)) merged[id] = { ...merged[id], ...e }
  const list = Object.values(merged).sort((a, b) => a.edge_id.localeCompare(b.edge_id))

  return { list, online: list.filter((e) => e.online).length, loading: isLoading && list.length === 0 }
}
