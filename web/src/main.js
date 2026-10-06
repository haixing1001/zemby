import { createApp } from 'vue'
import { createRouter, createWebHistory } from 'vue-router'
import App from './App.vue'
import './assets/style.css'

import Login from './views/Login.vue'
import Home from './views/Home.vue'
import Library from './views/Library.vue'
import Detail from './views/Detail.vue'
import Player from './views/Player.vue'
import Admin from './views/Admin.vue'

const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/login', component: Login },
    { path: '/', component: Home },
    { path: '/library/:id', component: Library },
    { path: '/item/:id', component: Detail },
    { path: '/play/:id', component: Player },
    { path: '/admin', component: Admin }
  ]
})

router.beforeEach((to) => {
  const token = localStorage.getItem('gemby_token')
  if (!token && to.path !== '/login') return '/login'
  if (token && to.path === '/login') return '/'
})

createApp(App).use(router).mount('#app')
