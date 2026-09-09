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
