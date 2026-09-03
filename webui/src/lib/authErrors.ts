// 认证面（/api/auth/login、/api/auth/setup）的错误 → 人话。纯函数、按 HTTP 状态判定。
//
// 两条原则：
//   ① 按**状态码**给文案，不把服务端 message 当规则复述（401 一律「用户名或密码错误」，
//      不泄漏「用户存在但密码错」这类区别，也不依赖服务端措辞）；
//   ② 429 只在服务端真的给了 Retry-After 时才报秒数，绝不自己编一个倒计时。
import { ApiError } from './api'

export interface AuthErrorCopy {
  /** 主文案（呈现给用户） */
  message: string
  /** 429 且服务端给了 Retry-After 时的秒数；undefined = 不做倒计时 */
  retryAfter?: number
  /** 是否「凭据不对」——用于决定是否清空密码框、是否聚焦回用户名 */
  badCredentials: boolean
  /** 是否属于「本实例已初始化 / 不该再走 setup」——UI 据此把用户导流到登录页 */
  alreadySetup: boolean
  /** 是否网络/服务不可达（不是账号问题） */
  unreachable: boolean
}

const BASE: AuthErrorCopy = {
  message: '', badCredentials: false, alreadySetup: false, unreachable: false,
}

/** POST /api/auth/login 的错误语义 */
export function loginErrorCopy(e: unknown): AuthErrorCopy {
  if (e instanceof ApiError) {
    switch (e.status) {
      case 401:
        return { ...BASE, message: '用户名或密码错误', badCredentials: true }
      case 429:
        return {
          ...BASE,
          retryAfter: e.retryAfter,
          message: e.retryAfter
            ? `登录尝试过多，请 ${e.retryAfter} 秒后重试`
            : '登录尝试过多，请稍后再试',
        }
      case 400:
        return { ...BASE, message: '账号或密码格式不被接受（用户名 ≤64 字符，密码 ≤256 字符）' }
      case 503:
        return { ...BASE, message: '服务端存储不可用，暂时无法登录。请稍后重试；持续失败请联系管理员。' }
      default:
        return { ...BASE, message: `登录失败（HTTP ${e.status}）` }
    }
  }
  return { ...BASE, message: '无法连接 server（服务未启动或网络不可达）', unreachable: true }
}

/**
 * POST /api/auth/setup 的错误语义。
 * 后端约定：真实 TCP 回环永远放行；非回环需一次性 setup token；首个用户落库后立即进入
 * 全鉴权账号模式。因此公网访问 Setup 基本会 403/409 —— 这两种都要说成人话并导流到登录页，
 * 不能白屏或把原始错误甩给用户。
 */
export function setupErrorCopy(e: unknown): AuthErrorCopy {
  if (e instanceof ApiError) {
    switch (e.status) {
      case 403:
        return {
          ...BASE, alreadySetup: true,
          message: '无法从这里初始化：本实例已完成初始化，或首次设置只允许从服务器本机（回环地址）进行。请联系管理员为你创建账号，然后在登录页登录。',
        }
      case 409:
        return {
          ...BASE, alreadySetup: true,
          message: '本实例已经初始化过了，不能再创建首个账号。请直接去登录页登录；忘记密码请联系管理员重置。',
        }
      case 401:
        return { ...BASE, message: '用户名或密码错误', badCredentials: true }
      case 400:
        return { ...BASE, message: '账号或密码格式不被接受（用户名 ≤64 字符，密码 ≤256 字符）' }
      case 429:
        return {
          ...BASE, retryAfter: e.retryAfter,
          message: e.retryAfter ? `操作过于频繁，请 ${e.retryAfter} 秒后重试` : '操作过于频繁，请稍后再试',
        }
      case 503:
        return { ...BASE, message: '服务端存储不可用，暂时无法创建账号。请稍后重试。' }
      default:
        return { ...BASE, message: `初始化失败（HTTP ${e.status}）` }
    }
  }
  return { ...BASE, message: '无法连接 server（服务未启动或网络不可达）', unreachable: true }
}