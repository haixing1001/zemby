<template>
  <div>
    <div class="log-head">
      <h1 class="adm-h1">日志管理</h1>
      <div class="log-tools">
        <button class="tool-btn" title="清空日志" @click="clearAll">🗑</button>
      </div>
    </div>
    <div class="adm-desc">按分类查看系统运行日志 · 实时推送（SSE）· 保留最近 2000 条</div>

    <div class="chip-rows">
      <div class="chip-rows-inner">
        <button
          v-for="c in cats" :key="c.key"
          class="cat-chip" :class="{ active: current === c.key }"
          @click="selectCat(c.key)"
        >
          {{ c.name }}<span class="chip-count" v-if="counts[c.key === 'error' ? 'error' : c.key]">{{ counts[c.key === 'error' ? 'error' : c.key] }}</span>
        </button>
      </div>
    </div>

    <div class="log-refresh">最近刷新 <span class="refresh-time">{{ refreshAt }}</span><span class="live-dot" :class="{ off: !sseOk }"></span>{{ sseOk ? '实时' : '已断开' }}</div>

    <div class="log-cards" ref="listBox">
      <div class="log-card" v-for="e in entries" :key="e.id">
        <div class="lc-title" :class="e.level">
          {{ e.message }}<span v-if="e.level === 'error'" class="lc-fail"> · 失败</span><span v-else-if="e.level === 'warn'" class="lc-warn-tag"> · 警告</span>
        </div>
        <div class="lc-time">{{ fmtTime(e.time) }}</div>
        <div class="lc-cat" v-if="e.category && e.category !== 'system'">{{ catName(e.category) }}</div>
        <div class="lc-detail" v-if="e.detail">{{ e.detail }}</div>
      </div>
      <div v-if="!entries.length" class="log-empty">暂无{{ catName(current) }}日志</div>
    </div>
  </div>
</template>

<script setup>
import { ref, reactive, onMounted, onBeforeUnmount } from 'vue'
import { api, state } from '../../api/client'
import { errText, toast } from './util'

const CATS = [
  { key: '', name: '全部' },
  { key: 'subtitle', name: '字幕' },
  { key: 'tmdb', name: 'TMDB' },
  { key: 'scrape', name: '刮削' },
  { key: 'scan', name: '扫描媒体' },
  { key: 'probe', name: '媒体信息提取' },
  { key: 'playback', name: '用户播放' },
  { key: 'redirect', name: '302日志' },
  { key: 'error', name: '错误日志' },
]

const CAT_NAMES = { system: '系统', scan: '扫描媒体', scrape: '刮削', tmdb: 'TMDB', probe: '媒体信息提取', subtitle: '字幕', playback: '用户播放', redirect: '302日志' }

const cats = CATS
const current = ref('')
const entries = ref([])
const counts = reactive({})
const refreshAt = ref('--:--:--')
const sseOk = ref(false)
let es = null

function catName(key) {
  if (!key) return ''
  if (key === 'error') return '错误'
  return CAT_NAMES[key] || key
}

function fmtTime(t) {
  if (!t) return '-'
  const d = new Date(t)
  if (isNaN(d.getTime())) return '-'
  const p = (n) => String(n).padStart(2, '0')
  return `${d.getFullYear()}/${d.getMonth() + 1}/${d.getDate()} ${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`
}

function matches(e) {
  if (current.value === '') return true
  if (current.value === 'error') return e.level === 'error'
  return e.category === current.value
}

function applyCounts(c) {
  Object.keys(counts).forEach((k) => delete counts[k])
  Object.assign(counts, c || {})
}

async function load() {
  try {
    const r = await api.admin.logs(current.value === 'error' ? '' : current.value, current.value === 'error' ? 'error' : '', 500)
    entries.value = r.Entries || []
    applyCounts(r.Counts)
    refreshAt.value = new Date().toTimeString().slice(0, 8)
  } catch (e) {
    toast(errText(e), true)
  }
}

function selectCat(key) {
  current.value = key
  load()
}

async function clearAll() {
  if (!confirm('确定清空全部日志？此操作不可恢复。')) return
  try {
    await api.admin.clearLogs()
    toast('日志已清空')
    load()
  } catch (e) {
    toast(errText(e), true)
  }
}

function connectSSE() {
  if (es) { es.close(); es = null }
  es = new EventSource(api.admin.logsStreamUrl())
  es.onopen = () => { sseOk.value = true }
  es.onerror = () => { sseOk.value = false }
  es.onmessage = (ev) => {
    try {
      const e = JSON.parse(ev.data)
      if (!e || !e.id) return
      sseOk.value = true
      refreshAt.value = new Date().toTimeString().slice(0, 8)
      counts[e.category] = (counts[e.category] || 0) + 1
      if (e.level === 'error') counts.error = (counts.error || 0) + 1
      if (matches(e)) {
        entries.value.unshift(e)
        if (entries.value.length > 500) entries.value.pop()
      }
    } catch {}
  }
}

onMounted(() => {
  load()
  connectSSE()
})

onBeforeUnmount(() => {
  if (es) { es.close(); es = null }
})
</script>

<style scoped>
.log-head { display: flex; align-items: center; justify-content: space-between; }
.log-tools { display: flex; gap: 8px; }
.tool-btn {
  width: 34px; height: 34px; border-radius: 9px; border: 1px solid #232a34;
  background: #171b22; color: #aab3bf; font-size: 15px; cursor: pointer; transition: all .15s;
}
.tool-btn:hover { border-color: #f0616d; color: #f0616d; }

.chip-rows { margin: 4px 0 14px; }
.chip-rows-inner { display: flex; flex-wrap: wrap; gap: 10px; }
.cat-chip {
  padding: 8px 16px; border-radius: 9px; border: 1px solid #232a34;
  background: #171b22; color: #c3c9d1; font-size: 13px; cursor: pointer;
  display: inline-flex; align-items: center; gap: 7px; transition: all .15s;
}
.cat-chip:hover { border-color: #334052; color: #fff; }
.cat-chip.active { border-color: #4a9eff; color: #eaf3ff; background: #14202e; }
.chip-count {
  font-size: 11px; background: #232a34; border-radius: 10px; padding: 1px 7px; color: #8b949e;
}
.cat-chip.active .chip-count { background: #1d3450; color: #7fb8f5; }

.log-refresh {
  font-size: 12.5px; color: #6b7482; margin-bottom: 12px;
  display: flex; align-items: center; gap: 7px;
}
.refresh-time { color: #9aa3ad; }
.live-dot { width: 7px; height: 7px; border-radius: 50%; background: #34c759; margin-left: 8px; display: inline-block; }
.live-dot.off { background: #f0616d; }

.log-cards {
  background: #12151b; border: 1px solid #1b2028; border-radius: 12px;
  max-height: calc(100vh - 320px); min-height: 200px; overflow-y: auto; padding: 4px 0;
}
.log-card { padding: 15px 20px; border-bottom: 1px solid #1a1f27; }
.log-card:last-child { border-bottom: none; }
.lc-title { font-size: 13.5px; font-weight: 600; color: #e8eaed; margin-bottom: 7px; word-break: break-all; }
.lc-title.error { color: #f0616d; }
.lc-title.warn { color: #e0a24a; }
.lc-fail, .lc-warn-tag { font-weight: 700; }
.lc-time { font-size: 12px; color: #6b7482; margin-bottom: 8px; }
.lc-cat {
  display: inline-block; font-size: 11px; color: #7fa7e0; background: #1c2531;
  border-radius: 10px; padding: 1px 8px; margin-bottom: 8px;
}
.lc-detail {
  font-size: 12.5px; color: #8b949e; line-height: 1.75; word-break: break-all;
  white-space: pre-wrap; background: #0e1116; border-radius: 8px; padding: 9px 12px;
}
.log-empty { padding: 48px 20px; text-align: center; color: #5a626e; font-size: 13px; }
</style>
