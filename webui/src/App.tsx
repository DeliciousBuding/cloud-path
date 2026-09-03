import { Suspense, lazy, useEffect } from 'react'
import { Route, Routes } from 'react-router'
import Layout from '@/components/Layout'
import { ErrorBoundary } from '@/components/ErrorBoundary'
import { PageSkeleton } from '@/components/Skeleton'
import { connectLive } from '@/store/ws'
import { applyTheme, watchSystemTheme } from '@/lib/theme'
import Dashboard from '@/pages/Dashboard'
import Devices from '@/pages/Devices'

// 重路由按需加载：图表（recharts）只在设备详情用到，不拖慢首屏
const DeviceDetail = lazy(() => import('@/pages/DeviceDetail'))
const Events = lazy(() => import('@/pages/Events'))
const Edges = lazy(() => import('@/pages/Edges'))
const Settings = lazy(() => import('@/pages/Settings'))
const NotFound = lazy(() => import('@/pages/NotFound'))

export default function App() {
  useEffect(() => {
    applyTheme()
    const unwatch = watchSystemTheme()
    connectLive()
    return unwatch
  }, [])

  return (
    <ErrorBoundary>
      <Suspense fallback={<PageSkeleton />}>
        <Routes>
          <Route element={<Layout />}>
            <Route index element={<Dashboard />} />
            <Route path="devices" element={<Devices />} />
            <Route path="devices/:edgeId/:deviceId" element={<DeviceDetail />} />
            <Route path="events" element={<Events />} />
            <Route path="edges" element={<Edges />} />
            <Route path="settings" element={<Settings />} />
            <Route path="*" element={<NotFound />} />
          </Route>
        </Routes>
      </Suspense>
    </ErrorBoundary>
  )
}
