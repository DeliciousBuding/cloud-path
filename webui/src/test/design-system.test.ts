// @vitest-environment node
// 设计系统守卫：把「验收时手工跑一遍」的检查固化成可重复执行的回归测试。
// 只读源码文件，不需要 DOM，故跑在 node 环境（import.meta.url 才是 file: 协议）。
//   ① 组件里不许出现裸色值（hex / rgb()）与 Tailwind 默认调色板 —— 颜色只走 index.css 的 token
//   ② light / dark 两份 token 必须齐，且深色不得声明浅色没有的孤儿 token
//   ③ prefers-reduced-motion 的 CSS 兜底不得被删（JS 侧只管条件类名）
//   ④ 390px：组件不许写死像素宽度；固定宽度必须配视口相对上限
import { readdirSync, readFileSync } from 'node:fs'
import { join, relative } from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'

const SRC_DIR = fileURLToPath(new URL('..', import.meta.url))

function walk(dir: string, out: string[] = []): string[] {
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const full = join(dir, entry.name)
    if (entry.isDirectory()) walk(full, out)
    else out.push(full)
  }
  return out
}

const tsx = walk(SRC_DIR)
  .filter((f) => f.endsWith('.tsx'))
  .map((f) => ({ path: relative(SRC_DIR, f).replaceAll('\\', '/'), text: readFileSync(f, 'utf8') }))

const css = readFileSync(join(SRC_DIR, 'index.css'), 'utf8')
const source = (name: string): string =>
  readFileSync(join(SRC_DIR, name), 'utf8')

/** 取出某个 CSS 块的完整文本（含大括号配对） */
function blockOf(text: string, header: string): string {
  const start = text.indexOf(header)
  if (start < 0) return ''
  let depth = 0
  for (let i = text.indexOf('{', start); i < text.length; i++) {
    if (text[i] === '{') depth++
    else if (text[i] === '}') {
      depth--
      if (depth === 0) return text.slice(start, i + 1)
    }
  }
  return ''
}

function colorTokens(block: string): string[] {
  return [...block.matchAll(/--color-([a-z0-9-]+)\s*:/g)].map((m) => m[1] as string)
}

describe('颜色只走 token', () => {
  it('扫描面非空（防止路径漂移导致守卫空转）', () => {
    expect(tsx.length).toBeGreaterThan(20)
    expect(tsx.some((f) => f.path.endsWith('SchemaRenderer.tsx'))).toBe(true)
  })

  it('组件内没有裸 hex / rgb() 色值', () => {
    const offenders = tsx
      .flatMap((f) => [...f.text.matchAll(/#[0-9a-fA-F]{3,8}|rgb\(/g)].map((m) => `${f.path}: ${m[0]}`))
    expect(offenders).toEqual([])
  })

  it('组件内不用 Tailwind 默认调色板（含 white/black），一律用项目 token', () => {
    const palette = /(?:^|[\s'"`])(?:bg|text|border|divide|ring|fill|stroke|from|via|to|shadow|outline|decoration|caret)-(?:white|black|red|blue|green|gray|grey|slate|zinc|yellow|orange|purple|pink|amber|emerald|cyan|teal|indigo|violet|fuchsia|rose|sky|lime|stone|neutral)(?:[-/\s'"`]|$)/g
    const offenders = tsx.flatMap((f) => [...f.text.matchAll(palette)].map((m) => `${f.path}: ${m[0].trim()}`))
    expect(offenders).toEqual([])
  })

  it('图表颜色走 CSS 变量，并且动画恒关（媒体查询管不到 recharts 的 JS 动画）', () => {
    const chart = source('components/TrendChart.tsx')
    expect(chart).toContain('var(--color-accent)')
    expect(chart).toContain('isAnimationActive={false}')
    expect(chart).not.toMatch(/#[0-9a-fA-F]{3,8}/)
  })
})

describe('light / dark token 对齐', () => {
  const light = colorTokens(blockOf(css, '@theme'))
  const dark = colorTokens(blockOf(css, '.dark'))

  it('浅色主题声明了全套语义 token', () => {
    for (const t of ['canvas', 'surface', 'surface-2', 'ink', 'ink-2', 'ink-3', 'hairline',
      'accent', 'accent-ink', 'ok', 'warn', 'bad', 'idle']) {
      expect(light, `@theme 缺少 --color-${t}`).toContain(t)
    }
  })

  it('深色主题覆盖所有会随主题变化的 token（否则 Login/Setup 与 SchemaRenderer 会在深色下掉色）', () => {
    // accent-ink（白字压强调色底）与 idle（中性灰）在两个主题下同值，故意不重复声明
    const themed = ['canvas', 'surface', 'surface-2', 'ink', 'ink-2', 'ink-3', 'hairline', 'accent', 'ok', 'warn', 'bad']
    for (const t of themed) expect(dark, `.dark 缺少 --color-${t}`).toContain(t)
  })

  it('深色不声明浅色没有的孤儿 token（拼写漂移会静默失效）', () => {
    expect(dark.filter((t) => !light.includes(t))).toEqual([])
  })

  it('强调色前景 token 在两主题下都可用（认证页/按钮依赖它）', () => {
    expect(light).toContain('accent-ink')
    expect(css).toContain('.btn-primary { background: var(--color-accent); color: var(--color-accent-ink); }')
  })
})

describe('减少动效偏好的 CSS 兜底', () => {
  const reduced = blockOf(css, '@media (prefers-reduced-motion: reduce)')

  it('全局把动画/过渡压到 0.001ms 并停掉循环', () => {
    expect(reduced).toContain('animation-duration: .001ms !important')
    expect(reduced).toContain('animation-iteration-count: 1 !important')
    expect(reduced).toContain('transition-duration: .001ms !important')
    expect(reduced).toContain('scroll-behavior: auto !important')
  })

  it('悬停位移也被中和（卡片 lift 不能绕过偏好）', () => {
    expect(reduced).toContain('.card-lift:hover { transform: none; }')
  })

  it('组件不写内联 animation/transition（会盖过媒体查询的 !important 兜底）', () => {
    const offenders = tsx
      .flatMap((f) => [...f.text.matchAll(/style=\{\{[^}]*(?:animation|transition)[^}]*\}\}/g)]
        .map((m) => `${f.path}: ${m[0]}`))
    expect(offenders).toEqual([])
  })
})

describe('390px 溢出收口（静态守卫）', () => {
  it('组件不写死像素/固定宽度（max-w-* 是上限，允许且鼓励）', () => {
    // 负向后顾排除 max-w-[9rem] 这类「宽度上限」，只抓 w-[320px] / min-w-[12rem] 这类硬宽度
    const fixed = /(?<![-\w])(?:w|min-w)-\[\d+(?:px|rem|vw|em)\]/g
    const offenders = tsx.flatMap((f) => [...f.text.matchAll(fixed)].map((m) => `${f.path}: ${m[0]}`))
    expect(offenders).toEqual([])
  })

  it('内联样式只允许百分比宽度（量程条），不许出现像素宽度', () => {
    const offenders = tsx.flatMap((f) => [...f.text.matchAll(/style=\{\{[^}]*width[^}]*\}\}/g)]
      .map((m) => `${f.path}: ${m[0]}`)
      .filter((s) => !s.includes('%')))
    expect(offenders).toEqual([])
  })

  it('固定宽度的浮层必须配视口相对上限（轻提示）', () => {
    const toast = source('components/Toast.tsx')
    expect(toast).toContain('w-80')
    expect(toast).toContain('max-w-[calc(100vw-3rem)]')
  })

  it('认证页全屏壳禁止横向滚动，内容宽度用 max-w', () => {
    const ui = source('components/ui.tsx')
    const shell = ui.slice(ui.indexOf('export function AuthCard'))
    expect(shell).toContain('overflow-x-hidden')
    expect(shell).toContain('w-full max-w-sm')
  })

  it('表格与 JSON 视图在自身容器内滚动（不把横向溢出推给 body）', () => {
    const schema = source('components/SchemaRenderer.tsx')
    expect(schema).toContain("cn('overflow-x-auto', className)")
    expect(schema).toContain('overflow-auto rounded-xl')
    const detail = source('pages/DeviceDetail.tsx')
    expect(detail).toContain('overflow-x-auto')
  })

  it('布局内容宽度是 max-w + 相对内边距，窄屏侧栏退出布局', () => {
    const layout = source('components/Layout.tsx')
    expect(layout).toContain('mx-auto max-w-6xl px-4')
    expect(layout).toContain('hidden w-60 flex-col border-r border-hairline px-3 py-5 lg:flex')
  })
})