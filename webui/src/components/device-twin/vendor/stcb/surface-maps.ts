/** Deterministic, subtle manufacturing grain, not photographic detail or measured scans. */
import * as THREE from 'three'

const cache = new Map<string, THREE.DataTexture>()
export function surfaceMap(kind: 'grain' | 'brushed' | 'mask') {
  if (cache.has(kind)) return cache.get(kind)!
  const size = 256, pixels = new Uint8Array(size * size * 4)
  let seed = 7319
  const random = () => { seed = (Math.imul(seed, 1664525) + 1013904223) >>> 0; return seed / 4294967296 }
  for (let y = 0; y < size; y++) {
    const stripe = random() * 14
    for (let x = 0; x < size; x++) {
      const value = kind === 'brushed' ? 225 + stripe + random() * 12
        : kind === 'mask' ? 165 + random() * 12 + 6 * Math.sin(x / 19) * Math.cos(y / 23)
        : 170 + random() * 35
      const i = (y * size + x) * 4
      pixels[i] = pixels[i + 1] = pixels[i + 2] = value; pixels[i + 3] = 255
    }
  }
  const map = new THREE.DataTexture(pixels, size, size)
  map.wrapS = map.wrapT = THREE.RepeatWrapping
  map.magFilter = THREE.LinearFilter; map.minFilter = THREE.LinearMipmapLinearFilter
  map.generateMipmaps = true; map.repeat.set(kind === 'mask' ? 10 : 2, kind === 'mask' ? 8 : 2)
  map.needsUpdate = true; cache.set(kind, map)
  return map
}
