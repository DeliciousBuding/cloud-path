import { Suspense, lazy, useEffect } from 'react'
import { Navigate, Outlet, Route, Routes, useLocation } from 'react-router'
import Layout from '@/components/Layout'
import { ErrorBoundary } from '@/components/ErrorBoundary'
import { PageSkeleton } from '@/components/Skeleton'
import { refreshAuth, useAuth } from '@/store/auth'
import { connectLive, disconnectLive } from '@/store/ws'
import { applyTheme, watchSystemTheme } from '@/lib/theme'
import Overview from '@/pages/Overview'
import Devices from '@/pages/Devices'

// 认证页（FE-DESIGN 提供的全屏壳，不套 Layout 侧栏）：懒加载
const Login = lazy(() => import('@/pages/Login'))
const Setup = lazy(() => import('@/pages/Setup'))

// 重路由按需加载：图表（recharts）只在设备详情用到，不拖慢首屏
const DeviceDetail = lazy(() => import('@/pages/DeviceDetail'))
const Events = lazy(() => import('@/pages/Events'))
const Edges = lazy(() => import('@/pages/Edges'))
const Settings = lazy(() => import('@/pages/Settings'))
// 管理页（用户/服务令牌）：页面自身按 me.role 收口，非 admin 只看到「需要管理员权限」空态
const Admin = lazy(() => import('@/pages/Admin'))
const NotFound = lazy(() => import('@/pages/NotFound'))

/** 登录态探测中的整页占位（与 Layout 相同的内容宽度约束，390px 无横向溢出） */
function AuthProbeFallback() {
  return (
    <div className="mx-auto max-w-6xl px-4 py-7 sm:px-6 lg:px-10">
      <PageSkeleton />
    </div>
  )
}

/**
 * 路由守卫：保护 Layout 内全部业务页。
 * 登录态事实源 = GET /api/auth/me（docs/api.md §2.2）：
 * 未登录（401）→ 跳 /login；me 不可用（BE-AUTH 未就绪/断网）→ 按开放访问放行。
 * 状态机与理由见 store/auth.ts 头注释。
 */
function RequireAuth() {
  const status = useAuth((s) => s.status)
  if (status === 'loading') return <AuthProbeFallback />
  if (status === 'out') return <Navigate to="/login" replace />
  return <Outlet />
}

/** /login：已登录（me→200）跳首页；未登录/开放访问渲染登录页 */
function LoginRoute() {
  const status = useAuth((s) => s.status)
  if (status === 'in') return <Navigate to="/" replace />
  if (status === 'loading') return <AuthProbeFallback />
  return <Login />
}

export default function App() {
  const authStatus = useAuth((s) => s.status)
  const { pathname } = useLocation()

  useEffect(() => {
    applyTheme()
    const unwatch = watchSystemTheme()
    return unwatch
  }, [])

  // 登录态探测：挂载一次 + 路由切换后重验（已判定时静默，不闪骨架；登录成功跳转后即在此收敛）
  useEffect(() => { void refreshAuth() }, [pathname])

  // 实时通道跟随登录态：已登录/开放访问才连接；登出即断开并停止重连（避免 401 重连风暴）
  useEffect(() => {
    if (authStatus === 'in' || authStatus === 'open') connectLive()
    else disconnectLive()
  }, [authStatus])

  return (
    <ErrorBoundary>
      <Suspense fallback={<PageSkeleton />}>
        <Routes>
          <Route path="/login" element={<LoginRoute />} />
          <Route path="/setup" element={<Setup />} />
          <Route element={<Layout />}>
            <Route element={<RequireAuth />}>
              <Route index element={<Overview />} />
              <Route path="devices" element={<Devices />} />
              <Route path="devices/:edgeId/:deviceId" element={<DeviceDetail />} />
              <Route path="events" element={<Events />} />
              <Route path="edges" element={<Edges />} />
              <Route path="settings" element={<Settings />} />
              <Route path="admin" element={<Admin />} />
              <Route path="*" element={<NotFound />} />
            </Route>
          </Route>
        </Routes>
      </Suspense>
    </ErrorBoundary>
  )
}