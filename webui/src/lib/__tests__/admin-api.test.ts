// 管理面 API 客户端契约（docs/api.md §3.2-3.3）：路径/方法/请求体冻结，
// 凭据沿用同一层（same-origin cookie + 可选 Bearer），401 触发全局登出收敛，
// 409 原样透出服务端人话，令牌明文只在创建响应里出现一次。
import { beforeEach, describe, expect, it } from 'vitest'
import { ApiError, api, setToken } from '@/lib/api'
import { useAuth } from '@/store/auth'
import { installFetch, stubResponse } from '@/test/http'
import { resetStores } from '@/test/render'
import type { TokenView, UserView } from '@/lib/types'

const user: UserView = { id: 3, username: 'ops', name: 'Ops', role: 'viewer', tenant_id: 1, tenant_slug: 'local' }
const token: TokenView = {
  id: 7, name: 'ci', prefix: 'cp_7d4f', scopes: ['read'], created_at: 1_700_000_000,
}
const SECRET = 'cp_7d4f1a9c2e8b4d6f0a3c5e7b9d1f2a4c'

beforeEach(() => { resetStores() })

describe('用户管理端点（§3.2）', () => {
  it('GET / POST / PATCH 的路径、方法与请求体冻结', async () => {
    const http = installFetch((url, init) => {
      const m = (init?.method ?? 'GET').toUpperCase()
      if (url === '/api/users' && m === 'GET') return stubResponse(200, { users: [user] })
      if (url === '/api/users' && m === 'POST') return stubResponse(200, { user })
      if (url === '/api/users/3' && m === 'PATCH') return stubResponse(200, { user })
      return stubResponse(404, {})
    })

    expect((await api.users()).users).toEqual([user])
    await api.createUser({ username: 'ops', role: 'viewer', password: 'pw' })
    await api.updateUser(3, { name: 'Ops', role: 'operator', disabled: true })

    expect(http.calls.map((c) => `${c.method} ${c.url}`)).toEqual([
      'GET /api/users', 'POST /api/users', 'PATCH /api/users/3',
    ])
    expect(http.calls[1]?.body).toEqual({ username: 'ops', role: 'viewer', password: 'pw' })
    expect(http.calls[2]?.body).toEqual({ name: 'Ops', role: 'operator', disabled: true })
  })

  it('PATCH 只带要改的字段：重置密码不顺手改角色/禁用位', async () => {
    const http = installFetch((_url, init) =>
      ((init?.method ?? 'GET') === 'PATCH' ? stubResponse(200, { user }) : stubResponse(404, {})))
    await api.updateUser(3, { password: 'new-pw' })
    expect(http.last()?.body).toEqual({ password: 'new-pw' })
    expect(http.last()?.body).not.toHaveProperty('role')
  })

  it('凭据沿用同一层：same-origin cookie + 有令牌时带 Bearer', async () => {
    const http = installFetch(() => stubResponse(200, { users: [] }))
    setToken('tok-legacy')
    await api.users()
    expect(http.last()?.credentials).toBe('same-origin')
    expect(http.last()?.headers.Authorization).toBe('Bearer tok-legacy')
  })

  it('409 原样透出服务端人话（最后一个 admin / username 重复）', async () => {
    installFetch(() => stubResponse(409, { error: '不能禁用或降级最后一个可用 admin' }))
    const err = await api.updateUser(1, { disabled: true }).catch((e: unknown) => e)
    expect(err).toBeInstanceOf(ApiError)
    expect((err as ApiError).status).toBe(409)
    expect((err as ApiError).message).toBe('不能禁用或降级最后一个可用 admin')

    installFetch(() => stubResponse(409, { error: 'username 已存在' }))
    await expect(api.createUser({ username: 'ops', role: 'viewer', password: 'p' }))
      .rejects.toMatchObject({ status: 409, message: 'username 已存在' })
  })

  it('401 触发全局登出收敛（路由守卫据此跳 /login）', async () => {
    installFetch(() => stubResponse(401, { error: 'authentication required' }))
    useAuth.setState({ status: 'in', user })
    await expect(api.users()).rejects.toMatchObject({ status: 401 })
    expect(useAuth.getState().status).toBe('out')
  })

  it('403 是角色不足，不触发登出收敛（页面就地提示）', async () => {
    installFetch(() => stubResponse(403, { error: 'permission denied' }))
    useAuth.setState({ status: 'in', user })
    await expect(api.users()).rejects.toMatchObject({ status: 403 })
    expect(useAuth.getState().status).toBe('in')
  })
})

describe('租户服务令牌端点（§3.3）', () => {
  it('GET / POST / DELETE 的路径、方法与请求体冻结', async () => {
    const http = installFetch((url, init) => {
      const m = (init?.method ?? 'GET').toUpperCase()
      if (url === '/api/tokens' && m === 'GET') return stubResponse(200, { tokens: [token] })
      if (url === '/api/tokens' && m === 'POST') return stubResponse(200, { ...token, token: SECRET })
      if (url === '/api/tokens/7' && m === 'DELETE') return stubResponse(204)
      return stubResponse(404, {})
    })

    expect((await api.tokens()).tokens).toEqual([token])
    const created = await api.createToken({ name: 'ci', scopes: ['read'], expires_at: 1_702_592_000 })
    expect(await api.revokeToken(7)).toBeUndefined()

    expect(http.calls.map((c) => `${c.method} ${c.url}`)).toEqual([
      'GET /api/tokens', 'POST /api/tokens', 'DELETE /api/tokens/7',
    ])
    expect(http.calls[1]?.body).toEqual({ name: 'ci', scopes: ['read'], expires_at: 1_702_592_000 })
    expect(created.token).toBe(SECRET)
  })

  it('expires_at 缺省（永不过期）时字段不出现在 body 里', async () => {
    const http = installFetch(() => stubResponse(200, { ...token, token: SECRET }))
    await api.createToken({ name: 'ci', scopes: ['read'] })
    expect(http.last()?.body).toEqual({ name: 'ci', scopes: ['read'] })
    expect(http.last()?.body).not.toHaveProperty('expires_at')
  })

  it('明文只在创建响应里出现：列表响应的每个字段都不是明文', async () => {
    installFetch(() => stubResponse(200, { tokens: [token] }))
    const list = await api.tokens()
    expect(JSON.stringify(list)).not.toContain(SECRET)
    expect(Object.keys(list.tokens[0] ?? {})).toEqual([
      'id', 'name', 'prefix', 'scopes', 'created_at',
    ])
  })

  it('DELETE 204 不解析响应体（幂等吊销）', async () => {
    const http = installFetch(() => stubResponse(204))
    expect(await api.revokeToken(7)).toBeUndefined()
    expect(http.last()?.method).toBe('DELETE')
  })

  it('创建被拒（403）时抛出 ApiError，不返回任何明文', async () => {
    installFetch(() => stubResponse(403, { error: 'permission denied' }))
    const err = await api.createToken({ name: 'ci', scopes: ['read'] }).catch((e: unknown) => e)
    expect(err).toBeInstanceOf(ApiError)
    expect(JSON.stringify({ message: (err as ApiError).message })).not.toContain(SECRET)
  })
})