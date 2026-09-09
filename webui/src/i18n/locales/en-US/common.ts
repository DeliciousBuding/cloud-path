export default {
  app: { name: 'CloudPath', overviewAria: 'CloudPath overview', tagline: 'CloudPath · Device Access Platform' },
  actions: {
    refresh: 'Refresh', retry: 'Retry', cancel: 'Cancel', save: 'Save', close: 'Close', delete: 'Delete', edit: 'Edit',
    create: 'Create', search: 'Search', clear: 'Clear', more: 'More', view: 'View', back: 'Back', loading: 'Loading…', none: '—',
    logout: 'Sign out', logoutTitle: 'Sign out of the current account',
  },
  status: { online: 'Online', offline: 'Offline', unknown: 'Status unknown', connected: 'Connected', connecting: 'Connecting', disconnected: 'Disconnected' },
  theme: { light: 'Light appearance', dark: 'Dark appearance', system: 'Use system setting', label: 'Appearance', switchTo: 'Switch to {{label}}' },
  language: { label: 'Language', zhCN: '简体中文', enUS: 'English' },
  layout: {
    skipToContent: 'Skip to main content', mainNavigation: 'Main navigation', moreNavigation: 'More navigation',
    morePages: 'More pages', more: 'More', moreNavAria: 'More navigation and account settings',
    appearance: 'Appearance', version: 'Version {{version}}', connectionTitle: 'Data connection: {{status}}',
    offlineConnecting: 'Restoring the data connection…',
    offlineDisconnected: 'The data connection is offline and reconnecting automatically (the page will keep refreshing).',
    offlineFailures: '{{count}} consecutive failures', offlineRechecking: ' · rechecking sign-in status',
  },
  time: {
    justNow: 'Just now', today: 'Today', yesterday: 'Yesterday',
    secondsAgo_one: '{{count}} second ago', secondsAgo_other: '{{count}} seconds ago',
    minutesAgo_one: '{{count}} minute ago', minutesAgo_other: '{{count}} minutes ago',
    hoursAgo_one: '{{count}} hour ago', hoursAgo_other: '{{count}} hours ago',
    daysAgo_one: '{{count}} day ago', daysAgo_other: '{{count}} days ago',
    seconds_one: '{{count}} second', seconds_other: '{{count}} seconds',
    minutes_one: '{{count}} minute', minutes_other: '{{count}} minutes',
    hoursMinutes: '{{hours}}h {{minutes}}m', daysHours: '{{days}}d {{hours}}h',
  },
}
