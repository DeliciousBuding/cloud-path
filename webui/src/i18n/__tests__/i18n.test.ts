import { beforeEach, describe, expect, it } from 'vitest'
import { DEFAULT_LOCALE, LOCALE_STORAGE_KEY, currentLocale, i18n, setLocale } from '../index'
import { resolveLocalizedText } from '../pluginText'
import { resources } from '../resources'

const PLURAL_SUFFIX = /_(zero|one|two|few|many|other)$/

function flatten(value: unknown, prefix = ''): Map<string, string> {
  const out = new Map<string, string>()
  const visit = (node: unknown, path: string): void => {
    if (typeof node === 'string') {
      out.set(path, node)
      return
    }
    if (!node || typeof node !== 'object' || Array.isArray(node)) {
      throw new Error(`unsupported translation node at ${path || '<root>'}`)
    }
    for (const [key, child] of Object.entries(node)) {
      visit(child, path ? `${path}.${key}` : key)
    }
  }
  visit(value, prefix)
  return out
}

function canonicalKey(key: string): string {
  return key.replace(PLURAL_SUFFIX, '')
}

function canonicalKeys(locale: keyof typeof resources): string[] {
  return [...new Set([...flatten(resources[locale]).keys()].map(canonicalKey))].sort()
}

describe('i18n resources', () => {
  it('keeps every locale structurally aligned', () => {
    const expected = canonicalKeys(DEFAULT_LOCALE)
    for (const locale of Object.keys(resources) as (keyof typeof resources)[]) {
      expect(canonicalKeys(locale), locale).toEqual(expected)
    }
  })

  it('does not ship empty translation values', () => {
    for (const locale of Object.keys(resources) as (keyof typeof resources)[]) {
      for (const [key, value] of flatten(resources[locale])) {
        expect(value.trim(), `${locale}:${key}`).not.toBe('')
      }
    }
  })
})

describe('i18n runtime', () => {
  beforeEach(async () => {
    await setLocale(DEFAULT_LOCALE)
  })

  it('falls back to the default locale when a key is missing', async () => {
    await setLocale('en-US')
    i18n.addResourceBundle(DEFAULT_LOCALE, 'common', { __a8_fallback__: '回退值' }, true, true)
    try {
      expect(i18n.t('common:__a8_fallback__')).toBe('回退值')
    } finally {
      i18n.removeResourceBundle(DEFAULT_LOCALE, 'common')
    }
  })

  it('switches locale, storage and the document language together', async () => {
    await setLocale('en-US')
    expect(currentLocale()).toBe('en-US')
    expect(localStorage.getItem(LOCALE_STORAGE_KEY)).toBe('en-US')
    expect(document.documentElement.lang).toBe('en-US')
    expect(i18n.t('nav:overview')).toBe('Overview')

    await setLocale('zh-CN')
    expect(currentLocale()).toBe('zh-CN')
    expect(localStorage.getItem(LOCALE_STORAGE_KEY)).toBe('zh-CN')
    expect(document.documentElement.lang).toBe('zh-CN')
    expect(i18n.t('nav:overview')).toBe('概览')
  })

  it('resolves plugin text in the active locale and falls back deliberately', async () => {
    await setLocale('en-US')
    expect(resolveLocalizedText({ i18n: { 'en-US': 'English title' } }, 'title')).toBe('English title')
    expect(resolveLocalizedText({ i18n: { 'zh-CN': '中文标题' } }, 'title')).toBe('中文标题')
    expect(resolveLocalizedText({ title: 'Legacy title' }, 'title')).toBe('Legacy title')
  })
})