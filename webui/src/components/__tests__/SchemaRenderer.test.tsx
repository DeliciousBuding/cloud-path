// SchemaRenderer 组件测试：声明 → DOM 的映射，重点覆盖
//   ① 未知 Capability 的通用回落（表格 / JSON，不白屏、不猜语义）
//   ② presentation / properties 声明驱动的 widget 与量程
//   ③ quality 状态提示的无障碍暴露
//   ④ 390px 溢出收口（表格/JSON 局部滚动，容器可键盘聚焦）
import { render, screen, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import {
  CapabilityBrowser, GenericTable, JsonBlock, ObservationTable,
  MetricTile, QualityDot, RawView, StateMatrix, StatusBadge, ValueWidget,
} from '@/components/SchemaRenderer'
import { indexCapabilities, normalizeCapabilityDocs, observationsOf } from '@/lib/descriptor'
import {
  CAP_TEMPERATURE, UNKNOWN_CAP, catalogPayload, makeDescriptor, makeDeviceView,
} from '@/test/fixtures'
import type { DescriptorEntity, Observation } from '@/lib/types'

const idx = indexCapabilities(normalizeCapabilityDocs(catalogPayload))
const descriptor = makeDescriptor()
const tempEntity = descriptor.entities[0] as DescriptorEntity
const relayEntity = descriptor.entities[1] as DescriptorEntity
const diagEntity = descriptor.entities[2] as DescriptorEntity

function obs(capability: string, property: string, value: unknown, extra: Partial<Observation> = {}): Observation {
  return { capability, property, value, ...extra }
}

describe('未知 Capability 回落', () => {
  it('StateMatrix 默认视图不放未收录标注/URI/原始 JSON（human-first）', () => {
    const { container } = render(<StateMatrix descriptor={descriptor} idx={idx} />)
    expect(screen.queryByText('未收录 Capability · 通用视图')).toBeNull()
    expect(screen.queryByRole('group', { name: /原始 JSON/ })).toBeNull()
    // 机器 ID 与载荷不进入默认视图：整块文本里不允许出现 URI 或 JSON 键
    const text = container.textContent ?? ''
    expect(text).not.toContain('cloudpath.dev')
    expect(text).not.toContain('"capability"')
    // 展示名是人类可读层：实体名与属性名都在
    expect(screen.getByText('温度探针')).toBeInTheDocument()
  })

  it('StateMatrix 有会话序列时行内嵌火花线，无序列不画假线', () => {
    const series = { 'e-temp.current': [{ t: 1, v: 20 }, { t: 2, v: 22 }, { t: 3, v: 21 }] }
    const { container, unmount } = render(<StateMatrix descriptor={descriptor} idx={idx} series={series} />)
    expect(container.querySelector('svg[aria-hidden]')).not.toBeNull()
    unmount()
    const plain = render(<StateMatrix descriptor={descriptor} idx={idx} />)
    expect(plain.container.querySelector('svg[aria-hidden]')).toBeNull()
  })

  it('CapabilityBrowser 标注未收录引用且 canonical ID 可见（机器 ID 只在开发层）', () => {
    render(<CapabilityBrowser descriptor={descriptor} idx={idx} />)
    expect(screen.getAllByText('未收录').length).toBeGreaterThan(0)
    expect(screen.getByText(UNKNOWN_CAP)).toBeInTheDocument()
    expect(screen.getByText(CAP_TEMPERATURE)).toBeInTheDocument()
  })

  it('ObservationTable 给未收录观测打「未收录」徽标，已收录的不打', () => {
    render(<ObservationTable observations={observationsOf(diagEntity)} idx={idx} />)
    expect(screen.getAllByText('未收录')).toHaveLength(2)
    render(<ObservationTable observations={observationsOf(tempEntity)} idx={idx} />)
    expect(screen.getAllByText('未收录')).toHaveLength(2) // 仍只有诊断面那两条
  })

  it('对象数组 → 通用表格（列名来自数据本身，humanize 后展示）', () => {
    render(<ValueWidget obs={obs(UNKNOWN_CAP, 'mystery_rows', [{ name: 'row-a', code: 7 }])} idx={idx} />)
    const table = screen.getByRole('group', { name: 'Mystery Rows 数据表' })
    expect(within(table).getByRole('columnheader', { name: 'Name' })).toBeInTheDocument()
    expect(within(table).getByRole('columnheader', { name: 'Code' })).toBeInTheDocument()
    expect(within(table).getByRole('cell', { name: 'row-a' })).toBeInTheDocument()
  })

  it('嵌套对象 → JSON 回落；扁平对象 + table widget → 键值定义列表', () => {
    const { unmount } = render(
      <ValueWidget obs={obs(UNKNOWN_CAP, 'mystery_blob', { nested: { a: [1, 2] } })} idx={idx} />,
    )
    expect(screen.getByRole('group', { name: 'Mystery Blob 原始 JSON' }).textContent).toContain('"nested"')
    unmount()

    render(<ValueWidget obs={obs(UNKNOWN_CAP, 'flat', { mode: 'auto', count: 3 })} idx={idx} />)
    expect(screen.getByText('Mode')).toBeInTheDocument()
    expect(screen.getByText('auto')).toBeInTheDocument()
    expect(screen.getByText('3')).toBeInTheDocument()
    expect(screen.queryByRole('group', { name: /原始 JSON/ })).not.toBeInTheDocument()
  })

  it('空集合与空观测有可读占位，不留白块', () => {
    render(<GenericTable value={[]} />)
    expect(screen.getByText('空集合')).toBeInTheDocument()
    render(<ObservationTable observations={[]} />)
    expect(screen.getByText('还没有观测值')).toBeInTheDocument()
  })

  it('Descriptor 缺席时 RawView 用上报字段通用渲染（不要求后端先就绪）', () => {
    render(<RawView raw={makeDeviceView().state} />)
    expect(screen.getByText('该设备未上报能力声明，此处按上报字段通用渲染')).toBeInTheDocument()
    expect(screen.getByText('Mode')).toBeInTheDocument()
    expect(screen.getByRole('group', { name: 'Slots 数据表' })).toBeInTheDocument()
    expect(screen.getByRole('group', { name: 'Diag 原始 JSON' })).toBeInTheDocument()
  })
})

describe('声明驱动的 widget 与量程', () => {
  it('属性级 widget=gauge + 声明 min/max → 画量程条（百分比来自声明，不猜语义量程）', () => {
    const { container } = render(<ValueWidget obs={obs(CAP_TEMPERATURE, 'current', 26.5, { unit: 'Cel' })} idx={idx} />)
    const bar = container.querySelector<HTMLElement>('span[style*="width"]')
    expect(bar).not.toBeNull()
    // (26.5 - (-40)) / (125 - (-40)) ≈ 40.303%
    expect(bar?.style.width).toMatch(/%$/)
    expect(parseFloat(bar?.style.width ?? '')).toBeCloseTo(((26.5 + 40) / 165) * 100, 3)
    expect(screen.getByText('26.5')).toBeInTheDocument()
    expect(screen.getByText('Cel')).toBeInTheDocument()
  })

  it('未声明 min/max → 不画量程条（避免编造量程）', () => {
    const { container } = render(<ValueWidget obs={obs(CAP_TEMPERATURE, 'sensor_state', 'stable')} idx={idx} />)
    expect(container.querySelector('span[style*="width"]')).toBeNull()
    expect(screen.getByText('stable')).toBeInTheDocument()
  })

  it('布尔属性按声明的 boolean widget 渲染语义胶囊', () => {
    render(<ValueWidget obs={observationsOf(relayEntity)[0] as Observation} idx={idx} />)
    expect(screen.getByText('否')).toBeInTheDocument()
  })

  it('数组标量 → 胶囊列表而不是表格', () => {
    render(<ValueWidget obs={obs(UNKNOWN_CAP, 'tags', ['a', 'b'])} idx={idx} />)
    expect(screen.getByText('a')).toBeInTheDocument()
    expect(screen.queryByRole('columnheader')).not.toBeInTheDocument()
  })
})

describe('quality / status 状态提示', () => {
  it('QualityDot 只对非 good 渲染，且以 role=img 暴露可读名称', () => {
    render(<QualityDot q="uncertain" />)
    expect(screen.getByRole('img', { name: '观测质量 不确定' })).toBeInTheDocument()
    const { container } = render(<QualityDot q="good" />)
    expect(container.firstChild).toBeNull()
    render(<QualityDot />)
    expect(screen.queryAllByRole('img')).toHaveLength(1)
  })

  it('观测表把质量点挂在属性名旁（异常可见但不抢主值）', () => {
    render(<ObservationTable observations={observationsOf(tempEntity)} idx={idx} />)
    expect(screen.getByRole('img', { name: '观测质量 不确定' })).toBeInTheDocument()
    expect(screen.queryByRole('img', { name: /良好/ })).not.toBeInTheDocument()
  })

  it('StatusBadge 覆盖四个契约状态', () => {
    for (const [status, label] of [['online', '在线'], ['degraded', '降级'], ['offline', '离线'], ['unavailable', '不可用']] as const) {
      const { unmount } = render(<StatusBadge status={status} />)
      expect(screen.getByText(label)).toBeInTheDocument()
      unmount()
    }
  })

  it('MetricTile 告警 tone 走 token 文字色类（颜色只走 token 类，不是内联色值）', () => {
    const { container } = render(<MetricTile v={{ label: '偏差', text: '0.4', tone: 'warn', title: 'drift' }} />)
    const value = container.querySelector('p.num')
    expect(value?.className).toContain('text-warn')
    expect(value?.getAttribute('style')).toBeNull()
  })
})

describe('StateMatrix 分组', () => {
  it('按平台 category 枚举分组：组头只有组名，主值右对齐可扫读', () => {
    render(<StateMatrix descriptor={descriptor} idx={idx} />)
    expect(screen.getByRole('heading', { name: /传感器/ })).toBeInTheDocument()
    expect(screen.getByRole('heading', { name: /执行器/ })).toBeInTheDocument()
    expect(screen.getByRole('heading', { name: /诊断/ })).toBeInTheDocument()
    expect(screen.getByText('温度探针')).toBeInTheDocument()
    expect(screen.getByText('26.5')).toBeInTheDocument()
  })

  it('没有 Entity 的 Descriptor 有明确空态', () => {
    render(<StateMatrix descriptor={makeDescriptor({ entities: [] })} idx={idx} />)
    expect(screen.getByText('设备声明里没有可呈现的观测项')).toBeInTheDocument()
  })
})

describe('390px 溢出收口', () => {
  it('表格与 JSON 都在自身容器内滚动，容器可键盘聚焦', () => {
    const { container } = render(
      <GenericTable value={[{ a: 1, b: 2, c: 3, d: 4, e: 5, f: 6, g: 7 }]} label="宽表" />,
    )
    const scroller = screen.getByRole('group', { name: '宽表' })
    expect(scroller).toHaveClass('overflow-x-auto')
    expect(scroller).toHaveAttribute('tabindex', '0')
    expect(scroller.firstElementChild?.tagName).toBe('TABLE')
    // 列数上限 6（含 # 列共 7 个表头），超出部分不进入 DOM，避免无限撑宽
    expect(container.querySelectorAll('th')).toHaveLength(7)
  })

  it('组件内没有任何内联像素宽度/固定宽度（只有百分比量程条）', () => {
    const { container } = render(
      <ValueWidget obs={obs(CAP_TEMPERATURE, 'current', 26.5, { unit: 'Cel' })} idx={idx} />,
    )
    const inline = [...container.querySelectorAll<HTMLElement>('[style]')]
    expect(inline.length).toBeGreaterThan(0)
    for (const el of inline) {
      expect(el.style.width).toMatch(/%$/)
      expect(el.style.minWidth).toBe('')
    }
  })

  it('JsonBlock 有高度上限并可滚动（长 JSON 不撑破卡片）', () => {
    render(<JsonBlock value={{ a: 1 }} label="样本 JSON" />)
    const pre = screen.getByRole('group', { name: '样本 JSON' })
    expect(pre).toHaveClass('overflow-auto')
    expect(pre).toHaveAttribute('tabindex', '0')
  })
})