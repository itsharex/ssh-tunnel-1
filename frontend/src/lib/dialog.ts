import {ref} from 'vue'

/**
 * 应用内对话框。
 *
 * 为什么不用 window.confirm / window.alert：
 * Wails v2 在 macOS 上把 WKWebView 的 UIDelegate 设成了自己，但只实现了
 * runOpenPanel / URLSchemeTask / didFinishNavigation，没有实现
 * runJavaScriptConfirmPanel 与 runJavaScriptAlertPanel。WKWebView 的约定是
 * 「代理不实现就回调 NO」，于是：
 *   - confirm() 不弹窗、直接返回 false
 *   - alert()   不弹窗、静默丢弃
 * 结果就是「点确定没反应」「校验失败没有任何提示」这类幽灵 bug。
 * 所以确认与提示一律走自绘弹层，不依赖宿主 WebView 的实现差异。
 */

export interface DialogButton {
  label: string
  value: boolean
  primary?: boolean
  danger?: boolean
}

interface DialogState {
  open: boolean
  title: string
  message: string
  buttons: DialogButton[]
  resolve: ((value: boolean) => void) | null
}

export const dialog = ref<DialogState>({
  open: false,
  title: '',
  message: '',
  buttons: [],
  resolve: null,
})

function open(state: Omit<DialogState, 'open'>) {
  // 上一个对话框还没关就被新的顶掉时，按「取消」把它结掉，
  // 否则那个 Promise 永远不会 resolve，调用方会一直挂着。
  const prev = dialog.value.resolve
  if (prev) prev(false)
  dialog.value = {...state, open: true}
}

/** 关闭对话框并回传用户的选择 */
export function closeDialog(value: boolean) {
  const d = dialog.value
  const resolve = d.resolve
  dialog.value = {...d, open: false, resolve: null}
  resolve?.(value)
}

/** 提示框。等用户点掉再返回。 */
export function showAlert(message: string, title = '提示'): Promise<void> {
  return new Promise<void>(resolve => {
    open({
      title,
      message,
      buttons: [{label: '好', value: true, primary: true}],
      resolve: () => resolve(),
    })
  })
}

/** 确认框。返回 true 表示用户确认。 */
export function confirmDialog(
  message: string,
  opts?: {title?: string; okText?: string; danger?: boolean},
): Promise<boolean> {
  return new Promise<boolean>(resolve => {
    open({
      title: opts?.title ?? '请确认',
      message,
      buttons: [
        {label: '取消', value: false},
        {
          label: opts?.okText ?? '确定',
          value: true,
          primary: !opts?.danger,
          danger: opts?.danger,
        },
      ],
      resolve,
    })
  })
}

/** 统一的错误提示：把异常转成一句人话 */
export function showError(prefix: string, e: unknown) {
  const msg = (e as {message?: string})?.message || String(e)
  void showAlert(`${prefix}${msg}`, '出错了')
}
