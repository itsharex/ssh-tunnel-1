<script lang="ts" setup>
import {computed, onMounted, onUnmounted, ref} from 'vue'
import * as App from '../../wailsjs/go/main/App'
import {EventsOn, EventsOff} from '../../wailsjs/runtime/runtime'
import {confirmDialog, showError} from '../lib/dialog'

interface ConnSnapshot {
  id: string
  tunnelId: string
  tunnel: string
  forward: string
  proto: string
  src: string
  dst: string
  pid: number
  proc: string
  state: 'active' | 'closed'
  reason: string
  up: number
  down: number
  upRate: number
  downRate: number
  started: number
  closedAt: number
}

const ROW_H = 54      // 行高，虚拟滚动按固定行高计算，改这里要同步改 CSS
const OVERSCAN = 4    // 上下各多渲染几行，滚动时不会露白

const rows = ref<ConnSnapshot[]>([])
const filter = ref<'all' | 'active' | 'closed'>('all')
const keyword = ref('')
const busy = ref('')
const tick = ref(Date.now())

const scrollEl = ref<HTMLElement | null>(null)
const scrollTop = ref(0)
const viewH = ref(360)
const sbw = ref(0) // 滚动条占用的宽度，表头要跟着让位

const filtered = computed(() => {
  const kw = keyword.value.trim().toLowerCase()
  return rows.value.filter(c => {
    if (filter.value === 'active' && c.state !== 'active') return false
    if (filter.value === 'closed' && c.state !== 'closed') return false
    if (!kw) return true
    const hay = `${c.proc} ${c.dst} ${c.src} ${c.tunnel} ${c.proto} ${c.pid || ''}`
    return hay.toLowerCase().includes(kw)
  })
})

type SortKey = 'proc' | 'dst' | 'traffic' | 'rate' | 'dur'

// 默认不排序：null 表示保持后端快照的原始顺序（最新的在前）。
const sortKey = ref<SortKey | null>(null)
const sortDir = ref<1 | -1>(1)

// numeric: true 让 10.0.0.9 排在 10.0.0.10 前面；不缓存实例的话
// 每次比较都 new 一个 Collator，4096 行会直接卡住。
const collator = new Intl.Collator(undefined, {numeric: true, sensitivity: 'base'})

function sortValue(c: ConnSnapshot, key: SortKey, now: number): number | string {
  switch (key) {
    case 'proc': return procLabel(c)
    case 'dst': return c.dst || ''
    // 流量 / 速率是上下两行，按合计排更符合「谁用得最多」的直觉
    case 'traffic': return c.up + c.down
    case 'rate': return c.upRate + c.downRate
    // 已关闭的用固定时长，活跃的随 tick 增长；两者放在一起比才有意义
    case 'dur': return (c.state === 'closed' && c.closedAt ? c.closedAt : now) - c.started
  }
}

// 排序结果。放在 computed 里，所以每次推送（rows 变化）都会按当前列重排，
// 不需要额外监听——刷新后仍然是排好序的。
const sorted = computed(() => {
  const list = filtered.value
  const key = sortKey.value
  if (!key) return list

  const dir = sortDir.value
  // 只有按「时长」排时才依赖 tick：其它列的排序键与时间无关，
  // 不读 tick 就不会每秒白白重排一次 4096 行。
  const now = key === 'dur' ? tick.value : 0

  // 先算一遍排序键，避免比较函数里反复取字段、反复算时长
  const dec = list.map(c => ({c, v: sortValue(c, key, now)}))
  dec.sort((a, b) => {
    const r = typeof a.v === 'number' && typeof b.v === 'number'
      ? a.v - b.v
      : collator.compare(String(a.v), String(b.v))
    // sort 是稳定的：键相同保持原顺序，不会每秒乱跳
    return r * dir
  })
  return dec.map(d => d.c)
})

// 数值列先看大的，文本列先按字典序
function defaultDir(key: SortKey): 1 | -1 {
  return key === 'traffic' || key === 'rate' || key === 'dur' ? -1 : 1
}

// 点表头：换列 → 用该列的默认方向；同列再点 → 反向；第三次 → 取消排序回到默认
function toggleSort(key: SortKey) {
  if (sortKey.value !== key) {
    sortKey.value = key
    sortDir.value = defaultDir(key)
  } else if (sortDir.value === defaultDir(key)) {
    sortDir.value = defaultDir(key) === 1 ? -1 : 1
  } else {
    sortKey.value = null
    sortDir.value = 1
  }
  // 换了顺序，原来的滚动位置指向的是别的行了
  scrollTop.value = 0
  if (scrollEl.value) scrollEl.value.scrollTop = 0
}

function sortMark(key: SortKey) {
  if (sortKey.value !== key) return ''
  return sortDir.value === 1 ? '▲' : '▼'
}

const totalH = computed(() => sorted.value.length * ROW_H)
const startIdx = computed(() => Math.max(0, Math.floor(scrollTop.value / ROW_H) - OVERSCAN))
const endIdx = computed(() =>
  Math.min(sorted.value.length, Math.ceil((scrollTop.value + viewH.value) / ROW_H) + OVERSCAN)
)
const visible = computed(() => sorted.value.slice(startIdx.value, endIdx.value))
const offsetY = computed(() => startIdx.value * ROW_H)

const activeCount = computed(() => rows.value.filter(c => c.state === 'active').length)
const sumUp = computed(() => rows.value.reduce((a, c) => a + c.up, 0))
const sumDown = computed(() => rows.value.reduce((a, c) => a + c.down, 0))
const rateUp = computed(() => rows.value.reduce((a, c) => a + c.upRate, 0))
const rateDown = computed(() => rows.value.reduce((a, c) => a + c.downRate, 0))

function fmtBytes(n: number) {
  if (!n) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let v = n
  let i = 0
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  return `${i > 0 && v < 10 ? v.toFixed(1) : Math.round(v)} ${units[i]}`
}

const fmtRate = (n: number) => `${fmtBytes(n)}/s`

function fmtDur(ms: number) {
  if (!Number.isFinite(ms) || ms < 0) ms = 0
  const s = Math.floor(ms / 1000)
  const h = Math.floor(s / 3600)
  const m = Math.floor((s % 3600) / 60)
  const sec = s % 60
  if (h > 0) return `${h}h${String(m).padStart(2, '0')}m`
  if (m > 0) return `${m}m${String(sec).padStart(2, '0')}s`
  return `${sec}s`
}

function reasonText(r: string) {
  switch (r) {
    case 'user': return '已手动断开'
    case 'tunnel': return '隧道停止'
    case 'dial': return '连接目标失败'
    default: return '已结束'
  }
}

function procLabel(c: ConnSnapshot) {
  if (c.proc) return c.proc
  if (c.pid) return `PID ${c.pid}`
  // 远程转发（-R）的发起方在 SSH 服务器那一侧，本机查不到进程
  return c.state === 'active' ? '外部连接' : '—'
}

function onUpdate(list: ConnSnapshot[]) {
  rows.value = list || []
  tick.value = Date.now()
  armWatchdog()
}

// 看门狗：万一「离开页面」的停止指令比「进入页面」的启动指令更晚到达后端，
// 推送会意外停住。超过 2.5s 没收到更新就重启一次推送。
let watchdog: number | undefined
let ro: ResizeObserver | undefined
let mounted = false
function armWatchdog() {
  window.clearTimeout(watchdog)
  watchdog = window.setTimeout(async () => {
    if (!mounted) return
    await App.ConnWatch(false)
    // 重新检查：await 期间用户可能已经离开页面。
    // 不检查的话会在这里把推送重新打开，而 unmounted 里的关闭指令已经执行过了，
    // 结果就是离开页面后后端还在每秒采样 + 序列化 + 往死掉的页面推事件。
    if (!mounted) return
    await App.ConnWatch(true)
    if (!mounted) {
      // 启动请求发出之后才离开页面：补一次关闭，
      // 否则推送会一直开着，前端却再也收不到（也再没人关）它。
      await App.ConnWatch(false)
      return
    }
    armWatchdog()
  }, 2500)
}

function measure() {
  const el = scrollEl.value
  if (!el) return
  viewH.value = el.clientHeight
  // 经典滚动条（Linux 的 WebKitGTK、旧版 WebView2、macOS 设了「始终显示滚动条」）
  // 会占掉 tbody 的内容宽度，而表头不在同一个滚动容器里。
  // 不让出同样的宽度，每一列都会错开一条滚动条——1fr 列先吃掉一点，定宽列整体左移。
  sbw.value = el.offsetWidth - el.clientWidth
}

function onScroll() {
  scrollTop.value = scrollEl.value ? scrollEl.value.scrollTop : 0
}

// 主动拉一次快照。断开操作后立刻调用，让界面当场反映结果，
// 而不是等最多 1 秒后的下一次推送——「点了没反应」正是这类延迟给人的错觉。
async function refresh() {
  try {
    rows.value = ((await App.ConnList()) as unknown as ConnSnapshot[]) || []
  } catch {
    /* 拉取失败就保持原样，下一次推送会补上 */
  }
  tick.value = Date.now()
}

async function kill(c: ConnSnapshot) {
  if (busy.value) return
  busy.value = c.id
  try {
    await App.ConnKill(c.id)
  } catch (e: any) {
    showError('断开失败: ', e)
  } finally {
    busy.value = ''
  }
  await refresh()
}

async function killAll() {
  if (busy.value || activeCount.value === 0) return
  const ok = await confirmDialog(
    `将断开全部 ${activeCount.value} 条活跃连接。这会中断正在使用这些连接的客户端，确定继续？`,
    {title: '断开全部连接', okText: '全部断开', danger: true},
  )
  if (!ok) return
  busy.value = 'all'
  try {
    await App.ConnKillAll()
  } catch (e: any) {
    showError('断开失败: ', e)
  } finally {
    busy.value = ''
  }
  await refresh()
}

onMounted(async () => {
  mounted = true
  await refresh()
  // 只有页面打开时才让后端采样推送，避免用户不看的时候还在每秒序列化整张表
  await App.ConnWatch(true)
  EventsOn('conn:update', onUpdate)
  armWatchdog()
  measure()
  window.addEventListener('resize', measure)
  // 列表从空到撑出滚动条（或反过来）时 clientWidth 会变，
  // 表头的让位宽度得跟着更新，否则一开始对齐、来了数据就错开。
  ro = new ResizeObserver(measure)
  if (scrollEl.value) ro.observe(scrollEl.value)
})

onUnmounted(() => {
  mounted = false
  window.clearTimeout(watchdog)
  EventsOff('conn:update')
  void App.ConnWatch(false)
  window.removeEventListener('resize', measure)
  ro?.disconnect()
  ro = undefined
})
</script>

<template>
  <div class="conn-page">
    <div class="page-title">
      <h2>连接监控</h2>
      <div class="actions">
        <button class="danger" :disabled="!!busy || activeCount === 0" @click="killAll">全部断开</button>
      </div>
    </div>

    <div class="stats">
      <div class="stat">
        <span class="k">活跃连接</span>
        <span class="v">{{ activeCount }}</span>
      </div>
      <div class="stat">
        <span class="k">总上行 / 下行</span>
        <span class="v small">{{ fmtBytes(sumUp) }} / {{ fmtBytes(sumDown) }}</span>
      </div>
      <div class="stat">
        <span class="k">当前速率</span>
        <span class="v small">
          <em class="up">↑ {{ fmtRate(rateUp) }}</em>
          <em class="down">↓ {{ fmtRate(rateDown) }}</em>
        </span>
      </div>
    </div>

    <div class="toolbar">
      <div class="tabs">
        <button :class="{on: filter === 'all'}" @click="filter = 'all'">全部 {{ rows.length }}</button>
        <button :class="{on: filter === 'active'}" @click="filter = 'active'">活动中 {{ activeCount }}</button>
        <button :class="{on: filter === 'closed'}" @click="filter = 'closed'">
          已关闭 {{ rows.length - activeCount }}
        </button>
      </div>
      <input v-model="keyword" class="search" type="search" placeholder="搜索进程、目标、隧道…" />
    </div>

    <div class="table">
      <!-- paddingRight 补上滚动条占掉的宽度，让表头与行共用同一条内容宽度 -->
      <div class="thead" :style="{paddingRight: 12 + sbw + 'px'}">
        <span></span>
        <button class="th" :class="{on: sortKey === 'proc'}" @click="toggleSort('proc')">
          进程 / 来源<i class="mark">{{ sortMark('proc') }}</i>
        </button>
        <button class="th" :class="{on: sortKey === 'dst'}" @click="toggleSort('dst')">
          目标 / 隧道<i class="mark">{{ sortMark('dst') }}</i>
        </button>
        <button class="th num" :class="{on: sortKey === 'traffic'}" @click="toggleSort('traffic')">
          流量<i class="mark">{{ sortMark('traffic') }}</i>
        </button>
        <button class="th num" :class="{on: sortKey === 'rate'}" @click="toggleSort('rate')">
          速率<i class="mark">{{ sortMark('rate') }}</i>
        </button>
        <button class="th num" :class="{on: sortKey === 'dur'}" @click="toggleSort('dur')">
          时长<i class="mark">{{ sortMark('dur') }}</i>
        </button>
        <span></span>
      </div>

      <div ref="scrollEl" class="tbody" @scroll="onScroll">
        <div v-if="sorted.length === 0" class="empty-hint">
          {{ rows.length === 0 ? '当前没有连接。启动隧道并产生流量后会显示在这里。' : '没有匹配的连接' }}
        </div>
        <div v-else :style="{height: totalH + 'px', position: 'relative'}">
          <div :style="{transform: `translateY(${offsetY}px)`}">
            <div
              v-for="c in visible"
              :key="c.id"
              class="row"
              :class="{closed: c.state === 'closed' && c.reason !== 'dial', failed: c.reason === 'dial'}"
            >
              <span class="dot" :title="c.state === 'closed' ? reasonText(c.reason) : '活动中'"></span>

              <span class="cell two">
                <span class="l1" :title="c.proc ? `${c.proc} (PID ${c.pid})` : ''">{{ procLabel(c) }}</span>
                <span class="l2">{{ c.src || '—' }}</span>
              </span>

              <span class="cell two">
                <span class="l1 mono" :title="c.dst">{{ c.dst || '—' }}</span>
                <span class="l2">
                  {{ c.tunnel || '—' }}<i class="sep">·</i>{{ c.proto || '未识别' }}
                  <i v-if="c.state === 'closed'" class="tag">{{ reasonText(c.reason) }}</i>
                </span>
              </span>

              <span class="cell two num">
                <span class="l1 up">↑ {{ fmtBytes(c.up) }}</span>
                <span class="l2 down">↓ {{ fmtBytes(c.down) }}</span>
              </span>

              <span class="cell two num">
                <span class="l1 up">{{ c.state === 'active' ? fmtRate(c.upRate) : '—' }}</span>
                <span class="l2 down">{{ c.state === 'active' ? fmtRate(c.downRate) : '—' }}</span>
              </span>

              <span class="cell dur">
                {{ fmtDur(c.state === 'closed' && c.closedAt ? c.closedAt - c.started : tick - c.started) }}
              </span>

              <span class="cell act">
                <button
                  v-if="c.state === 'active'"
                  class="ghost danger"
                  :disabled="!!busy"
                  title="断开这条连接"
                  @click="kill(c)"
                >✕</button>
              </span>
            </div>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
/* 撑满主区高度，让表格自己吃掉剩余空间。
   48px 是 .main 的上下内边距（24 × 2），是固定值，不随字体变化。 */
.conn-page {display: flex; flex-direction: column; height: calc(100vh - 48px);}

.stats {display: flex; gap: 12px; margin-bottom: 12px;}
.stat {
  flex: 1; background: var(--panel); border: 1px solid var(--border);
  border-radius: var(--radius); padding: 10px 14px; display: flex; flex-direction: column; gap: 2px;
}
.stat .k {font-size: 12px; color: var(--muted);}
.stat .v {font-size: 20px; font-weight: 600; font-variant-numeric: tabular-nums;}
.stat .v.small {font-size: 15px; font-weight: 500; display: flex; gap: 10px;}
.stat em {font-style: normal; font-variant-numeric: tabular-nums;}

.toolbar {display: flex; align-items: center; gap: 12px; margin-bottom: 10px;}
.tabs {display: flex; gap: 4px;}
.tabs button {padding: 4px 10px; font-size: 13px; border-color: transparent; background: transparent; color: var(--muted);}
.tabs button.on {background: var(--panel); border-color: var(--border); color: var(--text); box-shadow: var(--shadow);}
.search {flex: 1; max-width: 260px; margin-left: auto; font-size: 13px;}

.table {
  background: var(--panel); border: 1px solid var(--border); border-radius: var(--radius);
  overflow: hidden; box-shadow: var(--shadow); display: flex; flex-direction: column;
  flex: 1; min-height: 200px;
}

.thead, .row {
  display: grid;
  grid-template-columns: 16px minmax(110px, 1.1fr) minmax(130px, 1.7fr) 92px 96px 56px 32px;
  gap: 10px;
  align-items: center;
  padding: 0 12px;
}
.thead {
  height: 32px; font-size: 12px; color: var(--muted);
  border-bottom: 1px solid var(--border); background: #fafbfc; flex: none;
}

/* 表头是可点的排序按钮。数值列必须和数据单元格一样右对齐，
   否则「流量」「速率」贴在列左边、数字贴在列右边，中间差一整列宽。 */
.th {
  width: 100%; height: 100%; min-width: 0;
  display: flex; align-items: center; justify-content: flex-start;
  padding: 0; border: 0; background: transparent;
  font: inherit; font-size: 12px; color: var(--muted);
  border-radius: 4px; cursor: pointer;
  /* 全局 button:hover 会抬 1px、染蓝边，表头不要这些 */
  transform: none;
}
.th:hover {color: var(--text); background: var(--bg); border-color: transparent; transform: none;}
.th.num {justify-content: flex-end;}
.th.on {color: var(--text); font-weight: 500;}

/* 箭头始终占位（宽 9px），切换排序时表头文字不会左右抖 */
.th .mark {
  flex: none; width: 9px; margin-left: 4px;
  font-size: 9px; font-style: normal;
  opacity: 0; transition: opacity var(--transition);
}
.th.on .mark {opacity: 1;}
/* 右对齐的列把箭头放到文字左边，文字的右边缘才能和数字的右边缘对齐 */
.th.num .mark {order: -1; margin-left: 0; margin-right: 4px;}
.tbody {
  flex: 1;
  /* flex 子项默认 min-height:auto，不会收缩到内容高度以下，
     表格会被撑破、滚不动。必须显式置 0。 */
  min-height: 0;
  overflow-y: auto;
  position: relative;
}

.row {height: 54px; border-bottom: 1px solid var(--border); transition: background var(--transition);}
.row:hover {background: var(--bg);}
.row.closed {opacity: .45;}
/* 拨号失败的行不做淡化：它是最需要被看见的一类，只在保留期内短暂出现 */
.row.failed .dot {background: var(--danger);}
.row.failed .tag {color: var(--danger); border-color: var(--danger);}

.dot {width: 8px; height: 8px; border-radius: 50%; background: var(--success); display: block;}
.row.closed .dot {background: var(--muted);}

.cell {display: flex; flex-direction: column; gap: 2px; overflow: hidden;}
.cell.two .l1, .cell.two .l2 {white-space: nowrap; overflow: hidden; text-overflow: ellipsis;}
.cell .l1 {font-size: 13px; color: var(--text);}
.cell .l2 {font-size: 11px; color: var(--muted);}
.cell.num {align-items: flex-end; text-align: right; font-variant-numeric: tabular-nums;}
.cell .up {color: var(--primary);}
.cell .down {color: var(--success);}
.cell.dur {
  font-size: 12px; color: var(--muted); font-variant-numeric: tabular-nums;
  /* 与表头一致：时长也是数值列，右对齐后数字末尾对齐，扫视更快 */
  text-align: right; white-space: nowrap;
}
.cell.act {align-items: flex-end;}
.cell.act button {padding: 2px 6px; font-size: 12px;}
.mono {font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace; font-size: 12px;}
.sep {font-style: normal; margin: 0 5px; color: var(--border);}
.tag {
  font-style: normal; margin-left: 6px; padding: 0 5px; border-radius: 3px;
  background: var(--bg); border: 1px solid var(--border); font-size: 10px; color: var(--muted);
}

.empty-hint {padding: 40px 16px; text-align: center; color: var(--muted); font-size: 13px;}
</style>
