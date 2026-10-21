<script lang="ts" setup>
import {onMounted, onUnmounted} from 'vue'
import {useTunnelStore} from './stores/tunnel'
import {EventsOn, EventsOff} from '../wailsjs/runtime/runtime'
import * as App from '../wailsjs/go/main/App'
import DialogHost from './components/DialogHost.vue'

const store = useTunnelStore()

let mounted = true
onMounted(async () => {
  mounted = true
  await store.load()
  EventsOn('tunnel:status', (e: any) => store.onStatus(e.id, e.status, e.lastError))
})
onUnmounted(() => {
  mounted = false
  EventsOff('tunnel:status')
})

async function quitApp() {
  await App.QuitApp()
}
</script>

<template>
  <div class="layout">
    <aside class="sidebar">
      <div class="logo">SSH Tunnel</div>
      <nav>
        <router-link to="/">隧道列表</router-link>
        <router-link to="/connections">连接监控</router-link>
        <router-link to="/edit">新建隧道</router-link>
        <router-link to="/import">导入导出</router-link>
        <router-link to="/settings">设置</router-link>
      </nav>
      <div class="spacer"></div>
      <nav>
        <a href="#" @click.prevent="store.load()">刷新</a>
        <a href="#" class="danger-link" @click.prevent="quitApp">退出程序</a>
      </nav>
    </aside>
    <main class="main">
      <router-view />
    </main>
    <DialogHost />
  </div>
</template>
