import { act, screen } from '@testing-library/react'
import { Route, Routes } from 'react-router'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import ApplicationPage from '@/pages/ApplicationPage'
import { i18n } from '@/i18n'
import { installFetch, stubResponse } from '@/test/http'
import { renderWithProviders, resetStores } from '@/test/render'
import { useAuth } from '@/store/auth'
import type { PluginCatalogView, PluginInstanceView } from '@/lib/types'

const catalog: PluginCatalogView = {
  id: 'example.app', kind: 'application', version: 'v2.0.0', source: '', digest: 'sha256:x', verified: true,
  protocol: 1, permissions: {},
  contributes: {
    applications: [{
      id: 'example.app', title: '示例应用',
      ui: {
        apiVersion: 1,
        navigation: { title: '示例页面', route: 'example' },
        pages: [{ id: 'home', title: '示例页面', sections: [{ type: 'form', fields: [{ key: 'name', label: '名称', required: true }] }] }],
      },
    }],
  },
}

function instance(enabled = true): PluginInstanceView {
  return {
    id: 'server/app-a', tenant_id: 1, edge_id: 'server',
    desired: { instance_id: 'app-a', plugin_id: 'example.app', version: 'v2.0.0', enabled, isolation: 'shared', revision: 1, updated_at: 1, config: {} },
    has_observed: true, observed: { state: enabled ? 'running' : 'stopped', health: 'HEALTHY', restart_count: 0 },
    edge_online: false, desired_revision: 1, applied_revision: 1, drift: false, stale: false,
  }
}

function renderPage(route = '/apps/example') {
  return renderWithProviders(
    <Routes><Route path="/apps/:appRoute" element={<ApplicationPage />} /></Routes>,
    route,
  )
}

function mockData(plugins: PluginCatalogView[], instances: PluginInstanceView[]) {
  installFetch((url) => {
    if (url === '/api/plugins') return stubResponse(200, { plugins })
    if (url === '/api/plugin-instances') return stubResponse(200, { instances })
    if (url.includes('/api/plugin-instances/app-a/records')) return stubResponse(200, { instance_id: 'app-a', records: [], limit: 20, offset: 0 })
    if (url.includes('/api/plugin-instances/app-a/bindings')) return stubResponse(200, { instance_id: 'app-a', running: true, bindings: [] })
    if (url.includes('/api/plugin-instances/app-a/jobs')) return stubResponse(200, { instance_id: 'app-a', running: true, jobs: [], scheduled: [], job_descriptors: [] })
    return stubResponse(404, { error: 'not found' })
  })
}

beforeEach(async () => { await i18n.changeLanguage('zh-CN'); resetStores() })
afterEach(async () => { await i18n.changeLanguage('zh-CN') })

describe('application deep links', () => {
  it('localizes plugin navigation, page title, subtitle and tabs in the active locale', async () => {
    useAuth.setState({ status: 'open', user: null })
    const localized: PluginCatalogView = {
      ...catalog,
      contributes: {
        applications: [{
          id: 'example.app', title: '示例应用', i18n: { 'zh-CN': '示例应用', 'en-US': 'Example app' },
          ui: {
            apiVersion: 1,
            navigation: { title: '示例页面', i18n: { 'zh-CN': '示例页面', 'en-US': 'Example page' }, route: 'example' },
            pages: [
              { id: 'home', title: '首页', i18n: { 'zh-CN': '首页', 'en-US': 'Home' }, sections: [{ type: 'form', fields: [{ key: 'name', label: 'Name' }] }] },
              { id: 'settings', title: '设置', i18n: { 'zh-CN': '设置', 'en-US': 'Settings' }, sections: [{ type: 'form', fields: [{ key: 'name', label: 'Name' }] }] },
            ],
          },
        }],
      },
    }
    mockData([localized], [instance()])
    renderPage()
    expect(await screen.findByRole('heading', { name: '示例页面' })).toBeInTheDocument()

    await act(async () => { await i18n.changeLanguage('en-US') })

    expect(screen.getByRole('heading', { name: 'Example page' })).toBeInTheDocument()
    expect(screen.getByText('Example app')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Home' })).toHaveAttribute('href', '/apps/example')
    expect(screen.getByRole('link', { name: 'Settings' })).toHaveAttribute('href', '/apps/example/settings')
    expect(document.querySelector('[aria-label="Home"]')).toBeInTheDocument()
    expect(document.title).toBe('Home · CloudPath')
  })

  it('open L0 can read the console but the form stays read-only', async () => {
    useAuth.setState({ status: 'open', user: null })
    mockData([catalog], [instance()])
    renderPage()
    expect(await screen.findByRole('heading', { name: '示例页面' })).toBeInTheDocument()
    expect(screen.getByRole('textbox', { name: '名称' })).toBeDisabled()
    expect(screen.getByText('当前账号只能查看设置，不能修改。')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /保存设置/ })).not.toBeInTheDocument()
  })

  it('unknown, disabled and no-instance links render readable states instead of errors', async () => {
    useAuth.setState({ status: 'open', user: null })
    mockData([], [])
    const first = renderPage()
    expect(await screen.findByText('应用不存在')).toBeInTheDocument()
    first.unmount()

    mockData([catalog], [instance(false)])
    const second = renderPage()
    expect(await screen.findByText('应用已停用')).toBeInTheDocument()
    second.unmount()

    mockData([catalog], [])
    renderPage()
    expect(await screen.findByText('尚未启用')).toBeInTheDocument()
  })

  it('shows a readable no-permission state for 403 catalog/instance reads', async () => {
    useAuth.setState({ status: 'in', user: { id: 1, username: 'u', name: 'U', role: 'viewer', tenant_id: 1, tenant_slug: 't' } })
    installFetch((url) => url === '/api/plugins' || url === '/api/plugin-instances'
      ? stubResponse(403, { error: 'forbidden' }) : stubResponse(404, {}))
    renderPage()
    expect(await screen.findByText('没有查看权限')).toBeInTheDocument()
  })

  it('does not flash a not-found state while catalog/instances are still loading', async () => {
    useAuth.setState({ status: 'open', user: null })
    installFetch((url) => {
      if (url === '/api/plugins') return new Promise(() => {}) as never
      if (url === '/api/plugin-instances') return stubResponse(200, { instances: [instance()] })
      return stubResponse(404, {})
    })
    renderPage()
    expect(screen.queryByText('应用不存在')).not.toBeInTheDocument()
  })
})
