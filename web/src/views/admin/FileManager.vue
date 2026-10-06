<template>
  <div>
    <h1 class="adm-h1">文件管理</h1>
    <div style="display:flex;align-items:center;gap:10px;margin-bottom:16px;">
      <div class="adm-desc" style="margin:0;flex:1;">直接管理服务器媒体目录</div>
      <label class="btn sm" :style="{ margin: 0 }">上传<input type="file" style="display:none" @change="uploadFile" /></label>
      <button class="btn ghost sm" @click="mkDir">新建文件夹</button>
    </div>

    <!-- 导航行 -->
    <div class="collapse-sec" style="margin-bottom:14px;">
      <div class="body" style="display:flex;align-items:center;gap:12px;padding:12px 16px;flex-wrap:wrap;">
        <button class="btn ghost sm" @click="upDir" :disabled="!filePath">‹</button>
        <div class="crumb" style="flex:1;min-width:200px;">
          <a @click="loadFiles('')">/ 媒体文件</a>
          <template v-for="(seg, i) in crumbs" :key="i">
            <span class="sep">/</span>
            <a @click="loadFiles(seg.path)">{{ seg.name }}</a>
          </template>
        </div>
        <input v-model="search" placeholder="搜索文件" style="width:220px;" />
        <select v-model="sortKey" style="width:100px;">
          <option value="name">名称</option>
          <option value="size">大小</option>
          <option value="time">时间</option>
        </select>
        <button class="btn ghost sm" @click="asc = !asc">{{ asc ? '升序' : '降序' }}</button>
      </div>
    </div>

    <!-- 列表 -->
    <div class="collapse-sec">
      <div class="body" style="padding:0;">
        <div v-if="!filteredDirs.length && !filteredFiles.length" class="empty-line" style="margin:14px;">这个文件夹是空的</div>
        <table v-else class="tbl" style="margin:0;">
          <thead><tr><th>名称</th><th style="width:110px;">大小</th><th style="width:170px;">修改时间</th><th style="width:170px;">操作</th></tr></thead>
          <tbody>
            <tr v-for="d in filteredDirs" :key="d.path">
              <td><a href="javascript:void(0)" @click="loadFiles(d.path)">📁 {{ d.name }}</a></td>
              <td class="muted">-</td>
              <td class="muted">{{ fmtTime(d.modTime) }}</td>
              <td><button class="btn danger sm" @click="delFile(d)">删除</button></td>
            </tr>
            <tr v-for="f in filteredFiles" :key="f.path">
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
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, onMounted } from 'vue'
import { api } from '../../api/client'
import { fmtTime, fmtSize, toast, errText } from './util'

const filePath = ref('')
const dirs = ref([])
const files = ref([])
const search = ref('')
const sortKey = ref('name')
const asc = ref(true)

const crumbs = computed(() => {
  if (!filePath.value) return []
  const parts = filePath.value.replace(/\/+$/, '').split('/').filter(Boolean)
  const out = []
  let acc = ''
  for (const p of parts) {
    acc += '/' + p
    out.push({ name: p, path: acc.replace(/^\//, '') })
  }
  return out
})

function sortList(list) {
  const dir = asc.value ? 1 : -1
  return [...list].sort((a, b) => {
    if (sortKey.value === 'size') return ((a.size || 0) - (b.size || 0)) * dir
    if (sortKey.value === 'time') return (new Date(a.modTime) - new Date(b.modTime)) * dir
    return String(a.name).localeCompare(String(b.name), 'zh-CN') * dir
  })
}
const filteredDirs = computed(() => sortList(dirs.value.filter(d => !search.value || d.name.toLowerCase().includes(search.value.toLowerCase()))))
const filteredFiles = computed(() => sortList(files.value.filter(f => !search.value || f.name.toLowerCase().includes(search.value.toLowerCase()))))

async function loadFiles(path) {
  try {
    const d = await api.admin.files(path)
    filePath.value = d.Path || path
    dirs.value = d.Directories || []
    files.value = d.Files || []
  } catch (e) { toast(errText(e), true) }
}
function upDir() {
  const p = filePath.value.replace(/\/+$/, '')
  const i = p.lastIndexOf('/')
  if (i > 0) loadFiles(p.slice(0, i))
  else loadFiles('')
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

onMounted(() => loadFiles(''))
</script>
