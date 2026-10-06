<template>
  <div>
    <h1 class="adm-h1">增强功能</h1>
    <div class="adm-desc">服务器显示与行为增强设置，保存后立即生效</div>

    <div class="collapse-sec">
      <div class="body" style="padding:2px 16px 12px;">
        <!-- 服务器显示名称 -->
        <div class="set-row" style="flex-wrap:wrap;gap:12px;">
          <div style="min-width:130px;"><div class="t">服务器显示名称</div></div>
          <input v-model="st.ServerName" style="width:220px;" @change="saveName" />
        </div>

        <!-- 开启收藏功能 -->
        <div class="set-row">
          <div>
            <div class="t">开启收藏功能</div>
            <div class="d">在“我的媒体库”显示播放收藏；关闭不会删除用户收藏数据。</div>
          </div>
          <div style="display:flex;align-items:center;gap:10px;">
            <template v-if="enh.Favorites">
              <button class="btn ghost sm" @click="coverInput && coverInput.click()">上传封面</button>
              <button class="btn ghost sm" @click="removeCover" :disabled="!enh.HasFavoriteCover">移除封面</button>
            </template>
            <label class="switch"><input type="checkbox" v-model="enh.Favorites" @change="save('Favorites')" /><span class="track"></span><span class="knob"></span></label>
          </div>
          <input ref="coverInput" type="file" accept="image/*" style="display:none;" @change="uploadCover" />
        </div>

        <!-- 启动TMDB -->
        <div class="set-row">
          <div>
            <div class="t">启动TMDB</div>
            <div class="d">开启后可显示无元数据的影视库的海报墙及元数据</div>
          </div>
          <label class="switch"><input type="checkbox" v-model="enh.TMDB" @change="save('TMDB')" /><span class="track"></span><span class="knob"></span></label>
        </div>

        <!-- 按发行日期排序媒体库 -->
        <div class="set-row">
          <div>
            <div class="t">按发行日期排序媒体库</div>
            <div class="d">开启后影库首页按发行日期从新到旧显示；无发行日期的内容排在最后。关闭时仍按最新入库时间排序。</div>
          </div>
          <label class="switch"><input type="checkbox" v-model="enh.SortByReleaseDate" @change="save('SortByReleaseDate')" /><span class="track"></span><span class="knob"></span></label>
        </div>

        <!-- 剧集媒体信息复用 -->
        <div class="set-row">
          <div>
            <div class="t">剧集媒体信息复用</div>
            <div class="d">电视剧相同季的剧集媒体信息复用</div>
          </div>
          <label class="switch"><input type="checkbox" v-model="enh.EpisodeMediaReuse" @change="save('EpisodeMediaReuse')" /><span class="track"></span><span class="knob"></span></label>
        </div>

        <!-- 海报显示剧集集数角标 -->
        <div class="set-row">
          <div>
            <div class="t">海报显示剧集集数角标</div>
            <div class="d">Web 端显示总集数，客户端继续使用标准 Emby 集数信息。</div>
          </div>
          <label class="switch"><input type="checkbox" v-model="enh.PosterEpisodeBadge" @change="save('PosterEpisodeBadge')" /><span class="track"></span><span class="knob"></span></label>
        </div>

        <!-- 隐藏没有图片的演员信息 -->
        <div class="set-row">
          <div>
            <div class="t">隐藏没有图片的演员信息</div>
            <div class="d">隐藏无头像的演员和导演等演职人员，不删除人物元数据。</div>
          </div>
          <label class="switch"><input type="checkbox" v-model="enh.HideActorsNoImage" @change="save('HideActorsNoImage')" /><span class="track"></span><span class="knob"></span></label>
        </div>

        <!-- 同媒体库内多版本合并 -->
        <div class="set-row">
          <div>
            <div class="t">同媒体库内多版本合并</div>
            <div class="d">在同一个媒体库内合并识别为同一影片/剧集的多个版本，无需位于同一文件夹。</div>
          </div>
          <label class="switch"><input type="checkbox" v-model="enh.MergeVersionsInLibrary" @change="save('MergeVersionsInLibrary')" /><span class="track"></span><span class="knob"></span></label>
        </div>

        <!-- 跨媒体库合并多版本 -->
        <div class="set-row">
          <div>
            <div class="t">跨媒体库合并多版本</div>
            <div class="d">允许跨不同媒体库合并同一影片/剧集的多个版本；两个合并开关同时开启时，以跨媒体库规则为准。</div>
          </div>
          <label class="switch"><input type="checkbox" v-model="enh.MergeVersionsAcrossLibraries" @change="save('MergeVersionsAcrossLibraries')" /><span class="track"></span><span class="knob"></span></label>
        </div>

        <!-- 按照首字母搜索视频 -->
        <div class="set-row">
          <div>
            <div class="t">按照首字母搜索视频</div>
            <div class="d">支持中文片名和部分拼音首字母匹配。</div>
          </div>
          <label class="switch"><input type="checkbox" v-model="enh.SearchByInitials" @change="save('SearchByInitials')" /><span class="track"></span><span class="knob"></span></label>
        </div>

        <!-- 监听文件变动自动刷新路径 -->
        <div class="set-row">
          <div>
            <div class="t">监听文件变动自动刷新路径</div>
            <div class="d">文件变化合并后刷新路径；与刮削实时监控共用任务队列。</div>
          </div>
          <label class="switch"><input type="checkbox" v-model="enh.WatchEnabled" @change="save('WatchEnabled')" /><span class="track"></span><span class="knob"></span></label>
        </div>

        <!-- 媒体变动延时 -->
        <div class="set-row" style="flex-wrap:wrap;gap:12px;">
          <div style="min-width:130px;"><div class="t">媒体变动延时（秒）</div></div>
          <input v-model.number="enh.WatchDelaySeconds" style="width:120px;" @change="saveNumber('WatchDelaySeconds', 10, 86400, 30)" />
        </div>

        <!-- 播放模式分组 -->
        <div class="pm-box">
          <div class="pm-title">播放模式</div>
          <div class="set-row" style="padding-left:0;">
            <div>
              <div class="t">快速路径</div>
              <div class="d">限时解析 STRM 重定向，失败回退原始 STRM；不改变普通播放。</div>
            </div>
            <label class="switch"><input type="checkbox" v-model="enh.FastPath" @change="save('FastPath')" /><span class="track"></span><span class="knob"></span></label>
          </div>
          <div class="set-row" style="flex-wrap:wrap;gap:12px;border:none;padding-left:0;">
            <div style="min-width:130px;"><div class="t">等待上限（秒）</div></div>
            <input v-model.number="enh.FastPathWaitSec" style="width:120px;" @change="saveNumber('FastPathWaitSec', 1, 60, 5)" />
          </div>
        </div>
      </div>
    </div>

    <div class="collapse-sec">
      <div class="head">服务器</div>
      <div class="body" style="padding:2px 16px 12px;">
        <table class="tbl">
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
const enh = ref({
  Favorites: false, TMDB: false, SortByReleaseDate: false, EpisodeMediaReuse: false,
  PosterEpisodeBadge: true, HideActorsNoImage: true,
  MergeVersionsInLibrary: true, MergeVersionsAcrossLibraries: true,
  SearchByInitials: true, WatchEnabled: false, WatchDelaySeconds: 30,
  FastPath: false, FastPathWaitSec: 5, HasFavoriteCover: false
})
const pw = ref({ Old: '', New: '' })
const coverInput = ref(null)
const saving = ref(false)

onMounted(async () => {
  st.value = await api.admin.settings().catch(() => ({}))
  try {
    const e = await api.admin.enhancements()
    enh.value = { ...enh.value, ...e }
  } catch {}
})

async function save(key) {
  if (saving.value) return
  saving.value = true
  try {
    await api.admin.saveEnhancements({ [key]: enh.value[key] })
    toast('已保存')
  } catch (e) { toast(errText(e), true) } finally { saving.value = false }
}

async function saveNumber(key, min, max, def) {
  let v = Number(enh.value[key])
  if (!Number.isFinite(v) || v < min || v > max) {
    toast(`取值范围 ${min}–${max}，已恢复默认 ${def}`, true)
    v = def
    enh.value[key] = def
  }
  await save(key)
}

async function saveName() {
  try {
    await api.admin.saveSettings({ ServerName: st.value.ServerName })
    toast('服务器名称已保存')
  } catch (e) { toast(errText(e), true) }
}

async function uploadCover(ev) {
  const f = ev.target.files && ev.target.files[0]
  ev.target.value = ''
  if (!f) return
  try {
    await api.admin.uploadFavoriteCover(f)
    enh.value.HasFavoriteCover = true
    toast('收藏封面已上传')
  } catch (e) { toast(errText(e), true) }
}

async function removeCover() {
  try {
    await api.admin.deleteFavoriteCover()
    enh.value.HasFavoriteCover = false
    toast('收藏封面已移除')
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

<style scoped>
.pm-box {
  border: 1px solid rgba(255,255,255,.09);
  border-radius: 12px;
  padding: 14px 16px 6px;
  margin: 14px 0 6px;
  background: rgba(255,255,255,.02);
}
.pm-title { font-weight: 600; font-size: 14.5px; margin-bottom: 8px; }
.set-row { border-bottom: 1px solid rgba(255,255,255,.05); }
.set-row:last-child { border-bottom: none; }
</style>
