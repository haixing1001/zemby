import { createApp } from 'vue'
import { createRouter, createWebHistory } from 'vue-router'
import App from './App.vue'
import './assets/style.css'

import Login from './views/Login.vue'
import Home from './views/Home.vue'
import Library from './views/Library.vue'
import Detail from './views/Detail.vue'
import Player from './views/Player.vue'
import AdminLayout from './views/admin/AdminLayout.vue'
import Dashboard from './views/admin/Dashboard.vue'
import MediaManage from './views/admin/MediaManage.vue'
import FileManager from './views/admin/FileManager.vue'
import ScrapeManage from './views/admin/ScrapeManage.vue'
import UserManage from './views/admin/UserManage.vue'
import MediaSort from './views/admin/MediaSort.vue'
import MediaInfo from './views/admin/MediaInfo.vue'
import Settings from './views/admin/Settings.vue'
import Subtitles from './views/admin/Subtitles.vue'
import Tmdb from './views/admin/Tmdb.vue'
import ApiDocs from './views/admin/ApiDocs.vue'
import LogManage from './views/admin/LogManage.vue'

const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/login', component: Login },
    { path: '/', component: Home },
    { path: '/library/:id', component: Library },
    { path: '/item/:id', component: Detail },
    { path: '/play/:id', component: Player },
    {
      path: '/admin', component: AdminLayout, children: [
        { path: '', component: Dashboard },
        { path: 'media', component: MediaManage },
        { path: 'files', component: FileManager },
        { path: 'scrape', component: ScrapeManage },
        { path: 'users', component: UserManage },
        { path: 'sort', component: MediaSort },
        { path: 'media-info', component: MediaInfo },
        { path: 'logs', component: LogManage },
        { path: 'settings', component: Settings },
        { path: 'subtitles', component: Subtitles },
        { path: 'tmdb', component: Tmdb },
        { path: 'api', component: ApiDocs }
      ]
    }
  ]
})

router.beforeEach((to) => {
  const token = localStorage.getItem('gemby_token')
  const admin = localStorage.getItem('gemby_isAdmin') === 'true'
  if (!token && to.path !== '/login') return '/login'
  if (token && to.path === '/login') return '/'
  if (to.path.startsWith('/admin') && !admin) return '/'
})

createApp(App).use(router).mount('#app')
