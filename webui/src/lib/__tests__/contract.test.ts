// @vitest-environment node
// 只保留 Python scripts/check_contract.py 未覆盖的前端边界：
// Go 稳定错误码与 plugin_ack 状态值必须和 webui/src/lib/types.ts 同步。
// DTO 字段一致性由 Python 门禁负责，这里不再重复解析。
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'

const REPO_ROOT = fileURLToPath(new URL('../../../../', import.meta.url))
const GO_SRC = readFileSync(`${REPO_ROOT}internal/api/types.go`, 'utf8')
const TS_SRC = readFileSync(`${REPO_ROOT}webui/src/lib/types.ts`, 'utf8')

describe('稳定错误码 PluginErr* 镜像', () => {
  const goCodes = [...GO_SRC.matchAll(/PluginErr\w+\s*=\s*"([^"]+)"/g)].map((m) => m[1])

  it('Go 侧确实解析出了 11 个稳定码（解析器自检）', () => {
    expect(goCodes.length).toBe(11)
  })

  it('每个 Go 码都出现在 types.ts 的 PluginErr 常量里', () => {
    for (const code of goCodes) {
      expect(TS_SRC, `types.ts 缺少稳定错误码 ${code}`).toContain(`'${code}'`)
    }
  })

  it('types.ts 不得出现 Go 侧没有的插件错误码（防私自发明）', () => {
    const tsCodes = [...TS_SRC.matchAll(/PluginErr\s*=\s*\{([\s\S]*?)\}\s*as const/g)]
    expect(tsCodes, 'types.ts 缺少 PluginErr 常量表').toHaveLength(1)
    const literals = [...tsCodes[0][1].matchAll(/'([a-z0-9_]+)'/g)].map((m) => m[1])
    expect(literals.length, '每个 Go 码都应镜像一条').toBe(goCodes.length)
    expect([...literals].sort()).toEqual([...goCodes].sort())
  })
})

describe('WS MsgType 枚举镜像', () => {
  const goTypes = [...GO_SRC.matchAll(/Msg\w+\s+MsgType\s*=\s*"([^"]+)"/g)].map((m) => m[1])
  const tsUnion = TS_SRC.match(/export type WsType =([\s\S]*?)\r?\n\r?\n/)?.[1] ?? ''

  it('Go 侧解析出 16 个 WS 类型（解析器自检）', () => {
    expect(goTypes).toHaveLength(16)
  })

  it('每个 Go WS 类型都出现在 WsType 联合中', () => {
    for (const type of goTypes) expect(tsUnion, `WsType 缺少 ${type}`).toContain(`'${type}'`)
  })
})

describe('plugin_ack 稳定状态值镜像', () => {
  it('applied / rejected / failed 三个值与 Go 常量一致', () => {
    for (const [goConst, value] of [
      ['PluginAckApplied', 'applied'],
      ['PluginAckRejected', 'rejected'],
      ['PluginAckFailed', 'failed'],
    ] as const) {
      expect(new RegExp(`${goConst}\\s*=\\s*"${value}"`).test(GO_SRC),
        `Go 侧 ${goConst} 不再是 "${value}"，前端需同步`).toBe(true)
      expect(TS_SRC, `types.ts 缺少 ${value}`).toContain(`= '${value}'`)
    }
  })
})