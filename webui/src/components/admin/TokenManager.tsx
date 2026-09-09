// 服务令牌面板（docs/api.md §3.3）：TanStack Table + shadcn 风格工具栏 + 抽屉创建。
//
// 明文的唯一落点仍是下面的 `secret` 组件 state：
//   - 创建响应到达 → setSecret(created) → SecretPanel 一次性展示
//   - 关闭面板 → setSecret(null) → DOM 里再无任何明文
//   - 组件卸载（切页/登出）→ state 随之消失
// 刻意不走 useMutation：mutationCache 会保留结果对象，超出「组件内存」的范围。
import { useDeferredValue, useEffect, useMemo, useState } from 'react'
import { createColumnHelper } from '@tanstack/react-table'
import { KeyRound, Plus } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { useQueryClient } from '@tanstack/react-query'
import { Button, Panel } from '@/components/ui'
import { RowSkeleton } from '@/components/Skeleton'
import { Badge } from '@/components/ui'
import { Drawer, DrawerContent, DrawerDescription, DrawerHeader, DrawerTitle } from '@/components/ui/drawer'
import { TableCell, TableRow } from '@/components/ui/table'
import { DataTable, DataTableColumnHeader, DataTableToolbar, useDataTable, dataTableFeatures } from '@/components/data-table'
import { CreateTokenForm } from './CreateTokenForm'
import { ErrorNote } from './ErrorNote'
import { SecretPanel } from './SecretPanel'
import { TokenActions } from './TokenActions'
import { ADMIN_TOKENS_KEY, useAdminTokens } from '@/hooks/useAdmin'
import { adminErrorMessage } from '@/lib/admin'
import { fmtDateTime, timeAgo } from '@/lib/format'
import { toast } from '@/store/toast'
import type { CreatedToken, TokenScope, TokenView } from '@/lib/types'

const tokenColumn = createColumnHelper<typeof dataTableFeatures, TokenView>()

type TokenState = 'valid' | 'expired' | 'revoked'

function tokenState(token: TokenView): TokenState {
  if (token.revoked_at) return 'revoked'
  if (token.expires_at && token.expires_at * 1000 <= Date.now()) return 'expired'
  return 'valid'
}

function scopeTone(scope: TokenScope): string {
  return scope === 'admin' || scope === 'edge' ? 'text-warn' : 'text-ink-2'
}

function useMobileTokenCards(): boolean {
  const [mobile, setMobile] = useState(() => typeof window !== 'undefined'
    && typeof window.matchMedia === 'function' && window.matchMedia('(max-width: 767px)').matches)
  useEffect(() => {
    if (typeof window.matchMedia !== 'function') return
    const query = window.matchMedia('(max-width: 767px)')
    const update = () => setMobile(query.matches)
    update()
    query.addEventListener('change', update)
    return () => query.removeEventListener('change', update)
  }, [])
  return mobile
}

export function TokenManager() {
  const { t } = useTranslation('admin')
  const qc = useQueryClient()
  const { data, isPending, isError, error, refetch } = useAdminTokens()
  const tokens = data?.tokens ?? []
  const mobileCards = useMobileTokenCards()
  const [creating, setCreating] = useState(false)
  const [secret, setSecret] = useState<CreatedToken | null>(null)
  const [query, setQuery] = useState('')
  const deferredQuery = useDeferredValue(query)
  const [statusFilter, setStatusFilter] = useState<string[]>([])
  const [scopeFilter, setScopeFilter] = useState<string[]>([])

  const filtered = useMemo(() => {
    const q = deferredQuery.trim().toLowerCase()
    return tokens.filter((token) => {
      if (statusFilter.length > 0 && !statusFilter.includes(tokenState(token))) return false
      if (scopeFilter.length > 0 && !(token.scopes ?? []).some((scope) => scopeFilter.includes(scope))) return false
      if (!q) return true
      const haystack = `${token.name} ${token.prefix} ${(token.scopes ?? []).join(' ')}`.toLowerCase()
      return q.split(/\s+/).filter(Boolean).every((word) => haystack.includes(word))
    })
  }, [tokens, deferredQuery, statusFilter, scopeFilter])

  const counts = useMemo(() => {
    const state = { valid: 0, expired: 0, revoked: 0 }
    const scopes: Record<string, number> = { read: 0, write: 0, admin: 0, edge: 0 }
    for (const token of tokens) {
      state[tokenState(token)] += 1
      for (const scope of token.scopes ?? []) scopes[scope] = (scopes[scope] ?? 0) + 1
    }
    return { state, scopes }
  }, [tokens])

  const columns = useMemo(() => tokenColumn.columns([
    tokenColumn.accessor('name', {
      id: 'name',
      enableHiding: false,
      header: ({ column }) => <DataTableColumnHeader column={column} title={t('tokenManager.columns.name')} />,
      meta: { label: t('tokenManager.columns.name'), cellClassName: 'min-w-[12rem]' },
      cell: ({ row }) => (
        <div className="min-w-0">
          <p className="truncate font-semibold" title={row.original.name || t('tokenManager.unnamed')}>
            {row.original.name || t('tokenManager.unnamed')}
          </p>
          <p className="mt-1 truncate font-mono text-meta text-ink-3" title={row.original.prefix}>{row.original.prefix}</p>
        </div>
      ),
    }),
    tokenColumn.accessor((row) => (row.scopes ?? []).join(','), {
      id: 'scopes',
      header: t('tokenManager.columns.scopes'),
      enableSorting: false,
      meta: { label: t('tokenManager.columns.scopes'), cellClassName: 'min-w-[10rem]' },
      cell: ({ row }) => (row.original.scopes ?? []).length > 0 ? (
        <div className="flex flex-wrap gap-x-2 gap-y-1 text-meta">
          {(row.original.scopes ?? []).map((scope) => (
            <span key={scope} className={scopeTone(scope)}>
              {t(`tokenRow.scopes.${scope}`, { defaultValue: t('tokenRow.scopes.other') })}
            </span>
          ))}
        </div>
      ) : <span className="text-meta text-ink-3">{t('tokenRow.noScopes')}</span>,
    }),
    tokenColumn.accessor((row) => tokenState(row), {
      id: 'status',
      header: t('tokenManager.columns.status'),
      enableSorting: false,
      meta: { label: t('tokenManager.columns.status'), cellClassName: 'min-w-[5rem]' },
      cell: ({ row }) => {
        const state = tokenState(row.original)
        const tone = state === 'valid' ? 'ok' : state === 'expired' ? 'warn' : 'bad'
        return <Badge tone={tone}>{t(`tokenRow.states.${state}`)}</Badge>
      },
    }),
    tokenColumn.accessor('created_at', {
      id: 'createdAt',
      header: ({ column }) => <DataTableColumnHeader column={column} title={t('tokenManager.columns.createdAt')} />,
      meta: { label: t('tokenManager.columns.createdAt'), cellClassName: 'num min-w-[9rem] whitespace-nowrap font-mono text-meta text-ink-2' },
      cell: ({ row }) => <span className="num whitespace-nowrap font-mono text-meta text-ink-2">{fmtDateTime(row.original.created_at)}</span>,
    }),
    tokenColumn.accessor((row) => row.last_used_at ?? 0, {
      id: 'lastUsed',
      header: t('tokenManager.columns.lastUsed'),
      enableSorting: false,
      meta: { label: t('tokenManager.columns.lastUsed'), cellClassName: 'min-w-[7rem] whitespace-nowrap text-meta text-ink-2' },
      cell: ({ row }) => row.original.last_used_at ? (
        <span className="whitespace-nowrap text-meta text-ink-2" title={fmtDateTime(row.original.last_used_at)}>
          {timeAgo(row.original.last_used_at)}
        </span>
      ) : <span className="whitespace-nowrap text-meta text-ink-3">{t('tokenRow.neverUsed')}</span>,
    }),
    tokenColumn.accessor((row) => row.expires_at ?? 0, {
      id: 'expires',
      header: ({ column }) => <DataTableColumnHeader column={column} title={t('tokenManager.columns.expires')} />,
      meta: { label: t('tokenManager.columns.expires'), cellClassName: 'num min-w-[9rem] whitespace-nowrap font-mono text-meta text-ink-2' },
      cell: ({ row }) => row.original.expires_at ? (
        <span className="num whitespace-nowrap font-mono text-meta text-ink-2">{fmtDateTime(row.original.expires_at)}</span>
      ) : <span className="whitespace-nowrap text-meta text-ink-3">{t('tokenRow.neverExpires')}</span>,
    }),
    tokenColumn.display({
      id: 'actions',
      header: () => <span className="block text-right">{t('tokenManager.columns.actions')}</span>,
      enableSorting: false,
      meta: { label: t('tokenManager.columns.actions'), cellClassName: 'w-[6rem] min-w-[6rem] whitespace-nowrap' },
      enableHiding: false,
      cell: ({ row }) => <div className="text-right"><TokenActions token={row.original} /></div>,
    }),
  ]), [t])

  const table = useDataTable({ columns, data: filtered, getRowId: (row) => String(row.id) })

  const onCreated = (created: CreatedToken) => {
    setSecret(created)
    setCreating(false)
    void qc.invalidateQueries({ queryKey: ADMIN_TOKENS_KEY })
    // toast 只带名称：明文绝不进提示文本（提示会挂在 DOM 上好几秒，也会被截图）
    toast.ok(t('tokenManager.toast.createdTitle'), t('tokenManager.toast.createdMessage', {
      name: created.name || t('tokenManager.unnamed'),
    }))
  }

  const resetFilters = () => {
    setQuery('')
    setStatusFilter([])
    setScopeFilter([])
  }

  return (
    <Panel
      title={<span className="flex items-center gap-1.5"><KeyRound size={14} />{t('tokenManager.title')}</span>}
      right={(
        <Button
          variant={creating ? 'ghost' : 'primary'}
          aria-expanded={creating}
          onClick={() => setCreating((value) => !value)}
        >
          {!creating && <Plus size={14} />}{creating ? t('tokenManager.collapse') : t('tokenManager.create')}
        </Button>
      )}
    >
      {secret && <SecretPanel secret={secret} onClose={() => setSecret(null)} />}

      <Drawer open={creating} onOpenChange={setCreating}>
        <DrawerContent>
          <DrawerHeader>
            <DrawerTitle>{t('tokenManager.create')}</DrawerTitle>
            <DrawerDescription>{t('tokenManager.createHint')}</DrawerDescription>
          </DrawerHeader>
          <CreateTokenForm onCreated={onCreated} onCancel={() => setCreating(false)} />
        </DrawerContent>
      </Drawer>

      {isError ? (
        <ErrorNote message={adminErrorMessage(error)} onRetry={() => void refetch()} />
      ) : (
        <>
          <DataTableToolbar
            table={table}
            search={{
              value: query,
              onChange: setQuery,
              label: t('tokenManager.search.label'),
              placeholder: t('tokenManager.search.placeholder'),
            }}
            filters={[
              {
                id: 'status',
                label: t('tokenManager.columns.status'),
                selected: statusFilter,
                singleSelect: true,
                onChange: setStatusFilter,
                options: [
                  { value: 'valid', label: t('tokenRow.states.valid'), count: counts.state.valid },
                  { value: 'expired', label: t('tokenRow.states.expired'), count: counts.state.expired },
                  { value: 'revoked', label: t('tokenRow.states.revoked'), count: counts.state.revoked },
                ],
              },
              {
                id: 'scope',
                label: t('tokenManager.columns.scopes'),
                selected: scopeFilter,
                onChange: setScopeFilter,
                options: (['read', 'write', 'admin', 'edge'] as TokenScope[]).map((scope) => ({
                  value: scope,
                  label: t(`tokenRow.scopes.${scope}`),
                  count: counts.scopes[scope] ?? 0,
                })),
              },
            ]}
            resultCount={filtered.length}
            onReset={resetFilters}
          />
          {mobileCards && <div className="space-y-3">
            {isPending ? <RowSkeleton rows={3} /> : filtered.length === 0
              ? <p className="py-3 text-body text-ink-3">{tokens.length === 0 ? t('tokenManager.empty') : t('tokenManager.noMatches')}</p>
              : filtered.map((token) => {
                const state = tokenState(token)
                const tone = state === 'valid' ? 'ok' : state === 'expired' ? 'warn' : 'bad'
                return <article key={token.id} className="min-w-0 rounded-tile border border-hairline p-3.5">
                  <div className="flex min-w-0 items-start justify-between gap-3">
                    <div className="min-w-0">
                      <p className="break-words font-semibold">{token.name || t('tokenManager.unnamed')}</p>
                      <p className="mt-1 break-all font-mono text-meta text-ink-3">{token.prefix}</p>
                    </div>
                    <Badge tone={tone}>{t(`tokenRow.states.${state}`)}</Badge>
                  </div>
                  <div className="mt-3 flex flex-wrap gap-x-2 gap-y-1 text-meta">
                    {(token.scopes ?? []).length > 0 ? (token.scopes ?? []).map((scope) => (
                      <span key={scope} className={scopeTone(scope)}>{t(`tokenRow.scopes.${scope}`, { defaultValue: t('tokenRow.scopes.other') })}</span>
                    )) : <span className="text-ink-3">{t('tokenRow.noScopes')}</span>}
                  </div>
                  <dl className="mt-3 grid grid-cols-2 gap-2 border-t border-hairline pt-3 text-meta">
                    <div><dt className="text-ink-3">{t('tokenManager.columns.createdAt')}</dt><dd className="mt-0.5 num font-mono text-ink-2">{fmtDateTime(token.created_at)}</dd></div>
                    <div><dt className="text-ink-3">{t('tokenManager.columns.lastUsed')}</dt><dd className="mt-0.5 text-ink-2">{token.last_used_at ? timeAgo(token.last_used_at) : t('tokenRow.neverUsed')}</dd></div>
                    <div><dt className="text-ink-3">{t('tokenManager.columns.expires')}</dt><dd className="mt-0.5 num font-mono text-ink-2">{token.expires_at ? fmtDateTime(token.expires_at) : t('tokenRow.neverExpires')}</dd></div>
                  </dl>
                  <div className="mt-3 border-t border-hairline pt-3"><TokenActions token={token} /></div>
                </article>
              })}
          </div>}
          {!mobileCards && <div>
            <DataTable
              table={table}
              ariaLabel={t('tokenManager.listAria')}
              empty={tokens.length === 0 ? t('tokenManager.empty') : t('tokenManager.noMatches')}
              loading={isPending}
              loadingRows={3}
              minWidthClassName="min-w-[58rem]"
              footer={filtered.some((token) => token.revoked_at) ? (
                <TableRow className="hover:bg-transparent">
                  <TableCell colSpan={table.getVisibleLeafColumns().length} className="px-3 py-3 text-meta leading-relaxed text-ink-3">
                    {t('tokenRow.revokedHint')}
                  </TableCell>
                </TableRow>
              ) : undefined}
            />
          </div>}
        </>
      )}

      <div className="mt-4 border-t border-hairline pt-3 text-meta leading-relaxed text-ink-3">
        <p>{t('tokenManager.hint')}</p>
      </div>
    </Panel>
  )
}
