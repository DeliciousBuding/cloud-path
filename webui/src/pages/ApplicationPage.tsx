import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'
import { Link, useParams, useSearchParams } from 'react-router'
import { Boxes, Layers3, ShieldAlert } from 'lucide-react'
import { BackLink, EmptyState, ErrorState, PageHeader, Panel } from '@/components/ui'
import { ApiError } from '@/lib/api'
import { PageSkeleton } from '@/components/Skeleton'
import { ApplicationConsole } from '@/components/plugin-ui/ApplicationConsole'
import { usePageTitle } from '@/hooks/usePageTitle'
import { usePluginCatalog, usePluginInstances } from '@/hooks/usePlugins'
import { applicationUIReadable, resolveApplicationRoute } from '@/lib/plugin-ui'
import { useAuth } from '@/store/auth'

function ApplicationLink() {
  const { t } = useTranslation('plugins')
  return <Link to="/plugins" className="btn btn-ghost mt-4">{t('application.openPlugins')}</Link>
}

export default function ApplicationPage() {
  const { t } = useTranslation('plugins')
  const { appRoute = '', pageId } = useParams()
  const [searchParams, setSearchParams] = useSearchParams()
  const requestedInstance = searchParams.get('instance') ?? undefined
  const authStatus = useAuth((state) => state.status)
  const user = useAuth((state) => state.user)
  const readable = applicationUIReadable(user, authStatus)
  const catalog = usePluginCatalog()
  const instanceList = usePluginInstances()
  const resolution = useMemo(() => resolveApplicationRoute(
    catalog.plugins, instanceList.instances, appRoute, pageId, requestedInstance, readable,
  ), [appRoute, catalog.plugins, instanceList.instances, pageId, readable, requestedInstance])
  const title = resolution.kind === 'ready' ? resolution.page.title : t('application.fallbackTitle')
  usePageTitle(title)

  if (catalog.loading || instanceList.loading) return <PageSkeleton />
  if (catalog.error || instanceList.error) {
    const denied = catalog.error instanceof ApiError && catalog.error.status === 403
      || instanceList.error instanceof ApiError && instanceList.error.status === 403
    return <>
      <BackLink to="/plugins" label={t('application.back')} />
      <ErrorState icon={<Boxes size={20} />} title={denied ? t('application.permissionDenied') : t('application.loadFailed')}
        hint={denied ? t('application.permissionDeniedHint')
          : t('application.loadFailedHint')}
        onRetry={() => { catalog.refetch(); instanceList.refetch() }} />
    </>
  }

  switch (resolution.kind) {
    case 'auth-required':
      return <><BackLink to="/plugins" label={t('application.back')} /><EmptyState icon={<ShieldAlert size={24} />}
        title={t('application.authRequired')} hint={t('application.authRequiredHint')} action={<Link to="/login" className="btn btn-primary">{t('application.login')}</Link>} /></>
    case 'not-found':
      return <><BackLink to="/plugins" label={t('application.back')} /><EmptyState icon={<Layers3 size={24} />}
        title={t('application.notFound')} hint={t('application.notFoundHint', { route: resolution.route })} action={<ApplicationLink />} /></>
    case 'route-conflict':
      return <><BackLink to="/plugins" label={t('application.back')} /><ErrorState icon={<ShieldAlert size={20} />}
        title={t('application.routeConflict')} hint={t('application.routeConflictHint')} /></>
    case 'unverified':
      return <><BackLink to="/plugins" label={t('application.back')} /><EmptyState icon={<ShieldAlert size={24} />}
        title={t('application.unverified')} hint={t('application.unverifiedHint')} action={<ApplicationLink />} /></>
    case 'no-instance':
      return <><BackLink to="/plugins" label={t('application.back')} /><EmptyState icon={<Layers3 size={24} />}
        title={t('application.noInstance')} hint={t('application.noInstanceHint')} action={<ApplicationLink />} /></>
    case 'disabled':
      return <><BackLink to="/plugins" label={t('application.back')} /><EmptyState icon={<Layers3 size={24} />}
        title={t('application.disabled')} hint={t('application.disabledHint')} action={<ApplicationLink />} /></>
    case 'no-page':
      return <><BackLink to="/plugins" label={t('application.back')} /><EmptyState icon={<Layers3 size={24} />}
        title={t('application.noPage')} hint={t('application.noPageHint')} action={<ApplicationLink />} /></>
    case 'page-not-found':
      return <><BackLink to={`/apps/${encodeURIComponent(resolution.route)}`} label={t('application.backHome')} />
        <EmptyState icon={<Layers3 size={24} />} title={t('application.pageNotFound')} hint={t('application.pageNotFoundHint', { pageId: resolution.pageId })} /></>
    case 'ready': {
      const selected = resolution.instance
      const pages = resolution.ui.pages ?? []
      const setInstance = (instanceID: string) => setSearchParams((previous) => {
        const next = new URLSearchParams(previous)
        next.set('instance', instanceID)
        return next
      }, { replace: true })
      const pageLink = (id: string) => {
        const params = new URLSearchParams(searchParams)
        const query = params.toString()
        return `/apps/${encodeURIComponent(appRoute)}${id === pages[0]?.id ? '' : `/${encodeURIComponent(id)}`}${query ? `?${query}` : ''}`
      }
      const lifecycleKey = JSON.stringify([selected.desired.revision, selected.desired.enabled,
        selected.has_observed, selected.observed?.state, selected.applied_revision, selected.stale])
      return <>
        <BackLink to="/plugins" label={t('application.back')} />
        <PageHeader
          title={resolution.navigation.title}
          subtitle={resolution.contribution.title || resolution.plugin.id}
          actions={resolution.instances.length > 1
            ? <label className="flex items-center gap-2 text-xs text-ink-2">
              <span>{t('application.instance')}</span>
              <select className="input min-h-11 max-w-[14rem]" value={selected.desired.instance_id}
                onChange={(event) => setInstance(event.target.value)}>
                {resolution.instances.map((instance) => <option key={instance.id} value={instance.desired.instance_id}>
                  {instance.desired.instance_id}{instance.desired.enabled ? '' : t('application.instanceDisabled')}
                </option>)}
              </select>
            </label>
            : undefined}
        />
        {pages.length > 1 && <nav className="mb-5 flex min-w-0 flex-wrap gap-2" aria-label={t('application.pagesAria')}>
          {pages.map((page) => <Link key={page.id} to={pageLink(page.id)}
            className={`btn ${page.id === resolution.page.id ? 'btn-primary' : 'btn-ghost'}`}>{page.title}</Link>)}
        </nav>}
        {selected.desired.instance_id !== requestedInstance && resolution.instances.length > 1
          ? <Panel className="mb-5"><p className="text-sm text-ink-2">{t('application.defaultInstance')}</p></Panel>
          : null}
        <ApplicationConsole instance={selected} catalog={resolution.plugin} page={resolution.page}
          readOnly={authStatus !== 'in' || user?.role === 'viewer'} lifecycleKey={lifecycleKey} />
      </>
    }
  }
}
