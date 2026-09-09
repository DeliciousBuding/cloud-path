export default {
  generic: '操作失败，请稍后重试。',
  network: '无法连接服务（服务未启动或网络不可达）',
  unauthorized: '登录已失效，请重新登录后再试。',
  forbidden: '当前账号没有执行此操作的权限。',
  notFound: '目标不存在，或不属于当前组织。',
  conflict: '当前操作与已有状态冲突，请刷新后重试。',
  rateLimited: '操作过于频繁，请稍后重试。',
  unavailable: '服务暂时不可用，请稍后重试。',
  command: {
    badRequest: '操作或参数不被接受：设备不支持，或参数过长、包含无效字符', unauthorized: '登录已失效，请重新登录后再执行操作', forbidden: '当前账号没有执行操作的权限', notFound: '设备不存在，或不属于当前组织', offline: '设备所在网关离线，操作暂时无法执行', rateLimited: '操作过于频繁，请稍后重试', rateLimitedAfter: '操作过于频繁，请 {{seconds}} 秒后重试', unavailable: '服务暂时不可用或网关忙碌，请稍后重试', failed: '操作失败（HTTP {{status}}）',
  },
}
