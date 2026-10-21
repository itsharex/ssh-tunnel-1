import {ClipboardSetText} from '../../wailsjs/runtime/runtime'

/**
 * 复制文本到剪贴板。
 *
 * 走 Wails 的 ClipboardSetText 而不是 navigator.clipboard：后者要求安全上下文，
 * 在 WebView 的自定义 scheme 下可能直接是 undefined。
 *
 * 返回值约定为「成功 / 失败」，绝不抛异常：
 * - runtime 未就绪时 `window.runtime.ClipboardSetText` 会同步抛 TypeError，
 *   在 async 函数里就变成 rejected promise；
 * - 调用点是 `@click="copy(...)"`，Vue 不会接住返回的 promise，
 *   reject 只会变成一条无人处理的 unhandled rejection，界面上什么都不显示。
 * 两种情况都统一收敛成 false，由调用方提示。
 */
export async function copyText(text: string): Promise<boolean> {
  try {
    return await ClipboardSetText(text)
  } catch {
    return false
  }
}
