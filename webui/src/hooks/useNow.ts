import { useEffect, useState } from 'react'

/** 每秒滴答的当前时间（时钟展示用，秒级渲染不整页重刷） */
export function useNow(intervalMs = 1000): Date {
  const [now, setNow] = useState(() => new Date())
  useEffect(() => {
    const t = setInterval(() => setNow(new Date()), intervalMs)
    return () => clearInterval(t)
  }, [intervalMs])
  return now
}