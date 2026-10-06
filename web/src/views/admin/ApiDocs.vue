<template>
  <div>
    <div class="api-head">
      <h1 class="adm-h1">API管理</h1>
      <button class="add-btn" title="新建 API Key" @click="startCreate">＋</button>
    </div>
    <div class="adm-desc">签发兼容 Emby 的 API Key · 第三方工具以 api_key 参数或 X-Emby-Token 头携带调用，等同管理员权限</div>

    <div class="key-list">
      <div class="key-row" v-for="k in keys" :key="k.ID">
        <div class="key-info">
          <div class="key-name">{{ k.Name }}</div>
          <div class="key-mask">{{ k.Masked }}</div>
        </div>
        <div class="key-side">
          <div class="key-meta">
            <div class="key-time">{{ k.DateCreated }}</div>
            <div class="key-last" v-if="k.LastSeen">最近使用 {{ k.LastSeen }}</div>
          </div>
          <button class="key-del" @click="remove(k)">删除</button>
        </div>
      </div>
      <div v-if="!keys.length && !loading" class="key-empty">暂无 API Key，点击上方 ＋ 新建一个</div>
    </div>

    <!-- 新建 / 展示密钥弹窗 -->
    <div class="modal-mask" v-if="dialog" @click.self="closeDialog">
      <div class="modal">
        <template v-if="!newKey">
          <div class="modal-title">新建 API Key</div>
          <input v-model.trim="name" placeholder="名称，如 MoviePilot、Infuse" maxlength="100" @keyup.enter="create" />
          <div class="modal-actions">
            <button class="btn ghost sm" @click="closeDialog">取消</button>
            <button class="btn sm" :disabled="busy" @click="create">{{ busy ? '生成中…' : '生成' }}</button>
          </div>
        </template>
        <template v-else>
          <div class="modal-title">密钥已生成</div>
          <div class="key-reveal" @click="copy">{{ newKey.Key }}</div>
          <div class="key-warn">请立即复制保存。关闭后将仅显示掩码，无法再次查看完整密钥；泄露时可在此删除重建。</div>
          <div class="modal-actions">
            <button class="btn ghost sm" @click="copy">复制</button>
            <button class="btn sm" @click="closeDialog">完成</button>
          </div>
        </template>
      </div>
    </div>

    <!-- 接口文档（保留为折叠参考） -->
    <div class="collapse-sec" style="margin-top:18px;">
      <div class="head" @click="docsOpen = !docsOpen">接口文档<span style="flex:1"></span><span class="arrow" :class="{ open: docsOpen }">⌄</span></div>
      <div class="body" v-if="docsOpen" style="padding:0;">
        <div class="collapse-sec" v-for="g in groups" :key="g.name" style="margin:10px;border-radius:8px;">
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
        <div class="collapse-sec" style="margin:10px;border-radius:8px;">
          <div class="head">调用示例</div>
          <div class="body">
            <div class="log-box" style="max-height:none;">
<div class="log-line"><span>curl -H "X-Emby-Token: &lt;API Key&gt;" http://服务器:8097/emby/Items?Recursive=true&amp;Limit=20</span></div>
<div class="log-line"><span>curl "http://服务器:8097/emby/System/Info?api_key=&lt;API Key&gt;"</span></div>
            </div>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, reactive, computed, onMounted } from 'vue'
import { api } from '../../api/client'
import { errText, toast } from './util'

const keys = ref([])
const loading = ref(true)
const dialog = ref(false)
const name = ref('')
const newKey = ref(null)
const busy = ref(false)
const docsOpen = ref(false)
const docs = ref({ Endpoints: [] })
const open = reactive({})

const groups = computed(() => {
  const m = {}
  for (const e of docs.value.Endpoints || []) {
    (m[e.group] = m[e.group] || []).push(e)
  }
  return Object.entries(m).map(([gname, items]) => ({ name: gname, items }))
})

async function load() {
  loading.value = true
  try {
    const r = await api.admin.apiKeys()
    keys.value = r.Items || []
  } catch (e) {
    toast(errText(e), true)
  }
  loading.value = false
}

function startCreate() {
  name.value = ''
  newKey.value = null
  dialog.value = true
}

function closeDialog() {
  dialog.value = false
  if (newKey.value) load()
  newKey.value = null
}

async function create() {
  if (busy.value) return
  busy.value = true
  try {
    newKey.value = await api.admin.createApiKey(name.value)
  } catch (e) {
    toast(errText(e), true)
  }
  busy.value = false
}

async function copy() {
  try {
    await navigator.clipboard.writeText(newKey.value.Key)
    toast('已复制到剪贴板')
  } catch {
    toast('复制失败，请手动选择复制', true)
  }
}

async function remove(k) {
  if (!confirm(`确定删除 API Key「${k.Name}」？使用该密钥的第三方工具将立即失效。`)) return
  try {
    await api.admin.deleteApiKey(k.ID)
    toast('已删除')
    load()
  } catch (e) {
    toast(errText(e), true)
  }
}

onMounted(() => {
  load()
  api.admin.apiDocs().then((d) => { docs.value = d }).catch(() => {})
})
</script>

<style scoped>
.api-head { display: flex; align-items: center; gap: 16px; margin-bottom: 4px; }
.add-btn {
  width: 30px; height: 30px; border-radius: 8px; border: none; cursor: pointer;
  background: transparent; color: #aab3bf; font-size: 20px; line-height: 1;
  display: flex; align-items: center; justify-content: center; transition: all .15s;
}
.add-btn:hover { background: #1b2028; color: #fff; }

.key-list {
  background: #12151b; border: 1px solid #1b2028; border-radius: 12px; overflow: hidden;
}
.key-row {
  display: flex; align-items: center; justify-content: space-between; gap: 16px;
  padding: 16px 20px; border-bottom: 1px solid #1b2028;
}
.key-row:last-child { border-bottom: none; }
.key-row:hover { background: #151a21; }
.key-info { min-width: 0; }
.key-name { font-size: 14px; font-weight: 600; color: #e8eaed; margin-bottom: 6px; }
.key-mask {
  font-family: 'SFMono-Regular', Consolas, 'Liberation Mono', monospace;
  font-size: 12.5px; color: #4a9eff; letter-spacing: 1px; word-break: break-all;
}
.key-side { display: flex; align-items: center; gap: 18px; flex-shrink: 0; }
.key-meta { text-align: right; }
.key-time { font-size: 12.5px; color: #8b949e; white-space: nowrap; }
.key-last { font-size: 11.5px; color: #5a626e; margin-top: 3px; white-space: nowrap; }
.key-del {
  padding: 7px 16px; border-radius: 8px; cursor: pointer;
  background: transparent; border: 1px solid #5c2b2e; color: #f0616d;
  font-size: 13px; transition: all .15s;
}
.key-del:hover { background: rgba(240, 97, 109, .1); border-color: #f0616d; }
.key-empty { padding: 40px 20px; text-align: center; color: #5a626e; font-size: 13px; }

.modal-mask {
  position: fixed; inset: 0; background: rgba(0, 0, 0, .55); z-index: 100;
  display: flex; align-items: center; justify-content: center; padding: 20px;
}
.modal {
  width: 440px; max-width: 100%; background: #171b22; border: 1px solid #232a34;
  border-radius: 14px; padding: 22px; box-shadow: 0 12px 40px rgba(0, 0, 0, .5);
}
.modal-title { font-size: 16px; font-weight: 700; color: #e8eaed; margin-bottom: 14px; }
.modal-actions { display: flex; justify-content: flex-end; gap: 10px; margin-top: 16px; }
.key-reveal {
  font-family: 'SFMono-Regular', Consolas, 'Liberation Mono', monospace;
  font-size: 13px; color: #4a9eff; background: #12151b; border: 1px dashed #2a3341;
  border-radius: 8px; padding: 12px 14px; word-break: break-all; cursor: pointer;
  user-select: all;
}
.key-warn { font-size: 12.5px; color: #c9a34a; margin-top: 10px; line-height: 1.6; }
</style>
