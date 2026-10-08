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

    <!-- 任务详细日志 -->
    <div class="collapse-sec">
      <div class="head" @click="showLog = !showLog">
        任务详细日志
        <span class="arrow" :class="{ open: showLog }">⌄</span>
      </div>
      <div class="body" v-if="showLog">
        <TaskLogView :lines="st.Logs" />
      </div>
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
          <thead><tr><th>名称</th><th style="width:80px;">类型</th><th>失败原因</th><th style="width:190px;">操作</th></tr></thead>
          <tbody>
            <tr v-for="f in failed" :key="f.ID">
              <td><a href="javascript:void(0)" @click="$router.push('/item/' + f.ID)">{{ f.Name }}</a><div class="muted" style="font-size:11.5px;">{{ f.Path }}</div></td>
              <td>{{ f.Type === 'Series' ? '剧集' : '电影' }}</td>
              <td class="muted" style="font-size:12.5px;">{{ f.Error }}</td>
              <td><div style="display:flex;gap:6px;flex-wrap:wrap;">
                <button class="btn ghost sm" :disabled="workingID === f.ID" @click="scrapeOne(f)">重新获取</button>
                <button class="btn ghost sm" :disabled="workingID === f.ID" @click="openManual(f)">手动关键词</button>
              </div></td>
            </tr>
          </tbody>
        </table>
      </div>
      <div class="body" v-else><div class="empty-line">没有失败的刮削任务</div></div>
    </div>

    <!-- 信息完整性诊断 -->
    <div class="collapse-sec">
      <div class="head">
        信息不全诊断（{{ incompleteItems.TotalRecordCount || 0 }}）
        <span style="flex:1"></span>
        <span v-if="incompleteItems.TotalRecordCount > incompleteItems.Items.length" class="muted" style="font-size:12px;margin-right:8px;">
          当前显示 {{ incompleteItems.Items.length }} 条
        </span>
        <button class="btn ghost sm" @click="loadIncompleteItems(true)" :disabled="diagnosticLoading">
          {{ diagnosticLoading ? '诊断中…' : '刷新诊断' }}
        </button>
      </div>
      <div class="body">
        <div v-if="!incompleteItems.TMDBConfigured || !incompleteItems.DownloadImages" class="diagnostic-note">
          TMDB 未配置 API Key 或关闭图片下载时，自动重刮削无法补齐海报和背景图。
          <router-link to="/admin/tmdb">打开 TMDB 设置 ›</router-link>
        </div>
        <div v-if="diagnosticError" class="empty-line" style="color:var(--danger);">{{ diagnosticError }}</div>
        <table v-else-if="incompleteItems.Items.length" class="tbl">
          <thead><tr><th>名称</th><th style="width:80px;">类型</th><th>缺失信息</th><th style="width:190px;">操作</th></tr></thead>
          <tbody>
            <tr v-for="f in incompleteItems.Items" :key="f.ID">
              <td>
                <a href="javascript:void(0)" @click="$router.push('/item/' + f.ID)">{{ f.Name }}</a>
                <span v-if="f.Year" class="muted">（{{ f.Year }}）</span>
                <div class="muted" style="font-size:11.5px;">{{ f.Path }}</div>
              </td>
              <td>{{ f.Type === 'Series' ? '剧集' : '电影' }}</td>
              <td class="muted" style="font-size:12.5px;">
                <span v-for="field in f.MissingFields" :key="field" class="chip issue-chip">缺少{{ field }}</span>
                <div v-if="f.Error" style="color:var(--danger);margin-top:3px;">最近失败：{{ f.Error }}</div>
              </td>
              <td><div style="display:flex;gap:6px;flex-wrap:wrap;">
                <button class="btn ghost sm" :disabled="workingID === f.ID" @click="scrapeOne(f)">重新刮削</button>
                <button class="btn ghost sm" :disabled="workingID === f.ID" @click="openManual(f)">手动关键词</button>
              </div></td>
            </tr>
          </tbody>
        </table>
        <div v-else-if="!diagnosticLoading" class="empty-line">没有发现图片、演员表或简介缺失的影片</div>
      </div>
    </div>

    <div class="modal-mask" v-if="manualItem" @click.self="closeManual">
      <div class="manual-modal">
        <div class="modal-title">手动指定 TMDB 搜索关键词</div>
        <div class="muted" style="font-size:12.5px;margin-bottom:12px;">{{ manualItem.Name }} · {{ manualItem.Type === 'Series' ? '剧集' : '电影' }}</div>
        <label>片名或外文原名</label>
        <input v-model="manualKeyword" maxlength="200" autofocus placeholder="例如：The Matrix" @keyup.enter="submitManual" />
        <label>上映 / 首播年份（可选）</label>
        <input v-model.number="manualYear" type="number" min="0" max="2100" placeholder="留空则不限制年份" />
        <div class="muted" style="font-size:12px;margin-top:8px;">系统将忽略该条目已有 TMDB ID，按关键词重新搜索；找到后会更新该条目的元数据。</div>
        <div class="modal-actions">
          <button class="btn ghost" @click="closeManual" :disabled="workingID === manualItem.ID">取消</button>
          <button class="btn" @click="submitManual" :disabled="!manualKeyword.trim() || workingID === manualItem.ID">{{ workingID === manualItem.ID ? '提交中…' : '搜索并获取元数据' }}</button>
        </div>
      </div>
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
import TaskLogView from './TaskLogView.vue'
import { api } from '../../api/client'
import { fmtTime, toast, errText } from './util'

const cfg = ref({ enabled: true, realtime: false, autoRefresh: true, manual: true, overwrite: 'skip' })
const st = ref({ State: 'idle', Pending: 0, Failed: 0 })
const showLog = ref(true)
const failed = ref([])
const incompleteItems = ref({ Items: [], TotalRecordCount: 0, DownloadImages: true, TMDBConfigured: true })
const diagnosticLoading = ref(false)
const diagnosticError = ref('')
const logs = ref([])
const logBox = ref(null)
const sseOnline = ref(false)
const manualItem = ref(null)
const manualKeyword = ref('')
const manualYear = ref(0)
const workingID = ref('')
let es = null
let timer = null

const stateText = computed(() => ({ idle: '空闲', running: '运行中', paused: '已暂停' }[st.value.State] || st.value.State))

async function loadAll() {
  loadIncompleteItems()
  try {
    const [config, status, failures] = await Promise.all([
      api.admin.scrapeConfig(), api.admin.scrapeState(), api.admin.scrapeFailed()
    ])
    cfg.value = config
    st.value = status
    failed.value = failures
  } catch (e) { toast(errText(e), true) }
}
async function loadIncompleteItems(showError = false) {
  diagnosticLoading.value = true
  diagnosticError.value = ''
  try {
    incompleteItems.value = await api.admin.scrapeIncomplete()
  } catch (e) {
    diagnosticError.value = errText(e)
    if (showError) toast(errText(e), true)
  } finally {
    diagnosticLoading.value = false
  }
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
    if (action === 'scan' || (action === 'start' && (r.Queued ?? 0) > 0)) st.value.State = 'running'
    toast({ scan: '扫描已触发', start: `已开始（${r.Queued ?? 0} 个待刮削）`, pause: '已暂停', stop: '已停止' }[action] || '已执行')
    setTimeout(loadAll, 500)
  } catch (e) { toast(errText(e), true) }
}
async function retryAll() {
  try {
    const r = await api.admin.scrapeRetry()
    if ((r.Queued ?? 0) > 0) st.value.State = 'running'
    toast(`已重试 ${r.Queued ?? 0} 个条目`)
    setTimeout(loadAll, 600)
  } catch (e) { toast(errText(e), true) }
}
async function scrapeOne(f) {
  await queueScrape(f)
}
function openManual(f) {
  manualItem.value = f
  manualKeyword.value = f.Name || ''
  manualYear.value = f.Year || 0
}
function closeManual() {
  if (manualItem.value && workingID.value === manualItem.value.ID) return
  manualItem.value = null
}
async function submitManual() {
  if (!manualItem.value || !manualKeyword.value.trim()) return
  const item = manualItem.value
  const year = Number(manualYear.value) || 0
  if (!Number.isInteger(year) || year < 0 || year > 2100) {
    toast('年份需为 0 到 2100 的整数', true)
    return
  }
  const queued = await queueScrape(item, manualKeyword.value.trim(), year)
  if (queued) closeManual()
}
async function queueScrape(f, query = '', year = 0) {
  workingID.value = f.ID
  try {
    await api.admin.scrapeItem({ ItemID: f.ID, Query: query, Year: year })
    st.value.State = 'running'
    toast(query ? `已按「${query}」加入刮削队列` : `「${f.Name}」已加入重新获取队列`)
    setTimeout(loadAll, 600)
    return true
  } catch (e) {
    toast(errText(e), true)
    return false
  } finally {
    workingID.value = ''
  }
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
  timer = setInterval(() => {
    api.admin.scrapeState().then(s => {
      const wasActive = st.value.State === 'running' || st.value.State === 'paused'
      st.value = s
      if (wasActive && s.State === 'idle') loadIncompleteItems()
    }).catch(() => {})
    api.admin.scrapeFailed().then(items => failed.value = items).catch(() => {})
  }, 4000)
})
onBeforeUnmount(() => { clearInterval(timer); if (es) es.close() })
</script>

<style scoped>
.modal-mask {
  position: fixed; inset: 0; z-index: 1000; display: flex; align-items: center; justify-content: center;
  padding: 20px; background: rgba(0, 0, 0, .65);
}
.manual-modal {
  width: min(480px, 100%); padding: 20px; border: 1px solid #2a3040; border-radius: 12px;
  background: #171a21; box-shadow: 0 18px 60px rgba(0, 0, 0, .4);
}
.manual-modal label { display: block; margin: 12px 0 6px; color: #aeb6c2; font-size: 12.5px; }
.manual-modal input { width: 100%; }
.modal-title { margin-bottom: 8px; color: #e8eaed; font-size: 16px; font-weight: 700; }
.modal-actions { display: flex; justify-content: flex-end; gap: 10px; margin-top: 18px; }
.diagnostic-note { margin-bottom: 12px; padding: 10px 12px; border: 1px solid var(--border); border-radius: 8px; color: var(--text-dim); font-size: 12.5px; }
.diagnostic-note a { margin-left: 6px; color: var(--accent); }
.issue-chip { margin: 0 5px 4px 0; }
</style>
