// 组件/页面用例的统一外壳：
//   QueryClient（关掉重试与缓存，404/401 用例不必等退避）+ MemoryRouter + 主题类名复位。
// store 复位单独导出，用例在 beforeEach 里显式调用（zustand 是模块级单例，跨用例会串味）。
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router'
import { render } from '@testing-library/react'
import type { ReactElement } from 'react'
import { useAuth } from '@/store/auth'
import { useLive } from '@/store/ws'
import { useToasts } from '@/store/toast'

export function renderWithProviders(ui: ReactElement, route = '/') {
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: 0, staleTime: 0, refetchOnWindowFocus: false },
      mutations: { retry: false },
    },
  })
  const utils = render(
    <QueryClientProvider client={queryClient}>
      <MemoryRouter initialEntries={[route]}>{ui}</MemoryRouter>
    </QueryClientProvider>,
  )
  return { ...utils, queryClient }
}

/** 单例 store 复位（登录态 / 实时态 / 轻提示） */
export function resetStores(): void {
  useAuth.setState({ status: 'loading', user: null })
  useLive.setState({
    status: 'closed', failures: 0, domainRecord: null, connectionEpoch: 0, devices: {}, edges: {}, events: [], series: {}, descriptors: {}, acks: {},
  })
  useToasts.setState({ items: [] })
  document.documentElement.classList.remove('dark')
}