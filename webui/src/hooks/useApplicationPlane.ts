import { useEffect, useRef } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { ApiError, api } from '@/lib/api'
import { useLive } from '@/store/ws'
import { useAuth } from '@/store/auth'

export const APP_RECORD_PAGE_SIZE = 20

/** REST 是记录排序、运行状态和调度的事实源；WS 只通知失效，不建第二份记录库。 */
export function useApplicationPlane(instanceID: string, offset = 0, recordType = '', lifecycleKey = '') {
  const qc = useQueryClient()
  const tenantID = useAuth((s) => s.user?.tenant_id)
  const userID = useAuth((s) => s.user?.id)
  const authenticated = useAuth((s) => s.status === 'in')
  // 服务令牌没有账号 ID（id=0），仍可拥有已认证的租户身份。
  const canRead = authenticated && (tenantID ?? 0) > 0 && userID != null && Boolean(instanceID)
  const status = useLive((s) => s.status)
  const scope = ['application-plane', tenantID, userID, instanceID] as const
  const previousLifecycle = useRef(lifecycleKey)

  useEffect(() => {
    if (!canRead) return
    // 直接订阅单例，避免 React 合批吞掉不同实例紧邻的通知。
    let timer: ReturnType<typeof setTimeout> | undefined
    const invalidate = () => {
      if (timer !== undefined) return
      timer = setTimeout(() => {
        timer = undefined
        void qc.invalidateQueries({ queryKey: ['application-plane', tenantID, userID, instanceID] })
      }, 80)
    }
    const unsubscribe = useLive.subscribe((next, previous) => {
      if (next.connectionEpoch !== previous.connectionEpoch ||
          (next.domainRecord !== previous.domainRecord && next.domainRecord?.instanceID === instanceID)) invalidate()
    })
    return () => { unsubscribe(); clearTimeout(timer) }
  }, [qc, canRead, tenantID, userID, instanceID])

  useEffect(() => {
    if (previousLifecycle.current === lifecycleKey) return
    previousLifecycle.current = lifecycleKey
    if (canRead) void qc.invalidateQueries({ queryKey: ['application-plane', tenantID, userID, instanceID] })
  }, [qc, canRead, tenantID, userID, instanceID, lifecycleKey])

  const shared = {
    enabled: canRead,
    retry: false as const,
    // bindings/jobs 没有独立 WS 通知；连接正常时也轮询，覆盖停止与重启。
    // 权限拒绝不反复敲门；用户仍可手动重试。
    refetchInterval: (query: { state: { error: unknown } }) =>
      query.state.error instanceof ApiError && query.state.error.status === 403 ? false as const : 10_000,
  }
  const records = useQuery({
    ...shared, queryKey: [...scope, 'records', recordType, offset],
    queryFn: ({ signal }) => api.appRecords(instanceID, { offset, recordType, limit: APP_RECORD_PAGE_SIZE }, signal),
  })
  const bindings = useQuery({
    ...shared, queryKey: [...scope, 'bindings'],
    queryFn: ({ signal }) => api.appBindings(instanceID, signal),
  })
  const jobs = useQuery({
    ...shared, queryKey: [...scope, 'jobs'],
    queryFn: ({ signal }) => api.appJobs(instanceID, signal),
  })
  // 只复用公开批量描述符里的名称；没有名称时回落，不猜实体对应的设备。
  const presentation = useQuery({
    queryKey: ['application-plane-labels', tenantID, userID],
    queryFn: api.descriptors,
    enabled: canRead && bindings.isSuccess && bindings.data.bindings.length > 0,
    staleTime: 60_000, refetchInterval: 60_000, retry: false,
  })
  const running = bindings.isSuccess && jobs.isSuccess && bindings.data.running === jobs.data.running
    ? bindings.data.running : undefined
  return { records, bindings, jobs, presentation: presentation.data, status, running, canRead }
}
