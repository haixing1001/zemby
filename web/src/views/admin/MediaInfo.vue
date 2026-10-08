<template>
  <div>
    <h1 class="adm-h1">提取媒体信息设置</h1>
    <div class="adm-desc">使用 ffprobe 提取视频、音频与字幕轨道信息</div>

    <div class="collapse-sec">
      <div class="body" style="padding:2px 16px 12px;">
        <div class="set-row">
          <div><div class="t">浏览时提取</div><div class="d">浏览详情立即返回已有信息，后台补全视频、音频和字幕信息</div></div>
          <label class="switch"><input type="checkbox" v-model="cfg.onBrowse" @change="save" /><span class="track"></span><span class="knob"></span></label>
        </div>
        <div class="set-row">
          <div><div class="t">预加载下一集</div><div class="d">播放开始异步预取下一集媒体信息，播放本身不等待</div></div>
          <label class="switch"><input type="checkbox" v-model="cfg.preloadNext" @change="save" /><span class="track"></span><span class="knob"></span></label>
        </div>
        <div class="set-row">
          <div><div class="t">持久化媒体信息</div><div class="d">媒体移除后仍保留信息，重新入库可直接复用</div></div>
          <label class="switch"><input type="checkbox" v-model="cfg.persist" @change="save" /><span class="track"></span><span class="knob"></span></label>
        </div>
        <div class="set-row" style="flex-direction:column;align-items:stretch;">
          <div><div class="t">保存目录</div><div class="d">用于媒体信息、TMDB 元数据与图片；TMDB 内容按 TMDB ID 建目录，已有文件不自动迁移</div></div>
          <div style="display:flex;gap:10px;margin-top:10px;">
            <input v-model="cfg.saveDir" style="flex:1;" />
            <button class="btn sm" @click="save">✓ 保存</button>
          </div>
        </div>
      </div>
    </div>

    <!-- 批量提取 -->
    <div class="set-row" style="border:none;">
      <div><div class="t">批量提取媒体信息</div><div class="d">为缺少轨道信息的本地媒体批量运行提取</div></div>
      <div style="display:flex;gap:8px;">
        <button class="btn ghost sm" title="开始" @click="batch('start')">▶</button>
        <button class="btn ghost sm" title="停止" @click="batch('stop')">⏹</button>
      </div>
    </div>
    <div class="empty-line" style="text-align:left;">
      批量提取：{{ stateText }} · 完成 {{ ps.Completed ?? 0 }} · 跳过 {{ ps.Skipped ?? 0 }} · 失败 {{ ps.Failed ?? 0 }} ·
      等待 {{ ps.Waiting ?? 0 }} · 正在提取 {{ ps.Running ?? 0 }} · 并发 {{ ps.Concurrency ?? cfg.concurrency }}
    </div>

    <!-- 实时监控入库 -->
    <div class="set-row">
      <div><div class="t">实时监控入库</div><div class="d">新文件入库后自动补全媒体信息（由「刮削管理 - 实时监控」统一控制）</div></div>
      <router-link to="/admin/scrape" class="right">前往设置 ›</router-link>
    </div>

    <!-- 并发 -->
    <div class="set-row" style="border:none;">
      <div><div class="t">媒体信息提取并发</div><div class="d">等待提取数量：{{ ps.Waiting ?? 0 }} · 并发上限：{{ ps.Concurrency ?? cfg.concurrency }}</div></div>
      <div style="display:flex;gap:10px;align-items:center;">
        <input v-model.number="cfg.concurrency" type="number" min="1" max="8" style="width:90px;" />
        <button class="btn" @click="save">保存</button>
      </div>
    </div>

    <!-- 提取详细日志 -->
    <div class="collapse-sec">
      <div class="head" @click="showLog = !showLog">
        提取详细日志
        <span class="arrow" :class="{ open: showLog }">⌄</span>
      </div>
      <div class="body" v-if="showLog">
        <TaskLogView :lines="ps.Logs" />
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, onMounted, onBeforeUnmount } from 'vue'
import { api } from '../../api/client'
import { toast, errText } from './util'
import TaskLogView from './TaskLogView.vue'

const cfg = ref({ onBrowse: true, preloadNext: true, persist: false, saveDir: '', concurrency: 2 })
const ps = ref({})
const showLog = ref(true)
let timer = null

const stateText = computed(() =>
  ({ idle: '未运行', running: '运行中', extracting: '提取中', stopped: '已停止' }[ps.value.State] || '未运行'))

async function load() {
  try { cfg.value = await api.admin.probeConfig() } catch {}
}
async function loadStatus() {
  try { ps.value = await api.admin.probeStatus() } catch {}
}
async function save() {
  try {
    await api.admin.saveProbeConfig({
      OnBrowse: cfg.value.onBrowse, PreloadNext: cfg.value.preloadNext,
      Persist: cfg.value.persist, SaveDir: cfg.value.saveDir,
      Concurrency: Number(cfg.value.concurrency) || 2
    })
    toast('提取设置已保存')
    loadStatus()
  } catch (e) { toast(errText(e), true) }
}
async function batch(action) {
  try {
    const r = await api.admin.probeBatch(action)
    toast(action === 'start' ? `批量提取已开始（${r.Queued ?? 0} 个待处理）` : '批量提取已停止')
    loadStatus()
  } catch (e) { toast(errText(e), true) }
}

onMounted(() => { load(); loadStatus(); timer = setInterval(loadStatus, 2000) })
onBeforeUnmount(() => clearInterval(timer))
</script>
