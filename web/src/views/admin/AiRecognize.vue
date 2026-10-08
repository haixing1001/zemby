<template>
  <div>
    <h1 class="adm-h1">AI 识别辅助</h1>
    <div class="adm-desc">TMDB 识别失败时，自动调用 AI 从文件名 / 目录名提取关键词后重新刮削</div>

    <div class="collapse-sec">
      <div class="body" style="padding:2px 16px 16px;">
        <!-- 启用开关 -->
        <div class="set-row">
          <div>
            <div class="t">启用 AI 识别辅助</div>
            <div class="d">TMDB 使用原片名并尝试有年份、无年份搜索仍无结果时，才调用一次 AI；识别结果中的常用名和外文原名都会用于重搜，避免正常刮削增加 AI 调用。</div>
          </div>
          <label class="switch"><input type="checkbox" v-model="ai.Enabled" @change="save" /><span class="track"></span><span class="knob"></span></label>
        </div>

        <!-- 常用服务商 -->
        <div class="set-row" style="flex-wrap:wrap;gap:12px;">
          <div style="min-width:130px;"><div class="t">常用服务商</div></div>
          <select v-model="preset" style="width:240px;" @change="applyPreset">
            <option value="">自定义 / 手动填写</option>
            <option value="openai">OpenAI</option>
            <option value="deepseek">DeepSeek（深度求索）</option>
            <option value="glm">智谱 GLM</option>
            <option value="kimi">Kimi（月之暗面）</option>
            <option value="ollama">Ollama 本地模型</option>
          </select>
        </div>

        <!-- API 地址 -->
        <div class="set-row" style="flex-wrap:wrap;gap:12px;">
          <div style="min-width:130px;"><div class="t">API 根地址</div></div>
          <input v-model="ai.BaseURL" placeholder="https://api.openai.com/v1" autocomplete="url" style="width:340px;" />
        </div>

        <!-- API Key -->
        <div class="set-row" style="flex-wrap:wrap;gap:12px;">
          <div style="min-width:130px;"><div class="t">API Key</div></div>
          <input v-model="ai.APIKey" type="password" autocomplete="new-password" :placeholder="ai.HasKey ? '已配置（留空保持不变）' : 'sk-...'" style="width:340px;" />
        </div>

        <!-- 模型 -->
        <div class="set-row" style="flex-wrap:wrap;gap:12px;">
          <div style="min-width:130px;"><div class="t">模型名称</div></div>
          <input v-model="ai.Model" placeholder="gpt-4o-mini / deepseek-chat / glm-4-flash" style="width:340px;" />
        </div>

        <div class="toolbar mt">
          <button class="btn" @click="save" :disabled="saving">{{ saving ? '保存中…' : '保存' }}</button>
        </div>
        <p class="muted" style="font-size:12.5px;">
          兼容 OpenAI Chat Completions 风格接口；API 根地址也可填写完整的 /chat/completions 地址。
          请求只发送文件名和最近两级目录，不包含媒体库的宿主机绝对路径。识别日志可在「日志管理 → AI识别」查看。
        </p>
      </div>
    </div>

    <!-- 测试识别 -->
    <div class="collapse-sec">
      <div class="body" style="padding:2px 16px 16px;">
        <div class="t" style="margin:10px 0 4px;">识别测试</div>
        <div class="d" style="margin-bottom:10px;">输入示例文件路径，预览 AI 提取的名称和年份；发送给模型的只有文件名及最近两级目录：</div>
        <div style="display:flex;gap:8px;flex-wrap:wrap;align-items:center;">
          <select v-model="test.Type" style="width:110px;">
            <option value="Movie">电影</option>
            <option value="Series">剧集</option>
          </select>
          <input v-model="test.Path" placeholder="/media/电影/蜘蛛侠.纵横宇宙.2023.1080p.BluRay.mkv" style="flex:1;min-width:280px;" />
          <button class="btn ghost" @click="runTest" :disabled="testing">{{ testing ? '识别中…' : '测试识别' }}</button>
        </div>
        <div v-if="testResult" class="ai-test-result" :class="{ err: testError }">
          <template v-if="!testError">
            <div class="atr-row"><span class="k">识别名称</span><span class="v">{{ testResult.Title }}</span></div>
            <div class="atr-row" v-if="testResult.OriginalTitle"><span class="k">外文原名</span><span class="v">{{ testResult.OriginalTitle }}</span></div>
            <div class="atr-row"><span class="k">识别年份</span><span class="v">{{ testResult.Year || '未识别' }}</span></div>
            <div class="atr-ok">✓ AI 配置可用，刮削失败条目将自动走 AI 辅助重试</div>
          </template>
          <template v-else>{{ testResult }}</template>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import { api } from '../../api/client'
import { toast, errText } from './util'

const PRESETS = {
  openai: { BaseURL: 'https://api.openai.com/v1', Model: 'gpt-4o-mini' },
  deepseek: { BaseURL: 'https://api.deepseek.com/v1', Model: 'deepseek-chat' },
  glm: { BaseURL: 'https://open.bigmodel.cn/api/paas/v4', Model: 'glm-4-flash' },
  kimi: { BaseURL: 'https://api.moonshot.cn/v1', Model: 'moonshot-v1-8k' },
  ollama: { BaseURL: 'http://localhost:11434/v1', Model: 'llama3.1' }
}

const ai = ref({ Enabled: false, BaseURL: '', APIKey: '', HasKey: false, Model: '' })
const preset = ref('')
const saving = ref(false)
const testing = ref(false)
const test = ref({ Type: 'Movie', Path: '' })
const testResult = ref(null)
const testError = ref(false)

onMounted(async () => {
  try { ai.value = { ...ai.value, ...(await api.admin.ai()) } } catch {}
})

function applyPreset() {
  const p = PRESETS[preset.value]
  if (p) {
    ai.value.BaseURL = p.BaseURL
    ai.value.Model = p.Model
  }
}

async function save() {
  saving.value = true
  try {
    await api.admin.saveAI({
      Enabled: ai.value.Enabled,
      BaseURL: ai.value.BaseURL,
      APIKey: ai.value.APIKey,
      Model: ai.value.Model
    })
    toast('AI 识别辅助设置已保存')
    ai.value = { ...ai.value, ...(await api.admin.ai()) }
  } catch (e) { toast(errText(e), true) }
  saving.value = false
}

async function runTest() {
  testing.value = true
  testResult.value = null
  try {
    testResult.value = await api.admin.aiTest({ Type: test.value.Type, Path: test.value.Path })
    testError.value = false
  } catch (e) {
    testResult.value = errText(e)
    testError.value = true
  }
  testing.value = false
}
</script>

<style scoped>
.ai-test-result {
  margin-top: 12px;
  padding: 12px 14px;
  border-radius: 10px;
  background: #12151b;
  border: 1px solid #2a3040;
  font-size: 13px;
}
.ai-test-result.err { color: #f0616d; border-color: #5a2c33; }
.atr-row { display: flex; gap: 10px; margin: 4px 0; }
.atr-row .k { color: #6b7482; min-width: 64px; }
.atr-row .v { color: #e8eaed; font-weight: 600; word-break: break-all; }
.atr-ok { margin-top: 8px; color: #58c48d; font-size: 12.5px; }
</style>
