<template>
  <div>
    <div style="display:flex;align-items:flex-start;">
      <div>
        <h1 class="adm-h1">媒体库管理</h1>
        <div class="adm-desc">定时任务：{{ cfg.Realtime ? '每 2 分钟增量' : '关闭' }} · 提取并发：{{ cfg.Concurrency || 2 }} · 封面策略：手动</div>
      </div>
      <span style="flex:1"></span>
      <router-link to="/admin/scrape" class="btn ghost sm" title="刮削设置">⚙</router-link>
    </div>

    <!-- 工具行 -->
    <div class="set-row" style="padding:10px 4px;border:none;margin-bottom:6px;">
      <div style="display:flex;gap:10px;">
        <button class="btn ghost sm" title="添加媒体库" @click="showAddLib = true">＋</button>
        <button class="btn ghost sm" title="打开文件管理" @click="$router.push('/admin/files')">📁</button>
        <button class="btn ghost sm" title="全库扫描" @click="scanAll('update')" :disabled="busy">▶</button>
        <button class="btn ghost sm" title="停止扫描" @click="stopAll">⏹</button>
      </div>
      <span style="flex:1"></span>
      <div class="ring" :style="{'--p': progress}"><i>{{ progress }}%</i></div>
    </div>

    <!-- 库卡片 -->
    <div class="lib-card" v-for="lib in libs" :key="lib.ID" :style="lib.Hidden ? 'opacity:.55' : ''">
      <div class="lib-poster">
        <img v-if="lib.HasPoster" :src="posterUrl(lib.ID)" @error="$event.target.style.display='none'" />
        <span v-else>🎬</span>
      </div>
      <div style="flex:1;min-width:0;">
        <div style="display:flex;align-items:center;gap:8px;">
          <b>{{ lib.Name }}</b>
          <span class="tag gray" v-if="lib.Hidden">已隐藏</span>
          <span class="tag" v-if="lib.Scanning">扫描中</span>
        </div>
        <div class="muted" style="font-size:12.5px;margin-top:4px;">
          {{ lib.Type === 'tvshows' ? '剧集' : '电影' }} · {{ lib.ItemCount }} 项
        </div>
        <div class="muted" style="font-size:12px;margin-top:2px;">{{ lib.Path }}</div>
      </div>
      <span class="lib-dot" :class="{ warn: lib.Scanning }" :title="lib.Scanning ? '扫描中' : '已就绪'"></span>
      <div class="menu-wrap">
        <button class="btn ghost sm" @click="toggleMenu(lib.ID)">⋯</button>
        <div class="menu-pop" v-if="openMenu === lib.ID">
          <div class="mi" @click="doScan(lib, 'full')"><span class="ico" v-html="ic.scan"></span>全量扫描</div>
          <div class="mi" @click="doScan(lib, 'update')"><span class="ico" v-html="ic.refresh"></span>刷新扫描</div>
          <div class="mi" @click="addFolder(lib)"><span class="ico" v-html="ic.folder"></span>添加媒体文件夹</div>
          <div class="mi" @click="insertCover(lib)"><span class="ico" v-html="ic.img"></span>插入媒体封面</div>
          <div class="mi" @click="genCover(lib)"><span class="ico" v-html="ic.img"></span>生成封面</div>
          <div class="mi" @click="removeCover(lib)"><span class="ico" v-html="ic.trash"></span>移除封面</div>
          <div class="mi" @click="toggleHide(lib)"><span class="ico" v-html="ic.hide"></span>{{ lib.Hidden ? '显示媒体库' : '隐藏媒体库' }}</div>
          <div class="mi" @click="renameLib(lib)"><span class="ico" v-html="ic.rename"></span>重命名媒体库</div>
          <div class="mi danger" @click="removeLib(lib)"><span class="ico" v-html="ic.trash"></span>删除媒体库</div>
        </div>
      </div>
    </div>
    <div v-if="!libs.length" class="empty-line">暂无媒体库，点击右上角 ＋ 添加</div>

    <!-- 添加媒体库 -->
    <div class="collapse-sec" v-if="showAddLib" style="border-color:var(--accent);margin-top:14px;">
      <div class="head">添加媒体库</div>
      <div class="body">
        <label>名称</label>
        <input v-model="newLib.Name" placeholder="例如：电影" style="width:100%;" />
        <label>路径（多个目录用分号分隔）</label>
        <input v-model="newLib.Path" placeholder="/media/movies" style="width:100%;" />
        <label>类型</label>
        <select v-model="newLib.Type" style="width:200px;">
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
  </div>
</template>

<script setup>
import { ref, reactive, computed, onMounted, onBeforeUnmount } from 'vue'
import { api } from '../../api/client'
import { toast, errText } from './util'

const libs = ref([])
const cfg = ref({})
const showAddLib = ref(false)
const newLib = ref({ Name: '', Path: '', Type: 'movies', EnableTMDB: true })
const openMenu = ref('')
const busy = ref(false)
const progress = ref(0)
let timer = null

const ic = {
  scan: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M4 8V6a2 2 0 0 1 2-2h2M16 4h2a2 2 0 0 1 2 2v2M20 16v2a2 2 0 0 1-2 2h-2M8 20H6a2 2 0 0 1-2-2v-2"/></svg>',
  refresh: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M20 11A8 8 0 1 0 20 13M20 4v7h-7"/></svg>',
  folder: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v9a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V7z"/></svg>',
  img: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="3" y="4" width="18" height="16" rx="2"/><circle cx="9" cy="10" r="1.6"/><path d="M3 17l6-5 4 3 4-4 4 4"/></svg>',
  trash: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M4 7h16M10 11v6M14 11v6M6 7l1 13h10l1-13M9 7V4h6v3"/></svg>',
  hide: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M3 3l18 18"/><path d="M10.6 5.1A9.8 9.8 0 0 1 12 5c5 0 9 4.5 10 7-.3.8-1 2-2.2 3.3M6.6 6.6C4 8.1 2.6 10.5 2 12c1 2.5 5 7 10 7 1.5 0 3-.4 4.3-1.1"/><path d="M9.9 9.9a3 3 0 0 0 4.2 4.2"/></svg>',
  rename: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M4 20h4L19.5 8.5a2.1 2.1 0 0 0-3-3L5 17v3z"/></svg>'
}

function posterUrl(id) { return api.admin.libraryPosterUrl(id) }

async function load() {
  try {
    libs.value = await api.admin.libraries()
  } catch (e) { toast(errText(e), true) }
}
async function loadProgress() {
  try {
    const s = await api.admin.scanStatus()
    const running = (s.Tasks || []).filter(t => t.State === 'running' && t.Total > 0)
    if (running.length) {
      const done = running.reduce((a, t) => a + t.Done, 0)
      const total = running.reduce((a, t) => a + t.Total, 0)
      progress.value = total ? Math.min(100, Math.round(done / total * 100)) : 0
    } else {
      progress.value = 0
    }
  } catch {}
}
function toggleMenu(id) { openMenu.value = openMenu.value === id ? '' : id }

async function doScan(lib, mode) {
  openMenu.value = ''
  try {
    await api.admin.scan(lib.ID, mode)
    toast(mode === 'full' ? `「${lib.Name}」全量扫描已开始` : `「${lib.Name}」刷新扫描已开始`)
    setTimeout(load, 600)
  } catch (e) { toast(errText(e), true) }
}
async function scanAll(mode) {
  busy.value = true
  try {
    await api.admin.scanAll(mode)
    toast('全库扫描已开始')
  } catch (e) { toast(errText(e), true) } finally { busy.value = false }
}
async function stopAll() {
  await api.admin.scanStop()
  toast('已请求停止全部扫描')
}
async function addFolder(lib) {
  openMenu.value = ''
  const p = prompt(`为「${lib.Name}」添加媒体文件夹路径`)
  if (!p) return
  try {
    await api.admin.patchLibrary(lib.ID, { AddFolder: p })
    toast('文件夹已添加，建议执行一次扫描')
    load()
  } catch (e) { toast(errText(e), true) }
}
async function insertCover(lib) {
  openMenu.value = ''
  const p = prompt('输入封面图片的完整路径（jpg/png）')
  if (!p) return
  try {
    await api.post(`/admin/libraries/${lib.ID}/cover`, { Path: p })
    toast('封面已插入')
    load()
  } catch (e) { toast(errText(e), true) }
}
async function genCover(lib) {
  openMenu.value = ''
  toast('正在从视频抽帧生成封面…')
  try {
    await api.post(`/admin/libraries/${lib.ID}/cover/generate`, {})
    toast('封面已生成')
    load()
  } catch (e) { toast(errText(e), true) }
}
async function removeCover(lib) {
  openMenu.value = ''
  await api.del(`/admin/libraries/${lib.ID}/cover`)
  toast('封面已移除')
  load()
}
async function toggleHide(lib) {
  openMenu.value = ''
  await api.admin.patchLibrary(lib.ID, { Hidden: !lib.Hidden })
  toast(lib.Hidden ? '媒体库已显示' : '媒体库已隐藏')
  load()
}
async function renameLib(lib) {
  openMenu.value = ''
  const name = prompt('新名称', lib.Name)
  if (!name || name === lib.Name) return
  await api.admin.patchLibrary(lib.ID, { Name: name })
  toast('已重命名')
  load()
}
async function removeLib(lib) {
  openMenu.value = ''
  if (!confirm(`删除媒体库「${lib.Name}」？索引将被移除，媒体文件不受影响。`)) return
  await api.admin.deleteLibrary(lib.ID)
  load()
}
async function createLib() {
  try {
    await api.admin.createLibrary(newLib.value)
    toast('媒体库已创建，开始扫描')
    showAddLib.value = false
    newLib.value = { Name: '', Path: '', Type: 'movies', EnableTMDB: true }
    setTimeout(load, 500)
  } catch (e) { toast(errText(e), true) }
}

onMounted(async () => {
  load()
  loadProgress()
  try { cfg.value = await api.admin.probeConfig() } catch {}
  timer = setInterval(loadProgress, 3000)
})
onBeforeUnmount(() => clearInterval(timer))
</script>
