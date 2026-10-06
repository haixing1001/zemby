<template>
  <div>
    <h1 class="adm-h1">刮削管理</h1>
    <div class="adm-desc">扫描媒体库文件并获取或更新元数据与图片</div>

    <div class="collapse-sec">
      <div class="body" style="padding:2px 16px 12px;">
        <!-- 开关行 -->
        <div class="set-row">
          <div><div class="t">开启刮削</div><div class="d">处理当前媒体索引中的媒体</div></div>
          <label class="switch"><input type="checkbox" v-model="cfg.enabled" @change="save" /><span class="track"></span><span class="knob"></span></label>
        </div>
        <div class="set-row">
          <div><div class="t">实时监控</div><div class="d">监控新增内容并自动刮削（每 2 分钟增量扫描）</div></div>
          <label class="switch"><input type="checkbox" v-model="cfg.realtime" @change="save" /><span class="track"></span><span class="knob"></span></label>
        </div>
        <div class="set-row">
          <div><div class="t">自动刷新</div><div class="d">刮削完成后刷新本地媒体索引</div></div>
          <label class="switch"><input type="checkbox" v-model="cfg.autoRefresh" @change="save" /><span class="track"></span><span class="knob"></span></label>
        </div>
        <div class="set-row">
          <div><div class="t">手动刮削</div><div class="d">按当前配置执行手动刮削任务</div></div>
          <label class="switch"><input type="checkbox" v-model="cfg.manual" @change="save" /><span class="track"></span><span class="knob"></span></label>
        </div>
        <div class="set-row">
          <div><div class="t">刮削器（至少选择一个）</div><div class="d">选择元数据与图片来源</div></div>
          <router-link to="/admin/tmdb" class="right">TMDB ›</router-link>
        </div>
        <div class="set-row" style="border:none;">
          <div><div class="t">覆盖策略</div><div class="d">设置已有 NFO 和图片的处理方式</div></div>
          <a class="right" href="javascript:void(0)" @click="cycleOverwrite">
            {{ cfg.overwrite === 'overwrite' ? '覆盖已有文件' : '跳过已有文件' }} ›
          </a>
        </div>
      </div>
    </div>

    <!-- 当前任务 -->
    <div class="set-row" style="border:none;">
      <div>
        <div class="t">当前任务</div>
        <div class="d">
          {{ stateText }}
          <template v-if="st.Pending">· 待刮削 {{ st.Pending }}</template>
          <template v-if="st.Failed">· 失败 {{ st.Failed }}</template>
        </div>
      </div>
    </div>
    <div style="display:flex;gap:10px;margin:6px 0 22px;">
      <button class="btn ghost" @click="ctrl('scan')">扫描</button>
      <button class="btn" @click="ctrl('start')">开始</button>
      <button class="btn ghost" @click="ctrl('pause')" :disabled="st.State !== 'running'">暂停</button>
      <button class="btn ghost" @click="ctrl('stop')" :disabled="st.State === 'idle'">停止</button>
    </div>

    <!-- 失败清单 -->
    <div class="collapse-sec">
      <div class="head">
        刮削失败（{{ failed.length }}）
        <span style="flex:1"></span>
        <button v-if="failed.length" class="btn sm" @click="retryAll">重试全部</button>
      </div>
      <div class="body" v-if="failed.length">
        <table class="tbl">
          <thead><tr><th>名称</th><th style="width:80px;">类型</th><th>失败原因</th><th style="width:80px;">操作</th></tr></thead>
          <tbody>
            <tr v-for="f in failed" :key="f.ID">
              <td><a href="javascript:void(0)" @click="$router.push('/item/' + f.ID)">{{ f.Name }}</a><div class="muted" style="font-size:11.5px;">{{ f.Path }}</div></td>
              <td>{{ f.Type === 'Series' ? '剧集' : '电影' }}</td>
              <td class="muted" style="font-size:12.5px;">{{ f.Error }}</td>
              <td><button class="btn ghost sm" @click="scrapeOne(f)">重试</button></td>
            </tr>
          </tbody>
        </table>
      </div>
      <div class="body" v-else><div class="empty-line">没有失败的刮削任务</div></div>
    </div>

    <!-- 任务日志 -->
    <div class="collapse-sec">
      <div class="head">
        任务日志
        <span style="flex:1"></span>
        <span class="chip" :style="sseOnline ? '' : 'color:var(--danger)'">{{ sseOnline ? '● 实时' : '○ 未连接' }}</span>
      </div>
      <div class="body">
        <div class="log-box" ref="logBox" style="max-height:320px;">
          <div v-for="e in logs" :key="e.id" class="log-line" :class="e.level">
            <span class="t">{{ fmtTime(e.time) }}</span>
            <span class="lv">[{{ e.level.toUpperCase() }}]</span>
            <span>{{ e.message }}</span>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, onMounted, onBeforeUnmount, nextTick } from 'vue'
import { api } from '../../api/client'
import { fmtTime, toast, errText } from './util'

const cfg = ref({ enabled: true, realtime: false, autoRefresh: true, manual: true, overwrite: 'skip' })
const st = ref({ State: 'idle', Pending: 0, Failed: 0 })
const failed = ref([])
const logs = ref([])
const logBox = ref(null)
const sseOnline = ref(false)
let es = null
let timer = null

const stateText = computed(() => ({ idle: '空闲', running: '运行中', paused: '已暂停' }[st.value.State] || st.value.State))

async function loadAll() {
  try {
    cfg.value = await api.admin.scrapeConfig()
    st.value = await api.admin.scrapeState()
    failed.value = await api.admin.scrapeFailed()
  } catch (e) { toast(errText(e), true) }
}
async function save() {
  try {
    await api.admin.saveScrapeConfig({
      Enabled: cfg.value.enabled, Realtime: cfg.value.realtime,
      AutoRefresh: cfg.value.autoRefresh, Manual: cfg.value.manual,
      Overwrite: cfg.value.overwrite
    })
    toast('刮削设置已保存')
  } catch (e) { toast(errText(e), true) }
}
function cycleOverwrite() {
  cfg.value.overwrite = cfg.value.overwrite === 'overwrite' ? 'skip' : 'overwrite'
  save()
}
async function ctrl(action) {
  try {
    const r = await api.admin.scrapeControl(action)
    toast({ scan: '扫描已触发', start: `已开始（${r.Queued ?? 0} 个待刮削）`, pause: '已暂停', stop: '已停止' }[action] || '已执行')
    setTimeout(loadAll, 500)
  } catch (e) { toast(errText(e), true) }
}
async function retryAll() {
  const r = await api.admin.scrapeRetry()
  toast(`已重试 ${r.Queued ?? 0} 个条目`)
  setTimeout(loadAll, 600)
}
async function scrapeOne(f) {
  try {
    await api.post('/admin/scrape', { ItemID: f.ID })
    toast(`「${f.Name}」重新刮削中`)
  } catch (e) { toast(errText(e), true) }
}

function connectLogs() {
  if (es) es.close()
  es = new EventSource(`/admin/logs/stream?api_key=${encodeURIComponent(state.token)}`)
  es.onopen = () => { sseOnline.value = true }
  es.onerror = () => { sseOnline.value = false }
  es.onmessage = (ev) => {
    try {
      const e = JSON.parse(ev.data)
      logs.value.unshift(e)
      if (logs.value.length > 80) logs.value.pop()
      nextTick(() => { if (logBox.value) logBox.value.scrollTop = 0 })
    } catch {}
  }
}
import { state } from '../../api/client'

onMounted(() => {
  loadAll()
  connectLogs()
  timer = setInterval(() => { api.admin.scrapeState().then(s => st.value = s).catch(() => {}) }, 4000)
})
onBeforeUnmount(() => { clearInterval(timer); if (es) es.close() })
</script>
