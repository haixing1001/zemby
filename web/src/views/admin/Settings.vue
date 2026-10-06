<template>
  <div>
    <h1 class="adm-h1">设置</h1>
    <div class="adm-desc">服务器基础设置（增强功能）</div>

    <div class="collapse-sec">
      <div class="head">服务器</div>
      <div class="body" style="padding:2px 16px 12px;">
        <div class="set-row" style="flex-direction:column;align-items:stretch;">
          <div><div class="t">服务器名称</div><div class="d">显示在客户端与登录页</div></div>
          <div style="display:flex;gap:10px;margin-top:10px;">
            <input v-model="st.ServerName" style="flex:1;" />
            <button class="btn sm" @click="saveName">保存</button>
          </div>
        </div>
        <table class="tbl" style="margin-top:8px;">
          <tr><td class="muted">版本</td><td>{{ st.Version }}</td></tr>
          <tr><td class="muted">监听地址</td><td>{{ st.Addr }}</td></tr>
          <tr><td class="muted">媒体根目录</td><td>{{ (st.MediaRoots || []).join(' ; ') }}</td></tr>
          <tr><td class="muted">设备租约</td><td>{{ st.DeviceLeaseSeconds }} 秒</td></tr>
          <tr><td class="muted">播放模式</td><td>{{ st.Playback }}</td></tr>
        </table>
      </div>
    </div>

    <div class="collapse-sec">
      <div class="head">修改当前用户密码</div>
      <div class="body" style="padding:2px 16px 16px;">
        <label>原密码</label>
        <input v-model="pw.Old" type="password" style="width:100%;max-width:360px;" />
        <label>新密码</label>
        <input v-model="pw.New" type="password" style="width:100%;max-width:360px;" />
        <div class="toolbar mt">
          <button class="btn" @click="changePw" :disabled="!pw.Old || !pw.New">确认修改</button>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import { api } from '../../api/client'
import { toast, errText } from './util'

const st = ref({})
const pw = ref({ Old: '', New: '' })

onMounted(async () => { st.value = await api.admin.settings() })

async function saveName() {
  try {
    await api.admin.saveSettings({ ServerName: st.value.ServerName })
    toast('服务器名称已保存')
  } catch (e) { toast(errText(e), true) }
}
async function changePw() {
  try {
    await api.admin.changePassword({ Old: pw.value.Old, New: pw.value.New })
    toast('密码已修改')
    pw.value = { Old: '', New: '' }
  } catch (e) { toast(errText(e), true) }
}
</script>
