import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import * as THREE from 'three'
import { OrbitControls } from 'three/addons/controls/OrbitControls.js'
import { loadBoardArtwork } from './vendor/stcb/board-artwork'
import { createBoardModel } from './vendor/stcb/board-model'
import { createStudio, type StudioTheme } from './vendor/stcb/render-studio'
import type { BoardVisualState } from './vendor/stcb/visual-state'

const HOME_CAMERA_POSITION = new THREE.Vector3(0, 220, 80)

type StcbBoardTwinProps = {
  state: BoardVisualState
  label: string
}

function disposeObject3D(root: THREE.Object3D) {
  const geometries = new Set<THREE.BufferGeometry>()
  const materials = new Set<THREE.Material>()
  const textures = new Set<THREE.Texture>()

  root.traverse((object) => {
    const renderable = object as THREE.Mesh
    if (renderable.geometry) geometries.add(renderable.geometry)
    const material = renderable.material
    if (!material) return
    for (const item of Array.isArray(material) ? material : [material]) {
      materials.add(item)
      for (const value of Object.values(item)) {
        if (value instanceof THREE.Texture) textures.add(value)
      }
    }
  })

  textures.forEach((texture) => texture.dispose())
  materials.forEach((material) => material.dispose())
  geometries.forEach((geometry) => geometry.dispose())
}

function documentIsDark() {
  return document.documentElement.classList.contains('dark')
}

function useDarkDocumentTheme() {
  const [dark, setDark] = useState(documentIsDark)
  useEffect(() => {
    const root = document.documentElement
    const sync = () => setDark(root.classList.contains('dark'))
    const observer = new MutationObserver(sync)
    observer.observe(root, { attributes: true, attributeFilter: ['class'] })
    sync()
    return () => observer.disconnect()
  }, [])
  return dark
}

/** Device-page renderer. It intentionally exposes no controls beyond orbit/zoom. */
export default function StcbBoardTwin({ state, label }: StcbBoardTwinProps) {
  const { t } = useTranslation('devices')
  const hostRef = useRef<HTMLDivElement>(null)
  const modelRef = useRef<ReturnType<typeof createBoardModel> | null>(null)
  const studioRef = useRef<ReturnType<typeof createStudio> | null>(null)
  const invalidateRef = useRef<(() => void) | null>(null)
  const stateRef = useRef(state)
  const darkTheme = useDarkDocumentTheme()
  const darkThemeRef = useRef(darkTheme)
  const [status, setStatus] = useState<'loading' | 'ready' | 'error'>('loading')
  stateRef.current = state
  darkThemeRef.current = darkTheme

  useEffect(() => {
    const host = hostRef.current
    if (!host) return
    let disposed = false
    let frame = 0
    let renderer: THREE.WebGLRenderer | null = null
    let studio: ReturnType<typeof createStudio> | null = null
    let controls: OrbitControls | null = null
    let observer: ResizeObserver | null = null
    const scene = new THREE.Scene()
    const camera = new THREE.PerspectiveCamera(36, 1, 1, 1000)
    camera.position.copy(HOME_CAMERA_POSITION)

    const cleanup = () => {
      disposed = true
      if (frame) cancelAnimationFrame(frame)
      observer?.disconnect()
      invalidateRef.current = null
      controls?.dispose()
      studio?.dispose()
      studioRef.current = null
      modelRef.current = null
      disposeObject3D(scene)
      if (renderer) {
        renderer.dispose()
        renderer.forceContextLoss()
        renderer.domElement.remove()
      }
    }

    void (async () => {
      try {
        const artwork = await loadBoardArtwork()
        if (disposed) return

        const model = createBoardModel(artwork)
        modelRef.current = model
        model.setVisualState(stateRef.current)
        scene.add(model.root)
        const bounds = new THREE.Box3().setFromObject(model.root)

        renderer = new THREE.WebGLRenderer({ antialias: true, alpha: false, powerPreference: 'high-performance' })
        renderer.setPixelRatio(Math.min(window.devicePixelRatio, 1.75))
        renderer.shadowMap.enabled = true
        renderer.shadowMap.type = THREE.PCFShadowMap
        renderer.toneMapping = THREE.ACESFilmicToneMapping
        renderer.toneMappingExposure = 0.95
        renderer.outputColorSpace = THREE.SRGBColorSpace
        renderer.domElement.style.display = 'block'
        renderer.domElement.style.width = '100%'
        renderer.domElement.style.height = '100%'
        renderer.domElement.setAttribute('aria-hidden', 'true')
        host.appendChild(renderer.domElement)

        studio = createStudio(scene, renderer, camera, darkThemeRef.current ? 'dark' : 'light')
        studioRef.current = studio
        controls = new OrbitControls(camera, renderer.domElement)
        controls.enableDamping = true
        controls.dampingFactor = 0.075
        controls.enablePan = false
        controls.minDistance = 42
        controls.maxDistance = 320
        controls.maxPolarAngle = Math.PI * 0.52
        controls.target.set(0, 1, 0)

        const defaultTarget = new THREE.Vector3(0, 1, 0)
        const baseDirection = HOME_CAMERA_POSITION.clone().sub(defaultTarget).normalize()
        const homeRight = new THREE.Vector3().crossVectors(new THREE.Vector3(0, 1, 0), baseDirection).normalize()
        const homeUp = new THREE.Vector3().crossVectors(baseDirection, homeRight)
        let homeView = true
        controls.addEventListener('start', () => { homeView = false })

        const invalidate = () => {
          if (disposed || frame) return
          frame = requestAnimationFrame(() => {
            frame = 0
            if (disposed) return
            const moving = controls!.update()
            studio!.render()
            if (moving) invalidate()
          })
        }
        invalidateRef.current = invalidate
        controls.addEventListener('change', invalidate)

        const resize = () => {
          const width = Math.max(1, host.clientWidth)
          const height = Math.max(1, host.clientHeight)
          const aspect = width / height
          camera.aspect = aspect
          camera.updateProjectionMatrix()
          const tan = Math.tan(THREE.MathUtils.degToRad(camera.fov / 2))
          let distance = 171
          for (const x of [bounds.min.x, bounds.max.x]) for (const y of [bounds.min.y, bounds.max.y]) for (const z of [bounds.min.z, bounds.max.z]) {
            const point = new THREE.Vector3(x, y, z).sub(defaultTarget)
            distance = Math.max(distance, point.dot(baseDirection) + Math.max(Math.abs(point.dot(homeRight)) / (tan * aspect), Math.abs(point.dot(homeUp)) / tan) / 1.45)
          }
          controls!.maxDistance = Math.max(320, distance * 1.8)
          if (homeView) {
            camera.position.copy(defaultTarget).addScaledVector(baseDirection, distance)
            controls!.target.copy(defaultTarget)
          }
          renderer!.setSize(width, height, false)
          studio!.resize(width, height)
          controls!.update()
          invalidate()
        }

        observer = new ResizeObserver(resize)
        observer.observe(host)
        resize()
        setStatus('ready')
        invalidate()
      } catch {
        if (!disposed) setStatus('error')
      }
    })()

    return cleanup
  }, [])

  useEffect(() => {
    const theme: StudioTheme = darkTheme ? 'dark' : 'light'
    studioRef.current?.setTheme(theme)
    invalidateRef.current?.()
  }, [darkTheme])

  useEffect(() => {
    modelRef.current?.setVisualState(state)
    invalidateRef.current?.()
  }, [state])

  return (
    <div className="relative h-full w-full" role="img" aria-label={label}>
      <div ref={hostRef} className="h-full w-full" aria-hidden="true" />
      {status !== 'ready' && (
        <p className="absolute inset-0 grid place-items-center px-6 text-center text-body text-ink-2">
          {status === 'loading' ? t('twin.loading') : t('twin.loadFailed')}
        </p>
      )}
    </div>
  )
}
