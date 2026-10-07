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
    <div v-if="needTap" class="tap-play" @click="tapPlay" title="点击开始播放">▶ 点击播放</div>
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
// 浏览器自动播放策略阻止时（直接打开/刷新播放页无用户手势），显示手动播放覆盖层
const needTap = ref(false)

function tryAutoplay() {
  const v = videoEl.value
  if (!v) return
  const p = v.play()
  if (p && p.catch) {
    p.catch((e) => {
      if (e && e.name === 'NotAllowedError') needTap.value = true
    })
  }
}

function tapPlay() {
  const v = videoEl.value
  if (!v) return
  const p = v.play()
  if (p && p.then) {
    // 播放成功才收起覆盖层；再次被策略拒绝则保持显示，避免用户被卡在暂停态
    p.then(() => { needTap.value = false }).catch(() => { needTap.value = true })
  } else {
    needTap.value = false
  }
}

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
  // 自动播放（被浏览器策略拒绝时展示“点击播放”覆盖层）
  tryAutoplay()
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
