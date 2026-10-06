<template>
  <div class="app-shell">
    <header v-if="showNav" class="topbar">
      <div class="topbar-inner">
        <router-link to="/" class="brand">
          <svg viewBox="0 0 24 24" width="26" height="26" fill="none">
            <circle cx="12" cy="12" r="10" stroke="#5b8cff" stroke-width="2"/>
            <path d="M9 8.5v7l6-3.5-6-3.5z" fill="#5b8cff"/>
          </svg>
          <span>Go Emby</span>
        </router-link>
        <nav class="nav-links">
          <router-link to="/">首页</router-link>
          <router-link v-if="state.isAdmin" to="/admin">后台管理</router-link>
        </nav>
        <div class="user-area">
          <span class="username">{{ state.userName }}</span>
          <button class="btn ghost sm" @click="doLogout">退出</button>
        </div>
      </div>
    </header>
    <main class="main">
      <router-view />
    </main>
  </div>
</template>

<script setup>
import { computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { state, logout } from './api/client'

const route = useRoute()
const router = useRouter()
const showNav = computed(() => route.path !== '/login')

function doLogout() {
  logout()
  router.push('/login')
}
</script>
