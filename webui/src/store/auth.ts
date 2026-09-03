// 鉴权态（登录态事实源）：docs/api.md §2.2 GET /api/auth/me。
// - 200 → 'in'（已登录，user 为契约 user 对象）
// - 401 → 'out'（未登录；数据层任何受保护端点 401 也会置此态，路由守卫跳 /login）
// - me 不可用（404/网络错等：BE-AUTH 未就绪或 server 不可达）→ 'open'：
//   契约未定义该情形，按开放访问放行——保持单机模式（L0）现有行为，避免集成期把全站锁死。
import { create } from 'zustand'
import { ApiError, api, setToken } from '@/lib/api'
import type { UserView } from '@/lib/types'

export type AuthStatus = 'loading' | 'in' | 'out' | 'open'

export interface AuthState {
  status: AuthStatus
  user: UserView | null
}

export const useAuth = create<AuthState>(() => ({ status: 'loading', user: null }))

/** 数据请求/实时通道是否放行（已登录或开放访问） */
export function authReady(status: AuthStatus): boolean {
  return status === 'in' || status === 'open'
}

let refreshing = false

/**
 * 刷新登录态：App 挂载与路由切换后调用（契约探针 me）。
 * 已有判定结果时静默重验（不回 loading，避免路由/骨架闪烁）；仅首帧探测显示一次骨架。
 */
export async function refreshAuth(): Promise<void> {
  if (refreshing) return
  refreshing = true
  try {
    const me = await api.me()
    useAuth.setState({ status: 'in', user: me.user ?? null })
  } catch (e) {
    if (e instanceof ApiError && e.status === 401) {
      useAuth.setState({ status: 'out', user: null })
    } else if (useAuth.getState().status === 'loading') {
      useAuth.setState({ status: 'open', user: null })
    }
    // 其余情形（已判定后 me 暂不可达）：保持现状，数据层 401 仍会全局收敛
  } finally {
    refreshing = false
  }
}

/** 数据层收到受保护端点 401：置未登录（路由守卫负责跳 /login，实时通道随状态断开） */
export function markUnauthenticated(): void {
  const st = useAuth.getState()
  if (st.status === 'out') return
  useAuth.setState({ status: 'out', user: null })
}

/** 登出：清 server 会话（尽力而为）+ 本机令牌，置未登录；WS 断开由 App 监听状态触发 */
export async function logout(): Promise<void> {
  try {
    await api.logout()
  } catch { /* 会话已失效或 server 不可达时，本机清理即可 */ }
  setToken('')
  useAuth.setState({ status: 'out', user: null })
}