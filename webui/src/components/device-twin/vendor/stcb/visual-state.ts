/** Renderer-only state. A future hardware adapter must translate its own protocol explicitly. */
export type LedColor = 'blue' | 'red' | 'green'
export type BoardVisualPatch = { -readonly [K in keyof BoardVisualState]?: BoardVisualState[K] }
export type BoardVisualState = Readonly<{
  powered: boolean
  display: string
  /** Bit 0 = rightmost L0; bit 7 = leftmost L7. */
  ledMask: number
  /** Preview palette, NOT a claim of RGB capability on the physical board. */
  ledColor: LedColor
}>
export const DEFAULT_VISUAL_STATE: BoardVisualState = Object.freeze({
  powered: false, display: '12345678', ledMask: 255, ledColor: 'blue',
})
export function applyVisualPatch(current: BoardVisualState, patch: BoardVisualPatch): BoardVisualState {
  const next = { ...current, ...patch }
  if (typeof next.powered !== 'boolean') throw new TypeError('powered must be boolean')
  if (typeof next.display !== 'string' || !/^[0-9 -]{0,8}$/.test(next.display)) {
    throw new RangeError('display accepts at most eight digits, spaces or hyphens')
  }
  if (!Number.isInteger(next.ledMask) || next.ledMask < 0 || next.ledMask > 255) throw new RangeError('ledMask must be an integer from 0 to 255')
  if (!['blue', 'red', 'green'].includes(next.ledColor)) throw new RangeError('unsupported preview LED color')
  return Object.freeze({ powered: next.powered, display: next.display.padEnd(8, ' '), ledMask: next.ledMask, ledColor: next.ledColor })
}
