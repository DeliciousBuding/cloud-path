// 登录态状态机（store/auth.ts）：契约探针 GET /api/auth/me 的三种结局 →
//   200 in / 401 out / 其它（BE-AUTH 未就绪、断网）open（放行，不把全站锁死）。
// 以及数据层 401 的全局收敛与登出清理。
import { beforeEach, describe, expect, it } from 'vitest'
import { authReady, logout, markUnauthenticated, refreshAuth, useAuth } from '@/store/auth'
import { getToken, setToken } from '@/lib/api'
import { installFetch, stubResponse } from '@/test/http'
import { resetStores } from '@/test/render'

const user = { id: 1, username: 'ops', name: 'Ops', role: 'admin', tenant_id: 1, tenant_slug: 'local' } as const

beforeEach(() => { resetStores() })

describe('refreshAuth：me 探针 → 登录态', () => {
  it('200 → in，并保存契约 user 对象', async () => {
    installFetch(() => stubResponse(200, { user }))
    await refreshAuth()
    expect(useAuth.getState()).toMatchObject({ status: 'in', user: { username: 'ops', role: 'admin' } })
  })

  it('401 → out（未登录）', async () => {
    installFetch(() => stubResponse(401, { error: '未登录' }))
    await refreshAuth()
    expect(useAuth.getState()).toEqual({ status: 'out', user: null })
  })

  it('me 不可用（500/404/断网）且仍在首帧探测 → open（开放访问，不锁站）', async () => {
    installFetch(() => stubResponse(500, { error: 'boom' }))
    await refreshAuth()
    expect(useAuth.getState().status).toBe('open')

    resetStores()
    installFetch(() => { throw new TypeError('Failed to fetch') })
    await refreshAuth()
    expect(useAuth.getState().status).toBe('open')
  })

  it('已判定为 in 后 me 暂不可达 → 保持 in（不因一次抖动把用户踢出）', async () => {
    useAuth.setState({ status: 'in', user })
    installFetch(() => stubResponse(503, { error: 'unavailable' }))
    await refreshAuth()
    expect(useAuth.getState().status).toBe('in')
  })

  it('已判定为 in 后收到 401 → 收敛为 out（会话过期）', async () => {
    useAuth.setState({ status: 'in', user })
    installFetch(() => stubResponse(401, { error: '会话已过期' }))
    await refreshAuth()
    expect(useAuth.getState().status).toBe('out')
  })
})

describe('数据层 401 收敛与登出', () => {
  it('markUnauthenticated 置 out 且幂等', () => {
    useAuth.setState({ status: 'open', user: null })
    markUnauthenticated()
    expect(useAuth.getState().status).toBe('out')
    markUnauthenticated()
    expect(useAuth.getState().status).toBe('out')
  })

  it('logout 清本机令牌并置 out，即使 server 会话注销失败', async () => {
    setToken('tok-abc')
    installFetch(() => stubResponse(500, { error: 'boom' }))
    useAuth.setState({ status: 'in', user })
    await logout()
    expect(getToken()).toBe('')
    expect(useAuth.getState()).toEqual({ status: 'out', user: null })
  })

  it('logout 正常路径打 POST /api/auth/logout', async () => {
    const http = installFetch(() => stubResponse(204))
    await logout()
    expect(http.last()).toMatchObject({ url: '/api/auth/logout', method: 'POST' })
  })

  it('authReady：in / open 放行，loading / out 拦住数据与实时通道', () => {
    expect(authReady('in')).toBe(true)
    expect(authReady('open')).toBe(true)
    expect(authReady('loading')).toBe(false)
    expect(authReady('out')).toBe(false)
  })
})