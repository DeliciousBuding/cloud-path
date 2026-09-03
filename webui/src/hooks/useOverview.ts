import { useQuery } from '@tanstack/react-query'
import { api } from '@/lib/api'
import { normalizeOverview } from '@/lib/overview'
import type { OverviewView } from '@/lib/types'

export interface OverviewResult {
  /** 已归一化：即使后端字段缺席也是安全的空值，页面可直接 .map */
  data: OverviewView | undefined
  loading: boolean
  error: unknown
  isFetching: boolean
  refetch: () => void
}

/**
 * 概览聚合读面（GET /api/overview → api.OverviewView）。
 * 计数与列表全部由 server 聚合，前端不自算 —— 避免「前端算一套、后端算一套」的假数据分歧。
 * 归一化在这里做一次，页面组件保持展示型。
 */
export function useOverview(intervalMs = 15_000): OverviewResult {
  const { data, isLoading, isFetching, error, refetch } = useQuery({
    queryKey: ['overview'],
    queryFn: () => api.overview().then(normalizeOverview),
    refetchInterval: intervalMs,
  })
  return { data, loading: isLoading, error, isFetching, refetch: () => { void refetch() } }
}