<template>
  <div>
    <h1 class="adm-h1">媒体排序</h1>
    <div class="adm-desc">设置各媒体库在海报墙中的默认排序方式，对所有用户生效</div>

    <div class="collapse-sec" v-for="lib in libs" :key="lib.ID">
      <div class="body" style="padding:14px 16px;display:flex;align-items:center;gap:14px;flex-wrap:wrap;">
        <div style="flex:1;min-width:180px;">
          <div class="t" style="font-weight:600;">{{ lib.Name }}</div>
          <div class="muted" style="font-size:12.5px;margin-top:2px;">{{ lib.Type === 'tvshows' ? '剧集' : '电影' }} · {{ lib.ItemCount }} 项</div>
        </div>
        <select v-model="sorts[lib.ID].key" style="width:150px;">
          <option value="SortName">名称</option>
          <option value="DateCreated">添加时间</option>
          <option value="ProductionYear">年份</option>
          <option value="CommunityRating">评分</option>
        </select>
        <select v-model="sorts[lib.ID].order" style="width:110px;">
          <option value="Ascending">升序</option>
          <option value="Descending">降序</option>
        </select>
        <button class="btn sm" @click="save(lib)">保存</button>
      </div>
    </div>
    <div v-if="!libs.length" class="empty-line">暂无媒体库</div>
  </div>
</template>

<script setup>
import { ref, reactive, onMounted } from 'vue'
import { api } from '../../api/client'
import { toast, errText } from './util'

const libs = ref([])
const sorts = reactive({})

async function load() {
  libs.value = await api.admin.libraries()
  for (const l of libs.value) {
    const [key = 'SortName', order = 'Ascending'] = (l.DefaultSort || '').split('|')
    sorts[l.ID] = { key, order }
  }
}
async function save(lib) {
  try {
    await api.admin.patchLibrary(lib.ID, { DefaultSort: sorts[lib.ID].key + '|' + sorts[lib.ID].order })
    toast(`「${lib.Name}」默认排序已更新`)
  } catch (e) { toast(errText(e), true) }
}

onMounted(load)
</script>
