<script lang="ts" setup>
import {nextTick, ref, watch} from 'vue'
import {dialog, closeDialog} from '../lib/dialog'

const card = ref<HTMLElement | null>(null)

// 弹层出现后把焦点移进来。没有焦点就没有 keydown——
// 不聚焦的话 Esc 和 Enter 全都没反应，键盘用户只能靠鼠标。
watch(
  () => dialog.value.open,
  async open => {
    if (!open) return
    await nextTick()
    card.value?.focus()
  },
)

// 点遮罩等同于点「取消 / 好」里非主操作的那个，避免误触关闭后 Promise 悬空
function onMask() {
  const fallback = dialog.value.buttons.find(b => !b.primary)
  closeDialog(fallback ? fallback.value : true)
}

function onEnter(e: KeyboardEvent) {
  // 焦点落在按钮上时交给按钮自己处理，否则会和按钮的默认回车行为打架
  if (e.target !== card.value) return
  const main = dialog.value.buttons.find(b => b.primary || b.danger)
  if (main) closeDialog(main.value)
}
</script>

<template>
  <div
    v-if="dialog.open"
    class="modal-mask"
    @click.self="onMask"
    @keydown.esc.stop.prevent="onMask"
    @keydown.enter.stop.prevent="onEnter"
  >
    <div ref="card" class="card modal dialog-card" role="dialog" aria-modal="true" tabindex="-1">
      <div class="dialog-title">{{ dialog.title }}</div>
      <div class="dialog-body">{{ dialog.message }}</div>
      <div class="dialog-actions">
        <button
          v-for="b in dialog.buttons"
          :key="b.label"
          :class="{primary: b.primary, danger: b.danger && !b.primary}"
          @click="closeDialog(b.value)"
        >{{ b.label }}</button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.dialog-card {width: 400px; padding: 20px; outline: none;}
.dialog-title {font-size: 15px; font-weight: 600; margin-bottom: 8px;}
.dialog-body {
  font-size: 13px; color: var(--text); line-height: 1.6;
  white-space: pre-wrap; word-break: break-word; max-height: 40vh; overflow-y: auto;
}
.dialog-actions {display: flex; justify-content: flex-end; gap: 8px; margin-top: 18px;}
.dialog-actions button {min-width: 72px;}
/* 破坏性操作给实心红，和普通「取消」拉开距离，避免手滑 */
.dialog-actions button.danger {
  background: var(--danger); color: #fff; border-color: var(--danger);
}
.dialog-actions button.danger:hover {
  background: var(--danger); color: #fff; border-color: var(--danger);
  filter: brightness(.92);
}
</style>
