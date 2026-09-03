// @vitest-environment node
// 契约镜像守卫：把 AGENTS.md「契约三处同步」里前端这一处变成可执行回归。
// 直接解析 Go 侧 internal/api/types.go 的 struct + json tag，逐字对照 webui/src/lib/types.ts，
// 因此后端加字段/改 tag 而前端没跟上时，这里会红；前端私自发明后端没有的字段时，这里也会红。
// 只读源码文件，不需要 DOM，故跑在 node 环境。
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'

const REPO_ROOT = fileURLToPath(new URL('../../../../', import.meta.url))
const GO_SRC = readFileSync(`${REPO_ROOT}internal/api/types.go`, 'utf8')
const TS_SRC = readFileSync(`${REPO_ROOT}webui/src/lib/types.ts`, 'utf8')

/* ---------------- Go struct 解析 ---------------- */

interface GoField { json: string; omitempty: boolean; goType: string; ptr: boolean }

/** 取出 `type X struct { ... }` 的字段（按大括号配对，不受嵌套影响） */
function goStruct(name: string): GoField[] {
  const start = GO_SRC.indexOf(`type ${name} struct {`)
  if (start < 0) throw new Error(`Go 侧找不到 struct ${name}`)
  let depth = 0
  let end = -1
  for (let i = GO_SRC.indexOf('{', start); i < GO_SRC.length; i++) {
    if (GO_SRC[i] === '{') depth++
    else if (GO_SRC[i] === '}') { depth--; if (depth === 0) { end = i; break } }
  }
  const body = GO_SRC.slice(GO_SRC.indexOf('{', start) + 1, end)
  const out: GoField[] = []
  for (const line of body.split('\n')) {
    const m = /`json:"([^",]+)(,omitempty)?"`/.exec(line)
    if (!m) continue
    // 字段名之后、反引号之前的部分即 Go 类型
    const before = line.slice(0, line.indexOf('`json:')).trim()
    const parts = before.split(/\s+/)
    const goType = parts.slice(1).join(' ')
    out.push({ json: m[1], omitempty: Boolean(m[2]), goType, ptr: goType.startsWith('*') })
  }
  return out
}

/* ---------------- TS interface 解析 ---------------- */

interface TsField { name: string; optional: boolean; type: string }

function tsInterface(name: string): TsField[] {
  const start = TS_SRC.indexOf(`export interface ${name} {`)
  if (start < 0) throw new Error(`types.ts 找不到 interface ${name}`)
  let depth = 0
  let end = -1
  for (let i = TS_SRC.indexOf('{', start); i < TS_SRC.length; i++) {
    if (TS_SRC[i] === '{') depth++
    else if (TS_SRC[i] === '}') { depth--; if (depth === 0) { end = i; break } }
  }
  const body = TS_SRC.slice(TS_SRC.indexOf('{', start) + 1, end)
  const out: TsField[] = []
  for (const raw of body.split('\n')) {
    const line = raw.trim()
    if (!line || line.startsWith('//') || line.startsWith('*') || line.startsWith('/*')) continue
    const m = /^([A-Za-z_][\w]*)(\?)?:\s*(.+?)\s*$/.exec(line)
    if (!m) continue
    out.push({ name: m[1], optional: Boolean(m[2]), type: m[3] })
  }
  return out
}

/* ---------------- 对照 ---------------- */

/** Go 类型 → 允许的 TS 类型片段（宽松但足以挡住「string 写成 number」这类错） */
function tsTypeOf(g: GoField): string[] {
  const base = g.goType.replace(/^\*/, '')
  if (base.startsWith('[]')) {
    const elem = base.slice(2)
    return [`${goScalar(elem)}[]`]
  }
  if (base.startsWith('map[string]string')) return ['Record<string, string>']
  return [goScalar(base)]
}

function goScalar(t: string): string {
  switch (t) {
    case 'string': return 'string'
    case 'bool': return 'boolean'
    case 'int': case 'int64': case 'uint64': case 'int32': case 'float64': return 'number'
    default: return t // 结构体/别名类型：名字应与 TS 同名
  }
}

/** 必须逐字镜像的 DTO 清单（任务书 §3 冻结列表） */
const MIRRORED = [
  'OverviewView',
  'PluginInstanceDesiredView',
  'PluginInstanceObservedView',
  'PluginInstanceView',
  'PluginInstanceListResponse',
  'PluginInstanceCreateRequest',
  'PluginInstanceUpdateRequest',
  'PluginInstanceDeleteRequest',
  'PluginInstanceWriteResponse',
  'PluginInstanceActionRequest',
  'PluginPermissionsData',
  'PluginContributionsData',
  'PluginDriverContributionData',
  'PluginApplicationContributionData',
  'PluginConnectorContributionData',
  'PluginInstallationStatusData',
  'PluginObservedInstanceData',
  'PluginStatusData',
  'PluginDesiredInstanceData',
  'PluginDesiredData',
  'PluginApplyResultData',
  'PluginAckData',
] as const

describe('types.ts ↔ internal/api/types.go 字段一致性', () => {
  for (const name of MIRRORED) {
    it(`${name}：字段名 / 可选性 / 类型逐字对齐`, () => {
      const go = goStruct(name)
      const ts = tsInterface(name)
      expect(go.length, `${name} 在 Go 侧解析不到字段，解析器可能失效`).toBeGreaterThan(0)

      const tsByName = new Map(ts.map((f) => [f.name, f]))
      const missing: string[] = []
      const optMismatch: string[] = []
      const typeMismatch: string[] = []

      for (const g of go) {
        const f = tsByName.get(g.json)
        if (!f) { missing.push(g.json); continue }
        // Go 指针字段（*int64）与 omitempty 都表达「可缺席」→ TS 必须可选
        const goOptional = g.omitempty || g.ptr
        if (goOptional !== f.optional) {
          optMismatch.push(`${g.json}（Go omitempty=${g.omitempty} ptr=${g.ptr} / TS optional=${f.optional}）`)
        }
        const allowed = tsTypeOf(g)
        if (!allowed.includes(f.type)) {
          typeMismatch.push(`${g.json}: Go ${g.goType} → 期望 ${allowed.join('|')}，实际 ${f.type}`)
        }
      }

      // 反向：types.ts 不得出现 Go 侧没有的字段（防「私自发明后端没有的字段」）
      const goNames = new Set(go.map((g) => g.json))
      const invented = ts.filter((f) => !goNames.has(f.name)).map((f) => f.name)

      expect({ missing, optMismatch, typeMismatch, invented }).toEqual({
        missing: [], optMismatch: [], typeMismatch: [], invented: [],
      })
    })
  }
})

describe('稳定错误码 PluginErr* 镜像', () => {
  const goCodes = [...GO_SRC.matchAll(/PluginErr\w+\s*=\s*"([^"]+)"/g)].map((m) => m[1])

  it('Go 侧确实解析出了 7 个稳定码（解析器自检）', () => {
    expect(goCodes.length).toBe(7)
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

  // 文案映射的**行为**断言在 lib/__tests__/plugin-errors.test.ts（需 jsdom 才能 import 组件层 Tone）
})

describe('plugin_ack 稳定状态值镜像', () => {
  it('applied / rejected / failed 三个值与 Go 常量一致', () => {
    for (const [goConst, value] of [
      ['PluginAckApplied', 'applied'],
      ['PluginAckRejected', 'rejected'],
      ['PluginAckFailed', 'failed'],
    ] as const) {
      // gofmt 会按最长常量名对齐等号，故用 \s* 而不是写死空格数
      expect(new RegExp(`${goConst}\\s*=\\s*"${value}"`).test(GO_SRC),
        `Go 侧 ${goConst} 不再是 "${value}"，前端需同步`).toBe(true)
      expect(TS_SRC, `types.ts 缺少 ${value}`).toContain(`= '${value}'`)
    }
  })
})