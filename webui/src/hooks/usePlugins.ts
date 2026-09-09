// 插件面数据入口：目录（GET /api/plugins）与实例（GET/POST/PATCH/DELETE /api/plugin-instances）。
//
// 约定：
//   - REST 走 TanStack Query，组件保持展示型；
//   - 列表一律过 normalize*（形状不合法就丢弃，绝不让页面白屏）；
//   - 写操作成功后只失效查询、由服务端投影决定新事实 —— **前端不乐观地把
//     desired.enabled 渲染成 observed 运行中**（control-plane-sync.md 不变量 5）；
//   - 错误交给调用方按 lib/plugins.ts 的 pluginErrorCopy 呈现（稳定码，不解析文本）。
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '@/lib/api'
import { normalizeCatalog, normalizeInstance, normalizeInstances } from '@/lib/plugins'
import { toast } from '@/store/toast'
import type {
  PluginCatalogView, PluginInstanceActionRequest, PluginInstanceCreateRequest,
  PluginInstanceDeleteRequest, PluginInstanceUpdateRequest, PluginInstanceView,
} from '@/lib/types'

export const PLUGIN_KEYS = {
  catalog: ['plugin-catalog'] as const,
  instances: ['plugin-instances'] as const,
  instance: (id: string) => ['plugin-instance', id] as const,
}

export interface PluginCatalogResult {
  plugins: PluginCatalogView[]
  loading: boolean
  error: unknown
  refetch: () => void
}

/** 插件目录：插件声明事实（kind/version/digest/verified/permissions/contributes） */
export function usePluginCatalog(): PluginCatalogResult {
  const { data, isLoading, error, refetch } = useQuery({
    queryKey: PLUGIN_KEYS.catalog,
    queryFn: () => api.plugins().then((r) => normalizeCatalog(r)),
    staleTime: 60_000,
    retry: false,
  })
  return {
    plugins: data ?? [], loading: isLoading, error, refetch: () => { void refetch() },
  }
}

export interface PluginInstancesResult {
  instances: PluginInstanceView[]
  loading: boolean
  error: unknown
  isFetching: boolean
  refetch: () => void
}

/** 实例列表（期望态 + 实际态投影），10s 轮询以便看到 Edge 上报带来的变化 */
export function usePluginInstances(intervalMs = 10_000): PluginInstancesResult {
  const { data, isLoading, isFetching, error, refetch } = useQuery({
    queryKey: PLUGIN_KEYS.instances,
    queryFn: () => api.pluginInstances().then((r) => normalizeInstances(r)),
    refetchInterval: intervalMs,
    retry: false,
  })
  return {
    instances: data ?? [], loading: isLoading, error, isFetching,
    refetch: () => { void refetch() },
  }
}

export interface PluginInstanceResult {
  instance: PluginInstanceView | null
  loading: boolean
  error: unknown
}

/** 单实例详情：优先读列表缓存（同一份投影），再单独拉一次以拿到最新 ack */
export function usePluginInstance(id: string): PluginInstanceResult {
  const list = useQuery({
    queryKey: PLUGIN_KEYS.instances,
    queryFn: () => api.pluginInstances().then((r) => normalizeInstances(r)),
    refetchInterval: 10_000,
    retry: false,
  })
  const single = useQuery({
    queryKey: PLUGIN_KEYS.instance(id),
    queryFn: () => api.pluginInstance(id).then(normalizeInstance),
    refetchInterval: 10_000,
    enabled: Boolean(id),
    retry: false,
  })
  const fromList = (list.data ?? []).find((v) => v.id === id) ?? null
  return {
    instance: single.data ?? fromList,
    loading: (single.isLoading && !fromList) || (list.isLoading && !id),
    error: single.error ?? list.error,
  }
}

/** 写操作统一收尾：失效列表与单实例查询，让服务端投影决定界面上的新事实 */
function useInvalidate() {
  const qc = useQueryClient()
  return (id?: string) => {
    void qc.invalidateQueries({ queryKey: PLUGIN_KEYS.instances })
    if (id) void qc.invalidateQueries({ queryKey: PLUGIN_KEYS.instance(id) })
  }
}

export function useCreateInstance() {
  const invalidate = useInvalidate()
  return useMutation({
    mutationFn: (body: PluginInstanceCreateRequest) => api.createPluginInstance(body),
    // onSuccess 的第二个参数就是 mutationFn 收到的 variables，用它兜住响应缺 id 的情况
    onSuccess: (_r, body) => {
      invalidate(_r?.id || `${body.edge_id}/${body.instance_id}`)
      toast.ok('设置已保存', '已保存你的设置；收到运行状态后，这里会显示实际结果。')
    },
  })
}

export function useUpdateInstance() {
  const invalidate = useInvalidate()
  return useMutation({
    mutationFn: ({ id, body }: { id: string; body: PluginInstanceUpdateRequest }) =>
      api.updatePluginInstance(id, body),
    onSuccess: (_r, vars) => {
      invalidate(vars.id)
      toast.ok('设置已更新', '已保存你的修改；收到运行状态后，这里会显示实际结果。')
    },
  })
}

export function useDeleteInstance() {
  const invalidate = useInvalidate()
  return useMutation({
    mutationFn: ({ id, body }: { id: string; body?: PluginInstanceDeleteRequest }) =>
      api.deletePluginInstance(id, body),
    onSuccess: (_r, vars) => {
      invalidate(vars.id)
      toast.ok('实例已删除', '设置已移除；网关更新后会停止这个实例。')
    },
  })
}

export function useReconcileInstance() {
  const invalidate = useInvalidate()
  return useMutation({
    mutationFn: ({ id, body }: { id: string; body?: PluginInstanceActionRequest }) =>
      api.reconcilePluginInstance(id, body),
    onSuccess: (_r, vars) => {
      invalidate(vars.id)
      toast.ok('已请求重新应用', '最新设置已重新发送，正在等待运行状态更新。')
    },
  })
}