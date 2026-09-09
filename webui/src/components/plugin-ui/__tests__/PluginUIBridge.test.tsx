import { screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { PluginUIBridge } from '@/components/plugin-ui/PluginUIBridge'
import { renderWithProviders } from '@/test/render'
import type { PluginInstanceView } from '@/lib/types'

const instance: PluginInstanceView = {
  id: 'server/app-a', tenant_id: 1, edge_id: 'server',
  desired: { instance_id: 'app-a', plugin_id: 'example.app', version: 'v1.0.0', enabled: true, isolation: 'shared', revision: 1, updated_at: 1 },
  has_observed: true, observed: { state: 'running', health: 'HEALTHY', restart_count: 0 },
  edge_online: false, desired_revision: 1, applied_revision: 1, drift: false, stale: false,
}

describe('PluginUIBridge', () => {
  it('uses a versioned same-origin asset URL and never grants same-origin sandbox access', () => {
    renderWithProviders(<PluginUIBridge pluginId="example.app" version="v2.0.0" instance={instance}
      section={{ type: 'custom', entry: 'ui/index.html', scopes: ['records.read'] }} />)
    const frame = screen.getByTitle('插件自定义页面')
    expect(frame).toHaveAttribute('src', '/api/plugin-ui/assets/example.app/v2.0.0/ui/index.html')
    expect(frame).toHaveAttribute('sandbox', 'allow-scripts')
    expect(frame.getAttribute('sandbox')).not.toContain('allow-same-origin')
    expect(frame).toHaveAttribute('referrerpolicy', 'no-referrer')
  })

  it('falls back to the desired instance version when catalog version is absent', () => {
    renderWithProviders(<PluginUIBridge pluginId="example.app" instance={instance}
      section={{ type: 'custom', entry: 'ui/index.html', scopes: [] }} />)
    expect(screen.getByTitle('插件自定义页面')).toHaveAttribute('src', '/api/plugin-ui/assets/example.app/v1.0.0/ui/index.html')
  })

  it('fails closed when the custom entry is missing or unsafe', () => {
    const { unmount } = renderWithProviders(<PluginUIBridge pluginId="example.app" version="v2.0.0" instance={instance}
      section={{ type: 'custom', scopes: ['records.read'] }} />)
    expect(screen.getByRole('alert')).toHaveTextContent('自定义页面不可用')
    unmount()
    renderWithProviders(<PluginUIBridge pluginId="example.app" version="v2.0.0" instance={instance}
      section={{ type: 'custom', entry: '../evil.js', scopes: ['records.read'] }} />)
    expect(screen.getByRole('alert')).toHaveTextContent('自定义页面不可用')
    expect(screen.queryByTitle('插件自定义页面')).not.toBeInTheDocument()
  })
})
