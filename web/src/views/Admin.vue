<template>
  <div class="admin-layout">
    <aside class="admin-side">
      <a v-for="t in tabs" :key="t.key" :class="{ active: tab === t.key }" @click="tab = t.key">{{ t.label }}</a>
    </aside>
    <div class="admin-content">
      <!-- 总览 -->
      <div v-if="tab === 'overview'">
        <div class="stats">
          <div class="stat"><div class="num">{{ status.Movies || 0 }}</div><div class="lbl">电影</div></div>
          <div class="stat"><div class="num">{{ status.Series || 0 }}</div><div class="lbl">剧集</div></div>
          <div class="stat"><div class="num">{{ status.Episodes || 0 }}</div><div class="lbl">集数</div></div>
          <div class="stat"><div class="num">{{ status.Users || 0 }}</div><div class="lbl">用户</div></div>
          <div class="stat"><div class="num">{{ status.Libraries || 0 }}</div><div class="lbl">媒体库</div></div>
          <div class="stat"><div class="num">{{ status.OnlineDevices || 0 }}</div><div class="lbl">在线设备</div></div>
        </div>
        <div class="card mt">
          <h3>服务器信息</h3>
          <table class="tbl">
            <tr><td>名称</td><td>{{ status.ServerName }}</td></tr>
            <tr><td>版本</td><td>{{ status.Version }}</td></tr>
            <tr><td>播放模式</td><td>{{ status.Playback }}（不转码）</td></tr>
            <tr><td>设备租约</td><td>{{ status.DeviceLeaseSeconds }} 秒</td></tr>
            <tr><td>媒体根目录</td><td>{{ (status.MediaRoots || []).join(' ; ') }}</td></tr>
          </table>
        </div>
      </div>

      <!-- 媒体库 -->
      <div v-if="tab === 'libraries'">
        <div class="toolbar">
          <button class="btn green" @click="showAddLib = true">＋ 添加媒体库</button>
          <div class="spacer"></div>
          <button class="btn ghost" @click="scanAll('update')" :disabled="scanning">增量刷新全部</button>
          <button class="btn ghost" @click="scanAll('full')" :disabled="scanning">全量扫描全部</button>
        </div>
        <div class="card" v-for="lib in libs" :key="lib.ID">
          <div style="display:flex;align-items:center;gap:12px;">
            <div style="flex:1;">
              <div style="font-weight:600;">{{ lib.Name }}
                <span class="muted" style="font-weight:400;font-size:12px;margin-left:8px;">
                  {{ lib.Type === 'tvshows' ? '剧集' : '电影' }} · {{ lib.ItemCount }} 个条目
                </span>
                <span v-if="lib.Scanning" class="chip" style="margin-left:8px;">扫描中…</span>
              </div>
              <div class="muted" style="font-size:12.5px;margin-top:4px;">{{ lib.Path }}</div>
              <div class="muted" style="font-size:12px;margin-top:2px;">
                TMDB 刮削: {{ lib.EnableTMDB ? '开启' : '关闭' }} · 上次扫描: {{ fmtTime(lib.LastScan) }}
              </div>
            </div>
            <button class="btn ghost sm" @click="scan(lib.ID, 'update')" :disabled="lib.Scanning">增量</button>
            <button class="btn ghost sm" @click="scan(lib.ID, 'full')" :disabled="lib.Scanning">全量</button>
            <button class="btn danger sm" @click="removeLib(lib)">删除</button>
          </div>
        </div>
        <div v-if="!libs.length" class="empty">暂无媒体库</div>

        <div v-if="showAddLib" class="card" style="border-color: var(--accent);">
          <h3>添加媒体库</h3>
          <label>名称</label>
          <input v-model="newLib.Name" placeholder="例如：电影" style="width:100%;" />
          <label>路径（多个目录用分号分隔）</label>
          <input v-model="newLib.Path" placeholder="/media/movies" style="width:100%;" />
          <label>类型</label>
          <select v-model="newLib.Type" style="width:100%;">
            <option value="movies">电影</option>
            <option value="tvshows">剧集</option>
          </select>
          <label style="display:flex;align-items:center;gap:8px;margin-top:14px;">
            <input type="checkbox" v-model="newLib.EnableTMDB" /> 启用 TMDB 刮削
          </label>
          <div class="toolbar mt">
            <button class="btn" @click="createLib" :disabled="!newLib.Name || !newLib.Path">创建并扫描</button>
            <button class="btn ghost" @click="showAddLib = false">取消</button>
          </div>
        </div>
      </div>

      <!-- 用户 -->
      <div v-if="tab === 'users'">
        <div class="toolbar">
          <button class="btn green" @click="showAddUser = true">＋ 添加用户</button>
        </div>
        <div class="card">
          <table class="tbl">
            <thead><tr><th>用户名</th><th>角色</th><th>播放权限</th><th>设备上限</th><th>操作</th></tr></thead>
            <tbody>
              <tr v-for="u in users" :key="u.Id">
                <td>{{ u.Name }}</td>
                <td>{{ u.Policy?.IsAdministrator ? '管理员' : '普通用户' }}</td>
                <td>
                  <button class="btn sm" :class="u.Policy?.EnableMediaPlayback ? 'green' : 'ghost'"
                    @click="togglePlay(u)">{{ u.Policy?.EnableMediaPlayback ? '允许' : '禁止' }}</button>
                </td>
                <td>
                  <input type="number" min="1" max="100" style="width:70px;"
                    :value="u.Policy?.SimultaneousStreamLimit"
                    @change="setMaxDevices(u, $event.target.value)" />
                </td>
                <td>
                  <button class="btn ghost sm" @click="resetPw(u)">改密</button>
                  <button v-if="!u.FirstAdminHint" class="btn danger sm" style="margin-left:6px;" @click="removeUser(u)">删除</button>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
        <div v-if="showAddUser" class="card" style="border-color: var(--accent);">
          <h3>添加用户</h3>
          <label>用户名</label>
          <input v-model="newUser.Name" style="width:100%;" />
          <label>密码</label>
          <input v-model="newUser.Password" type="password" style="width:100%;" />
          <label style="display:flex;align-items:center;gap:8px;">
            <input type="checkbox" v-model="newUser.IsAdmin" /> 设为管理员
          </label>
          <label>同时播放设备上限</label>
          <input v-model.number="newUser.MaxDevices" type="number" min="1" max="100" style="width:100px;" />
          <div class="toolbar mt">
            <button class="btn" @click="createUser" :disabled="!newUser.Name">创建</button>
            <button class="btn ghost" @click="showAddUser = false">取消</button>
          </div>
        </div>
      </div>

      <!-- TMDB -->
      <div v-if="tab === 'tmdb'">
        <div class="card">
          <h3>TMDB 刮削设置</h3>
          <label>API Key（v3 auth）{{ tmdb.HasKey ? '（已配置）' : '' }}</label>
          <input v-model="tmdb.APIKey" placeholder="在 themoviedb.org 申请" style="width:100%;" />
          <label>元数据语言</label>
          <select v-model="tmdb.Language" style="width:200px;">
            <option value="zh-CN">中文（zh-CN）</option>
            <option value="en-US">English（en-US）</option>
            <option value="ja-JP">日本語（ja-JP）</option>
          </select>
          <label style="display:flex;align-items:center;gap:8px;">
            <input type="checkbox" v-model="tmdb.DownloadImages" /> 下载海报与背景图到服务器
          </label>
          <div class="toolbar mt">
            <button class="btn" @click="saveTmdb">保存</button>
          </div>
          <p class="muted" style="font-size:12.5px;">
            刮削在新条目扫描后自动进行；也可在媒体库页重新扫描触发。带 tmdbid 的 NFO 优先使用 NFO 中的 ID。
          </p>
        </div>
      </div>

      <!-- 文件 -->
      <div v-if="tab === 'files'">
        <div class="toolbar">
          <button class="btn ghost sm" @click="upDir" :disabled="!filePath">↑ 上一级</button>
          <input v-model="filePath" style="flex:1;" placeholder="路径" @keyup.enter="loadFiles(filePath)" />
          <button class="btn sm" @click="loadFiles(filePath)">打开</button>
          <button class="btn ghost sm" @click="mkDir">新建目录</button>
          <label class="btn ghost sm" :style="{margin:0}">
            上传<input type="file" style="display:none" @change="uploadFile" />
          </label>
        </div>
        <div class="card">
          <table class="tbl">
            <thead><tr><th>名称</th><th>大小</th><th>修改时间</th><th>操作</th></tr></thead>
            <tbody>
              <tr v-for="d in fileDirs" :key="d.path">
                <td><a href="javascript:void(0)" @click="loadFiles(d.path)">📁 {{ d.name }}</a></td>
                <td class="muted">-</td><td class="muted">{{ fmtTime(d.modTime) }}</td>
                <td><button class="btn danger sm" @click="delFile(d)">删除</button></td>
              </tr>
              <tr v-for="f in fileItems" :key="f.path">
                <td>📄 {{ f.name }}</td>
                <td class="muted">{{ fmtSize(f.size) }}</td>
                <td class="muted">{{ fmtTime(f.modTime) }}</td>
                <td>
                  <button class="btn ghost sm" @click="renameFile(f)">重命名</button>
                  <button class="btn danger sm" style="margin-left:6px;" @click="delFile(f)">删除</button>
                </td>
              </tr>
            </tbody>
          </table>
          <div v-if="!fileDirs.length && !fileItems.length" class="empty">空目录</div>
        </div>
      </div>

      <!-- 日志 -->
      <div v-if="tab === 'logs'">
        <div class="toolbar">
          <div class="chip" :style="sseOnline ? '' : 'color:var(--danger)'">{{ sseOnline ? '● 实时推送中' : '○ 未连接' }}</div>
          <div class="spacer"></div>
          <button class="btn ghost sm" @click="clearLogs">清空</button>
        </div>
        <div class="log-box" ref="logBox">
          <div v-for="e in logs" :key="e.id" class="log-line" :class="e.level">
            <span class="t">{{ fmtTime(e.time) }}</span>
            <span class="lv">[{{ e.level.toUpperCase() }}]</span>
            <span>{{ e.message }}</span>
            <div v-if="e.detail" style="padding-left:60px;color:var(--text-dim);">{{ e.detail }}</div>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, onMounted, onBeforeUnmount, nextTick } from 'vue'
import { api, state } from '../api/client'

const tabs = [
  { key: 'overview', label: '总览' },
  { key: 'libraries', label: '媒体库' },
  { key: 'users', label: '用户与设备' },
  { key: 'tmdb', label: 'TMDB 刮削' },
  { key: 'files', label: '文件管理' },
  { key: 'logs', label: '实时日志' }
]
const tab = ref('overview')
const status = ref({})
const libs = ref([])
const users = ref([])
const scanning = ref(false)
const showAddLib = ref(false)
const showAddUser = ref(false)
const newLib = ref({ Name: '', Path: '', Type: 'movies', EnableTMDB: true })
const newUser = ref({ Name: '', Password: '', IsAdmin: false, MaxDevices: 2 })
const tmdb = ref({ APIKey: '', Language: 'zh-CN', DownloadImages: true, HasKey: false })

// 文件管理
const filePath = ref('')
const fileDirs = ref([])
const fileItems = ref([])

// 日志
const logs = ref([])
const logBox = ref(null)
let es = null
const sseOnline = ref(false)

function fmtTime(t) {
  if (!t) return '-'
  const d = new Date(t)
  if (isNaN(d.getTime())) return '-'
  return d.toLocaleString('zh-CN', { hour12: false })
}
function fmtSize(n) {
  if (!n) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let i = 0
  while (n >= 1024 && i < units.length - 1) { n /= 1024; i++ }
  return n.toFixed(i ? 1 : 0) + ' ' + units[i]
}
function toast(msg, isErr = false) {
  const el = document.createElement('div')
  el.className = 'toast' + (isErr ? ' err' : '')
  el.textContent = msg
  document.body.appendChild(el)
  setTimeout(() => el.remove(), 2400)
}

async function loadStatus() { status.value = await api.admin.status() }
async function loadLibs() { libs.value = await api.admin.libraries() }
async function loadUsers() { users.value = await api.admin.users() }
async function loadTmdb() { tmdb.value = { ...tmdb.value, ...(await api.admin.tmdb()) } }

async function scanAll(mode) {
  scanning.value = true
  try {
    await api.admin.scanAll(mode)
    toast(mode === 'full' ? '全量扫描已开始' : '增量刷新已开始')
    setTimeout(loadLibs, 800)
  } catch (e) { toast(e.message, true) } finally { scanning.value = false }
}
async function scan(id, mode) {
  try { await api.admin.scan(id, mode); toast('扫描已开始'); setTimeout(loadLibs, 800) }
  catch (e) { toast(e.message, true) }
}
async function createLib() {
  try {
    await api.admin.createLibrary(newLib.value)
    toast('媒体库已创建，开始扫描')
    showAddLib.value = false
    newLib.value = { Name: '', Path: '', Type: 'movies', EnableTMDB: true }
    setTimeout(loadLibs, 500)
  } catch (e) { toast(e.message, true) }
}
async function removeLib(lib) {
  if (!confirm(`删除媒体库「${lib.Name}」？其中的条目索引将被移除，媒体文件不受影响。`)) return
  await api.admin.deleteLibrary(lib.ID)
  loadLibs()
}
async function togglePlay(u) {
  await api.admin.updateUser(u.Id, { AllowPlayback: !u.Policy.EnableMediaPlayback })
  loadUsers()
}
async function setMaxDevices(u, v) {
  const n = parseInt(v)
  if (!n || n < 1) return
  await api.admin.updateUser(u.Id, { MaxDevices: n })
  toast(`设备上限已设为 ${n}`)
}
async function resetPw(u) {
  const pw = prompt(`为用户 ${u.Name} 设置新密码`)
  if (!pw) return
  await api.admin.updateUser(u.Id, { Password: pw })
  toast('密码已更新')
}
async function removeUser(u) {
  if (!confirm(`删除用户 ${u.Name}？`)) return
  await api.admin.deleteUser(u.Id)
  loadUsers()
}
async function createUser() {
  try {
    await api.admin.createUser(newUser.value)
    toast('用户已创建')
    showAddUser.value = false
    newUser.value = { Name: '', Password: '', IsAdmin: false, MaxDevices: 2 }
    loadUsers()
  } catch (e) { toast(e.message, true) }
}
async function saveTmdb() {
  try {
    await api.admin.saveTmdb({
      APIKey: tmdb.value.APIKey,
      Language: tmdb.value.Language,
      DownloadImages: tmdb.value.DownloadImages
    })
    toast('刮削设置已保存')
    loadTmdb()
  } catch (e) { toast(e.message, true) }
}

// 文件管理
async function loadFiles(path) {
  try {
    const d = await api.admin.files(path)
    filePath.value = d.Path || path
    fileDirs.value = d.Directories || []
    fileItems.value = d.Files || []
  } catch (e) { toast(e.message, true) }
}
function upDir() {
  const p = filePath.value.replace(/\/+$/, '')
  const i = p.lastIndexOf('/')
  if (i > 0) loadFiles(p.slice(0, i))
}
async function mkDir() {
  const name = prompt('目录名')
  if (!name) return
  await api.admin.fileOp('mkdir', filePath.value, name)
  loadFiles(filePath.value)
}
async function renameFile(f) {
  const name = prompt('新名称', f.name)
  if (!name || name === f.name) return
  await api.admin.fileOp('rename', f.path, name)
  loadFiles(filePath.value)
}
async function delFile(f) {
  if (!confirm(`删除 ${f.name}？`)) return
  await api.admin.fileOp('delete', f.path, '')
  loadFiles(filePath.value)
}
async function uploadFile(ev) {
  const file = ev.target.files[0]
  if (!file) return
  const fd = new FormData()
  fd.append('file', file)
  const res = await fetch(api.admin.uploadUrl(filePath.value), { method: 'POST', body: fd })
  if (res.ok) { toast('上传完成'); loadFiles(filePath.value) }
  else toast('上传失败', true)
  ev.target.value = ''
}

// 日志 SSE
function connectLogs() {
  if (es) es.close()
  es = new EventSource(`/admin/logs/stream?api_key=${encodeURIComponent(state.token)}`)
  es.onopen = () => { sseOnline.value = true }
  es.onerror = () => { sseOnline.value = false }
  es.onmessage = (ev) => {
    try {
      const e = JSON.parse(ev.data)
      logs.value.unshift(e)
      if (logs.value.length > 300) logs.value.pop()
      nextTick(() => { if (logBox.value) logBox.value.scrollTop = 0 })
    } catch {}
  }
}
async function clearLogs() {
  await api.del('/admin/logs')
  logs.value = []
}

onMounted(() => {
  loadStatus()
  loadLibs()
  loadUsers()
  loadTmdb()
  loadFiles('')
  connectLogs()
})
onBeforeUnmount(() => { if (es) es.close() })
</script>
