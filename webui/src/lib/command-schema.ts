// 操作输入使用的 JSON Schema 子集：不代入 default，不转换类型；支持组合，不解析引用。
// 不支持的关键字显式反馈，仍允许 JSON 编辑；设备端才是完整契约的最终裁决者。
import { i18n } from '@/i18n'
import { argsError } from './format'

type Schema = Record<string, unknown>
type Validation = { state: 'valid' | 'unknown' } | { state: 'invalid'; error: string }
const VALID: Validation = { state: 'valid' }
const UNKNOWN: Validation = { state: 'unknown' }
const COMBINATORS = ['oneOf', 'anyOf', 'allOf'] as const
const TYPES = ['object', 'array', 'string', 'number', 'integer', 'boolean', 'null']
const FIELD_LABEL_KEYS: Record<string, string> = {
  notes: 'notes', frequency_hz: 'frequency_hz', duration_ms: 'duration_ms', gap_ms: 'gap_ms',
}

function tr(key: string, options?: Record<string, unknown>): string {
  return i18n.t(`commandSchema.${key}`, { ns: 'devices', ...options })
}

function typeName(type: string): string {
  return tr(`types.${type}`)
}

function knownFieldLabel(key: string): string | undefined {
  const labelKey = FIELD_LABEL_KEYS[key]
  return labelKey ? tr(`fields.${labelKey}`) : undefined
}
const ANNOTATIONS = new Set(['title', 'description', 'default', 'examples', 'unit', 'enumNames', '$schema', '$id', '$comment', '$defs', 'definitions', 'readOnly', 'writeOnly', 'deprecated'])

function object(v: unknown): v is Record<string, unknown> {
  return v !== null && typeof v === 'object' && !Array.isArray(v)
}

/** 空对象/纯注释 schema 不构成参数输入；无字段、无组合、无必填项的 object 也是无参数操作。 */
export function commandHasInput(schema?: Schema): boolean {
  if (!schema || !object(schema)) return false
  if (Object.keys(schema).filter((key) => !ANNOTATIONS.has(key)).length === 0) return false
  if (schema.type !== 'object') return true

  const properties = object(schema.properties) ? schema.properties : null
  if (properties && Object.keys(properties).length > 0) return true
  if (COMBINATORS.some((key) => schemaList(schema[key]))) return true
  if (Array.isArray(schema.required) && schema.required.length > 0) return true
  if ('patternProperties' in schema) return true
  if ('additionalProperties' in schema && schema.additionalProperties !== false) return true
  if (count(schema.minProperties) || count(schema.maxProperties)) return true
  return false
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

function isSchema(value: unknown): value is Schema | boolean {
  return typeof value === 'boolean' || object(value)
}
function schemaList(value: unknown): value is (Schema | boolean)[] {
  return Array.isArray(value) && value.length > 0 && value.every(isSchema)
}

// 静态反馈与动态匹配共用同一份支持范围，未知/无效约束不能被当作通过。
function supportedKeyword(key: string, value: unknown, schema: Schema): boolean {
  if (ANNOTATIONS.has(key)) return true
  switch (key) {
    case 'type': return types(value) !== null
    case 'enum': return Array.isArray(value)
    case 'const': return true
    case 'minimum': case 'maximum': case 'exclusiveMinimum': case 'exclusiveMaximum': return number(value)
    case 'multipleOf': return number(value) && value > 0
    case 'minLength': case 'maxLength': case 'minItems': case 'maxItems': case 'minProperties': case 'maxProperties': return count(value)
    case 'uniqueItems': return typeof value === 'boolean'
    case 'required': return Array.isArray(value) && value.every((v) => typeof v === 'string')
    case 'properties': return object(value) && Object.values(value).every(isSchema)
    case 'items': return isSchema(value) && !('prefixItems' in schema)
    case 'additionalProperties': return isSchema(value) && !('patternProperties' in schema)
    case 'oneOf': case 'anyOf': case 'allOf': return schemaList(value)
    default: return false
  }
}

/** 未实现或形状不合法的关键字，不冒充为已通过的约束。 */
export function unsupportedSchemaKeywords(schema: Schema): string[] {
  const unsupported = new Set<string>()
  const visit = (s: unknown) => {
    if (typeof s === 'boolean') return
    if (!object(s)) { unsupported.add('schema'); return }
    for (const [key, value] of Object.entries(s)) {
      if (!supportedKeyword(key, value, s)) unsupported.add(key)
      if (key === 'properties' && object(value)) Object.values(value).forEach(visit)
      else if ((key === 'oneOf' || key === 'anyOf' || key === 'allOf') && Array.isArray(value)) value.forEach(visit)
      else if ((key === 'items' || key === 'additionalProperties') && isSchema(value)) visit(value)
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
  return knownFieldLabel(key) ?? key
}

function validate(value: unknown, schema: unknown, at?: string): Validation {
  const root = at === undefined
  const path = at ?? tr('parameter')
  const fail = (why: string): Validation => ({ state: 'invalid', error: path + tr('pathSeparator') + why })
  if (schema === false) return fail(tr('validation.valueNotAllowed'))
  if (schema === true) return VALID
  if (!object(schema)) return UNKNOWN
  let uncertain = Object.entries(schema).some(([key, v]) => !supportedKeyword(key, v, schema))
  const declaredTypes = types(schema.type)
  if (declaredTypes && !declaredTypes.some((t) => matchesType(value, t))) {
    return fail(tr('validation.expectedType', {
      types: declaredTypes.map(typeName).join(tr('typeJoin')),
    }))
  }
  if (Array.isArray(schema.enum) && !schema.enum.some((v) => equal(value, v))) return fail(tr('validation.chooseAllowed'))
  if (Object.hasOwn(schema, 'const') && !equal(value, schema.const)) {
    return fail(tr('validation.mustEqual', { value: JSON.stringify(schema.const) }))
  }
  if (number(value)) {
    if (number(schema.minimum) && value < schema.minimum) return fail(tr('validation.minimum', { value: schema.minimum }))
    if (number(schema.maximum) && value > schema.maximum) return fail(tr('validation.maximum', { value: schema.maximum }))
    if (number(schema.exclusiveMinimum) && value <= schema.exclusiveMinimum) return fail(tr('validation.exclusiveMinimum', { value: schema.exclusiveMinimum }))
    if (number(schema.exclusiveMaximum) && value >= schema.exclusiveMaximum) return fail(tr('validation.exclusiveMaximum', { value: schema.exclusiveMaximum }))
    if (number(schema.multipleOf) && schema.multipleOf > 0) {
      const quotient = value / schema.multipleOf
      if (!Number.isFinite(quotient) || Math.abs(quotient - Math.round(quotient)) > Number.EPSILON * Math.max(1, Math.abs(quotient)) * 4) {
        return fail(tr('validation.multipleOf', { value: schema.multipleOf }))
      }
    }
  }
  if (typeof value === 'string') {
    const length = [...value].length // JSON Schema 的字符串长度是 Unicode 码点，不是传输字节数。
    if (count(schema.minLength) && length < schema.minLength) return fail(tr('validation.minLength', { count: schema.minLength }))
    if (count(schema.maxLength) && length > schema.maxLength) return fail(tr('validation.maxLength', { count: schema.maxLength }))
  }
  if (Array.isArray(value)) {
    if (count(schema.minItems) && value.length < schema.minItems) return fail(tr('validation.minItems', { count: schema.minItems }))
    if (count(schema.maxItems) && value.length > schema.maxItems) return fail(tr('validation.maxItems', { count: schema.maxItems }))
    if (schema.uniqueItems === true && value.some((v, i) => value.slice(0, i).some((other) => equal(v, other)))) {
      return fail(tr('validation.uniqueItems'))
    }
    if (Object.hasOwn(schema, 'items') && !('prefixItems' in schema)) {
      for (const [i, item] of value.entries()) {
        const result = validate(item, schema.items, at + '[' + i + ']')
        if (result.state === 'invalid') return result
        if (result.state === 'unknown') uncertain = true
      }
    }
  }
  if (object(value)) {
    const keys = Object.keys(value)
    const properties = object(schema.properties) ? schema.properties : {}
    if (Array.isArray(schema.required) && schema.required.every((k) => typeof k === 'string')) {
      for (const key of schema.required) {
        if (!Object.hasOwn(value, key)) {
          return fail(tr('validation.missingRequired', { field: propertyLabel(key, properties[key]) }))
        }
      }
    }
    if (count(schema.minProperties) && keys.length < schema.minProperties) return fail(tr('validation.minProperties', { count: schema.minProperties }))
    if (count(schema.maxProperties) && keys.length > schema.maxProperties) return fail(tr('validation.maxProperties', { count: schema.maxProperties }))
    for (const key of keys) {
      const label = propertyLabel(key, properties[key])
      const child = root ? label : path + ' / ' + label
      if (Object.hasOwn(properties, key)) {
        const result = validate(value[key], properties[key], child)
        if (result.state === 'invalid') return result
        if (result.state === 'unknown') uncertain = true
      } else if (Object.hasOwn(schema, 'additionalProperties') && !('patternProperties' in schema)) {
        if (schema.additionalProperties === false) return fail(tr('validation.unsupportedProperty', { key }))
        const result = validate(value[key], schema.additionalProperties, child)
        if (result.state === 'invalid') return result
        if (result.state === 'unknown') uncertain = true
      }
    }
  }
  for (const keyword of COMBINATORS) {
    const branches = schema[keyword]
    if (!schemaList(branches)) continue // 形状无效已标记 unknown，不能只执行其中一部分。
    const results = branches.map((branch) => validate(value, branch, at))
    const matches = results.filter((result) => result.state === 'valid').length
    const possible = matches + results.filter((result) => result.state === 'unknown').length
    if (keyword === 'allOf') {
      const failure = results.find((result) => result.state === 'invalid')
      if (failure) return failure
      if (matches !== results.length) uncertain = true
    } else if (keyword === 'anyOf') {
      if (possible === 0) return fail(tr('validation.anyOf'))
      if (matches === 0) uncertain = true
    } else {
      if (matches > 1) return fail(tr('validation.oneOfMultiple'))
      if (possible === 0) return fail(tr('validation.oneOfNone'))
      // 未知方案既不算匹配，也不算失败；只在所有可能性都不合法时拒绝。
      if (matches !== 1 || possible !== 1) uncertain = true
    }
  }
  return uncertain ? UNKNOWN : VALID
}

/** 用户可见文案：机器单位的传输错误只用于内部校验，不直接展示给普通用户。 */
export function commandArgsErrorCopy(error?: string): string | undefined {
  if (!error) return undefined
  if (/\d+ \u5b57\u8282\uff0c\u8d85\u8fc7 \d+ \u5b57\u8282\u4e0a\u9650/.test(error)) return tr('args.tooLong')
  return error
}

/** JSON 语法 + 已知 schema 约束 + 未改变的传输门禁。没有 schema 的原始参数不强制 JSON。 */
export function commandArgsError(args: string, schema?: Schema, maxBytes?: number): string | undefined {
  const wireError = argsError(args, maxBytes)
  return wireError ?? (schema ? schemaArgsError(args, schema) : undefined)
}

/** JSON/schema validation only; each action transport owns its byte/control-character limits. */
export function schemaArgsError(args: string, schema: Schema): string | undefined {
  if (!args.trim()) return tr('args.required')
  let value: unknown
  try {
    value = JSON.parse(args, (_key, v: unknown) => {
      if (typeof v === 'number' && !Number.isFinite(v)) throw new Error('non-finite JSON number')
      return v
    })
  } catch {
    return tr('args.invalidJson')
  }
  const result = validate(value, schema)
  return result.state === 'invalid' ? result.error : undefined
}

export interface CommandField {
  key: string
  label: string
  description?: string
  required: boolean
  type: 'string' | 'number' | 'integer' | 'boolean' | 'enum' | 'array' | 'object-rows'
  schema: Schema
  choices?: unknown[]
  /** 与 choices 同序的展示名；只影响下拉文案，不改下发值。 */
  choiceLabels?: string[]
  itemType?: 'string' | 'number' | 'integer'
  fields?: CommandField[]
  examples?: unknown[]
  minItems?: number
  maxItems?: number
  /** Declared default/const value. Presentation only; validation still uses the schema. */
  defaultValue?: unknown
}

export interface CommandFormChoice {
  key: string
  label: string
  fields: CommandField[]
}

export interface CommandForm {
  fields: CommandField[]
  choices?: CommandFormChoice[]
}

function containsCombinator(schema: unknown): boolean {
  if (!object(schema)) return false
  return COMBINATORS.some((key) => Object.hasOwn(schema, key))
    || (object(schema.properties) && Object.values(schema.properties).some(containsCombinator))
    || containsCombinator(schema.items) || containsCombinator(schema.additionalProperties)
}

/** 明确的小范围整数直接给选项，避免用户对着数字输入框猜合法值。
 *  只做 UI 呈现推导，不改变 schema 校验或下发类型。 */
function boundedIntegerChoices(prop: Schema): unknown[] | undefined {
  if (prop.type !== 'integer' || !number(prop.minimum) || !number(prop.maximum)) return undefined
  const min = prop.minimum
  const max = prop.maximum
  if (!Number.isInteger(min) || !Number.isInteger(max) || max < min || max - min > 9) return undefined
  return Array.from({ length: max - min + 1 }, (_, index) => min + index)
}

/** 仅把能无损表示的平铺标量对象变成字段；嵌套、数组、组合、未知约束保留 JSON。 */
export function commandFields(schema: Schema): CommandField[] | null {
  if (schema.type !== 'object' || !object(schema.properties) || containsCombinator(schema) || unsupportedSchemaKeywords(schema).length) return null
  const entries = Object.entries(schema.properties)
  if (!entries.length) return null
  if (Array.isArray(schema.required) && schema.required.some((k) => !Object.hasOwn(schema.properties as Schema, k))) return null
  const fields: CommandField[] = []
  for (const [key, prop] of entries) {
    if (!object(prop)) return null
    const declaredChoices = Array.isArray(prop.enum) && prop.enum.length > 0
      && prop.enum.every((v) => v === null || ['string', 'boolean', 'number'].includes(typeof v)) ? prop.enum : undefined
    const choices = declaredChoices ?? boundedIntegerChoices(prop)
    const choiceLabels = choices && Array.isArray(prop.enumNames)
      && prop.enumNames.length === choices.length
      && prop.enumNames.every((value) => typeof value === 'string' && value.trim())
      ? prop.enumNames.map((value) => (value as string).trim()) : undefined
    let type: CommandField['type'] = choices ? 'enum' : prop.type as CommandField['type']
    let itemType: CommandField['itemType']
    let nestedFields: CommandField[] | undefined
    if (type === 'array') {
      const items = object(prop.items) ? prop.items : null
      if (items?.type === 'object') {
        const nested = commandFields(items)
        if (!nested) return null
        type = 'object-rows'
        nestedFields = nested
      } else {
        const candidate = items?.type
        if (candidate !== 'string' && candidate !== 'number' && candidate !== 'integer') return null
        itemType = candidate
      }
    } else if (type !== 'string' && type !== 'number' && type !== 'integer' && type !== 'boolean' && type !== 'enum') return null
    const title = typeof prop.title === 'string' && prop.title.trim() ? prop.title : undefined
    const description = typeof prop.description === 'string' && prop.description.trim() ? prop.description : undefined
    const defaultValue = Object.hasOwn(prop, 'const') ? prop.const : undefined
    fields.push({ key, label: propertyLabel(key, prop), description: title && description !== title ? description : undefined,
      required: Array.isArray(schema.required) && schema.required.includes(key), type, schema: prop, choices, choiceLabels,
      itemType, fields: nestedFields, examples: Array.isArray(prop.examples) ? prop.examples : undefined,
      minItems: count(prop.minItems) ? prop.minItems : undefined, maxItems: count(prop.maxItems) ? prop.maxItems : undefined,
      defaultValue })
  }
  return fields
}

function branchFields(root: Schema, branch: Schema): CommandField[] | null {
  const rootProperties = object(root.properties) ? root.properties : {}
  const branchProperties = object(branch.properties) ? branch.properties : {}
  const rootRequired = Array.isArray(root.required) ? root.required.filter((key): key is string => typeof key === 'string') : []
  const branchRequired = Array.isArray(branch.required) ? branch.required.filter((key): key is string => typeof key === 'string') : []
  const keys = new Set([...rootRequired, ...branchRequired, ...Object.keys(branchProperties)])
  if (keys.size === 0) Object.keys(rootProperties).forEach((key) => keys.add(key))
  const properties = Object.fromEntries([...keys]
    .filter((key) => Object.hasOwn(rootProperties, key) || Object.hasOwn(branchProperties, key))
    .map((key) => [key, Object.hasOwn(branchProperties, key) ? branchProperties[key] : rootProperties[key]]))
  const required = [...new Set([...rootRequired, ...branchRequired])]
  const merged: Schema = { ...root, ...branch, properties, required }
  for (const key of COMBINATORS) delete merged[key]
  return commandFields(merged)
}

function branchLabel(root: Schema, branch: Schema, index: number): string {
  if (typeof branch.title === 'string' && branch.title.trim()) return branch.title
  const required = Array.isArray(branch.required) ? branch.required.filter((key): key is string => typeof key === 'string') : []
  const properties = object(root.properties) ? root.properties : {}
  const labels = required.map((key) => propertyLabel(key, properties[key]))
  return labels.length > 0 ? labels.join(' / ') : tr('args.fallbackChoice', { number: index + 1 })
}

/** 普通参数表单：支持平铺字段与根级 oneOf 的“设置方式”选择；其余复杂结构保留 JSON 回落。 */
export function commandForm(schema: Schema): CommandForm | null {
  const branches = schema.oneOf
  const hasOtherCombinator = Object.hasOwn(schema, 'anyOf') || Object.hasOwn(schema, 'allOf')
  if (Array.isArray(branches) && branches.length > 0 && !hasOtherCombinator) {
    const choices: CommandFormChoice[] = []
    for (const [index, branch] of branches.entries()) {
      if (!object(branch)) return null
      const fields = branchFields(schema, branch)
      if (!fields || fields.length === 0) return null
      choices.push({ key: String(index), label: branchLabel(schema, branch, index), fields })
    }
    return { fields: [], choices }
  }
  const fields = commandFields(schema)
  if (fields) return { fields }
  const emptyObject = (schema.type === undefined || schema.type === 'object')
    && (schema.properties === undefined || (object(schema.properties) && Object.keys(schema.properties).length === 0))
    && (schema.required === undefined || (Array.isArray(schema.required) && schema.required.length === 0))
    && schema.additionalProperties !== true && !object(schema.additionalProperties)
  if (!containsCombinator(schema) && unsupportedSchemaKeywords(schema).length === 0 && emptyObject) {
    return { fields: [] }
  }
  return null
}


/** Strict form fields still need user input only when a required field has no declared default/const. */
export function commandHasMeaningfulInput(schema?: Schema): boolean {
  if (!commandHasInput(schema)) return false
  const form = schema ? commandForm(schema) : null
  if (!form) return true
  const fields = form.choices ? form.choices.flatMap((choice) => choice.fields) : form.fields
  return fields.some((field) => field.required && field.defaultValue === undefined)
}
