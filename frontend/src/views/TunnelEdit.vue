<script lang="ts" setup>
import {ref, onMounted, computed, watch} from 'vue'
import {useRoute, useRouter} from 'vue-router'
import {useTunnelStore, type Tunnel} from '../stores/tunnel'
import * as App from '../../wailsjs/go/main/App'
import {config} from '../../wailsjs/go/models'

const route = useRoute()
const router = useRouter()
const store = useTunnelStore()

function blankTunnel(): Tunnel {
  return new config.Tunnel({
    name: '',
    host: '',
    port: 22,
    user: '',
    authType: 'key',
    keyPath: '',
    proxy: {type: '', host: '', port: 1080, user: '', password: ''},
    forwards: [],
    autoReconnect: true,
    reconnectMinMs: 1000,
    reconnectMaxMs: 30000,
    serverAliveInterval: 30,
  })
}

const t = ref<Tunnel>(blankTunnel())

const testing = ref(false)
const testMsg = ref('')

async function loadTunnel() {
  const id = route.params.id as string | undefined
  testMsg.value = ''
  if (!id) {
    // 从「编辑」切到「新建」时要彻底重置，否则会带着上一条隧道的 ID 保存
    t.value = blankTunnel()
    return
  }
  const found = await App.TunnelList().then(list => list.find(x => x.id === id))
  t.value = found ?? blankTunnel()
  // 兼容旧配置：proxy 可能为 undefined，补默认对象避免绑定报错
  if (!t.value.proxy) {
    t.value.proxy = new config.Proxy({type: '', host: '', port: 1080, user: '', password: ''})
  }
}

onMounted(loadTunnel)
// /edit/:id 与 /edit 是同一条路由记录，组件会被复用、onMounted 不再触发，
// 所以必须监听参数变化来重新加载表单。
watch(() => route.params.id, loadTunnel)

function addForward() {
  t.value.forwards = t.value.forwards || []
  t.value.forwards.push(new config.Forward({type: 'L', listen: '', target: ''}))
}
function delForward(i: number) {
  t.value.forwards.splice(i, 1)
}

async function save() {
  if (!t.value.host || !t.value.user) {
    alert('请填写主机和用户')
    return
  }
  if (!t.value.forwards?.length) {
    alert('请至少添加一条转发规则')
    return
  }
  try {
    await store.save(t.value)
    router.push('/')
  } catch (e: any) {
    alert('保存失败: ' + (e?.message || String(e)))
  }
}

async function test() {
  testing.value = true
  testMsg.value = ''
  try {
    await App.TestConnection(t.value)
    testMsg.value = '连接成功'
  } catch (e: any) {
    testMsg.value = '连接失败: ' + (e?.message || String(e))
  } finally {
    testing.value = false
  }
}

const forwardsValid = computed(() => t.value.forwards && t.value.forwards.length > 0)
</script>

<template>
  <div class="page-title">
    <h2>{{ route.params.id ? '编辑隧道' : '新建隧道' }}</h2>
    <div class="actions">
      <button @click="router.push('/')">取消</button>
      <button @click="test" :disabled="testing">{{ testing ? '测试中...' : '测试连接' }}</button>
      <button class="primary" @click="save">保存</button>
    </div>
  </div>

  <div v-if="testMsg" class="card" style="margin-bottom:12px;">{{ testMsg }}</div>

  <div class="card">
    <div class="field">
      <label>名称</label>
      <input v-model="t.name" placeholder="可选，如 my-server">
    </div>
    <div style="display:grid; grid-template-columns:2fr 1fr; gap:12px;">
      <div class="field">
        <label>主机 / Host</label>
        <input v-model="t.host" placeholder="如 192.168.1.10 或 example.com">
      </div>
      <div class="field">
        <label>端口 / Port</label>
        <input type="number" v-model.number="t.port">
      </div>
    </div>
    <div class="field">
      <label>用户 / User</label>
      <input v-model="t.user" placeholder="如 root">
    </div>
  </div>

  <div class="card">
    <div class="row" style="justify-content:space-between; margin-bottom:12px;">
      <strong>认证方式</strong>
      <select v-model="t.authType" style="width:120px;">
        <option value="key">密钥文件</option>
        <option value="password">密码</option>
      </select>
    </div>
    <template v-if="t.authType === 'key'">
      <div class="field">
        <label>密钥路径</label>
        <input v-model="t.keyPath" placeholder="如 ~/.ssh/id_rsa">
      </div>
      <div class="field">
        <label>密钥密码短语（可选）</label>
        <input type="password" v-model="t.keyPassphrase">
      </div>
    </template>
    <template v-else>
      <div class="field">
        <label>密码</label>
        <input type="password" v-model="t.password">
      </div>
    </template>
  </div>

  <div class="card">
    <div class="row" style="justify-content:space-between; margin-bottom:12px;">
      <strong>代理（绕过网络限制）</strong>
      <select v-model="t.proxy!.type" style="width:140px;">
        <option value="">不使用代理</option>
        <option value="http">HTTP</option>
        <option value="https">HTTPS</option>
        <option value="socks5">SOCKS5</option>
      </select>
    </div>
    <template v-if="t.proxy!.type">
      <div style="display:grid; grid-template-columns:2fr 1fr; gap:12px;">
        <div class="field">
          <label>代理主机</label>
          <input v-model="t.proxy!.host" placeholder="如 127.0.0.1 或 proxy.example.com">
        </div>
        <div class="field">
          <label>代理端口</label>
          <input type="number" v-model.number="t.proxy!.port">
        </div>
      </div>
      <div style="display:grid; grid-template-columns:1fr 1fr; gap:12px;">
        <div class="field">
          <label>用户名（可选）</label>
          <input v-model="t.proxy!.user" placeholder="代理认证用户名">
        </div>
        <div class="field">
          <label>密码（可选）</label>
          <input type="password" v-model="t.proxy!.password" placeholder="代理认证密码">
        </div>
      </div>
      <div class="hint">到 SSH 服务器的连接将经该代理建立，绕过对 SSH 端口的网络限制。</div>
    </template>
  </div>

  <div class="card">
    <div class="row" style="justify-content:space-between; margin-bottom:12px;">
      <strong>端口转发规则</strong>
      <button @click="addForward">+ 添加</button>
    </div>
    <div v-if="!forwardsValid" class="hint" style="margin-bottom:8px;">至少添加一条转发规则</div>
    <div v-for="(f, i) in t.forwards" :key="i" class="forward-row">
      <select v-model="f.type" class="type-select">
        <option value="L">-L 本地</option>
        <option value="R">-R 远程</option>
        <option value="D">-D 动态</option>
      </select>
      <input v-model="f.listen" :placeholder="f.type === 'D' ? '本地监听，如 1080' : (f.type === 'R' ? '远端监听，如 9090 或 0.0.0.0:9090' : '本地监听，如 8080')">
      <input v-if="f.type !== 'D'" v-model="f.target" :placeholder="f.type === 'R' ? '本地目标，如 localhost:90' : '远端目标，如 localhost:80'">
      <button class="del danger" @click="delForward(i)">✕</button>
    </div>
  </div>

  <div class="card">
    <strong style="display:block; margin-bottom:12px;">高级选项</strong>
    <label class="row" style="gap:8px; margin-bottom:12px;">
      <input type="checkbox" v-model="t.autoReconnect" style="width:auto;">
      <span>断线自动重连</span>
    </label>
    <div style="display:grid; grid-template-columns:1fr 1fr 1fr; gap:12px;">
      <div class="field">
        <label>最小退避 (ms)</label>
        <input type="number" v-model.number="t.reconnectMinMs">
      </div>
      <div class="field">
        <label>最大退避 (ms)</label>
        <input type="number" v-model.number="t.reconnectMaxMs">
      </div>
      <div class="field">
        <label>心跳间隔 (秒, 0=关闭)</label>
        <input type="number" v-model.number="t.serverAliveInterval">
      </div>
    </div>
  </div>
</template>
