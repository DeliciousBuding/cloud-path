import * as THREE from 'three'
import manifest from './artwork/manifest.json'
import frontSilkURL from './artwork/front-silk.svg?url'
import frontCopperURL from './artwork/front-copper.svg?url'
import backSilkURL from './artwork/back-silk.svg?url'
import backCopperURL from './artwork/back-copper.svg?url'

export const ARTWORK_HOLES = manifest.holes.filter(h => h.artworkRadius < 1.5).map(h => ({
  x: h.x, z: h.z, throughHole: h.shape !== 'round' || h.artworkRadius > 0.6,
  shape: h.shape, padWidth: h.padWidth, padDepth: h.padDepth,
  // Filled circles in assembly artwork are symbols, not a drill table.
  radius: h.shape === 'oval' ? 0.28 : h.shape === 'square' || h.artworkRadius > 0.6 ? 0.38 : 0.17,
}))
export type BoardArtwork = Record<'frontSilk' | 'frontCopper' | 'backSilk' | 'backCopper', HTMLImageElement>
export async function loadBoardArtwork(): Promise<BoardArtwork> {
  const entries = await Promise.all([
    ['frontSilk', frontSilkURL], ['frontCopper', frontCopperURL], ['backSilk', backSilkURL], ['backCopper', backCopperURL],
  ].map(async ([key, src]) => {
    const image = new Image()
    image.src = src
    await image.decode()
    return [key, image]
  }))
  return Object.fromEntries(entries) as BoardArtwork
}
function mask(image: HTMLImageElement, color: string, mirrored: boolean, width: number, height: number) {
  const canvas = document.createElement('canvas'); canvas.width = width; canvas.height = height
  const ctx = canvas.getContext('2d')!
  if (mirrored) { ctx.translate(canvas.width, 0); ctx.scale(-1, 1) }
  ctx.drawImage(image, 0, 0, canvas.width, canvas.height)
  ctx.setTransform(1,0,0,1,0,0)
  ctx.globalCompositeOperation = 'source-in'; ctx.fillStyle = color; ctx.fillRect(0,0,canvas.width,canvas.height)
  return canvas
}
export function boardLayerTextures(art: BoardArtwork, back = false) {
  // Reference back pages are shown as viewed from below. Shared front-world UVs therefore need one x mirror.
  const copper = back ? art.backCopper : art.frontCopper
  const silk = back ? art.backSilk : art.frontSilk
  const create = (mode: 'covered' | 'exposed' | 'relief' | 'surface') => {
    const canvas = document.createElement('canvas')
    // Roughness G and metalness B share a half-resolution linear map, not two full RGBA maps.
    canvas.width = mode === 'surface' ? 1150 : 2300; canvas.height = mode === 'surface' ? 900 : 1800
    const ctx = canvas.getContext('2d')!
    const palette = {
      covered: ['#102e40', '#163748'], exposed: ['#20313a', '#c5924e'],
      relief: ['#707070', '#929292'], surface: ['#00b000', '#00a5ff'],
    }[mode]
    ctx.fillStyle = palette[0]; ctx.fillRect(0, 0, canvas.width, canvas.height)
    ctx.drawImage(mask(copper, palette[1], back, canvas.width, canvas.height), 0, 0)
    if (mode === 'covered' || mode === 'surface') {
      // Lighten changes G for ink without erasing the B copper mask beneath it.
      if (mode === 'surface') ctx.globalCompositeOperation = 'lighten'
      ctx.drawImage(mask(silk, mode === 'covered' ? '#c6cdc1' : '#00f000', back, canvas.width, canvas.height), 0, 0)
    }
    const texture = new THREE.CanvasTexture(canvas)
    texture.colorSpace = mode === 'covered' || mode === 'exposed' ? THREE.SRGBColorSpace : THREE.NoColorSpace
    texture.anisotropy = 8
    return texture
  }
  return { covered: create('covered'), exposed: create('exposed'), relief: create('relief'), surface: create('surface') }
}
