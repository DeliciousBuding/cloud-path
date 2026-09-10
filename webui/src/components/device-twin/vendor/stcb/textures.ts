import * as THREE from 'three'

function canvasTexture(width: number, height: number, paint: (ctx: CanvasRenderingContext2D) => void) {
  const canvas = document.createElement('canvas')
  canvas.width = width
  canvas.height = height
  const ctx = canvas.getContext('2d')!
  paint(ctx)
  const texture = new THREE.CanvasTexture(canvas)
  texture.colorSpace = THREE.SRGBColorSpace
  texture.anisotropy = 8
  return { canvas, ctx, texture }
}

export function createMarkingTexture(text: string, color = '#797c72') {
  return canvasTexture(768, 384, (ctx) => {
    ctx.fillStyle = color
    ctx.textAlign = 'center'
    ctx.textBaseline = 'middle'
    const lines = text.split('\n')
    const size = Math.min(85, 560 / Math.max(...lines.map((line) => line.length)) * 1.65)
    ctx.font = `500 ${size}px Arial, sans-serif`
    lines.forEach((line, index) => ctx.fillText(line, 384, 192 + (index - (lines.length - 1) / 2) * (size + 16)))
  }).texture
}

/** Compact markings have their own scale; IC-label padding made 0805 codes illegibly tiny. */
export function createPassiveMarkingTexture(code: string) {
  return canvasTexture(384, 192, ctx => {
    ctx.fillStyle = '#b8bcaa'; ctx.font = '500 148px Arial, sans-serif'
    ctx.textAlign = 'center'; ctx.textBaseline = 'middle'; ctx.fillText(code, 192, 100, 350)
  }).texture
}

export function createRoundTexture(kind: 'battery' | 'capacitor' | 'ldr' | 'buzzer', relief = false) {
  return canvasTexture(512, 512, (ctx) => {
    ctx.textAlign = 'center'
    ctx.textBaseline = 'middle'
    if (kind === 'ldr') {
      ctx.fillStyle = '#d5b581'
      ctx.fillRect(0, 0, 512, 512)
      ctx.strokeStyle = '#653f24'
      ctx.lineWidth = 16
      ctx.lineCap = 'round'
      ctx.beginPath()
      // The photo shows a single serpentine electrode, not two ladder rails.
      ctx.moveTo(120, 100); ctx.lineTo(352, 100)
      for (let i = 0; i < 5; i++) {
        const y = 100 + i * 60, right = i % 2 === 0
        const x = right ? 352 : 160
        ctx.bezierCurveTo(x + (right ? 36 : -36), y, x + (right ? 36 : -36), y + 60, x, y + 60)
        ctx.lineTo(right ? 160 : 352, y + 60)
      }
      ctx.stroke()

    } else {
      ctx.fillStyle = relief ? '#888888' : kind === 'battery' ? '#9b9b8e' : kind === 'capacitor' ? '#b8bdb7' : '#121314'
      ctx.fillRect(0, 0, 512, 512)
      ctx.strokeStyle = relief ? '#505050' : kind === 'buzzer' ? '#252729' : '#74766a'
      ctx.lineWidth = 3
      ctx.beginPath(); ctx.arc(256, 256, 226, 0, Math.PI * 2); ctx.stroke()
      ctx.fillStyle = relief ? '#444444' : kind === 'buzzer' ? '#585b50' : kind === 'battery' ? '#797c70' : '#414942'
      if (kind === 'battery') {
        ctx.font = '72px Consolas'; ctx.fillText('+', 256, 100)
        ctx.font = '48px Consolas'; ctx.fillText('CR1220', 256, 230)
        ctx.font = '40px Consolas'; ctx.fillText('3V', 256, 312)
        ctx.font = '24px Consolas'; ctx.fillText('LITHIUM CELL', 256, 377)
      } else if (kind === 'capacitor') {
        // Assembly photo reads 100; schematic p6 specifies 220 uF. Do not invent a voltage rating.
        ctx.font = '92px Consolas'; ctx.fillText('100', 280, 260)
        ctx.beginPath(); ctx.arc(256, 256, 228, Math.PI * 0.73, Math.PI * 1.27); ctx.closePath(); ctx.fill()
      } else {
        ctx.font = '72px Consolas'; ctx.fillText('+', 352, 112)
        ctx.font = '38px Consolas'; ctx.fillText('BZ', 167, 152)
      }
    }
  }).texture
}

const SEGMENTS: Record<string, number> = {
  '0': 0x3f, '1': 0x06, '2': 0x5b, '3': 0x4f, '4': 0x66, '5': 0x6d,
  '6': 0x7d, '7': 0x07, '8': 0x7f, '9': 0x6f, '-': 0x40, H: 0x76, L: 0x38,
  C: 0x39, b: 0x7c, S: 0x6d, t: 0x78, ' ': 0,
}

/** Diffuse tint and emitted light are independent: the face and unlit diffusers never emit. */
export function createDisplayTexture() {
  const color = canvasTexture(1200, 520, () => {})
  const emission = canvasTexture(1200, 520, () => {})
  let last = ''
  const draw = (text: string, powered: boolean) => {
    const key = String(powered) + ':' + text
    if (key === last) return
    last = key
    for (const [ctx, emitted] of [[color.ctx, false], [emission.ctx, true]] as const) {
      ctx.fillStyle = emitted ? '#000000' : '#101415'
      ctx.fillRect(0, 0, 1200, 520)
      for (let digit = 0; digit < 4; digit++) {
        const bits = SEGMENTS[text[digit] ?? ' '] ?? 0
        ctx.save()
        ctx.translate(digit * 300 + 62, 57)
        ctx.transform(1, 0, -0.075, 1, 0, 0)
        const w = 202, h = 388, t = 24
        const horizontal = (x: number, y: number) => [[x, y + t / 2], [x + t / 2, y], [x + w - t * 1.5, y], [x + w - t, y + t / 2], [x + w - t * 1.5, y + t], [x + t / 2, y + t]]
        const vertical = (x: number, y: number) => [[x + t / 2, y], [x + t, y + t / 2], [x + t, y + h / 2 - t * 1.5], [x + t / 2, y + h / 2 - t], [x, y + h / 2 - t * 1.5], [x, y + t / 2]]
        const polygons = [horizontal(t / 2, 0), vertical(w - t, t), vertical(w - t, h / 2 + 4), horizontal(t / 2, h - t), vertical(0, h / 2 + 4), vertical(0, t), horizontal(t / 2, h / 2 - t / 2)]
        polygons.forEach((vertices, segment) => {
          const on = powered && Boolean(bits & (1 << segment))
          if (emitted && !on) return
          ctx.fillStyle = emitted ? '#ff1005' : on ? '#86100a' : '#b8b8a0'
          ctx.beginPath()
          vertices.forEach(([x, y], i) => i ? ctx.lineTo(x, y) : ctx.moveTo(x, y))
          ctx.closePath(); ctx.fill()
        })
        if (!emitted) {
          ctx.fillStyle = '#b8b8a0'
          ctx.beginPath(); ctx.arc(235, h - 10, 11, 0, Math.PI * 2); ctx.fill()
        }
        ctx.restore()
      }
    }
    color.texture.needsUpdate = true
    emission.texture.needsUpdate = true
  }
  draw('    ', false)
  return { texture: color.texture, emissiveTexture: emission.texture, draw }
}

let ledGlow: THREE.Texture | undefined
export function createLedGlowTexture() {
  ledGlow ??= canvasTexture(64, 64, ctx => {
    const glow = ctx.createRadialGradient(32, 32, 1, 32, 32, 32)
    glow.addColorStop(0, 'rgba(255,255,255,0.6)')
    glow.addColorStop(0.35, 'rgba(255,255,255,0.12)')
    glow.addColorStop(1, 'rgba(255,255,255,0)')
    ctx.fillStyle = glow; ctx.fillRect(0, 0, 64, 64)
  }).texture
  return ledGlow
}
