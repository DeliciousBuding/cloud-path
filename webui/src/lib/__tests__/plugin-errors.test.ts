// 插件写面错误码与 desired/observed 呈现逻辑的行为断言。
// 核心不是「文案好不好看」，而是三条硬不变量：
//   ① 10 个稳定码各有独立、可执行的文案，且只有 PermissionConfirm 会要求显式确认权限；
//   ② 服务端没给码时不得把 message 当业务规则复述（只报状态）；
//   ③ desired 永不被当成 observed —— has_observed=false / stale / drift 各有独立状态。
import { describe, expect, it } from 'vitest'
import { ApiError } from '@/lib/api'
import { i18n } from '@/i18n'
import {
  healthMeta, permissionCount, permissionGroups, pluginDisplayName, pluginErrorCopy, safeConfigEntries,
  hostDetailLabel, instanceStatus, secretHandleName, stateMeta, syncState, trustMeta,
} from '@/lib/plugins'
import { PluginErr } from '@/lib/types'
import type { PluginCatalogView, PluginInstanceView } from '@/lib/types'

const ALL_CODES = Object.values(PluginErr)

function err(status: number, code?: string, message = 'x'): ApiError {
  return new ApiError(status, message, undefined, code)
}

function instance(over: Partial<PluginInstanceView> = {}): PluginInstanceView {
  return {
    id: 'edge-a/inst-1', tenant_id: 1, edge_id: 'edge-a',
    desired: {
      instance_id: 'inst-1', plugin_id: 'io.github.acme.driver', version: 'v1.2.0',
      enabled: true, isolation: 'process', revision: 42, updated_at: 1_770_000_000,
    },
    has_observed: true,
    observed: { state: 'HEALTHY', health: 'HEALTHY', version: 'v1.1.0', restart_count: 0 },
    edge_online: true, desired_revision: 42, applied_revision: 42,
    drift: false, stale: false, last_ack_at: 1_770_000_000,
    ...over,
  }
}

describe('稳定错误码 → 文案', () => {
  it('切换语言只改变展示文案，稳定码与权限确认语义不变', async () => {
    const previous = i18n.language
    try {
      await i18n.changeLanguage('en-US')
      const en = pluginErrorCopy(err(400, PluginErr.Quota))
      expect(en.code).toBe(PluginErr.Quota)
      expect(en.title).toMatch(/limit/i)
      expect(en.hint).toMatch(/not saved/i)
      expect(en.needsPermissionConfirm).toBe(false)

      await i18n.changeLanguage('zh-CN')
      const zh = pluginErrorCopy(err(400, PluginErr.Quota))
      expect(zh.code).toBe(PluginErr.Quota)
      expect(zh.title).toMatch(/数量上限/)
      expect(zh.needsPermissionConfirm).toBe(false)
    } finally {
      await i18n.changeLanguage(previous)
    }
  })

  it('11 个码全部有映射，且文案互不相同（不留「未知错误」黑洞）', () => {
    expect(ALL_CODES).toHaveLength(11)
    expect(Object.values(PluginErr)).toHaveLength(11)
    const titles = new Set<string>()
    for (const code of ALL_CODES) {
      const copy = pluginErrorCopy(err(400, code))
      expect(copy.code, `${code} 必须回报命中的码`).toBe(code)
      expect(copy.title.length).toBeGreaterThan(1)
      expect(copy.hint.length, `${code} 必须给出可执行的下一步`).toBeGreaterThan(5)
      expect(['ok', 'warn', 'bad', 'accent', 'idle']).toContain(copy.tone)
      titles.add(copy.title)
    }
    expect(titles.size, '不同码不得共用同一标题').toBe(ALL_CODES.length)
  })

  it('只有「权限扩大」要求显式确认；quota 明确说明未生效（不可留成功假象）', () => {
    for (const code of ALL_CODES) {
      const copy = pluginErrorCopy(err(400, code))
      expect(copy.needsPermissionConfirm, code).toBe(code === PluginErr.PermissionConfirm)
    }
    expect(pluginErrorCopy(err(400, PluginErr.Quota)).hint).toMatch(/未生效/)
    expect(pluginErrorCopy(err(400, PluginErr.PermissionConfirm)).hint).toMatch(/确认/)
  })

  it('运行位置不匹配时说明 Driver 走网关、Application 走中心服务，主文案不露机器码', () => {
    const copy = pluginErrorCopy(err(409, 'plugin_instance_host_mismatch'))
    expect(copy.code).toBe('plugin_instance_host_mismatch')
    expect(copy.title).toMatch(/运行位置.*不匹配/)
    expect(copy.hint).toMatch(/驱动.*网关/)
    expect(copy.hint).toMatch(/应用.*中心服务/)
    expect(copy.hint).toMatch(/选择.*运行位置/)
    expect(`${copy.title} ${copy.hint}`).not.toMatch(/plugin_instance_host_mismatch/)
    expect(copy.retryable).toBe(false)
  })

  it('Connector 无运行时必须如实拒绝，不把换宿主说成解法', () => {
    const copy = pluginErrorCopy(err(409, 'plugin_instance_kind_unsupported'))
    expect(copy.code).toBe('plugin_instance_kind_unsupported')
    expect(copy.title).toMatch(/暂不支持/)
    expect(copy.hint).toMatch(/连接器.*没有可用运行时/)
    expect(copy.hint).toMatch(/选择网关或中心服务都不能/)
    expect(`${copy.title} ${copy.hint}`).not.toMatch(/plugin_instance_kind_unsupported/)
    expect(copy.retryable).toBe(false)
  })

  it('无法解析插件类型时提示确认安装与同步，不用成功态掩盖 fail-closed', () => {
    const copy = pluginErrorCopy(err(409, 'plugin_instance_kind_unavailable'))
    expect(copy.code).toBe('plugin_instance_kind_unavailable')
    expect(copy.title).toMatch(/无法确认插件类型/)
    expect(copy.hint).toMatch(/插件已经安装到目标运行位置/)
    expect(copy.hint).toMatch(/完成同步/)
    expect(copy.hint).toMatch(/刷新重试/)
    expect(`${copy.title} ${copy.hint}`).not.toMatch(/plugin_instance_kind_unavailable/)
    expect(copy.retryable).toBe(true)
  })

  it('Edge 离线不得说成失败：说明重连后会自动同步', () => {
    const copy = pluginErrorCopy(err(409, PluginErr.EdgeOffline))
    expect(copy.hint).toMatch(/重新连接/)
    expect(copy.retryable).toBe(true)
  })

  it('secret 相关文案只谈 handle，不诱导填写明文', () => {
    const copy = pluginErrorCopy(err(403, PluginErr.SecretForbidden))
    expect(copy.hint).toMatch(/密钥名称/)
    expect(copy.hint).toMatch(/不显示明文|只显示名称/)
  })

  it('服务端未给码时只报状态，不复述服务端 message 当业务规则', () => {
    const copy = pluginErrorCopy(err(409, undefined, '不能禁用最后一个 admin'))
    expect(copy.code).toBeUndefined()
    expect(copy.title).not.toMatch(/最后一个 admin/)
    expect(copy.hint).not.toMatch(/最后一个 admin/)
    expect(copy.title).toMatch(/409/)
  })

  it('401/403/429 有本地可解释语义；网络不可达不当成服务端拒绝', () => {
    expect(pluginErrorCopy(err(401)).title).toMatch(/登录/)
    expect(pluginErrorCopy(err(403)).title).toMatch(/权限不足/)
    expect(pluginErrorCopy(err(429)).title).toMatch(/频繁/)
    const net = pluginErrorCopy(new Error('无法连接 server'))
    expect(net.title).toMatch(/无法连接/)
    expect(net.hint).toMatch(/未提交/)
  })
})

describe('插件声明文本本地化', () => {
  it('优先使用 i18n map，缺省时回落到插件 title，机器 ID 永不翻译', async () => {
    const catalog = {
      id: 'io.github.acme.temperature', kind: 'application', version: 'v1', source: '', digest: '', verified: true,
      protocol: 1, permissions: {},
      contributes: { applications: [{ id: 'acme.temperature', title: '温度', i18n: { 'en-US': 'Temperature' } }] },
    } as PluginCatalogView
    const previous = i18n.language
    try {
      await i18n.changeLanguage('en-US')
      expect(pluginDisplayName(catalog)).toBe('Temperature')
      await i18n.changeLanguage('zh-CN')
      // 只有 en-US 翻译时，按架构回退顺序仍优先使用可用的 i18n 值。
      expect(pluginDisplayName(catalog)).toBe('Temperature')
      const legacy = { ...catalog, contributes: { applications: [{ id: 'acme.legacy', title: '温度' }] } } as PluginCatalogView
      expect(pluginDisplayName(legacy)).toBe('温度')
      expect(pluginDisplayName({ ...catalog, contributes: {} })).toBe(catalog.id)
    } finally {
      await i18n.changeLanguage(previous)
    }
  })
})

describe('desired / observed 永远分别呈现', () => {
  it('has_observed=false → 显式「Edge 未上报」，且绝不因 desired.enabled 变成运行中', () => {
    const un = syncState(instance({ has_observed: false, observed: undefined }))
    expect(un.key).toBe('unreported')
    expect(un.label).toMatch(/状态待确认|未上报/)
    expect(un.tone).not.toBe('ok')
    expect(un.hint).toMatch(/不能据此判断|尚未回过实际状态/)
    // desired.enabled=true 不得泄漏成「运行中/健康」
    expect(un.label).not.toMatch(/运行中|健康|已同步/)
  })

  it('未上报 + Edge 离线 → 说明重连后才会有事实（不承诺当前状态）', () => {
    const un = syncState(instance({ has_observed: false, observed: undefined, edge_online: false }))
    expect(un.key).toBe('unreported')
    expect(un.hint).toMatch(/离线/)
  })

  it('已启用但网关离线 → 计入需要处理，而不是“需要处理 0”', () => {
    const v = instance({ has_observed: false, observed: undefined, edge_online: false })
    const s = instanceStatus(v)
    expect(s.key).toBe('attention')
    expect(s.needsAttention).toBe(true)
    expect(s.summary).toMatch(/网关离线/)
    expect(s.next).toMatch(/恢复网关连接/)
  })

  it('stale=true → 独立「已过期」状态，并声明下面的是历史事实', () => {
    const s = syncState(instance({ stale: true }))
    expect(s.key).toBe('stale')
    expect(s.label).toMatch(/过期/)
    expect(s.tone).toBe('warn')
    expect(s.hint).toMatch(/上次状态|当前显示/)
  })

  it('drift=true → 独立「不一致」状态，说明网关尚未应用最新设置', () => {
    const d = syncState(instance({ drift: true, desired_revision: 42, applied_revision: 41 }))
    expect(d.key).toBe('drift')
    expect(d.tone).toBe('warn')
    expect(d.hint).toMatch(/还没有应用最新设置/)
    expect(d.hint).toMatch(/重新同步/)
  })

  it('stale 优先于 drift（过期数据谈一致性没有意义）', () => {
    expect(syncState(instance({ stale: true, drift: true })).key).toBe('stale')
  })

  it('applied < desired 但未标 drift → 「等待网关应用」，不是成功', () => {
    const p = syncState(instance({ applied_revision: 41, desired_revision: 42 }))
    expect(p.key).toBe('pending')
    expect(p.tone).not.toBe('ok')
  })

  it('全部对齐才是「已同步」', () => {
    expect(syncState(instance()).key).toBe('synced')
  })
})

describe('Edge 上报的运行态语义（规范大写名）', () => {
  it('pluginhost.State / Health 的规范名有中文语义', () => {
    expect(stateMeta('HEALTHY').tone).toBe('ok')
    expect(stateMeta('CRASHED').tone).toBe('bad')
    expect(stateMeta('DISABLED').tone).toBe('idle')
    // server AppHost（appruntime.InstanceState）小写状态同样有人话词汇，不露机器串
    expect(stateMeta('running').label).toBe('运行中')
    expect(stateMeta('stopping').label).toBe('停止中')
    expect(stateMeta('failed').tone).toBe('bad')
    expect(hostDetailLabel('server-apphost')).toBe('中心服务')
    expect(healthMeta('DEGRADED').tone).toBe('warn')
    expect(healthMeta('UNKNOWN').tone).toBe('idle')
  })

  it('未知值在主路径用中性文案，不把机器串当成用户状态', () => {
    const s = stateMeta('WEIRD')
    expect(s.label).toBe('状态待确认')
    expect(s.tone).toBe('idle')
    expect(healthMeta(undefined).label).toMatch(/未上报/)
  })

  it('verified=false 一律 warn（信任状态不得默认绿）', () => {
    expect(trustMeta('tofu', true).tone).toBe('ok')
    expect(trustMeta('tofu', false).tone).toBe('warn')
  })
})

describe('权限与 secret 边界', () => {
  it('只列出声明了的权限组，不塞「无」占位', () => {
    expect(permissionGroups(undefined)).toEqual([])
    expect(permissionGroups({})).toEqual([])
    const g = permissionGroups({ hardware: ['uart'], secrets: ['db'] })
    expect(g.map((x) => x.key)).toEqual(['hardware', 'secrets'])
    expect(permissionCount({ hardware: ['uart'], secrets: ['db'] })).toBe(2)
  })

  it('secret:// 值只显示 handle 名，明文不进 DOM', () => {
    expect(secretHandleName('secret://db-password')).toBe('db-password')
    expect(secretHandleName('db-password')).toBe('db-password')
    const rows = safeConfigEntries({ user: 'acme', pass: 'secret://db-password' })
    expect(rows).toEqual([
      { key: 'pass', value: 'db-password', isSecret: true },
      { key: 'user', value: 'acme', isSecret: false },
    ])
    expect(JSON.stringify(rows)).not.toMatch(/secret:\/\//)
  })

  it('配置为空/缺席时返回空数组，不抛错', () => {
    expect(safeConfigEntries(undefined)).toEqual([])
    expect(safeConfigEntries({})).toEqual([])
  })
})