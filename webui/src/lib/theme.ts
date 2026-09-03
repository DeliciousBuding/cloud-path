// 主题：light | dark | system（localStorage 持久化 + 系统偏好跟随）
export type ThemeMode = 'light' | 'dark' | 'system'

const KEY = 'cloudpath.theme'

export function getTheme(): ThemeMode {
  const v = localStorage.getItem(KEY)
  return v === 'light' || v === 'dark' ? v : 'system'
}

export function setTheme(mode: ThemeMode) {
  localStorage.setItem(KEY, mode)
  applyTheme(mode)
}

export function applyTheme(mode: ThemeMode = getTheme()) {
  const dark = mode === 'dark' ||
    (mode === 'system' && window.matchMedia('(prefers-color-scheme: dark)').matches)
  document.documentElement.classList.toggle('dark', dark)
}

/** system 模式下监听系统切换；返回清理函数（App 挂载一次） */
export function watchSystemTheme(): () => void {
  const mq = window.matchMedia('(prefers-color-scheme: dark)')
  const onChange = () => { if (getTheme() === 'system') applyTheme('system') }
  mq.addEventListener('change', onChange)
  return () => mq.removeEventListener('change', onChange)
}