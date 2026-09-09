import { emptyRecordValue, recordEntries, recordFieldLabel, recordTimestamp } from '@/lib/application-plane'

/** 应用内容是数据，不是可执行展示代码；普通视图保留字段身份与结构。 */
export function StructuredValue({ value, depth = 0, omitKeys = [] }: {
  value: unknown; depth?: number; omitKeys?: readonly string[]
}) {
  if (value === null || value === '') return <span className="text-ink-3">未填写</span>
  if (typeof value !== 'object') {
    const time = typeof value === 'string' ? recordTimestamp(value) : undefined
    return time && typeof value === 'string'
      ? <time dateTime={value} title={value} className="num break-words">{time}</time>
      : <span className="whitespace-pre-wrap break-words [overflow-wrap:anywhere]">{
        typeof value === 'boolean' ? value ? '是' : '否' : String(value)
      }</span>
  }
  const entries = recordEntries(value).filter(([key]) => !omitKeys.includes(key))
  if (!entries.length) return <span className="text-ink-3">{omitKeys.length ? '暂无更多内容' : '暂无内容'}</span>
  if (depth >= 2) return <span className="text-ink-3">{entries.length} 项嵌套内容，详见技术详情</span>
  const preview = Array.isArray(value) ? entries.slice(0, 6) : entries.filter(([, item]) => !emptyRecordValue(item)).slice(0, 6)
  const shown = new Set(preview.map(([key]) => key))
  const rest = entries.filter(([key]) => !shown.has(key))
  const field = ([key, item]: [string, unknown]) => <div key={key} className="min-w-0">
    <dt className="break-words text-meta text-ink-3 [overflow-wrap:anywhere]" title={key}>{Array.isArray(value) ? '第 ' + (Number(key) + 1) + ' 项' : recordFieldLabel(key)}</dt>
    <dd className="mt-1 min-w-0 text-body leading-relaxed text-ink-2"><StructuredValue value={item} depth={depth + 1} /></dd>
  </div>
  return <div className="min-w-0">
    {preview.length ? <dl className={depth === 0 ? 'grid min-w-0 gap-x-8 gap-y-4 sm:grid-cols-2' : 'grid min-w-0 gap-3'}>{preview.map(field)}</dl>
      : <p className="text-body text-ink-3">暂无已填写内容</p>}
    {rest.length > 0 && <details className="mt-4 min-w-0 border-t border-hairline pt-3">
      <summary className="flex min-h-touch cursor-pointer items-center text-meta text-ink-2">其余字段（{rest.length}）</summary>
      <dl className="mt-3 grid min-w-0 gap-x-8 gap-y-4 sm:grid-cols-2">{rest.slice(0, 40).map(field)}</dl>
      {rest.length > 40 && <p className="mt-3 text-meta text-ink-3">另有 {rest.length - 40} 项，完整内容见技术详情。</p>}
    </details>}
  </div>
}
