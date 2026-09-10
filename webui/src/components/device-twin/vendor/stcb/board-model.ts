import { DEFAULT_VISUAL_STATE, applyVisualPatch, type BoardVisualState } from './visual-state'
import * as THREE from 'three'
import { RoundedBoxGeometry } from 'three/addons/geometries/RoundedBoxGeometry.js'
import { mergeGeometries } from 'three/addons/utils/BufferGeometryUtils.js'
import { solderFilletGeometry, solderJointGeometry, padLandGeometry } from './detail-geometry'
import { createPassiveMarkingTexture } from './textures'
import { CSS2DObject } from 'three/addons/renderers/CSS2DRenderer.js'
import { BOARD, COMPONENTS, PASSIVES, boardUV, worldPosition, type Component } from './board-layout'
import { surfaceMap } from './surface-maps'
import { ARTWORK_HOLES, boardLayerTextures, type BoardArtwork } from './board-artwork'
import { createPackage, createDiscretePackage, materials, type PackageModel } from './packages'

export type BoardPart = PackageModel & { component: Component; label: CSS2DObject; basePosition: THREE.Vector3 }
export type HighlightTone = 'accent' | 'ok' | 'warn' | 'bad' | 'idle'

function pcbGeometry() {
  const w = BOARD.width / 2, d = BOARD.depth / 2, r = BOARD.cornerRadius
  const shape = new THREE.Shape()
  shape.moveTo(-w + r, -d); shape.lineTo(w - r, -d)
  shape.quadraticCurveTo(w, -d, w, -d + r); shape.lineTo(w, d - r)
  shape.quadraticCurveTo(w, d, w - r, d); shape.lineTo(-w + r, d)
  shape.quadraticCurveTo(-w, d, -w, d - r); shape.lineTo(-w, -d + r)
  shape.quadraticCurveTo(-w, -d, -w + r, -d)
  for (const [x, z, radius] of [...BOARD.holes.map(([x,z]) => [x,z,BOARD.holeRadius]), ...ARTWORK_HOLES.map(h => [h.x,h.z,h.radius])]) {
    const hole = new THREE.Path()
    hole.absarc(x - w, -(z - d), radius, 0, Math.PI * 2, true)
    shape.holes.push(hole)
  }
  const geometry = new THREE.ExtrudeGeometry(shape, { depth: BOARD.thickness, bevelEnabled: false, curveSegments: 8 })
  geometry.rotateX(-Math.PI / 2)
  const positions = geometry.attributes.position, uv = geometry.attributes.uv, normals = geometry.attributes.normal
  for (let i = 0; i < positions.count; i++) uv.setXY(i, ...boardUV(positions.getX(i), positions.getZ(i)))
  // ExtrudeGeometry uses the same material for BOTH caps by default. The bottom is
  // independently textured from the real back-layer artwork, not a copy of the front.
  geometry.clearGroups()
  let start = 0, last = -1
  for (let i = 0; i < normals.count; i += 3) {
    const material = normals.getY(i) > 0.5 ? 0 : normals.getY(i) < -0.5 ? 1 : 2
    if (material !== last) {
      if (i > 0) geometry.addGroup(start, i - start, last)
      start = i; last = material
    }
  }
  geometry.addGroup(start, normals.count - start, last)
  return geometry
}

type Instance = { x: number; y: number; z: number; w: number; h: number; d: number; angle?: number }
function instances(rows: Instance[], material: THREE.Material, geometry: THREE.BufferGeometry = new RoundedBoxGeometry(1, 1, 1, 1, 0.12)) {
  const mesh = new THREE.InstancedMesh(geometry, material, rows.length)
  const object = new THREE.Object3D()
  rows.forEach((r, i) => {
    object.position.set(r.x, r.y, r.z); object.scale.set(r.w, r.h, r.d)
    object.rotation.set(0, r.angle ?? 0, 0); object.updateMatrix(); mesh.setMatrixAt(i, object.matrix)
  })
  mesh.castShadow = true; mesh.receiveShadow = true
  return mesh
}
function addSurfaceDetails(root: THREE.Group) {
  const banks = new Map<THREE.Material, Instance[]>()
  const fillets: Instance[] = []
  const markings = new Map<string, Instance[]>()
  const add = (material: THREE.Material, row: Instance) => {
    if (!banks.has(material)) banks.set(material, [])
    banks.get(material)!.push(row)
  }
  for (const p of PASSIVES) {
    const [x, y, z] = worldPosition(p.x, p.z)
    const angle = p.vertical ? Math.PI / 2 : 0
    if (p.kind === 'diode' || p.kind === 'transistor') {
      const discrete = createDiscretePackage(p.kind)
      discrete.name = p.ref; discrete.position.set(x, y, z); discrete.rotation.y = angle
      root.add(discrete)
      continue
    }
    const resistor = p.kind === 'resistor'
    const mat = p.kind === 'capacitor' ? materials.ceramic : p.kind === 'led' ? materials.ivory : materials.resistorBase
    add(mat, { x, y: y + 0.28, z, w: 1.45, h: 0.47, d: 0.78, angle })
    if (resistor) add(materials.chip, { x, y: y + 0.49, z, w: 1.12, h: 0.07, d: 0.77, angle })
    for (const side of [-1, 1]) {
      add(materials.silver, { x: x + Math.cos(angle) * side * 0.69, y: y + 0.28, z: z - Math.sin(angle) * side * 0.69, w: 0.3, h: 0.48, d: 0.82, angle })
      add(materials.solder, { x: x + Math.cos(angle) * side * 0.83, y: y + 0.03, z: z - Math.sin(angle) * side * 0.83, w: 0.56, h: 0.055, d: 0.92, angle })
      fillets.push({ x: x + Math.cos(angle) * side * 0.88, y: y + 0.055, z: z - Math.sin(angle) * side * 0.88, w: 0.46, h: 0.31, d: 0.88, angle: angle + (side < 0 ? Math.PI : 0) })
    }
    if (p.marking) {
      if (!markings.has(p.marking)) markings.set(p.marking, [])
      markings.get(p.marking)!.push({ x, y: y + 0.535, z, w: 0.99, h: 1, d: 0.63, angle })
    }
    if (p.kind === 'led') {
      add(materials.ceramic, { x, y: y + 0.54, z, w: 0.83, h: 0.1, d: 0.6, angle })
      add(materials.polarity, { x: x - Math.cos(angle) * 0.28, y: y + 0.602, z: z + Math.sin(angle) * 0.28, w: 0.12, h: 0.012, d: 0.32, angle })
    }
  }
  for (const [code, rows] of markings) {
    const material = new THREE.MeshStandardMaterial({ map: createPassiveMarkingTexture(code), transparent: true, depthWrite: false, roughness: 0.83, polygonOffset: true, polygonOffsetFactor: -1 })
    root.add(instances(rows, material, new THREE.PlaneGeometry(1, 1).rotateX(-Math.PI / 2)))
  }
  const solderFillets = instances(fillets, materials.solder, solderFilletGeometry())
  solderFillets.name = 'smd-solder-fillets'; root.add(solderFillets)
  for (const c of COMPONENTS.filter((c) => c.kind === 'qfp' || c.kind === 'soic')) {
    const qfp = c.kind === 'qfp', n = (c.pins ?? 8) / (qfp ? 4 : 2)
    const angle = -(c.rotation ?? 0) * Math.PI / 180
    const put = (x: number, z: number, w: number, d: number) => {
      const [cx, cy, cz] = worldPosition(c.x, c.z)
      add(materials.solder, { x: cx + Math.cos(angle) * x + Math.sin(angle) * z, y: cy + 0.065,
        z: cz - Math.sin(angle) * x + Math.cos(angle) * z, w, h: 0.13, d, angle })
    }
    for (let i = 0; i < n; i++) {
      const v = (i - (n - 1) / 2) * (qfp ? 0.8 : 1.27)
      put(v, -c.d / 2 - 0.95, 0.5, 1.0); put(v, c.d / 2 + 0.95, 0.5, 1.0)
      if (qfp) { put(-c.w / 2 - 0.95, v, 1.0, 0.5); put(c.w / 2 + 0.95, v, 1.0, 0.5) }
    }
  }
  for (const [material, rows] of banks) root.add(instances(rows, material))
}

export function createBoardModel(artwork: BoardArtwork) {
  const root = new THREE.Group()
  const frontLayers = boardLayerTextures(artwork), backLayers = boardLayerTextures(artwork, true)
  const maskMaterial = (layers: typeof frontLayers) => new THREE.MeshPhysicalMaterial({
    map: layers.covered, roughness: 0.9, specularIntensity: 0.14, metalness: 0,
    roughnessMap: layers.surface, bumpMap: layers.relief, bumpScale: 0.016,
    clearcoat: 0.045, clearcoatRoughness: 0.65,
  })
  const frontMaterial = maskMaterial(frontLayers), backMaterial = maskMaterial(backLayers)
  const board = new THREE.Mesh(pcbGeometry(), [frontMaterial, backMaterial,
    new THREE.MeshStandardMaterial({ color: 0x535741, roughness: 0.94, bumpMap: surfaceMap('grain'), bumpScale: 0.014 })])
  board.name = 'pcb'; board.castShadow = true; board.receiveShadow = true; root.add(board)
  for (const [x, z] of BOARD.holes) {
    const rim = new THREE.Mesh(new THREE.RingGeometry(BOARD.holeRadius, BOARD.holeRadius + 0.35, 48), materials.solder)
    rim.rotation.x = -Math.PI / 2; rim.position.set(...worldPosition(x, z)); rim.position.y += 0.012
    root.add(rim)
  }
  const surfaceDetails = new THREE.Group()
  addSurfaceDetails(surfaceDetails); root.add(surfaceDetails)
  // Pad outlines and bores are distinct: an oblong land does not imply an oblong drill.
  const lands: THREE.BufferGeometry[] = []
  for (const h of ARTWORK_HOLES) {
    for (const side of [0, 1]) {
      // Small round symbols exaggerate via lands in this assembly drawing. Keep the established visual annulus approximation.
      const w = h.shape === 'round' ? h.radius * 3.3 : h.padWidth
      const d = h.shape === 'round' ? h.radius * 3.3 : h.padDepth
      const land = padLandGeometry(h.shape, w, d, h.radius)
      land.rotateX(side ? Math.PI / 2 : -Math.PI / 2)
      land.translate(h.x - BOARD.width / 2, side ? -0.015 : BOARD.thickness + 0.015, h.z - BOARD.depth / 2)
      lands.push(land)
    }
  }
  const padLands = new THREE.Mesh(mergeGeometries(lands)!, materials.plated)
  padLands.name = 'plated-pad-lands'; padLands.receiveShadow = true; root.add(padLands)
  lands.forEach(land => land.dispose())
  const transform = new THREE.Object3D()
  // Larger assembly pad symbols identify through-hole assembly locations. Joint shape
  // and lead protrusion are visual approximations, not manufacturing dimensions.
  const throughHoles = ARTWORK_HOLES.filter(h => h.throughHole)
  const joints = new THREE.InstancedMesh(solderJointGeometry(), materials.solder, throughHoles.length)
  const topJoints = new THREE.InstancedMesh(joints.geometry, materials.solder, throughHoles.length)
  const leadEnds = new THREE.InstancedMesh(new THREE.CylinderGeometry(0.12, 0.12, 0.75, 8), materials.silver, throughHoles.length)
  throughHoles.forEach((h, i) => {
    transform.position.set(h.x - BOARD.width / 2, -0.018, h.z - BOARD.depth / 2)
    const spread = 0.95 + (i % 5) * 0.018 // Repeatable appearance variation, not measured joint tolerances.
    transform.rotation.set(Math.PI,0,0); transform.scale.set(h.radius * 1.68 * spread, 0.43, h.radius * 1.6)
    transform.updateMatrix(); joints.setMatrixAt(i, transform.matrix)
    transform.position.y = BOARD.thickness + 0.018; transform.rotation.x = 0
    transform.scale.y = 0.18; transform.updateMatrix(); topJoints.setMatrixAt(i, transform.matrix)
    transform.position.y = -0.34; transform.scale.set(1,1,1); transform.updateMatrix(); leadEnds.setMatrixAt(i, transform.matrix)
  })
  joints.name = 'through-hole-joints'; topJoints.name = 'top-solder-joints'; surfaceDetails.add(joints, topJoints, leadEnds)
  const parts: BoardPart[] = COMPONENTS.map((component) => {
    const pkg = createPackage(component)
    pkg.group.position.set(...worldPosition(component.x, component.z))
    pkg.group.userData.partId = component.id
    const element = document.createElement('div')
    element.className = 'scene-label'; element.textContent = component.ref
    const label = new CSS2DObject(element)
    label.position.set(0, component.h + 4.0, 0); label.visible = false
    // Label offset lives in world axes, so the component rotation must not rotate it.
    pkg.group.add(label); root.add(pkg.group)
    return { ...pkg, component, label, basePosition: pkg.group.position.clone() }
  })
  type HighlightVisual = {
    mesh: THREE.Mesh
    helper: THREE.Box3Helper
    ring: THREE.Mesh
    beam: THREE.Line
    light: THREE.PointLight
  }
  const highlightRoot = new THREE.Group()
  highlightRoot.name = 'component-highlights'
  root.add(highlightRoot)
  let highlightVisuals: HighlightVisual[] = []
  let highlightStart = 0
  let highlightDuration = 0

  const clearComponentHighlight = () => {
    for (const visual of highlightVisuals) {
      highlightRoot.remove(visual.mesh, visual.helper, visual.ring, visual.beam, visual.light)
      visual.mesh.geometry.dispose()
      ;(visual.mesh.material as THREE.Material).dispose()
      visual.helper.geometry.dispose()
      ;(visual.helper.material as THREE.Material).dispose()
      visual.ring.geometry.dispose()
      ;(visual.ring.material as THREE.Material).dispose()
      visual.beam.geometry.dispose()
      ;(visual.beam.material as THREE.Material).dispose()
    }
    highlightVisuals = []
    highlightDuration = 0
  }

  const setComponentHighlight = (ids: readonly string[], tone: HighlightTone, duration = 2200) => {
    clearComponentHighlight()
    const selected = new Set(ids)
    if (!selected.size) return
    const color = new THREE.Color({ accent: 0x2f7df6, ok: 0x1fa971, warn: 0xd98a16, bad: 0xe2483d, idle: 0x667085 }[tone])
    for (const part of parts) {
      if (!selected.has(part.component.id)) continue
      const box = new THREE.Box3().setFromObject(part.group)
      const size = box.getSize(new THREE.Vector3())
      const center = box.getCenter(new THREE.Vector3())
      const extent = Math.max(size.x, size.y, size.z, 5)
      const geometry = new RoundedBoxGeometry(size.x + 1.1, size.y + 1.1, size.z + 1.1, 2, 0.18)
      const material = new THREE.MeshBasicMaterial({ color, transparent: true, opacity: 0.16, depthWrite: false, blending: THREE.AdditiveBlending })
      const mesh = new THREE.Mesh(geometry, material)
      mesh.position.copy(center)
      mesh.renderOrder = 80
      const helper = new THREE.Box3Helper(box.clone().expandByScalar(0.45), color)
      const helperMaterial = helper.material as THREE.LineBasicMaterial
      helperMaterial.transparent = true
      helperMaterial.depthTest = false
      helperMaterial.opacity = 0.72
      helper.renderOrder = 81
      const ring = new THREE.Mesh(
        new THREE.RingGeometry(extent * 0.48, extent * 0.66, 36),
        new THREE.MeshBasicMaterial({ color, transparent: true, opacity: 0.62, depthTest: false, depthWrite: false, blending: THREE.AdditiveBlending, side: THREE.DoubleSide }),
      )
      ring.rotation.x = -Math.PI / 2
      ring.position.set(center.x, box.max.y + Math.max(3, extent * 0.8), center.z)
      ring.renderOrder = 82
      const beamGeometry = new THREE.BufferGeometry().setFromPoints([
        new THREE.Vector3(center.x, box.max.y + 0.2, center.z),
        new THREE.Vector3(center.x, ring.position.y, center.z),
      ])
      const beam = new THREE.Line(beamGeometry, new THREE.LineBasicMaterial({ color, transparent: true, opacity: 0.54, depthTest: false, depthWrite: false }))
      beam.renderOrder = 82
      const light = new THREE.PointLight(color, 5, extent * 3.2, 2)
      light.position.copy(center).add(new THREE.Vector3(0, extent * 1.25, 0))
      highlightRoot.add(mesh, helper, ring, beam, light)
      highlightVisuals.push({ mesh, helper, ring, beam, light })
    }
    highlightStart = performance.now()
    highlightDuration = duration
  }

  const updateComponentHighlight = (now: number) => {
    if (!highlightVisuals.length) return false
    const progress = Math.min(1, Math.max(0, (now - highlightStart) / highlightDuration))
    const pulse = 0.5 + 0.5 * Math.sin(progress * Math.PI * 4)
    const fade = 1 - Math.max(0, (progress - 0.72) / 0.28)
    for (const visual of highlightVisuals) {
      ;(visual.mesh.material as THREE.MeshBasicMaterial).opacity = (0.10 + 0.18 * pulse) * fade
      visual.mesh.scale.setScalar(0.985 + 0.035 * pulse)
      const helperMaterial = visual.helper.material as THREE.LineBasicMaterial
      helperMaterial.opacity = (0.42 + 0.52 * pulse) * fade
      ;(visual.ring.material as THREE.MeshBasicMaterial).opacity = (0.38 + 0.52 * pulse) * fade
      visual.ring.scale.setScalar(0.92 + 0.16 * pulse)
      ;(visual.beam.material as THREE.LineBasicMaterial).opacity = (0.30 + 0.48 * pulse) * fade
      visual.light.intensity = (3 + 4 * pulse) * fade
    }
    if (progress >= 1) {
      clearComponentHighlight()
      return false
    }
    return true
  }

  let state = DEFAULT_VISUAL_STATE
  const updateDisplay = () => {
    for (const part of parts) {
      const text = part.component.id === 'display-2' ? state.display.slice(4, 8) : state.display.slice(0, 4)
      part.setPower?.(state.powered, text)
      part.setLeds?.(state.powered ? state.ledMask : 0, state.ledColor)
    }
  }
  const setVisualState = (patch: Partial<BoardVisualState>) => {
    state = applyVisualPatch(state, patch); updateDisplay()
  }
  updateDisplay()
  return {
    root, parts,
    setComponentsVisible(visible: boolean) { surfaceDetails.visible = visible; highlightRoot.visible = visible; parts.forEach(p => { p.group.visible = visible }) },
    setCopperVisible(exposed: boolean) {
      for (const [mat, layers] of [[frontMaterial, frontLayers], [backMaterial, backLayers]] as const) {
        mat.map = exposed ? layers.exposed : layers.covered
        mat.metalness = exposed ? 0.8 : 0; mat.metalnessMap = exposed ? layers.surface : null
        mat.roughness = exposed ? 0.62 : 0.9; mat.roughnessMap = exposed ? null : layers.surface; mat.needsUpdate = true
      }
    },
    getVisualState: () => state,
    setVisualState,
    setPower: (value: boolean) => setVisualState({ powered: value }),
    setDisplayText: (text: string) => setVisualState({ display: text }),
    setComponentHighlight,
    updateComponentHighlight,
    setExplode(amount: number) {
      for (const part of parts) {
        part.group.position.copy(part.basePosition)
        part.group.position.x += part.basePosition.x * 0.16 * amount
        part.group.position.z += part.basePosition.z * 0.16 * amount
        part.group.position.y += (12 + part.component.h * 0.8) * amount
      }
    },
  }
}
