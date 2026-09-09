import { useInfiniteQuery } from '@tanstack/react-query'
import { api } from '@/lib/api'

export interface DeviceSamplesOptions {
  from?: number
  to?: number
  limit?: number
}

/** 设备数值序列历史：REST 游标分页，不依赖 WebSocket，因此离线设备也能打开。 */
export function useDeviceSamples(
  edgeId: string, deviceId: string, seriesKey: string, options: DeviceSamplesOptions = {},
) {
  return useInfiniteQuery({
    queryKey: ['device-samples', edgeId, deviceId, seriesKey, options.from, options.to, options.limit],
    queryFn: ({ pageParam }) => api.deviceSamples(edgeId, deviceId, {
      key: seriesKey,
      from: options.from,
      to: options.to,
      before: pageParam || undefined,
      limit: options.limit,
    }),
    initialPageParam: 0,
    getNextPageParam: (last) => (last.next_before && last.next_before > 0 ? last.next_before : undefined),
    enabled: Boolean(edgeId && deviceId && seriesKey),
    staleTime: 30_000,
  })
}
