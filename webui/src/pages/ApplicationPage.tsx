import { useMemo } from 'react'
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
  return <Link to="/plugins" className="btn btn-ghost mt-4">查看应用与插件</Link>
}

export default function ApplicationPage() {
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
  const title = resolution.kind === 'ready' ? resolution.page.title : '应用'
  usePageTitle(title)

  if (catalog.loading || instanceList.loading) return <PageSkeleton />
  if (catalog.error || instanceList.error) {
    const denied = catalog.error instanceof ApiError && catalog.error.status === 403
      || instanceList.error instanceof ApiError && instanceList.error.status === 403
    return <>
      <BackLink to="/plugins" label="应用与插件" />
      <ErrorState icon={<Boxes size={20} />} title={denied ? '没有查看权限' : '应用加载失败'}
        hint={denied ? '当前账号不能查看这个应用的入口或运行数据，请联系管理员核对访问权限。'
          : '暂时无法读取应用入口或运行实例，请重试。'}
        onRetry={() => { catalog.refetch(); instanceList.refetch() }} />
    </>
  }

  switch (resolution.kind) {
    case 'auth-required':
      return <><BackLink to="/plugins" label="应用与插件" /><EmptyState icon={<ShieldAlert size={24} />}
        title="需要登录" hint="登录后可以查看当前组织的应用页面和运行数据。" action={<Link to="/login" className="btn btn-primary">前往登录</Link>} /></>
    case 'not-found':
      return <><BackLink to="/plugins" label="应用与插件" /><EmptyState icon={<Layers3 size={24} />}
        title="应用不存在" hint={`没有找到入口 ${resolution.route}。它可能已卸载，或不属于当前组织。`} action={<ApplicationLink />} /></>
    case 'route-conflict':
      return <><BackLink to="/plugins" label="应用与插件" /><ErrorState icon={<ShieldAlert size={20} />}
        title="应用入口冲突" hint="多个插件声明了同一个页面地址。为避免打开错误的应用，平台已暂时关闭这个入口。" /></>
    case 'unverified':
      return <><BackLink to="/plugins" label="应用与插件" /><EmptyState icon={<ShieldAlert size={24} />}
        title="应用未通过验证" hint="这个插件还没有通过来源验证，因此不会开放业务页面。" action={<ApplicationLink />} /></>
    case 'no-instance':
      return <><BackLink to="/plugins" label="应用与插件" /><EmptyState icon={<Layers3 size={24} />}
        title="尚未启用" hint="插件已经安装，但还没有创建这个应用。请先到应用与插件中创建运行实例。" action={<ApplicationLink />} /></>
    case 'disabled':
      return <><BackLink to="/plugins" label="应用与插件" /><EmptyState icon={<Layers3 size={24} />}
        title="应用已停用" hint="这个应用的所有运行实例都已停用。重新启用后会恢复入口和操作。" action={<ApplicationLink />} /></>
    case 'no-page':
      return <><BackLink to="/plugins" label="应用与插件" /><EmptyState icon={<Layers3 size={24} />}
        title="页面尚未配置" hint="插件声明了导航入口，但没有提供可显示的页面。" action={<ApplicationLink />} /></>
    case 'page-not-found':
      return <><BackLink to={`/apps/${encodeURIComponent(resolution.route)}`} label="返回应用首页" />
        <EmptyState icon={<Layers3 size={24} />} title="页面不存在" hint={`没有找到页面 ${resolution.pageId}。`} /></>
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
        <BackLink to="/plugins" label="应用与插件" />
        <PageHeader
          title={resolution.navigation.title}
          subtitle={resolution.contribution.title || resolution.plugin.id}
          actions={resolution.instances.length > 1
            ? <label className="flex items-center gap-2 text-meta text-ink-2">
              <span>运行实例</span>
              <select className="input min-h-touch max-w-[14rem]" value={selected.desired.instance_id}
                onChange={(event) => setInstance(event.target.value)}>
                {resolution.instances.map((instance) => <option key={instance.id} value={instance.desired.instance_id}>
                  {instance.desired.instance_id}{instance.desired.enabled ? '' : '（已停用）'}
                </option>)}
              </select>
            </label>
            : undefined}
        />
        {pages.length > 1 && <nav className="mb-5 flex min-w-0 flex-wrap gap-2" aria-label="应用页面">
          {pages.map((page) => <Link key={page.id} to={pageLink(page.id)}
            className={`btn ${page.id === resolution.page.id ? 'btn-primary' : 'btn-ghost'}`}>{page.title}</Link>)}
        </nav>}
        {selected.desired.instance_id !== requestedInstance && resolution.instances.length > 1
          ? <Panel className="mb-5"><p className="text-body text-ink-2">已打开默认实例。可在上方切换其他实例。</p></Panel>
          : null}
        <ApplicationConsole instance={selected} catalog={resolution.plugin} page={resolution.page}
          readOnly={authStatus !== 'in' || user?.role === 'viewer'} lifecycleKey={lifecycleKey} />
      </>
    }
  }
}
