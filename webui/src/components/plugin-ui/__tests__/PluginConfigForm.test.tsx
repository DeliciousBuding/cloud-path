import { fireEvent, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it } from 'vitest'
import { PluginConfigForm } from '@/components/plugin-ui/PluginConfigForm'
import { installFetch, stubResponse } from '@/test/http'
import { renderWithProviders, resetStores } from '@/test/render'
import { useAuth } from '@/store/auth'
import { appUser } from '@/test/application-plane'
import type { PluginInstanceView, PluginUISection } from '@/lib/types'

const appConfig = {
  timezone: 'Asia/Shanghai',
  reminder: { freq: 2, threshold: 1.5, enabled: true },
  keep: 'unchanged',
}
const instance: PluginInstanceView = {
  id: 'server/app-a', tenant_id: 1, edge_id: 'server',
  desired: {
    instance_id: 'app-a', plugin_id: 'example.app', version: 'v1', enabled: true, isolation: 'shared',
    revision: 1, updated_at: 1, config: { app_config: JSON.stringify(appConfig), other: 'keep-me' },
  },
  has_observed: true, observed: { state: 'running', health: 'HEALTHY', restart_count: 0 },
  edge_online: false, desired_revision: 1, applied_revision: 1, drift: false, stale: false,
}
const section: PluginUISection = {
  type: 'form', source: 'config', fields: [
    { key: 'app_config.timezone', label: '时区', type: 'string' },
    { key: 'app_config.reminder.freq', label: '频率', type: 'integer' },
    { key: 'app_config.reminder.threshold', label: '阈值', type: 'number' },
    { key: 'app_config.reminder.enabled', label: '启用', type: 'boolean' },
  ],
}

beforeEach(() => {
  resetStores()
  useAuth.setState({ status: 'in', user: { ...appUser, role: 'operator' } })
})

describe('PluginConfigForm typed app_config writeback', () => {
  it('reads typed values and writes number/integer/boolean back without stringifying them', async () => {
    const http = installFetch((url, init) => {
      if (url === '/api/plugin-instances/server%2Fapp-a' && init?.method === 'PATCH') {
        return stubResponse(200, { id: 'server/app-a', revision: 2, request_id: 'r1', instance })
      }
      return stubResponse(404, {})
    })
    const user = userEvent.setup()
    renderWithProviders(<PluginConfigForm instance={instance} section={section} readOnly={false} />)
    expect(screen.getByRole('textbox', { name: '时区' })).toHaveValue('Asia/Shanghai')
    expect(screen.getByRole('spinbutton', { name: '频率' })).toHaveValue(2)
    expect(screen.getByRole('spinbutton', { name: '阈值' })).toHaveValue(1.5)
    expect(screen.getByRole('checkbox', { name: '启用' })).toBeChecked()

    fireEvent.change(screen.getByRole('textbox', { name: '时区' }), { target: { value: 'UTC' } })
    fireEvent.change(screen.getByRole('spinbutton', { name: '频率' }), { target: { value: '3' } })
    fireEvent.change(screen.getByRole('spinbutton', { name: '阈值' }), { target: { value: '2.25' } })
    await user.click(screen.getByRole('checkbox', { name: '启用' }))
    await user.click(screen.getByRole('button', { name: /保存设置/ }))

    const patch = http.to('/api/plugin-instances/server%2Fapp-a').find((call) => call.method === 'PATCH')
    expect(patch).toBeDefined()
    const body = patch?.body as { config: Record<string, string> }
    const written = JSON.parse(body.config.app_config) as Record<string, unknown>
    expect(written).toMatchObject({ timezone: 'UTC', keep: 'unchanged' })
    expect(written.reminder).toEqual({ freq: 3, threshold: 2.25, enabled: false })
    expect(body.config.other).toBe('keep-me')
    expect(typeof (written.reminder as Record<string, unknown>).freq).toBe('number')
    expect(typeof (written.reminder as Record<string, unknown>).threshold).toBe('number')
    expect(typeof (written.reminder as Record<string, unknown>).enabled).toBe('boolean')
  })


  it('renders select labels, help text, and textarea controls from the manifest', () => {
    const richSection: PluginUISection = {
      type: 'form', source: 'config', fields: [
        { key: 'app_config.threshold_mode', label: '触发方式', type: 'select', enum: ['below', 'above'], values: { below: '低于下限', above: '高于上限' }, description: '选择触发条件。' },
        { key: 'app_config.alert', label: '开启告警', type: 'boolean', description: '关闭后只记录状态。' },
        { key: 'app_config.note', label: '说明', type: 'textarea', description: '可填写处理说明。' },
      ],
    }
    renderWithProviders(<PluginConfigForm instance={instance} section={richSection} readOnly={false} />)
    expect(screen.getByText('关闭后只记录状态。')).toBeInTheDocument()
    expect(screen.getByText('可填写处理说明。')).toBeInTheDocument()
    expect(screen.getByRole('textbox', { name: '说明' }).tagName).toBe('TEXTAREA')
    expect(screen.getByRole('combobox', { name: /触发方式/ })).toBeInTheDocument()
    expect(screen.getByRole('option', { name: '低于下限' })).toBeInTheDocument()
    expect(screen.getByText('选择触发条件。')).toBeInTheDocument()
  })


  it('edits nested array fields without requiring raw JSON', async () => {
    const http = installFetch((url, init) => url.includes('/api/plugin-instances/') && init?.method === 'PATCH'
      ? stubResponse(200, { id: 'server/app-a', revision: 2, request_id: 'r1', instance })
      : stubResponse(404, {}))
    const user = userEvent.setup()
    const arraySection: PluginUISection = {
      type: 'form', source: 'config', fields: [{
        key: 'app_config.compartments', label: '药格', type: 'array', required: true, minItems: 1,
        itemFields: [
          { key: 'id', label: '编号', type: 'string', required: true },
          { key: 'name', label: '名称', type: 'string' },
        ],
      }],
    }
    renderWithProviders(<PluginConfigForm instance={instance} section={arraySection} readOnly={false} />)
    await user.click(screen.getByRole('button', { name: '添加一项' }))
    fireEvent.change(screen.getByRole('textbox', { name: /编号/ }), { target: { value: 'c1' } })
    fireEvent.change(screen.getByRole('textbox', { name: '名称' }), { target: { value: '早药' } })
    await user.click(screen.getByRole('button', { name: /保存设置/ }))
    const patch = http.to('/api/plugin-instances/server%2Fapp-a').find((call) => call.method === 'PATCH')
    const written = JSON.parse((patch?.body as { config: Record<string, string> }).config.app_config) as Record<string, unknown>
    expect(written.compartments).toEqual([{ id: 'c1', name: '早药' }])
  })

  it('deletes an empty leaf without dropping sibling app_config keys', async () => {
    const http = installFetch((url, init) => url.includes('/api/plugin-instances/') && init?.method === 'PATCH'
      ? stubResponse(200, { id: 'server/app-a', revision: 2, request_id: 'r1', instance })
      : stubResponse(404, {}))
    const user = userEvent.setup()
    renderWithProviders(<PluginConfigForm instance={instance} section={section} readOnly={false} />)
    fireEvent.change(screen.getByRole('textbox', { name: '时区' }), { target: { value: '' } })
    await user.click(screen.getByRole('button', { name: /保存设置/ }))
    const patch = http.to('/api/plugin-instances/').find((call) => call.method === 'PATCH')
    const body = patch?.body as { config: Record<string, string> }
    const written = JSON.parse(body.config.app_config) as Record<string, unknown>
    expect(written).not.toHaveProperty('timezone')
    expect(written).toHaveProperty('keep', 'unchanged')
    expect(written).toHaveProperty('reminder')
  })
})
