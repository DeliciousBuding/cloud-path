import { describe, expect, it } from 'vitest'
import {
  applicationUIReadable, buildApplicationNavigation, normalizePluginUI, pluginUIAssetURL,
  resolveApplicationRoute,
} from '@/lib/plugin-ui'
import { normalizeCatalog } from '@/lib/plugins'
import type { PluginCatalogView, PluginInstanceView } from '@/lib/types'

function instance(pluginId = 'example.app', over: Partial<PluginInstanceView> = {}): PluginInstanceView {
  return {
    id: `server/${pluginId}`,
    tenant_id: 1,
    edge_id: 'server',
    desired: {
      instance_id: 'app-a', plugin_id: pluginId, version: 'v1.2.3', enabled: true,
      isolation: 'shared', revision: 1, updated_at: 1,
    },
    has_observed: true,
    observed: { state: 'running', health: 'HEALTHY', restart_count: 0 },
    edge_online: false, desired_revision: 1, applied_revision: 1, drift: false, stale: false,
    ...over,
  }
}

function plugin(over: Partial<PluginCatalogView> = {}): PluginCatalogView {
  return {
    id: 'example.app', kind: 'application', version: 'v1.2.3', source: '', digest: 'sha256:x',
    verified: true, protocol: 1, permissions: {},
    contributes: {
      applications: [{
        id: 'example.app', title: '示例应用',
        ui: {
          apiVersion: 1,
          navigation: { title: '示例页面', i18n: { 'en-US': 'Example page' }, route: 'example', icon: 'box', order: 10 },
          pages: [{ id: 'home', title: '示例页面', i18n: { 'en-US': 'Example page' }, sections: [{ type: 'status' }, { type: 'records', recordType: 'sample', presentation: 'timeline' }] }],
        },
      }],
    },
    ...over,
  }
}

describe('plugin UI normalization and asset URLs', () => {
  it('uses the canonical versioned backend asset route', () => {
    expect(pluginUIAssetURL('example.app', 'v1.2.3', 'ui/index.html'))
      .toBe('/api/plugin-ui/assets/example.app/v1.2.3/ui/index.html')
    expect(pluginUIAssetURL('example.app', '', 'ui/index.html')).toBeUndefined()
    expect(pluginUIAssetURL('example.app', 'v1.2.3', '../secret.js')).toBeUndefined()
    expect(pluginUIAssetURL('example.app', 'v1.2.3', 'https://evil.example/ui.html')).toBeUndefined()
  })

  it('keeps only allowlisted sections, scopes and package-relative custom entries', () => {
    const ui = normalizePluginUI({
      apiVersion: 1,
      navigation: { title: '示例', route: 'example' },
      pages: [{
        id: 'home', title: '首页', sections: [
          { type: 'custom', entry: '../evil.js', scopes: ['jobs.run', 'root.everything'] },
          { type: 'not-real' },
          { type: 'form', fields: [{ key: 'name', type: 'text', required: true }, { key: '' }] },
        ],
      }],
    })
    expect(ui?.navigation?.route).toBe('example')
    expect(ui?.pages?.[0]?.sections).toHaveLength(2)
    expect(ui?.pages?.[0]?.sections[0]).toMatchObject({ type: 'custom', entry: undefined, scopes: ['jobs.run'] })
    expect(ui?.pages?.[0]?.sections[1].fields).toEqual([{ key: 'name', required: true, type: undefined, label: undefined, description: undefined, placeholder: undefined, minimum: undefined, maximum: undefined, pattern: undefined, secret: undefined }])
  })

  it('preserves navigation/page i18n maps and drops invalid values', () => {
    const ui = normalizePluginUI({
      apiVersion: 1,
      navigation: { title: '示例', i18n: { 'en-US': 'Example', 'zh-CN': 1, '': 'ignored' }, route: 'example' },
      pages: [{ id: 'home', title: '首页', i18n: { 'en-US': 'Home' }, sections: [{ type: 'status' }] }],
    })
    expect(ui?.navigation?.i18n).toEqual({ 'en-US': 'Example' })
    expect(ui?.pages?.[0]?.i18n).toEqual({ 'en-US': 'Home' })
  })

  it('normalizeCatalog drops malformed UI contributions without dropping the plugin', () => {
    const list = normalizeCatalog({ plugins: [{
      ...plugin(), contributes: { applications: [{
        id: 'example.app', title: '示例应用', ui: { apiVersion: 2, navigation: { title: '坏', route: 'BAD ROUTE' } },
      }] },
    }] })
    expect(list).toHaveLength(1)
    expect(list[0]?.contributes.applications?.[0]?.ui).toBeUndefined()
  })
})

describe('application navigation and route resolution', () => {
  it('shows verified Application plugins with enabled instances and never Driver plugins', () => {
    const driver = plugin({ id: 'example.driver', kind: 'driver', contributes: { drivers: [{ id: 'd', ui: { apiVersion: 1, navigation: { title: '驱动', route: 'driver' } } }] } })
    const items = buildApplicationNavigation([plugin(), driver], [instance()], true)
    expect(items.map((item) => item.route)).toEqual(['example'])
  })

  it('respects visibility, disabled instances and route conflicts fail-closed', () => {
    const disabled = instance('example.app', { desired: { ...instance().desired, enabled: false } })
    expect(buildApplicationNavigation([plugin()], [disabled], true)).toHaveLength(0)
    const always = plugin({ contributes: { applications: [{
      ...plugin().contributes.applications![0], ui: { ...plugin().contributes.applications![0].ui!, navigation: { title: '示例', route: 'example', visibility: 'always' } },
    }] } })
    expect(buildApplicationNavigation([always], [disabled], true)).toHaveLength(1)
    const conflict = plugin({ id: 'example.other', contributes: { applications: [{ id: 'other', ui: { apiVersion: 1, navigation: { title: '其他', route: 'example' }, pages: [{ id: 'home', title: '其他', sections: [{ type: 'status' }] }] } }] } })
    expect(buildApplicationNavigation([plugin(), conflict], [instance(), instance('example.other')], true)).toHaveLength(0)
  })

  it('returns explicit deep-link states and selects a requested instance', () => {
    const second = instance('example.app', { id: 'server/app-b', desired: { ...instance().desired, instance_id: 'app-b' } })
    const ready = resolveApplicationRoute([plugin()], [instance(), second], 'example', undefined, 'app-b', true)
    expect(ready.kind).toBe('ready')
    if (ready.kind === 'ready') expect(ready.instance.desired.instance_id).toBe('app-b')
    expect(resolveApplicationRoute([plugin()], [], 'example', undefined, undefined, true).kind).toBe('no-instance')
    expect(resolveApplicationRoute([plugin()], [instance('example.app', { desired: { ...instance().desired, enabled: false } })], 'example', undefined, undefined, true).kind).toBe('disabled')
    expect(resolveApplicationRoute([plugin()], [instance()], 'missing', undefined, undefined, true).kind).toBe('not-found')
    expect(resolveApplicationRoute([plugin()], [instance()], 'example', 'missing', undefined, true).kind).toBe('page-not-found')
  })

  it('treats open L0 as readable while preserving account-mode tenant gating', () => {
    expect(applicationUIReadable(null, 'open')).toBe(true)
    expect(applicationUIReadable(null, 'out')).toBe(false)
    expect(applicationUIReadable({ id: 1, username: 'u', name: 'U', role: 'viewer', tenant_id: 1, tenant_slug: 't' }, 'in')).toBe(true)
    expect(applicationUIReadable({ id: 1, username: 'u', name: 'U', role: 'viewer', tenant_id: 0, tenant_slug: 't' }, 'in')).toBe(false)
  })
})
