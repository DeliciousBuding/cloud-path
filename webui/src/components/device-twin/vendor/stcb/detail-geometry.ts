/** Small package geometry in millimetres. Decorative radii/fillets are not manufacturing tolerances. */
import * as THREE from 'three'

/** Solder rises concavely from an outer pad toe (+x) to a wetted termination (-x). */
export function solderFilletGeometry() {
  const profile = new THREE.Shape()
  profile.moveTo(-0.5, 0); profile.lineTo(0.5, 0); profile.lineTo(0.5, 0.08)
  profile.quadraticCurveTo(0.5, 0.17, 0.36, 0.18)
  profile.bezierCurveTo(-0.12, 0.2, -0.39, 0.35, -0.45, 0.94)
  profile.quadraticCurveTo(-0.48, 1, -0.5, 1); profile.closePath()
  return new THREE.ExtrudeGeometry(profile, { depth: 1, steps: 1, bevelEnabled: false, curveSegments: 6 }).translate(0, 0, -0.5)
}

/** Through-hole solder meniscus with an actual lead opening, local +y away from the PCB. */
export function solderJointGeometry() {
  return new THREE.LatheGeometry([
    new THREE.Vector2(1, 0), new THREE.Vector2(0.99, 0.09), new THREE.Vector2(0.85, 0.26),
    new THREE.Vector2(0.55, 0.48), new THREE.Vector2(0.27, 0.8), new THREE.Vector2(0.24, 1),
    new THREE.Vector2(0.14, 1), new THREE.Vector2(0.14, 0),
  ], 24)
}

/** Outer pad keeps its reference-artwork shape; the drill remains circular because no slot/drill file exists. */
export function padLandGeometry(shape: string, w: number, d: number, drillRadius: number) {
  const contour = new THREE.Shape()
  if (shape === 'square') {
    contour.moveTo(-w / 2, -d / 2); contour.lineTo(w / 2, -d / 2)
    contour.lineTo(w / 2, d / 2); contour.lineTo(-w / 2, d / 2); contour.closePath()
  } else if (shape === 'oval') {
    const r = w / 2, half = Math.max(0, (d - w) / 2)
    contour.absarc(0, half, r, 0, Math.PI, false)
    contour.lineTo(-r, -half); contour.absarc(0, -half, r, Math.PI, 2 * Math.PI, false); contour.closePath()
  } else contour.absellipse(0, 0, w / 2, d / 2, 0, Math.PI * 2, false, 0)
  const drill = new THREE.Path(); drill.absarc(0, 0, drillRadius, 0, Math.PI * 2, true); contour.holes.push(drill)
  return new THREE.ShapeGeometry(contour, 24)
}

/** Two leads share one end, bend smoothly to independently registered board-pad centers. */
export function radialLeadGeometry(start: THREE.Vector3, end: THREE.Vector3, radius: number) {
  const control1 = start.clone().lerp(end, 0.38); control1.y = start.y
  const control2 = start.clone().lerp(end, 0.75); control2.y = Math.max(0.15, end.y + 0.28)
  return new THREE.TubeGeometry(new THREE.CubicBezierCurve3(start, control1, control2, end), 12, radius, 8, false)
}
