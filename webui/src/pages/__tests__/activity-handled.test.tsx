import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it } from 'vitest'
import Activity from '@/pages/Activity'
import { installFetch, stubResponse } from '@/test/http'
import { renderWithProviders, resetStores } from '@/test/render'
import { useToasts } from '@/store/toast'

const failed = {
  id: 77,
  device_id: 'edge-a/dev-1',
  cmd: 'relay_on',
  args: 'A1',
  status: 'failed',
  created_at: 1_770_000_000,
  acked_at: 1_770_000_010,
  result: 'device busy',
}

const handled = {
  ...failed,
  id: 78,
  status: 'timeout',
  handled_at: 1_770_000_020,
}

function route(commands: unknown[]) {
  return installFetch((url, init) => {
    if (url.startsWith('/api/commands/handled') && init?.method === 'POST') {
      return stubResponse(200, { handled: 1 })
    }
    if (url.startsWith('/api/commands')) return stubResponse(200, { commands })
    if (url === '/api/devices') {
      return stubResponse(200, { devices: [{ id: 'edge-a/dev-1', edge_id: 'edge-a', adapter: 'demo', name: '药盒', port: 'COM3', online: true, state: {}, updated_at: 1, last_seen: 1 }] })
    }
    if (url === '/api/edges') return stubResponse(200, { edges: [] })
    if (url.startsWith('/api/events')) return stubResponse(200, { events: [] })
    return stubResponse(404, { error: 'not found' })
  })
}

beforeEach(() => resetStores())

describe('运行记录：失败操作可标记已处理', () => {
  it('从概览深链进入未处理失败筛选，并能单条标记', async () => {
    const stub = route([failed])
    renderWithProviders(<Activity />, '/activity?tab=commands&status=failed&handled=unhandled')

    const mark = await screen.findByRole('button', { name: '标记已处理' })
    expect(screen.getByLabelText('按处理状态筛选')).toHaveValue('unhandled')
    await userEvent.click(mark)

    expect(stub.to('/api/commands/handled').at(-1)?.body).toEqual({ ids: [77] })
    expect(useToasts.getState().items.at(-1)).toMatchObject({ title: '操作已标记为已处理' })
  })

  it('可以一次清空当前 24 小时窗口内的未处理失败操作', async () => {
    const stub = route([failed])
    renderWithProviders(<Activity />, '/activity?tab=commands')

    await userEvent.click(await screen.findByRole('button', { name: '全部标记为已处理' }))
    expect(stub.to('/api/commands/handled').at(-1)?.body).toEqual({ all_unhandled: true })
  })

  it('已处理记录仍保留原始行，只显示已处理状态', async () => {
    route([handled])
    renderWithProviders(<Activity />, '/activity?tab=commands&status=timeout&handled=handled')

    await screen.findByText('当前 1 条')
    expect(screen.getAllByText('已处理')).toHaveLength(2)
    expect(screen.queryByRole('button', { name: '标记已处理' })).not.toBeInTheDocument()
  })
})
