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

        <!-- 当前使用供应商 -->
        <div class="set-row" style="flex-wrap:wrap;gap:12px;">
          <div style="min-width:130px;">
            <div class="t">当前使用的供应商</div>
            <div class="d">刮削时调用哪个供应商由这里决定，可随时切换</div>
          </div>
          <select v-model="ai.ActiveID" style="width:280px;" @change="save" :disabled="!ai.Providers.length">
            <option v-for="p in ai.Providers" :key="p.ID" :value="p.ID">{{ p.Name }}（{{ p.Model || '未配置模型' }}）</option>
          </select>
          <span v-if="!ai.Providers.length" class="muted" style="font-size:12px;">尚未添加供应商，请在下方新增并保存</span>
        </div>
      </div>
    </div>

    <!-- 供应商列表 -->
    <div class="collapse-sec">
      <div class="body" style="padding:2px 16px 16px;">
        <div style="display:flex;align-items:flex-start;justify-content:space-between;gap:12px;margin:10px 0 4px;">
          <div>
            <div class="t">AI 供应商</div>
            <div class="d">可保存多个 OpenAI 兼容供应商；密钥保存后不再回显明文，留空即保持不变。</div>
          </div>
          <button class="btn" @click="startAdd" :disabled="!!editing || ai.Providers.length >= 20">+ 新增供应商</button>
        </div>

        <div v-if="!ai.Providers.length && !editing" class="muted" style="padding:14px 0;font-size:13px;">
          还没有供应商，点击「新增供应商」添加第一个（OpenAI / DeepSeek / 智谱 GLM / Kimi / Ollama 等）。
        </div>

        <div v-for="p in ai.Providers" :key="p.ID" class="prov-row" :class="{ active: p.ID === ai.ActiveID }">
          <label class="prov-use" :title="p.ID === ai.ActiveID ? '当前使用中' : '设为当前使用'">
            <input type="radio" name="activeProvider" :value="p.ID" v-model="ai.ActiveID" @change="save" />
            <span>使用</span>
          </label>
          <div class="prov-info">
            <div class="prov-name">
              {{ p.Name }}
              <span v-if="p.ID === ai.ActiveID" class="prov-tag">当前使用</span>
            </div>
            <div class="prov-meta">
              {{ p.Model || '未配置模型' }} · {{ p.BaseURL || '未配置地址' }} · <span :class="p.HasKey ? 'ok' : 'bad'">{{ p.HasKey ? '密钥已配置' : '密钥未配置' }}</span>
            </div>
          </div>
          <div class="prov-acts">
            <button class="btn ghost" @click="runTest(p)" :disabled="testing">{{ testing && testingID === p.ID ? '识别中…' : '测试' }}</button>
            <button class="btn ghost" @click="startEdit(p)" :disabled="!!editing">编辑</button>
            <button class="btn ghost danger" @click="removeProvider(p)" :disabled="!!editing">删除</button>
          </div>
        </div>

        <!-- 新增 / 编辑表单 -->
        <div v-if="editing" class="prov-editor">
          <div class="t" style="margin-bottom:10px;">{{ editing.isNew ? '新增供应商' : '编辑供应商：' + editing.Name }}</div>

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

          <div class="set-row" style="flex-wrap:wrap;gap:12px;">
            <div style="min-width:130px;"><div class="t">供应商名称</div></div>
            <input v-model="editing.Name" placeholder="如 DeepSeek 主力 / 本地 Ollama" style="width:340px;" />
          </div>

          <div class="set-row" style="flex-wrap:wrap;gap:12px;">
            <div style="min-width:130px;"><div class="t">API 根地址</div></div>
            <input v-model="editing.BaseURL" placeholder="https://api.openai.com/v1" autocomplete="url" style="width:340px;" />
          </div>

          <div class="set-row" style="flex-wrap:wrap;gap:12px;">
            <div style="min-width:130px;"><div class="t">API Key</div></div>
            <input v-model="editing.APIKey" type="password" autocomplete="new-password" :placeholder="editing.HasKey ? '已配置（留空保持不变）' : 'sk-...'" style="width:340px;" />
          </div>

          <div class="set-row" style="flex-wrap:wrap;gap:12px;">
            <div style="min-width:130px;"><div class="t">模型名称</div></div>
            <input v-model="editing.Model" placeholder="gpt-4o-mini / deepseek-chat / glm-4-flash" style="width:340px;" />
          </div>

          <div class="set-row" style="flex-wrap:wrap;gap:12px;">
            <div style="min-width:130px;"><div class="t">最大输出 tokens</div></div>
            <input v-model.number="editing.MaxTokens" type="number" min="64" max="4096" step="64" style="width:180px;" />
            <span class="muted" style="font-size:12px;">默认 512；若响应被截断，可调高此值并确认模型服务端允许相应输出长度。</span>
          </div>

          <div class="toolbar mt">
            <button class="btn" @click="applyEdit" :disabled="saving">{{ saving ? '保存中…' : '保存供应商' }}</button>
            <button class="btn ghost" @click="cancelEdit" :disabled="saving">取消</button>
          </div>
        </div>

        <p class="muted" style="font-size:12.5px;margin-top:12px;">
          兼容 OpenAI Chat Completions 风格接口；API 根地址也可填写完整的 /chat/completions 地址。
          请求只发送文件名和最近两级目录，不包含媒体库的宿主机绝对路径。识别日志可在「日志管理 → AI识别」查看。
        </p>
      </div>
    </div>

    <!-- 测试识别 -->
    <div class="collapse-sec">
      <div class="body" style="padding:2px 16px 16px;">
        <div class="t" style="margin:10px 0 4px;">识别测试</div>
        <div class="d" style="margin-bottom:10px;">输入示例文件路径，预览 AI 提取的名称和年份；不填供应商时使用「当前使用的供应商」，也可点击列表中单个供应商的「测试」按钮：</div>
        <div style="display:flex;gap:8px;flex-wrap:wrap;align-items:center;">
          <select v-model="test.Type" style="width:110px;">
            <option value="Movie">电影</option>
            <option value="Series">剧集</option>
          </select>
          <input v-model="test.Path" placeholder="/media/电影/蜘蛛侠.纵横宇宙.2023.1080p.BluRay.mkv" style="flex:1;min-width:280px;" />
          <button class="btn ghost" @click="runTest()" :disabled="testing">{{ testing && !testingID ? '识别中…' : '测试当前供应商' }}</button>
        </div>
        <div v-if="testResult" class="ai-test-result" :class="{ err: testError }">
          <template v-if="!testError">
            <div v-if="testResult.Provider" class="atr-row"><span class="k">测试供应商</span><span class="v">{{ testResult.Provider }}</span></div>
            <div class="atr-row"><span class="k">识别名称</span><span class="v">{{ testResult.Title }}</span></div>
            <div class="atr-row" v-if="testResult.OriginalTitle"><span class="k">外文原名</span><span class="v">{{ testResult.OriginalTitle }}</span></div>
            <div class="atr-row"><span class="k">识别年份</span><span class="v">{{ testResult.Year || '未识别' }}</span></div>
            <div class="atr-row" v-if="testResult.Type"><span class="k">视频类型</span><span class="v">{{ testResult.Type }}</span></div>
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
  openai: { label: 'OpenAI', BaseURL: 'https://api.openai.com/v1', Model: 'gpt-4o-mini' },
  deepseek: { label: 'DeepSeek', BaseURL: 'https://api.deepseek.com/v1', Model: 'deepseek-chat' },
  glm: { label: '智谱 GLM', BaseURL: 'https://open.bigmodel.cn/api/paas/v4', Model: 'glm-4-flash' },
  kimi: { label: 'Kimi', BaseURL: 'https://api.moonshot.cn/v1', Model: 'moonshot-v1-8k' },
  ollama: { label: 'Ollama', BaseURL: 'http://localhost:11434/v1', Model: 'llama3.1' }
}

const ai = ref({ Enabled: false, ActiveID: '', Providers: [] })
const editing = ref(null)
const preset = ref('')
const saving = ref(false)
const testing = ref(false)
const testingID = ref('')
const test = ref({ Type: 'Movie', Path: '' })
const testResult = ref(null)
const testError = ref(false)

onMounted(async () => {
  try {
    const fresh = await api.admin.ai()
    ai.value = { Enabled: !!fresh.Enabled, ActiveID: fresh.ActiveID || '', Providers: fresh.Providers || [] }
  } catch {}
})

function startAdd() {
  editing.value = { ID: '', Name: '', BaseURL: '', APIKey: '', Model: '', MaxTokens: 512, HasKey: false, isNew: true }
  preset.value = ''
}

function startEdit(p) {
  editing.value = { ...p, APIKey: '', isNew: false }
  preset.value = ''
}

function cancelEdit() {
  editing.value = null
  preset.value = ''
}

function applyPreset() {
  const p = PRESETS[preset.value]
  if (p && editing.value) {
    editing.value.BaseURL = p.BaseURL
    editing.value.Model = p.Model
    if (!editing.value.Name) editing.value.Name = p.label
  }
}

function applyEdit() {
  const e = editing.value
  if (!e) return
  if (!e.BaseURL.trim() || !e.Model.trim()) {
    toast('请填写 API 根地址与模型名称', true)
    return
  }
  if (!e.HasKey && !e.APIKey.trim()) {
    toast('请填写 API Key', true)
    return
  }
  const item = {
    ID: e.ID,
    Name: e.Name.trim(),
    BaseURL: e.BaseURL.trim(),
    APIKey: e.APIKey.trim(),
    Model: e.Model.trim(),
    MaxTokens: Number(e.MaxTokens) || 512,
    HasKey: e.HasKey || !!e.APIKey.trim()
  }
  const idx = ai.value.Providers.findIndex(p => p.ID === e.ID)
  if (idx >= 0) {
    const cur = ai.value.Providers[idx]
    ai.value.Providers.splice(idx, 1, { ...cur, ...item, APIKey: item.APIKey || cur.APIKey })
  } else {
    item.ID = 'new-' + Date.now()
    ai.value.Providers.push(item)
    if (!ai.value.ActiveID) ai.value.ActiveID = item.ID
  }
  editing.value = null
  preset.value = ''
  save()
}

async function removeProvider(p) {
  if (!confirm(`删除供应商「${p.Name}」？`)) return
  ai.value.Providers = ai.value.Providers.filter(x => x.ID !== p.ID)
  if (ai.value.ActiveID === p.ID) ai.value.ActiveID = ai.value.Providers[0]?.ID || ''
  await save()
}

async function save() {
  saving.value = true
  try {
    await api.admin.saveAI({
      Enabled: ai.value.Enabled,
      ActiveID: ai.value.ActiveID || '',
      Providers: ai.value.Providers.map(p => ({
        ID: p.ID,
        Name: p.Name,
        BaseURL: p.BaseURL,
        APIKey: p.APIKey || '',
        Model: p.Model,
        MaxTokens: Number(p.MaxTokens) || 512
      }))
    })
    toast('AI 识别辅助设置已保存')
    const fresh = await api.admin.ai()
    ai.value = { Enabled: !!fresh.Enabled, ActiveID: fresh.ActiveID || '', Providers: fresh.Providers || [] }
  } catch (e) {
    toast(errText(e), true)
    try {
      const fresh = await api.admin.ai()
      ai.value = { Enabled: !!fresh.Enabled, ActiveID: fresh.ActiveID || '', Providers: fresh.Providers || [] }
    } catch {}
  }
  saving.value = false
}

async function runTest(provider) {
  testing.value = true
  testingID.value = provider ? provider.ID : ''
  testResult.value = null
  try {
    testResult.value = await api.admin.aiTest({
      Type: test.value.Type,
      Path: test.value.Path,
      ProviderID: provider ? provider.ID : ''
    })
    testError.value = false
  } catch (e) {
    testResult.value = errText(e)
    testError.value = true
  }
  testing.value = false
  testingID.value = ''
}
</script>

<style scoped>
.prov-row {
  display: flex;
  align-items: center;
  gap: 14px;
  padding: 10px 12px;
  margin: 8px 0;
  border: 1px solid #232a36;
  border-radius: 10px;
  background: #12151b;
}
.prov-row.active { border-color: #3d5a80; }
.prov-use { display: flex; align-items: center; gap: 5px; font-size: 12.5px; color: #8b95a5; cursor: pointer; white-space: nowrap; }
.prov-use input { accent-color: #4a7dbe; }
.prov-info { flex: 1; min-width: 0; }
.prov-name { color: #e8eaed; font-weight: 600; font-size: 13.5px; display: flex; align-items: center; gap: 8px; }
.prov-tag { font-size: 11px; font-weight: 500; color: #7db4e8; background: #1d3250; border-radius: 4px; padding: 1px 6px; }
.prov-meta { color: #6b7482; font-size: 12px; margin-top: 2px; word-break: break-all; }
.prov-meta .ok { color: #58c48d; }
.prov-meta .bad { color: #f0616d; }
.prov-acts { display: flex; gap: 6px; flex-shrink: 0; }
.prov-editor {
  margin-top: 14px;
  padding: 14px 14px 4px;
  border: 1px solid #2a3040;
  border-radius: 10px;
  background: #10131a;
}
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
