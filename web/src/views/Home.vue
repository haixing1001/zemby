<template>
  <div v-if="loading" class="spin"></div>
  <div v-else>
    <div v-if="resumeItems.length" class="section">
      <h2>继续观看</h2>
      <div class="grid scroll">
        <div v-for="it in resumeItems" :key="it.Id" class="poster-card" @click="play(it)">
          <div class="poster">
            <img v-if="imgOk(it)" :src="posterUrl(it, 300)" @error="fail(it)" loading="lazy" />
            <div v-else class="placeholder">{{ initial(it) }}</div>
            <div class="progress-bar"><div :style="{ width: progressPct(it) + '%' }"></div></div>
          </div>
          <div class="title">{{ it.Name }}</div>
          <div class="meta">{{ it.Type === 'Episode' ? it.SeriesName : it.ProductionYear }}</div>
        </div>
      </div>
    </div>

    <div v-if="latestItems.length" class="section">
      <h2>最近添加</h2>
      <div class="grid scroll">
        <div v-for="it in latestItems" :key="it.Id" class="poster-card" @click="open(it)">
          <div class="poster">
            <img v-if="imgOk(it)" :src="posterUrl(it, 300)" @error="fail(it)" loading="lazy" />
            <div v-else class="placeholder">{{ initial(it) }}</div>
          </div>
          <div class="title">{{ it.Name }}</div>
          <div class="meta">{{ it.ProductionYear || '' }}</div>
        </div>
      </div>
    </div>

    <div class="section">
      <h2>我的媒体库</h2>
      <div v-if="!views.length" class="empty">
        <p>还没有媒体库</p>
        <router-link v-if="state.isAdmin" to="/admin" class="btn">去后台添加</router-link>
      </div>
      <div class="grid">
        <div v-for="v in views" :key="v.Id" class="poster-card" @click="router.push('/library/' + v.Id)">
          <div class="poster" style="aspect-ratio: 16/9;">
            <img v-if="viewImgOk(v)" :src="imageUrl(v.Id, 'Primary', 400)" @error="viewFail(v)" loading="lazy" />
            <div v-else class="placeholder" style="font-size: 22px;">{{ v.Name }}</div>
          </div>
          <div class="title">{{ v.Name }}</div>
          <div class="meta">{{ viewKind(v) }}</div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { api, state, imageUrl } from '../api/client'

const router = useRouter()
const loading = ref(true)
const views = ref([])
const resumeItems = ref([])
const latestItems = ref([])
const broken = ref(new Set())
const viewBroken = ref(new Set())

function posterUrl(it, w) { return imageUrl(it.Id, it.ImageTags?.Primary ? 'Primary' : 'Primary', w) }
function imgOk(it) { return !broken.value.has(it.Id) }
function fail(it) { broken.value.add(it.Id) }
function viewImgOk(v) { return !viewBroken.value.has(v.Id) }
function viewFail(v) { viewBroken.value.add(v.Id) }
function initial(it) { return (it.Name || '?')[0] }
function progressPct(it) {
  const rt = it.RunTimeTicks || 0
  const pos = it.UserData?.PlaybackPositionTicks || 0
  return rt > 0 ? Math.min(100, Math.round((pos / rt) * 100)) : 0
}
function open(it) {
  router.push('/item/' + (it.RouteId || it.Id))
}
function play(it) {
  router.push('/play/' + it.Id)
}
function viewKind(v) {
  if (v.CollectionType === 'favorites') return '收藏'
  if (v.CollectionType === 'tvshows') return '剧集'
  if (v.CollectionType === 'movies') return '电影'
  return '媒体库'
}

onMounted(async () => {
  try {
    const [v, r, l] = await Promise.all([
      api.views(state.userId),
      api.resume().catch(() => ({ Items: [] })),
      api.latest().catch(() => [])
    ])
    views.value = v.Items || []
    resumeItems.value = r.Items || []
    latestItems.value = Array.isArray(l) ? l : (l.Items || [])
  } catch (e) {
    console.error(e)
  } finally {
    loading.value = false
  }
})
</script>
