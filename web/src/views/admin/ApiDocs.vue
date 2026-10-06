<template>
  <div>
    <h1 class="adm-h1">API</h1>
    <div class="adm-desc">Emby 兼容接口一览 · 客户端以 api_key 或 X-Emby-Token 头携带令牌</div>

    <div class="collapse-sec" v-for="g in groups" :key="g.name">
      <div class="head">{{ g.name }}<span style="flex:1"></span><span class="tag gray">{{ g.items.length }}</span></div>
      <div class="body" v-if="open[g.name] !== false" style="padding:0;">
        <table class="tbl" style="margin:0;">
          <thead><tr><th style="width:80px;">方法</th><th>路径</th><th>说明</th></tr></thead>
          <tbody>
            <tr v-for="(e, i) in g.items" :key="i">
              <td><span class="tag" :class="{ gray: e.method !== 'GET' }">{{ e.method }}</span></td>
              <td><code style="font-size:12.5px;">{{ e.path }}</code></td>
              <td class="muted">{{ e.desc }}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>

    <div class="collapse-sec">
      <div class="head">调用示例</div>
      <div class="body">
        <div class="log-box" style="max-height:none;">
<div class="log-line"><span>curl -H "X-Emby-Token: &lt;token&gt;" http://服务器:8097/emby/Items?Recursive=true&amp;Limit=20</span></div>
<div class="log-line"><span>curl -X POST -H "Content-Type: application/json" -d '{"Username":"admin","Pw":"***"}' http://服务器:8097/emby/Users/AuthenticateByName</span></div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, reactive, computed, onMounted } from 'vue'
import { api } from '../../api/client'
import { errText, toast } from './util'

const docs = ref({ Endpoints: [] })
const open = reactive({})

const groups = computed(() => {
  const m = {}
  for (const e of docs.value.Endpoints || []) {
    (m[e.group] = m[e.group] || []).push(e)
  }
  return Object.entries(m).map(([name, items]) => ({ name, items }))
})

onMounted(async () => {
  try { docs.value = await api.admin.apiDocs() } catch (e) { toast(errText(e), true) }
})
</script>
