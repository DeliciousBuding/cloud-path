import { useEffect, useState } from 'react'

const QUERY = '(prefers-reduced-motion: reduce)'

/**
 * 系统「减少动效」偏好（实时跟随）。
 * index.css 已全局把 animation/transition 压到 0.001ms 兜底；
 * 本 hook 供 JS 侧控制条件类名（如 fade-up 进场、toast 弹出）彻底不挂动画。
 */
export function useReducedMotion(): boolean {
  const [reduced, setReduced] = useState(() => window.matchMedia(QUERY).matches)
  useEffect(() => {
    const mq = window.matchMedia(QUERY)
    const onChange = () => setReduced(mq.matches)
    mq.addEventListener('change', onChange)
    setReduced(mq.matches) // 挂载时同步一次（订阅前可能已变化）
    return () => mq.removeEventListener('change', onChange)
  }, [])
  return reduced
}