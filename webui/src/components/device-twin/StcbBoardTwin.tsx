import { useEffect, useLayoutEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import * as THREE from 'three'
import { OrbitControls } from 'three/addons/controls/OrbitControls.js'
import { loadBoardArtwork } from './vendor/stcb/board-artwork'
import { createBoardModel, type HighlightTone } from './vendor/stcb/board-model'
import { createStudio, type StudioTheme } from './vendor/stcb/render-studio'
import type { BoardVisualState } from './vendor/stcb/visual-state'

const HOME_CAMERA_POSITION = new THREE.Vector3(0, 220, 80)

type StcbBoardTwinProps = {
  cacheKey: string
  state: BoardVisualState
  label: string
  highlight?: { id: string; componentIDs: string[]; tone: HighlightTone }
}

type BoardRuntime = {
  key: string
  attach(host: HTMLDivElement): void
  detach(): void
  setTheme(theme: StudioTheme): void
  setVisualState(state: BoardVisualState): void
  setHighlight(componentIDs: string[], tone: HighlightTone): void
  dispose(): void
}

let cachedRuntime: BoardRuntime | null = null
let pendingRuntime: { key: string; promise: Promise<BoardRuntime> } | null = null

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

async function createRuntime(key: string, initialState: BoardVisualState, initialDark: boolean): Promise<BoardRuntime> {
  const artwork = await loadBoardArtwork()
  const scene = new THREE.Scene()
  const camera = new THREE.PerspectiveCamera(36, 1, 1, 1000)
  camera.position.copy(HOME_CAMERA_POSITION)

  const model = createBoardModel(artwork)
  model.setVisualState(initialState)
  scene.add(model.root)
  const bounds = new THREE.Box3().setFromObject(model.root)

  const renderer = new THREE.WebGLRenderer({ antialias: true, alpha: false, powerPreference: 'high-performance' })
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

  const studio = createStudio(scene, renderer, camera, initialDark ? 'dark' : 'light')
  const controls = new OrbitControls(camera, renderer.domElement)
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
  let disposed = false
  let host: HTMLDivElement | null = null
  let observer: ResizeObserver | null = null
  let frame = 0
  let viewportWidth = 0
  let viewportHeight = 0

  const invalidate = () => {
    if (disposed || frame || !host) return
    frame = requestAnimationFrame(() => {
      frame = 0
      if (disposed || !host) return
      const moving = controls.update()
      const animating = model.updateComponentHighlight(performance.now())
      studio.render()
      if (moving || animating) invalidate()
    })
  }

  const resize = () => {
    if (!host) return
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
    controls.maxDistance = Math.max(320, distance * 1.8)
    if (homeView) {
      camera.position.copy(defaultTarget).addScaledVector(baseDirection, distance)
      controls.target.copy(defaultTarget)
    }
    if (viewportWidth !== width || viewportHeight !== height) {
      viewportWidth = width
      viewportHeight = height
      renderer.setSize(width, height, false)
      studio.resize(width, height)
    }
    controls.update()
    invalidate()
  }

  controls.addEventListener('start', () => { homeView = false })
  controls.addEventListener('change', invalidate)

  return {
    key,
    attach(nextHost) {
      if (disposed) return
      if (host === nextHost && renderer.domElement.parentElement === nextHost) {
        resize()
        return
      }
      host = nextHost
      nextHost.appendChild(renderer.domElement)
      observer?.disconnect()
      observer = new ResizeObserver(resize)
      observer.observe(nextHost)
      resize()
    },
    detach() {
      if (frame) cancelAnimationFrame(frame)
      frame = 0
      observer?.disconnect()
      observer = null
      renderer.domElement.remove()
      host = null
    },
    setTheme(theme) {
      studio.setTheme(theme)
      invalidate()
    },
    setVisualState(state) {
      model.setVisualState(state)
      invalidate()
    },
    setHighlight(componentIDs, tone) {
      model.setComponentHighlight(componentIDs, tone)
      invalidate()
    },
    dispose() {
      disposed = true
      if (frame) cancelAnimationFrame(frame)
      frame = 0
      observer?.disconnect()
      observer = null
      renderer.domElement.remove()
      controls.dispose()
      studio.dispose()
      disposeObject3D(scene)
      renderer.dispose()
      renderer.forceContextLoss()
    },
  }
}

function getCachedRuntime(key: string): BoardRuntime | null {
  return cachedRuntime?.key === key ? cachedRuntime : null
}

function acquireRuntime(key: string, state: BoardVisualState, dark: boolean): Promise<BoardRuntime> {
  const cached = getCachedRuntime(key)
  if (cached) return Promise.resolve(cached)
  if (pendingRuntime?.key === key) return pendingRuntime.promise

  // A device transition can overlap the previous scene build. Serialize the
  // swap instead of racing two WebGL contexts for the single cache slot.
  if (pendingRuntime) {
    const previous = pendingRuntime
    return previous.promise.catch(() => undefined).then((runtime) => {
      runtime?.dispose()
      if (cachedRuntime === runtime) cachedRuntime = null
      return acquireRuntime(key, state, dark)
    })
  }

  if (cachedRuntime) {
    cachedRuntime.dispose()
    cachedRuntime = null
  }

  const promise = createRuntime(key, state, dark).then((runtime) => {
    cachedRuntime = runtime
    return runtime
  })
  pendingRuntime = { key, promise }
  void promise.finally(() => {
    if (pendingRuntime?.promise === promise) pendingRuntime = null
  })
  return promise
}

/** Device-page renderer. It intentionally exposes no controls beyond orbit/zoom. */
export default function StcbBoardTwin({ cacheKey, state, label, highlight }: StcbBoardTwinProps) {
  const { t } = useTranslation('devices')
  const hostRef = useRef<HTMLDivElement>(null)
  const runtimeRef = useRef<BoardRuntime | null>(null)
  const stateRef = useRef(state)
  const darkTheme = useDarkDocumentTheme()
  const darkThemeRef = useRef(darkTheme)
  const highlightRef = useRef(highlight)
  const [status, setStatus] = useState<'loading' | 'ready' | 'error'>('loading')
  stateRef.current = state
  darkThemeRef.current = darkTheme
  highlightRef.current = highlight

  useLayoutEffect(() => {
    const host = hostRef.current
    if (!host) return
    let cancelled = false

    const attach = (runtime: BoardRuntime) => {
      if (cancelled) return
      runtime.attach(host)
      runtime.setTheme(darkThemeRef.current ? 'dark' : 'light')
      runtime.setVisualState(stateRef.current)
      if (highlightRef.current) runtime.setHighlight(highlightRef.current.componentIDs, highlightRef.current.tone)
      runtimeRef.current = runtime
      setStatus('ready')
    }

    // Reuse the existing canvas before paint when switching tabs, so the
    // cached WebGL scene never falls back to a loading placeholder.
    const cached = getCachedRuntime(cacheKey)
    if (cached) {
      attach(cached)
    } else {
      setStatus('loading')
      void acquireRuntime(cacheKey, stateRef.current, darkThemeRef.current).then(attach).catch(() => {
        if (!cancelled) setStatus('error')
      })
    }

    return () => {
      cancelled = true
      runtimeRef.current?.detach()
      runtimeRef.current = null
    }
  }, [cacheKey])

  useEffect(() => {
    runtimeRef.current?.setTheme(darkTheme ? 'dark' : 'light')
  }, [darkTheme])

  useEffect(() => {
    runtimeRef.current?.setVisualState(state)
  }, [state])

  useEffect(() => {
    if (highlight) runtimeRef.current?.setHighlight(highlight.componentIDs, highlight.tone)
  }, [highlight])

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
