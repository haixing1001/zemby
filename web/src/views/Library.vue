<template>
  <div>
    <div class="toolbar">
      <h2 style="margin:0;">{{ libName }}</h2>
      <div class="spacer"></div>
      <input v-model="search" placeholder="搜索…" style="width: 200px;" @input="debouncedLoad" />
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

    <div v-if="loading" class="spin"></div>
    <div v-else-if="!items.length" class="empty">没有找到条目</div>
    <div v-else class="grid">
      <div v-for="it in items" :key="it.Id" class="poster-card" @click="router.push('/item/' + it.Id)">
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
import { ref, onMounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { api, imageUrl, state } from '../api/client'

const route = useRoute()
const router = useRouter()
const libId = route.params.id
const libName = ref('媒体库')
const items = ref([])
const total = ref(0)
const loading = ref(true)
const search = ref('')
const sortBy = ref('SortName')
const sortOrder = ref('Ascending')
const broken = ref(new Set())
const startIndex = ref(0)
const libDefaultSort = ref(false)

function posterUrl(it, w) { return imageUrl(it.Id, 'Primary', w) }
function imgOk(it) { return !broken.value.has(it.Id) }
function fail(it) { broken.value.add(it.Id) }
function initial(it) { return (it.Name || '?')[0] }
const defaultApplied = ref(false)

async function load(append = false) {
  loading.value = true
  try {
    // 首次加载时应用媒体库默认排序
    if (!append && !defaultApplied.value) {
      defaultApplied.value = true
      try {
        const v = await api.get(`/Users/${state.userId}/Views`)
        const lib = (v.Items || []).find(x => x.Id === libId)
        if (lib && lib.DefaultSort) {
          const [k, o] = String(lib.DefaultSort).split('|')
          if (k) { sortBy.value = k; libDefaultSort.value = true }
          if (o) sortOrder.value = o
        }
      } catch {}
      // 增强功能：按发行日期排序媒体库（无库级默认排序时生效，从新到旧）
      try {
        const e = await api.admin.enhancements()
        if (e.SortByReleaseDate && !libDefaultSort.value) {
          sortBy.value = 'PremiereDate'
          sortOrder.value = 'Descending'
        }
      } catch {}
    }
    const params = {
      ParentId: libId,
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
    libName.value = d.Items?.length ? libName.value : libName.value
    items.value = append ? [...items.value, ...(d.Items || [])] : (d.Items || [])
    total.value = d.TotalRecordCount || 0
  } finally {
    loading.value = false
  }
}
function debouncedLoad() {
  clearTimeout(debouncedLoad._t)
  debouncedLoad._t = setTimeout(() => { startIndex.value = 0; load() }, 350)
}
function more() {
  startIndex.value += 60
  load(true)
}

onMounted(async () => {
  // 库名
  try {
    const d = await api.get(`/Users/${state.userId}/Views`)
    const lib = (d.Items || []).find(x => x.Id === libId)
    if (lib) libName.value = lib.Name
  } catch {}
  load()
})
</script>
