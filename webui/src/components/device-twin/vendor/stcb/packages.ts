import * as THREE from 'three'
import { RoundedBoxGeometry } from 'three/addons/geometries/RoundedBoxGeometry.js'
import { mergeGeometries } from 'three/addons/utils/BufferGeometryUtils.js'
import type { LedColor } from './visual-state'
import { surfaceMap } from './surface-maps'
import { radialLeadGeometry } from './detail-geometry'
import type { Component } from './board-layout'
import { createLedGlowTexture, createDisplayTexture, createMarkingTexture, createRoundTexture } from './textures'

export const materials = {
  plastic: new THREE.MeshStandardMaterial({ color: 0x101113, roughness: 0.64, bumpMap: surfaceMap('grain'), bumpScale: 0.012 }),
  rubber: new THREE.MeshStandardMaterial({ color: 0x151618, roughness: 0.92, bumpMap: surfaceMap('grain'), bumpScale: 0.018 }),
  chip: new THREE.MeshStandardMaterial({ color: 0x191a1c, roughness: 0.76, bumpMap: surfaceMap('grain'), bumpScale: 0.01 }),
  silver: new THREE.MeshStandardMaterial({ color: 0xaeb4b7, roughness: 0.47, metalness: 1, roughnessMap: surfaceMap('brushed'), bumpMap: surfaceMap('brushed'), bumpScale: 0.004 }),
  solder: new THREE.MeshStandardMaterial({ color: 0x969ea2, roughness: 0.46, metalness: 1, bumpMap: surfaceMap('grain'), bumpScale: 0.006 }),
  stamped: new THREE.MeshStandardMaterial({ color: 0xb1b2a4, roughness: 0.5, metalness: 1, roughnessMap: surfaceMap('brushed'), bumpMap: surfaceMap('brushed'), bumpScale: 0.003 }),
  gold: new THREE.MeshStandardMaterial({ color: 0xa28a52, roughness: 0.44, metalness: 1, roughnessMap: surfaceMap('brushed') }),
  resistorBase: new THREE.MeshStandardMaterial({ color: 0x747b76, roughness: 0.78, bumpMap: surfaceMap('grain'), bumpScale: 0.004 }),
  polarity: new THREE.MeshStandardMaterial({ color: 0x278447, roughness: 0.58 }),
  plated: new THREE.MeshStandardMaterial({ color: 0x697e7c, roughness: 0.53, metalness: 0.8 }),
  ceramic: new THREE.MeshStandardMaterial({ color: 0xa59070, roughness: 0.7 }),
  ivory: new THREE.MeshStandardMaterial({ color: 0xc5c5b7, roughness: 0.64 }),
  thermistor: new THREE.MeshPhysicalMaterial({ color: 0x171b17, roughness: 0.3, clearcoat: 0.55, clearcoatRoughness: 0.2 }),
  sensorCan: new THREE.MeshPhysicalMaterial({ color: 0x0d1420, roughness: 0.27, clearcoat: 0.7, clearcoatRoughness: 0.28 }),
  glass: new THREE.MeshPhysicalMaterial({ color: 0xdbe3e5, roughness: 0.12, metalness: 0, transparent: true, opacity: 0.4, depthWrite: false, clearcoat: 1 }),
  diodeGlass: new THREE.MeshPhysicalMaterial({ color: 0xa93713, roughness: 0.24, clearcoat: 0.8, clearcoatRoughness: 0.22 }),
  red: new THREE.MeshStandardMaterial({ color: 0x815038, roughness: 0.44 }),
}

const boxCache = new Map<string, THREE.BufferGeometry>()
export function block(w: number, h: number, d: number, mat: THREE.Material, radius = 0.1) {
  const r = Math.min(radius, w / 2, h / 2, d / 2)
  const key = [w, h, d, r].join(':')
  if (!boxCache.has(key)) boxCache.set(key, new RoundedBoxGeometry(w, h, d, 2, r))
  const mesh = new THREE.Mesh(boxCache.get(key)!, mat)
  mesh.castShadow = true; mesh.receiveShadow = true
  return mesh
}
function b(g: THREE.Group, w: number, h: number, d: number, x: number, y: number, z: number, mat = materials.plastic, radius = 0.1) {
  const mesh = block(w, h, d, mat, radius)
  mesh.position.set(x, y, z); g.add(mesh)
  return mesh
}
function cyl(g: THREE.Group, r: number, height: number, x: number, y: number, z: number, mat: THREE.Material = materials.plastic) {
  const mesh = new THREE.Mesh(new THREE.CylinderGeometry(r, r, height, 48), mat)
  mesh.position.set(x, y, z); mesh.castShadow = true; mesh.receiveShadow = true; g.add(mesh)
  return mesh
}
function ring(g: THREE.Group, inner: number, outer: number, x: number, y: number, z: number, mat: THREE.Material = materials.silver) {
  const mesh = new THREE.Mesh(new THREE.RingGeometry(inner, outer, 48), mat)
  mesh.rotation.x = -Math.PI / 2; mesh.position.set(x, y, z); g.add(mesh)
  return mesh
}
function topFace(g: THREE.Group, w: number, d: number, y: number, texture: THREE.Texture, round = false, metallic = false, relief?: THREE.Texture, opening = 0) {
  if (relief) relief.colorSpace = THREE.NoColorSpace
  const mat = new THREE.MeshStandardMaterial({ map: texture, transparent: !round, roughness: metallic ? 0.48 : 0.72, metalness: metallic ? 1 : 0, bumpMap: relief ?? (metallic ? surfaceMap('brushed') : null), bumpScale: relief ? 0.045 : 0.004, depthWrite: false, polygonOffset: true, polygonOffsetFactor: -1 })
  const mesh = new THREE.Mesh(round ? (opening ? new THREE.RingGeometry(opening, w / 2, 64) : new THREE.CircleGeometry(w / 2, 64)) : new THREE.PlaneGeometry(w, d), mat)
  mesh.rotation.x = -Math.PI / 2; mesh.position.y = y; g.add(mesh)
  return mesh
}
function marking(g: THREE.Group, text: string, w: number, d: number, y: number) {
  return topFace(g, w, d, y, createMarkingTexture(text))
}

type PlateHole = { x: number; z: number; radius: number } | { x: number; z: number; w: number; d: number }
/** Stamped sheet with real apertures, not black decals on a solid lid. */
function cutPlate(g: THREE.Group, w: number, d: number, thickness: number, y: number, holes: PlateHole[], mat = materials.silver) {
  const shape = new THREE.Shape(), r = 0.16
  shape.moveTo(-w / 2 + r, -d / 2); shape.lineTo(w / 2 - r, -d / 2)
  shape.quadraticCurveTo(w / 2, -d / 2, w / 2, -d / 2 + r); shape.lineTo(w / 2, d / 2 - r)
  shape.quadraticCurveTo(w / 2, d / 2, w / 2 - r, d / 2); shape.lineTo(-w / 2 + r, d / 2)
  shape.quadraticCurveTo(-w / 2, d / 2, -w / 2, d / 2 - r); shape.lineTo(-w / 2, -d / 2 + r)
  shape.quadraticCurveTo(-w / 2, -d / 2, -w / 2 + r, -d / 2)
  for (const hole of holes) {
    const path = new THREE.Path(), { x, z } = hole
    if ('radius' in hole) path.absarc(x, -z, hole.radius, 0, Math.PI * 2, true)
    else {
      path.moveTo(x - hole.w / 2, -z - hole.d / 2); path.lineTo(x - hole.w / 2, -z + hole.d / 2)
      path.lineTo(x + hole.w / 2, -z + hole.d / 2); path.lineTo(x + hole.w / 2, -z - hole.d / 2); path.closePath()
    }
    shape.holes.push(path)
  }
  const geometry = new THREE.ExtrudeGeometry(shape, { depth: thickness, bevelEnabled: false, curveSegments: 12 })
  geometry.rotateX(-Math.PI / 2)
  const mesh = new THREE.Mesh(geometry, mat)
  mesh.position.y = y; mesh.castShadow = true; mesh.receiveShadow = true; g.add(mesh)
  return mesh
}
function rolledRim(g: THREE.Group, radius: number, tube: number, y: number, mat = materials.silver) {
  const mesh = new THREE.Mesh(new THREE.TorusGeometry(radius, tube, 8, 64), mat)
  mesh.rotation.x = -Math.PI / 2; mesh.position.y = y; mesh.castShadow = true; g.add(mesh)
}

/** Batch static triangles by material within a selectable component; dynamic faces stay separate. */
export function batchStatic(g: THREE.Group) {
  const groups = new Map<THREE.Material, THREE.Mesh[]>()
  for (const child of [...g.children]) {
    if (child.name || !(child instanceof THREE.Mesh) || child instanceof THREE.InstancedMesh || Array.isArray(child.material) || child.material.transparent) continue
    const mat = child.material as THREE.Material
    if (!groups.has(mat)) groups.set(mat, [])
    groups.get(mat)!.push(child)
  }
  for (const [mat, meshes] of groups) {
    if (meshes.length < 2) continue
    const copies = meshes.map((m) => {
      m.updateMatrix()
      // Rounded boxes are non-indexed, cylinders indexed; normalize before batching.
      const geometry = m.geometry.index ? m.geometry.toNonIndexed() : m.geometry.clone()
      return geometry.applyMatrix4(m.matrix)
    })
    const geometry = mergeGeometries(copies)
    copies.forEach((copy) => copy.dispose())
    if (!geometry) continue
    const mesh = new THREE.Mesh(geometry, mat)
    mesh.castShadow = true; mesh.receiveShadow = true
    meshes.forEach((m) => g.remove(m)); g.add(mesh)
  }
}

function gullwingGeometry(width: number) {
  const shape = new THREE.Shape()
  shape.moveTo(0, 0.78); shape.lineTo(0.36, 0.78); shape.lineTo(0.73, 0.2)
  shape.lineTo(1.22, 0.2); shape.lineTo(1.22, 0.04); shape.lineTo(0.62, 0.04)
  shape.lineTo(0.25, 0.62); shape.lineTo(0, 0.62); shape.closePath()
  const geometry = new THREE.ExtrudeGeometry(shape, { depth: width, bevelEnabled: false })
  geometry.translate(0, 0, -width / 2)
  return geometry
}
function chip(g: THREE.Group, c: Component) {
  b(g, c.w, c.h - 0.2, c.d, 0, c.h / 2 + 0.1, 0, materials.chip, 0.15)
  const qfp = c.kind === 'qfp'
  const count = c.pins ?? 8, perSide = count / (qfp ? 4 : 2)
  const pitch = qfp ? 0.8 : 1.27
  const pins = new THREE.InstancedMesh(gullwingGeometry(qfp ? 0.27 : 0.4), materials.silver, count)
  const transform = new THREE.Object3D()
  let index = 0
  const put = (x: number, z: number, angle: number) => {
    transform.position.set(x, 0, z); transform.rotation.set(0, angle, 0); transform.updateMatrix()
    pins.setMatrixAt(index++, transform.matrix)
  }
  for (let i = 0; i < perSide; i++) {
    const v = (i - (perSide - 1) / 2) * pitch
    put(v, -c.d / 2, Math.PI / 2); put(v, c.d / 2, -Math.PI / 2)
    if (qfp) { put(-c.w / 2, v, Math.PI); put(c.w / 2, v, 0) }
  }
  pins.castShadow = true; pins.receiveShadow = true; g.add(pins)
  marking(g, c.marking ?? c.ref, c.w - 1.2, c.d - 1, c.h + 0.015)
  cyl(g, 0.24, 0.02, -c.w / 2 + 0.62, c.h + 0.03, -c.d / 2 + 0.65, materials.rubber)
}

export type PackageModel = { group: THREE.Group; setPower?: (powered: boolean, text?: string) => void; displayTexture?: THREE.Texture; setLeds?: (mask: number, color: LedColor) => void }
export function createPackage(c: Component): PackageModel {
  const g = new THREE.Group()
  g.name = c.id
  const result: PackageModel = { group: g }
  const { w, d, h } = c
  switch (c.kind) {
    case 'display': {
      b(g, w, 1.1, d + 0.55, 0, 0.65, 0, materials.ivory, 0.18)
      b(g, w - 0.1, h - 1.1, d, 0, 1.1 + (h - 1.1) / 2, 0, materials.plastic, 0.17)
      const display = createDisplayTexture()
      const face = new THREE.Mesh(new THREE.PlaneGeometry(w - 0.7, d - 0.6), new THREE.MeshPhysicalMaterial({ map: display.texture, roughness: 0.65, specularIntensity: 0.12, clearcoat: 0.05, clearcoatRoughness: 0.6, emissiveMap: display.emissiveTexture, emissive: 0xffffff, emissiveIntensity: 0, polygonOffset: true, polygonOffsetFactor: -1, polygonOffsetUnits: -2 }))
      face.name = 'display-face'; face.rotation.x = -Math.PI / 2; face.position.y = h + 0.01; g.add(face)
      result.setPower = (powered, text = '2026') => {
        display.draw(text, powered); face.material.emissiveIntensity = powered ? 1.45 : 0
      }
      result.displayTexture = display.texture
      for (let i = 0; i < 6; i++) for (const z of [-d / 2 + 0.7, d / 2 - 0.7]) b(g, 0.45, 0.3, 0.9, (i - 2.5) * 4.2, 0.15, z, materials.silver, 0.03)
      break
    }
    case 'qfp': case 'soic': chip(g, c); break
    case 'lga': {
      b(g, w, h, d, 0, h / 2, 0, materials.chip, 0.08)
      for (let i = 0; i < 7; i++) for (const z of [-d / 2, d / 2]) b(g, 0.3, 0.16, 0.6, (i - 3) * 0.65, 0.09, z, materials.silver, 0.02)
      marking(g, c.marking ?? '', w - 0.6, d - 0.4, h + 0.01)
      break
    }
    case 'buzzer': {
      const shell = new THREE.Mesh(new THREE.CylinderGeometry(w / 2, w / 2, h - 0.25, 64, 1, true), materials.plastic)
      shell.position.y = h / 2; shell.castShadow = true; shell.receiveShadow = true; g.add(shell)
      ring(g, w / 2 - 0.45, w / 2, 0, h - 0.03, 0, materials.rubber)
      const lid = topFace(g, w - 0.5, w - 0.5, h, createRoundTexture('buzzer'), true, false, undefined, 0.85)
      lid.name = 'buzzer-lid'
      const bore = new THREE.Mesh(new THREE.CylinderGeometry(0.85, 0.85, 0.75, 32, 1, true), new THREE.MeshStandardMaterial({ color: 0x111214, roughness: 0.95, side: THREE.BackSide }))
      bore.position.y = h - 0.375; g.add(bore)
      cyl(g, 0.8, 0.05, 0, h - 0.76, 0, materials.red)
      break
    }
    case 'battery': {
      cyl(g, 6.75, 0.28, 0, 0.35, 0, materials.gold)
      ring(g, 5.9, 6.95, 0, 0.7, 0, materials.gold)
      cyl(g, 6.08, 2.05, 0, 2.02, 0, materials.silver)
      rolledRim(g, 6.03, 0.09, 2.98)
      const coinFace = topFace(g, 11.85, 11.85, 3.08, createRoundTexture('battery'), true, true, createRoundTexture('battery', true))
      coinFace.rotation.z = Math.PI
      for (const x of [-6.45, 6.45]) b(g, 0.28, 2.45, 2.1, x, 1.58, -4.5, materials.gold, 0.09)
      b(g, 13.4, 0.2, 1.05, 0, 3.1, -5.55, materials.gold, 0.08)
      const spring = b(g, 1.9, 0.18, 2.4, 0, 2.72, -6.0, materials.gold, 0.05)
      spring.rotation.x = -0.28
      b(g, 2.8, 0.28, 1.4, 0, 0.2, -7.0, materials.solder)
      b(g, 2.7, 0.25, 2.0, 0, 0.15, 7.1, materials.solder)
      break
    }
    case 'button': case 'navigation': {
      const navigation = c.kind === 'navigation'
      const deck = navigation ? 1.45 : h - 0.6
      b(g, w, deck - 0.05, d, 0, (deck - 0.05) / 2, 0, materials.plastic, 0.22)
      const holes: PlateHole[] = [{ x: 0, z: 0, radius: 1.83 }]
      for (const x of [-w / 2 + 0.78, w / 2 - 0.78]) for (const z of [-d / 2 + 0.78, d / 2 - 0.78]) holes.push({ x, z, radius: 0.42 })
      const lid = cutPlate(g, w - 0.35, d - 0.35, 0.2, deck, holes, materials.stamped)
      lid.name = 'switch-lid'
      for (const x of [-w / 2 - 0.35, w / 2 + 0.35]) for (const z of [-d / 2 + 0.8, d / 2 - 0.8]) b(g, 1.0, 0.3, 0.7, x, 0.2, z, materials.solder)
      const top = navigation ? h - 0.6 : h
      cyl(g, 1.7, top - deck, 0, (top + deck) / 2, 0, materials.rubber)
      if (navigation) {
        cyl(g, 0.92, 0.5, 0, h - 0.34, 0, materials.ivory)
        b(g, 1.45, 0.18, 1.45, 0, h - 0.09, 0, materials.ivory, 0.2)
      }
      break
    }
    case 'usb': {
      b(g, w, 0.26, d, 0, 0.24, 0, materials.silver)
      for (const z of [-d / 2 + 0.12, d / 2 - 0.12]) b(g, w, h - 0.5, 0.24, 0, h / 2, z, materials.silver, 0.04)
      b(g, 0.22, h - 0.25, d, w / 2 - 0.12, h / 2, 0, materials.silver, 0.04)
      const holes: PlateHole[] = [
        { x: -1.8, z: 0, w: 3.25, d: 1.85 },
        { x: -1.7, z: -2.58, w: 2.7, d: 0.58 }, { x: -1.7, z: 2.58, w: 2.7, d: 0.58 },
        { x: 2.2, z: -1.5, w: 0.6, d: 2.1 }, { x: 2.2, z: 1.5, w: 0.6, d: 2.1 },
      ]
      const roof = cutPlate(g, w, d, 0.2, h - 0.2, holes)
      roof.name = 'usb-shell-roof'
      b(g, w - 2.4, 0.7, d - 1.5, 0.8, 1.28, 0, materials.plastic, 0.06)
      for (let i = 0; i < 5; i++) {
        const z = (i - 2) * 1.1
        b(g, 4.7, 0.12, 0.32, -0.6, 1.72, z, materials.gold, 0.02)
        b(g, 1.65, 0.22, 0.38, w / 2 + 0.55, 0.22, z, materials.solder, 0.03)
      }
      for (const z of [-d / 2 - 0.45, d / 2 + 0.45]) for (const x of [-2.5, 2.4]) b(g, 1.65, 0.28, 1.2, x, 0.2, z, materials.solder, 0.04)
      // Folded leading edges frame the opening without blocking the tongue.
      b(g, 0.25, 0.35, d, -w / 2 + 0.04, h - 0.17, 0, materials.silver, 0.04)
      b(g, 0.25, 0.28, d, -w / 2 + 0.04, 0.29, 0, materials.silver, 0.04)
      break
    }
    case 'audio': {
      b(g, w, 1.2, d, 0, 0.65, 0, materials.plastic, 0.15)
      const tunnel = new THREE.Mesh(new THREE.CylinderGeometry(1.9, 1.9, w, 48, 1, true), materials.plastic)
      tunnel.rotation.z = Math.PI / 2; tunnel.position.y = 2.5; tunnel.castShadow = true; g.add(tunnel)
      const bore = new THREE.Mesh(new THREE.CylinderGeometry(1.48, 1.48, w - 0.2, 48, 1, true), new THREE.MeshStandardMaterial({ color: 0x121314, roughness: 0.85, side: THREE.BackSide }))
      bore.rotation.z = Math.PI / 2; bore.position.y = 2.5; g.add(bore)
      const rear = cyl(g, 1.85, 0.2, w / 2 - 0.2, 2.5, 0, materials.rubber); rear.rotation.z = Math.PI / 2
      const mouth = new THREE.Mesh(new THREE.RingGeometry(1.5, 2.2, 48), materials.silver)
      mouth.rotation.y = -Math.PI / 2; mouth.position.set(-w / 2 - 0.05, 2.5, 0); g.add(mouth)
      for (const x of [-3.5, 0, 3.5]) {
        b(g, 1.2, 2.7, 0.45, x, 2.3, -2.55, materials.silver)
        b(g, 1.2, 2.7, 0.45, x, 2.3, 2.55, materials.silver)
        b(g, 1.2, 0.5, 1.65, x, 3.8, -1.85, materials.silver)
        b(g, 1.2, 0.5, 1.65, x, 3.8, 1.85, materials.silver)
        b(g, 1.5, 0.2, 7, x, 0.1, 0, materials.solder)
      }
      break
    }
    case 'header': {
      // The assembly photo shows right-angle FEMALE sockets, not exposed male pins.
      const n = c.pins ?? 0
      b(g, w, 0.45, d, 0, 0.55, 0, materials.plastic)
      b(g, w, 0.45, d, 0, 3.45, 0, materials.plastic)
      b(g, 0.45, 3.0, d, -w / 2 + 0.2, 2, 0, materials.plastic)
      for (let i = 0; i <= n; i++) b(g, w, 2.5, 0.52, 0, 2, (i - n / 2) * 2.54, materials.plastic, 0.05)
      for (let i = 0; i < n; i++) {
        const z = (i - (n - 1) / 2) * 2.54
        for (const dz of [-0.62, 0.62]) b(g, w - 2, 0.7, 0.16, -0.5, 2, z + dz, materials.gold, 0.025)
        b(g, 0.8, 0.2, 0.6, -w / 2 - 0.15, 0.13, z, materials.solder, 0.025)
      }
      break
    }
    case 'hall': case 'ir-receiver': {
      b(g, w, h, d, 0, h / 2, 0, materials.plastic, 0.3)
      for (const x of [-1.27, 0, 1.27]) b(g, 0.4, 0.25, c.kind === 'hall' ? 0.9 : 1.5, x, 0.15, d / 2 + 0.25, materials.silver, 0.03)
      if (c.kind === 'ir-receiver') {
        const lens = cyl(g, 1.65, 0.35, 0, h / 2, -d / 2, materials.rubber); lens.rotation.x = Math.PI / 2
      }
      break
    }
    case 'ir-emitter': {
      const body = cyl(g, 2.35, 3.9, 0, 2.5, -0.7, materials.glass); body.rotation.x = Math.PI / 2
      const dome = new THREE.Mesh(new THREE.SphereGeometry(2.35, 32, 20, 0, Math.PI * 2, 0, Math.PI / 2), materials.glass)
      dome.rotation.x = -Math.PI / 2; dome.position.set(0, 2.5, -2.65); g.add(dome)
      b(g, 1.0, 1.3, 1.0, 0, 2.5, -0.7, materials.silver)
      for (const x of [-1.25, 1.25]) b(g, 0.32, 0.32, 4.5, x, 0.3, 2.5, materials.silver, 0.04)
      break
    }
    case 'ldr': {
      for (const x of [-1.1, 1.1]) b(g, 0.25, 2.4, 0.25, x, 1.2, 0, materials.silver)
      cyl(g, w / 2, 0.75, 0, h - 0.4, 0, materials.ceramic)
      topFace(g, w - 0.2, d - 0.2, h, createRoundTexture('ldr'), true)
      break
    }
    case 'thermistor': {
      for (const x of [-0.8, 0.8]) b(g, 0.18, 2.4, 0.18, x, 1.2, 0, materials.silver)
      const bead = new THREE.Mesh(new THREE.SphereGeometry(1, 24, 16), materials.thermistor)
      bead.scale.set(1.2, 0.8, 0.8); bead.position.y = 2.5; g.add(bead)
      break
    }
    case 'vibration': case 'watch-crystal': {
      const metal = c.kind === 'watch-crystal'
      const end = metal ? -1 : 1, radius = w / 2, axis = radius + 0.12
      const body = cyl(g, radius, d, 0, axis, 0, metal ? materials.silver : materials.sensorCan)
      body.rotation.x = Math.PI / 2
      const seal = cyl(g, radius * 0.91, 0.14, 0, axis, end * (d / 2 + 0.025), materials.rubber)
      seal.rotation.x = Math.PI / 2
      const lip = new THREE.Mesh(new THREE.TorusGeometry(radius * 0.95, metal ? 0.06 : 0.09, 8, 48), metal ? materials.silver : materials.sensorCan)
      lip.position.set(0, axis, end * d / 2); g.add(lip)
      const leadContacts = []
      for (const [i, pad] of (c.contacts ?? []).entries()) {
        const start = new THREE.Vector3((i ? 1 : -1) * (metal ? 0.42 : 1.05), axis, end * (d / 2 + 0.08))
        const tip = new THREE.Vector3(pad[0] - c.x, 0.04, pad[1] - c.z)
        const lead = new THREE.Mesh(radialLeadGeometry(start, tip, metal ? 0.1 : 0.14), materials.silver)
        lead.name = `radial-lead-${i}`; lead.castShadow = true; lead.receiveShadow = true; g.add(lead)
        leadContacts.push({ start: start.toArray(), end: tip.toArray() })
      }
      g.userData.leadContacts = leadContacts
      if (metal) {
        // Photo shows the can strapped to a separate land, not a lead at its sealed end.
        b(g, w + 0.8, 0.17, 1.25, 0.36, 0.18, 0.65, materials.solder, 0.08)
      }
      break
    }
    case 'crystal': {
      b(g, w + 0.6, 0.3, d + 0.4, 0, 0.2, 0, materials.solder, 0.4)
      b(g, w, h - 0.3, d, 0, h / 2 + 0.15, 0, materials.gold, 1.7)
      const label = marking(g, c.marking ?? '', d - 2.0, w - 0.5, h + 0.01)
      label.rotation.z = Math.PI / 2
      break
    }
    case 'capacitor': {
      b(g, w + 0.4, 0.55, d + 0.4, 0, 0.3, 0, materials.plastic, 0.5)
      cyl(g, w / 2, h - 0.5, 0, (h + 0.5) / 2, 0, materials.silver)
      topFace(g, w - 0.1, d - 0.1, h + 0.02, createRoundTexture('capacitor'), true, true)
      b(g, 0.3, h - 1.2, 1.6, -w / 2, h / 2, 0, materials.rubber)
      break
    }
    case 'regulator': {
      b(g, w, h, d, 0, h / 2, 0, materials.chip, 0.2)
      b(g, 2.7, 0.25, 3.5, w / 2 + 0.7, 0.2, 0, materials.silver)
      for (const z of [-2.3, 0, 2.3]) b(g, 2.0, 0.25, 0.6, -w / 2 - 0.7, 0.2, z, materials.silver)
      marking(g, '1117', w - 0.4, d - 1.5, h + 0.02)
      break
    }
    case 'led-bank': {
      const dies: THREE.MeshStandardMaterial[] = []
      const glows: THREE.SpriteMaterial[] = []
      for (let i = 0; i < 8; i++) {
        const die = new THREE.MeshPhysicalMaterial({ color: 0xc6ca8a, roughness: 0.27, clearcoat: 0.65, clearcoatRoughness: 0.22, emissive: 0x1675ff, emissiveIntensity: 0 })
        dies.push(die)
        const x = (i - 3.5) * 2.54
        b(g, 1.35, 0.7, 1.9, x, 0.38, 0, materials.ivory)
        const lens = b(g, 0.85, 0.2, 1.25, x, 0.8, 0, die)
        lens.name = `L${7 - i}`
        for (const dx of [-0.24, 0.24]) b(g, 0.17, 0.025, 0.22, x + dx, 0.912, -0.41, materials.polarity, 0.03)
        for (const z of [-0.87, 0.87]) b(g, 1.3, 0.36, 0.26, x, 0.39, z, materials.silver, 0.045)
        const glowMaterial = new THREE.SpriteMaterial({ map: createLedGlowTexture(), color: 0x1265ff, transparent: true, depthWrite: false, blending: THREE.AdditiveBlending, opacity: 0 })
        const glow = new THREE.Sprite(glowMaterial)
        glow.position.set(x, 1.04, 0); glow.scale.set(3.2, 3.2, 1)
        g.add(glow); glows.push(glowMaterial)
        for (const z of [-1.13, 1.13]) b(g, 1.3, 0.14, 0.45, x, 0.1, z, materials.solder)
      }
      result.setLeds = (mask, color) => {
        const colors = { blue: 0x1265ff, red: 0xff1405, green: 0x18d85a }
        dies.forEach((die, i) => {
          const on = Boolean(mask & (1 << (7 - i)))
          die.color.set(on ? colors[color] : 0xc6ca8a)
          die.emissive.set(colors[color]); die.emissiveIntensity = on ? 3.4 : 0
          glows[i].color.set(colors[color]); glows[i].opacity = on ? 0.38 : 0
        })
      }
      break
    }
  }
  batchStatic(g)
  g.rotation.y = -(c.rotation ?? 0) * Math.PI / 180
  return result
}

/** Distinct packages confirmed in the assembly photo and schematic p6; no electrical simulation. */
export function createDiscretePackage(kind: 'diode' | 'transistor') {
  const g = new THREE.Group()
  if (kind === 'diode') {
    const body = cyl(g, 0.5, 2.05, 0, 0.64, 0, materials.diodeGlass)
    body.rotation.z = Math.PI / 2
    const band = cyl(g, 0.506, 0.3, -0.66, 0.64, 0, materials.rubber)
    band.rotation.z = Math.PI / 2
    for (const x of [-1.6, 1.6]) {
      const lead = cyl(g, 0.13, 1.05, x, 0.59, 0, materials.silver)
      lead.rotation.z = Math.PI / 2
      b(g, 0.85, 0.34, 0.92, x, 0.2, 0, materials.solder, 0.14)
    }
  } else {
    b(g, 1.2, 0.85, 2.0, 0, 0.5, 0, materials.chip, 0.11)
    for (const [x, z] of [[-0.96, -0.66], [-0.96, 0.66], [1.02, 0]]) b(g, 1.05, 0.24, 0.5, x, 0.18, z, materials.solder, 0.06)
  }
  batchStatic(g)
  return g
}
