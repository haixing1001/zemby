<template>
  <div class="player-page">
    <div class="player-top">
      <button class="back" @click="goBack">← 返回</button>
      <div class="title">{{ item?.Name || '' }}</div>
    </div>
    <video
      ref="videoEl"
      controls
      autoplay
      playsinline
      @loadedmetadata="onMeta"
      @timeupdate="onTime"
      @ended="onEnded"
      @pause="reportProgress"
    ></video>
    <div v-if="err" class="empty" style="color:var(--danger)">{{ err }}</div>
  </div>
</template>

<script setup>
import { ref, onMounted, onBeforeUnmount } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { api, state } from '../api/client'

const route = useRoute()
const router = useRouter()
const id = route.params.id
const videoEl = ref(null)
const item = ref(null)
const err = ref('')

let srcId = ''
let container = 'mp4'
let runTimeTicks = 0
let lastReport = 0
let started = false

function goBack() {
  reportProgress()
  if (document.fullscreenElement) document.exitFullscreen().catch(() => {})
  router.back()
}

function onMeta() {
  const v = videoEl.value
  if (!v) return
  // 恢复进度
  const pos = item.value?.UserData?.PlaybackPositionTicks || 0
  if (pos > 0 && v.duration && isFinite(v.duration)) {
    const sec = pos / 10000000
    if (sec > 5 && sec < v.duration * 0.95) v.currentTime = sec
  }
  runTimeTicks = Math.round(v.duration * 10000000)
}

function onTime() {
  const now = Date.now()
  if (now - lastReport > 10000) {
    lastReport = now
    reportProgress()
  }
}

async function reportProgress() {
  const v = videoEl.value
  if (!v || !started) return
  try {
    await api.playingProgress(id, v.currentTime * 10000000, runTimeTicks, srcId, v.paused)
  } catch {}
}

async function onEnded() {
  try {
    await api.playingStopped(id, runTimeTicks, runTimeTicks, srcId)
    if (state.userId) await api.markPlayed(state.userId, id).catch(() => {})
  } catch {}
}

onMounted(async () => {
  try {
    item.value = await api.item(id)
    const pb = await api.playbackInfo(id)
    if (!pb.MediaSources?.length) {
      err.value = '无可用媒体源'
      return
    }
    const src = pb.MediaSources[0]
    srcId = src.Id
    container = src.Container || 'mp4'
    // 字幕轨道
    const subs = (src.MediaStreams || []).filter(s => s.Type === 'Subtitle')
    const v = videoEl.value
    v.src = api.streamUrl(id, srcId, container)
    // 外挂字幕以 <track> 挂载
    let trackIdx = 0
    for (const s of subs) {
      if (!s.IsExternal) continue
      const track = document.createElement('track')
      track.kind = 'subtitles'
      track.label = s.DisplayTitle || s.Language || `字幕${trackIdx + 1}`
      track.srclang = s.Language || 'zh'
      track.src = api.subtitleUrl(id, trackIdx)
      v.appendChild(track)
      trackIdx++
    }
    api.playingStart(id, srcId).catch(() => {})
    started = true
  } catch (e) {
    err.value = e.message || '加载失败'
  }
})

onBeforeUnmount(() => {
  reportProgress()
})
</script>
