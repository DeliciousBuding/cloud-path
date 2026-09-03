export type CnArg = string | false | null | undefined | Record<string, boolean>

export function cn(...xs: CnArg[]): string {
  const out: string[] = []
  for (const x of xs) {
    if (!x) continue
    if (typeof x === 'string') { out.push(x); continue }
    for (const [k, v] of Object.entries(x)) if (v) out.push(k)
  }
  return out.join(' ')
}
