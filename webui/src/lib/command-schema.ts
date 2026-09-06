// 命令输入使用的 JSON Schema 子集：不代入 default，不转换类型，不解析引用/组合。
// 不支持的关键字显式反馈，仍允许 JSON 编辑；设备端才是完整契约的最终裁决者。
import { argsError } from './format'

type Schema = Record<string, unknown>
const TYPE_NAMES: Record<string, string> = { object: '对象', array: '数组', string: '文本', number: '数值', integer: '整数', boolean: '布尔值', null: '空值' }
const TYPES = Object.keys(TYPE_NAMES)
const ANNOTATIONS = new Set(['title', 'description', 'default', 'examples', '$schema', '$id', '$comment', '$defs', 'definitions', 'readOnly', 'writeOnly', 'deprecated'])

function object(v: unknown): v is Record<string, unknown> {
  return v !== null && typeof v === 'object' && !Array.isArray(v)
}
function number(v: unknown): v is number {
  return typeof v === 'number' && Number.isFinite(v)
}
function count(v: unknown): v is number {
  return number(v) && Number.isInteger(v) && v >= 0
}
function types(v: unknown): string[] | null {
  const list = Array.isArray(v) ? v : [v]
  return list.length > 0 && list.every((t) => typeof t === 'string' && TYPES.includes(t)) ? list : null
}
function matchesType(value: unknown, type: string): boolean {
  if (type === 'null') return value === null
  if (type === 'object') return object(value)
  if (type === 'array') return Array.isArray(value)
  if (type === 'number') return number(value)
  if (type === 'integer') return number(value) && Number.isInteger(value)
  return typeof value === type
}
function equal(a: unknown, b: unknown): boolean {
  if (a === b) return true
  if (Array.isArray(a) && Array.isArray(b)) return a.length === b.length && a.every((v, i) => equal(v, b[i]))
  if (object(a) && object(b)) {
    const keys = Object.keys(a)
    return keys.length === Object.keys(b).length && keys.every((k) => Object.hasOwn(b, k) && equal(a[k], b[k]))
  }
  return false
}

/** 未实现或形状不合法的关键字，不冒充为已通过的约束。 */
export function unsupportedSchemaKeywords(schema: Schema): string[] {
  const unsupported = new Set<string>()
  const visit = (s: unknown) => {
    if (typeof s === 'boolean') return
    if (!object(s)) { unsupported.add('schema'); return }
    for (const [key, value] of Object.entries(s)) {
      if (ANNOTATIONS.has(key)) continue
      let supported = false
      switch (key) {
        case 'type': supported = types(value) !== null; break
        case 'enum': supported = Array.isArray(value); break
        case 'const': supported = true; break
        case 'minimum': case 'maximum': case 'exclusiveMinimum': case 'exclusiveMaximum':
          supported = number(value); break
        case 'multipleOf': supported = number(value) && value > 0; break
        case 'minLength': case 'maxLength': case 'minItems': case 'maxItems': case 'minProperties': case 'maxProperties':
          supported = count(value); break
        case 'uniqueItems': supported = typeof value === 'boolean'; break
        case 'required': supported = Array.isArray(value) && value.every((v) => typeof v === 'string'); break
        case 'properties':
          supported = object(value)
          if (supported) Object.values(value as Schema).forEach(visit)
          break
        case 'items': case 'additionalProperties':
          supported = typeof value === 'boolean' || object(value)
          // prefixItems / patternProperties 会改变对应关键字的作用域，不能半懂半猜。
          if (key === 'items' && 'prefixItems' in s) supported = false
          if (key === 'additionalProperties' && 'patternProperties' in s) supported = false
          if (supported) visit(value)
          break
      }
      if (!supported) unsupported.add(key)
    }
  }
  visit(schema)
  return [...unsupported].sort()
}

function propertyLabel(key: string, schema: unknown): string {
  if (!object(schema)) return key
  for (const label of [schema.title, schema.description]) {
    if (typeof label === 'string' && label.trim()) return label
  }
  return key
}

function validate(value: unknown, schema: unknown, at = '参数'): string | undefined {
  const fail = (why: string) => at + '：' + why
  if (schema === false) return fail('不允许此值')
  if (!object(schema)) return undefined
  const declaredTypes = types(schema.type)
  if (declaredTypes && !declaredTypes.some((t) => matchesType(value, t))) {
    return fail('需要' + declaredTypes.map((type) => TYPE_NAMES[type]).join('或') + '类型')
  }
  if (Array.isArray(schema.enum) && !schema.enum.some((v) => equal(value, v))) return fail('请选择声明的枚举值')
  if (Object.hasOwn(schema, 'const') && !equal(value, schema.const)) return fail('必须等于 ' + JSON.stringify(schema.const))
  if (number(value)) {
    if (number(schema.minimum) && value < schema.minimum) return fail('不能小于 ' + schema.minimum)
    if (number(schema.maximum) && value > schema.maximum) return fail('不能大于 ' + schema.maximum)
    if (number(schema.exclusiveMinimum) && value <= schema.exclusiveMinimum) return fail('必须大于 ' + schema.exclusiveMinimum)
    if (number(schema.exclusiveMaximum) && value >= schema.exclusiveMaximum) return fail('必须小于 ' + schema.exclusiveMaximum)
    if (number(schema.multipleOf) && schema.multipleOf > 0) {
      const quotient = value / schema.multipleOf
      if (!Number.isFinite(quotient) || Math.abs(quotient - Math.round(quotient)) > Number.EPSILON * Math.max(1, Math.abs(quotient)) * 4) {
        return fail('必须是 ' + schema.multipleOf + ' 的倍数')
      }
    }
  }
  if (typeof value === 'string') {
    const length = [...value].length // JSON Schema 的字符串长度是 Unicode 码点，不是传输字节数。
    if (count(schema.minLength) && length < schema.minLength) return fail('至少 ' + schema.minLength + ' 个字符')
    if (count(schema.maxLength) && length > schema.maxLength) return fail('最多 ' + schema.maxLength + ' 个字符')
  }
  if (Array.isArray(value)) {
    if (count(schema.minItems) && value.length < schema.minItems) return fail('至少 ' + schema.minItems + ' 项')
    if (count(schema.maxItems) && value.length > schema.maxItems) return fail('最多 ' + schema.maxItems + ' 项')
    if (schema.uniqueItems === true && value.some((v, i) => value.slice(0, i).some((other) => equal(v, other)))) {
      return fail('数组项不能重复')
    }
    if (!('prefixItems' in schema)) {
      for (const [i, item] of value.entries()) {
        const error = validate(item, schema.items, at + '[' + i + ']')
        if (error) return error
      }
    }
  }
  if (object(value)) {
    const keys = Object.keys(value)
    const properties = object(schema.properties) ? schema.properties : {}
    if (Array.isArray(schema.required) && schema.required.every((k) => typeof k === 'string')) {
      for (const key of schema.required) {
        if (!Object.hasOwn(value, key)) return fail('缺少必填参数 ' + propertyLabel(key, properties[key]))
      }
    }
    if (count(schema.minProperties) && keys.length < schema.minProperties) return fail('至少填写 ' + schema.minProperties + ' 个参数')
    if (count(schema.maxProperties) && keys.length > schema.maxProperties) return fail('最多填写 ' + schema.maxProperties + ' 个参数')
    for (const key of keys) {
      const label = propertyLabel(key, properties[key])
      const child = at === '参数' ? label : at + ' / ' + label
      if (Object.hasOwn(properties, key)) {
        const error = validate(value[key], properties[key], child)
        if (error) return error
      } else if (!('patternProperties' in schema)) {
        if (schema.additionalProperties === false) return fail('未声明的参数 ' + key)
        const error = validate(value[key], schema.additionalProperties, child)
        if (error) return error
      }
    }
  }
  return undefined
}

/** JSON 语法 + 已知 schema 约束 + 未改变的传输门禁。没有 schema 的原始参数不强制 JSON。 */
export function commandArgsError(args: string, schema?: Schema, maxBytes?: number): string | undefined {
  const wireError = argsError(args, maxBytes)
  if (wireError || !schema) return wireError
  if (!args.trim()) return '请填写 JSON 参数'
  let value: unknown
  try {
    value = JSON.parse(args, (_key, v: unknown) => {
      if (typeof v === 'number' && !Number.isFinite(v)) throw new Error('non-finite JSON number')
      return v
    })
  } catch {
    return 'JSON 格式无效，请检查括号、引号和数值'
  }
  return validate(value, schema)
}

export interface CommandField {
  key: string
  label: string
  description?: string
  required: boolean
  type: 'string' | 'number' | 'integer' | 'boolean' | 'enum'
  schema: Schema
  choices?: unknown[]
}

/** 仅把能无损表示的平铺标量对象变成字段；嵌套、数组、组合、未知约束保留 JSON。 */
export function commandFields(schema: Schema): CommandField[] | null {
  if (schema.type !== 'object' || !object(schema.properties) || unsupportedSchemaKeywords(schema).length) return null
  const entries = Object.entries(schema.properties)
  if (!entries.length) return null
  if (Array.isArray(schema.required) && schema.required.some((k) => !Object.hasOwn(schema.properties as Schema, k))) return null
  const fields: CommandField[] = []
  for (const [key, prop] of entries) {
    if (!object(prop)) return null
    const choices = Array.isArray(prop.enum) && prop.enum.length > 0 && prop.enum.every((v) => v === null || ['string', 'boolean', 'number'].includes(typeof v)) ? prop.enum : undefined
    const type = choices ? 'enum' : prop.type
    if (type !== 'string' && type !== 'number' && type !== 'integer' && type !== 'boolean' && type !== 'enum') return null
    const title = typeof prop.title === 'string' && prop.title.trim() ? prop.title : undefined
    const description = typeof prop.description === 'string' && prop.description.trim() ? prop.description : undefined
    fields.push({ key, label: propertyLabel(key, prop), description: title && description !== title ? description : undefined,
      required: Array.isArray(schema.required) && schema.required.includes(key), type, schema: prop, choices })
  }
  return fields
}
