import { beforeEach, describe, expect, it } from 'vitest'
import { appActionScope, appJobArgsError, appJobSchema, manualAppJobs } from '@/lib/application-actions'
import { api, setToken } from '@/lib/api'
import { appUser } from '@/test/application-plane'
import { accepted, emptyJob } from '@/test/application-actions'
import { installFetch } from '@/test/http'
import { resetStores } from '@/test/render'
import { useAuth } from '@/store/auth'
import type { AppJobView } from '@/lib/types'

beforeEach(resetStores)

describe('应用操作的请求契约', () => {
  it('路径分段编码，传入原始 JSON/key，沿用同源 cookie 与可选 Bearer', async () => {
    useAuth.setState({ status: 'in', user: { ...appUser, role: 'operator' } })
    setToken('test-action-token')
    const http = installFetch(() => accepted('app/a?#', 'run /?#'))
    const body = { args_json: '{\n  "note": "保留空白"\n}', idempotency_key: 'logical-request-1' }
    await api.runAppJob('app/a?#', 'run /?#', body)
    expect(http.calls).toHaveLength(1)
    expect(http.calls[0]).toMatchObject({
      url: '/api/plugin-instances/app%2Fa%3F%23/jobs/run%20%2F%3F%23/run', method: 'POST', body,
      credentials: 'same-origin', headers: { 'Content-Type': 'application/json', Authorization: 'Bearer test-action-token' },
    })
  })

  it.each(['[]', 'null', 'true', '123', '"text"', '{oops', '{"n":1e999}'])('不接受非对象或无效 JSON：%s', (args) => {
    expect(appJobArgsError(args, {})).toBeTruthy()
  })

  it('不是设备的 64 字节限制；多行 JSON 原文按 UTF-8 的 4096 字节计算', () => {
    const prefix = '{"note":"'
    const suffix = '"}'
    const exact = prefix + '字'.repeat(1361) + 'aa' + suffix
    expect(new TextEncoder().encode(exact)).toHaveLength(4096)
    expect(appJobArgsError(exact, { type: 'object' })).toBeUndefined()
    expect(appJobArgsError(exact.replace('aa', 'aaa'), {})).toContain('4096')
    expect(appJobArgsError(['{', '  "count": 2', '}'].join(String.fromCharCode(10)), { type: 'object' })).toBeUndefined()
  })

  it('未知约束不伪装已验证，仍执行已知 required/range/schema 组合校验', () => {
    const schema = { type: 'object', required: ['count'], properties: { count: { type: 'integer', minimum: 2 } }, $ref: '#/$defs/input' }
    expect(appJobArgsError('{}', schema)).toContain('count')
    expect(appJobArgsError('{"count":1}', schema)).toBeTruthy()
    expect(appJobArgsError('{"count":2}', schema)).toBeUndefined()
  })

  it.each(['{', '[]', '"object"', 'null'])('无效参数声明不降级成无约束执行：%s', (json) => {
    expect(appJobSchema(json).error).toBeTruthy()
    expect(appJobSchema(json).schema).toBeUndefined()
  })
  it('无声明仍要求对象；布尔 false 声明必须拒绝执行', () => {
    expect(appJobArgsError('[]', appJobSchema('').schema!)).toBeTruthy()
    expect(appJobArgsError('{}', appJobSchema('false').schema!)).toBeTruthy()
    expect(appJobArgsError('{}', appJobSchema('true').schema!)).toBeUndefined()
  })
  it('只接收严格的 manual_only=true，不把字符串 true 或缺失标记当授权', () => {
    const malformed = { ...emptyJob, id: 'not-manual', manual_only: 'true' } as unknown as AppJobView
    expect(manualAppJobs([emptyJob, { ...emptyJob, manual_only: false }, malformed])).toEqual([emptyJob])
    expect(manualAppJobs(undefined)).toEqual([])
  })
  it('服务令牌 id=0 可按 operator 执行，无租户/禁用身份/开放访问不能执行', () => {
    const user = { ...appUser, role: 'operator' as const, id: 0 }
    expect(appActionScope({ status: 'in', user }, 'app-a')).not.toBeNull()
    expect(appActionScope({ status: 'open', user }, 'app-a')).toBeNull()
    expect(appActionScope({ status: 'in', user: { ...user, tenant_id: 0 } }, 'app-a')).toBeNull()
    expect(appActionScope({ status: 'in', user: { ...user, disabled: true } }, 'app-a')).toBeNull()
    expect(appActionScope({ status: 'in', user: appUser }, 'app-a')).toBeNull()
  })
})
