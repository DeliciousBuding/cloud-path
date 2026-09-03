// lib/admin.ts 的纯函数面：最小权限默认值、有效期映射、状态码 → 人话。
// 这里的断言就是「默认不预选 admin / 不复述业务规则」的可执行规格。
import { describe, expect, it } from 'vitest'
import { ApiError } from '@/lib/api'
import {
  DEFAULT_EXPIRY, DEFAULT_ROLE, DEFAULT_SCOPES, EXPIRY_OPTIONS, ROLE_OPTIONS, SCOPE_OPTIONS,
  adminErrorMessage, expiryToUnix, roleOption,
} from '@/lib/admin'

const DAY_S = 86_400

describe('TestScopeDefaultsLeastPrivilege（常量层）', () => {
  it('scope 默认只有 read，write/admin/edge 一律不预选', () => {
    expect(DEFAULT_SCOPES).toEqual(['read'])
    expect(DEFAULT_SCOPES).not.toContain('admin')
    expect(DEFAULT_SCOPES).not.toContain('edge')
  })

  it('四个 scope 齐备（read/write/admin/edge），且危险范围都带说明文案', () => {
    expect(SCOPE_OPTIONS.map((o) => o.value)).toEqual(['read', 'write', 'admin', 'edge'])
    for (const o of SCOPE_OPTIONS) expect(o.hint.length, `${o.value} 缺少说明`).toBeGreaterThan(0)
    for (const o of SCOPE_OPTIONS.filter((x) => x.danger)) {
      expect(['admin', 'edge']).toContain(o.value)
      expect(o.hint.length).toBeGreaterThan(8)
    }
  })

  it('新建用户默认角色是 viewer（最小权限），admin 必须显式选择', () => {
    expect(DEFAULT_ROLE).toBe('viewer')
    expect(ROLE_OPTIONS.map((o) => o.value)).toEqual(['viewer', 'operator', 'admin'])
    expect(roleOption('admin')?.hint).toMatch(/服务令牌/)
    expect(roleOption('nope')).toBeUndefined()
  })

  it('默认有效期是 30 天而不是永不过期', () => {
    expect(DEFAULT_EXPIRY).toBe('30')
    expect(EXPIRY_OPTIONS.map((o) => o.value)).toEqual(['1', '7', '30', '90', 'never'])
  })
})

describe('expiryToUnix', () => {
  const now = 1_700_000_000_000

  it('天数 → 未来的 unix 秒', () => {
    expect(expiryToUnix('1', now)).toBe(Math.floor(now / 1000) + DAY_S)
    expect(expiryToUnix('30', now)).toBe(Math.floor(now / 1000) + 30 * DAY_S)
  })

  it.each(['never', '', '0', '-3', 'abc'])('%p → undefined（字段不进 body）', (v) => {
    expect(expiryToUnix(v, now)).toBeUndefined()
  })
})

describe('adminErrorMessage', () => {
  it('401/403 是本地可解释的语义，不直接把英文错误丢给用户', () => {
    expect(adminErrorMessage(new ApiError(401, 'authentication required'))).toBe('登录已失效，请重新登录后再操作')
    expect(adminErrorMessage(new ApiError(403, 'permission denied'))).toBe('权限不足：该操作需要管理员角色')
  })

  it('409 原样采用服务端人话（最后一个 admin 的规则由 server 判定，前端不复述）', () => {
    const msg = '不能禁用或降级最后一个可用 admin'
    expect(adminErrorMessage(new ApiError(409, msg))).toBe(msg)
  })

  it('400/404/500 同样透传服务端说明；空说明回落状态码', () => {
    expect(adminErrorMessage(new ApiError(409, 'username 已存在'))).toBe('username 已存在')
    expect(adminErrorMessage(new ApiError(500, ''))).toBe('请求失败（HTTP 500）')
  })

  it('429 带上 Retry-After', () => {
    expect(adminErrorMessage(new ApiError(429, 'too many', 7))).toBe('操作过于频繁，请 7 秒后重试')
    expect(adminErrorMessage(new ApiError(429, 'too many'))).toBe('操作过于频繁，请稍后重试')
  })

  it('网络错误保持 lib/api.ts 的可读中文，不泄漏堆栈', () => {
    expect(adminErrorMessage(new Error('无法连接 server（服务未启动或网络不可达）')))
      .toBe('无法连接 server（服务未启动或网络不可达）')
    expect(adminErrorMessage('boom')).toBe('请求失败')
  })
})