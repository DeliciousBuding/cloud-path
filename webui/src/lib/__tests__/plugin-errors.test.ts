// 插件写面错误码与 desired/observed 呈现逻辑的行为断言。
// 核心不是「文案好不好看」，而是三条硬不变量：
//   ① 7 个稳定码各有独立、可执行的文案，且只有 PermissionConfirm 会要求显式确认权限；
//   ② 服务端没给码时不得把 message 当业务规则复述（只报状态）；
//   ③ desired 永不被当成 observed —— has_observed=false / stale / drift 各有独立状态。
import { describe, expect, it } from 'vitest'
import { ApiError } from '@/lib/api'
import {
  healthMeta, permissionCount, permissionGroups, pluginErrorCopy, safeConfigEntries,
  hostDetailLabel, secretHandleName, stateMeta, syncState, trustMeta,
} from '@/lib/plugins'
import { PluginErr, PLUGIN_ERR_CODES } from '@/lib/types'
import type { PluginInstanceView } from '@/lib/types'

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
  it('7 个码全部有映射，且文案互不相同（不留「未知错误」黑洞）', () => {
    expect(ALL_CODES).toHaveLength(7)
    expect(ALL_CODES.sort()).toEqual([...PLUGIN_ERR_CODES].sort())
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

  it('Edge 离线不得说成失败：说明重连后会自动收敛', () => {
    const copy = pluginErrorCopy(err(409, PluginErr.EdgeOffline))
    expect(copy.hint).toMatch(/重连/)
    expect(copy.retryable).toBe(true)
  })

  it('secret 相关文案只谈 handle，不诱导填写明文', () => {
    const copy = pluginErrorCopy(err(403, PluginErr.SecretForbidden))
    expect(copy.hint).toMatch(/handle/)
    expect(copy.hint).toMatch(/不显示明文|只显示 handle/)
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

describe('desired / observed 永远分别呈现', () => {
  it('has_observed=false → 显式「Edge 未上报」，且绝不因 desired.enabled 变成运行中', () => {
    const un = syncState(instance({ has_observed: false, observed: undefined }))
    expect(un.key).toBe('unreported')
    expect(un.label).toMatch(/未上报/)
    expect(un.tone).not.toBe('ok')
    expect(un.hint).toMatch(/不能据此判断|尚未回过实际态/)
    // desired.enabled=true 不得泄漏成「运行中/健康」
    expect(un.label).not.toMatch(/运行中|健康|已收敛/)
  })

  it('未上报 + Edge 离线 → 说明重连后才会有事实（不承诺当前状态）', () => {
    const un = syncState(instance({ has_observed: false, observed: undefined, edge_online: false }))
    expect(un.key).toBe('unreported')
    expect(un.hint).toMatch(/离线/)
  })

  it('stale=true → 独立「已过期」状态，并声明下面的是历史事实', () => {
    const s = syncState(instance({ stale: true }))
    expect(s.key).toBe('stale')
    expect(s.label).toMatch(/过期/)
    expect(s.tone).toBe('warn')
    expect(s.hint).toMatch(/历史事实/)
  })

  it('drift=true → 独立「不一致」状态，并把两个 revision 都摊开给用户看', () => {
    const d = syncState(instance({ drift: true, desired_revision: 42, applied_revision: 41 }))
    expect(d.key).toBe('drift')
    expect(d.tone).toBe('warn')
    expect(d.hint).toMatch(/42/)
    expect(d.hint).toMatch(/41/)
    expect(d.hint).toMatch(/重新下发/)
  })

  it('stale 优先于 drift（过期数据谈一致性没有意义）', () => {
    expect(syncState(instance({ stale: true, drift: true })).key).toBe('stale')
  })

  it('applied < desired 但未标 drift → 「等待边缘节点应用」，不是成功', () => {
    const p = syncState(instance({ applied_revision: 41, desired_revision: 42 }))
    expect(p.key).toBe('pending')
    expect(p.tone).not.toBe('ok')
  })

  it('全部对齐才是「已收敛」', () => {
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
    expect(hostDetailLabel('server-apphost')).toBe('服务器本地宿主（进程内）')
    expect(healthMeta('DEGRADED').tone).toBe('warn')
    expect(healthMeta('UNKNOWN').tone).toBe('idle')
  })

  it('未知值原样呈现为中性，不猜含义、不当成正常', () => {
    const s = stateMeta('WEIRD')
    expect(s.label).toBe('WEIRD')
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