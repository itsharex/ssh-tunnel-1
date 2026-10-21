<script lang="ts" setup>
import {ref, onMounted} from 'vue'
import * as App from '../../wailsjs/go/main/App'
import {config} from '../../wailsjs/go/models'

const settings = ref<config.Settings>(new config.Settings({
  autostart: false,
  theme: 'system',
  minimizeToTray: true,
  startMinimized: false,
  hideFromDock: false,
}))
const configPath = ref('')
const sysAutostart = ref(false)
const msg = ref('')

onMounted(async () => {
  settings.value = await App.GetSettings()
  configPath.value = await App.ConfigPath()
  sysAutostart.value = await App.GetAutostart()
})

async function save() {
  msg.value = ''
  try {
    await App.SetSettings(settings.value)
    sysAutostart.value = await App.GetAutostart()
    msg.value = '已保存'
  } catch (e: any) {
    msg.value = '保存失败: ' + (e?.message || String(e))
  }
}

async function openFolder() {
  await App.OpenInFolder(configPath.value)
}

async function quitApp() {
  await App.QuitApp()
}
</script>

<template>
  <div class="page-title">
    <h2>设置</h2>
    <button class="primary" @click="save">保存</button>
  </div>

  <div v-if="msg" class="card">{{ msg }}</div>

  <div class="card">
    <label class="row" style="gap:8px; margin-bottom:12px;">
      <input type="checkbox" v-model="settings.autostart" style="width:auto;">
      <div class="col" style="gap:2px;">
        <span>开机自启动</span>
        <span class="hint">当前系统状态: {{ sysAutostart ? '已启用' : '未启用' }}</span>
      </div>
    </label>
    <label class="row" style="gap:8px; margin-bottom:12px;">
      <input type="checkbox" v-model="settings.minimizeToTray" style="width:auto;">
      <div class="col" style="gap:2px;">
        <span>关闭窗口时最小化到托盘</span>
        <span class="hint">关闭按钮和 Dock 退出仅隐藏窗口，托盘图标保留</span>
      </div>
    </label>
    <label class="row" style="gap:8px; margin-bottom:12px;">
      <input type="checkbox" v-model="settings.startMinimized" style="width:auto;">
      <div class="col" style="gap:2px;">
        <span>启动时不显示窗口</span>
        <span class="hint">仅显示托盘图标，不弹出主窗口</span>
      </div>
    </label>
    <label class="row" style="gap:8px; margin-bottom:12px;">
      <input type="checkbox" v-model="settings.hideFromDock" style="width:auto;">
      <div class="col" style="gap:2px;">
        <span>不显示在 Dock 栏</span>
        <span class="hint">作为菜单栏应用运行，Dock 中不显示图标</span>
      </div>
    </label>
  </div>

  <div class="card">
    <div class="field">
      <label>配置文件路径</label>
      <div class="row">
        <input :value="configPath" readonly>
        <button @click="openFolder" style="width:auto; flex:none;">打开</button>
      </div>
    </div>
  </div>

  <div class="card">
    <div class="row" style="justify-content:space-between;">
      <div class="col" style="gap:2px;">
        <strong>退出程序</strong>
        <span class="hint">停止所有隧道并完全退出程序</span>
      </div>
      <button class="danger" @click="quitApp">退出程序</button>
    </div>
  </div>
</template>
