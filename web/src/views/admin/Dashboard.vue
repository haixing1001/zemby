<template>
  <div>
    <h1 class="adm-h1">控制台</h1>
    <div class="dash-status">
      <span class="dot"></span>服务正常 · 运行 {{ d.Uptime || '0天 0小时' }}
    </div>

    <!-- 统计卡 -->
    <div class="stat-cards">
      <div class="stat-card"><div class="lbl">电影</div><div class="num">{{ d.Movies ?? 0 }}</div></div>
      <div class="stat-card"><div class="lbl">电视剧</div><div class="num">{{ d.Series ?? 0 }}</div></div>
      <div class="stat-card"><div class="lbl">剧集</div><div class="num">{{ d.Episodes ?? 0 }}</div></div>
      <div class="stat-card"><div class="lbl">用户</div><div class="num">{{ d.Users ?? 0 }}</div></div>
      <div class="stat-card">
        <div class="lbl">CPU</div>
        <div class="num">{{ (d.CPUPercent ?? 0).toFixed(1) }}<span style="font-size:14px;">%</span></div>
        <div class="sub">zemby 进程</div>
      </div>
      <div class="stat-card">
        <div class="lbl">内存</div>
        <div class="num">{{ (d.MemMB ?? 0).toFixed(0) }} <span style="font-size:14px;">MB</span></div>
        <div class="sub">zemby 进程占用</div>
      </div>
      <div class="stat-card"><div class="lbl">运行时长</div><div class="num" style="font-size:19px;">{{ d.Uptime || '-' }}</div></div>
      <div class="stat-card"><div class="lbl">正在播放</div><div class="num">{{ d.PlayingCount ?? 0 }}</div></div>
    </div>

    <!-- 正在播放 -->
    <div class="collapse-sec">
      <div class="head" @click="sec.playing = !sec.playing">正在播放<span class="arrow" :class="{ open: sec.playing }">⌄</span></div>
      <div class="body" v-if="sec.playing">
        <div v-if="!(d.NowPlaying && d.NowPlaying.length)" class="empty-line">当前没有正在播放</div>
        <table v-else class="tbl">
          <thead><tr><th>用户</th><th>条目</th><th>设备</th><th>开始时间</th></tr></thead>
          <tbody>
            <tr v-for="p in d.NowPlaying" :key="p.DeviceId + p.ItemId">
              <td>{{ p.UserName }}</td>
              <td><a href="javascript:void(0)" @click="$router.push('/item/' + p.ItemId)">{{ p.ItemName || p.ItemId }}</a></td>
              <td class="muted">{{ p.DeviceId }}</td>
              <td class="muted">{{ fmtTime(p.UpdatedAt) }}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>

    <!-- 任务状态 -->
    <div class="collapse-sec">
      <div class="head" @click="sec.tasks = !sec.tasks">任务状态<span class="arrow" :class="{ open: sec.tasks }">⌄</span></div>
      <div class="body" v-if="sec.tasks">
        <div v-if="!(d.Tasks && d.Tasks.length)" class="empty-line">当前没有任务</div>
        <table v-else class="tbl">
          <thead><tr><th>媒体库</th><th>类型</th><th>状态</th><th>开始时间</th></tr></thead>
          <tbody>
            <tr v-for="t in d.Tasks" :key="t.ID">
              <td>{{ t.Library || '-' }}</td>
              <td>{{ t.Mode === 'full' ? '全量扫描' : t.Mode === 'scrape' ? '刮削' : '增量扫描' }}</td>
              <td>
                <span class="tag" :class="{ gray: t.State === 'done' }">{{ taskState(t.State) }}</span>
              </td>
              <td class="muted">{{ fmtTime(t.StartedAt) }}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>

    <!-- 活跃状态 -->
    <div class="collapse-sec">
      <div class="head" @click="sec.activity = !sec.activity">
        活跃状态
        <span class="arrow" :class="{ open: sec.activity }">⌄</span>
        <span style="flex:1"></span>
        <button v-if="sec.activity && d.Activity && d.Activity.length" class="btn ghost sm" @click.stop="clearActs">🗑</button>
      </div>
      <div class="body" v-if="sec.activity">
        <div v-if="!(d.Activity && d.Activity.length)" class="empty-line">暂无播放活动</div>
        <div v-else>
          <div v-for="(a, i) in d.Activity" :key="i" class="empty-line" style="text-align:left;margin-bottom:8px;display:flex;gap:10px;">
            <span class="tag" :class="{ gray: a.Action !== 'start' }">{{ actName(a.Action) }}</span>
            <span style="flex:1;">{{ a.UserName }} · {{ a.ItemName }}</span>
            <span class="muted" style="font-size:12px;">{{ fmtTime(a.CreatedAt) }}</span>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, reactive, onMounted, onBeforeUnmount } from 'vue'
import { api } from '../../api/client'
import { fmtTime, toast, errText } from './util'

const d = ref({})
const sec = reactive({ playing: true, tasks: true, activity: true })
let timer = null

async function load() {
  try { d.value = await api.admin.dashboard() } catch {}
}
async function clearActs() {
  await api.admin.clearActivity()
  toast('活跃记录已清空')
  load()
}
function taskState(s) {
  return { running: '进行中', done: '完成', error: '异常' }[s] || s
}
function actName(a) {
  return { start: '开始播放', stop: '停止', progress: '进度' }[a] || a
}

onMounted(() => {
  load()
  timer = setInterval(load, 8000)
})
onBeforeUnmount(() => clearInterval(timer))
</script>
