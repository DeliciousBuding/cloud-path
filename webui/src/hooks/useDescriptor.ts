// Descriptor / Capability 数据入口（A2 消费侧）。
//
// 事实源优先级（新→旧）：
//   1. WS 实时下发的 Descriptor（store/ws.ts descriptors[key]，含内联在 state 载荷里的过渡形态）
//   2. REST 单设备载荷里内联的 Descriptor（后端若把 descriptor 挂在 DeviceView 上）
//   3. GET /api/devices/{edge}/{dev}/descriptor
//   4. GET /api/descriptors（批量；列表页共享一次请求）
// 全部缺席 → descriptor=null，UI 走「通用值渲染」回落（不报错、不空白）。
// Capability catalog（presentation / actions 事实源）同理：GET /api/capabilities + 随 Descriptor 一并返回的 capabilities。
import { useMemo } from 'react'
import { useQuery } from '@tanstack/react-query'
import { api } from '@/lib/api'
import { useLive } from '@/store/ws'
import {
  EMPTY_INDEX, commandActions, indexCapabilities, normalizeCapabilityDocs,
  normalizeDescriptor, pickDescriptorFor, readInlineDescriptor,
} from '@/lib/descriptor'
import type { CapabilityIndex, CommandSet } from '@/lib/descriptor'
import type { DeviceDescriptor, DeviceView } from '@/lib/types'

export type DescriptorSource = 'ws' | 'inline' | 'rest' | 'bulk' | 'none'

export interface DescriptorResult {
  descriptor: DeviceDescriptor | null
  capabilities: CapabilityIndex
  /** Descriptor 从哪条通道来（UI 上标注「Schema 驱动 / 通用回落」用） */
  source: DescriptorSource
  loading: boolean
  /** 命令集：Capability actions / Descriptor commands 优先，回落适配器白名单 */
  commands: CommandSet
}

interface Options {
  /** 已有的设备视图（用于嗅探内联 Descriptor） */
  device?: DeviceView | null
  /** 适配器命令白名单（/api/adapters），Descriptor 缺席时的命令集回落 */
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
  const live = useLive((s) => s.descriptors[key])

  const bulk = useQuery({
    queryKey: ['descriptors'],
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
    queryKey: ['descriptor', key],
    queryFn: () => api.deviceDescriptor(edgeId, devId),
    enabled: !live && !bulkHit && bulkSettled,
    staleTime: 5 * 60_000,
    refetchInterval: 120_000,
    retry: false,
  })

  const catalog = useQuery({
    queryKey: ['capabilities'],
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

  const source: DescriptorSource =
    live ? 'ws' : inline ? 'inline' : singleSplit.descriptor ? 'rest' : bulkHit ? 'bulk' : 'none'

  const capabilities = useMemo(() => {
    const docs = [...catalogDocs, ...bulkDocs, ...normalizeCapabilityDocs(singleSplit.docs)]
    return docs.length ? indexCapabilities(docs) : EMPTY_INDEX
  }, [catalogDocs, bulkDocs, singleSplit.docs])

  const commands = useMemo(
    () => commandActions({
      descriptor, index: capabilities, adapterCommands: opts.adapterCommands,
    }),
    // adapterCommands 来自上层 useMemo 的稳定数组引用
    [descriptor, capabilities, opts.adapterCommands],
  )

  const loading = !descriptor && (bulk.isLoading || single.isLoading || catalog.isLoading)

  return { descriptor, capabilities, source, loading, commands }
}

/** 只要 Capability catalog（无设备上下文，例如事件/命令标签的通用推导） */
export function useCapabilityIndex(): CapabilityIndex {
  const { data } = useQuery({
    queryKey: ['capabilities'],
    queryFn: api.capabilities,
    staleTime: 10 * 60_000,
    retry: false,
  })
  return useMemo(() => {
    const docs = normalizeCapabilityDocs(data ?? null)
    return docs.length ? indexCapabilities(docs) : EMPTY_INDEX
  }, [data])
}