<template>
  <div class="tlog" ref="box">
    <div v-for="(l, i) in lines" :key="i" class="tlog-line">
      <span class="tlog-time">{{ fmtT(l.time) }}</span>
      <span class="tlog-lv" :class="l.level">{{ lvName(l.level) }}</span>
      <span class="tlog-msg" :class="{ err: l.level === 'error', warn: l.level === 'warn' }">{{ l.msg }}</span>
    </div>
    <div v-if="!lines || !lines.length" class="tlog-empty">暂无详细日志</div>
  </div>
</template>

<script setup>
import { ref, watch, nextTick } from 'vue'

const props = defineProps({ lines: { type: Array, default: () => [] } })
const box = ref(null)

function fmtT(t) {
  if (!t) return ''
  const d = new Date(t)
  const p = (n) => String(n).padStart(2, '0')
  return `${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`
}
function lvName(l) {
  return { info: '信息', warn: '警告', error: '错误' }[l] || l
}

watch(() => props.lines, async () => {
  await nextTick()
  if (box.value) box.value.scrollTop = box.value.scrollHeight
}, { deep: true })
</script>
