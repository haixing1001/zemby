<template>
  <div>
    <div class="toolbar">
      <h2 style="margin:0;">{{ libName }}</h2>
      <div class="spacer"></div>
      <input v-model="search" placeholder="搜索…" class="search-input" @input="debouncedLoad" />
      <select v-model="sortBy" @change="load">
        <option value="SortName">名称</option>
        <option value="PremiereDate">发行日期</option>
        <option value="DateCreated">添加时间</option>
        <option value="ProductionYear">年份</option>
        <option value="CommunityRating">评分</option>
      </select>
      <select v-model="sortOrder" @change="load">
        <option value="Ascending">升序</option>
        <option value="Descending">降序</option>
      </select>
    </div>

    <div v-if="error" class="empty" role="alert">{{ error }}</div>
    <div v-if="loading" class="spin"></div>
    <div v-else-if="!items.length && !error" class="empty">没有找到条目</div>
    <div v-else-if="items.length" class="grid">
      <div v-for="it in items" :key="it.Id" class="poster-card" @click="router.push('/item/' + itemRouteId(it))">
        <div class="poster">
          <img v-if="imgOk(it)" :src="posterUrl(it, 300)" @error="fail(it)" loading="lazy" />
          <div v-else class="placeholder">{{ initial(it) }}</div>
          <span v-if="it.CommunityRating" class="badge">★ {{ it.CommunityRating.toFixed(1) }}</span>
          <span v-if="it.Type === 'Series' && it.RecursiveItemCount" class="badge-eps">{{ it.RecursiveItemCount }} 集</span>
        </div>
        <div class="title">{{ it.Name }}</div>
        <div class="meta">{{ it.ProductionYear || '' }}{{ it.Type === 'Series' ? ' · 剧集' : '' }}</div>
      </div>
    </div>

    <div class="toolbar mt" v-if="total > items.length">
      <div class="spacer"></div>
      <button class="btn ghost sm" :disabled="loading" @click="more">加载更多（{{ items.length }}/{{ total }}）</button>
      <div class="spacer"></div>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, watch, onBeforeUnmount } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { api, imageUrl, state } from '../api/client'

const route = useRoute()
const router = useRouter()
const libId = computed(() => String(route.params.id || ''))
const libName = ref('媒体库')
const items = ref([])
const total = ref(0)
const loading = ref(true)
const error = ref('')
const search = ref('')
const sortBy = ref('SortName')
const sortOrder = ref('Ascending')
const broken = ref(new Set())
const startIndex = ref(0)
const libDefaultSort = ref(false)
let loadSeq = 0
let settingsLibId = ''
let settingsPromise = null
let debounceTimer = null

function posterUrl(it, w) { return imageUrl(it.Id, 'Primary', w) }
function imgOk(it) { return !broken.value.has(it.Id) }
function fail(it) { broken.value.add(it.Id) }
function initial(it) { return (it.Name || '?')[0] }
function itemRouteId(it) { return it.RouteId || it.Id }

async function applyListSettings(targetLibId) {
  if (settingsPromise && settingsLibId === targetLibId) return settingsPromise
  settingsLibId = targetLibId
  settingsPromise = (async () => {
    const [viewsResult, enhancementsResult] = await Promise.allSettled([
      api.views(state.userId), api.admin.enhancements()
    ])
    if (targetLibId !== libId.value) return

    const views = viewsResult.status === 'fulfilled' ? viewsResult.value : { Items: [] }
    const lib = (views.Items || []).find(x => x.Id === targetLibId)
    if (lib) {
      libName.value = lib.Name
      if (lib.DefaultSort) {
        const [key, order] = String(lib.DefaultSort).split('|')
        if (key) { sortBy.value = key; libDefaultSort.value = true }
        if (order) sortOrder.value = order
      }
    }
    const enhancements = enhancementsResult.status === 'fulfilled' ? enhancementsResult.value : {}
    if (enhancements.SortByReleaseDate && !libDefaultSort.value) {
      sortBy.value = 'PremiereDate'
      sortOrder.value = 'Descending'
    }
  })()
  return settingsPromise
}

async function load(append = false) {
  const request = ++loadSeq
  const targetLibId = libId.value
  if (!append) {
    startIndex.value = 0
    error.value = ''
  }
  loading.value = true
  try {
    await applyListSettings(targetLibId)
    if (request !== loadSeq || targetLibId !== libId.value) return
    const params = {
      ParentId: targetLibId,
      Recursive: 'true',
      IncludeItemTypes: 'Movie,Series',
      SortBy: sortBy.value,
      SortOrder: sortOrder.value,
      Limit: 60,
      StartIndex: startIndex.value,
      Fields: 'Overview,ProviderIds'
    }
    if (search.value) params.SearchTerm = search.value
    const d = await api.items(params)
    if (request !== loadSeq || targetLibId !== libId.value) return
    items.value = append ? [...items.value, ...(d.Items || [])] : (d.Items || [])
    total.value = d.TotalRecordCount || 0
  } catch (e) {
    if (request === loadSeq && targetLibId === libId.value) {
      error.value = e.message || '媒体库加载失败'
      if (!append) items.value = []
    }
  } finally {
    if (request === loadSeq) loading.value = false
  }
}
function debouncedLoad() {
  clearTimeout(debounceTimer)
  debounceTimer = setTimeout(() => load(), 350)
}
function more() {
  if (loading.value) return
  startIndex.value = items.value.length
  load(true)
}

watch(() => route.params.id, () => {
  clearTimeout(debounceTimer)
  loadSeq++
  settingsLibId = ''
  settingsPromise = null
  libDefaultSort.value = false
  sortBy.value = 'SortName'
  sortOrder.value = 'Ascending'
  libName.value = '媒体库'
  items.value = []
  total.value = 0
  search.value = ''
  startIndex.value = 0
  broken.value = new Set()
  load()
}, { immediate: true })

onBeforeUnmount(() => {
  clearTimeout(debounceTimer)
  loadSeq++
})
</script>
