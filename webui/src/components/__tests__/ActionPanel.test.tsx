// ActionPanel + CommandButton：命令集完全由声明驱动（前端无白名单/文案表）。
// 覆盖 actions.inputSchema → 参数输入、危险动作确认、args 卫生、冻结下发路径、
// 设备支持的操作回落的「带参数下发」入口，以及键盘可达性与无障碍名称。
import { fireEvent, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it } from 'vitest'
import { ActionPanel } from '@/components/ActionPanel'
import { commandActions, indexCapabilities, normalizeCapabilityDocs } from '@/lib/descriptor'
import { useLive } from '@/store/ws'
import { useAuth } from '@/store/auth'
import type { CommandSet } from '@/lib/descriptor'
import { useToasts } from '@/store/toast'
import { catalogPayload, makeDescriptor } from '@/test/fixtures'
import { installFetch, stubResponse } from '@/test/http'
import { renderWithProviders, resetStores } from '@/test/render'

const idx = indexCapabilities(normalizeCapabilityDocs(catalogPayload))
const KEY = 'edge-1/dev-9'
const descriptor = makeDescriptor()
const declared = commandActions({ descriptor, index: idx })
const fromAdapter = commandActions({ descriptor: null, adapterCommands: ['raw', 'query_state'] })

function okPost() {
  return installFetch(() => stubResponse(200, {
    id: 7, device_id: KEY, cmd: 'x', args: '', status: 'sent', created_at: 0, acked_at: 0, result: '',
  }))
}

beforeEach(() => {
  resetStores()
  useAuth.setState({ status: 'in', user: { id: 1, username: 'operator', name: '操作员', role: 'operator', tenant_id: 1, tenant_slug: 'default' } })
})

function schemaSet(inputSchema: Record<string, unknown>): CommandSet {
  return { source: 'descriptor', actions: [{ cmd: 'configure', label: '设置', inputSchema, needsInput: true }] }
}
const simpleSchema = {
  type: 'object', required: ['n', 'mode', 'enabled'], additionalProperties: false,
  properties: {
    n: { type: 'integer', title: '数量', description: '允许零值', minimum: 0, maximum: 9, default: 7 },
    mode: { type: 'string', description: '工作模式', enum: ['a', 'b'], default: 'b' },
    enabled: { type: 'boolean', default: true },
  },
}

describe('命令集来源与空态', () => {
  it('无声明 → 明确空态文案 + 「无声明」徽标，不摆一排猜出来的按钮', () => {
    renderWithProviders(<ActionPanel deviceId={KEY} set={{ actions: [], source: 'none' }} />)
    expect(screen.getByText('这台设备暂时没有可执行的操作')).toBeInTheDocument()
    expect(screen.queryByRole('button')).not.toBeInTheDocument()
  })

  it('不把来源和适配器术语展示给普通用户', () => {
    const { unmount } = renderWithProviders(<ActionPanel deviceId={KEY} set={declared} />)
    expect(screen.queryByText('Schema 声明')).not.toBeInTheDocument()
    expect(screen.queryByText('设备支持的操作')).not.toBeInTheDocument()
    unmount()
    renderWithProviders(<ActionPanel deviceId={KEY} set={fromAdapter} />)
    expect(screen.queryByText('设备支持的操作')).not.toBeInTheDocument()
    expect(screen.getByText('高级：手动输入参数')).toBeInTheDocument()
  })

  it('每个动作都是可读名称的按钮（名称来自声明 title）', () => {
    renderWithProviders(<ActionPanel deviceId={KEY} set={declared} />)
    expect(screen.getByRole('button', { name: '闭合' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '断开' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '恢复出厂' })).toBeInTheDocument()
  })
})

describe('actions.inputSchema → 参数输入', () => {
  it('默认展示空白的标量字段，不注入 0/false/模板或展示 JSON 编辑器', () => {
    renderWithProviders(<ActionPanel deviceId={KEY} set={declared} />)
    expect(screen.getByRole('spinbutton', { name: 'ms' })).toHaveValue(null)
    expect(screen.getByRole('textbox', { name: 'note' })).toHaveValue('')
    expect(screen.getByRole('textbox', { name: 'note' })).not.toHaveAttribute('maxlength')
    expect(screen.queryByRole('textbox', { name: '点动 技术参数' })).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: '点动' })).toBeDisabled()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(screen.getByText('填写参数后即可执行。')).toBeInTheDocument()
    expect(screen.queryByText(/UTF-8|NUL|JSON/)).not.toBeInTheDocument()
  })

  it('单个左括号必须报错并禁用下发；修正 JSON 才恢复', async () => {
    const http = okPost()
    const user = userEvent.setup()
    renderWithProviders(<ActionPanel deviceId={KEY} set={declared} />)
    await user.click(screen.getByRole('button', { name: '点动 技术人员选项' }))
    const input = screen.getByRole('textbox', { name: '点动 技术参数' })
    fireEvent.change(input, { target: { value: '{' } })
    expect(input).toHaveValue('{')
    expect(input).toHaveAttribute('aria-invalid', 'true')
    expect(screen.getByRole('alert')).toHaveTextContent('参数格式无效')
    await user.click(screen.getByRole('button', { name: '点动' }))
    expect(http.calls).toHaveLength(0)
    fireEvent.change(input, { target: { value: '{"ms":100}' } })
    expect(screen.getByRole('button', { name: '点动' })).toBeEnabled()
  })

  it('字段输入的 UTF-8 超限保留原文，反馈实际序列化后的字节数', () => {
    renderWithProviders(<ActionPanel deviceId={KEY} set={declared} />)
    const input = screen.getByRole('textbox', { name: 'note' })
    fireEvent.change(input, { target: { value: '汉'.repeat(22) } })
    expect(input).toHaveValue('汉'.repeat(22))
    expect(screen.getByRole('alert')).toHaveTextContent('内容太长，请减少输入内容')
    expect(screen.getByRole('button', { name: '点动' })).toBeDisabled()
  })

  it('合法 JSON 按原文下发到既有路径，不重排、压缩或截断', async () => {
    const user = userEvent.setup()
    const http = okPost()
    renderWithProviders(<ActionPanel deviceId={KEY} set={declared} />)
    await user.click(screen.getByRole('button', { name: '点动 技术人员选项' }))
    const args = '{"note":"' + 'x'.repeat(51) + '"}  '
    expect(new TextEncoder().encode(args)).toHaveLength(64)
    fireEvent.change(screen.getByRole('textbox', { name: '点动 技术参数' }), { target: { value: args } })
    await user.click(screen.getByRole('button', { name: '点动' }))
    expect(http.last()).toMatchObject({ url: '/api/devices/edge-1/dev-9/commands', method: 'POST', body: { cmd: 'pulse', args } })
  })

  it('title/description/key 标签与合适控件，required/enum/布尔均不自动选值', async () => {
    const http = okPost()
    const user = userEvent.setup()
    renderWithProviders(<ActionPanel deviceId={KEY} set={schemaSet(simpleSchema)} />)
    const number = screen.getByRole('combobox', { name: '数量' })
    const mode = screen.getByRole('combobox', { name: '工作模式' })
    const enabled = screen.getByRole('combobox', { name: 'enabled' })
    expect(number).toHaveValue('')
    expect(number).toBeRequired()
    expect(within(number).getAllByRole('option').map((option) => option.textContent)).toEqual(
      ['请选择', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9'],
    )
    expect(number).toHaveAccessibleDescription(expect.stringContaining('允许零值'))
    expect(mode).toHaveValue('')
    expect(enabled).toHaveValue('')
    expect(screen.getByRole('button', { name: '设置' })).toBeDisabled()
    await user.selectOptions(number, '0')
    expect(screen.getByRole('alert')).toHaveTextContent('缺少必填参数 工作模式')
    await user.selectOptions(mode, '0')
    expect(screen.getByRole('alert')).toHaveTextContent('缺少必填参数 enabled')
    await user.selectOptions(enabled, 'false')
    await user.click(screen.getByRole('button', { name: '设置' }))
    expect(http.last()?.body).toEqual({ cmd: 'configure', args: '{"n":0,"mode":"a","enabled":false}' })
  })

  it.each([
    ['{}', '缺少必填参数 数量'], ['[]', '需要对象'],
    ['{"n":"1","mode":"a","enabled":false}', '需要整数'],
    ['{"n":10,"mode":"a","enabled":false}', '不能大于 9'],
    ['{"n":1,"mode":"other","enabled":false}', '请选择允许的值'],
  ])('JSON 技术入口也拒绝 schema 违约：%s', async (args, error) => {
    const user = userEvent.setup()
    const http = okPost()
    renderWithProviders(<ActionPanel deviceId={KEY} set={schemaSet(simpleSchema)} />)
    await user.click(screen.getByRole('button', { name: '设置 技术人员选项' }))
    fireEvent.change(screen.getByRole('textbox', { name: '设置 技术参数' }), { target: { value: args } })
    expect(screen.getByRole('alert')).toHaveTextContent(error)
    expect(screen.getByRole('button', { name: '设置' })).toBeDisabled()
    await user.click(screen.getByRole('button', { name: '设置' }))
    expect(http.calls).toHaveLength(0)
  })

  it('JSON 换行不静默压缩，保留输入并遵守后端拒绝规则', () => {
    renderWithProviders(<ActionPanel deviceId={KEY} set={schemaSet({ type: 'object', properties: { note: { type: 'string' } } })} />)
    fireEvent.click(screen.getByRole('button', { name: '设置 技术人员选项' }))
    const input = screen.getByRole('textbox', { name: '设置 技术参数' })
    const raw = '{\n"n":1}'
    fireEvent.change(input, { target: { value: raw } })
    expect(input).toHaveValue(raw)
    expect(screen.getByRole('alert')).toHaveTextContent('换行')
    expect(screen.getByRole('button', { name: '设置' })).toBeDisabled()
  })

  it('空对象 inputSchema 不再渲染空参数表单，只保留可执行动作', () => {
    renderWithProviders(<ActionPanel deviceId={KEY} set={schemaSet({ type: 'object', properties: {} })} />)
    expect(screen.getByRole('button', { name: '设置' })).toBeInTheDocument()
    expect(screen.queryByRole('textbox')).not.toBeInTheDocument()
    expect(screen.queryByRole('radiogroup', { name: '设置方式' })).not.toBeInTheDocument()
  })

  it('字段与 JSON 双向同步，不复活上次字段值', async () => {
    const user = userEvent.setup()
    const http = okPost()
    renderWithProviders(<ActionPanel deviceId={KEY} set={declared} />)
    fireEvent.change(screen.getByRole('spinbutton', { name: 'ms' }), { target: { value: '100' } })
    await user.click(screen.getByRole('button', { name: '点动 技术人员选项' }))
    const input = screen.getByRole('textbox', { name: '点动 技术参数' })
    expect(input).toHaveValue('{"ms":100}')
    fireEvent.change(input, { target: { value: '{"ms":200,"note":""}' } })
    await user.click(screen.getByRole('button', { name: '点动 使用表单填写' }))
    expect(screen.getByRole('spinbutton', { name: 'ms' })).toHaveValue(200)
    await user.click(screen.getByRole('button', { name: '点动' }))
    expect(http.last()?.body).toEqual({ cmd: 'pulse', args: '{"ms":200,"note":""}' })
  })

  it('未声明但 schema 允许的额外字段保留在 JSON，不在切回字段时丢失', async () => {
    const user = userEvent.setup()
    const http = okPost()
    renderWithProviders(<ActionPanel deviceId={KEY} set={declared} />)
    await user.click(screen.getByRole('button', { name: '点动 技术人员选项' }))
    const args = '{"ms":100,"extra":2}'
    fireEvent.change(screen.getByRole('textbox', { name: '点动 技术参数' }), { target: { value: args } })
    expect(screen.getByRole('button', { name: '点动 使用表单填写' })).toBeDisabled()
    expect(screen.getByText(/当前参数无法自动转换为表单/)).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '点动' }))
    expect(http.last()?.body).toEqual({ cmd: 'pulse', args })
  })

  it('对象数组用逐行编辑器，不要求用户手写 JSON', () => {
    renderWithProviders(<ActionPanel deviceId={KEY} set={schemaSet({ type: 'object', required: ['rows'], properties: {
      rows: { type: 'array', minItems: 1, items: { type: 'object', required: ['n'], properties: { n: { type: 'number', title: '数量' } } } },
    } })} />)
    const input = screen.getByRole('textbox', { name: 'rows' })
    expect(input).toHaveAttribute('placeholder', '数量')
    fireEvent.change(input, { target: { value: 'bad' } })
    expect(screen.getByRole('alert')).toHaveTextContent('数量：请输入有效数值')
    fireEvent.change(input, { target: { value: '1' } })
    expect(screen.getByRole('button', { name: '设置' })).toBeEnabled()
  })

  it('组合中的未知约束显式标记为未校验，不把未知分支当作失败', async () => {
    const user = userEvent.setup()
    const http = okPost()
    renderWithProviders(<ActionPanel deviceId={KEY} set={schemaSet({ type: 'object', required: ['id'], oneOf: [{ $ref: '#/$defs/choice' }, { required: ['b'] }] })} />)
    expect(screen.getByText(/未校验：\$ref/)).toBeInTheDocument()
    const input = screen.getByRole('textbox', { name: '设置 技术参数' })
    fireEvent.change(input, { target: { value: '{}' } })
    expect(screen.getByRole('alert')).toHaveTextContent('缺少必填参数 id')
    fireEvent.change(input, { target: { value: '{"id":1}' } })
    await user.click(screen.getByRole('button', { name: '设置' }))
    expect(http.last()?.body).toEqual({ cmd: 'configure', args: '{"id":1}' })
  })
})

describe('危险动作与确认结果', () => {
  it('声明了 confirmation 的动作先弹设计过的二次确认：取消不下发，确认才下发', async () => {
    const user = userEvent.setup()
    const http = okPost()
    renderWithProviders(<ActionPanel deviceId={KEY} set={declared} />)

    await user.click(screen.getByRole('button', { name: '恢复出厂' }))
    const dialog = screen.getByRole('dialog')
    // 确认文案的事实源仍是 Capability 声明，必须逐字出现（不是前端自己编的话术）
    expect(dialog).toHaveTextContent('确认恢复出厂？设备侧配置将被清空。')
    expect(dialog).toHaveTextContent(KEY)
    expect(dialog).not.toHaveTextContent('factory_reset')
    expect(http.calls).toHaveLength(0)

    // 危险动作（variant=danger）必须显式勾选才允许执行
    const go = within(dialog).getByRole('button', { name: '恢复出厂' })
    expect(go).toBeDisabled()
    await user.click(within(dialog).getByRole('checkbox'))
    expect(go).toBeEnabled()
    await user.click(go)
    expect((http.last()?.body as { cmd: string }).cmd).toBe('factory_reset')
  })

  it('二次确认可以取消或 Esc 关闭，两种路径都不下发命令', async () => {
    const user = userEvent.setup()
    const http = okPost()
    renderWithProviders(<ActionPanel deviceId={KEY} set={declared} />)

    await user.click(screen.getByRole('button', { name: '恢复出厂' }))
    await user.click(within(screen.getByRole('dialog')).getByRole('button', { name: '取消' }))
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(http.calls).toHaveLength(0)

    await user.click(screen.getByRole('button', { name: '恢复出厂' }))
    fireEvent.keyDown(window, { key: 'Escape' })
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(http.calls).toHaveLength(0)
  })

  it('非危险动作不要求勾选：确认键直接可点', async () => {
    const user = userEvent.setup()
    const http = okPost()
    // pulse 有 inputSchema 但没有 destructive/confirmation → 填写合法参数后直接下发
    renderWithProviders(<ActionPanel deviceId={KEY} set={declared} />)
    fireEvent.change(screen.getByRole('spinbutton', { name: 'ms' }), { target: { value: '100' } })
    await user.click(screen.getByRole('button', { name: '点动' }))
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect((http.last()?.body as { cmd: string }).cmd).toBe('pulse')
  })

  it('下发后按钮进入 aria-busy，WS ack 到达后结算并给出可读提示', async () => {
    const user = userEvent.setup()
    okPost()
    renderWithProviders(<ActionPanel deviceId={KEY} set={declared} />)
    const btn = screen.getByRole('button', { name: '闭合' })
    await user.click(btn)
    expect(btn).toHaveAttribute('aria-busy', 'true')
    expect(btn).toBeDisabled()

    useLive.setState({ acks: { 7: { command_id: 7, status: 'ok', detail: '已接通' } } })
    expect(await screen.findByRole('button', { name: '闭合' })).toHaveAttribute('aria-busy', 'false')
    const items = useToasts.getState().items
    expect(items[items.length - 1]).toMatchObject({ title: '闭合已完成', detail: '已接通', tone: 'ok' })
  })

  it('下发失败（server 500）→ 失败提示，按钮恢复可用', async () => {
    const user = userEvent.setup()
    installFetch(() => stubResponse(500, { error: '内部错误' }))
    renderWithProviders(<ActionPanel deviceId={KEY} set={declared} />)
    await user.click(screen.getByRole('button', { name: '断开' }))
    const items = useToasts.getState().items
    expect(items[items.length - 1]).toMatchObject({ title: '断开没有执行', tone: 'bad' })
    expect(screen.getByRole('button', { name: '断开' })).toBeEnabled()
  })
})

describe('适配器无 schema 命令：高级手动参数入口', () => {
  it('下拉选择命令后才允许填参数，并提交到既有命令路径', async () => {
    const user = userEvent.setup()
    const http = okPost()
    renderWithProviders(<ActionPanel deviceId={KEY} set={fromAdapter} />)
    const select = screen.getByRole('combobox', { name: '选择操作' })
    const args = screen.getByRole('textbox', { name: '操作参数' })
    expect(args).toBeDisabled()

    await user.selectOptions(select, 'query_state')
    expect(args).toBeEnabled()
    // JSON 参数里的 { 会被 user-event 当按键描述符，这里用 change 事件写入完整值
    fireEvent.change(args, { target: { value: '{"k":1}' } })
    expect(args).toHaveValue('{"k":1}')
    const send = screen.getByRole('button', { name: 'Query State' })
    await user.click(send)
    expect(http.last()?.body).toEqual({ cmd: 'query_state', args: '{"k":1}' })
  })

  it('有设备声明时不出现万能参数入口（避免绕过设备操作）', () => {
    renderWithProviders(<ActionPanel deviceId={KEY} set={declared} />)
    expect(screen.getByRole('combobox', { name: '选择参数操作' })).toBeInTheDocument()
    expect(screen.queryByRole('combobox', { name: '选择操作' })).not.toBeInTheDocument()
    expect(screen.queryByRole('textbox', { name: '操作参数' })).not.toBeInTheDocument()
    expect(screen.queryByText(/带参数下发/)).not.toBeInTheDocument()
  })
})

describe('键盘与焦点', () => {
  it('Tab 依次到达参数输入框与命令按钮，Enter 即可下发', async () => {
    const user = userEvent.setup()
    const http = okPost()
    renderWithProviders(<ActionPanel deviceId={KEY} set={declared} />)
    const input = screen.getByRole('spinbutton', { name: 'ms' })
    fireEvent.change(input, { target: { value: '100' } })
    input.focus()
    await user.tab()
    expect(screen.getByRole('textbox', { name: 'note' })).toHaveFocus()
    await user.tab()
    expect(screen.getByRole('button', { name: '点动 技术人员选项' })).toHaveFocus()
    await user.tab()
    expect(screen.getByRole('button', { name: '点动' })).toHaveFocus()
    await user.keyboard('{Enter}')
    expect((http.last()?.body as { args: string }).args).toBe('{"ms":100}')
  })

  it('面板标题是 h2，命令区在无障碍树里有可读结构', () => {
    renderWithProviders(<ActionPanel deviceId={KEY} set={declared} />)
    const heading = screen.getByRole('heading', { level: 2, name: /设备操作/ })
    expect(within(heading).getByText('设备操作')).toBeInTheDocument()
  })
})

describe('命令按钮说明（title/description）', () => {
  it('有 description 的动作渲染可见说明；无说明的不加冗余题注', () => {
    renderWithProviders(<ActionPanel deviceId={KEY} set={declared} />)
    // close 的 description 来自后端声明 → 落到 hint，渲染为按钮下可见说明
    expect(screen.getByText('接通负载')).toBeInTheDocument()
    // open / factory_reset 未声明描述 → 按钮文案自足，不编造题注占位
    expect(screen.queryByText(/下发命令「/)).not.toBeInTheDocument()
  })

  it('按钮不塞内部溯源信息，说明留在按钮下方', () => {
    renderWithProviders(<ActionPanel deviceId={KEY} set={declared} />)
    const close = screen.getByRole('button', { name: '闭合' })
    expect(close).not.toHaveAttribute('title')
    expect(screen.getByText('接通负载')).toBeInTheDocument()
  })

  it('参数类操作显示功能选择、字段和可读说明，不摊内部溯源', () => {
    renderWithProviders(<ActionPanel deviceId={KEY} set={declared} />)
    expect(screen.getByRole('combobox', { name: '选择参数操作' })).toHaveValue('pulse')
    expect(screen.getByRole('spinbutton', { name: 'ms' })).toBeInTheDocument()
    expect(screen.getByText('按毫秒脉冲')).toBeInTheDocument()
    expect(screen.queryByText('点动 · 按毫秒脉冲')).not.toBeInTheDocument()
  })
})

it('危险操作不抢占首个快捷操作位置，保留完整确认与名称', () => {
  renderWithProviders(<ActionPanel deviceId={KEY} set={{ source: 'descriptor', actions: [
    { cmd: 'risky', label: '恢复设置', variant: 'danger', confirmText: '不可撤销' },
    { cmd: 'inspect', label: '检查连接' },
  ] }} />)
  expect(screen.getAllByRole('button').map((button) => button.getAttribute('aria-label'))).toEqual(['检查连接', '恢复设置'])
})


it('LED 组合条件显示为设置方式，不会要求用户手写 JSON', async () => {
  const user = userEvent.setup()
  const http = okPost()
  renderWithProviders(<ActionPanel deviceId={KEY} set={schemaSet({
    type: 'object', properties: {
      mask: { type: 'integer', minimum: 0, maximum: 255 },
      pattern: { type: 'integer', minimum: 0, maximum: 9 },
    }, oneOf: [{ required: ['mask'] }, { required: ['pattern'] }],
  })} />)
  expect(screen.getByRole('radiogroup', { name: '设置方式' })).toBeInTheDocument()
  expect(screen.getByRole('radio', { name: 'mask' })).toBeChecked()
  expect(screen.getByRole('spinbutton', { name: 'mask' })).toBeInTheDocument()
  expect(screen.queryByRole('spinbutton', { name: 'pattern' })).not.toBeInTheDocument()
  expect(screen.queryByRole('textbox', { name: '设置 技术参数' })).not.toBeInTheDocument()
  fireEvent.change(screen.getByRole('spinbutton', { name: 'mask' }), { target: { value: '0' } })
  expect(screen.getByRole('button', { name: '设置' })).toBeEnabled()
  await user.click(screen.getByRole('button', { name: '设置' }))
  expect(http.last()).toMatchObject({ url: '/api/devices/edge-1/dev-9/commands', method: 'POST', body: { cmd: 'configure', args: '{"mask":0}' } })
})
