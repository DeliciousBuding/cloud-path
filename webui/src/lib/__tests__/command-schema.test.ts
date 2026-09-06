import { describe, expect, it } from 'vitest'
import { commandArgsError, commandFields, unsupportedSchemaKeywords } from '../command-schema'

const objectSchema = {
  type: 'object', required: ['level', 'enabled'], additionalProperties: false,
  properties: { level: { type: 'integer', minimum: 0, maximum: 10 }, enabled: { type: 'boolean' } },
}

describe('命令参数 JSON 与类型契约', () => {
  it.each(['', ' ', '{', '{"level":1,}', '{level:1}', 'undefined', 'NaN', '1e999'])('拒绝缺失或不可解析的 JSON：%j', (args) => {
    expect(commandArgsError(args, {})).toBeDefined()
  })
  it('没有 schema 的 legacy 参数仍可为原始字符串，且不修改原文', () => {
    expect(commandArgsError('legacy argument {')).toBeUndefined()
    expect(commandArgsError('')).toBeUndefined()
    expect(commandArgsError(' { "level":0,"enabled":false } ', objectSchema)).toBeUndefined()
  })
  it.each([
    ['object', '{}', '[]'], ['array', '[]', '{}'], ['integer', '0', '1.5'],
    ['number', '1.5', '"1.5"'], ['boolean', 'false', '0'], ['string', '"0"', '0'], ['null', 'null', '"null"'],
  ])('声明 %s 类型而非宽松类型转换', (type, valid, invalid) => {
    expect(commandArgsError(valid, { type })).toBeUndefined()
    expect(commandArgsError(invalid, { type })).toContain('类型')
  })
  it('已知联合类型可以校验，但不假装是单一标量字段', () => {
    expect(commandArgsError('null', { type: ['string', 'null'] })).toBeUndefined()
    expect(commandArgsError('false', { type: ['string', 'null'] })).toContain('类型')
    expect(commandFields({ type: 'object', properties: { value: { type: ['string', 'null'] } } })).toBeNull()
  })
  it('缺必填、额外参数、错误类型均被拒绝；0/false 是已填写值', () => {
    expect(commandArgsError('{}', objectSchema)).toContain('缺少必填参数 level')
    expect(commandArgsError('{"level":0}', objectSchema)).toContain('缺少必填参数 enabled')
    expect(commandArgsError('{"level":0,"enabled":"false"}', objectSchema)).toContain('类型')
    expect(commandArgsError('{"level":0,"enabled":false,"extra":1}', objectSchema)).toContain('未声明的参数 extra')
    expect(commandArgsError('{"level":0,"enabled":false}', objectSchema)).toBeUndefined()
  })
  it('required 不依赖 properties，原型属性也不能冒充已填写参数', () => {
    expect(commandArgsError('{}', { type: 'object', required: ['offset'] })).toContain('offset')
    expect(commandArgsError('{}', { type: 'object', required: ['toString'] })).toContain('toString')
    expect(commandArgsError('{"__proto__":1}', { type: 'object', required: ['__proto__'] })).toBeUndefined()
  })
})

describe('数值、字符串、数组与对象的声明边界', () => {
  it.each([
    [{ type: 'number', minimum: -1, maximum: 2 }, '-1', '-2'],
    [{ type: 'number', minimum: -1, maximum: 2 }, '2', '3'],
    [{ type: 'number', exclusiveMinimum: 0 }, '0.1', '0'],
    [{ type: 'number', exclusiveMaximum: 1 }, '0.9', '1'],
    [{ type: 'number', multipleOf: 0.1 }, '0.3', '0.31'],
    [{ type: 'string', minLength: 1 }, '"x"', '""'],
    [{ type: 'string', maxLength: 1 }, '"😀"', '"😀a"'],
    [{ type: 'array', minItems: 1 }, '[0]', '[]'],
    [{ type: 'array', maxItems: 1 }, '[0]', '[0,1]'],
    [{ type: 'array', uniqueItems: true }, '[0,false]', '[1,1]'],
    [{ type: 'object', minProperties: 1 }, '{"x":0}', '{}'],
    [{ type: 'object', maxProperties: 1 }, '{"x":0}', '{"x":0,"y":0}'],
  ])('准确检查边界 %j', (schema, valid, invalid) => {
    expect(commandArgsError(valid, schema)).toBeUndefined()
    expect(commandArgsError(invalid, schema)).toBeDefined()
  })
  it('enum / const 保持 JSON 类型，结构值比较不受对象键顺序影响', () => {
    expect(commandArgsError('0', { enum: ['0', false] })).toContain('枚举')
    expect(commandArgsError('false', { enum: ['0', false] })).toBeUndefined()
    expect(commandArgsError('{"b":2,"a":1}', { const: { a: 1, b: 2 } })).toBeUndefined()
    expect(commandArgsError('[]', { const: {} })).toContain('必须等于')
    expect(commandArgsError('[{"a":1,"b":2},{"b":2,"a":1}]', { uniqueItems: true })).toContain('不能重复')
  })
  it('复杂字段使用 JSON 但仍递归校验已知 required、类型与数组项', () => {
    const schema = { type: 'object', required: ['rows'], properties: {
      rows: { type: 'array', minItems: 1, items: { type: 'object', required: ['n'], properties: { n: { type: 'integer', minimum: 1 } } } },
    } }
    expect(commandFields(schema)).toBeNull()
    expect(commandArgsError('{"rows":[{}]}', schema)).toContain('rows[0]：缺少必填参数 n')
    expect(commandArgsError('{"rows":[{"n":0}]}', schema)).toContain('不能小于 1')
    expect(commandArgsError('{"rows":[{"n":1}]}', schema)).toBeUndefined()
  })
  it('additionalProperties 的子 schema 以及 boolean schema 不被忽略', () => {
    expect(commandArgsError('{"x":"a"}', { additionalProperties: { type: 'number' } })).toContain('类型')
    expect(commandArgsError('{"x":1}', { additionalProperties: { type: 'number' } })).toBeUndefined()
    expect(commandArgsError('[1]', { items: false })).toContain('不允许此值')
    expect(commandArgsError('{"x":1}', { properties: { x: false } })).toContain('不允许此值')
  })
  it('JSON 的逻辑字符约束与原始 UTF-8 传输长度是两道独立门禁', () => {
    expect(commandArgsError('"😀"', { type: 'string', maxLength: 1 })).toBeUndefined()
    expect(commandArgsError(JSON.stringify('汉'.repeat(21)), { type: 'string', maxLength: 30 })).toContain('65 UTF-8 字节')
    expect(commandArgsError('{\n"n":1}', { type: 'object' })).toContain('换行')
    expect(commandArgsError('"\\n"', { type: 'string', maxLength: 1 })).toBeUndefined()
  })
})

describe('字段选择与诚实的 JSON 回落', () => {
  it('title → description → key，保留必填和控件类型而不消费默认值', () => {
    const fields = commandFields({ type: 'object', required: ['n'], properties: {
      n: { type: 'integer', title: '数量', description: '单位数量', default: 99 },
      mode: { enum: ['a', 'b'], description: '模式' },
      enabled: { type: 'boolean', default: true },
    } })
    expect(fields).toMatchObject([
      { key: 'n', label: '数量', description: '单位数量', type: 'integer', required: true },
      { key: 'mode', label: '模式', type: 'enum', choices: ['a', 'b'], required: false },
      { key: 'enabled', label: 'enabled', type: 'boolean', required: false },
    ])
  })
  it.each([
    {}, { type: 'object' }, { type: 'object', required: ['unlisted'], properties: { x: { type: 'string' } } },
    { type: 'object', properties: { nested: { type: 'object' } } },
    { type: 'object', properties: { values: { type: 'array' } } },
    { type: 'object', properties: { opaque: {} } },
  ])('不能安全变成字段的 schema 保留 JSON：%j', (schema) => {
    expect(commandFields(schema)).toBeNull()
  })
  it('引用、组合、条件、pattern/format 和未知扩展显式列出；不拼凑分支语义', () => {
    const schema = { type: 'object', required: ['id'], properties: { id: { type: 'string', pattern: '^x', format: 'uuid' } },
      oneOf: [{ required: ['a'] }, { required: ['b'] }], $ref: '#/$defs/example', 'vendor-check': true }
    expect(unsupportedSchemaKeywords(schema)).toEqual(['$ref', 'format', 'oneOf', 'pattern', 'vendor-check'])
    expect(commandFields(schema)).toBeNull()
    expect(commandArgsError('{}', schema)).toContain('缺少必填参数 id')
    expect(commandArgsError('{"id":"x"}', schema)).toBeUndefined()
  })
  it('未知类型和非法约束值不伪造语义，注释/default 不是校验错误', () => {
    const schema = { type: 'opaque', minLength: '2', default: 'hello', title: '标题', $defs: { x: { type: 'number' } } }
    expect(unsupportedSchemaKeywords(schema)).toEqual(['minLength', 'type'])
    expect(commandArgsError('1', schema)).toBeUndefined()
  })
  it('未实现 prefixItems / patternProperties 时不错误套用 items / additionalProperties', () => {
    const tuple = { prefixItems: [{ type: 'string' }], items: false }
    expect(unsupportedSchemaKeywords(tuple)).toEqual(['items', 'prefixItems'])
    expect(commandArgsError('["x"]', tuple)).toBeUndefined()
    const pattern = { patternProperties: { '^x': { type: 'number' } }, additionalProperties: false }
    expect(unsupportedSchemaKeywords(pattern)).toEqual(['additionalProperties', 'patternProperties'])
    expect(commandArgsError('{"x":1}', pattern)).toBeUndefined()
  })
})

it('错误沿用字段展示名，无展示声明时保留真实字段名', () => {
  const schema = { type: 'object', required: ['level'], properties: { level: { type: 'integer', title: '输出强度', maximum: 10 } } }
  expect(commandArgsError('{}', schema)).toBe('参数：缺少必填参数 输出强度')
  expect(commandArgsError('{"level":11}', schema)).toBe('输出强度：不能大于 10')
  expect(commandArgsError('{"level":"1"}', schema)).toBe('输出强度：需要整数类型')
  expect(commandArgsError('{"raw_key":11}', { type: 'object', properties: { raw_key: { type: 'number', maximum: 10 } } })).toBe('raw_key：不能大于 10')
})
