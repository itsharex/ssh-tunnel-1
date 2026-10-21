<script lang="ts" setup>
import {ref} from 'vue'
import {useRouter} from 'vue-router'
import * as App from '../../wailsjs/go/main/App'
import {config} from '../../wailsjs/go/models'
import {useTunnelStore} from '../stores/tunnel'
import {copyText} from '../lib/clipboard'
import {showError} from '../lib/dialog'

const router = useRouter()
const store = useTunnelStore()

const tab = ref<'cmd' | 'json' | 'export'>('cmd')
const cmdline = ref('ssh -L 8080:localhost:80 -i ~/.ssh/id_rsa user@host')
const jsonText = ref('')
const mode = ref<'merge' | 'replace'>('merge')
const preview = ref<any>(null)
const err = ref('')
const msg = ref('')
const exportList = ref<config.Tunnel[]>([])
const exportCmds = ref<Record<string, string>>({})

async function parseCmd() {
  err.value = ''
  msg.value = ''
  preview.value = null
  try {
    preview.value = await App.ParseSSHCommand(cmdline.value)
  } catch (e: any) {
    err.value = e?.message || String(e)
  }
}

async function saveFromCmd() {
  if (!preview.value) {
    await parseCmd()
    if (!preview.value) return
  }
  try {
    await store.save(preview.value as config.Tunnel)
    router.push('/')
  } catch (e: any) {
    err.value = '保存失败: ' + (e?.message || String(e))
  }
}

async function importJson() {
  err.value = ''
  msg.value = ''
  try {
    const n = await App.ImportConfig(jsonText.value, mode.value)
    msg.value = `成功导入 ${n} 条隧道`
    await store.load()
  } catch (e: any) {
    err.value = e?.message || String(e)
  }
}

async function exportJson() {
  err.value = ''
  try {
    jsonText.value = await App.ExportConfig()
  } catch (e: any) {
    err.value = e?.message || String(e)
  }
}

async function loadExportList() {
  exportList.value = await App.TunnelList()
  exportCmds.value = {}
  for (const t of exportList.value) {
    exportCmds.value[t.id] = await App.ToSSHCommand(t)
  }
}

async function copy(text: string) {
  const ok = await copyText(text)
  if (!ok) showError('复制失败: ', new Error('剪贴板不可用'))
}

function forwardText(f: any) {
  if (f.type === 'D') return `-D ${f.listen}`
  if (f.type === 'R') return `-R ${f.listen} → ${f.target}`
  return `-L ${f.listen} → ${f.target}`
}
</script>

<template>
  <div class="page-title">
    <h2>导入导出</h2>
  </div>

  <div class="card">
    <div class="row" style="margin-bottom:12px; gap:0;">
      <button :class="{primary: tab === 'cmd'}" @click="tab = 'cmd'" style="border-radius:8px 0 0 8px;">从 SSH 命令导入</button>
      <button :class="{primary: tab === 'json'}" @click="tab = 'json'" style="border-radius:0;">导入/导出 JSON</button>
      <button :class="{primary: tab === 'export'}" @click="tab = 'export'; loadExportList()" style="border-radius:0 8px 8px 0;">导出 SSH 命令</button>
    </div>

    <div v-if="tab === 'cmd'">
      <div class="field">
        <label>SSH 命令行</label>
        <textarea class="cmd" v-model="cmdline"></textarea>
        <div class="hint">支持 -L / -R / -D / -p / -i / -N 等选项，例: ssh -L 8080:localhost:80 -i ~/.ssh/id_rsa user@host</div>
      </div>
      <div class="actions" style="margin-bottom:12px;">
        <button @click="parseCmd">解析预览</button>
        <button class="primary" @click="saveFromCmd">保存为隧道</button>
      </div>
      <div v-if="err" class="err" style="color:var(--danger); margin-bottom:8px;">{{ err }}</div>
      <div v-if="preview" class="card" style="background:var(--bg);">
        <div><strong>{{ preview.name }}</strong></div>
        <div class="meta" style="color:var(--muted); font-size:12px; margin-top:4px;">
          {{ preview.user }}@{{ preview.host }}:{{ preview.port }}
          · {{ preview.authType === 'key' ? '密钥: ' + preview.keyPath : '密码' }}
        </div>
        <div style="margin-top:6px; font-family:monospace; font-size:12px;">
          <div v-for="(f, i) in preview.forwards" :key="i">{{ forwardText(f) }}</div>
        </div>
      </div>
    </div>

    <div v-else-if="tab === 'json'">
      <div class="field">
        <label>配置 JSON</label>
        <textarea class="cmd" v-model="jsonText" placeholder='粘贴配置 JSON 或点击"导出当前"获取'></textarea>
      </div>
      <div class="row" style="margin-bottom:12px; gap:8px;">
        <label class="row" style="gap:4px;">
          <input type="radio" v-model="mode" value="merge" style="width:auto;"> 合并
        </label>
        <label class="row" style="gap:4px;">
          <input type="radio" v-model="mode" value="replace" style="width:auto;"> 覆盖
        </label>
      </div>
      <div class="actions">
        <button @click="importJson">导入</button>
        <button @click="exportJson">导出当前</button>
      </div>
      <div v-if="err" style="color:var(--danger); margin-top:8px;">{{ err }}</div>
      <div v-if="msg" style="color:var(--success); margin-top:8px;">{{ msg }}</div>
    </div>

    <div v-else>
      <div v-if="exportList.length === 0" class="hint" style="padding:20px 0; text-align:center;">暂无隧道配置</div>
      <div v-for="t in exportList" :key="t.id" class="card" style="background:var(--bg); margin-bottom:8px;">
        <div class="row" style="justify-content:space-between; margin-bottom:6px;">
          <strong>{{ t.name || (t.user + '@' + t.host) }}</strong>
          <button class="ghost" @click="copy(exportCmds[t.id])">复制</button>
        </div>
        <textarea class="cmd" readonly :value="exportCmds[t.id]" style="min-height:60px; font-size:12px;"></textarea>
      </div>
    </div>
  </div>
</template>
