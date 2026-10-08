const state = { libraries: [], jobs: [], selectedLibrary: null, offset: 0, total: 0, requestSerial: 0, refreshTimer: null, toastTimer: null };
const $ = (selector) => document.querySelector(selector);
const number = new Intl.NumberFormat('zh-CN');
const audioExtensions = new Set(['.mp3', '.flac', '.aac', '.m4a', '.wav', '.opus']);

function kindLabel(kind) { return ({ movies: '电影', series: '剧集', music: '音乐' })[kind] || '媒体'; }
function kindIcon(kind) { return ({ movies: '▤', series: '▦', music: '♫' })[kind] || '▤'; }

async function api(path, options = {}) {
  const response = await fetch(path, { credentials: 'same-origin', ...options, headers: { ...(options.body ? { 'Content-Type': 'application/json' } : {}), ...options.headers } });
  if (response.status === 401) showLogin();
  if (response.status === 204) return null;
  const data = await response.json().catch(() => ({}));
  if (!response.ok) throw new Error(data.error || `请求失败（${response.status}）`);
  return data;
}

function showLogin() {
  $('#login-view').hidden = false;
  $('#app-view').hidden = true;
  if (state.refreshTimer) clearTimeout(state.refreshTimer);
  state.refreshTimer = null;
}

function showApp() {
  $('#login-view').hidden = true;
  $('#app-view').hidden = false;
}

function showToast(message, error = false) {
  const toast = $('#toast');
  toast.textContent = message;
  toast.classList.toggle('is-error', error);
  toast.classList.add('is-visible');
  clearTimeout(state.toastTimer);
  state.toastTimer = setTimeout(() => toast.classList.remove('is-visible'), 2800);
}

function escapeHTML(value) {
  return String(value).replace(/[&<>"']/g, (char) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[char]);
}

function formatBytes(value) {
  if (!value) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  const index = Math.min(Math.floor(Math.log(value) / Math.log(1024)), units.length - 1);
  return `${(value / (1024 ** index)).toFixed(index === 0 ? 0 : 1)} ${units[index]}`;
}

function libraryCard(library) {
  return `<button class="library-card" data-library="${library.id}"><span class="library-card-icon">${kindIcon(library.kind)}</span><span class="library-card-copy"><strong>${escapeHTML(library.name)}</strong><small>${number.format(library.itemCount)} 个媒体文件 · ${kindLabel(library.kind)}</small></span><span class="card-arrow">→</span></button>`;
}

function renderLibraries() {
  $('#stat-libraries').textContent = number.format(state.libraries.length);
  $('#stat-items').textContent = number.format(state.libraries.reduce((sum, library) => sum + library.itemCount, 0));
  $('#home-libraries').innerHTML = state.libraries.map(libraryCard).join('');
  $('#empty-libraries').hidden = state.libraries.length > 0;
  $('#library-rail-items').innerHTML = state.libraries.map((library) => `<button class="rail-item ${state.selectedLibrary?.id === library.id ? 'is-selected' : ''}" data-library="${library.id}"><span class="rail-icon">${kindIcon(library.kind)}</span><span>${escapeHTML(library.name)}</span><small>${number.format(library.itemCount)}</small></button>`).join('');
  document.querySelectorAll('[data-library]').forEach((button) => button.addEventListener('click', () => selectLibrary(Number(button.dataset.library))));
}

function renderJobs() {
  const active = state.jobs.filter((job) => job.state === 'pending' || job.state === 'running').length;
  $('#stat-jobs').textContent = number.format(active);
  $('#jobs-caption').textContent = active ? `${active} 个任务处理中` : '后台任务状态';
  const labels = { pending: '排队中', running: '扫描中', done: '已完成', failed: '失败' };
  const jobs = state.jobs.slice(0, 6);
  $('#recent-jobs').innerHTML = jobs.length ? jobs.map((job) => `<div class="job-row"><span class="job-state state-${job.state}"><i></i>${labels[job.state] || job.state}</span><strong>${escapeHTML(job.library)}</strong><span class="job-count">${number.format(job.scanned)} 个文件</span><span class="job-time">${job.endedAt ? new Date(job.endedAt).toLocaleString() : new Date(job.createdAt).toLocaleTimeString()}</span>${job.error ? `<span class="job-error">${escapeHTML(job.error)}</span>` : ''}</div>`).join('') : '<div class="jobs-empty">还没有扫描任务</div>';
  if (active && !state.refreshTimer) state.refreshTimer = setTimeout(async () => { state.refreshTimer = null; await refreshData(); }, 2500);
}

async function refreshData(reloadSelected = false) {
  const previousJobs = new Map(state.jobs.map((job) => [job.id, job.state]));
  try {
    const [libraries, jobs] = await Promise.all([api('/api/libraries'), api('/api/jobs')]);
    state.libraries = libraries.items;
    state.jobs = jobs.items;
    if (state.selectedLibrary) state.selectedLibrary = state.libraries.find((library) => library.id === state.selectedLibrary.id) || null;
    renderLibraries();
    renderJobs();
    const selectedScanFinished = state.selectedLibrary && state.jobs.some((job) => job.libraryId === state.selectedLibrary.id && job.state === 'done' && ['pending', 'running'].includes(previousJobs.get(job.id)));
    if (state.selectedLibrary && (reloadSelected || selectedScanFinished)) await loadItems(true);
  } catch (error) {
    if ($('#app-view').hidden) return;
    showToast(error.message, true);
  }
}

function setView(view) {
  const libraryView = view === 'library';
  $('#home-view').hidden = libraryView;
  $('#library-view').hidden = !libraryView;
  $('#page-title').textContent = libraryView ? '媒体库' : '总览';
  document.querySelectorAll('.nav-item[data-view]').forEach((button) => button.classList.toggle('is-active', button.dataset.view === view));
  if (libraryView && !state.selectedLibrary && state.libraries.length) selectLibrary(state.libraries[0].id);
}

function selectLibrary(id) {
  state.selectedLibrary = state.libraries.find((library) => library.id === id) || null;
  state.offset = 0;
  renderLibraries();
  setView('library');
  loadItems(true);
}

async function loadItems(reset) {
  if (!state.selectedLibrary) return;
  const serial = ++state.requestSerial;
  const library = state.selectedLibrary;
  if (reset) {
    state.offset = 0;
    $('#media-items').innerHTML = '<div class="loading-state">正在读取媒体索引…</div>';
  }
  $('#media-heading').innerHTML = `<div><h2>${escapeHTML(library.name)}</h2><p class="muted">${escapeHTML(library.path)} · ${number.format(library.itemCount)} 个媒体文件</p></div><button class="button button-secondary scan-button" data-scan="${library.id}">↻ 重新扫描</button>`;
  $('#media-heading [data-scan]').addEventListener('click', queueScan);
  try {
    const data = await api(`/api/items?libraryId=${library.id}&limit=60&offset=${state.offset}`);
    if (serial !== state.requestSerial || state.selectedLibrary?.id !== library.id) return;
    const markup = data.items.map((item) => `<article class="media-card"><button class="poster" data-play="${item.id}" data-title="${escapeHTML(item.title)}" data-extension="${item.extension}"><span class="poster-art">${audioExtensions.has(item.extension) ? '♫' : '▶'}</span><span class="play-overlay">▶</span></button><div class="media-copy"><strong title="${escapeHTML(item.title)}">${escapeHTML(item.title)}</strong><span>${escapeHTML(item.extension.replace('.', '').toUpperCase())} · ${formatBytes(item.size)}</span></div></article>`).join('');
    $('#media-items').innerHTML = reset ? markup : $('#media-items').innerHTML + markup;
    state.total = data.total;
    state.offset += data.items.length;
    $('#empty-media').hidden = data.total !== 0;
    $('#load-more').hidden = state.offset >= data.total || data.total === 0;
    document.querySelectorAll('[data-play]').forEach((button) => button.addEventListener('click', () => playItem(button.dataset.play, button.dataset.title, button.dataset.extension)));
  } catch (error) {
    if (serial === state.requestSerial) $('#media-items').innerHTML = `<div class="inline-error">${escapeHTML(error.message)}</div>`;
  }
}

async function queueScan(event) {
  const id = Number(event.currentTarget.dataset.scan);
  event.currentTarget.disabled = true;
  try {
    await api(`/api/libraries/${id}/scan`, { method: 'POST' });
    showToast('扫描已加入后台队列');
    await refreshData();
  } catch (error) { showToast(error.message, true); }
  finally { event.currentTarget.disabled = false; }
}

function playItem(id, title, extension) {
  $('#player-title').textContent = title;
  const isAudio = audioExtensions.has(extension);
  const video = $('#video-player');
  const audio = $('#audio-player');
  video.hidden = isAudio;
  audio.hidden = !isAudio;
  const player = isAudio ? audio : video;
  player.src = `/api/items/${id}/stream`;
  $('#player-dialog').showModal();
  player.play().catch(() => {});
}

function showAddDialog() {
  $('#add-error').textContent = '';
  $('#add-dialog').showModal();
}

$('#login-form').addEventListener('submit', async (event) => {
  event.preventDefault();
  $('#login-error').textContent = '';
  try {
    await api('/api/login', { method: 'POST', body: JSON.stringify({ password: $('#password').value }) });
    $('#password').value = '';
    showApp();
    await refreshData();
  } catch (error) { $('#login-error').textContent = error.message; }
});

$('#add-form').addEventListener('submit', async (event) => {
  event.preventDefault();
  $('#add-error').textContent = '';
  const submit = $('#add-submit');
  submit.disabled = true;
  const form = new FormData(event.currentTarget);
  try {
    const created = await api('/api/libraries', { method: 'POST', body: JSON.stringify({ name: form.get('name'), path: form.get('path'), kind: form.get('kind') }) });
    $('#add-dialog').close();
    event.currentTarget.reset();
    await refreshData();
    showToast('媒体库已添加，扫描任务已启动');
    selectLibrary(created.library.id);
  } catch (error) { $('#add-error').textContent = error.message; }
  finally { submit.disabled = false; }
});

document.querySelectorAll('[data-view]').forEach((button) => button.addEventListener('click', () => setView(button.dataset.view)));
document.querySelectorAll('[data-action="show-add"]').forEach((button) => button.addEventListener('click', showAddDialog));
$('#refresh-button').addEventListener('click', () => refreshData(true));
$('#load-more').addEventListener('click', () => loadItems(false));
$('#logout-button').addEventListener('click', async () => { try { await api('/api/logout', { method: 'POST', body: '{}' }); } catch {} showLogin(); });
$('#close-player').addEventListener('click', () => $('#player-dialog').close());
$('#player-dialog').addEventListener('close', () => { for (const player of [$('#video-player'), $('#audio-player')]) { player.pause(); player.removeAttribute('src'); player.load(); } });
$('#player-dialog').addEventListener('click', (event) => { if (event.target === $('#player-dialog')) $('#close-player').click(); });

api('/api/session').then(() => { showApp(); return refreshData(); }).catch(() => showLogin());
