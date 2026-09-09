export default {
  app: { name: 'CloudPath', overviewAria: 'CloudPath 概览', tagline: '云径 · 设备接入平台' },
  actions: {
    refresh: '刷新', retry: '重试', cancel: '取消', save: '保存', close: '关闭', delete: '删除', edit: '编辑',
    create: '新建', search: '搜索', clear: '清除', more: '更多', view: '查看', back: '返回', loading: '正在加载…', none: '—',
    logout: '登出', logoutTitle: '登出当前账号',
  },
  status: { online: '在线', offline: '离线', unknown: '状态待确认', connected: '已连接', connecting: '连接中', disconnected: '已断开' },
  theme: { light: '浅色外观', dark: '深色外观', system: '跟随系统', label: '外观主题', switchTo: '切换为{{label}}' },
  language: { label: '语言', zhCN: '简体中文', enUS: 'English' },
  layout: {
    skipToContent: '跳到主内容', mainNavigation: '主导航', moreNavigation: '更多导航', morePages: '更多页面',
    more: '更多', moreNavAria: '更多导航与账号设置', appearance: '外观', version: '版本 {{version}}',
    connectionTitle: '数据连接：{{status}}', offlineConnecting: '正在恢复数据连接…',
    offlineDisconnected: '数据连接已断开，正在自动恢复（页面会继续刷新）',
    offlineFailures: '已连续失败 {{count}} 次', offlineRechecking: ' · 正在重新检查登录状态',
  },
  time: {
    justNow: '刚刚', today: '今天', yesterday: '昨天',
    secondsAgo: '{{count}} 秒前', minutesAgo: '{{count}} 分钟前', hoursAgo: '{{count}} 小时前', daysAgo: '{{count}} 天前',
    seconds: '{{count}} 秒', minutes: '{{count}} 分钟', hoursMinutes: '{{hours}} 小时 {{minutes}} 分', daysHours: '{{days}} 天 {{hours}} 小时',
  },
}
