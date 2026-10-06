<template>
  <div>
    <h1 class="adm-h1">字幕</h1>
    <div class="adm-desc">媒体库中的外挂字幕文件（增强功能）</div>

    <div class="collapse-sec">
      <div class="body" style="padding:0;">
        <div v-if="!items.length" class="empty-line" style="margin:14px;">暂无外挂字幕</div>
        <table v-else class="tbl" style="margin:0;">
          <thead><tr><th>所属条目</th><th>语言</th><th>标题</th><th>文件</th><th>属性</th></tr></thead>
          <tbody>
            <tr v-for="(s, i) in items" :key="i">
              <td><a href="javascript:void(0)" @click="$router.push('/item/' + s.ItemID)">{{ s.ItemName || s.ItemID }}</a></td>
              <td>{{ s.Language || '-' }}</td>
              <td class="muted">{{ s.DisplayTitle || s.Title || '-' }}</td>
              <td class="muted" style="font-size:12px;">{{ s.Path }}</td>
              <td>
                <span class="tag" v-if="s.IsDefault">默认</span>
                <span class="tag gray" v-if="s.IsForced" style="margin-left:4px;">强制</span>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>
    <div class="adm-desc" v-if="items.length">共 {{ items.length }} 条 · 外挂字幕在扫描时自动匹配同名视频</div>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import { api, state } from '../../api/client'
import { errText, toast } from './util'

const items = ref([])
onMounted(async () => {
  try {
    const d = await api.admin.subtitles()
    items.value = d.Items || []
  } catch (e) { toast(errText(e), true) }
})
</script>
