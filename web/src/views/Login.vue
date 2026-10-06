<template>
  <div class="login-wrap">
    <div class="login-card">
      <div class="login-logo">
        <svg viewBox="0 0 24 24" width="52" height="52" fill="none">
          <circle cx="12" cy="12" r="10" stroke="#5b8cff" stroke-width="2"/>
          <path d="M9 8.5v7l6-3.5-6-3.5z" fill="#5b8cff"/>
        </svg>
      </div>
      <h1>Go Emby Server</h1>
      <p class="muted">登录以访问你的媒体库</p>
      <form @submit.prevent="doLogin">
        <label>用户名</label>
        <input v-model="username" autocomplete="username" placeholder="用户名" required />
        <label>密码</label>
        <input v-model="password" type="password" autocomplete="current-password" placeholder="密码" required />
        <div v-if="err" class="login-err">{{ err }}</div>
        <button class="btn login-btn" :disabled="loading">{{ loading ? '登录中…' : '登录' }}</button>
      </form>
    </div>
  </div>
</template>

<script setup>
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import { api, saveAuth, deviceId } from '../api/client'

const username = ref('')
const password = ref('')
const err = ref('')
const loading = ref(false)
const router = useRouter()

async function doLogin() {
  err.value = ''
  loading.value = true
  try {
    const login = await api.login(username.value, password.value, deviceId)
    saveAuth(login)
    router.push('/')
  } catch (e) {
    err.value = e.message
  } finally {
    loading.value = false
  }
}
</script>

<style scoped>
.login-wrap {
  min-height: 100vh; display: flex; align-items: center; justify-content: center;
  background: radial-gradient(1200px 600px at 30% -10%, #1a2740 0%, var(--bg) 60%);
}
.login-card {
  width: 380px; background: var(--bg-2); border: 1px solid var(--border);
  border-radius: 16px; padding: 40px 36px; text-align: center;
}
.login-card h1 { font-size: 22px; margin: 12px 0 4px; }
.login-card form { text-align: left; }
.login-card input { width: 100%; }
.login-btn { width: 100%; margin-top: 22px; }
.login-err { color: var(--danger); font-size: 13px; margin-top: 10px; }
.login-logo { display: flex; justify-content: center; }
</style>
