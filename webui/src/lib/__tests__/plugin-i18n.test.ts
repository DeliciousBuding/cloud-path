import { afterEach, describe, expect, it } from 'vitest'
import { i18n } from '@/i18n'
import { resolveLocalizedText } from '@/i18n/pluginText'
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

  it('locale key 大小写和连字符宽容', () => {
    expect(resolveLocalizedText({ i18n: { EN_us: 'English' } }, 'title', 'en-US')).toBe('English')
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
