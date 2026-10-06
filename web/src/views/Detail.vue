<template>
  <div v-if="loading" class="spin"></div>
  <div v-else>
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
import { ref, computed, onMounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { api, state, imageUrl } from '../api/client'

const route = useRoute()
const router = useRouter()
const id = route.params.id
const loading = ref(true)
const item = ref({})
const broken = ref(false)
const seasons = ref([])
const episodes = ref([])
const currentSeason = ref('')
const episodeCount = ref(0)

const genres = computed(() => item.value.Genres || [])
const people = computed(() => item.value.People || [])
function onCastImgError(ev) { ev.target.style.display = 'none' }
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

async function play() {
  router.push('/play/' + id)
}
async function playEpisode(ep) {
  router.push('/play/' + ep.Id)
}
async function toggleFav() {
  if (item.value.UserData?.IsFavorite) {
    await api.del(`/Users/${state.userId}/FavoriteItems/${id}`)
    item.value.UserData.IsFavorite = false
  } else {
    await api.markFavorite(state.userId, id)
    item.value.UserData.IsFavorite = true
  }
}
async function markSeen() {
  if (item.value.UserData?.Played) return
  await api.markPlayed(state.userId, id)
  item.value.UserData.Played = true
}

async function selectSeason(sid) {
  currentSeason.value = sid
  const d = await api.episodes(id, sid)
  episodes.value = d.Items || []
}

onMounted(async () => {
  try {
    item.value = await api.item(id)
    if (item.value.Type === 'Series') {
      const s = await api.seasons(id)
      seasons.value = s.Items || []
      // 统计总集数
      let cnt = 0
      for (const sn of seasons.value) {
        const eps = await api.episodes(id, sn.Id)
        cnt += (eps.Items || []).length
      }
      episodeCount.value = cnt
      if (seasons.value.length) await selectSeason(seasons.value[0].Id)
    }
  } finally {
    loading.value = false
  }
})
</script>
