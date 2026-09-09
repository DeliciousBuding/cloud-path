import { describe, expect, it } from 'vitest'
import { commandArgsError, commandFields, commandForm, unsupportedSchemaKeywords } from '../command-schema'

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
    expect(commandArgsError('{"level":0,"enabled":false,"extra":1}', objectSchema)).toContain('不支持的参数 extra')
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
    expect(commandArgsError('0', { enum: ['0', false] })).toContain('请选择允许的值')
    expect(commandArgsError('false', { enum: ['0', false] })).toBeUndefined()
    expect(commandArgsError('{"b":2,"a":1}', { const: { a: 1, b: 2 } })).toBeUndefined()
    expect(commandArgsError('[]', { const: {} })).toContain('必须等于')
    expect(commandArgsError('[{"a":1,"b":2},{"b":2,"a":1}]', { uniqueItems: true })).toContain('不能重复')
  })
  it('复杂字段使用 JSON 但仍递归校验已知 required、类型与数组项', () => {
    const schema = { type: 'object', required: ['rows'], properties: {
      rows: { type: 'array', minItems: 1, items: { type: 'object', required: ['n'], properties: { n: { type: 'integer', minimum: 1 } } } },
    } }
    expect(commandFields(schema)?.[0]).toMatchObject({ key: 'rows', type: 'object-rows', minItems: 1, fields: [{ key: 'n', type: 'integer', required: true }] })
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
    expect(commandArgsError(JSON.stringify('汉'.repeat(21)), { type: 'string', maxLength: 30 })).toContain('65 字节')
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
  it('引用、pattern/format 和未知扩展仍显式列出，组合内已知约束独立生效', () => {
    const schema = { type: 'object', required: ['id'], properties: { id: { type: 'string', pattern: '^x', format: 'uuid' } },
      oneOf: [{ required: ['a'] }, { required: ['b'] }], $ref: '#/$defs/example', 'vendor-check': true }
    expect(unsupportedSchemaKeywords(schema)).toEqual(['$ref', 'format', 'pattern', 'vendor-check'])
    expect(commandFields(schema)).toBeNull()
    expect(commandArgsError('{}', schema)).toContain('缺少必填参数 id')
    expect(commandArgsError('{"id":"x","a":1}', schema)).toBeUndefined()
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

// Shapes supplied by real Driver descriptors; command names never enter the validator.
const ledSchema = {
  type: 'object', properties: {
    mask: { type: 'integer', minimum: 0, maximum: 255 },
    pattern: { type: 'integer', minimum: 0, maximum: 9 },
  }, oneOf: [{ required: ['mask'] }, { required: ['pattern'] }],
}
const displaySchema = {
  type: 'object', properties: {
    digits: { type: 'array', minItems: 8, maxItems: 8, items: { type: 'integer', minimum: 0, maximum: 9 } },
    codes: { type: 'array', minItems: 8, maxItems: 8, items: { type: 'integer', minimum: 0, maximum: 25 } },
    mode: { enum: ['clock'] },
  }, oneOf: [{ required: ['digits'] }, { required: ['codes'] }, { required: ['mode'] }],
}

describe('组合参数组合', () => {
  it('LED oneOf 只接受一个方案，缺省和双参数都不能下发', () => {
    for (const value of [{ mask: 0 }, { mask: 255 }, { pattern: 0 }, { pattern: 9 }]) {
      expect(commandArgsError(JSON.stringify(value), ledSchema)).toBeUndefined()
    }
    expect(commandArgsError('{}', ledSchema)).toContain('请选择一种设置方式')
    expect(commandArgsError('{"mask":1,"pattern":2}', ledSchema)).toContain('请只选择一种设置方式')
    for (const value of [{ mask: -1 }, { mask: 256 }, { mask: 1.5 }, { mask: '1' }, { pattern: 10 }]) {
      expect(commandArgsError(JSON.stringify(value), ledSchema)).toBeDefined()
    }
  })
  it('display 三种方案分别可发，零方案和每一对组合都拒绝', () => {
    const digits = { digits: [0, 1, 2, 3, 4, 5, 6, 9] }
    const codes = { codes: [0, 1, 2, 3, 4, 5, 6, 25] }
    const mode = { mode: 'clock' }
    for (const value of [digits, codes, mode]) expect(commandArgsError(JSON.stringify(value), displaySchema)).toBeUndefined()
    for (const value of [{}, { ...digits, ...codes }, { ...digits, ...mode }, { ...codes, ...mode }]) {
      const args = JSON.stringify(value)
      expect(new TextEncoder().encode(args).length).toBeLessThanOrEqual(64)
      expect(commandArgsError(args, displaySchema)).toContain('选择一种设置方式')
    }
    for (const value of [{ digits: Array(7).fill(0) }, { digits: Array(9).fill(0) },
      { digits: Array(8).fill(10) }, { codes: Array(8).fill(26) }, { codes: Array(8).fill(0.5) }, { mode: 'other' }]) {
      expect(commandArgsError(JSON.stringify(value), displaySchema)).toBeDefined()
    }
  })
  it.each([
    ['oneOf', [true, false], true], ['oneOf', [true, true], false], ['oneOf', [false, false], false], ['oneOf', [{}], true],
    ['anyOf', [false, true], true], ['anyOf', [false, false], false], ['anyOf', [true, true], true],
    ['allOf', [true, {}], true], ['allOf', [true, false], false], ['allOf', [false, false], false],
  ] as const)('%s 接受布尔子 schema：%j', (keyword, branches, valid) => {
    const schema = { [keyword]: branches }
    expect(unsupportedSchemaKeywords(schema)).toEqual([])
    expect(commandArgsError('0', schema) === undefined).toBe(valid)
  })
  it('三种组合可以彼此嵌套，并继续递归到 properties/items', () => {
    const choice = { oneOf: [
      { allOf: [{ type: 'integer' }, { minimum: 0 }] },
      { anyOf: [{ const: 'auto' }, { const: false }] },
    ] }
    const schema = { type: 'object', properties: { values: { type: 'array', items: choice } } }
    expect(commandArgsError('{"values":[0,"auto",false]}', schema)).toBeUndefined()
    for (const args of ['{"values":[-1]}', '{"values":[0.5]}', '{"values":[null]}']) {
      expect(commandArgsError(args, schema)).toContain('values[0]')
    }
    expect(commandArgsError('2', { allOf: [{ minimum: 1 }, { maximum: 3 }] })).toBeUndefined()
    expect(commandArgsError('4', { allOf: [{ minimum: 1 }, { maximum: 3 }] })).toContain('不能大于 3')
  })
  it.each(['oneOf', 'anyOf', 'allOf'])('%s 的形状无效时明确未校验，不猜测或部分执行方案', (keyword) => {
    for (const branches of [null, true, {}, 'schema', [], [null], [false, 1], [true, true, null], [false, []]]) {
      const schema = { [keyword]: branches }
      expect(unsupportedSchemaKeywords(schema)).toContain(keyword)
      expect(commandArgsError('0', schema)).toBeUndefined()
      expect(commandArgsError('"bad"', { type: 'number', ...schema })).toContain('数值类型')
    }
  })
  it('保留 UTF-8/JSON 门禁与原文，不从 default 补出一个方案', () => {
    const before = structuredClone(ledSchema)
    const raw = '{"mask":0}' + ' '.repeat(54)
    expect(new TextEncoder().encode(raw)).toHaveLength(64)
    expect(commandArgsError(raw, ledSchema)).toBeUndefined()
    expect(commandArgsError(raw + ' ', ledSchema, 1024)).toContain('64 字节上限')
    expect(commandArgsError('{', ledSchema)).toContain('参数格式无效')
    expect(commandArgsError('{\n"mask":0}', ledSchema)).toContain('换行')
    expect(commandArgsError(JSON.stringify('汉'.repeat(21)), { anyOf: [{ type: 'string' }, false] })).toContain('65 字节')
    expect(commandArgsError('{}', { ...ledSchema, default: { mask: 0 } })).toContain('请选择一种设置方式')
    expect(ledSchema).toEqual(before)
  })
})

describe('组合中的未知约束不冒充匹配或不匹配', () => {
  it.each([
    { oneOf: [{ type: 'string', pattern: '^a' }, { type: 'string', pattern: '^b' }] },
    { oneOf: [{ type: 'string' }, { type: 'string', pattern: '^b' }] },
    { oneOf: [{ $ref: '#/$defs/value' }, false] },
    { anyOf: [{ type: 'string', format: 'email' }, { type: 'number' }] },
    { oneOf: [{ anyOf: [{ type: 'string', pattern: '^a' }, false] }, true] },
    { oneOf: [{ allOf: [true, { pattern: '^a' }] }, true] },
    { oneOf: [{ oneOf: [true, { pattern: '^a' }] }, false] },
    { anyOf: [{ allOf: [true, { oneOf: [false, { pattern: '^a' }] }] }, false] },
  ])('无法确定的子方案必须保留 JSON 可用回落：%j', (schema) => {
    expect(commandArgsError('"apple"', schema)).toBeUndefined()
    expect(unsupportedSchemaKeywords(schema).length).toBeGreaterThan(0)
  })
  it('嵌套 properties/items 的未知性也必须上传，不能误算多个匹配', () => {
    const properties = { oneOf: [{ properties: { v: { pattern: '^a' } } }, true] }
    expect(commandArgsError('{"v":"apple"}', properties)).toBeUndefined()
    expect(commandArgsError('["apple"]', { oneOf: [{ items: { format: 'email' } }, true] })).toBeUndefined()
    expect(commandArgsError('{"v":"apple"}', { oneOf: [{ additionalProperties: { $ref: '#/$defs/value' } }, true] })).toBeUndefined()
    // 未出现的属性不参与校验；这里两个方案都确定通过，应拒绝。
    expect(commandArgsError('{}', properties)).toContain('选择一种设置方式')
  })
  it('已确定的失败和匹配数仍然生效，未知约束不能遮蔽它们', () => {
    const unknown = { $ref: '#/$defs/value' }
    expect(commandArgsError('0', { oneOf: [true, true, unknown] })).toContain('选择一种设置方式')
    expect(commandArgsError('0', { anyOf: [false, { type: 'string', ...unknown }] })).toContain('请至少选择一种设置方式')
    expect(commandArgsError('0', { allOf: [false, unknown] })).toBeDefined()
    // anyOf 有一个确定匹配即可确定通过，即使另一个方案未知。
    expect(commandArgsError('0', { oneOf: [{ anyOf: [true, unknown] }, true] })).toContain('选择一种设置方式')
    // allOf 有一个确定失败即可确定失败，不把它误算为可能匹配。
    expect(commandArgsError('0', { oneOf: [{ allOf: [false, unknown] }, true] })).toBeUndefined()
  })
  it('未知关键字从任意组合深度继续列出，已支持组合本身不再列为未知', () => {
    expect(unsupportedSchemaKeywords({ oneOf: [
      { properties: { v: { $ref: '#/$defs/value' } } },
      { anyOf: [{ format: 'email' }, { allOf: [{ pattern: '^x' }, { 'vendor-check': true }] }] },
    ] })).toEqual(['$ref', 'format', 'pattern', 'vendor-check'])
  })
  it.each(['oneOf', 'anyOf', 'allOf'])('%s 即使完全可校验，字段 UI 也不展开组合或嵌套组合', (keyword) => {
    const flat = { type: 'object', properties: { n: { type: 'integer' } } }
    const root = { ...flat, [keyword]: [{ required: ['n'] }] }
    expect(unsupportedSchemaKeywords(root)).toEqual([])
    expect(commandFields(root)).toBeNull()
    expect(commandFields({ ...flat, properties: { n: { type: 'integer', [keyword]: [true] } } })).toBeNull()
    expect(commandFields({ ...flat, additionalProperties: { [keyword]: [true] } })).toBeNull()
    expect(commandFields(ledSchema)).toBeNull()
    expect(commandFields(displaySchema)).toBeNull()
    expect(commandFields(flat)).not.toBeNull()
  })
})

describe('普通参数表单模型', () => {
  it('平铺对象直接生成字段，数组字段生成可读输入', () => {
    const form = commandForm({
      type: 'object', required: ['digits'],
      properties: {
        digits: { type: 'array', items: { type: 'integer', minimum: 0, maximum: 9 }, minItems: 8, maxItems: 8, title: '8 位数字' },
      },
    })
    expect(form?.choices).toBeUndefined()
    expect(form?.fields).toHaveLength(1)
    expect(form?.fields[0]).toMatchObject({ key: 'digits', type: 'array', itemType: 'integer', minItems: 8, maxItems: 8, required: true })
  })

  it('根级 oneOf 生成设置方式，每个方式只包含自己的字段', () => {
    const form = commandForm({
      type: 'object',
      properties: {
        mask: { type: 'integer', title: '灯位掩码' },
        pattern: { type: 'integer', title: '兼容档' },
      },
      oneOf: [{ required: ['mask'] }, { required: ['pattern'] }],
    })
    expect(form?.choices?.map((choice) => choice.label)).toEqual(['灯位掩码', '兼容档'])
    expect(form?.choices?.[0]?.fields.map((field) => field.key)).toEqual(['mask'])
    expect(form?.choices?.[1]?.fields.map((field) => field.key)).toEqual(['pattern'])
  })

  it('数码管三选一保留数组和枚举字段，不退回 JSON', () => {
    const form = commandForm({
      type: 'object',
      properties: {
        digits: { type: 'array', items: { type: 'integer' }, minItems: 8, maxItems: 8, title: '8 位数字' },
        codes: { type: 'array', items: { type: 'integer' }, minItems: 8, maxItems: 8, title: '8 位字形码' },
        mode: { type: 'string', enum: ['clock'], title: '恢复时钟' },
      },
      oneOf: [{ required: ['digits'] }, { required: ['codes'] }, { required: ['mode'] }],
    })
    expect(form?.choices).toHaveLength(3)
    expect(form?.choices?.map((choice) => choice.fields[0]?.type)).toEqual(['array', 'array', 'enum'])
  })
})
