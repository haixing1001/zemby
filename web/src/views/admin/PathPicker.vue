<template>
  <div v-if="show" class="pp-mask" @click.self="$emit('close')">
    <div class="pp-box">
      <div class="pp-head">
        <b>{{ title || (mode === 'dir' ? '选择目录' : '选择文件') }}</b>
        <button class="pp-x" @click="$emit('close')">✕</button>
      </div>

      <div class="pp-crumb">
        <span class="pp-ci" @click="go('')">根目录</span>
        <template v-for="(seg, i) in crumbs" :key="seg.path">
          <span class="pp-sep">/</span>
          <span class="pp-ci" :class="{ cur: i === crumbs.length - 1 }" @click="go(seg.path)">{{ seg.name }}</span>
        </template>
        <span style="flex:1"></span>
        <button class="btn ghost sm" :disabled="!curPath || loading" @click="up">↑ 上一级</button>
        <button v-if="mode === 'dir' && curPath" class="btn ghost sm" @click="mkdir">＋ 新建文件夹</button>
      </div>

      <div class="pp-list">
        <div v-if="loading" class="spin"></div>
        <div v-else-if="!dirs.length && !(mode === 'file' && files.length)" class="pp-empty">空目录</div>
        <template v-else>
          <div v-for="d in dirs" :key="'d' + d.path" class="pp-row" @click="go(d.path)">
            <span class="pp-ico">📁</span>
            <span class="pp-name">{{ d.name }}</span>
            <span class="pp-go" v-if="mode === 'dir'">选择 ›</span>
          </div>
          <template v-if="mode === 'file'">
            <div v-for="f in files" :key="'f' + f.path" class="pp-row" :class="{ sel: selPath === f.path }" @click="selPath = f.path">
              <span class="pp-ico">📄</span>
              <span class="pp-name">{{ f.name }}</span>
              <span class="pp-size" v-if="f.size">{{ fmtSize(f.size) }}</span>
            </div>
          </template>
          <div v-else-if="files.length" class="pp-files-note">（{{ files.length }} 个文件不显示，仅可选择目录）</div>
        </template>
      </div>

      <div class="pp-foot">
        <input v-model="selPath" :placeholder="mode === 'dir' ? '当前目录（可手动输入）' : '选择或手动输入文件路径'" />
        <button class="btn ghost" @click="$emit('close')">取消</button>
        <button class="btn" :disabled="!selPath || !selPath.trim()" @click="confirm">{{ mode === 'dir' ? '选择此目录' : '确定' }}</button>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, watch } from 'vue'
import { api } from '../../api/client'
import { toast, errText } from './util'

const props = defineProps({
  show: Boolean,
  mode: { type: String, default: 'dir' }, // dir | file
  title: { type: String, default: '' },
  initial: { type: String, default: '' }
})
const emit = defineEmits(['close', 'select'])

const loading = ref(false)
const curPath = ref('')
const dirs = ref([])
const files = ref([])
const selPath = ref('')

const crumbs = computed(() => {
  if (!curPath.value) return []
  const parts = curPath.value.split('/').filter(Boolean)
  return parts.map((p, i) => ({
    name: p,
    path: '/' + parts.slice(0, i + 1).join('/')
  }))
})

watch(() => props.show, (v) => {
  if (!v) return
  selPath.value = props.initial || ''
  load(props.initial && props.mode === 'file' ? parentOf(props.initial) : (props.initial || ''))
})

async function load(path) {
  loading.value = true
  try {
    const d = await api.admin.files(path || '')
    curPath.value = d.Path || ''
    dirs.value = d.Directories || []
    files.value = d.Files || []
    if (props.mode === 'dir') selPath.value = curPath.value
  } catch (e) {
    toast(errText(e), true)
  } finally {
    loading.value = false
  }
}

function go(p) { load(p) }

function parentOf(p) {
  const s = String(p || '').replace(/\/+$/, '')
  const i = s.lastIndexOf('/')
  return i <= 0 ? '' : s.slice(0, i)
}

function up() { go(parentOf(curPath.value)) }

async function mkdir() {
  const name = prompt('新文件夹名称')
  if (!name) return
  try {
    await api.admin.fileOp('mkdir', curPath.value, name)
    toast('目录已创建')
    load(curPath.value)
  } catch (e) { toast(errText(e), true) }
}

function confirm() {
  const p = selPath.value.trim()
  if (!p) return
  emit('select', p)
  emit('close')
}

function fmtSize(n) {
  if (n > 1 << 30) return (n / (1 << 30)).toFixed(1) + ' GB'
  if (n > 1 << 20) return (n / (1 << 20)).toFixed(1) + ' MB'
  if (n > 1 << 10) return (n / (1 << 10)).toFixed(1) + ' KB'
  return n + ' B'
}
</script>

<style scoped>
.pp-mask { position: fixed; inset: 0; background: rgba(0,0,0,.55); z-index: 300; display: flex; align-items: center; justify-content: center; }
.pp-box { width: 640px; max-width: 94vw; max-height: 84vh; background: #12151b; border: 1px solid #262d38; border-radius: 14px; display: flex; flex-direction: column; overflow: hidden; }
.pp-head { display: flex; align-items: center; padding: 14px 16px; border-bottom: 1px solid #1e232b; font-size: 15px; }
.pp-x { margin-left: auto; background: none; border: none; color: #6b7482; cursor: pointer; font-size: 15px; }
.pp-x:hover { color: var(--text); }
.pp-crumb { display: flex; align-items: center; gap: 4px; flex-wrap: wrap; padding: 10px 16px; border-bottom: 1px solid #1a1f27; font-size: 12.5px; color: #6b7482; }
.pp-ci { cursor: pointer; color: #9aa4b2; padding: 2px 4px; border-radius: 6px; }
.pp-ci:hover { background: #1a1f27; color: var(--text); }
.pp-ci.cur { color: #5b9bff; }
.pp-sep { color: #3a4250; }
.pp-list { flex: 1; overflow-y: auto; padding: 8px 10px; min-height: 240px; }
.pp-empty { text-align: center; color: #566070; padding: 40px 0; font-size: 13px; }
.pp-row { display: flex; align-items: center; gap: 10px; padding: 8px 10px; border-radius: 8px; cursor: pointer; color: #c6cdd8; font-size: 13.5px; }
.pp-row:hover { background: #181d25; }
.pp-row.sel { background: #1a2740; color: #5b9bff; }
.pp-ico { flex: 0 0 auto; }
.pp-name { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.pp-go { color: #566070; font-size: 12px; }
.pp-size { color: #566070; font-size: 12px; }
.pp-files-note { text-align: center; color: #4d5560; font-size: 12px; padding: 10px 0 6px; }
.pp-foot { display: flex; gap: 8px; padding: 12px 16px; border-top: 1px solid #1e232b; }
.pp-foot input { flex: 1; }
</style>
