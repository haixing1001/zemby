<template>
  <div>
    <div style="display:flex;align-items:center;gap:10px;margin-bottom:14px;">
      <div><h1 class="adm-h1">用户管理</h1><div class="adm-desc">账号、权限与设备数量控制</div></div>
      <span style="flex:1"></span>
      <button class="btn" @click="showAddUser = true">＋ 添加用户</button>
    </div>

    <div class="collapse-sec">
      <div class="body" style="padding:0;">
        <table class="tbl" style="margin:0;">
          <thead><tr><th>用户名</th><th>角色</th><th>播放权限</th><th>设备上限</th><th>操作</th></tr></thead>
          <tbody>
            <tr v-for="u in users" :key="u.Id">
              <td><b>{{ u.Name }}</b></td>
              <td>{{ u.Policy?.IsAdministrator ? '管理员' : '普通用户' }}</td>
              <td>
                <button class="btn sm" :class="u.Policy?.EnableMediaPlayback ? 'green' : 'ghost'"
                  @click="togglePlay(u)">{{ u.Policy?.EnableMediaPlayback ? '允许' : '禁止' }}</button>
              </td>
              <td>
                <input type="number" min="1" max="100" style="width:70px;"
                  :value="u.Policy?.SimultaneousStreamLimit"
                  @change="setMaxDevices(u, $event.target.value)" />
              </td>
              <td>
                <button class="btn ghost sm" @click="resetPw(u)">改密</button>
                <button v-if="!u.FirstAdminHint" class="btn danger sm" style="margin-left:6px;" @click="removeUser(u)">删除</button>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>

    <div class="collapse-sec" v-if="showAddUser" style="border-color:var(--accent);">
      <div class="head">添加用户</div>
      <div class="body">
        <label>用户名</label>
        <input v-model="newUser.Name" style="width:100%;" />
        <label>密码</label>
        <input v-model="newUser.Password" type="password" style="width:100%;" />
        <label style="display:flex;align-items:center;gap:8px;">
          <input type="checkbox" v-model="newUser.IsAdmin" /> 设为管理员
        </label>
        <label>同时播放设备上限</label>
        <input v-model.number="newUser.MaxDevices" type="number" min="1" max="100" style="width:100px;" />
        <div class="toolbar mt">
          <button class="btn" @click="createUser" :disabled="!newUser.Name">创建</button>
          <button class="btn ghost" @click="showAddUser = false">取消</button>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import { api } from '../../api/client'
import { toast, errText } from './util'

const users = ref([])
const showAddUser = ref(false)
const newUser = ref({ Name: '', Password: '', IsAdmin: false, MaxDevices: 2 })

async function loadUsers() { users.value = await api.admin.users() }
async function togglePlay(u) {
  await api.admin.updateUser(u.Id, { AllowPlayback: !u.Policy.EnableMediaPlayback })
  loadUsers()
}
async function setMaxDevices(u, v) {
  const n = parseInt(v)
  if (!n || n < 1) return
  await api.admin.updateUser(u.Id, { MaxDevices: n })
  toast(`设备上限已设为 ${n}`)
}
async function resetPw(u) {
  const pw = prompt(`为用户 ${u.Name} 设置新密码`)
  if (!pw) return
  await api.admin.updateUser(u.Id, { Password: pw })
  toast('密码已更新')
}
async function removeUser(u) {
  if (!confirm(`删除用户 ${u.Name}？`)) return
  await api.admin.deleteUser(u.Id)
  loadUsers()
}
async function createUser() {
  try {
    await api.admin.createUser(newUser.value)
    toast('用户已创建')
    showAddUser.value = false
    newUser.value = { Name: '', Password: '', IsAdmin: false, MaxDevices: 2 }
    loadUsers()
  } catch (e) { toast(errText(e), true) }
}

onMounted(loadUsers)
</script>
