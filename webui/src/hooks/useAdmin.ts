// 管理面数据层（docs/api.md §3.2-3.3）。
// 刻意不用 useMutation 承载「创建令牌」：mutationCache 会保留结果对象，
// 而明文只允许活在 TokenManager 的组件 state 里（关闭面板/卸载即消失）。
// 其余无密文读写走 Query + Mutation，写成功后只失效对应 query key。
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '@/lib/api'
import { useIsAdmin } from '@/store/auth'
import type { CreateUserInput, UpdateUserInput } from '@/lib/types'

export const ADMIN_USERS_KEY = ['admin', 'users'] as const
export const ADMIN_TOKENS_KEY = ['admin', 'tokens'] as const

/** 用户列表：非 admin 时 query 直接禁用（不发请求，也就没有可渲染的数据） */
export function useAdminUsers() {
  const admin = useIsAdmin()
  return useQuery({
    queryKey: ADMIN_USERS_KEY, queryFn: api.users, enabled: admin, staleTime: 15_000,
  })
}

/** 令牌元数据列表（只有 prefix，无明文） */
export function useAdminTokens() {
  const admin = useIsAdmin()
  return useQuery({
    queryKey: ADMIN_TOKENS_KEY, queryFn: api.tokens, enabled: admin, staleTime: 15_000,
  })
}

export function useCreateUser() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (input: CreateUserInput) => api.createUser(input),
    onSuccess: () => { void qc.invalidateQueries({ queryKey: ADMIN_USERS_KEY }) },
  })
}

export function useUpdateUser() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, patch }: { id: number; patch: UpdateUserInput }) => api.updateUser(id, patch),
    onSuccess: () => { void qc.invalidateQueries({ queryKey: ADMIN_USERS_KEY }) },
  })
}

export function useRevokeToken() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: number) => api.revokeToken(id),
    onSuccess: () => { void qc.invalidateQueries({ queryKey: ADMIN_TOKENS_KEY }) },
  })
}