<template>
  <div v-if="loading" class="spin"></div>
  <div v-else-if="error && !item.Id" class="empty">{{ error }}</div>
  <div v-else>
    <div v-if="error" class="empty" role="alert">{{ error }}</div>
    <div class="detail-hero">
      <div class="poster">
        <img v-if="!broken" :src="imageUrl(item.Id, 'Primary', 480)" @error="broken = true" />
        <div v-else class="placeholder" style="display:flex;align-items:center;justify-content:center;height:100%;font-size:56px;color:var(--text-dim);">{{ (item.Name||'?')[0] }}</div>
      </div>
      <div class="detail-info">
        <h1>{{ item.Name }}</h1>
        <div class="sub">
          <span v-if="item.ProductionYear">{{ item.ProductionYear }}</span>
          <span v-if="item.CommunityRating" class="rating-star">★ {{ item.CommunityRating.toFixed(1) }}</span>
          <span v-if="item.OfficialRating" class="chip">{{ item.OfficialRating }}</span>
          <span v-if="runtimeText">{{ runtimeText }}</span>
          <span v-if="item.Type === 'Series' && episodeCount" class="chip">共 {{ episodeCount }} 集</span>
        </div>
        <div v-if="genres.length" class="sub">
          <span v-for="g in genres" :key="g" class="chip">{{ g }}</span>
        </div>
        <div class="overview">{{ item.Overview || '暂无简介' }}</div>
        <div v-if="item.Type === 'Movie' && playbackSources.length > 1" class="version-picker">
          <label for="playback-version">播放版本</label>
          <select id="playback-version" v-model="selectedSourceId">
            <option v-for="(source, index) in playbackSources" :key="source.Id" :value="String(source.Id)">
              {{ mediaSourceLabel(source, index) }}{{ index === 0 ? ' · 默认' : '' }}
            </option>
          </select>
          <span class="muted">共 {{ playbackSources.length }} 个版本</span>
        </div>
        <div class="toolbar mt">
          <button v-if="item.Type === 'Movie'" class="btn green" @click="play()">▶ 播放</button>
          <button v-if="resumePct > 0 && item.Type === 'Movie'" class="btn ghost" @click="play(true)">
            从 {{ resumeText }} 继续播放
          </button>
          <button class="btn ghost" @click="toggleFav">{{ item.UserData?.IsFavorite ? '★ 已收藏' : '☆ 收藏' }}</button>
          <button class="btn ghost" @click="markSeen">{{ item.UserData?.Played ? '已观看 ✓' : '标记已看' }}</button>
        </div>
      </div>
    </div>

    <!-- 演职人员 -->
    <div v-if="people.length" class="section">
      <h2>演职人员</h2>
      <div class="cast-row">
        <div v-for="p in people" :key="p.Id" class="cast-card">
          <div class="cast-avatar">
            <img v-if="p.Thumb" :src="p.Thumb" loading="lazy" @error="onCastImgError" />
            <span v-else>{{ (p.Name || '?')[0] }}</span>
          </div>
          <div class="cast-name">{{ p.Name }}</div>
          <div class="cast-role">{{ p.Role || (p.Type === 'Director' ? '导演' : '') }}</div>
        </div>
      </div>
    </div>

    <!-- 相关影片 -->
    <div v-if="relatedItems.length" class="section">
      <h2>相关影片</h2>
      <div class="grid">
        <div v-for="related in relatedItems" :key="related.Id" class="poster-card" @click="openRelated(related)">
          <div class="poster">
            <img v-if="!relatedImageBroken(related)" :src="imageUrl(related.Id, 'Primary', 300)" loading="lazy" @error="breakRelatedImage(related)" />
            <div v-else class="placeholder">{{ (related.Name || '?')[0] }}</div>
            <span v-if="related.CommunityRating" class="badge">★ {{ related.CommunityRating.toFixed(1) }}</span>
          </div>
          <div class="title">{{ related.Name }}</div>
          <div class="meta">{{ related.ProductionYear || '' }}{{ related.Type === 'Series' ? ' · 剧集' : '' }}</div>
        </div>
      </div>
    </div>

    <!-- 剧集：季与集 -->
    <div v-if="item.Type === 'Series'" class="section">
      <div class="season-tabs">
        <div v-for="s in seasons" :key="s.Id" class="tab" :class="{ active: s.Id === currentSeason }" @click="selectSeason(s.Id)">
          {{ s.Name }}
        </div>
      </div>
      <div v-for="ep in episodes" :key="ep.Id" class="episode-row" @click="playEpisode(ep)">
        <div class="ep-num">{{ ep.IndexNumber }}</div>
        <div class="ep-thumb">
          <img v-if="ep.ImageTags?.Thumb" :src="imageUrl(ep.Id, 'Thumb', 320)" />
          <span v-else>E{{ ep.IndexNumber }}</span>
        </div>
        <div style="flex:1;min-width:0;">
          <div class="ep-title">{{ ep.Name }}</div>
          <div class="ep-overview">{{ ep.Overview || '暂无简介' }}</div>
        </div>
        <div class="muted" style="font-size:12px;">{{ fmtRuntime(ep.RunTimeTicks) }}</div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { api, state, imageUrl } from '../api/client'

const route = useRoute()
const router = useRouter()
const id = computed(() => String(route.params.id || ''))
const internalId = computed(() => String(item.value.Id || ''))
const loading = ref(true)
const error = ref('')
const item = ref({})
const broken = ref(false)
const selectedSourceId = ref('')
const relatedItems = ref([])
const brokenRelatedImages = ref(new Set())
const seasons = ref([])
const episodes = ref([])
const currentSeason = ref('')
const episodeCount = ref(0)
let requestSeq = 0
let seasonRequestSeq = 0

const genres = computed(() => item.value.Genres || [])
const people = computed(() => item.value.People || [])
const playbackSources = computed(() => item.value.MediaSources || [])
function onCastImgError(ev) { ev.target.style.display = 'none' }
function relatedImageBroken(related) { return brokenRelatedImages.value.has(related.Id) }
function breakRelatedImage(related) {
  const next = new Set(brokenRelatedImages.value)
  next.add(related.Id)
  brokenRelatedImages.value = next
}
function openRelated(related) { router.push('/item/' + (related.RouteId || related.Id)) }
function mediaSourceLabel(source, index) {
  const streams = source.MediaStreams || []
  const video = streams.find(stream => stream.Type === 'Video')
  const audio = streams.find(stream => stream.Type === 'Audio')
  const labels = []
  const height = Number(video?.Height) || 0
  if (height >= 2000) labels.push('4K')
  else if (height > 0) labels.push(`${height}p`)
  const range = String(video?.VideoRange || '').trim()
  if (range && range.toLowerCase() !== 'sdr') labels.push(range.toUpperCase())
  if (video?.Codec) labels.push(String(video.Codec).toUpperCase())
  if (audio) {
    const audioLabel = audio.DisplayTitle || [audio.Language, audio.Codec, audio.Channels ? `${audio.Channels}ch` : ''].filter(Boolean).join(' ')
    if (audioLabel) labels.push(audioLabel)
  }
  if (source.Size > 0) labels.push(`${(source.Size / (1024 ** 3)).toFixed(1)} GB`)
  if (source.Name && source.Name !== item.value.Name) labels.push(source.Name)
  return labels.join(' · ') || source.Name || `版本 ${index + 1}`
}
const runtimeText = computed(() => fmtRuntime(item.value.RunTimeTicks))
const resumePct = computed(() => {
  const rt = item.value.RunTimeTicks || 0
  const pos = item.value.UserData?.PlaybackPositionTicks || 0
  return rt > 0 ? Math.round((pos / rt) * 100) : 0
})
const resumeText = computed(() => {
  const ticks = item.value.UserData?.PlaybackPositionTicks || 0
  const sec = Math.floor(ticks / 10000000)
  const m = Math.floor(sec / 60)
  return m >= 60 ? `${Math.floor(m / 60)}小时${m % 60}分` : `${m}分钟`
})

function fmtRuntime(ticks) {
  if (!ticks) return ''
  const min = Math.round(ticks / 600000000)
  if (min <= 0) return ''
  if (min < 60) return `${min} 分钟`
  return `${Math.floor(min / 60)} 小时 ${min % 60} 分`
}

function play() {
  router.push({ path: '/play/' + internalId.value, query: { source: selectedSourceId.value || undefined } })
}
async function playEpisode(ep) {
  router.push('/play/' + ep.Id)
}
async function toggleFav() {
  const itemId = internalId.value
  if (item.value.UserData?.IsFavorite) {
    await api.del(`/Users/${state.userId}/FavoriteItems/${itemId}`)
    item.value.UserData.IsFavorite = false
  } else {
    await api.markFavorite(state.userId, itemId)
    item.value.UserData.IsFavorite = true
  }
}
async function markSeen() {
  if (item.value.UserData?.Played) return
  await api.markPlayed(state.userId, internalId.value)
  item.value.UserData.Played = true
}

async function selectSeason(sid) {
  const itemId = internalId.value
  const request = requestSeq
  const seasonRequest = ++seasonRequestSeq
  currentSeason.value = sid
  episodes.value = []
  try {
    const d = await api.episodes(itemId, sid)
    if (request === requestSeq && seasonRequest === seasonRequestSeq && itemId === internalId.value) {
      episodes.value = d.Items || []
    }
  } catch (e) {
    if (request === requestSeq && seasonRequest === seasonRequestSeq) error.value = e.message || '剧集加载失败'
  }
}

async function loadItem(itemId) {
  const request = ++requestSeq
  seasonRequestSeq++
  loading.value = true
  error.value = ''
  item.value = {}
  broken.value = false
  selectedSourceId.value = ''
  relatedItems.value = []
  brokenRelatedImages.value = new Set()
  seasons.value = []
  episodes.value = []
  currentSeason.value = ''
  episodeCount.value = 0
  try {
    const loadedItem = await api.item(itemId)
    if (request !== requestSeq) return
    item.value = loadedItem || {}
    const internalItemId = String(item.value.Id || itemId)
    selectedSourceId.value = String(item.value.MediaSources?.[0]?.Id || '')
    if (item.value.Type === 'Movie' || item.value.Type === 'Series') {
      api.similarItems(internalItemId, 8).then(result => {
        if (request === requestSeq) relatedItems.value = result.Items || []
      }).catch(() => {})
    }
    if (item.value.Type === 'Series') {
      const s = await api.seasons(internalItemId)
      if (request !== requestSeq) return
      seasons.value = s.Items || []
      if (seasons.value.length) {
        currentSeason.value = seasons.value[0].Id
        const requests = seasons.value.map(sn => api.episodes(internalItemId, sn.Id).then(
          value => ({ ok: true, value }),
          reason => ({ ok: false, reason })
        ))
        const first = await requests[0]
        if (request !== requestSeq) return
        if (first.ok) {
          episodes.value = first.value.Items || []
          episodeCount.value = episodes.value.length
        } else {
          error.value = first.reason.message || '首季剧集加载失败'
        }
        loading.value = false

        const rest = await Promise.all(requests.slice(1))
        if (request !== requestSeq) return
        for (const result of rest) {
          if (result.ok) episodeCount.value += (result.value.Items || []).length
          else error.value = '部分剧集加载失败，请切换季后重试'
        }
      }
    }
  } catch (e) {
    if (request === requestSeq) error.value = e.message || '条目加载失败'
  } finally {
    if (request === requestSeq) loading.value = false
  }
}

watch(() => route.params.id, value => loadItem(String(value || '')), { immediate: true })
</script>

<style scoped>
.version-picker { display: flex; align-items: center; flex-wrap: wrap; gap: 10px; margin-top: 14px; }
.version-picker label { width: auto; margin: 0; color: var(--text-dim); font-size: 13px; }
.version-picker select { width: min(100%, 480px); min-width: 220px; }
@media (max-width: 760px) {
  .version-picker { align-items: flex-start; flex-direction: column; gap: 6px; }
  .version-picker select { width: 100%; min-width: 0; }
}
</style>
