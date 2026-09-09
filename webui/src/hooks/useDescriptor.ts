// Descriptor / Capability 数据入口（A2 消费侧）。
//
// 事实源优先级（新→旧）：
//   1. WS 实时下发的 Descriptor（store/ws.ts descriptors[key]，含内联在 state 载荷里的过渡形态）
//   2. REST 单设备载荷里内联的 Descriptor（后端若把 descriptor 挂在 DeviceView 上）
//   3. GET /api/devices/{edge}/{dev}/descriptor
//   4. GET /api/descriptors（批量；列表页共享一次请求）
// 仅 404/405/501 属于缺席 → descriptor=null，UI 走「通用值渲染」回落；
// 502/503/504 或网络故障属于真实错误 → error/errorStatus 必须上抛给 UI。
// Capability catalog（presentation / actions 事实源）同理：GET /api/capabilities + 随 Descriptor 一并返回的 capabilities。
import { useMemo } from 'react'
import { useQuery } from '@tanstack/react-query'
import { ApiError, api } from '@/lib/api'
import { useLive } from '@/store/ws'
import { authIdentity, useAuth } from '@/store/auth'
import {
  EMPTY_INDEX, commandActions, indexCapabilities, normalizeCapabilityDocs,
  normalizeDescriptor, pickDescriptorFor, readInlineDescriptor,
} from '@/lib/descriptor'
import type { CapabilityIndex, CommandSet } from '@/lib/descriptor'
import type { DeviceDescriptor, DeviceView } from '@/lib/types'

export type DescriptorSource = 'ws' | 'inline' | 'rest' | 'bulk' | 'none' | 'error'

export interface DescriptorResult {
  descriptor: DeviceDescriptor | null
  capabilities: CapabilityIndex
  /** Descriptor 从哪条通道来（UI 上标注「Schema 驱动 / 通用回落」用） */
  source: DescriptorSource
  loading: boolean
  /** Descriptor/Capability 读取的真实故障；404/405/501 缺席时为 null。 */
  error: unknown | null
  /** ApiError 的 HTTP 状态码，便于调用方区分错误态文案。 */
  errorStatus: number | null
  /** 操作集：Capability actions / Descriptor commands 优先，回落适配器白名单 */
  commands: CommandSet
}

interface Options {
  /** 已有的设备视图（用于嗅探内联 Descriptor） */
  device?: DeviceView | null
  /** 适配器操作白名单（/api/adapters），Descriptor 缺席时的操作集回落 */
  adapterCommands?: string[]
  /** 关闭批量探测（详情页已单独探测时用不上） */
  skipBulk?: boolean
}

/** 从任意 REST 载荷里同时取出 descriptor 与随行的 capabilities */
function splitPayload(payload: unknown): { descriptor: DeviceDescriptor | null; docs: unknown[] } {
  const docs: unknown[] = []
  const o = payload && typeof payload === 'object' && !Array.isArray(payload)
    ? (payload as Record<string, unknown>) : null
  if (o) {
    for (const k of ['capabilities', 'capabilityCatalog', 'catalog']) {
      const v = o[k]
      if (Array.isArray(v)) docs.push(...v)
      else if (v && typeof v === 'object') docs.push(...Object.values(v as object))
    }
  }
  return { descriptor: normalizeDescriptor(payload), docs }
}

export function useDeviceDescriptor(
  key: string, edgeId: string, devId: string, opts: Options = {},
): DescriptorResult {
  const identity = useAuth(authIdentity)
  const live = useLive((s) => s.descriptors[key])

  const bulk = useQuery({
    queryKey: ['descriptors', identity],
    queryFn: api.descriptors,
    enabled: !opts.skipBulk,
    staleTime: 60_000,
    refetchInterval: 120_000,
    retry: false,
  })

  const bulkHit = useMemo(
    () => (bulk.data == null ? null : pickDescriptorFor(bulk.data, edgeId, devId)),
    [bulk.data, edgeId, devId],
  )
  const bulkDocs = useMemo(
    () => (bulk.data == null ? [] : normalizeCapabilityDocs(
      (bulk.data as { capabilities?: unknown })?.capabilities ?? bulk.data,
    )),
    [bulk.data],
  )

  // 批量端点在飞时不要抢跑单设备探测：列表页每张卡都会挂一个本 hook，
  // 抢跑等于「N 张卡 × 1 次单设备请求」全部打在批量结果落地之前（纯浪费）。
  // skipBulk（详情页自行探测）或批量已结算（含 404 缺席）时才允许单设备探测。
  const bulkSettled = Boolean(opts.skipBulk) || bulk.isSuccess || bulk.isError

  const single = useQuery({
    queryKey: ['descriptor', key, identity],
    queryFn: () => api.deviceDescriptor(edgeId, devId),
    enabled: !live && !bulkHit && bulkSettled,
    staleTime: 5 * 60_000,
    refetchInterval: 120_000,
    retry: false,
  })

  const catalog = useQuery({
    queryKey: ['capabilities', identity],
    queryFn: api.capabilities,
    staleTime: 10 * 60_000,
    retry: false,
  })

  // deps 用值级标识（updated_at / state 引用），避免上层每次合并出的新对象触发重复嗅探
  const device = opts.device ?? null
  const inline = useMemo(
    () => readInlineDescriptor(device),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [device?.id, device?.updated_at, device?.state],
  )
  const singleSplit = useMemo(() => splitPayload(single.data ?? null), [single.data])
  const catalogDocs = useMemo(() => normalizeCapabilityDocs(catalog.data ?? null), [catalog.data])

  const descriptor = live ?? inline ?? singleSplit.descriptor ?? bulkHit ?? null

  // Descriptor 已有事实源时，批量探测失败不覆盖成功结果；Capability catalog 失败仍要上报，
  // 因为它会影响动作/展示声明。没有 Descriptor 时，任何非缺席失败都阻止“无操作”假象。
  const error = descriptor
    ? (catalog.error ?? null)
    : (bulk.error ?? single.error ?? catalog.error ?? null)
  const errorStatus = error instanceof ApiError ? error.status : null

  const source: DescriptorSource =
    live ? 'ws' : inline ? 'inline' : singleSplit.descriptor ? 'rest' : bulkHit ? 'bulk'
      : error ? 'error' : 'none'

  const capabilities = useMemo(() => {
    const docs = [...catalogDocs, ...bulkDocs, ...normalizeCapabilityDocs(singleSplit.docs)]
    return docs.length ? indexCapabilities(docs) : EMPTY_INDEX
  }, [catalogDocs, bulkDocs, singleSplit.docs])

  const commands = useMemo(
    () => (!descriptor && error)
      ? { actions: [], source: 'none' } as CommandSet
      : commandActions({
        descriptor, index: capabilities, adapterCommands: opts.adapterCommands,
      }),
    // adapterCommands 来自上层 useMemo 的稳定数组引用
    [descriptor, capabilities, opts.adapterCommands, error],
  )

  const loading = !descriptor && (bulk.isLoading || single.isLoading || catalog.isLoading)

  return { descriptor, capabilities, source, loading, error, errorStatus, commands }
}

/** Capability catalog 的读取状态；索引字段与 CapabilityIndex 完全兼容。 */
export type CapabilityIndexResult = CapabilityIndex & {
  loading: boolean
  /** 404/405/501 缺席时为 null；502/网络故障保留真实错误。 */
  error: unknown | null
  errorStatus: number | null
}

/** 只要 Capability catalog（无设备上下文，例如事件/操作标签的通用推导） */
export function useCapabilityIndex(): CapabilityIndexResult {
  const identity = useAuth(authIdentity)
  const { data, isLoading, error } = useQuery({
    queryKey: ['capabilities', identity],
    queryFn: api.capabilities,
    staleTime: 10 * 60_000,
    retry: false,
  })
  const index = useMemo(() => {
    const docs = normalizeCapabilityDocs(data ?? null)
    return docs.length ? indexCapabilities(docs) : EMPTY_INDEX
  }, [data])
  return useMemo(() => ({
    ...index,
    loading: isLoading,
    error: error ?? null,
    errorStatus: error instanceof ApiError ? error.status : null,
  }), [index, isLoading, error])
}
