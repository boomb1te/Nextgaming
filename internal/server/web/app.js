// NextGaming admin panel. Plain JavaScript, no build step, no dependencies.
// The server pushes a full snapshot over Server-Sent Events whenever anything
// changes; this file only renders it and sends actions to the JSON API.
'use strict';

const S = {
  me: null, snap: null, snapAt: 0, tab: 'pcs', live: false, es: null,
  seenAlert: -1, reportDate: todayISO(), auditDate: todayISO(),
  selecting: false, selected: new Set(),
  screens: null, // live state of the Screens tab (see screens.js section)
};

// ---------- helpers ----------

function h(tag, attrs, ...kids) {
  const el = document.createElement(tag);
  if (attrs) {
    for (const [k, v] of Object.entries(attrs)) {
      if (v === null || v === undefined || v === false) continue;
      if (k === 'class') el.className = v;
      else if (k.startsWith('on')) el.addEventListener(k.slice(2), v);
      else if (k === 'value') el.value = v;
      else if (v === true) el.setAttribute(k, '');
      else el.setAttribute(k, v);
    }
  }
  for (const kid of kids.flat(Infinity)) {
    if (kid === null || kid === undefined || kid === false) continue;
    el.append(kid instanceof Node ? kid : document.createTextNode(String(kid)));
  }
  return el;
}

function todayISO() {
  const d = new Date();
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`;
}
function cur() { return (S.snap && S.snap.settings.currency) || ''; }
function fmtMoney(m) {
  const neg = m < 0; m = Math.abs(m);
  return (neg ? '-' : '') + Math.floor(m / 100) + '.' + String(m % 100).padStart(2, '0');
}
function money(m) { return fmtMoney(m) + ' ' + cur(); }
function parseMoney(s) {
  s = String(s).trim().replace(',', '.');
  if (!/^-?\d*(\.\d{0,2})?$/.test(s) || s === '' || s === '-' || s === '.') return NaN;
  const neg = s.startsWith('-'); if (neg) s = s.slice(1);
  const [w, f = ''] = s.split('.');
  const v = parseInt(w || '0', 10) * 100 + parseInt((f + '00').slice(0, 2), 10);
  return neg ? -v : v;
}
function dur(sec) {
  sec = Math.max(0, Math.floor(sec));
  const hh = Math.floor(sec / 3600), mm = Math.floor((sec % 3600) / 60), ss = sec % 60;
  return `${hh}:${String(mm).padStart(2, '0')}:${String(ss).padStart(2, '0')}`;
}
function mins(m) {
  const hh = Math.floor(m / 60), mm = m % 60;
  return hh ? (mm ? `${hh} h ${mm} min` : `${hh} h`) : `${mm} min`;
}
function priceFor(rate, minutes) { return minutes > 0 && rate > 0 ? Math.floor((rate * minutes + 30) / 60) : 0; }
function fmtBytes(n) {
  if (n < 0) return '—';
  if (n < 1024) return n + ' B';
  const u = ['KB', 'MB', 'GB', 'TB']; let i = -1; do { n /= 1024; i++; } while (n >= 1024 && i < u.length - 1);
  return (n < 10 ? n.toFixed(1) : Math.round(n)) + ' ' + u[i];
}
function timeStr(t) { const d = new Date(t); return isNaN(d) || d.getFullYear() < 2000 ? '—' : d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }); }
function dateTimeStr(t) { const d = new Date(t); return isNaN(d) || d.getFullYear() < 2000 ? '—' : d.toLocaleString([], { dateStyle: 'short', timeStyle: 'short' }); }
function isOwner() { return S.me && S.me.role === 'owner'; }
function pcById(id) { return S.snap && S.snap.pcs.find(p => p.id === id); }
function zoneById(id) { return S.snap && S.snap.zones.find(z => z.id === id); }

async function api(method, path, body) {
  const opts = { method, headers: { 'X-NextGaming': '1' }, credentials: 'same-origin' };
  if (body !== undefined) { opts.headers['Content-Type'] = 'application/json'; opts.body = JSON.stringify(body); }
  let res;
  try { res = await fetch(path, opts); } catch (e) { throw new Error('Cannot reach the NextGaming server.'); }
  let data = null;
  try { data = await res.json(); } catch (e) { /* empty */ }
  if (res.status === 401 && !path.startsWith('/api/login') && !path.startsWith('/api/me')) {
    renderLogin();
    throw new Error('Please log in again.');
  }
  if (!res.ok) throw new Error((data && data.error) || `Error ${res.status}`);
  return data;
}

function toast(text, level) {
  const t = h('div', { class: 'toast ' + (level || '') }, text);
  document.getElementById('toasts').append(t);
  setTimeout(() => t.remove(), level === 'error' ? 9000 : 5000);
}

// Run an API action from a button: disables it, shows errors.
async function act(btn, fn, okText) {
  if (btn) btn.disabled = true;
  try {
    const r = await fn();
    if (okText) toast(typeof okText === 'function' ? okText(r) : okText, 'ok');
    return r;
  } catch (e) {
    toast(e.message, 'error');
    throw e;
  } finally {
    if (btn) btn.disabled = false;
  }
}

function modal(title, body, footer) {
  const root = document.getElementById('modal-root');
  const back = h('div', { class: 'modal-back' });
  const close = () => { back.remove(); document.removeEventListener('keydown', onKey); };
  const onKey = e => { if (e.key === 'Escape') close(); };
  document.addEventListener('keydown', onKey);
  back.addEventListener('mousedown', e => { if (e.target === back) close(); });
  const box = h('div', { class: 'modal' },
    h('h2', null, title, h('button', { class: 'x', title: 'Close', onclick: close }, '×')),
    body, footer ? h('div', { class: 'actions' }, footer) : null);
  back.append(box);
  root.append(back);
  const first = box.querySelector('input, select, textarea');
  if (first) setTimeout(() => first.focus(), 30);
  return { close, box };
}

function confirmBox(title, text, okLabel, danger) {
  return new Promise(resolve => {
    let done = false;
    const finish = v => { if (!done) { done = true; m.close(); resolve(v); } };
    const m = modal(title, h('p', null, text), [
      h('button', { class: 'btn', onclick: () => finish(false) }, 'Cancel'),
      h('button', { class: 'btn ' + (danger ? 'danger' : 'primary'), onclick: () => finish(true) }, okLabel || 'OK'),
    ]);
    const obs = new MutationObserver(() => { if (!document.body.contains(m.box)) { obs.disconnect(); if (!done) { done = true; resolve(false); } } });
    obs.observe(document.getElementById('modal-root'), { childList: true });
  });
}

function field(label, input, hint) {
  return h('label', { class: 'field' }, h('span', null, label), input, hint ? h('span', null, hint) : null);
}

// ---------- boot, setup, login ----------

async function boot() {
  try {
    const s = await api('GET', '/api/setup');
    if (s.needsSetup) return renderSetup();
    try { S.me = await api('GET', '/api/me'); } catch (e) { return renderLogin(); }
    startApp();
  } catch (e) {
    document.getElementById('app').replaceChildren(h('p', { class: 'boot' }, e.message));
    setTimeout(boot, 3000);
  }
}

function renderSetup() {
  const club = h('input', { type: 'text', value: 'NextGaming Club' });
  const user = h('input', { type: 'text', value: 'owner', autocomplete: 'username' });
  const pass = h('input', { type: 'password', autocomplete: 'new-password' });
  const pass2 = h('input', { type: 'password', autocomplete: 'new-password' });
  const err = h('div', { class: 'error' });
  const btn = h('button', { class: 'btn primary', type: 'submit' }, 'Create owner account');
  const form = h('form', { class: 'card auth', onsubmit: async e => {
    e.preventDefault(); err.textContent = '';
    if (pass.value !== pass2.value) { err.textContent = 'Passwords do not match.'; return; }
    try {
      await api('POST', '/api/setup', { username: user.value, password: pass.value, clubName: club.value });
      S.me = await api('GET', '/api/me');
      startApp();
    } catch (e2) { err.textContent = e2.message; }
  } },
  h('h2', null, 'Welcome to NextGaming'),
  h('p', { class: 'muted' }, 'Create the owner account. You can add cashier accounts later.'),
  field('Club name', club), field('Username', user), field('Password (6+ characters)', pass), field('Repeat password', pass2),
  btn, err);
  document.getElementById('app').replaceChildren(form);
}

function renderLogin() {
  screensStop();
  if (S.es) { S.es.close(); S.es = null; }
  document.getElementById('modal-root').replaceChildren();
  const user = h('input', { type: 'text', autocomplete: 'username' });
  const pass = h('input', { type: 'password', autocomplete: 'current-password' });
  const err = h('div', { class: 'error' });
  const form = h('form', { class: 'card auth', onsubmit: async e => {
    e.preventDefault(); err.textContent = '';
    try {
      S.me = await api('POST', '/api/login', { username: user.value, password: pass.value });
      startApp();
    } catch (e2) { err.textContent = e2.message; }
  } },
  h('h2', null, 'NextGaming — log in'),
  field('Username', user), field('Password', pass),
  h('button', { class: 'btn primary', type: 'submit' }, 'Log in'), err);
  document.getElementById('app').replaceChildren(form);
}

let timerInterval = null;
function startApp() {
  renderShell();
  connectEvents();
  if (!timerInterval) timerInterval = setInterval(tickTimers, 1000);
}

function connectEvents() {
  if (S.es) S.es.close();
  const es = new EventSource('/api/events');
  S.es = es;
  es.addEventListener('state', ev => {
    S.snap = JSON.parse(ev.data);
    S.snapAt = Date.now();
    S.live = true;
    handleAlerts();
    render();
  });
  es.onerror = async () => {
    S.live = false;
    renderHeader();
    try { await api('GET', '/api/me'); } catch (e) { /* api() shows login on 401 */ }
  };
}

function handleAlerts() {
  const alerts = S.snap.alerts || [];
  const maxId = alerts.reduce((m, a) => Math.max(m, a.id), 0);
  if (S.seenAlert < 0) { S.seenAlert = maxId; return; } // don't replay old alerts on page load
  for (const a of alerts) {
    if (a.id > S.seenAlert) toast(a.text, a.level === 'info' ? '' : a.level);
  }
  S.seenAlert = Math.max(S.seenAlert, maxId);
}

// ---------- shell ----------

function renderShell() {
  const tabs = [['pcs', 'PCs'], ['screens', 'Screens'], ['files', 'Files'], ['bookings', 'Bookings'], ['clients', 'Clients'], ['shift', 'Shift & cash'], ['reports', 'Reports'], ['log', 'Activity log'], ['settings', 'Settings']];
  document.getElementById('app').replaceChildren(
    h('header', { class: 'top', id: 'hdr' }),
    h('nav', { class: 'tabs' }, tabs.map(([id, label]) =>
      h('button', { 'data-tab': id, class: S.tab === id ? 'on' : '', onclick: () => { switchTab(id); } }, label))),
    h('main', { id: 'main' }, h('p', { class: 'muted' }, 'Connecting…')));
}

function renderTabs() {
  document.querySelectorAll('nav.tabs button').forEach(b => b.classList.toggle('on', b.dataset.tab === S.tab));
}

function renderHeader() {
  const hdr = document.getElementById('hdr');
  if (!hdr) return;
  const snap = S.snap;
  const shift = snap && snap.shift;
  hdr.replaceChildren(
    h('h1', null, snap ? snap.settings.clubName : 'NextGaming'),
    shift
      ? h('span', { class: 'badge ok', title: 'Cash in the drawer should be this amount' }, `Shift open · cash ${money(shift.cashNow)}`)
      : h('button', { class: 'btn small', onclick: () => { S.tab = 'shift'; renderTabs(); render(); } }, 'Open a shift'),
    h('span', { class: 'grow' }),
    h('span', { class: 'conn' + (S.live ? '' : ' bad') }, S.live ? 'Live' : 'Reconnecting…'),
    h('span', { class: 'muted small' }, `${S.me.username} (${S.me.role})`),
    h('button', { class: 'btn small', onclick: async () => { await api('POST', '/api/logout').catch(() => {}); S.me = null; renderLogin(); } }, 'Log out'));
}

function render() {
  if (!S.me || !document.getElementById('main')) return;
  renderHeader();
  if (!S.snap) return;
  const main = document.getElementById('main');
  if (S.tab !== 'screens') screensStop();
  if (S.tab !== 'files') filesStop();
  // Keep forms on Settings/Shift/Reports stable while snapshots stream in.
  if (S.tab === 'pcs') main.replaceChildren(renderPCs());
  else if (main.dataset.tab !== S.tab) {
    if (S.tab === 'screens') main.replaceChildren(renderScreens());
    if (S.tab === 'files') main.replaceChildren(renderFiles());
    if (S.tab === 'bookings') main.replaceChildren(renderBookings());
    if (S.tab === 'clients') main.replaceChildren(renderClients());
    if (S.tab === 'shift') main.replaceChildren(renderShift());
    if (S.tab === 'reports') main.replaceChildren(renderReports());
    if (S.tab === 'log') main.replaceChildren(renderLog());
    if (S.tab === 'settings') main.replaceChildren(renderSettings());
  } else if (S.tab === 'screens') {
    screensReconcile();
  } else if (S.tab === 'shift') {
    const live = document.getElementById('shift-live');
    if (live) live.replaceChildren(shiftLive());
    if (!!S.snap.shift !== (main.dataset.shiftOpen === '1')) main.replaceChildren(renderShift());
  }
  main.dataset.tab = S.tab;
}

// Re-render only the view (used after actions on non-live tabs).
function rerender() { const m = document.getElementById('main'); if (m) m.dataset.tab = ''; render(); }

// switchTab changes the active tab and stops screen monitoring when leaving it.
function switchTab(id) { S.tab = id; renderTabs(); render(); }

// ---------- live timers ----------

function tickTimers() {
  if (!S.snap) return;
  const delta = (Date.now() - S.snapAt) / 1000;
  document.querySelectorAll('[data-rem]').forEach(el => {
    const r = Number(el.dataset.rem) - delta;
    el.textContent = dur(r);
    el.classList.toggle('low', r < 300);
  });
  document.querySelectorAll('[data-el]').forEach(el => { el.textContent = dur(Number(el.dataset.el) + delta); });
}

// ---------- PCs ----------

const STATE_LABEL = { free: 'Free', busy: 'In use', paused: 'Paused', checkout: 'Wants to pay', staff: 'Staff (free)', offline: 'Offline' };

function wolBadge(pc) {
  const s = pc.wol.status;
  if (s === 'ok') return null;
  if (s === 'testing') return h('span', { class: 'badge info', title: pc.wol.stage }, 'WoL test…');
  if (s === 'bios_needed' || s === 'windows_settings') return h('span', { class: 'badge bad', title: pc.wol.detail }, 'WoL ✘');
  if (s === 'wifi') return h('span', { class: 'badge warn', title: pc.wol.detail }, 'Wi-Fi');
  if (s === 'untested' || s === 'fixed') return h('span', { class: 'badge', title: pc.wol.detail }, 'WoL ?');
  return null;
}

function tileTimer(pc) {
  const s = pc.session;
  if (!s) return h('div', { class: 'timer muted' }, pc.runtime.online ? '—' : '');
  if (s.status === 'paused') return h('div', { class: 'timer' }, s.mode === 'prepaid' ? dur(s.remainingSec) : dur(s.elapsedSec));
  if (s.mode === 'prepaid') return h('div', { class: 'timer' + (s.remainingSec < 300 ? ' low' : ''), 'data-rem': s.remainingSec }, dur(s.remainingSec));
  return h('div', { class: 'timer', 'data-el': s.elapsedSec }, dur(s.elapsedSec));
}

function tileSub(pc) {
  const s = pc.session;
  if (!s) return pc.zoneName || '';
  if (s.mode === 'prepaid') return `${s.tariffName} · paid ${money(s.paid)}`;
  if (s.mode === 'open') return `Open · due ${money(s.due)}`;
  return s.tariffName;
}

function renderPCs() {
  const snap = S.snap;
  const pcs = snap.pcs;
  // Drop selections for PCs that no longer exist.
  for (const id of [...S.selected]) if (!pcs.find(p => p.id === id)) S.selected.delete(id);
  const counts = pcs.reduce((c, p) => (c[p.state] = (c[p.state] || 0) + 1, c), {});

  const selectBtn = h('button', { class: 'btn' + (S.selecting ? ' primary' : ''), onclick: () => {
    S.selecting = !S.selecting; if (!S.selecting) S.selected.clear(); rerenderPCs();
  } }, S.selecting ? 'Done selecting' : 'Select');

  const toolbar = h('div', { class: 'toolbar' },
    selectBtn,
    h('button', { class: 'btn', onclick: e => bulk(e.target, 'wake') }, 'Wake all offline PCs'),
    h('button', { class: 'btn', onclick: e => bulk(e.target, 'shutdown_free') }, 'Shut down free PCs'),
    h('button', { class: 'btn', onclick: () => messageModal(null) }, 'Message all'),
    h('div', { class: 'legend' }, Object.entries(STATE_LABEL).map(([k, v]) =>
      h('span', null, h('i', { class: 'st-' + k, style: `background:var(--${{ free: 'green', busy: 'blue', paused: 'amber', checkout: 'red', staff: 'purple', offline: 'gray' }[k]})` }), `${v} ${counts[k] || 0}`))));

  if (!pcs.length) {
    return h('div', null, toolbar, h('div', { class: 'card' },
      h('h2', null, 'No PCs yet'),
      h('p', null, 'Install the NextGaming agent on each gaming PC. They appear here automatically as soon as they connect.'),
      isOwner() ? h('button', { class: 'btn primary', onclick: () => { S.tab = 'settings'; renderTabs(); rerender(); } }, 'Show install instructions') : null));
  }

  const grid = h('div', { class: 'grid' }, pcs.map(pc => {
    const on = S.selected.has(pc.id);
    return h('div', { class: 'tile st-' + pc.state + (S.selecting && on ? ' selected' : ''), onclick: () => {
      if (S.selecting) { on ? S.selected.delete(pc.id) : S.selected.add(pc.id); rerenderPCs(); }
      else pcModal(pc.id);
    } },
      S.selecting ? h('div', { class: 'tickbox' }, on ? '✓' : '') : null,
      h('div', { class: 'name' }, pc.name, pc.note ? h('span', { class: 'noteflag', title: pc.note }, '📝') : null, wolBadge(pc)),
      h('div', { class: 'state' }, STATE_LABEL[pc.state] || pc.state),
      tileTimer(pc),
      h('div', { class: 'sub' }, tileSub(pc)),
      pc.nextBooking ? h('div', { class: 'booked' }, `📅 Booked ${timeStr(pc.nextBooking.at)} — ${pc.nextBooking.who}`) : null,
      pc.runtime.online && !pc.runtime.clientAlive ? h('div', { class: 'warn' }, 'Lock screen not running') : null,
      !pc.runtime.online && pc.session ? h('div', { class: 'warn' }, 'Offline during session') : null);
  }));

  return h('div', null, toolbar, S.selecting ? selectionBar(pcs) : null, grid);
}

// Re-render just the PCs tab (selection changes shouldn't wait for a snapshot).
function rerenderPCs() {
  const main = document.getElementById('main');
  if (main && S.tab === 'pcs') main.replaceChildren(renderPCs());
}

function selectionBar(pcs) {
  const chosen = () => pcs.filter(p => S.selected.has(p.id));
  const n = S.selected.size;
  const freeOnline = chosen().filter(p => p.state === 'free');
  const onlineSel = chosen().filter(p => p.runtime.online);
  const offlineSel = chosen().filter(p => !p.runtime.online);
  const withSession = chosen().filter(p => p.session);
  const pausable = chosen().filter(p => p.session && p.session.status === 'active' && p.session.mode !== 'free');
  const resumable = chosen().filter(p => p.session && p.session.status === 'paused');

  const bar = h('div', { class: 'card selbar' },
    h('div', { class: 'row' },
      h('b', null, `${n} selected`),
      h('button', { class: 'btn small', onclick: () => { pcs.forEach(p => S.selected.add(p.id)); rerenderPCs(); } }, 'Select all'),
      h('button', { class: 'btn small', onclick: () => { S.selected.clear(); rerenderPCs(); } }, 'Clear')));
  if (n === 0) { bar.append(h('p', { class: 'muted small' }, 'Tap PCs to select them.')); return bar; }

  const actions = h('div', { class: 'row', style: 'margin-top:8px' },
    h('button', { class: 'btn primary', disabled: !freeOnline.length, onclick: () => startManyModal(freeOnline.map(p => p.id)) }, `Start session (${freeOnline.length} free)`),
    h('button', { class: 'btn', disabled: !onlineSel.length, onclick: () => messageManyModal(onlineSel.map(p => p.id)) }, 'Message'),
    h('button', { class: 'btn', disabled: !pausable.length, onclick: e => runMany(e.target, 'Pause', pausable, p => api('POST', `/api/pcs/${p.id}/pause`)) }, `Pause${pausable.length ? ' (' + pausable.length + ')' : ''}`),
    h('button', { class: 'btn', disabled: !resumable.length, onclick: e => runMany(e.target, 'Resume', resumable, p => api('POST', `/api/pcs/${p.id}/resume`)) }, `Resume${resumable.length ? ' (' + resumable.length + ')' : ''}`),
    h('button', { class: 'btn', disabled: !onlineSel.length, onclick: e => runMany(e.target, 'Lock', onlineSel, p => api('POST', `/api/pcs/${p.id}/lock`)) }, 'Lock'),
    h('button', { class: 'btn primary', disabled: !withSession.length, onclick: () => endManyModal(withSession.map(p => p.id)) }, `End session${withSession.length ? ' (' + withSession.length + ')' : ''}`),
    h('button', { class: 'btn', disabled: !offlineSel.length, onclick: e => runMany(e.target, 'Wake', offlineSel, p => api('POST', `/api/pcs/${p.id}/wake`)) }, 'Wake'),
    h('button', { class: 'btn danger', disabled: !onlineSel.length, onclick: async e => {
      if (!await confirmBox('Shut down', `Shut down ${onlineSel.length} PC(s)? Any paid session is left running on the server.`, 'Shut down', true)) return;
      runMany(e.target, 'Shut down', onlineSel, p => api('POST', `/api/pcs/${p.id}/power`, { action: 'shutdown', force: true }));
    } }, 'Shut down'));
  bar.append(actions);
  if (withSession.length) bar.append(h('p', { class: 'muted small' }, `${withSession.length} selected PC(s) have a session — "Start session" skips them.`));
  return bar;
}

// runMany applies fn to each PC and reports how it went.
async function runMany(btn, label, list, fn) {
  btn.disabled = true;
  const done = [], failed = [];
  for (const p of list) {
    try { await fn(p); done.push(p.name); } catch (e) { failed.push(`${p.name} (${e.message})`); }
  }
  btn.disabled = false;
  toast(`${label}: ${done.length} done` + (failed.length ? `, ${failed.length} failed — ${failed.join('; ')}` : ''), failed.length ? 'warn' : 'ok');
}

async function bulk(btn, action) {
  const label = action === 'wake' ? 'Wake all offline PCs?' : 'Shut down every PC that is not in use?';
  if (!await confirmBox('Confirm', label, 'Yes')) return;
  const r = await act(btn, () => api('POST', '/api/bulk', { action }));
  const parts = [];
  if (r.done && r.done.length) parts.push(`Done: ${r.done.join(', ')}`);
  if (r.skipped && r.skipped.length) parts.push(`Skipped: ${r.skipped.join(', ')}`);
  toast(parts.join(' · ') || 'Nothing to do.', r.skipped && r.skipped.length ? 'warn' : 'ok');
}

function pcModal(id) {
  const pc = pcById(id);
  if (!pc) return;
  const s = pc.session;
  const online = pc.runtime.online;
  let m;
  const run = (fn, ok) => async e => { try { await act(e.target, fn, ok); m.close(); } catch (err) { /* toast shown */ } };
  const then = f => () => { m.close(); f(); };

  const info = h('dl', { class: 'kv' },
    h('dt', null, 'Status'), h('dd', null, STATE_LABEL[pc.state], online ? '' : ` (last seen ${dateTimeStr(pc.runtime.lastSeen)})`),
    h('dt', null, 'Zone'), h('dd', null, pc.zoneName || '—', pc.zoneId && zoneById(pc.zoneId) ? ` · ${money(zoneById(pc.zoneId).hourlyRate)}/h` : ''),
    s ? [
      h('dt', null, 'Session'), h('dd', null, `${s.tariffName} · started ${timeStr(s.startedAt)}`),
      s.mode === 'prepaid' ? [h('dt', null, 'Time left'), h('dd', null, dur(s.remainingSec))] : [h('dt', null, 'Time used'), h('dd', null, dur(s.elapsedSec))],
      s.mode === 'open' ? [h('dt', null, 'Due now'), h('dd', null, money(s.due))] : [h('dt', null, 'Paid'), h('dd', null, money(s.paid))],
      s.note ? [h('dt', null, 'Note'), h('dd', null, s.note)] : null,
    ] : null,
    h('dt', null, 'Network'), h('dd', null, `${pc.ip || '—'} · MAC ${pc.mac || '—'}`),
    h('dt', null, 'Computer'), h('dd', null, `${pc.hostname || '—'} · ${[pc.boardMaker, pc.boardProduct].filter(Boolean).join(' ') || 'board unknown'}`),
    online ? [h('dt', null, 'Lock screen'), h('dd', null, pc.runtime.clientAlive ? 'running' : h('b', { style: 'color:var(--red)' }, 'not running'))] : null);

  const btns = [];
  if (pc.state === 'free') {
    btns.push(h('button', { class: 'btn primary', onclick: then(() => startModal(pc.id)) }, 'Start session'));
    btns.push(h('button', { class: 'btn', onclick: run(() => api('POST', `/api/pcs/${pc.id}/unlock-free`, {}), 'Unlocked for staff (not billed)') }, 'Unlock free (staff)'));
  }
  if (s) {
    if (s.checkoutRequested) btns.push(h('button', { class: 'btn primary', onclick: then(() => endModal(pc.id)) }, 'Settle & end'));
    if (s.mode === 'prepaid') btns.push(h('button', { class: 'btn' + (s.checkoutRequested ? '' : ' primary'), onclick: then(() => timeModal(pc.id)) }, 'Add time'));
    if (s.mode !== 'free' && s.status === 'active') btns.push(h('button', { class: 'btn', onclick: run(() => api('POST', `/api/pcs/${pc.id}/pause`), 'Paused') }, 'Pause'));
    if (s.status === 'paused') btns.push(h('button', { class: 'btn', onclick: run(() => api('POST', `/api/pcs/${pc.id}/resume`), 'Resumed') }, 'Resume'));
    if (s.mode === 'free') btns.push(h('button', { class: 'btn primary', onclick: run(() => api('POST', `/api/pcs/${pc.id}/lock`), 'Locked') }, 'Lock'));
    else if (!s.checkoutRequested) btns.push(h('button', { class: 'btn', onclick: then(() => endModal(pc.id)) }, 'End session'));
    btns.push(h('button', { class: 'btn', onclick: then(() => moveModal(pc.id)) }, 'Move to another PC'));
  }
  const power = [];
  if (online) {
    power.push(h('button', { class: 'btn', onclick: then(() => messageModal(pc.id)) }, 'Send message'));
    power.push(h('button', { class: 'btn', onclick: e => powerAction(e.target, pc, 'restart', m) }, 'Restart'));
    power.push(h('button', { class: 'btn danger', onclick: e => powerAction(e.target, pc, 'shutdown', m) }, 'Shut down'));
  } else {
    power.push(h('button', { class: 'btn primary', disabled: !pc.mac, onclick: run(() => api('POST', `/api/pcs/${pc.id}/wake`), 'Wake signal sent. The PC should appear online within a minute.') }, 'Turn on (Wake-on-LAN)'));
  }

  // Per-PC note.
  const noteInput = h('input', { type: 'text', maxlength: 500, value: pc.note || '', placeholder: 'e.g. keyboard replaced, VIP seat…', style: 'flex:1' });
  const noteBtn = h('button', { class: 'btn' }, 'Save note');
  noteBtn.onclick = e => act(e.target, () => api('POST', `/api/pcs/${pc.id}/note`, { note: noteInput.value }), 'Note saved').catch(() => {});
  const noteSection = h('div', { class: 'section' },
    h('div', { class: 'muted small', style: 'margin-bottom:4px' }, 'PC note'),
    h('div', { class: 'row' }, noteInput, noteBtn));

  m = modal(pc.name, h('div', null,
    info,
    btns.length ? h('div', { class: 'section row' }, btns) : null,
    h('div', { class: 'section row' }, power),
    noteSection,
    h('div', { class: 'section' }, wolSection(pc, () => m.close()))));
}

async function powerAction(btn, pc, action, m) {
  const verb = action === 'restart' ? 'Restart' : 'Shut down';
  if (!await confirmBox(verb, `${verb} ${pc.name}?`, verb, action === 'shutdown')) return;
  try {
    await act(btn, () => api('POST', `/api/pcs/${pc.id}/power`, { action }), `${verb} sent to ${pc.name}`);
    m.close();
  } catch (e) {
    if (/paid session/.test(e.message) && await confirmBox('PC is in use', `${pc.name} has a paid session. ${verb} anyway? The session keeps running on the server.`, `${verb} anyway`, true)) {
      await act(btn, () => api('POST', `/api/pcs/${pc.id}/power`, { action, force: true }), `${verb} sent to ${pc.name}`);
      m.close();
    }
  }
}

const WOL_LABEL = {
  ok: ['ok', 'Works'], untested: ['info', 'Not tested yet'], fixed: ['info', 'Fixed automatically — run a test'],
  windows_settings: ['bad', 'Windows settings need changes'], bios_needed: ['bad', 'BIOS setup needed'],
  wifi: ['warn', 'Needs an Ethernet cable'], testing: ['info', 'Testing…'], unknown: ['', 'Unknown'],
};

function wolSection(pc, closeParent) {
  const w = pc.wol;
  const [cls, label] = WOL_LABEL[w.status] || ['', w.status];
  const online = pc.runtime.online;
  const kids = [
    h('div', { class: 'row' }, h('b', null, 'Remote power-on (Wake-on-LAN)'), h('span', { class: 'badge ' + cls }, label)),
    w.status === 'testing' && w.stage ? h('p', null, w.stage) : null,
    w.detail ? h('p', { class: 'small' }, w.detail) : null,
  ];
  if (w.report && w.report.settings && w.report.settings.length) {
    kids.push(h('details', null, h('summary', { class: 'small' }, 'Windows settings checked ' + dateTimeStr(w.checkedAt)),
      h('ul', { class: 'setlist' }, w.report.settings.map(st =>
        h('li', { class: st.ok ? '' : 'bad' }, `${st.name}: ${st.value}${st.fixed ? ' (fixed)' : ''}`, st.ok || !st.advice ? '' : h('div', { class: 'muted small' }, st.advice))))));
  }
  if ((w.status === 'bios_needed' || (w.status === 'windows_settings' && w.testedAt && new Date(w.testedAt).getFullYear() > 2000)) && w.guide) {
    kids.push(h('div', { class: 'card', style: 'margin-top:8px;background:#fff7ed;border-color:#fed7aa' },
      h('b', null, `${pc.name}${w.guide.vendor ? ' (' + w.guide.vendor + ')' : ''} can't be turned on remotely.`),
      h('ol', { class: 'steps' }, w.guide.steps.map(s => h('li', null, s))),
      h('p', { class: 'muted small' }, w.guide.note)));
  }
  const btns = [];
  if (online) {
    btns.push(h('button', { class: 'btn small', onclick: e => act(e.target, () => api('POST', `/api/pcs/${pc.id}/wol-check`, { fix: true }), 'Check finished') }, 'Check & fix Windows settings'));
    const force = w.status === 'windows_settings';
    btns.push(h('button', { class: 'btn small', disabled: !!pc.session || w.status === 'testing', onclick: async e => {
      const extra = force ? ' The Windows check reported problems, so the test may fail — but if the check is wrong, this shows it.' : '';
      if (!await confirmBox('Test Wake-on-LAN', `${pc.name} will shut down, then NextGaming sends the wake signal and waits for it to start again. This takes 1–4 minutes.${extra}`, 'Start test')) return;
      try {
        await act(e.target, () => api('POST', `/api/pcs/${pc.id}/wol-test`, { force }), 'Test started — watch the PC tile');
        closeParent();
      } catch (err) { /* shown */ }
    } }, force ? 'Test anyway' : 'Test Wake-on-LAN'));
  }
  if (btns.length) kids.push(h('div', { class: 'row' }, btns));
  return h('div', null, kids);
}

// Package / minutes picker used by Start and Add time.
function tariffPicker(pc, rate, onChange) {
  const now = new Date();
  const hour = now.getHours();
  const pkgs = S.snap.packages.filter(p => p.active && (p.zoneId === 0 || p.zoneId === pc.zoneId) && available(p, hour));
  let sel = { packageId: 0, minutes: 60 };
  const minutesIn = h('input', { type: 'number', min: 1, max: 1440, step: 1, value: 60, style: 'width:90px' });
  const pkgBtns = pkgs.map(p => h('button', { type: 'button', class: 'pkg', onclick: () => { sel = { packageId: p.id, minutes: 0 }; update(); } },
    h('b', null, p.name), `${money(p.price)} · ${mins(pkgMinutes(p, now))}`));
  const quick = [30, 60, 90, 120, 180].map(n => h('button', { type: 'button', class: 'btn small', onclick: () => { minutesIn.value = n; sel = { packageId: 0, minutes: n }; update(); } }, mins(n)));
  minutesIn.addEventListener('input', () => { sel = { packageId: 0, minutes: parseInt(minutesIn.value, 10) || 0 }; update(); });
  function update() {
    pkgBtns.forEach((b, i) => b.classList.toggle('on', pkgs[i].id === sel.packageId));
    let price = 0, minutes = sel.minutes, label = 'Hourly';
    if (sel.packageId) { const p = pkgs.find(x => x.id === sel.packageId); price = p.price; minutes = pkgMinutes(p, now); label = p.name; }
    else price = priceFor(rate, sel.minutes);
    onChange({ ...sel, price, minutesTotal: minutes, label });
  }
  setTimeout(update);
  return h('div', null,
    pkgs.length ? [h('div', { class: 'muted small' }, 'Packages'), h('div', { class: 'pkgs' }, pkgBtns)] : null,
    h('div', { class: 'muted small' }, `By the hour (${money(rate)}/h)`),
    h('div', { class: 'row' }, minutesIn, 'minutes', quick));
}

function available(p, hour) {
  if (p.availableFrom < 0 || p.availableTo < 0 || p.availableFrom === p.availableTo) return true;
  return p.availableFrom < p.availableTo ? hour >= p.availableFrom && hour < p.availableTo : hour >= p.availableFrom || hour < p.availableTo;
}
function pkgMinutes(p, now) {
  if (p.endsAtHour < 0) return p.minutes;
  const end = new Date(now); end.setHours(p.endsAtHour, 0, 0, 0);
  if (end <= now) end.setDate(end.getDate() + 1);
  return Math.floor((end - now) / 60000);
}

function startModal(id) {
  const pc = pcById(id);
  const zone = zoneById(pc.zoneId);
  const rate = zone ? zone.hourlyRate : 0;
  let mode = 'prepaid', choice = null;
  const priceEl = h('div', { class: 'price' });
  const note = h('input', { type: 'text', placeholder: 'optional, e.g. customer name' });
  const pickerBox = h('div');
  const startBtn = h('button', { class: 'btn primary' }, 'Start');
  const modeBtns = [['prepaid', 'Prepaid (fixed time)'], ['open', 'Open (pay at the end)']].map(([k, l]) =>
    h('button', { type: 'button', class: 'btn', onclick: () => { mode = k; draw(); } }, l));
  function draw() {
    modeBtns.forEach((b, i) => b.classList.toggle('primary', ['prepaid', 'open'][i] === mode));
    if (mode === 'prepaid') {
      pickerBox.replaceChildren(tariffPicker(pc, rate, c => { choice = c; priceEl.textContent = `${money(c.price)} for ${mins(c.minutesTotal)}`; startBtn.textContent = `Start — collect ${money(c.price)}`; }));
    } else {
      choice = null;
      pickerBox.replaceChildren(h('p', null, `Timer counts up. The customer pays at the end: ${money(rate)} per hour, billed per started minute.`));
      priceEl.textContent = '';
      startBtn.textContent = 'Start open session';
    }
  }
  const m = modal(`Start session — ${pc.name}`, h('div', null,
    h('div', { class: 'row', style: 'margin-bottom:10px' }, modeBtns), pickerBox, priceEl, field('Note', note)),
  [h('button', { class: 'btn', onclick: () => m.close() }, 'Cancel'), startBtn]);
  startBtn.onclick = async () => {
    const body = { mode, note: note.value };
    if (mode === 'prepaid') {
      if (!choice || (!choice.packageId && !(choice.minutes > 0))) { toast('Choose a package or enter minutes.', 'error'); return; }
      body.packageId = choice.packageId; body.minutes = choice.minutes;
    }
    try { await act(startBtn, () => api('POST', `/api/pcs/${pc.id}/start`, body), `${pc.name} started`); m.close(); } catch (e) { /* shown */ }
  };
  draw();
}

function startManyModal(ids) {
  if (!ids.length) { toast('No free, online PC selected.', 'warn'); return; }
  // Use the first PC's zone rate for the preview; each PC is billed at its own zone rate.
  const first = pcById(ids[0]);
  const rate = (zoneById(first.zoneId) || {}).hourlyRate || 0;
  const zonesInSel = new Set(ids.map(i => (pcById(i) || {}).zoneId));
  let mode = null, choice = null; // ask prepaid vs open first
  const priceEl = h('div', { class: 'price' });
  const pickerBox = h('div');
  const startBtn = h('button', { class: 'btn primary', style: 'display:none' }, 'Start');
  const modeBtns = [['prepaid', 'Prepaid (fixed time)'], ['open', 'Open (pay at the end)']].map(([k, l]) =>
    h('button', { type: 'button', class: 'btn', onclick: () => { mode = k; draw(); } }, l));
  function draw() {
    modeBtns.forEach((b, i) => b.classList.toggle('primary', ['prepaid', 'open'][i] === mode));
    startBtn.style.display = mode ? '' : 'none';
    if (mode === null) {
      pickerBox.replaceChildren(h('p', { class: 'muted' }, 'Choose Prepaid or Open above.'));
      priceEl.textContent = '';
    } else if (mode === 'prepaid') {
      pickerBox.replaceChildren(tariffPicker(first, rate, c => { choice = c; priceEl.textContent = `${mins(c.minutesTotal)} on each PC`; startBtn.textContent = `Start on ${ids.length} PC(s)`; }));
    } else {
      choice = null;
      pickerBox.replaceChildren(h('p', null, `Each PC counts up and is paid at the end at its own hourly rate.`));
      priceEl.textContent = '';
      startBtn.textContent = `Start open on ${ids.length} PC(s)`;
    }
  }
  const m = modal(`Start session on ${ids.length} PCs`, h('div', null,
    zonesInSel.size > 1 ? h('p', { class: 'muted small' }, 'Selected PCs are in different zones; each is billed at its own zone rate. A package applies only to PCs in its zone.') : null,
    h('div', { class: 'muted small', style: 'margin-bottom:4px' }, 'Session type'),
    h('div', { class: 'row', style: 'margin-bottom:10px' }, modeBtns), pickerBox, priceEl),
  [h('button', { class: 'btn', onclick: () => m.close() }, 'Cancel'), startBtn]);
  startBtn.onclick = async () => {
    const body = { mode };
    if (mode === 'prepaid') {
      if (!choice || (!choice.packageId && !(choice.minutes > 0))) { toast('Choose a package or enter minutes.', 'error'); return; }
      body.packageId = choice.packageId; body.minutes = choice.minutes;
    }
    await runMany(startBtn, 'Start', ids.map(pcById), p => api('POST', `/api/pcs/${p.id}/start`, body));
    m.close();
  };
  draw();
}

function messageManyModal(ids) {
  const text = h('textarea', { rows: 3, maxlength: 500, placeholder: 'e.g. 10 minutes until closing' });
  const btn = h('button', { class: 'btn primary' }, 'Send');
  const m = modal(`Message ${ids.length} PCs`, field('Text shown on screen', text), [h('button', { class: 'btn', onclick: () => m.close() }, 'Cancel'), btn]);
  btn.onclick = async () => {
    if (!text.value.trim()) { toast('Message is empty.', 'error'); return; }
    await runMany(btn, 'Message', ids.map(pcById), p => api('POST', `/api/pcs/${p.id}/message`, { text: text.value }));
    m.close();
  };
}

function timeModal(id) {
  const pc = pcById(id);
  const s = pc.session;
  let choice = null;
  const priceEl = h('div', { class: 'price' });
  const btn = h('button', { class: 'btn primary' }, 'Add time');
  const m = modal(`Add time — ${pc.name}`, h('div', null,
    tariffPicker(pc, s.ratePerHour, c => { choice = c; priceEl.textContent = `+${mins(c.minutesTotal)} for ${money(c.price)}`; btn.textContent = `Add — collect ${money(c.price)}`; }),
    priceEl), [h('button', { class: 'btn', onclick: () => m.close() }, 'Cancel'), btn]);
  btn.onclick = async () => {
    if (!choice || (!choice.packageId && !(choice.minutes > 0))) { toast('Choose a package or enter minutes.', 'error'); return; }
    try { await act(btn, () => api('POST', `/api/pcs/${pc.id}/add-time`, { packageId: choice.packageId, minutes: choice.minutes }), 'Time added'); m.close(); } catch (e) { /* shown */ }
  };
}

async function endModal(id) {
  const pc = pcById(id);
  let q;
  try { q = await api('GET', `/api/pcs/${pc.id}/quote`); } catch (e) { toast(e.message, 'error'); return; }
  const btn = h('button', { class: 'btn primary' });
  const body = h('div');
  let refundIn = null, reason = null;
  if (q.mode === 'open') {
    body.append(h('dl', { class: 'kv' }, h('dt', null, 'Time used'), h('dd', null, dur(q.activeSec))),
      h('p', null, 'Collect from the customer:'), h('div', { class: 'price' }, money(q.charge)));
    btn.textContent = `Collected ${money(q.charge)} — end session`;
  } else if (q.mode === 'prepaid') {
    refundIn = h('input', { type: 'text', value: '0.00', style: 'width:120px' });
    reason = h('input', { type: 'text', placeholder: 'unused time' });
    body.append(h('dl', { class: 'kv' },
      h('dt', null, 'Time used'), h('dd', null, dur(q.activeSec)),
      h('dt', null, 'Time left'), h('dd', null, dur(q.remainingSec)),
      h('dt', null, 'Paid'), h('dd', null, money(q.paid))),
    h('div', { class: 'section' },
      field('Refund to the customer (optional)', h('div', { class: 'row' }, refundIn, cur(),
        q.suggestedRefund > 0 ? h('button', { type: 'button', class: 'btn small', onclick: () => { refundIn.value = fmtMoney(q.suggestedRefund); } }, `Use unused share: ${money(q.suggestedRefund)}`) : null)),
      field('Reason', reason)));
    btn.textContent = 'End session';
  } else {
    body.append(h('p', null, 'End the staff session and lock the PC?'));
    btn.textContent = 'End & lock';
  }
  const m = modal(`End session — ${pc.name}`, body, [h('button', { class: 'btn', onclick: () => m.close() }, 'Cancel'), btn]);
  btn.onclick = async () => {
    const req = {};
    if (refundIn) {
      const v = parseMoney(refundIn.value);
      if (isNaN(v) || v < 0) { toast('Refund must be an amount like 5.00', 'error'); return; }
      req.refund = v; req.reason = reason.value;
    }
    try {
      const r = await act(btn, () => api('POST', `/api/pcs/${pc.id}/end`, req));
      toast(r.collected ? `Session ended. Collected ${money(r.collected)}.` : r.refunded ? `Session ended. Refunded ${money(r.refunded)}.` : 'Session ended.', 'ok');
      m.close();
    } catch (e) { /* shown */ }
  };
}

// endManyModal ends a selection of sessions at once. It previews what each PC
// collects (open sessions) and the grand total, then ends them all on confirm.
// Prepaid sessions are closed with no refund (settle those individually first
// if a refund is due). The server recomputes money at end time, so the preview
// is "as of now".
async function endManyModal(ids) {
  const list = ids.map(pcById).filter(p => p && p.session);
  if (!list.length) { toast('No sessions to end.', 'warn'); return; }
  const loading = modal('End sessions', h('p', { class: 'muted' }, 'Working out the amounts…'));
  let quotes;
  try {
    quotes = await Promise.all(list.map(p =>
      api('GET', `/api/pcs/${p.id}/quote`).then(q => ({ p, q })).catch(e => ({ p, err: e.message }))));
  } catch (e) { loading.close(); toast(e.message, 'error'); return; }
  loading.close();

  let total = 0;
  const rows = quotes.map(({ p, q, err }) => {
    if (err) return h('tr', null, h('td', null, p.name), h('td', null, '—'), h('td', { class: 'small' }, h('span', { class: 'muted' }, err)), h('td', { class: 'num' }, '—'));
    let typ = 'Staff', amt = 0, detail = 'not billed';
    if (q.mode === 'open') { typ = 'Open'; amt = q.charge; detail = 'used ' + dur(q.activeSec); }
    else if (q.mode === 'prepaid') { typ = 'Prepaid'; amt = 0; detail = 'paid ' + money(q.paid); }
    total += amt;
    return h('tr', null, h('td', null, p.name), h('td', null, typ), h('td', { class: 'small' }, detail), h('td', { class: 'num' }, amt ? money(amt) : '—'));
  });
  const table = h('table', null,
    h('tr', null, h('th', null, 'PC'), h('th', null, 'Type'), h('th', null, 'Used / paid'), h('th', { class: 'num' }, 'Collect')),
    rows,
    h('tr', null, h('td', null, h('b', null, 'Total to collect')), h('td', null, ''), h('td', null, ''), h('td', { class: 'num' }, h('b', null, money(total)))));
  const btn = h('button', { class: 'btn primary' }, `End ${list.length} & collect ${money(total)}`);
  const m = modal(`End ${list.length} session(s)`, h('div', null,
    h('p', { class: 'muted small' }, 'Open sessions are collected now. Prepaid sessions are closed with no refund — settle those one by one first if a refund is due. Amounts are as of now and finalised when you confirm.'),
    h('div', { style: 'overflow-x:auto' }, table)),
    [h('button', { class: 'btn', onclick: () => m.close() }, 'Cancel'), btn]);
  btn.onclick = async () => {
    await runMany(btn, 'End', list, p => api('POST', `/api/pcs/${p.id}/end`, {}));
    m.close();
    S.selected.clear(); S.selecting = false; rerenderPCs();
  };
}

function moveModal(id) {
  const pc = pcById(id);
  const targets = S.snap.pcs.filter(p => p.id !== id && p.state === 'free');
  if (!targets.length) { toast('No free, online PC to move to.', 'warn'); return; }
  const sel = h('select', null, targets.map(p => h('option', { value: p.id }, `${p.name}${p.zoneId !== pc.zoneId ? ` (${p.zoneName}, rate kept)` : ''}`)));
  const btn = h('button', { class: 'btn primary' }, 'Move');
  const m = modal(`Move session from ${pc.name}`, field('Move to', sel), [h('button', { class: 'btn', onclick: () => m.close() }, 'Cancel'), btn]);
  btn.onclick = async () => { try { await act(btn, () => api('POST', `/api/pcs/${pc.id}/move`, { toPcId: Number(sel.value) }), 'Session moved'); m.close(); } catch (e) { /* shown */ } };
}

function messageModal(id) {
  const pc = id ? pcById(id) : null;
  const text = h('textarea', { rows: 3, maxlength: 500, placeholder: 'e.g. 10 minutes until closing' });
  const btn = h('button', { class: 'btn primary' }, 'Send');
  const m = modal(pc ? `Message to ${pc.name}` : 'Message to all online PCs', field('Text shown on screen', text), [h('button', { class: 'btn', onclick: () => m.close() }, 'Cancel'), btn]);
  btn.onclick = async () => {
    try {
      if (pc) await act(btn, () => api('POST', `/api/pcs/${pc.id}/message`, { text: text.value }), 'Message sent');
      else await act(btn, () => api('POST', '/api/bulk', { action: 'message', text: text.value }), r => `Sent to ${r.done ? r.done.length : 0} PCs`);
      m.close();
    } catch (e) { /* shown */ }
  };
}

// ---------- Screens (live monitoring) ----------
//
// While this tab is open the panel asks the server to monitor; the server
// tells every PC to send a small screenshot every ~2.5 s, synchronised. Each
// tile pulls its PC's latest frame. Clicking a tile opens it full-size and the
// server sends that one PC a sharper frame every second. Closing the tab stops
// all of it, so idle PCs are never captured.

const SCREEN_GRID_MS = 2500;
const SCREEN_FOCUS_MS = 1000;

function onlineScreenPCs() {
  return (S.snap ? S.snap.pcs : []).filter(p => p.runtime.online);
}

function renderScreens() {
  screensStop();
  const grid = h('div', { class: 'screens-grid' });
  const empty = h('p', { class: 'muted' }, 'No PCs are online. Screens appear here as soon as a gaming PC connects.');
  const wrap = h('div', null,
    h('div', { class: 'toolbar' },
      h('b', null, 'Live screens'),
      h('span', { class: 'muted small' }, 'Updates every 2–3 seconds while this tab is open. Click a screen to enlarge it.')),
    grid, empty);
  S.screens = { grid, empty, tiles: new Map(), focus: 0, focusModal: null, lastKey: '' };
  screensBuildTiles();
  // Start monitoring right away, then heartbeat so it keeps going.
  screenPost(true);
  S.screens.timer = setInterval(screensTick, SCREEN_GRID_MS);
  S.screens.heartbeat = setInterval(() => screenPost(true), 4000);
  return wrap;
}

function screenPost(watching) {
  const focus = S.screens ? S.screens.focus : 0;
  api('POST', '/api/screens/state', { watching, focus }).catch(() => {});
}

// screensReconcile rebuilds the grid when the set of online PCs changes.
function screensReconcile() {
  if (!S.screens) return;
  const key = onlineScreenPCs().map(p => p.id).join(',');
  if (key !== S.screens.lastKey) screensBuildTiles();
}

function screensBuildTiles() {
  const sc = S.screens;
  if (!sc) return;
  const pcs = onlineScreenPCs();
  sc.lastKey = pcs.map(p => p.id).join(',');
  sc.empty.style.display = pcs.length ? 'none' : '';
  // Revoke any object URLs we are about to drop.
  for (const t of sc.tiles.values()) if (t.img.dataset.url) URL.revokeObjectURL(t.img.dataset.url);
  sc.tiles.clear();
  sc.grid.replaceChildren(...pcs.map(pc => {
    const img = h('img', { class: 'screen-img', alt: '' });
    const waiting = h('div', { class: 'screen-wait' }, 'Waiting for image…');
    const badge = h('span', { class: 'screen-state st-' + pc.state }, STATE_LABEL[pc.state] || pc.state);
    const tile = h('div', { class: 'screen-tile', title: 'Click to enlarge', onclick: () => openScreenModal(pc.id) },
      h('div', { class: 'screen-frame' }, img, waiting),
      h('div', { class: 'screen-cap' }, h('span', { class: 'screen-name' }, pc.name), badge));
    sc.tiles.set(pc.id, { img, waiting });
    return tile;
  }));
  screensTick();
}

function screensTick() {
  if (!S.screens) return;
  screensReconcile();
  for (const [id, t] of S.screens.tiles) refreshScreen(id, t.img, t.waiting, SCREEN_GRID_MS * 2);
}

// refreshScreen pulls the latest frame as a blob and swaps it in only on 200,
// so a PC with no frame yet shows "waiting" instead of a broken image.
async function refreshScreen(id, img, waiting, timeoutMs) {
  try {
    const ctrl = new AbortController();
    const to = setTimeout(() => ctrl.abort(), timeoutMs || 6000);
    const res = await fetch(`/api/pcs/${id}/screen.jpg?t=${Date.now()}`, { credentials: 'same-origin', signal: ctrl.signal });
    clearTimeout(to);
    if (res.status !== 200) return;
    const blob = await res.blob();
    if (!blob.size) return;
    const url = URL.createObjectURL(blob);
    const old = img.dataset.url;
    img.src = url; img.dataset.url = url; img.classList.add('has');
    if (waiting) waiting.style.display = 'none';
    if (old) URL.revokeObjectURL(old);
  } catch (e) { /* network hiccup or aborted; next tick retries */ }
}

function openScreenModal(id) {
  const pc = pcById(id);
  if (!pc || !S.screens) return;
  S.screens.focus = id;
  screenPost(true); // ask the server for a sharper, faster frame on this PC
  const img = h('img', { class: 'screen-big', alt: '' });
  const waiting = h('div', { class: 'screen-wait' }, 'Waiting for image…');
  const body = h('div', { class: 'screen-big-wrap' }, img, waiting);
  const m = modal(pc.name + ' — live screen', body);
  // Seed from the thumbnail immediately, then refresh at full rate.
  refreshScreen(id, img, waiting, SCREEN_FOCUS_MS * 3);
  const timer = setInterval(() => {
    if (!document.body.contains(m.box)) { stop(); return; }
    refreshScreen(id, img, waiting, SCREEN_FOCUS_MS * 3);
  }, SCREEN_FOCUS_MS);
  const stop = () => {
    clearInterval(timer);
    if (img.dataset.url) URL.revokeObjectURL(img.dataset.url);
    if (S.screens) { S.screens.focus = 0; screenPost(true); }
  };
  S.screens.focusModal = m;
}

function screensStop() {
  const sc = S.screens;
  if (!sc) return;
  S.screens = null;
  if (sc.timer) clearInterval(sc.timer);
  if (sc.heartbeat) clearInterval(sc.heartbeat);
  for (const t of sc.tiles.values()) if (t.img.dataset.url) URL.revokeObjectURL(t.img.dataset.url);
  if (sc.focusModal) sc.focusModal.close();
  screenPostRaw(false, 0); // tell the server to stop capturing now
}

// screenPostRaw posts watching state without reading S.screens (used on stop).
function screenPostRaw(watching, focus) {
  api('POST', '/api/screens/state', { watching, focus }).catch(() => {});
}

// ---------- Files (LAN file sharing) ----------
//
// Two panes: the front-desk PC's own filesystem (left) and one gaming PC's
// single shared folder (right). Copy a file either way. The front desk can go
// anywhere on its own machine; the gaming PC only ever exposes its shared
// folder. This feature is local-network only.

function relJoin(rel, name) { return rel ? rel + '/' + name : name; }
function relParent(rel) { const i = rel.lastIndexOf('/'); return i < 0 ? '' : rel.slice(0, i); }
function relLast(rel) { const i = rel.lastIndexOf('/'); return i < 0 ? rel : rel.slice(i + 1); }

function renderFiles() {
  S.files = { pcId: 0, localPath: '', localSel: null, pcRel: '', pcSel: null, timer: null };
  // Default to the first online PC.
  const online = (S.snap ? S.snap.pcs : []).filter(p => p.runtime && p.runtime.online);
  if (online.length) S.files.pcId = online[0].id;

  const local = localPane();
  const pc = pcPane();
  const xfers = h('div', { class: 'card', id: 'fs-xfers' }, h('p', { class: 'muted' }, 'No transfers yet.'));

  S.files.refreshLocal = local.reload;
  S.files.refreshPC = pc.reload;

  const loadXfers = async () => {
    let list; try { list = await api('GET', '/api/fs/transfers'); } catch (e) { return; }
    if (!S.files) return;
    xfers.replaceChildren(h('h2', null, 'Transfers'),
      list.length ? h('table', { class: 'fs-xfer' },
        h('tr', null, ['', 'File', 'PC', 'Size', 'Progress', 'Status'].map(t => h('th', null, t))),
        list.map(tf => {
          const pct = tf.size > 0 ? Math.min(100, Math.round(100 * tf.done / tf.size)) : (tf.status === 'done' ? 100 : 0);
          const bar = h('div', { class: 'fs-bar' }, h('i', { style: `width:${pct}%` }));
          const st = { active: 'badge info', done: 'badge ok', error: 'badge bad', cancelled: 'badge' }[tf.status] || 'badge';
          return h('tr', null,
            h('td', null, tf.dir === 'to_pc' ? '→ PC' : '← desk'),
            h('td', null, tf.name),
            h('td', null, tf.pcName),
            h('td', null, fmtBytes(tf.size)),
            h('td', null, tf.status === 'active' ? bar : (tf.status === 'done' ? fmtBytes(tf.done) : '—')),
            h('td', null, h('span', { class: st, title: tf.error || '' }, tf.status === 'error' ? (tf.error || 'failed') : tf.status)));
        })) : h('p', { class: 'muted' }, 'No transfers yet.'));
  };
  S.files.loadXfers = loadXfers;
  loadXfers();
  S.files.timer = setInterval(loadXfers, 1000);

  setTimeout(() => paintActions(), 0); // ensure the action bar exists once attached
  return h('div', null,
    h('p', { class: 'muted small' }, 'Copy files between this front-desk PC and a gaming PC. The gaming PC only shares one folder; this PC can browse anywhere. Local network only.'),
    h('div', { class: 'fs-panes' }, local.el, pc.el),
    xfers);
}

function filesStop() {
  const f = S.files;
  if (!f) return;
  S.files = null;
  if (f.timer) clearInterval(f.timer);
}

// Left pane: this (front-desk) PC's filesystem.
function localPane() {
  const head = h('div', { class: 'fs-head' });
  const body = h('div', { class: 'fs-list' }, h('p', { class: 'muted' }, 'Loading…'));
  let cur = null; // last LocalListing

  const paint = () => {
    const f = S.files; if (!f || !cur) return;
    head.replaceChildren(
      h('b', null, 'This PC'),
      h('button', { class: 'btn small', disabled: cur.atRoot, onclick: () => go(cur.parent || '') }, '↑ Up'),
      h('span', { class: 'fs-path', title: cur.path || 'Drives' }, cur.path || 'Drives'));
    const rows = cur.entries.map(e => {
      if (e.isDir) return h('div', { class: 'fs-row dir', onclick: () => go(e.path) }, h('span', { class: 'fs-ic' }, '📁'), h('span', { class: 'fs-nm' }, e.name));
      const on = f.localSel && f.localSel.path === e.path;
      return h('div', { class: 'fs-row file' + (on ? ' sel' : ''), onclick: () => { f.localSel = { path: e.path, name: e.name }; paint(); } },
        h('span', { class: 'fs-ic' }, '📄'), h('span', { class: 'fs-nm' }, e.name), h('span', { class: 'fs-sz' }, fmtBytes(e.size)));
    });
    body.replaceChildren(cur.entries.length ? h('div', null, rows) : h('p', { class: 'muted' }, 'Empty folder.'));
    paintActions();
  };
  const go = async (path) => {
    const f = S.files; if (!f) return;
    try { cur = await api('GET', `/api/fs/local?path=${encodeURIComponent(path)}`); }
    catch (e) { body.replaceChildren(h('p', { class: 'error' }, e.message)); return; }
    if (!S.files) return;
    f.localPath = cur.path; f.localSel = null;
    paint();
  };
  go('');
  return { el: h('div', { class: 'card fs-pane' }, head, body), reload: () => go(S.files ? S.files.localPath : '') };
}

// Right pane: one gaming PC's shared folder.
function pcPane() {
  const head = h('div', { class: 'fs-head' });
  const body = h('div', { class: 'fs-list' }, h('p', { class: 'muted' }, 'Pick a PC.'));
  let cur = null; // last FSListing

  const picker = () => {
    const online = (S.snap ? S.snap.pcs : []).filter(p => p.runtime && p.runtime.online);
    const sel = h('select', { onchange: () => { S.files.pcId = Number(sel.value); S.files.pcRel = ''; S.files.pcSel = null; load(); } },
      h('option', { value: 0 }, online.length ? '— choose a PC —' : 'No PCs online'),
      online.map(p => h('option', { value: p.id, selected: p.id === S.files.pcId }, p.name)));
    return sel;
  };
  const paint = () => {
    const f = S.files; if (!f) return;
    head.replaceChildren(
      picker(),
      h('button', { class: 'btn small', disabled: !cur || !f.pcRel, onclick: () => nav(relParent(f.pcRel)) }, '↑ Up'),
      h('span', { class: 'fs-path', title: f.pcRel || 'Shared' }, f.pcRel ? 'Shared/' + f.pcRel : 'Shared'));
    if (!cur) { body.replaceChildren(h('p', { class: 'muted' }, f.pcId ? 'Loading…' : 'Pick a PC to see its shared folder.')); paintActions(); return; }
    const rows = cur.entries.map(e => {
      if (e.isDir) return h('div', { class: 'fs-row dir', onclick: () => nav(relJoin(f.pcRel, e.name)) }, h('span', { class: 'fs-ic' }, '📁'), h('span', { class: 'fs-nm' }, e.name));
      const on = f.pcSel === e.name;
      return h('div', { class: 'fs-row file' + (on ? ' sel' : ''), onclick: () => { f.pcSel = e.name; paint(); } },
        h('span', { class: 'fs-ic' }, '📄'), h('span', { class: 'fs-nm' }, e.name), h('span', { class: 'fs-sz' }, fmtBytes(e.size)));
    });
    body.replaceChildren(cur.entries.length ? h('div', null, rows) : h('p', { class: 'muted' }, 'This folder is empty. Files dropped here (or sent from the left) appear here.'));
    paintActions();
  };
  const nav = async (rel) => {
    const f = S.files; if (!f) return;
    f.pcRel = rel; f.pcSel = null;
    paint(); // show the new path immediately
    try { const r = await api('GET', `/api/fs/pc/${f.pcId}?rel=${encodeURIComponent(rel)}`); if (!S.files) return; cur = r; }
    catch (e) { cur = null; body.replaceChildren(h('p', { class: 'error' }, e.message)); return; }
    paint();
  };
  const load = () => { cur = null; if (S.files && S.files.pcId) nav(''); else paint(); };
  load();
  // reload re-lists the folder currently open (keeps the admin's place after a send).
  return { el: h('div', { class: 'card fs-pane' }, head, body), reload: () => { cur = null; if (S.files && S.files.pcId) nav(S.files.pcRel); else paint(); } };
}

// The send/get action bar lives between the panes; repaint it whenever a
// selection changes so the buttons enable/disable correctly.
function paintActions() {
  const f = S.files; if (!f) return;
  let bar = document.getElementById('fs-actions');
  if (!bar) {
    bar = h('div', { class: 'fs-actions', id: 'fs-actions' });
    const panes = document.querySelector('.fs-panes');
    if (panes) panes.after(bar); else return;
  }
  const canSend = !!(f.localSel && f.pcId);
  const canGet = !!(f.pcSel && f.pcId && f.localPath);
  const send = h('button', { class: 'btn primary', disabled: !canSend, onclick: e => doSend(e.target) },
    f.localSel ? `Send “${f.localSel.name}” →` : 'Send → PC');
  const get = h('button', { class: 'btn primary', disabled: !canGet, onclick: e => doGet(e.target) },
    f.pcSel ? `← Get “${f.pcSel}”` : '← Get from PC');
  const hint = !f.pcId ? 'Pick a PC on the right.'
    : (!f.localSel && !f.pcSel ? 'Pick a file in either pane.'
      : (f.pcSel && !f.localPath ? 'Open a folder on the left to receive the file.' : ''));
  bar.replaceChildren(get, send, hint ? h('span', { class: 'muted small' }, hint) : null);
}

async function doSend(btn) {
  const f = S.files; if (!f || !f.localSel || !f.pcId) return;
  await act(btn, () => api('POST', '/api/fs/send', { pcId: f.pcId, srcPath: f.localSel.path, destRel: f.pcRel }), 'Sending…')
    .then(() => { if (S.files) { S.files.loadXfers(); setTimeout(() => S.files && S.files.refreshPC(), 400); } }).catch(() => {});
}

async function doGet(btn) {
  const f = S.files; if (!f || !f.pcSel || !f.pcId || !f.localPath) return;
  await act(btn, () => api('POST', '/api/fs/get', { pcId: f.pcId, srcRel: f.pcRel, name: f.pcSel, destDir: f.localPath }), 'Getting…')
    .then(() => { if (S.files) { S.files.loadXfers(); setTimeout(() => S.files && S.files.refreshLocal(), 400); } }).catch(() => {});
}

// ---------- Bookings ----------

function isoOf(d) { return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`; }
function timeInputVal(d) { return `${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`; }

function rerenderBookingsTab() { const m = document.getElementById('main'); if (m && S.tab === 'bookings') m.replaceChildren(renderBookings()); }

function renderBookings() {
  if (!S.bookView) S.bookView = 'timeline';
  if (!S.bookDay) S.bookDay = todayISO();
  const out = h('div', null, h('p', { class: 'muted' }, 'Loading…'));
  const label = h('b', { style: 'min-width:200px;text-align:center' });
  const span = S.bookView === 'timeline' ? 1 : 7;
  const load = async () => {
    const from = new Date(S.bookDay + 'T00:00:00');
    const to = new Date(from.getTime() + span * 86400000);
    if (S.bookView === 'timeline') label.textContent = from.toLocaleDateString([], { weekday: 'long', month: 'short', day: 'numeric' });
    else label.textContent = `${from.toLocaleDateString()} – ${new Date(to.getTime() - 86400000).toLocaleDateString()}`;
    try {
      const list = await api('GET', `/api/bookings?from=${Math.floor(from.getTime() / 1000)}&to=${Math.floor(to.getTime() / 1000)}`);
      out.replaceChildren(S.bookView === 'timeline' ? bookingsTimeline(list, from, load) : bookingsWeek(list, from, load));
    } catch (e) { out.replaceChildren(h('p', { class: 'error' }, e.message)); }
  };
  const nav = d => { const f = new Date(S.bookDay + 'T00:00:00'); f.setDate(f.getDate() + d * span); S.bookDay = isoOf(f); load(); };
  const viewBtn = (v, l) => h('button', { class: 'btn small' + (S.bookView === v ? ' primary' : ''), onclick: () => { if (S.bookView !== v) { S.bookView = v; rerenderBookingsTab(); } } }, l);
  load();
  return h('div', null,
    h('div', { class: 'toolbar' },
      h('button', { class: 'btn primary', onclick: () => bookingModal(null, load) }, '+ New booking'),
      h('span', { class: 'grow' }),
      viewBtn('timeline', 'Timeline'), viewBtn('list', 'Week list'),
      h('span', { style: 'width:10px' }),
      h('button', { class: 'btn small', onclick: () => nav(-1) }, '‹'),
      h('button', { class: 'btn small', onclick: () => { S.bookDay = todayISO(); load(); } }, 'Today'),
      label,
      h('button', { class: 'btn small', onclick: () => nav(1) }, '›')),
    out);
}

// bookingsTimeline draws a day grid: one row per PC, hours across, a bar per booking.
function bookingsTimeline(list, dayStart, reload) {
  const pcs = (S.snap ? S.snap.pcs : []);
  const d0 = dayStart.getTime(), d1 = d0 + 86400000;
  const clamp = ms => Math.max(d0, Math.min(d1, ms));
  const hOf = ms => (ms - d0) / 3600000;
  let minH = 24, maxH = 0;
  list.forEach(b => {
    const s = clamp(new Date(b.startAt).getTime()), e = clamp(new Date(b.endAt).getTime());
    if (e <= s) return;
    minH = Math.min(minH, hOf(s)); maxH = Math.max(maxH, hOf(e));
  });
  if (minH >= maxH) { minH = 12; maxH = 24; }
  minH = Math.floor(minH); maxH = Math.ceil(maxH);
  if (maxH - minH < 4) maxH = Math.min(24, minH + 4);
  const hourW = 64, span = maxH - minH, trackW = span * hourW;
  const left = ms => (hOf(ms) - minH) * hourW;

  const byPC = {};
  list.forEach(b => (b.pcIds || []).forEach(id => { (byPC[id] = byPC[id] || []).push(b); }));

  const ruler = h('div', { class: 'tl-ruler', style: `width:${trackW}px` },
    Array.from({ length: span }, (_, i) => h('div', { class: 'tl-hour', style: `left:${i * hourW}px;width:${hourW}px` }, `${String((minH + i) % 24).padStart(2, '0')}:00`)));

  const rows = pcs.map(pc => {
    const bars = (byPC[pc.id] || []).map(b => {
      const s = clamp(new Date(b.startAt).getTime()), e = clamp(new Date(b.endAt).getTime());
      if (e <= s) return null;
      const x = left(s), w = Math.max(10, left(e) - left(s));
      return h('div', { class: 'tl-bar st-' + b.status, style: `left:${x}px;width:${w}px`, title: `${b.who} · ${timeStr(b.startAt)}–${timeStr(b.endAt)} · ${(b.pcNames || []).join(', ')}`, onclick: () => bookingModal(b, reload) }, b.who);
    });
    return h('div', { class: 'tl-row' }, h('div', { class: 'tl-name' }, pc.name),
      h('div', { class: 'tl-track', style: `width:${trackW}px;background-size:${hourW}px 100%` }, bars));
  });
  if (!pcs.length) return h('div', { class: 'card' }, h('p', { class: 'muted' }, 'No PCs yet.'));
  return h('div', { class: 'card' }, h('div', { class: 'tl-scroll' },
    h('div', { class: 'tl-head' }, h('div', { class: 'tl-name tl-corner' }, 'PC'), ruler), rows));
}

const BOOK_BADGE = { scheduled: ['info', 'Scheduled'], started: ['ok', 'Started'], cancelled: ['', 'Cancelled'], noshow: ['warn', 'No-show'] };

function bookingsWeek(list, from, reload) {
  if (!list.length) return h('div', { class: 'card' }, h('p', { class: 'muted' }, 'No bookings this week. Click "New booking" to add one.'));
  const box = h('div');
  for (let i = 0; i < 7; i++) {
    const d = new Date(from.getTime() + i * 86400000);
    const todays = list.filter(b => new Date(b.startAt).toDateString() === d.toDateString());
    if (!todays.length) continue;
    box.append(h('div', { class: 'card' },
      h('h3', null, d.toLocaleDateString([], { weekday: 'long', month: 'short', day: 'numeric' })),
      h('div', { style: 'overflow-x:auto' }, h('table', null,
        h('tr', null, ['Time', 'Who', 'PCs', 'Status', ''].map(t => h('th', null, t))),
        todays.map(b => bookingRow(b, reload))))));
  }
  return box;
}

function bookingRow(b, reload) {
  const [cls, lbl] = BOOK_BADGE[b.status] || ['', b.status];
  const actions = [];
  if (b.status === 'scheduled' || b.status === 'started') {
    actions.push(h('button', { class: 'btn small', onclick: () => bookingModal(b, reload) }, 'Edit'));
    if (b.status === 'started') actions.push(h('button', { class: 'btn small danger', onclick: async e => { if (await confirmBox('No-show', `Release ${b.who}'s PCs as a no-show? Their sessions end with no charge.`, 'Release', true)) act(e.target, () => api('POST', `/api/bookings/${b.id}/noshow`), 'Released').then(reload).catch(() => {}); } }, 'No-show'));
    actions.push(h('button', { class: 'btn small', onclick: async e => { if (await confirmBox('Cancel booking', `Cancel ${b.who}'s booking?`, 'Cancel booking', true)) act(e.target, () => api('POST', `/api/bookings/${b.id}/cancel`), 'Cancelled').then(reload).catch(() => {}); } }, 'Cancel'));
  }
  return h('tr', null,
    h('td', null, `${timeStr(b.startAt)}–${timeStr(b.endAt)}`),
    h('td', null, b.who, b.phone ? h('div', { class: 'muted small' }, b.phone) : null, b.kind === 'group' ? h('div', { class: 'muted small' }, `group · ${b.members} member(s)`) : null, b.note ? h('div', { class: 'muted small' }, b.note) : null),
    h('td', { class: 'small' }, (b.pcNames || []).join(', ')),
    h('td', null, h('span', { class: 'badge ' + cls }, lbl)),
    h('td', null, h('div', { class: 'row' }, actions)));
}

async function bookingModal(existing, reload) {
  let clients = [], groups = [];
  try { [clients, groups] = await Promise.all([api('GET', '/api/clients'), api('GET', '/api/groups')]); }
  catch (e) { toast(e.message, 'error'); return; }

  const start = existing ? new Date(existing.startAt) : (() => { const d = new Date(); d.setMinutes(0, 0, 0); d.setHours(d.getHours() + 1); return d; })();
  const end = existing ? new Date(existing.endAt) : new Date(start.getTime() + 2 * 3600000);

  let mode = existing && existing.kind === 'group' ? 'group' : 'client';
  const modeBtns = [['client', 'Individual client'], ['group', 'Group']].map(([k, l]) =>
    h('button', { type: 'button', class: 'btn', onclick: () => { mode = k; drawPicker(); } }, l));
  const pickerBox = h('div');
  let picker = null;
  function drawPicker() {
    modeBtns.forEach((btn, i) => btn.classList.toggle('primary', ['client', 'group'][i] === mode));
    if (mode === 'client') {
      picker = h('select', null, h('option', { value: 0 }, '— choose a client —'), clients.map(c => h('option', { value: c.id }, `${c.name} · ${c.phone}`)));
      if (existing && existing.clientId) picker.value = String(existing.clientId);
      pickerBox.replaceChildren(clients.length ? picker : h('p', { class: 'muted small' }, 'No clients yet — add one in the Clients tab first.'));
    } else {
      picker = h('select', null, h('option', { value: 0 }, '— choose a group —'), groups.map(g => h('option', { value: g.id }, `${g.name} (${g.members.length})`)));
      if (existing && existing.groupId) picker.value = String(existing.groupId);
      pickerBox.replaceChildren(groups.length ? picker : h('p', { class: 'muted small' }, 'No groups yet — make one in the Clients tab first.'));
    }
  }

  const dateIn = h('input', { type: 'date', value: isoOf(start) });
  const startIn = h('input', { type: 'time', value: timeInputVal(start) });
  const endIn = h('input', { type: 'time', value: timeInputVal(end) });
  const note = h('input', { type: 'text', value: existing ? existing.note || '' : '', placeholder: 'optional' });

  const pcs = (S.snap ? S.snap.pcs : []);
  const checks = pcs.map(pc => {
    const cb = h('input', { type: 'checkbox', value: pc.id });
    if (existing && (existing.pcIds || []).includes(pc.id)) cb.checked = true;
    return { pc, cb, el: h('label', { class: 'pcpick' }, cb, pc.name) };
  });
  const pcBox = h('div', { class: 'pcpicks' }, checks.map(c => c.el));

  const chosenPCs = () => checks.filter(c => c.cb.checked).map(c => c.pc.id);
  const btn = h('button', { class: 'btn primary' }, existing ? 'Save booking' : 'Create booking');
  const m = modal(existing ? 'Edit booking' : 'New booking', h('div', null,
    h('div', { class: 'muted small', style: 'margin-bottom:4px' }, 'Booked for'),
    h('div', { class: 'row', style: 'margin-bottom:6px' }, modeBtns), pickerBox,
    h('div', { class: 'section' },
      h('div', { class: 'row' }, field('Day', dateIn), field('From', startIn), field('To', endIn)),
      h('div', { class: 'muted small' }, 'If "To" is earlier than "From", it counts as overnight (next day).')),
    h('div', { class: 'section' }, h('div', { class: 'muted small', style: 'margin-bottom:4px' }, 'PCs'), pcBox),
    field('Note', note)),
  [h('button', { class: 'btn', onclick: () => m.close() }, 'Cancel'), btn]);
  drawPicker();

  async function submit(force) {
    const whoId = Number(picker && picker.value);
    if (!whoId) { toast(mode === 'client' ? 'Choose a client.' : 'Choose a group.', 'error'); return; }
    const ids = chosenPCs();
    if (!ids.length) { toast('Choose at least one PC.', 'error'); return; }
    const startD = new Date(`${dateIn.value}T${startIn.value}`);
    let endD = new Date(`${dateIn.value}T${endIn.value}`);
    if (isNaN(startD) || isNaN(endD)) { toast('Enter a valid day and times.', 'error'); return; }
    if (endD <= startD) endD = new Date(endD.getTime() + 86400000);
    const body = { note: note.value, pcIds: ids, start: Math.floor(startD.getTime() / 1000), end: Math.floor(endD.getTime() / 1000), force };
    if (mode === 'group') body.groupId = whoId; else body.clientId = whoId;
    try {
      btn.disabled = true;
      const r = existing ? await api('PUT', `/api/bookings/${existing.id}`, body) : await api('POST', '/api/bookings', body);
      if (r.booking) { toast('Booking saved', 'ok'); m.close(); reload(); return; }
      if (r.conflicts && r.conflicts.length) {
        const txt = r.conflicts.map(c => `${c.who} ${timeStr(c.startAt)}–${timeStr(c.endAt)} on ${(c.pcNames || []).join(', ')}`).join('; ');
        if (await confirmBox('Overlap', `This clashes with: ${txt}. Book anyway?`, 'Book anyway', true)) { btn.disabled = false; submit(true); return; }
      }
    } catch (e) { toast(e.message, 'error'); }
    finally { btn.disabled = false; }
  }
  btn.onclick = () => submit(false);
}

// ---------- Clients & groups ----------

function renderClients() {
  const wrap = h('div');
  const groups = groupsCard();
  wrap.append(clientsCard(() => groups.reload()), groups.el);
  return wrap;
}

function clientsCard(onChange) {
  const box = h('div', null, h('p', { class: 'muted' }, 'Loading…'));
  const search = h('input', { type: 'text', placeholder: 'Search by name or phone…', style: 'flex:1' });
  let timer = null;
  const load = async () => {
    try {
      const list = await api('GET', `/api/clients?q=${encodeURIComponent(search.value.trim())}`);
      box.replaceChildren(list.length ? h('div', { style: 'overflow-x:auto' }, h('table', null,
        h('tr', null, ['Name', 'Phone', 'Note', ''].map(t => h('th', null, t))),
        list.map(c => {
          const name = h('input', { type: 'text', value: c.name, style: 'width:140px' });
          const phone = h('input', { type: 'text', value: c.phone, style: 'width:120px' });
          const note = h('input', { type: 'text', value: c.note || '', placeholder: 'e.g. VIP, banned', style: 'width:160px' });
          const save = h('button', { class: 'btn small primary', onclick: e => act(e.target, () => api('PUT', `/api/clients/${c.id}`, { name: name.value, phone: phone.value, note: note.value }), 'Saved').catch(() => {}) }, 'Save');
          const del = h('button', { class: 'btn small danger', onclick: async e => { if (await confirmBox('Delete client', `Delete ${c.name}? Booking history is kept.`, 'Delete', true)) { await act(e.target, () => api('DELETE', `/api/clients/${c.id}`), 'Deleted').then(() => { load(); onChange && onChange(); }).catch(() => {}); } } }, 'Delete');
          return h('tr', null, h('td', null, name), h('td', null, phone), h('td', null, note), h('td', null, save, ' ', del));
        }))) : h('p', { class: 'muted' }, search.value.trim() ? 'No clients match.' : 'No clients yet.'));
    } catch (e) { box.replaceChildren(h('p', { class: 'error' }, e.message)); }
  };
  search.addEventListener('input', () => { clearTimeout(timer); timer = setTimeout(load, 250); });
  load();

  const nName = h('input', { type: 'text', placeholder: 'Name', style: 'width:140px' });
  const nPhone = h('input', { type: 'text', placeholder: 'Phone', style: 'width:120px' });
  const nNote = h('input', { type: 'text', placeholder: 'Note (optional)', style: 'width:160px' });
  const add = h('button', { class: 'btn primary' }, 'Add client');
  add.onclick = async () => {
    if (!nName.value.trim() || !nPhone.value.trim()) { toast('Name and phone are required.', 'error'); return; }
    await act(add, () => api('POST', '/api/clients', { name: nName.value, phone: nPhone.value, note: nNote.value }), 'Client added').then(() => { nName.value = nPhone.value = nNote.value = ''; load(); onChange && onChange(); }).catch(() => {});
  };
  return h('div', { class: 'card' }, h('h2', null, 'Clients'),
    h('p', { class: 'muted small' }, 'Name and phone are both required. They don\'t have to be unique — the same name or number can appear more than once.'),
    h('div', { class: 'row', style: 'margin-bottom:10px' }, search),
    box,
    h('h3', null, 'Add a client'),
    h('div', { class: 'row' }, nName, nPhone, nNote, add));
}

function groupsCard() {
  const box = h('div', null, h('p', { class: 'muted' }, 'Loading…'));
  let clients = [];
  const clientName = id => { const c = clients.find(x => x.id === id); return c ? `${c.name} (${c.phone})` : `#${id}`; };
  const clientPicker = () => {
    const sel = h('select', null, h('option', { value: 0 }, '— choose a client —'), clients.map(c => h('option', { value: c.id }, `${c.name} · ${c.phone}`)));
    return sel;
  };
  const load = async () => {
    try {
      clients = await api('GET', '/api/clients');
      // refill the "first member" picker now that clients are loaded
      firstMember.replaceChildren(h('option', { value: 0 }, '— choose a client —'),
        ...clients.map(c => h('option', { value: c.id }, `${c.name} · ${c.phone}`)));
      const groups = await api('GET', '/api/groups');
      box.replaceChildren(groups.length ? h('div', null, groups.map(g => {
        const name = h('input', { type: 'text', value: g.name, style: 'width:160px' });
        const note = h('input', { type: 'text', value: g.note || '', placeholder: 'note', style: 'width:160px' });
        const members = h('div', { class: 'row', style: 'margin:6px 0' }, (g.members || []).map(m =>
          h('span', { class: 'badge info' }, `${m.name}`,
            h('button', { class: 'xchip', title: 'Remove from group', onclick: async e => { await act(e.target, () => api('DELETE', `/api/groups/${g.id}/members/${m.id}`), 'Removed').then(load).catch(() => {}); } }, '×'))));
        const addSel = clientPicker();
        const addBtn = h('button', { class: 'btn small', onclick: e => { const cid = Number(addSel.value); if (!cid) return; act(e.target, () => api('POST', `/api/groups/${g.id}/members`, { clientId: cid }), 'Added').then(load).catch(() => {}); } }, 'Add member');
        const save = h('button', { class: 'btn small primary', onclick: e => act(e.target, () => api('PUT', `/api/groups/${g.id}`, { name: name.value, note: note.value }), 'Saved').catch(() => {}) }, 'Save');
        const del = h('button', { class: 'btn small danger', onclick: async e => { if (await confirmBox('Delete group', `Delete group ${g.name}? The clients themselves are kept.`, 'Delete', true)) { await act(e.target, () => api('DELETE', `/api/groups/${g.id}`), 'Deleted').then(load).catch(() => {}); } } }, 'Delete');
        return h('div', { class: 'card', style: 'margin-bottom:10px' },
          h('div', { class: 'row' }, h('b', null, 'Group:'), name, note, save, del),
          h('div', { class: 'muted small', style: 'margin-top:6px' }, `${(g.members || []).length} member(s):`),
          members,
          h('div', { class: 'row' }, addSel, addBtn));
      })) : h('p', { class: 'muted' }, 'No groups yet.'));
    } catch (e) { box.replaceChildren(h('p', { class: 'error' }, e.message)); }
  };
  load();

  const gName = h('input', { type: 'text', placeholder: 'Group name', style: 'width:160px' });
  const gNote = h('input', { type: 'text', placeholder: 'Note (optional)', style: 'width:160px' });
  const firstMember = clientPicker();
  const create = h('button', { class: 'btn primary' }, 'Create group');
  create.onclick = async () => {
    const cid = Number(firstMember.value);
    if (!gName.value.trim()) { toast('Group name is required.', 'error'); return; }
    if (!cid) { toast('Pick at least one client for the group.', 'error'); return; }
    await act(create, () => api('POST', '/api/groups', { name: gName.value, note: gNote.value, memberIds: [cid] }), 'Group created').then(() => { gName.value = gNote.value = ''; load(); }).catch(() => {});
  };
  const el = h('div', { class: 'card' }, h('h2', null, 'Groups'),
    h('p', { class: 'muted small' }, 'A group is a set of clients booked together (e.g. a team). It needs at least one client; add or remove members anytime.'),
    box,
    h('h3', null, 'Create a group'),
    h('div', { class: 'row' }, gName, gNote, h('span', { class: 'muted small' }, 'first member:'), firstMember, create));
  return { el, reload: load };
}

// ---------- Shift & cash ----------

function shiftLive() {
  const sh = S.snap.shift;
  if (!sh) return h('span');
  return h('div', { class: 'stats' },
    stat('Opened', `${dateTimeStr(sh.openedAt)} by ${sh.adminName}`),
    stat('Opening cash', money(sh.openingCash)),
    stat('Taken this shift', money(sh.takings)),
    stat('Cash that should be in the drawer', money(sh.cashNow)),
    stat('Transactions', sh.txCount));
}

function stat(label, value) { return h('div', { class: 'stat' }, h('div', { class: 'v' }, value), h('div', { class: 'l' }, label)); }

function renderShift() {
  const sh = S.snap.shift;
  const wrap = h('div');
  document.getElementById('main').dataset.shiftOpen = sh ? '1' : '0';
  if (!sh) {
    const cash = h('input', { type: 'text', value: '0.00' });
    const note = h('input', { type: 'text' });
    const btn = h('button', { class: 'btn primary' }, 'Open shift');
    btn.onclick = async () => {
      const v = parseMoney(cash.value);
      if (isNaN(v) || v < 0) { toast('Enter the cash in the drawer, e.g. 50.00', 'error'); return; }
      try { await act(btn, () => api('POST', '/api/shift/open', { openingCash: v, note: note.value }), 'Shift opened'); rerender(); } catch (e) { /* shown */ }
    };
    wrap.append(h('div', { class: 'card' }, h('h2', null, 'Open a shift'),
      h('p', { class: 'muted' }, 'Count the cash in the drawer. Paid sessions can only start while a shift is open, so every payment is tied to a cashier.'),
      field(`Cash in the drawer now (${cur()})`, cash), field('Note', note), btn));
  } else {
    const counted = h('input', { type: 'text', placeholder: fmtMoney(sh.cashNow) });
    const note = h('input', { type: 'text' });
    const result = h('div');
    const btn = h('button', { class: 'btn primary' }, 'Close shift');
    btn.onclick = async () => {
      const v = parseMoney(counted.value);
      if (isNaN(v) || v < 0) { toast('Enter the counted cash, e.g. 250.00', 'error'); return; }
      if (!await confirmBox('Close shift', `Close the shift with ${money(v)} counted?`, 'Close shift')) return;
      try {
        const r = await act(btn, () => api('POST', '/api/shift/close', { countedCash: v, note: note.value }));
        const diff = r.closingCash - r.expected;
        result.replaceChildren(h('div', { class: 'card' }, h('h2', null, 'Shift closed'),
          h('dl', { class: 'kv' }, h('dt', null, 'Expected'), h('dd', null, money(r.expected)), h('dt', null, 'Counted'), h('dd', null, money(r.closingCash)),
            h('dt', null, 'Difference'), h('dd', null, h('b', { style: `color:var(--${diff === 0 ? 'green' : 'red'})` }, (diff > 0 ? '+' : '') + money(diff))))));
        document.getElementById('main').dataset.shiftOpen = '0';
      } catch (e) { /* shown */ }
    };
    wrap.append(h('div', { class: 'card' }, h('h2', null, 'Current shift'), h('div', { id: 'shift-live' }, shiftLive())),
      h('div', { class: 'card' }, h('h2', null, 'Close shift'),
        field(`Cash counted in the drawer (${cur()})`, counted), field('Note', note), btn), result);
  }
  const amt = h('input', { type: 'text', placeholder: 'e.g. -5.00 or 10.00' });
  const why = h('input', { type: 'text' });
  const cbtn = h('button', { class: 'btn' }, 'Record correction');
  cbtn.onclick = async () => {
    const v = parseMoney(amt.value);
    if (isNaN(v) || v === 0) { toast('Enter a non-zero amount', 'error'); return; }
    try { await act(cbtn, () => api('POST', '/api/corrections', { amount: v, reason: why.value }), 'Correction recorded'); amt.value = ''; why.value = ''; } catch (e) { /* shown */ }
  };
  wrap.append(h('div', { class: 'card' }, h('h2', null, 'Cash correction'),
    h('p', { class: 'muted small' }, 'For money that moved outside a session (a mistake, change given back). Positive = money in, negative = money out. It is logged with your name and cannot be edited later.'),
    h('div', { class: 'row' }, field('Amount', amt), field('Reason (required)', why)), cbtn));
  return wrap;
}

// ---------- Reports ----------

const KIND_LABEL = { session_start: 'Prepaid sessions', add_time: 'Added time', open_close: 'Open sessions', refund: 'Refunds', correction: 'Corrections' };

function renderReports() {
  const date = h('input', { type: 'date', value: S.reportDate });
  const out = h('div', null, h('p', { class: 'muted' }, 'Loading…'));
  const load = async () => {
    S.reportDate = date.value || todayISO();
    try { out.replaceChildren(reportView(await api('GET', `/api/reports/daily?date=${S.reportDate}`))); } catch (e) { out.replaceChildren(h('p', { class: 'error' }, e.message)); }
  };
  date.addEventListener('change', load);
  load();
  return h('div', null, h('div', { class: 'toolbar' }, 'Day:', date, h('button', { class: 'btn', onclick: load }, 'Refresh')), out);
}

function reportView(r) {
  const maxH = Math.max(1, ...r.byHour.map(v => Math.abs(v)));
  return h('div', null,
    h('div', { class: 'stats', style: 'margin-bottom:14px' },
      stat('Revenue', money(r.revenue)), stat('Sessions', r.sessions), stat('Paid sessions', r.paidSessions), stat('Hours played', r.hoursPlayed),
      Object.entries(r.byKind).map(([k, v]) => stat(KIND_LABEL[k] || k, money(v)))),
    h('div', { class: 'card' }, h('h2', null, 'Revenue by hour'),
      h('div', { class: 'bars' }, r.byHour.map((v, i) => h('div', { title: `${i}:00 — ${money(v)}`, style: `height:${Math.round(Math.max(0, v) / maxH * 100)}%` }))),
      h('div', { class: 'bars-labels' }, r.byHour.map((_, i) => h('span', null, i % 3 === 0 ? i : '')))),
    h('div', { class: 'card' }, h('h2', null, 'Busiest PCs'),
      (r.byPc || []).length ? h('table', null, h('tr', null, h('th', null, 'PC'), h('th', { class: 'num' }, 'Revenue'), h('th', { class: 'num' }, 'Sessions'), h('th', { class: 'num' }, 'Hours')),
        r.byPc.map(p => h('tr', null, h('td', null, p.name), h('td', { class: 'num' }, money(p.revenue)), h('td', { class: 'num' }, p.sessions), h('td', { class: 'num' }, p.hours)))) : h('p', { class: 'muted' }, 'No sessions.')),
    h('div', { class: 'card' }, h('h2', null, 'By cashier'),
      (r.byAdmin || []).length ? h('table', null, h('tr', null, h('th', null, 'Cashier'), h('th', { class: 'num' }, 'Revenue'), h('th', { class: 'num' }, 'Payments')),
        r.byAdmin.map(a => h('tr', null, h('td', null, a.name), h('td', { class: 'num' }, money(a.revenue)), h('td', { class: 'num' }, a.count)))) : h('p', { class: 'muted' }, 'No payments.')),
    h('div', { class: 'card' }, h('h2', null, 'Shifts'),
      r.shifts.length ? h('table', null, h('tr', null, h('th', null, 'Opened'), h('th', null, 'Closed'), h('th', { class: 'num' }, 'Expected'), h('th', { class: 'num' }, 'Counted'), h('th', { class: 'num' }, 'Difference')),
        r.shifts.map(s => h('tr', null, h('td', null, dateTimeStr(s.openedAt)), h('td', null, s.closedAt ? dateTimeStr(s.closedAt) : 'open'),
          h('td', { class: 'num' }, s.closedAt ? money(s.expected) : '—'), h('td', { class: 'num' }, s.closedAt ? money(s.closingCash) : '—'),
          h('td', { class: 'num' }, s.closedAt ? money(s.closingCash - s.expected) : '—')))) : h('p', { class: 'muted' }, 'No shifts.')),
    h('div', { class: 'card' }, h('h2', null, 'All payments'),
      r.transactions.length ? h('table', null, h('tr', null, h('th', null, 'Time'), h('th', null, 'PC'), h('th', null, 'Type'), h('th', null, 'Cashier'), h('th', null, 'Reason'), h('th', { class: 'num' }, 'Amount')),
        r.transactions.map(t => h('tr', null, h('td', null, timeStr(t.createdAt)), h('td', null, t.pcName || '—'), h('td', null, KIND_LABEL[t.kind] || t.kind),
          h('td', null, t.adminName), h('td', null, t.reason || ''), h('td', { class: 'num' }, money(t.amount))))) : h('p', { class: 'muted' }, 'No payments.')));
}

// ---------- Activity log ----------

function renderLog() {
  const date = h('input', { type: 'date', value: S.auditDate });
  const out = h('div', null, h('p', { class: 'muted' }, 'Loading…'));
  const load = async () => {
    S.auditDate = date.value || todayISO();
    try {
      const rows = await api('GET', `/api/audit?date=${S.auditDate}`);
      const pcName = id => { const p = pcById(id); return p ? p.name : (id ? `#${id}` : ''); };
      out.replaceChildren(rows.length ? h('div', { class: 'card' }, h('table', null,
        h('tr', null, h('th', null, 'Time'), h('th', null, 'Who'), h('th', null, 'Action'), h('th', null, 'PC'), h('th', null, 'Details')),
        rows.map(e => h('tr', null, h('td', null, timeStr(e.createdAt)), h('td', null, e.admin), h('td', null, e.action.replaceAll('_', ' ')), h('td', null, pcName(e.pcId)), h('td', { class: 'small' }, e.details)))))
        : h('p', { class: 'muted' }, 'Nothing recorded on this day.'));
    } catch (e) { out.replaceChildren(h('p', { class: 'error' }, e.message)); }
  };
  date.addEventListener('change', load);
  load();
  return h('div', null, h('div', { class: 'toolbar' }, 'Day:', date, h('button', { class: 'btn', onclick: load }, 'Refresh')), out);
}

// ---------- Settings ----------

function renderSettings() {
  const wrap = h('div');
  wrap.append(accountCard());
  if (!isOwner()) return wrap;
  wrap.append(clubCard(), dataCard(), installCard(), zonesCard(), packagesCard(), pcsCard(), staffCard());
  return wrap;
}

function dataCard() {
  const today = todayISO();
  const from = h('input', { type: 'date', value: today });
  const to = h('input', { type: 'date', value: today });
  const dl = (path) => { window.location.href = path; };
  const importFile = h('input', { type: 'file', accept: '.db' });
  const importMsg = h('div', { class: 'small', style: 'margin-top:6px' });
  const importBtn = h('button', { class: 'btn' }, 'Upload & restore');
  importBtn.onclick = async () => {
    const f = importFile.files[0];
    if (!f) { toast('Choose a .db file first.', 'error'); return; }
    if (!await confirmBox('Restore database', 'This replaces ALL current data when the server restarts. The current data is kept as a .replaced copy. Continue?', 'Upload', true)) return;
    const fd = new FormData(); fd.append('file', f);
    importBtn.disabled = true;
    try {
      const res = await fetch('/api/import/db', { method: 'POST', headers: { 'X-NextGaming': '1' }, credentials: 'same-origin', body: fd });
      const d = await res.json().catch(() => ({}));
      if (!res.ok) throw new Error(d.error || 'Upload failed');
      importMsg.textContent = d.message || 'Uploaded. Restart the server to apply.';
      importMsg.style.color = 'var(--green)';
      toast('Uploaded. Restart the server to apply.', 'ok');
    } catch (e) { importMsg.textContent = e.message; importMsg.style.color = 'var(--red)'; }
    finally { importBtn.disabled = false; }
  };
  return h('div', { class: 'card' }, h('h2', null, 'Data: backup, export, import'),
    h('h3', null, 'Full backup'),
    h('p', { class: 'muted small' }, 'Downloads the whole database (all history and settings). Keep a copy off this PC.'),
    h('button', { class: 'btn primary', onclick: () => dl('/api/export/db') }, 'Download database'),
    h('h3', null, 'Export to Excel (CSV)'),
    h('div', { class: 'row' }, field('From', from), field('To', to),
      h('button', { class: 'btn', onclick: () => dl(`/api/export/payments.csv?from=${from.value}&to=${to.value}`) }, 'Payments CSV'),
      h('button', { class: 'btn', onclick: () => dl(`/api/export/sessions.csv?from=${from.value}&to=${to.value}`) }, 'Sessions CSV')),
    h('h3', null, 'Restore from a backup'),
    h('p', { class: 'muted small' }, 'Replaces all current data with an uploaded database file. Takes effect after the server restarts.'),
    h('div', { class: 'row' }, importFile, importBtn), importMsg);
}

function accountCard() {
  const curPw = h('input', { type: 'password', autocomplete: 'current-password' });
  const newPw = h('input', { type: 'password', autocomplete: 'new-password' });
  const btn = h('button', { class: 'btn' }, 'Change my password');
  btn.onclick = async () => {
    try { await act(btn, () => api('POST', `/api/admins/${S.me.id}/password`, { current: curPw.value, password: newPw.value }), 'Password changed'); curPw.value = newPw.value = ''; } catch (e) { /* shown */ }
  };
  return h('div', { class: 'card' }, h('h2', null, 'My account'), h('div', { class: 'row' }, field('Current password', curPw), field('New password', newPw)), btn);
}

function clubCard() {
  const st = S.snap.settings;
  const name = h('input', { type: 'text', value: st.clubName });
  const currency = h('input', { type: 'text', value: st.currency, style: 'width:80px' });
  const grace = h('input', { type: 'number', min: 0, value: st.graceSeconds, style: 'width:90px' });
  const minCharge = h('input', { type: 'number', min: 0, value: st.minChargeMinutes, style: 'width:90px' });
  const warn = h('input', { type: 'text', value: (st.warnMinutes || []).join(', '), style: 'width:120px' });
  const autofix = h('input', { type: 'checkbox', checked: st.autoFixWol });
  const reqShift = h('input', { type: 'checkbox', checked: st.requireShift });
  const btn = h('button', { class: 'btn primary' }, 'Save');
  btn.onclick = () => act(btn, () => api('PUT', '/api/settings', {
    clubName: name.value, currency: currency.value, graceSeconds: Number(grace.value) || 0, minChargeMinutes: Number(minCharge.value) || 0,
    warnMinutes: warn.value.split(/[ ,]+/).map(Number).filter(n => n > 0), autoFixWol: autofix.checked, requireShift: reqShift.checked,
  }), 'Settings saved').catch(() => {});
  return h('div', { class: 'card' }, h('h2', null, 'Club'),
    h('div', { class: 'row' }, field('Club name (shown on lock screens)', name), field('Currency', currency)),
    h('div', { class: 'row' },
      field('Free if ended within (seconds)', grace, 'Open sessions only'),
      field('Minimum charge (minutes)', minCharge, 'Open sessions only'),
      field('Warn players at (minutes left)', warn)),
    h('label', { class: 'check' }, autofix, 'Let PCs fix their Wake-on-LAN settings in Windows automatically'),
    h('label', { class: 'check' }, reqShift, 'Require an open shift before taking money'),
    h('div', { class: 'row' }, btn, h('button', { class: 'btn', onclick: e => act(e.target, () => api('POST', '/api/backup'), r => `Backup written: ${r.path}`).catch(() => {}) }, 'Back up database now')));
}

function installCard() {
  const box = h('div', null, h('p', { class: 'muted' }, 'Loading…'));
  api('GET', '/api/club-key').then(k => {
    const ip = (k.serverIPs && k.serverIPs[0]) || 'SERVER-IP';
    box.replaceChildren(...[
      h('p', null, 'Club key: ', h('code', { class: 'key' }, k.clubKey)),
      h('p', null, 'On each gaming PC, copy the NextGaming folder, then run this in an Administrator command prompt inside it:'),
      h('pre', { class: 'cmd' }, `nextgaming-agent.exe install -server ${ip}:${k.agentPort} -key ${k.clubKey}`),
      k.serverIPs && k.serverIPs.length > 1 ? h('p', { class: 'muted small' }, `This computer has several addresses: ${k.serverIPs.join(', ')}. Use the one on the club network.`) : null,
      h('p', { class: 'muted small' }, 'The PC appears on the PCs tab within seconds. See the README for locking down the gamer account.'),
    ].filter(Boolean));
  }).catch(e => box.replaceChildren(h('p', { class: 'error' }, e.message)));
  return h('div', { class: 'card' }, h('h2', null, 'Add gaming PCs'), box);
}

function zonesCard() {
  const rows = S.snap.zones.map(z => {
    const name = h('input', { type: 'text', value: z.name });
    const rate = h('input', { type: 'text', value: fmtMoney(z.hourlyRate), style: 'width:100px' });
    return h('tr', null, h('td', null, name), h('td', null, rate, ' ', cur(), '/h'), h('td', null,
      h('button', { class: 'btn small', onclick: e => { const r = parseMoney(rate.value); if (isNaN(r)) return toast('Bad rate', 'error'); act(e.target, () => api('PUT', `/api/zones/${z.id}`, { name: name.value, hourlyRate: r }), 'Zone saved').catch(() => {}); } }, 'Save'), ' ',
      h('button', { class: 'btn small danger', onclick: async e => { if (await confirmBox('Delete zone', `Delete zone ${z.name}?`, 'Delete', true)) { await act(e.target, () => api('DELETE', `/api/zones/${z.id}`), 'Zone deleted').catch(() => {}); rerender(); } } }, 'Delete')));
  });
  const nName = h('input', { type: 'text', placeholder: 'e.g. VIP' });
  const nRate = h('input', { type: 'text', placeholder: '15.00', style: 'width:100px' });
  const add = h('button', { class: 'btn small primary' }, 'Add zone');
  add.onclick = async () => {
    const r = parseMoney(nRate.value);
    if (isNaN(r)) return toast('Enter an hourly rate like 15.00', 'error');
    await act(add, () => api('POST', '/api/zones', { name: nName.value, hourlyRate: r }), 'Zone added').catch(() => {});
    setTimeout(rerender, 300);
  };
  return h('div', { class: 'card' }, h('h2', null, 'Zones and hourly rates'),
    h('table', null, h('tr', null, h('th', null, 'Name'), h('th', null, 'Hourly rate'), h('th', null, '')), rows,
      h('tr', null, h('td', null, nName), h('td', null, nRate, ' ', cur(), '/h'), h('td', null, add))));
}

function hourSelect(v, allowNone) {
  const s = h('select', null, allowNone ? h('option', { value: -1 }, '—') : null,
    Array.from({ length: 24 }, (_, i) => h('option', { value: i }, `${String(i).padStart(2, '0')}:00`)));
  s.value = String(v);
  return s;
}

function packagesCard() {
  const zoneSel = v => { const s = h('select', null, h('option', { value: 0 }, 'All zones'), S.snap.zones.map(z => h('option', { value: z.id }, z.name))); s.value = String(v); return s; };
  const row = p => {
    const name = h('input', { type: 'text', value: p.name, style: 'width:150px' });
    const price = h('input', { type: 'text', value: p.id ? fmtMoney(p.price) : '', placeholder: '25.00', style: 'width:80px' });
    const minutes = h('input', { type: 'number', min: 0, value: p.minutes, style: 'width:70px' });
    const ends = hourSelect(p.endsAtHour, true);
    const from = hourSelect(p.availableFrom, true);
    const to = hourSelect(p.availableTo, true);
    const zone = zoneSel(p.zoneId);
    const active = h('input', { type: 'checkbox', checked: p.active });
    const collect = () => ({ name: name.value, price: parseMoney(price.value), minutes: Number(minutes.value) || 0, endsAtHour: Number(ends.value),
      availableFrom: Number(from.value), availableTo: Number(to.value), zoneId: Number(zone.value), active: active.checked });
    const save = h('button', { class: 'btn small primary' }, p.id ? 'Save' : 'Add');
    save.onclick = async () => {
      const body = collect();
      if (isNaN(body.price)) return toast('Enter a price like 25.00', 'error');
      if (p.id) await act(save, () => api('PUT', `/api/packages/${p.id}`, body), 'Package saved').catch(() => {});
      else { await act(save, () => api('POST', '/api/packages', body), 'Package added').catch(() => {}); setTimeout(rerender, 300); }
    };
    const del = p.id ? h('button', { class: 'btn small danger', onclick: async e => { if (await confirmBox('Delete package', `Delete ${p.name}?`, 'Delete', true)) { await act(e.target, () => api('DELETE', `/api/packages/${p.id}`), 'Deleted').catch(() => {}); setTimeout(rerender, 300); } } }, 'Delete') : null;
    return h('tr', null, h('td', null, name), h('td', null, price), h('td', null, minutes), h('td', null, ends), h('td', null, from, ' – ', to), h('td', null, zone), h('td', null, active), h('td', null, save, ' ', del));
  };
  return h('div', { class: 'card' }, h('h2', null, 'Packages'),
    h('p', { class: 'muted small' }, '"Ends at" makes a package last until that hour (night pack). "Sold" limits when it can be sold. Leave both as — for a normal package.'),
    h('div', { style: 'overflow-x:auto' }, h('table', null,
      h('tr', null, ['Name', 'Price', 'Minutes', 'Ends at', 'Sold', 'Zone', 'Active', ''].map(t => h('th', null, t))),
      S.snap.packages.map(row),
      row({ id: 0, name: '', price: 0, minutes: 60, endsAtHour: -1, availableFrom: -1, availableTo: -1, zoneId: 0, active: true }))));
}

function pcsCard() {
  const zoneSel = v => { const s = h('select', null, h('option', { value: 0 }, '— none —'), S.snap.zones.map(z => h('option', { value: z.id }, z.name))); s.value = String(v); return s; };
  return h('div', { class: 'card' }, h('h2', null, 'PCs'),
    S.snap.pcs.length ? h('div', { style: 'overflow-x:auto' }, h('table', null,
      h('tr', null, ['Name', 'Zone', 'Order', 'Computer', 'MAC', ''].map(t => h('th', null, t))),
      S.snap.pcs.map(pc => {
        const name = h('input', { type: 'text', value: pc.name, style: 'width:110px' });
        const zone = zoneSel(pc.zoneId);
        const order = h('input', { type: 'number', value: pc.sortOrder, style: 'width:70px' });
        return h('tr', null, h('td', null, name), h('td', null, zone), h('td', null, order),
          h('td', { class: 'small' }, pc.hostname, h('br'), [pc.boardMaker, pc.boardProduct].filter(Boolean).join(' ')),
          h('td', { class: 'small' }, pc.mac || '—'),
          h('td', null, h('button', { class: 'btn small primary', onclick: e => act(e.target, () => api('PUT', `/api/pcs/${pc.id}`, { name: name.value, zoneId: Number(zone.value), sortOrder: Number(order.value) || 0 }), 'PC saved').catch(() => {}) }, 'Save'), ' ',
            h('button', { class: 'btn small danger', onclick: async e => { if (await confirmBox('Remove PC', `Remove ${pc.name} from the list? History is kept. If its agent connects again, it comes back.`, 'Remove', true)) { await act(e.target, () => api('DELETE', `/api/pcs/${pc.id}`), 'Removed').catch(() => {}); setTimeout(rerender, 300); } } }, 'Remove')));
      }))) : h('p', { class: 'muted' }, 'No PCs yet.'),
    h('p', { class: 'muted small' }, 'Lower "Order" numbers show first. PCs with the same order are sorted by name.'));
}

function staffCard() {
  const box = h('div', null, h('p', { class: 'muted' }, 'Loading…'));
  const load = () => api('GET', '/api/admins').then(list => {
    box.replaceChildren(h('div', { style: 'overflow-x:auto' }, h('table', null,
      h('tr', null, ['Username', 'Role', 'Status', 'New password', ''].map(t => h('th', null, t))),
      list.map(a => {
        const isMe = a.id === S.me.id;
        const name = h('input', { type: 'text', value: a.username, style: 'width:120px' });
        const role = h('select', null, h('option', { value: 'cashier' }, 'Cashier'), h('option', { value: 'owner' }, 'Owner'));
        role.value = a.role;
        const pw = h('input', { type: 'password', placeholder: '(unchanged)', style: 'width:120px', autocomplete: 'new-password' });
        const saveDetails = h('button', { class: 'btn small primary' }, 'Save');
        saveDetails.onclick = async e => {
          await act(e.target, () => api('PUT', `/api/admins/${a.id}`, { username: name.value, role: role.value }), 'Saved').catch(() => {});
          if (pw.value) await act(e.target, () => api('POST', `/api/admins/${a.id}/password`, { password: pw.value }), 'Password set').catch(() => {});
          pw.value = ''; load();
        };
        const toggle = h('button', { class: 'btn small', onclick: e => act(e.target, () => api('POST', `/api/admins/${a.id}/disabled`, { disabled: !a.disabled }), 'Saved').then(load).catch(() => {}) }, a.disabled ? 'Enable' : 'Disable');
        const del = h('button', { class: 'btn small danger', onclick: async e => { if (await confirmBox('Delete account', `Delete ${a.username}?`, 'Delete', true)) { await act(e.target, () => api('DELETE', `/api/admins/${a.id}`), 'Deleted').then(load).catch(() => {}); } } }, 'Delete');
        return h('tr', null,
          h('td', null, isMe ? h('b', null, a.username) : name),
          h('td', null, isMe ? a.role : role),
          h('td', null, a.disabled ? 'disabled' : 'active'),
          h('td', null, isMe ? h('span', { class: 'muted small' }, '—') : pw),
          h('td', null, isMe ? h('span', { class: 'muted small' }, 'you') : [saveDetails, ' ', toggle, ' ', del]));
      }))));
  }).catch(e => box.replaceChildren(h('p', { class: 'error' }, e.message)));
  load();
  const u = h('input', { type: 'text' });
  const p = h('input', { type: 'password' });
  const role = h('select', null, h('option', { value: 'cashier' }, 'Cashier'), h('option', { value: 'owner' }, 'Owner'));
  const add = h('button', { class: 'btn primary' }, 'Add account');
  add.onclick = () => act(add, () => api('POST', '/api/admins', { username: u.value, password: p.value, role: role.value }), 'Account added').then(() => { u.value = p.value = ''; load(); }).catch(() => {});
  return h('div', { class: 'card' }, h('h2', null, 'Staff accounts'), box,
    h('h3', null, 'Add an account'), h('div', { class: 'row' }, field('Username', u), field('Password', p), field('Role', role)), add,
    h('p', { class: 'muted small' }, 'Cashiers can run sessions, shifts and PCs. Only owners can change prices, settings and staff.'));
}

boot();
