<script lang="ts" setup>
import {computed, ref} from 'vue'
import {useTunnelStore, type Tunnel} from '../stores/tunnel'
import {useRouter} from 'vue-router'
import * as App from '../../wailsjs/go/main/App'
import {copyText} from '../lib/clipboard'
import {confirmDialog, showError} from '../lib/dialog'

const store = useTunnelStore()
const router = useRouter()

const list = computed(() => store.tunnels)

const exporting = ref<string>('')
const exportCmd = ref('')
const exportName = ref('')
const busy = ref<string>('')

function statusText(s?: string) {
  switch (s) {
    case 'running': return '运行中'
    case 'connecting': return '连接中'
    case 'error': return '错误'
    default: return '已停止'
  }
}

function forwardText(f: any) {
  if (f.type === 'D') return `-D ${f.listen}`
  if (f.type === 'R') return `-R ${f.listen} → ${f.target}`
  return `-L ${f.listen} → ${f.target}`
}

async function toggle(t: Tunnel) {
  if (busy.value) return
  const running = t.status === 'running' || t.status === 'connecting'
  busy.value = t.id
  try {
    if (running) await store.stop(t.id)
    else await store.start(t.id)
  } catch (e: any) {
    showError('', e)
  } finally {
    busy.value = ''
  }
}

async function remove(t: Tunnel) {
  const name = t.name || `${t.user}@${t.host}`
  const ok = await confirmDialog(`确定删除隧道「${name}」？删除后无法恢复。`, {
    title: '删除隧道',
    okText: '删除',
    danger: true,
  })
  if (!ok) return
  try {
    await store.remove(t.id)
  } catch (e: any) {
    showError('删除失败: ', e)
  }
}

function edit(t: Tunnel) {
  router.push(`/edit/${t.id}`)
}

async function exportSsh(t: Tunnel) {
  exporting.value = t.id
  exportCmd.value = ''
  exportName.value = t.name || (t.user + '@' + t.host)
  try {
    exportCmd.value = await App.ToSSHCommandByID(t.id)
  } catch (e: any) {
    showError('导出失败: ', e)
  } finally {
    exporting.value = ''
  }
}

async function copyCmd() {
  const ok = await copyText(exportCmd.value)
  if (!ok) showError('复制失败: ', new Error('剪贴板不可用'))
}

async function startAll() {
  if (busy.value) return
  busy.value = 'all'
  try {
    await App.TunnelStartAll()
  } catch (e: any) {
    showError('', e)
  } finally {
    busy.value = ''
  }
}

async function stopAll() {
  if (busy.value) return
  busy.value = 'all'
  try {
    await App.TunnelStopAll()
  } catch (e: any) {
    showError('', e)
  } finally {
    busy.value = ''
  }
}

const anyActive = computed(() =>
  list.value.some(t => t.status === 'running' || t.status === 'connecting')
)
</script>

<template>
  <div class="page-title">
    <h2>隧道列表</h2>
    <div class="actions">
      <button @click="startAll" :disabled="busy === 'all' || list.length === 0">全部启用</button>
      <button @click="stopAll" :disabled="busy === 'all' || !anyActive">全部关闭</button>
      <router-link to="/import"><button>导入导出</button></router-link>
      <router-link to="/edit"><button class="primary">+ 新建隧道</button></router-link>
    </div>
  </div>

  <div v-if="list.length === 0" class="empty">
    <svg class="empty-icon" viewBox="0 0 120 120" width="80" height="80">
      <circle cx="60" cy="60" r="52" fill="none" stroke="var(--border)" stroke-width="3" stroke-dasharray="8 6"/>
      <path d="M40 55 L60 40 L80 55 L80 80 L40 80 Z" fill="none" stroke="var(--muted)" stroke-width="3" stroke-linejoin="round"/>
      <path d="M52 80 L52 65 L68 65 L68 80" fill="none" stroke="var(--muted)" stroke-width="3" stroke-linejoin="round"/>
    </svg>
    <div>暂无隧道配置，点击右上角"新建隧道"开始</div>
  </div>

  <div v-for="t in list" :key="t.id" class="card tunnel-card">
    <div class="info">
      <div class="name">
        {{ t.name || (t.user + '@' + t.host) }}
        <span class="badge" :class="t.status || 'stopped'" style="margin-left:8px;">
          {{ statusText(t.status) }}
        </span>
      </div>
      <div class="meta">
        <span>{{ t.user }}@{{ t.host }}:{{ t.port }}</span>
        <span>{{ t.authType === 'key' ? '密钥' : '密码' }}</span>
        <span v-if="t.autoReconnect">自动重连</span>
      </div>
      <div class="forwards" v-if="t.forwards?.length">
        <div v-for="(f, i) in t.forwards" :key="i">{{ forwardText(f) }}</div>
      </div>
      <div class="err" v-if="t.lastError">{{ t.lastError }}</div>
    </div>
    <div class="actions">
      <button class="ghost" @click="exportSsh(t)" title="导出 SSH 命令" :disabled="exporting === t.id">⌘</button>
      <button class="ghost" @click="edit(t)" title="编辑">✎</button>
      <button class="ghost danger" @click="remove(t)" title="删除">✕</button>
      <div class="toggle" :class="{on: t.status === 'running' || t.status === 'connecting', busy: busy === t.id}" @click="toggle(t)"></div>
    </div>
  </div>

  <div v-if="exportCmd" class="modal-mask" @click.self="exportCmd = ''">
    <div class="card modal">
      <div class="row" style="justify-content:space-between; margin-bottom:8px;">
        <strong>SSH 命令 - {{ exportName }}</strong>
        <button class="ghost" @click="exportCmd = ''">✕</button>
      </div>
      <textarea class="cmd" readonly :value="exportCmd" style="min-height:80px;"></textarea>
      <div class="actions" style="margin-top:8px; justify-content:flex-end;">
        <button class="primary" @click="copyCmd">复制</button>
      </div>
    </div>
  </div>
</template>
