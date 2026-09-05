import { useEffect } from 'react'

/** 浏览器标签标题跟随路由：运维多标签场景下静态站名无法区分标签页。
 *  格式统一「<页面> · Cloudpath」；详情页带对象标识（设备名/节点 id/实例 id）。 */
export function usePageTitle(page: string): void {
  useEffect(() => {
    document.title = `${page} · Cloudpath`
  }, [page])
}
