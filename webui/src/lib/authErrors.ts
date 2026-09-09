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

/**
 * 「服务端已经认下这次认证，但会话没落地」的诚实文案。
 *
 * 为什么单列：login/setup 返回 2xx 之后再失败，就**不是**凭据问题——真实原因是
 * Set-Cookie 被浏览器或反向代理拦掉、或服务端会话写入失败。旧实现把它和凭据错误
 * 塞进同一个 catch，于是显示「用户名或密码错误」，用户被引导去反复重输一套正确的
 * 密码；首装场景更糟：账号已不可逆落库，重试只会拿到 409。
 */
export const SESSION_NOT_ESTABLISHED = {
  /** POST /api/auth/login 2xx 之后 GET /api/auth/me 复核失败：凭据是对的，可以重试 */
  login: '账号和密码是对的，但登录状态没有保存成功。请重试一次；如果仍然失败，请允许此页面保存网站数据，或联系管理员协助。',
  /** POST /api/auth/setup 2xx 之后复核失败：账号已创建且不可逆，只能去登录页 */
  setup: '管理员账号已创建，但登录状态没有保存成功。请直接到登录页使用刚创建的账号登录。',
} as const

/** POST /api/auth/login 的错误语义 */
export function loginErrorCopy(e: unknown): AuthErrorCopy {
  if (e instanceof ApiError) {
    switch (e.status) {
      case 401:
        return { ...BASE, message: '用户名或密码不正确，请检查后重试。', badCredentials: true }
      case 429:
        return {
          ...BASE,
          retryAfter: e.retryAfter,
          message: e.retryAfter
            ? `登录尝试过多，请 ${e.retryAfter} 秒后重试`
            : '登录尝试过多，请稍后再试',
        }
      case 400:
        return { ...BASE, message: '登录信息格式不正确，请检查用户名和密码后重试。' }
      case 503:
        return { ...BASE, message: '暂时无法完成登录，请稍后重试；持续失败请联系管理员。' }
      default:
        return { ...BASE, message: '登录暂时没有成功，请稍后重试。' }
    }
  }
  return { ...BASE, message: '暂时无法连接 CloudPath。请检查网络后重试。', unreachable: true }
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
          message: '现在无法完成初始化：系统可能已经设置过，或首次设置需要在允许的设备上操作。请联系管理员创建账号，然后到登录页登录。',
        }
      case 409:
        return {
          ...BASE, alreadySetup: true,
          message: '系统已经完成初始化，不能再创建首个账号。请直接到登录页登录；忘记密码请联系管理员重置。',
        }
      // 没有 401 分支：/api/auth/setup 是免认证端点，只会 403/409/400/503。
      // 建号成功后 me→401 是「会话没落地」，属另一件事，由 SESSION_NOT_ESTABLISHED.setup
      // 说真话——旧实现在这里报「用户名或密码错误」，把已建号说成凭据错。
      case 400:
        return { ...BASE, message: '账号或密码格式不正确，请检查后重试。' }
      case 429:
        return {
          ...BASE, retryAfter: e.retryAfter,
          message: e.retryAfter ? `操作过于频繁，请 ${e.retryAfter} 秒后重试` : '操作过于频繁，请稍后再试',
        }
      case 503:
        return { ...BASE, message: '暂时无法保存账号信息，请稍后重试。' }
      default:
        return { ...BASE, message: '初始化暂时没有成功，请稍后重试。' }
    }
  }
  return { ...BASE, message: '暂时无法连接 CloudPath。请检查网络后重试。', unreachable: true }
}