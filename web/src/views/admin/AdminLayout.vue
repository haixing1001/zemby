<template>
  <div class="adm-shell">
    <!-- 移动端顶栏 -->
    <div class="adm-topbar">
      <button class="btn ghost sm" @click="sideOpen = !sideOpen">☰</button>
      <div class="brand-mini">zemby</div>
    </div>

    <!-- 侧边栏 -->
    <aside class="adm-side" :class="{ open: sideOpen }">
      <div class="adm-nav">
        <router-link to="/" class="adm-item" @click="sideOpen = false">
          <span class="ico" v-html="icons.back"></span>返回影库
        </router-link>
        <router-link to="/admin" class="adm-item" exact-active-class="active" @click="sideOpen = false">
          <span class="ico" v-html="icons.dash"></span>控制台
        </router-link>
        <router-link to="/admin/media" class="adm-item" active-class="active" @click="sideOpen = false">
          <span class="ico" v-html="icons.media"></span>媒体管理
        </router-link>
        <router-link to="/admin/files" class="adm-item" active-class="active" @click="sideOpen = false">
          <span class="ico" v-html="icons.folder"></span>文件管理
        </router-link>
        <router-link to="/admin/scrape" class="adm-item" active-class="active" @click="sideOpen = false">
          <span class="ico" v-html="icons.scrape"></span>刮削管理
        </router-link>
        <router-link to="/admin/users" class="adm-item" active-class="active" @click="sideOpen = false">
          <span class="ico" v-html="icons.users"></span>用户管理
        </router-link>
        <router-link to="/admin/sort" class="adm-item" active-class="active" @click="sideOpen = false">
          <span class="ico" v-html="icons.sort"></span>媒体排序
        </router-link>
        <router-link to="/admin/media-info" class="adm-item" active-class="active" @click="sideOpen = false">
          <span class="ico" v-html="icons.info"></span>媒体信息
        </router-link>
        <router-link to="/admin/logs" class="adm-item" active-class="active" @click="sideOpen = false">
          <span class="ico" v-html="icons.logs"></span>日志管理
        </router-link>

        <div class="adm-group-title">增强功能</div>
        <div class="adm-sub" style="display:flex;align-items:center;" @click="enhOpen = !enhOpen">
          <span style="flex:1;">设置 · 字幕 · TMDB · 更多</span>
          <span :class="{ open: enhOpen }" style="color:#6b7482;">⌄</span>
        </div>
        <template v-if="enhOpen">
          <router-link to="/admin/settings" class="adm-sub" active-class="active">增强功能</router-link>
          <router-link to="/admin/subtitles" class="adm-sub" active-class="active">字幕</router-link>
          <router-link to="/admin/tmdb" class="adm-sub" active-class="active">TMDB</router-link>
          <div class="adm-sub disabled" title="开发中">Bot</div>
          <div class="adm-sub disabled" title="开发中">片头片尾</div>
          <div class="adm-sub disabled" title="开发中">代理</div>
        </template>

        <router-link to="/admin/api" class="adm-item" active-class="active" @click="sideOpen = false">
          <span class="ico" v-html="icons.api"></span>API
        </router-link>
      </div>
      <div class="adm-version">
        <svg viewBox="0 0 24 24" width="18" height="18" fill="none">
          <circle cx="12" cy="12" r="10" stroke="#5b8cff" stroke-width="2"/>
          <path d="M9 8.5v7l6-3.5-6-3.5z" fill="#5b8cff"/>
        </svg>
        <span>{{ version }}</span>
      </div>
    </aside>

    <!-- 内容 -->
    <div class="adm-content">
      <router-view />
    </div>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import { api } from '../../api/client'

const sideOpen = ref(false)
const enhOpen = ref(true)
const version = ref('zemby')

onMounted(async () => {
  try {
    const s = await api.admin.settings()
    version.value = `zemby ${s.Version || ''}`
  } catch {}
})

const icons = {
  back: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M19 12H5M11 18l-6-6 6-6"/></svg>',
  dash: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><circle cx="12" cy="12" r="9"/><path d="M12 12l3.5-3.5"/><path d="M12 7v1"/></svg>',
  media: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><rect x="4" y="4" width="16" height="16" rx="2"/><path d="M4 9h16M9 9v11"/></svg>',
  folder: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v9a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V7z"/></svg>',
  scrape: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 3l1.8 5.2L19 10l-5.2 1.8L12 17l-1.8-5.2L5 10l5.2-1.8L12 3z"/><path d="M19 15l.9 2.6L22.5 18.5l-2.6.9L19 22l-.9-2.6-2.6-.9 2.6-.9L19 15z"/></svg>',
  users: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><circle cx="9" cy="8" r="3.5"/><path d="M2.5 20c.8-3.2 3.4-5 6.5-5s5.7 1.8 6.5 5"/><circle cx="17" cy="9" r="2.5"/><path d="M16 15.2c2.6.3 4.6 1.8 5.4 4.3"/></svg>',
  sort: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M4 6h16M4 12h10M4 18h6"/></svg>',
  info: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><circle cx="12" cy="12" r="9"/><path d="M12 11v5M12 8v.01"/></svg>',
  logs: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><path d="M14 2v6h6M9 13h6M9 17h6"/></svg>',
  api: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M8 6l-6 6 6 6M16 6l6 6-6 6"/></svg>'
}
</script>
