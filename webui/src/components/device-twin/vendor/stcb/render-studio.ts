/** Camera-independent bench lighting and a linear-light contact-shadow render pipeline. */
import * as THREE from 'three'
import { EffectComposer } from 'three/addons/postprocessing/EffectComposer.js'
import { RenderPass } from 'three/addons/postprocessing/RenderPass.js'
import { SSAOPass } from 'three/addons/postprocessing/SSAOPass.js'
import { OutputPass } from 'three/addons/postprocessing/OutputPass.js'

type Camera = THREE.PerspectiveCamera | THREE.OrthographicCamera
export type StudioTheme = 'light' | 'dark'

const STUDIO_THEMES: Record<StudioTheme, { background: number; backgroundIntensity: number; floor: number; floorIntensity: number }> = {
  light: { background: 0xffffff, backgroundIntensity: 5, floor: 0xffffff, floorIntensity: 5 },
  dark: { background: 0x181c22, backgroundIntensity: 1, floor: 0x232933, floorIntensity: 1 },
}

function themedColor(hex: number, intensity: number): THREE.Color {
  const color = new THREE.Color(hex)
  return intensity === 1 ? color : color.multiplyScalar(intensity)
}

export function createStudio(scene: THREE.Scene, renderer: THREE.WebGLRenderer, camera: Camera, initialTheme: StudioTheme = 'dark') {
  renderer.info.autoReset = false // Count the whole multipass frame, not only OutputPass.
  // No near-field fog: the former 150 mm fog start washed out the board itself.
  const initialPalette = STUDIO_THEMES[initialTheme]
  scene.background = themedColor(initialPalette.background, initialPalette.backgroundIntensity)
  const environment = new THREE.Scene()
  environment.background = new THREE.Color(0x7c8390)
  const panels: THREE.Mesh[] = []
  for (const [x, y, z, w, h, intensity] of [
    [-105, 85, 15, 70, 110, 3], [90, 35, 45, 40, 100, 1.6], [10, 45, -110, 120, 30, 2.4],
  ]) {
    const panel = new THREE.Mesh(new THREE.PlaneGeometry(w, h),
      new THREE.MeshBasicMaterial({ color: new THREE.Color(1, 0.97, 0.92).multiplyScalar(intensity) }))
    panel.position.set(x, y, z); panel.lookAt(0, 0, 0); environment.add(panel); panels.push(panel)
  }
  const pmrem = new THREE.PMREMGenerator(renderer)
  const target = pmrem.fromScene(environment, 0.025)
  scene.environment = target.texture; scene.environmentIntensity = 0.85
  panels.forEach(panel => { panel.geometry.dispose(); (panel.material as THREE.Material).dispose() }); pmrem.dispose()
  const ambient = new THREE.HemisphereLight(0xe8eef7, 0x343342, 0.5)
  scene.add(ambient)
  const key = new THREE.DirectionalLight(0xfff1df, 2.2)
  key.position.set(-60, 100, 20)
  key.castShadow = true; key.shadow.mapSize.set(2048, 2048)
  Object.assign(key.shadow.camera, { left: -75, right: 75, top: 65, bottom: -65, near: 1, far: 260 })
  key.shadow.radius = 3.5
  key.shadow.bias = -0.000025; key.shadow.normalBias = 0.025
  scene.add(key)
  const fill = new THREE.DirectionalLight(0xd4e4ff, 1.15)
  fill.position.set(65, 48, 35); scene.add(fill)
  const rim = new THREE.DirectionalLight(0xffffff, 0.9)
  rim.position.set(10, 30, -85); scene.add(rim)
  const backLight = new THREE.DirectionalLight(0xe4eeff, 2.8)
  backLight.position.set(-45, -95, -60); backLight.visible = false; scene.add(backLight)
  const floorMaterial = new THREE.MeshStandardMaterial({ color: themedColor(initialPalette.floor, initialPalette.floorIntensity), roughness: 0.96 })
  const floor = new THREE.Mesh(new THREE.PlaneGeometry(2000, 2000), floorMaterial)
  floor.rotation.x = -Math.PI / 2; floor.position.y = -0.74
  floor.receiveShadow = true; scene.add(floor)
  const grid = new THREE.GridHelper(190, 38, 0x68788a, 0x46515f)
  grid.position.y = -0.72; grid.material.transparent = true; grid.material.opacity = 0.12
  grid.visible = false; scene.add(grid)

  const renderTarget = new THREE.WebGLRenderTarget(1, 1, { type: THREE.HalfFloatType, samples: 4 })
  const composer = new EffectComposer(renderer, renderTarget)
  const renderPass = new RenderPass(scene, camera)
  const ao = new SSAOPass(scene, camera, 1, 1, 16)
  ao.kernelRadius = 1.6; ao.minDistance = 0.00008; ao.maxDistance = 0.003
  // Fix kernel and noise so identical camera/state yields repeatable visual evidence.
  ao.kernel.forEach((v, i) => {
    const angle = i * 2.399963, z = (i + 0.5) / ao.kernel.length
    v.set(Math.cos(angle) * Math.sqrt(1 - z * z), Math.sin(angle) * Math.sqrt(1 - z * z), z)
      .multiplyScalar(0.1 + 0.9 * z * z)
  })
  const noise = ao.noiseTexture.image.data as Float32Array
  for (let i = 0; i < noise.length; i++) noise[i] = Math.sin(i * 12.9898 + 78.233)
  ao.noiseTexture.needsUpdate = true
  composer.addPass(renderPass); composer.addPass(ao)
  composer.addPass(new OutputPass())
  let renderedFrames = 0
  let activeCamera = camera
  let gridEnabled = false, backMode = false
  return {
    resize(width: number, height: number) { composer.setSize(width, height) },
    setCamera(next: Camera) {
      activeCamera = next; renderPass.camera = next; ao.camera = next
      const perspective = next instanceof THREE.PerspectiveCamera ? 1 : 0
      if (ao.ssaoMaterial.defines.PERSPECTIVE_CAMERA !== perspective) {
        ao.ssaoMaterial.defines.PERSPECTIVE_CAMERA = perspective; ao.ssaoMaterial.needsUpdate = true
        ao.depthRenderMaterial.defines.PERSPECTIVE_CAMERA = perspective; ao.depthRenderMaterial.needsUpdate = true
      }
    },
    setBackView(back: boolean) { backMode = back; floor.visible = !back; backLight.visible = back; grid.visible = gridEnabled && !back },
    setAO(value: boolean) { ao.enabled = value },
    setGrid(value: boolean) { gridEnabled = value; grid.visible = value && !backMode },
    setTheme(theme: StudioTheme) {
      const palette = STUDIO_THEMES[theme]
      if (scene.background instanceof THREE.Color) scene.background.copy(themedColor(palette.background, palette.backgroundIntensity))
      floorMaterial.color.copy(themedColor(palette.floor, palette.floorIntensity))
    },
    frameStats() { return { renderedFrames, calls: renderer.info.render.calls, triangles: renderer.info.render.triangles, geometries: renderer.info.memory.geometries, textures: renderer.info.memory.textures } },
    dispose() {
      composer.dispose()
      renderTarget.dispose()
      target.dispose()
      floor.geometry.dispose(); (floor.material as THREE.Material).dispose()
      grid.geometry.dispose(); (grid.material as THREE.Material).dispose()
      scene.remove(ambient, key, fill, rim, backLight, floor, grid)
    },
    render() {
      renderer.info.reset()
      renderedFrames++
      const uniforms = ao.ssaoMaterial.uniforms
      uniforms.cameraNear.value = activeCamera.near; uniforms.cameraFar.value = activeCamera.far
      uniforms.cameraProjectionMatrix.value.copy(activeCamera.projectionMatrix)
      uniforms.cameraInverseProjectionMatrix.value.copy(activeCamera.projectionMatrixInverse)
      composer.render()
    },
  }
}
