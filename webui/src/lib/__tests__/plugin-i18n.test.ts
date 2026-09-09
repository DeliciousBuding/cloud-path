import { afterEach, describe, expect, it } from 'vitest'
import { i18n } from '@/i18n'
import { resolveLocalizedText } from '@/i18n/pluginText'
import { resolveUIFieldDescription, resolveUIFieldLabel, resolveUIFieldValue } from '@/lib/plugin-ui'
import {
  capabilityLabel, commandDecl, entityTitle, indexCapabilities, normalizeCapabilityDocs, normalizeDescriptor,
} from '@/lib/descriptor'

afterEach(async () => {
  await i18n.changeLanguage('zh-CN')
})

describe('resolveLocalizedText', () => {
  it('按精确 locale、基础 locale、兼容 locale、旧字段顺序解析', () => {
    expect(resolveLocalizedText({
      title: 'legacy',
      i18n: { 'en-US': 'Exact', en: 'Base', 'zh-CN': '中文', 'en-US.description': 'Description' },
    }, 'title', 'en-US')).toBe('Exact')
    expect(resolveLocalizedText({ title: 'legacy', i18n: { en: 'Base' } }, 'title', 'en-US')).toBe('Base')
    expect(resolveLocalizedText({ title: 'legacy', i18n: { 'zh-CN': '兼容' } }, 'title', 'fr-FR')).toBe('兼容')
    expect(resolveLocalizedText({ title: 'legacy', i18n: { 'en-US': 'Compatibility' } }, 'title', 'fr-FR')).toBe('Compatibility')
    expect(resolveLocalizedText({ title: '  legacy  ' }, 'title', 'fr-FR')).toBe('legacy')
  })

  it('Action description 使用字段限定键，不误用 title 翻译', () => {
    const action = {
      title: 'Open',
      description: 'legacy description',
      i18n: {
        'en-US': 'Open',
        'en-US.description': 'Open the compartment',
      },
    }
    expect(resolveLocalizedText(action, 'title', 'en-US')).toBe('Open')
    expect(resolveLocalizedText(action, 'description', 'en-US')).toBe('Open the compartment')
  })


  it('emptyText 使用驼峰字段键时仍按大小写不敏感解析', () => {
    const section = {
      title: '当前告警', emptyText: '还没有告警记录。',
      i18n: {
        'en-US': 'Current alert',
        'en-US.emptyText': 'No alert records yet.',
      },
    }
    expect(resolveLocalizedText(section, 'emptyText', 'en-US')).toBe('No alert records yet.')
  })
  it('解析字段 label/description 与枚举 valuesI18n', () => {
    const field = {
      key: 'state', label: '状态', description: '当前状态',
      i18n: { 'en-US': 'Status', 'en-US.description': 'Current state' },
      values: { opened: '待取药' },
      valuesI18n: { opened: { 'en-US': 'Waiting for pickup', 'zh-CN': '待取药' } },
    }
    expect(resolveUIFieldLabel(field, 'en-US')).toBe('Status')
    expect(resolveUIFieldDescription(field, 'en-US')).toBe('Current state')
    expect(resolveUIFieldValue(field, 'opened', 'en-US')).toBe('Waiting for pickup')
    expect(resolveUIFieldValue(field, 'opened', 'zh-CN')).toBe('待取药')
  })
  it('locale key 大小写和连字符宽容', () => {
    expect(resolveLocalizedText({ i18n: { EN_us: 'English' } }, 'title', 'en-US')).toBe('English')
  })

  it('解析插件 UI 导航和页面标题', () => {
    const navigation = { title: '药盒提醒', i18n: { 'zh-CN': '药盒提醒', 'en-US': 'Pillbox reminders' }, route: 'pillbox' }
    const page = { id: 'home', title: '首页', i18n: { 'en-US': 'Home' }, sections: [] }
    expect(resolveLocalizedText(navigation, 'title', 'en-US')).toBe('Pillbox reminders')
    expect(resolveLocalizedText(page, 'title', 'en-US')).toBe('Home')
    expect(resolveLocalizedText(navigation, 'title', 'zh-CN')).toBe('药盒提醒')
  })
})

describe('descriptor i18n parsing', () => {
  it('保留并解析 Entity name 的 i18n map', async () => {
    await i18n.changeLanguage('en-US')
    const descriptor = normalizeDescriptor({
      device_id: 'device-1',
      external_id: 'device-1',
      status: 'online',
      entities: [{
        entity_id: 'temperature',
        unique_key: 'temperature',
        name: '温度',
        i18n: { 'en-US': 'Temperature' },
        category: 'sensor',
        capabilities: ['cloudpath.dev/capability/temperature@1'],
      }],
    })
    expect(descriptor?.entities[0].i18n).toEqual({ 'en-US': 'Temperature' })
    expect(entityTitle(descriptor!.entities[0])).toBe('Temperature')
  })

  it('保留并解析 Capability metadata 与 Action 的 i18n', async () => {
    await i18n.changeLanguage('en-US')
    const docs = normalizeCapabilityDocs([{
      apiVersion: 'capabilities.cloudpath.dev/v1alpha1',
      kind: 'Capability',
      metadata: {
        id: 'cloudpath.dev/capability/door@1',
        version: 1,
        title: '门',
        i18n: { 'en-US': 'Door' },
      },
      spec: {
        actions: {
          open: {
            title: '打开',
            description: '打开舱门',
            i18n: { 'en-US': 'Open', 'en-US.description': 'Open the compartment' },
          },
        },
      },
    }])
    const index = indexCapabilities(docs)
    expect(capabilityLabel('cloudpath.dev/capability/door@1', index)).toBe('Door')
    expect(commandDecl('open', index)).toEqual({
      title: 'Open',
      description: 'Open the compartment',
    })
  })
})
