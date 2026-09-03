// ActionPanel + CommandButton：命令集完全由声明驱动（前端无白名单/文案表）。
// 覆盖 actions.inputSchema → 参数输入、危险动作确认、args 卫生、冻结下发路径、
// 适配器白名单回落的「带参数下发」入口，以及键盘可达性与无障碍名称。
import { fireEvent, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it } from 'vitest'
import { ActionPanel } from '@/components/ActionPanel'
import { commandActions, indexCapabilities, normalizeCapabilityDocs } from '@/lib/descriptor'
import { useLive } from '@/store/ws'
import { useToasts } from '@/store/toast'
import { catalogPayload, makeDescriptor } from '@/test/fixtures'
import { installFetch, stubResponse } from '@/test/http'
import { resetStores } from '@/test/render'

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

beforeEach(() => { resetStores() })

describe('命令集来源与空态', () => {
  it('无声明 → 明确空态文案 + 「无声明」徽标，不摆一排猜出来的按钮', () => {
    render(<ActionPanel deviceId={KEY} set={{ actions: [], source: 'none' }} />)
    expect(screen.getByText('该设备未声明可下发命令（等待 Descriptor / Capability catalog）')).toBeInTheDocument()
    expect(screen.getByText('无声明')).toBeInTheDocument()
    expect(screen.queryByRole('button')).not.toBeInTheDocument()
  })

  it('Schema 声明来源标注「Schema 声明」，适配器回落标注「适配器白名单」+ 适配器名', () => {
    const { unmount } = render(<ActionPanel deviceId={KEY} set={declared} adapterName="demo" />)
    expect(screen.getByText('Schema 声明')).toBeInTheDocument()
    expect(screen.getByText('demo')).toBeInTheDocument()
    unmount()
    render(<ActionPanel deviceId={KEY} set={fromAdapter} adapterName="demo" />)
    expect(screen.getByText('适配器白名单')).toBeInTheDocument()
  })

  it('每个动作都是可读名称的按钮（名称来自声明 title，破坏性动作占满一行）', () => {
    render(<ActionPanel deviceId={KEY} set={declared} />)
    expect(screen.getByRole('button', { name: '闭合' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '断开' })).toBeInTheDocument()
    const danger = screen.getByRole('button', { name: '恢复出厂' })
    expect(danger.className).toContain('col-span-2')
    expect(danger.className).toContain('text-bad')
    expect(danger).toHaveAttribute('title', expect.stringContaining('cmd=factory_reset'))
  })
})

describe('actions.inputSchema → 参数输入', () => {
  it('声明了 inputSchema 的动作给出带标签的输入框，占位符即参数模板，长度受限', () => {
    render(<ActionPanel deviceId={KEY} set={declared} />)
    const input = screen.getByPlaceholderText('{"ms":0,"note":""}')
    expect(input).toHaveAttribute('maxlength', '64')
    expect(input).toHaveAccessibleName(expect.stringContaining('点动'))
    // label 与 input 通过 for/id 关联（不是靠视觉相邻）
    const label = document.querySelector(`label[for="${input.id}"]`)
    expect(label?.textContent).toContain('≤64 字符')
  })

  it('输入自动剔除换行/NUL（后端参数校验的前端第一道收敛）', async () => {
    const user = userEvent.setup()
    render(<ActionPanel deviceId={KEY} set={declared} />)
    const input = screen.getByPlaceholderText('{"ms":0,"note":""}')
    await user.type(input, 'a{enter}b')
    expect(input).toHaveValue('ab')
  })

  it('下发时 args 按声明长度截断，并 POST 到冻结路径', async () => {
    const user = userEvent.setup()
    const http = okPost()
    render(<ActionPanel deviceId={KEY} set={declared} />)
    const input = screen.getByPlaceholderText('{"ms":0,"note":""}')
    fireEvent.change(input, { target: { value: 'x'.repeat(90) } })
    await user.click(screen.getByRole('button', { name: '点动' }))
    expect(http.last()).toMatchObject({
      url: `/api/devices/edge-1/dev-9/commands`, method: 'POST',
    })
    expect((http.last()?.body as { cmd: string; args: string }).cmd).toBe('pulse')
    expect(((http.last()?.body as { args: string }).args)).toHaveLength(64)
  })
})

describe('危险动作与回执', () => {
  it('声明了 confirmation 的动作先弹设计过的二次确认：取消不下发，确认才下发', async () => {
    const user = userEvent.setup()
    const http = okPost()
    render(<ActionPanel deviceId={KEY} set={declared} />)

    await user.click(screen.getByRole('button', { name: '恢复出厂' }))
    const dialog = screen.getByRole('dialog')
    // 确认文案的事实源仍是 Capability 声明，必须逐字出现（不是前端自己编的话术）
    expect(dialog).toHaveTextContent('确认恢复出厂？设备侧配置将被清空。')
    expect(dialog).toHaveTextContent(KEY)
    expect(dialog).toHaveTextContent('factory_reset')
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
    render(<ActionPanel deviceId={KEY} set={declared} />)

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
    // pulse 有 inputSchema 但没有 destructive/confirmation → 不进对话框
    render(<ActionPanel deviceId={KEY} set={declared} />)
    await user.click(screen.getByRole('button', { name: '点动' }))
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect((http.last()?.body as { cmd: string }).cmd).toBe('pulse')
  })

  it('下发后按钮进入 aria-busy，WS ack 到达后结算并给出可读提示', async () => {
    const user = userEvent.setup()
    okPost()
    render(<ActionPanel deviceId={KEY} set={declared} />)
    const btn = screen.getByRole('button', { name: '闭合' })
    await user.click(btn)
    expect(btn).toHaveAttribute('aria-busy', 'true')
    expect(btn).toBeDisabled()

    useLive.setState({ acks: { 7: { command_id: 7, status: 'ok', detail: '已接通' } } })
    expect(await screen.findByRole('button', { name: '闭合' })).toHaveAttribute('aria-busy', 'false')
    const items = useToasts.getState().items
    expect(items[items.length - 1]).toMatchObject({ title: '闭合已执行', detail: '已接通', tone: 'ok' })
  })

  it('下发失败（server 500）→ 失败提示，按钮恢复可用', async () => {
    const user = userEvent.setup()
    installFetch(() => stubResponse(500, { error: '内部错误' }))
    render(<ActionPanel deviceId={KEY} set={declared} />)
    await user.click(screen.getByRole('button', { name: '断开' }))
    const items = useToasts.getState().items
    expect(items[items.length - 1]).toMatchObject({ title: '断开未下发', tone: 'bad' })
    expect(screen.getByRole('button', { name: '断开' })).toBeEnabled()
  })
})

describe('适配器白名单回落：带参数下发入口', () => {
  it('下拉选择命令后才允许填参数，选择框与输入框都有可读名称', async () => {
    const user = userEvent.setup()
    const http = okPost()
    render(<ActionPanel deviceId={KEY} set={fromAdapter} />)
    const select = screen.getByRole('combobox', { name: '选择命令' })
    const args = screen.getByRole('textbox', { name: '命令参数' })
    expect(args).toBeDisabled()

    await user.selectOptions(select, 'query_state')
    expect(args).toBeEnabled()
    // JSON 参数里的 { 会被 user-event 当按键描述符，这里用 change 事件写入完整值
    fireEvent.change(args, { target: { value: '{"k":1}' } })
    expect(args).toHaveValue('{"k":1}')
    // 白名单命令同时出现在「一键下发」网格与「带参数下发」行，取后者（带 args）
    const send = screen.getAllByRole('button', { name: 'Query State' })
    expect(send).toHaveLength(2)
    await user.click(send[send.length - 1] as HTMLElement)
    expect(http.last()?.body).toEqual({ cmd: 'query_state', args: '{"k":1}' })
  })

  it('Schema 声明来源时不出现「带参数下发」万能入口（避免绕过声明）', () => {
    render(<ActionPanel deviceId={KEY} set={declared} />)
    expect(screen.queryByRole('combobox')).not.toBeInTheDocument()
    expect(screen.queryByText(/带参数下发/)).not.toBeInTheDocument()
  })
})

describe('键盘与焦点', () => {
  it('Tab 依次到达参数输入框与命令按钮，Enter 即可下发', async () => {
    const user = userEvent.setup()
    const http = okPost()
    render(<ActionPanel deviceId={KEY} set={declared} />)
    const input = screen.getByPlaceholderText('{"ms":0,"note":""}')
    fireEvent.change(input, { target: { value: '{"ms":100}' } })
    input.focus()
    await user.tab()
    expect(screen.getByRole('button', { name: '点动' })).toHaveFocus()
    await user.keyboard('{Enter}')
    expect((http.last()?.body as { args: string }).args).toBe('{"ms":100}')
  })

  it('面板标题是 h2，命令区在无障碍树里有可读结构', () => {
    render(<ActionPanel deviceId={KEY} set={declared} />)
    const heading = screen.getByRole('heading', { level: 2, name: /命令/ })
    expect(within(heading).getByText('命令')).toBeInTheDocument()
  })
})