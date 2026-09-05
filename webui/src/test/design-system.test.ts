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
import * as ts from 'typescript'
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

/**
 * 找出「写在 JSX children 位置的 // 行注释」。
 * 那不是注释：JSX 会把它当字面文本渲染到界面上（`<div>// foo<span/></div>` 编译后
 * 就是 `createElement("div", null, "// foo", ...)`），用户在页面上能看见一串 //。
 * 纯正则既会误伤 TS 代码里的正常注释，也会漏掉缩进不规整的写法，所以走 TS 自己的
 * 解析器，只看 JsxText 节点：去空白后以 // 开头即为踩雷。
 */
function jsxLineCommentText(path: string, text: string): string[] {
  const sf = ts.createSourceFile(path, text, ts.ScriptTarget.ESNext, true)
  const out: string[] = []
  const visit = (node: ts.Node): void => {
    if (node.kind === ts.SyntaxKind.JsxText) {
      const raw = node.getText(sf).trim()
      if (raw.startsWith('//')) {
        const line = sf.getLineAndCharacterOfPosition(node.getStart(sf)).line + 1
        out.push(`${path}:${line}: ${raw.split('\n')[0].trim()}`)
      }
    }
    ts.forEachChild(node, visit)
  }
  visit(sf)
  return out
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
    // 轴刻度是时间戳/数值＝机器文本：11px mono（字号下限纪律在 SVG 轴上同样生效）
    expect(chart).toContain('fontSize: 11')
    expect(chart).toContain("fontFamily: 'var(--font-mono)'")
    expect(chart).not.toMatch(/#[0-9a-fA-F]{3,8}/)
  })
})

describe('JSX children 不许写 // 行注释（会被当字面文本渲染）', () => {
  it('全量 .tsx 的 JsxText 里没有以 // 开头的内容', () => {
    const offenders = tsx.flatMap((f) => jsxLineCommentText(f.path, f.text))
    expect(offenders).toEqual([])
  })

  it('守卫不是空转：历史三处踩雷文件都在扫描面里', () => {
    for (const p of ['components/Layout.tsx', 'pages/Edges.tsx', 'pages/Settings.tsx']) {
      expect(tsx.some((f) => f.path === p), `扫描面缺少 ${p}`).toBe(true)
    }
  })

  it('阳性对照：坏写法必抓，合法写法（{/* */}、正文里的 URL、表达式里的字符串）不误报', () => {
    const bad = 'const A = () => (\n  <div>\n    // 会被渲染出来\n    <span>x</span>\n  </div>\n)\n'
    expect(jsxLineCommentText('bad.tsx', bad)).toEqual(['bad.tsx:3: // 会被渲染出来'])

    const good = [
      'const A = () => (',
      '  <div>',
      '    {/* 合法 JSX 注释 */}',
      '    <span>见 https://example.com 文档</span>',
      '    <code>{"// 这是字符串不是注释"}</code>',
      '  </div>',
      ')',
      '// 普通 TS 行注释',
      'export default A',
      '',
    ].join('\n')
    expect(jsxLineCommentText('good.tsx', good)).toEqual([])
  })
})

describe('light / dark token 对齐', () => {
  const light = colorTokens(blockOf(css, '@theme'))
  const dark = colorTokens(blockOf(css, '.dark {'))

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

describe('字号刻度（type scale：10/11/12/13/14/15/22/24/26/28/30）', () => {
  const SCALE = new Set(['10', '11', '12', '13', '14', '15', '22', '24', '26', '28', '30'])

  it('组件不使用刻度外的任意字号（半像素与孤儿尺寸是密度漂移源）', () => {
    const offenders = tsx.flatMap((f) => [...f.text.matchAll(/text-\[(\d+(?:\.\d+)?)px\]/g)]
      .filter((m) => !SCALE.has(m[1]))
      .map((m) => `${f.path}: text-[${m[1]}px]`))
    expect(offenders).toEqual([])
  })
})

describe('weight 纪律（Vercel design.md：标题不超 semibold）', () => {
  it('组件不使用 font-bold 及更重的任意字重（regular 400 / medium 500 / semibold 600）', () => {
    for (const f of tsx) expect(f.text, f.path).not.toMatch(/font-(bold|extrabold|black)\b/)
  })
})

describe('命令下发契约：用户原文不被前端静默改写', () => {
  it('CommandButton 不做静默截断/剥离（校验单一出口 = ActionPanel argsError 显式报错）', () => {
    const btn = source('components/CommandButton.tsx')
    expect(btn).not.toContain('sanitizeArgs')
    expect(btn).not.toContain('.slice(')
    expect(btn).not.toContain('replace(')
  })

  it('ack 失败缺 detail 时不说机器串（状态词汇人话兜底）', () => {
    const btn = source('components/CommandButton.tsx')
    expect(btn).not.toContain('ack.detail || ack.status')
  })
})

describe('JSX 文本不许漏 Markdown 语法（** 会原样渲染给用户）', () => {
  it('全量 .tsx 的 JsxText 里没有 ** 字面量（强调走 semibold span，不走伪 markdown）', () => {
    const offenders = tsx.flatMap((f) => {
      const sf = ts.createSourceFile(f.path, f.text, ts.ScriptTarget.ESNext, true)
      const out: string[] = []
      const visit = (node: ts.Node): void => {
        if (node.kind === ts.SyntaxKind.JsxText && node.getText(sf).includes('**')) {
          out.push(`${f.path}:${sf.getLineAndCharacterOfPosition(node.getStart(sf)).line + 1}`)
        }
        ts.forEachChild(node, visit)
      }
      visit(sf)
      return out
    })
    expect(offenders).toEqual([])
  })
})

describe('字距纪律（CJK 安全：负字距止于 -0.01em；mono 零字距）', () => {
  it('组件不用 tracking-tight / tracking-tighter（-0.025em 是拉丁刻度，CJK 全角字面会挤）', () => {
    const offenders = tsx.flatMap((f) => [...f.text.matchAll(/tracking-(?:tight|tighter)\b/g)].map((m) => `${f.path}: ${m[0]}`))
    expect(offenders).toEqual([])
  })

  it('任意负字距不超 -0.01em', () => {
    const offenders = tsx.flatMap((f) => [...f.text.matchAll(/tracking-\[-0\.0[2-9]\d*em\]/g)].map((m) => `${f.path}: ${m[0]}`))
    expect(offenders).toEqual([])
  })

  it('mono 标识零字距（等宽面负字距破坏网格）', () => {
    const offenders = tsx.flatMap((f) => [...f.text.matchAll(/className="[^"]*font-mono[^"]*tracking-\[-/g)].map((m) => `${f.path}: ${m[0]}`))
    expect(offenders).toEqual([])
  })

  it('正文基底 450 = CJK/浅色画布光学墨度平价（可变轴真实实例，非合成粗）', () => {
    expect(css).toContain('font-weight: 450;')
  })

  it('暗画布整条字重阶梯等差上移 50（450→500 / 500→550 / 600→650），相对差与层级不变', () => {
    expect(css).toContain('.dark body { font-weight: 500; }')
    expect(css).toContain('.dark .font-medium { font-weight: 550; }')
    expect(css).toContain('.dark .font-semibold { font-weight: 650; }')
  })
})

describe('CJK 标点与数字 display 刻度', () => {
  it('正文 palt 收紧中文标点；数字面保留 tnum', () => {
    expect(css).toContain('font-feature-settings: "palt";')
    expect(css).toContain('font-feature-settings: "tnum", "palt";')
  })

  it('.metric 是拉丁负字距 -0.02em 的唯一出口，tsx 不得直接写该字距', () => {
    expect(css).toMatch(/\.metric \{[^}]*letter-spacing: -0\.02em;/)
    for (const f of tsx) expect(f.text, f.path).not.toMatch(/tracking-\[-0\.02em\]/)
  })

  it('标题断行平衡、散文避孤行（CJK 两行标题长短悬殊是廉价感来源）', () => {
    expect(css).toContain('h1, h2, h3 { text-wrap: balance; }')
    expect(css).toContain('p { text-wrap: pretty; }')
  })
})

describe('字号下限（Vercel：不用 tiny gray copy 挤密度）', () => {
  it('全仓灭 10px（micro mono 也自 11px 起）', () => {
    const offenders = tsx.flatMap((f) => [...f.text.matchAll(/text-\[10px\]/g)].map((m) => `${f.path}: ${m[0]}`))
    expect(offenders).toEqual([])
  })

  it('11px 只留给 mono 微文本（标识/raw JSON/版本号）；其余文字下限 12px', () => {
    const offenders = tsx.flatMap((f) =>
      [...f.text.matchAll(/(['"])([^'"\n]*)\1/g)]
        .filter((m) => m[2].includes('text-[11px]') && !m[2].includes('font-mono'))
        .map((m) => `${f.path}: ${m[2]}`))
    expect(offenders).toEqual([])
  })
})

describe('圆角刻度（foundation：tile 8 / card 12；越界圆角拒）', () => {
  it('rounded-2xl / rounded-3xl 全仓灭绝（16px+ 越界）', () => {
    const offenders = tsx.flatMap((f) => [...f.text.matchAll(/rounded-(?:2xl|3xl)\b/g)].map((m) => `${f.path}: ${m[0]}`))
    expect(offenders).toEqual([])
  })

  it('rounded-xl（12px=card 半径）只留给浮层卡与品牌 logo；卡内子面一律 rounded-lg（8px）', () => {
    const allowed = new Set(['components/Toast.tsx', 'components/ui.tsx'])
    const offenders = tsx.filter((f) => !allowed.has(f.path))
      .flatMap((f) => [...f.text.matchAll(/rounded-xl\b/g)].map((m) => `${f.path}: ${m[0]}`))
    expect(offenders).toEqual([])
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
  /**
   * 硬宽度扫描。裸 w-[..] / min-w-[..] 在 390px 会撑出横向滚动，必须拦；
   * sm:/md:/lg: 等断点前缀只在更宽视口生效，390 不受影响，放行；
   * max-* 前缀恰恰在窄屏生效，仍拦。
   */
  function fixedWidths(text: string): string[] {
    const re = /(?<![-\w])(?:([\w-]+):)?(?:w|min-w)-\[\d+(?:px|rem|vw|em)\]/g
    return [...text.matchAll(re)]
      .filter((m) => !m[1] || m[1].startsWith('max-'))
      .map((m) => m[0])
  }

  it('组件不写死像素/固定宽度（max-w-* 是上限，允许且鼓励）', () => {
    const offenders = tsx.flatMap((f) => fixedWidths(f.text).map((c) => `${f.path}: ${c}`))
    expect(offenders).toEqual([])
  })

  it('守卫语义对照：裸硬宽度与 max-* 硬宽度必抓，断点前缀放行', () => {
    expect(fixedWidths('className="w-[218px]"')).toEqual(['w-[218px]'])
    expect(fixedWidths('className="max-sm:w-[500px]"')).toEqual(['max-sm:w-[500px]'])
    expect(fixedWidths('className="sm:w-[218px] max-w-full"')).toEqual([])
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
    expect(schema).toContain('overflow-auto rounded-lg')
    const detail = source('pages/DeviceDetail.tsx')
    expect(detail).toContain('overflow-x-auto')
  })

  it('布局内容宽度是 max-w + 相对内边距，窄屏侧栏退出布局', () => {
    const layout = source('components/Layout.tsx')
    expect(layout).toContain('mx-auto max-w-[1360px] px-4')
    expect(layout).toContain('hidden w-60 flex-col border-r border-hairline px-3 py-5 lg:flex')
  })
})