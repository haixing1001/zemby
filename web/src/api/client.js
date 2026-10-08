// Emby API 客户端
const BASE = '/emby'

export const state = {
  token: localStorage.getItem('gemby_token') || '',
  userId: localStorage.getItem('gemby_userId') || '',
  userName: localStorage.getItem('gemby_userName') || '',
  isAdmin: localStorage.getItem('gemby_isAdmin') === 'true',
  serverId: ''
}

export function saveAuth(login) {
  state.token = login.AccessToken
  state.userId = login.User.Id
  state.userName = login.User.Name
  state.isAdmin = !!login.User.Policy?.IsAdministrator
  state.serverId = login.ServerId || ''
  localStorage.setItem('gemby_token', state.token)
  localStorage.setItem('gemby_userId', state.userId)
  localStorage.setItem('gemby_userName', state.userName)
  localStorage.setItem('gemby_isAdmin', String(state.isAdmin))
}

export function logout() {
  state.token = ''
  state.userId = ''
  state.userName = ''
  state.isAdmin = false
  state.serverId = ''
  localStorage.removeItem('gemby_token')
  localStorage.removeItem('gemby_userId')
  localStorage.removeItem('gemby_userName')
  localStorage.removeItem('gemby_isAdmin')
}

export function imageUrl(itemId, type = 'Primary', maxWidth = 300) {
  if (!itemId) return ''
  return `${BASE}/Items/${itemId}/Images/${type}?MaxWidth=${maxWidth}&api_key=${encodeURIComponent(state.token)}`
}

async function req(method, path, body, raw = false, keepalive = false) {
  const headers = {}
  if (body !== undefined) headers['Content-Type'] = 'application/json'
  if (state.token) headers['X-Emby-Token'] = state.token
  const controller = new AbortController()
  const timeout = setTimeout(() => controller.abort(), 30000)
  try {
    const res = await fetch(`${BASE}${path}`, {
      method,
      headers,
      keepalive,
      signal: controller.signal,
      body: body !== undefined ? JSON.stringify(body) : undefined
    })
    if (res.status === 204) return null
    if (!res.ok) {
      let msg = `HTTP ${res.status}`
      try {
        const j = await res.json()
        msg = j.Message || j.error || msg
      } catch {}
      throw new Error(msg)
    }
    const ct = res.headers.get('content-type') || ''
    if (raw || !ct.includes('json')) return await res.text()
    return await res.json()
  } catch (e) {
    if (e.name === 'AbortError') throw new Error('请求超时，请检查网络后重试')
    throw e
  } finally {
    clearTimeout(timeout)
  }
}

export const api = {
  get: (p) => req('GET', p),
  post: (p, b) => req('POST', p, b ?? {}),
  put: (p, b) => req('PUT', p, b ?? {}),
  del: (p) => req('DELETE', p),

  // 认证
  async login(username, password, deviceId) {
    const headers = { 'Content-Type': 'application/json' }
    const res = await fetch(`${BASE}/Users/AuthenticateByName`, {
      method: 'POST',
      headers: {
        ...headers,
        'X-Emby-Authorization': `MediaBrowser Client="Go Emby Web", Device="Web Browser", DeviceId="${deviceId}", Version="1.0"`
      },
      body: JSON.stringify({ Username: username, Pw: password })
    })
    if (!res.ok) {
      let msg = '登录失败'
      try { const j = await res.json(); msg = j.Message || msg } catch {}
      throw new Error(msg)
    }
    return res.json()
  },

  // 条目
  views: (userId) => api.get(`/Users/${userId}/Views`),
  items: (params) => {
    const q = new URLSearchParams()
    Object.entries(params).forEach(([k, v]) => {
      if (v !== undefined && v !== null && v !== '') q.set(k, v)
    })
    return api.get(`/Items?${q.toString()}`)
  },
  item: (id) => api.get(`/Items/${id}`),
  similarItems: (id, limit = 8) => api.get(`/Items/${id}/Similar?Limit=${limit}`),
  seasons: (seriesId) => api.get(`/Shows/${seriesId}/Seasons`),
  episodes: (seriesId, seasonId) => api.get(`/Shows/${seriesId}/Episodes?SeasonId=${seasonId}`),
  playbackInfo: (id) => api.post(`/Items/${id}/PlaybackInfo`, {}),
  resume: () => api.get(`/Items/Resume?Limit=20`),
  latest: () => api.get(`/Items/Latest?Limit=16`),

  // 播放上报
  playingStart: (itemId, srcId) => api.post('/Sessions/Playing', { ItemId: itemId, MediaSourceId: srcId }),
  playingProgress: (itemId, pos, rt, srcId, paused) =>
    api.post('/Sessions/Playing/Progress', { ItemId: itemId, MediaSourceId: srcId, PositionTicks: Math.floor(pos), RunTimeTicks: rt, IsPaused: paused }),
  playingStopped: (itemId, pos, rt, srcId) =>
    req('POST', '/Sessions/Playing/Stopped', { ItemId: itemId, MediaSourceId: srcId, PositionTicks: Math.floor(pos), RunTimeTicks: rt }, false, true),
  markPlayed: (userId, itemId) => api.post(`/Users/${userId}/PlayedItems/${itemId}`),
  markFavorite: (userId, itemId) => api.post(`/Users/${userId}/FavoriteItems/${itemId}`),

  // 管理
  admin: {
    status: () => api.get('/admin/status'),
    dashboard: () => api.get('/admin/dashboard'),
    clearActivity: () => api.del('/admin/activity'),
    libraries: () => api.get('/admin/libraries'),
    createLibrary: (body) => api.post('/admin/libraries', body),
    patchLibrary: (id, body) => api.post(`/admin/libraries/${id}`, body),
    deleteLibrary: (id) => api.del(`/admin/libraries/${id}`),
    libraryPosterUrl: (id) => `${BASE}/admin/libraries/${id}/poster?api_key=${encodeURIComponent(state.token)}`,
    scan: (id, mode) => api.post('/admin/scan', { ID: id, Mode: mode }),
    scanAll: (mode) => api.post('/admin/scan', { Mode: mode }),
    scanStop: () => api.post('/admin/scan-stop', {}),
    scanStatus: () => api.get('/admin/scan-status'),
    tmdb: () => api.get('/admin/tmdb'),
    saveTmdb: (body) => api.put('/admin/tmdb', body),
    ai: () => api.get('/admin/ai'),
    saveAI: (body) => api.put('/admin/ai', body),
    aiTest: (body) => api.post('/admin/ai/test', body),
    scrapeConfig: () => api.get('/admin/scrape/config'),
    saveScrapeConfig: (b) => api.put('/admin/scrape/config', b),
    scrapeState: () => api.get('/admin/scrape/state'),
    scrapeControl: (action) => api.post('/admin/scrape/control', { Action: action }),
    scrapeFailed: () => api.get('/admin/scrape/failed'),
    scrapeIncomplete: () => api.get('/admin/scrape/incomplete?limit=200'),
    scrapeRetry: () => api.post('/admin/scrape/retry', {}),
    scrapeItem: (body) => api.post('/admin/scrape', body),
    probeConfig: () => api.get('/admin/probe/config'),
    saveProbeConfig: (b) => api.put('/admin/probe/config', b),
    probeStatus: () => api.get('/admin/probe/status'),
    probeBatch: (action) => api.post('/admin/probe/batch', { Action: action }),
    settings: () => api.get('/admin/settings'),
    saveSettings: (b) => api.put('/admin/settings', b),
    enhancements: () => api.get('/admin/enhancements'),
    saveEnhancements: (b) => api.put('/admin/enhancements', b),
    uploadFavoriteCover: (file) => {
      const fd = new FormData()
      fd.append('file', file)
      return fetch(`${BASE}/admin/favorites/cover`, {
        method: 'POST', headers: { 'X-Emby-Token': state.token }, body: fd
      }).then(r => { if (!r.ok) throw new Error('上传失败') ; return r.json() })
    },
    deleteFavoriteCover: () => api.del('/admin/favorites/cover'),
    changePassword: (b) => api.put('/admin/password', b),
    subtitles: () => api.get('/admin/subtitles'),
    apiDocs: () => api.get('/admin/api'),
    apiKeys: () => api.get('/admin/apikeys'),
    createApiKey: (Name) => api.post('/admin/apikeys', { Name }),
    deleteApiKey: (id) => api.del(`/admin/apikeys/${id}`),
    logs: (category, level, limit) => {
      const qp = new URLSearchParams()
      if (category) qp.set('category', category)
      if (level) qp.set('level', level)
      qp.set('limit', String(limit || 500))
      return api.get(`/admin/logs?${qp.toString()}`)
    },
    clearLogs: () => api.del('/admin/logs'),
    logsStreamUrl: () => `${BASE}/admin/logs/stream?api_key=${encodeURIComponent(state.token)}`,
    users: () => api.get('/Users'),
    createUser: (body) => api.post('/Users', body),
    updateUser: (id, body) => api.put(`/Users/${id}`, body),
    deleteUser: (id) => api.del(`/Users/${id}`),
    files: (path) => api.get(`/admin/files?path=${encodeURIComponent(path || '')}`),
    fileOp: (Action, Path, Arg) => api.post('/admin/files/op', { Action, Path, Arg }),
    uploadUrl: (path) => `${BASE}/admin/files/upload?path=${encodeURIComponent(path)}`
  },

  streamUrl(itemId, srcId, container) {
    return `${BASE}/Videos/${itemId}/stream.${container || 'mp4'}?Static=true&MediaSourceId=${srcId}&api_key=${encodeURIComponent(state.token)}`
  },

  subtitleUrl(itemId, index) {
    return `${BASE}/Videos/${itemId}/subtitles/${index}/stream.vtt?api_key=${encodeURIComponent(state.token)}`
  }
}

// 随机设备 ID（每浏览器固定）
const didKey = 'gemby_device_id'
if (!localStorage.getItem(didKey)) {
  localStorage.setItem(didKey, 'web-' + Math.random().toString(36).slice(2, 12))
}
export const deviceId = localStorage.getItem(didKey)
