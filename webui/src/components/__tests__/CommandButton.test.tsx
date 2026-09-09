import { act, fireEvent, render, screen, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { MockInstance } from 'vitest'
import type { ReactElement } from 'react'
import { ActionPanel } from '../ActionPanel'
import { CommandButton } from '../CommandButton'
import { api, ApiError } from '@/lib/api'
import type { CommandAction, CommandSet } from '@/lib/descriptor'
import type { CommandView, UserView } from '@/lib/types'
import { useAuth } from '@/store/auth'
import type { AuthState } from '@/store/auth'
import { useLive } from '@/store/ws'
import { toast, useToasts } from '@/store/toast'
import { resetStores } from '@/test/render'

const KEY = 'edge-1/dev-9'
const otherDevice = 'edge-2/dev-10'
const operator: UserView = { id: 1, username: 'operator', name: '操作员', role: 'operator', tenant_id: 1, tenant_slug: 'one' }
const simple: CommandAction = { cmd: 'read', label: '读取' }
const dangerous: CommandAction = {
  cmd: 'configure', label: '写入设置', variant: 'danger', confirmText: '设备将更新配置。', needsInput: true,
  inputSchema: { type: 'object', required: ['level'], properties: { level: { type: 'integer', title: '设定值' } } },
}
const commands: CommandSet = { source: 'descriptor', actions: [simple, dangerous] }
const receipt: CommandView = { id: 7, device_id: KEY, cmd: 'read', args: '', status: 'sent', created_at: 0, acked_at: 0, result: '' }
let send: MockInstance<typeof api.sendCommand>

function mount(ui: ReactElement) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } })
  return { queryClient, ...render(ui, { wrapper: ({ children }) => <QueryClientProvider client={queryClient}>{children}</QueryClientProvider> }) }
}
function auth(state: AuthState) { act(() => useAuth.setState(state)) }
function fillAndConfirm() {
  fireEvent.change(screen.getByRole('spinbutton', { name: '设定值' }), { target: { value: '5' } })
  fireEvent.click(screen.getByRole('button', { name: '写入设置' }))
  fireEvent.click(within(screen.getByRole('dialog')).getByRole('checkbox'))
  expect(within(screen.getByRole('dialog')).getByRole('button', { name: '写入设置' })).toBeEnabled()
}
async function clickRead() {
  await act(async () => { fireEvent.click(screen.getByRole('button', { name: '读取' })) })
}

beforeEach(() => {
  resetStores()
  useAuth.setState({ status: 'in', user: operator })
  send = vi.spyOn(api, 'sendCommand').mockResolvedValue(receipt)
})
afterEach(() => { vi.useRealTimers(); vi.restoreAllMocks() })

const denied: [string, AuthState][] = [
  ['loading', { status: 'loading', user: null }], ['out', { status: 'out', user: null }],
  ['in 缺身份', { status: 'in', user: null }],
  ['viewer', { status: 'in', user: { ...operator, role: 'viewer' } }],
  ['disabled', { status: 'in', user: { ...operator, disabled: true } }],
  ['非法角色', { status: 'in', user: { ...operator, role: 'root' as UserView['role'] } }],
  ['缺 id', { status: 'in', user: { ...operator, id: undefined } as unknown as UserView }],
  ['NaN id', { status: 'in', user: { ...operator, id: Number.NaN } }],
  ['负 id', { status: 'in', user: { ...operator, id: -1 } }],
  ['缺租户', { status: 'in', user: { ...operator, tenant_id: undefined } as unknown as UserView }],
]

describe('公共命令权限与 legacy 兼容边界', () => {
  it.each(denied)('%s 不渲染表单、命令按钮或确认框（包括独立按钮）', (_name, state) => {
    auth(state)
    mount(<><ActionPanel deviceId={KEY} set={commands} /><CommandButton deviceId={KEY} action={simple} /></>)
    expect(screen.getByText('当前账号没有操作权限。')).toBeInTheDocument()
    expect(screen.queryByRole('button')).not.toBeInTheDocument()
    expect(screen.queryByRole('textbox')).not.toBeInTheDocument()
    expect(screen.queryByRole('spinbutton')).not.toBeInTheDocument()
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(send).not.toHaveBeenCalled()
  })
  it.each(['operator', 'admin'] as const)('合法 %s 的 id=0 / tenant_id=0 仍可下发', async (role) => {
    auth({ status: 'in', user: { ...operator, id: 0, tenant_id: 0, role } })
    mount(<ActionPanel deviceId={KEY} set={commands} />)
    await clickRead()
    expect(send).toHaveBeenCalledWith('edge-1', 'dev-9', 'read', undefined)
  })
  it.each(['面板', '独立按钮'])('显式 open 保留 legacy %s 命令入口', async (kind) => {
    auth({ status: 'open', user: null })
    mount(kind === '面板' ? <ActionPanel deviceId={KEY} set={commands} /> : <CommandButton deviceId={KEY} action={simple} />)
    await clickRead()
    expect(send).toHaveBeenCalledOnce()
  })
  it('open 不伪造后端权限：服务端拒绝仍按现有失败链反馈', async () => {
    auth({ status: 'open', user: null })
    send.mockRejectedValue(new ApiError(403, 'forbidden'))
    mount(<CommandButton deviceId={KEY} action={simple} />)
    await clickRead()
    expect(useToasts.getState().items.at(-1)).toMatchObject({ title: '读取没有执行', tone: 'bad', detail: expect.stringContaining('当前账号没有执行操作的权限') })
  })
})

describe('表单/确认状态隔离', () => {
  it('角色降级卸载整张表单与已勾选确认，恢复角色也不复活旧值', () => {
    mount(<ActionPanel deviceId={KEY} set={commands} />)
    fillAndConfirm()
    auth({ status: 'in', user: { ...operator, role: 'viewer' } })
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(screen.queryByRole('spinbutton')).not.toBeInTheDocument()
    auth({ status: 'in', user: operator })
    expect(screen.getByRole('spinbutton', { name: '设定值' })).toHaveValue(null)
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: '写入设置' })).toBeDisabled()
    expect(send).not.toHaveBeenCalled()
  })
  it.each([
    ['账号', { id: 2 }], ['同 id 的服务身份', { username: 'other-token' }],
    ['租户', { tenant_id: 2, tenant_slug: 'two' }], ['角色', { role: 'admin' as const }],
  ])('切换%s 清空字段和确认；返回旧身份也不恢复', (_name, patch) => {
    mount(<ActionPanel deviceId={KEY} set={commands} />)
    fillAndConfirm()
    auth({ status: 'in', user: { ...operator, ...patch } })
    expect(screen.getByRole('spinbutton', { name: '设定值' })).toHaveValue(null)
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    auth({ status: 'in', user: operator })
    expect(screen.getByRole('spinbutton', { name: '设定值' })).toHaveValue(null)
    expect(send).not.toHaveBeenCalled()
  })
  it('同一身份的 me 刷新不破坏正在编辑的参数', () => {
    mount(<ActionPanel deviceId={KEY} set={commands} />)
    fireEvent.change(screen.getByRole('spinbutton', { name: '设定值' }), { target: { value: '5' } })
    auth({ status: 'in', user: { ...operator, name: '更新展示名' } })
    expect(screen.getByRole('spinbutton', { name: '设定值' })).toHaveValue(5)
  })
  it('open 与已验证身份间切换也清空参数和确认', () => {
    auth({ status: 'open', user: null })
    mount(<ActionPanel deviceId={KEY} set={commands} />)
    fillAndConfirm()
    auth({ status: 'in', user: operator })
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(screen.getByRole('spinbutton', { name: '设定值' })).toHaveValue(null)
    auth({ status: 'open', user: null })
    expect(screen.getByRole('spinbutton', { name: '设定值' })).toHaveValue(null)
  })
  it('设备切换和返回不会复活参数/确认，下发使用当前设备', async () => {
    const view = mount(<ActionPanel deviceId={KEY} set={commands} />)
    fillAndConfirm()
    view.rerender(<ActionPanel deviceId={otherDevice} set={commands} />)
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(screen.getByRole('spinbutton', { name: '设定值' })).toHaveValue(null)
    await clickRead()
    expect(send).toHaveBeenCalledWith('edge-2', 'dev-10', 'read', undefined)
    view.rerender(<ActionPanel deviceId={KEY} set={commands} />)
    expect(screen.getByRole('spinbutton', { name: '设定值' })).toHaveValue(null)
  })
  it('同一命令的 schema 变更清空旧参数与确认', () => {
    const view = mount(<ActionPanel deviceId={KEY} set={commands} />)
    fillAndConfirm()
    const changed = { ...dangerous, inputSchema: { type: 'object', required: ['other'], properties: { other: { type: 'string', title: '其他参数' } } } }
    view.rerender(<ActionPanel deviceId={KEY} set={{ source: 'descriptor', actions: [changed] }} />)
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(screen.getByRole('textbox', { name: '其他参数' })).toHaveValue('')
    expect(screen.getByRole('button', { name: '写入设置' })).toBeDisabled()
  })
  it('legacy 高级入口换命令/降级后不会复活原始参数', () => {
    const set: CommandSet = { source: 'adapter', actions: [simple, { cmd: 'other', label: '其他命令' }] }
    mount(<ActionPanel deviceId={KEY} set={set} />)
    const select = screen.getByRole('combobox', { name: '选择操作' })
    const args = screen.getByRole('textbox', { name: '操作参数' })
    fireEvent.change(select, { target: { value: 'read' } })
    fireEvent.change(args, { target: { value: 'legacy raw' } })
    fireEvent.change(select, { target: { value: 'other' } })
    fireEvent.change(select, { target: { value: 'read' } })
    expect(args).toHaveValue('')
    fireEvent.change(args, { target: { value: 'second draft' } })
    auth({ status: 'in', user: { ...operator, role: 'viewer' } })
    expect(screen.queryByRole('textbox')).not.toBeInTheDocument()
    auth({ status: 'in', user: operator })
    expect(screen.getByRole('combobox', { name: '选择操作' })).toHaveValue('')
    expect(screen.getByRole('textbox', { name: '操作参数' })).toHaveValue('')
    expect(screen.getByRole('textbox', { name: '操作参数' })).toBeDisabled()
  })
})

describe('独立按钮校验与确认防陈旧', () => {
  it('脱离 ActionPanel 也不能发送非法 JSON 或缺必填参数', () => {
    const view = mount(<CommandButton deviceId={KEY} action={dangerous} args="{" />)
    expect(screen.getByRole('button', { name: '写入设置' })).toBeDisabled()
    view.rerender(<CommandButton deviceId={KEY} action={dangerous} args="{}" />)
    expect(screen.getByRole('button', { name: '写入设置' })).toBeDisabled()
    expect(send).not.toHaveBeenCalled()
  })
  it('参数变化后关掉已勾选确认，改回原参数仍要重新勾选', () => {
    const args = '{"level":5}'
    const view = mount(<CommandButton deviceId={KEY} action={dangerous} args={args} />)
    fireEvent.click(screen.getByRole('button', { name: '写入设置' }))
    fireEvent.click(within(screen.getByRole('dialog')).getByRole('checkbox'))
    view.rerender(<CommandButton deviceId={KEY} action={dangerous} args={'{"level":6}'} />)
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    view.rerender(<CommandButton deviceId={KEY} action={dangerous} args={args} />)
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: '写入设置' }))
    expect(within(screen.getByRole('dialog')).getByRole('checkbox')).not.toBeChecked()
    expect(within(screen.getByRole('dialog')).getByRole('button', { name: '写入设置' })).toBeDisabled()
    expect(send).not.toHaveBeenCalled()
  })
  it('独立按钮角色降级或 disabled 后，旧确认不能继续下发', () => {
    const view = mount(<CommandButton deviceId={KEY} action={dangerous} args={'{"level":5}'} />)
    fireEvent.click(screen.getByRole('button', { name: '写入设置' }))
    view.rerender(<CommandButton deviceId={KEY} action={dangerous} args={'{"level":5}'} disabled />)
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    view.rerender(<CommandButton deviceId={KEY} action={dangerous} args={'{"level":5}'} />)
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: '写入设置' }))
    auth({ status: 'in', user: { ...operator, role: 'viewer' } })
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(screen.queryByRole('button')).not.toBeInTheDocument()
    expect(send).not.toHaveBeenCalled()
  })
  it('声明 danger 但没文案仍需勾选，只有普通 confirmation 不要求危险勾选', () => {
    const view = mount(<CommandButton deviceId={KEY} action={{ ...simple, variant: 'danger' }} />)
    fireEvent.click(screen.getByRole('button', { name: '读取' }))
    expect(within(screen.getByRole('dialog')).getByRole('button', { name: '读取' })).toBeDisabled()
    view.rerender(<CommandButton deviceId={KEY} action={{ ...simple, confirmText: '请核对目标设备。' }} />)
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: '读取' }))
    expect(screen.getByRole('dialog')).toHaveTextContent('请核对目标设备。')
    expect(within(screen.getByRole('dialog')).queryByRole('checkbox')).not.toBeInTheDocument()
    expect(within(screen.getByRole('dialog')).getByRole('button', { name: '读取' })).toBeEnabled()
  })
})

describe('POST / WS ACK / history / timeout 生命周期', () => {
  it.each([0, 7])('command_id=%s 下发与 ACK 各刷新历史/事件，重复 ACK 不重复结算', async (id) => {
    send.mockResolvedValue({ ...receipt, id })
    const ok = vi.spyOn(toast, 'ok')
    const view = mount(<CommandButton deviceId={KEY} action={simple} />)
    const invalidate = vi.spyOn(view.queryClient, 'invalidateQueries')
    await clickRead()
    expect(screen.getByRole('button', { name: '读取' })).toHaveAttribute('aria-busy', 'true')
    expect(invalidate).toHaveBeenCalledWith({ queryKey: ['device-commands', KEY] })
    expect(invalidate).toHaveBeenCalledWith({ queryKey: ['device-events', KEY] })
    expect(invalidate).toHaveBeenCalledTimes(2)
    act(() => useLive.setState({ acks: { [id]: { command_id: id, status: 'ok', detail: 'done' } } }))
    expect(screen.getByRole('button', { name: '读取' })).toHaveAttribute('aria-busy', 'false')
    expect(invalidate).toHaveBeenCalledTimes(4)
    act(() => useLive.setState({ acks: { [id]: { command_id: id, status: 'ok', detail: 'duplicate' } } }))
    expect(ok).toHaveBeenCalledExactlyOnceWith('读取已完成', 'done')
    expect(invalidate).toHaveBeenCalledTimes(4)
  })
  it('ACK 的机器细节不直接塞进轻提示', async () => {
    const ok = vi.spyOn(toast, 'ok')
    mount(<CommandButton deviceId={KEY} action={simple} />)
    await clickRead()
    act(() => useLive.setState({ acks: { 7: { command_id: 7, status: 'ok', detail: 'ping pings=1 commands=199 uptime_s=395' } } }))
    expect(ok).toHaveBeenCalledWith('读取已完成', '设备已返回确认，结果请在操作记录中查看。')
  })

  it('失败 ACK 同样结算并刷新历史', async () => {
    const view = mount(<CommandButton deviceId={KEY} action={simple} />)
    const invalidate = vi.spyOn(view.queryClient, 'invalidateQueries')
    await clickRead()
    act(() => useLive.setState({ acks: { 7: { command_id: 7, status: 'failed', detail: 'device rejected' } } }))
    expect(screen.getByRole('button', { name: '读取' })).toBeEnabled()
    expect(invalidate).toHaveBeenCalledTimes(4)
    expect(useToasts.getState().items.at(-1)).toMatchObject({ title: '读取失败', detail: '设备返回失败，请在操作记录中查看结果。', tone: 'bad' })
  })
  it('15s 未确认只提示仍在等待，不把设备未回执误报为失败', async () => {
    vi.useFakeTimers()
    const info = vi.spyOn(toast, 'info')
    const ok = vi.spyOn(toast, 'ok')
    const view = mount(<CommandButton deviceId={KEY} action={simple} />)
    const invalidate = vi.spyOn(view.queryClient, 'invalidateQueries')
    await clickRead()
    await act(async () => { await vi.advanceTimersByTimeAsync(14999) })
    expect(screen.getByRole('button', { name: '读取' })).toBeDisabled()
    await act(async () => { await vi.advanceTimersByTimeAsync(1) })
    expect(screen.getByRole('button', { name: '读取' })).toBeEnabled()
    expect(info).toHaveBeenCalledOnce()
    expect(info.mock.calls[0]).toEqual(['读取仍在等待确认', '已下发，设备暂未返回结果；请到操作记录查看，避免重复执行。'])
    expect(invalidate).toHaveBeenCalledTimes(4)
    act(() => useLive.setState({ acks: { 7: { command_id: 7, status: 'ok' } } }))
    expect(ok).not.toHaveBeenCalled()
  })
  it('ACK 先于 POST 响应到达也能正常结算', async () => {
    let resolve!: (cv: CommandView) => void
    send.mockReturnValue(new Promise((r) => { resolve = r }))
    mount(<CommandButton deviceId={KEY} action={simple} />)
    await clickRead()
    act(() => useLive.setState({ acks: { 7: { command_id: 7, status: 'ok' } } }))
    await act(async () => { resolve(receipt) })
    expect(screen.getByRole('button', { name: '读取' })).toBeEnabled()
    expect(useToasts.getState().items.at(-1)?.title).toBe('读取已完成')
  })
  it('发送中连点不会重复 POST', async () => {
    send.mockReturnValue(new Promise(() => {}))
    mount(<CommandButton deviceId={KEY} action={simple} />)
    await clickRead()
    await clickRead()
    expect(send).toHaveBeenCalledOnce()
  })
  it.each(['响应成功', '响应失败'])('换设备后旧 POST %s 不影响新界面或缓存', async (result) => {
    let resolve!: (cv: CommandView) => void
    let reject!: (error: Error) => void
    send.mockReturnValue(new Promise((yes, no) => { resolve = yes; reject = no }))
    const view = mount(<CommandButton deviceId={KEY} action={simple} />)
    const invalidate = vi.spyOn(view.queryClient, 'invalidateQueries')
    await clickRead()
    view.rerender(<CommandButton deviceId={otherDevice} action={simple} />)
    await act(async () => { if (result === '响应成功') resolve(receipt); else reject(new Error('old request')) })
    expect(screen.getByRole('button', { name: '读取' })).toBeEnabled()
    expect(invalidate).not.toHaveBeenCalled()
    expect(useToasts.getState().items).toHaveLength(0)
  })
  it('换身份/降级后不接收旧 ACK 或超时，也不会在恢复身份时复活 pending', async () => {
    vi.useFakeTimers()
    mount(<CommandButton deviceId={KEY} action={simple} />)
    await clickRead()
    auth({ status: 'in', user: { ...operator, role: 'viewer' } })
    act(() => useLive.setState({ acks: { 7: { command_id: 7, status: 'ok' } } }))
    await act(async () => { await vi.advanceTimersByTimeAsync(15000) })
    auth({ status: 'in', user: operator })
    expect(screen.getByRole('button', { name: '读取' })).toBeEnabled()
    expect(useToasts.getState().items).toHaveLength(0)
  })
})
