<template>
  <div>
    <h1 class="adm-h1">TMDB 刮削设置</h1>
    <div class="adm-desc">配置元数据与图片来源（增强功能 · 刮削器）</div>

    <div class="collapse-sec">
      <div class="body" style="padding:2px 16px 16px;">
        <label>API Key（v3 auth）{{ tmdb.HasKey ? '（已配置）' : '' }}</label>
        <input v-model="tmdb.APIKey" placeholder="在 themoviedb.org 申请" style="width:100%;max-width:520px;" />
        <label>TMDB API 镜像地址</label>
        <input v-model="tmdb.APIBaseURL" placeholder="https://api.themoviedb.org/3" style="width:100%;max-width:520px;" />
        <label>TMDB 图片镜像地址</label>
        <input v-model="tmdb.ImageBaseURL" placeholder="https://image.tmdb.org/t/p" style="width:100%;max-width:520px;" />
        <p class="muted" style="font-size:12.5px;margin-top:6px;">
          可分别填写兼容 TMDB API 与图片路径的镜像基础地址；留空使用官方地址。API 地址后会追加 /search、/movie 等路径，图片地址后会追加尺寸与图片路径。
        </p>
        <label>元数据语言</label>
        <select v-model="tmdb.Language" style="width:200px;">
          <option value="zh-CN">中文（zh-CN）</option>
          <option value="en-US">English（en-US）</option>
          <option value="ja-JP">日本語（ja-JP）</option>
        </select>
        <label style="display:flex;align-items:center;gap:8px;">
          <input type="checkbox" v-model="tmdb.DownloadImages" /> 下载海报与背景图到服务器
        </label>
        <div class="toolbar mt">
          <button class="btn" @click="save">保存</button>
        </div>
        <p class="muted" style="font-size:12.5px;">
          刮削在新条目扫描后自动进行；带 tmdbid 的 NFO 优先使用 NFO 中的 ID。
          匹配失败的条目可在「刮削管理」中查看失败原因并重试。
        </p>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import { api } from '../../api/client'
import { toast, errText } from './util'

const tmdb = ref({ APIKey: '', Language: 'zh-CN', DownloadImages: true, HasKey: false, APIBaseURL: 'https://api.themoviedb.org/3', ImageBaseURL: 'https://image.tmdb.org/t/p' })

onMounted(async () => {
  try { tmdb.value = { ...tmdb.value, ...(await api.admin.tmdb()) } } catch {}
})
async function save() {
  try {
    await api.admin.saveTmdb({
      APIKey: tmdb.value.APIKey,
      Language: tmdb.value.Language,
      DownloadImages: tmdb.value.DownloadImages,
      APIBaseURL: tmdb.value.APIBaseURL,
      ImageBaseURL: tmdb.value.ImageBaseURL
    })
    toast('TMDB 设置已保存')
    tmdb.value = { ...tmdb.value, ...(await api.admin.tmdb()) }
  } catch (e) { toast(errText(e), true) }
}
</script>
