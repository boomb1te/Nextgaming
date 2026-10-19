// NextGaming admin panel. Plain JavaScript, no build step, no dependencies.
// The server pushes a full snapshot over Server-Sent Events whenever anything
// changes; this file only renders it and sends actions to the JSON API.
'use strict';

const S = {
  me: null, snap: null, snapAt: 0, tab: 'pcs', live: false, es: null,
  seenAlert: -1, reportDate: todayISO(), auditDate: todayISO(),
  selecting: false, selected: new Set(),
  clientView: 'basic', // Clients sub-view: 'basic' | 'auth'
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

// Inline stroke-SVG icon (h() can't create SVG namespaces, so use innerHTML).
const ICON = {
  pcs: '<rect x="3" y="3" width="7" height="7" rx="1"/><rect x="14" y="3" width="7" height="7" rx="1"/><rect x="3" y="14" width="7" height="7" rx="1"/><rect x="14" y="14" width="7" height="7" rx="1"/>',
  screens: '<rect x="2" y="4" width="20" height="14" rx="2"/><path d="M8 21h8"/>',
  files: '<path d="M3 7a2 2 0 012-2h4l2 2h8a2 2 0 012 2v8a2 2 0 01-2 2H5a2 2 0 01-2-2z"/>',
  log: '<path d="M4 5h16M4 12h16M4 19h10"/>',
  clients: '<circle cx="12" cy="8" r="3.4"/><path d="M5 20a7 7 0 0114 0"/>',
  bookings: '<rect x="3" y="4" width="18" height="18" rx="2"/><path d="M3 10h18M8 2v4M16 2v4"/>',
  shift: '<rect x="2" y="6" width="20" height="13" rx="2"/><path d="M2 10h20M6 15h4"/>',
  reports: '<path d="M5 20V9M12 20V4M19 20v-7"/>',
  settings: '<circle cx="12" cy="12" r="3.2"/><path d="M12 2v3M12 19v3M2 12h3M19 12h3M5 5l2 2M17 17l2 2M19 5l-2 2M7 17l-2 2"/>',
  lock: '<rect x="5" y="11" width="14" height="9" rx="2"/><path d="M8 11V8a4 4 0 018 0v3"/>',
  note: '<path d="M4 20h4L18.5 9.5l-4-4L4 16z"/><path d="M13 7l4 4"/>',
  mouse: '<rect x="7" y="3" width="10" height="18" rx="5"/><path d="M12 7v3"/>',
  move: '<path d="M7 7h11l-3-3M17 17H6l3 3"/>',
  info: '<circle cx="12" cy="12" r="9"/><path d="M12 11v5"/><circle cx="12" cy="7.6" r="0.7" fill="currentColor" stroke="none"/>',
  warn: '<path d="M12 9v4M12 17h.01M10.3 3.9L2 18a2 2 0 001.7 3h16.6a2 2 0 001.7-3L13.7 3.9a2 2 0 00-3.4 0z"/>',
  control: '<path d="M4 4l16 8-7 2-2 7z"/>',
  camera: '<path d="M4 7h3l2-2h6l2 2h3v12H4z"/><circle cx="12" cy="13" r="3.5"/>',
  msg: '<path d="M4 5h16v11H8l-4 4z"/>',
  bolt: '<path d="M13 2L4 14h7l-1 8 9-12h-7z"/>',
  power: '<path d="M12 3v9"/><path d="M6.4 7.2a8 8 0 1011.2 0"/>',
  more: '<circle cx="5" cy="12" r="1.6" fill="currentColor" stroke="none"/><circle cx="12" cy="12" r="1.6" fill="currentColor" stroke="none"/><circle cx="19" cy="12" r="1.6" fill="currentColor" stroke="none"/>',
  chevron: '<path d="M6 9l6 6 6-6"/>',
  check: '<path d="M5 12l5 5L20 7"/>',
  folder: '<path d="M3 7a2 2 0 012-2h4l2 2h8a2 2 0 012 2v8a2 2 0 01-2 2H5a2 2 0 01-2-2z"/>',
  file: '<path d="M14 3H7a2 2 0 00-2 2v14a2 2 0 002 2h10a2 2 0 002-2V8z"/><path d="M14 3v5h5"/>',
  up: '<path d="M12 19V5M5 12l7-7 7 7"/>',
  download: '<path d="M12 3v12M7 10l5 5 5-5M5 20h14"/>',
  send: '<path d="M5 12h14M13 6l6 6-6 6"/>',
};
// Status → .badge variant for the PC tile pill (color always paired with a label).
const STATE_BADGE = { free: 'ok', busy: 'info', paused: 'warn', checkout: 'warn', staff: 'purple', offline: '' };
const STATE_DOT = { free: 'green', busy: 'blue', paused: 'amber', checkout: 'amber', staff: 'purple', offline: 'gray' };
function statusBadge(state) {
  const v = STATE_BADGE[state];
  const b = h('span', { class: 'badge stbadge' + (v && v !== 'purple' ? ' ' + v : '') },
    h('i', { class: 'dot', style: `background:var(--${STATE_DOT[state] || 'gray'})` }),
    STATE_LABEL[state] || state);
  if (v === 'purple') { b.style.color = 'var(--purple)'; b.style.background = 'color-mix(in srgb, var(--purple) 16%, transparent)'; }
  return b;
}
function ico(inner, size) {
  const s = document.createElement('span');
  s.className = 'ico'; s.setAttribute('aria-hidden', 'true');
  s.innerHTML = `<svg width="${size || 18}" height="${size || 18}" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">${inner}</svg>`;
  return s;
}

// ---------- Dropdown menus (design: NextGaming Dropdowns) ----------
// One click-anywhere listener closes every open menu.
let _ddWired = false;
function wireDropdowns() {
  if (_ddWired) return; _ddWired = true;
  document.addEventListener('click', () => document.querySelectorAll('.dd.open').forEach(d => d.classList.remove('open')));
}
// Toggle one dropdown, closing any others first.
function toggleDD(dd) {
  const open = !dd.classList.contains('open');
  document.querySelectorAll('.dd.open').forEach(d => { if (d !== dd) d.classList.remove('open'); });
  dd.classList.toggle('open', open);
}
// dropdown(trigger, items[, opts]) — generic menu. items: elements built with
// menuItem(), or menuSep()/menuCap(). trigger is a .btn (or any element).
function dropdown(trigger, items, opts = {}) {
  wireDropdowns();
  const menu = h('div', { class: 'menu' + (opts.right ? ' right' : '') + (opts.scroll ? ' scroll' : '') },
    items && items.length ? items : h('div', { class: 'mmempty' }, opts.empty || 'Nothing here'));
  trigger.classList.add('dd-trigger');
  trigger.addEventListener('click', ev => { ev.stopPropagation(); toggleDD(dd); });
  menu.addEventListener('click', ev => ev.stopPropagation());
  const dd = h('span', { class: 'dd' }, trigger, menu);
  return dd;
}
function menuItem(label, onClick, opts = {}) {
  return h('button', { class: 'mi' + (opts.sel ? ' sel' : '') + (opts.danger ? ' danger' : ''), type: 'button', disabled: !!opts.disabled,
    onclick: ev => { ev.stopPropagation(); const dd = ev.target.closest('.dd'); if (dd) dd.classList.remove('open'); onClick && onClick(ev); } },
    opts.check ? h('span', { class: 'check' }, ico(ICON.check, 15)) : (opts.icon ? ico(opts.icon, 16) : null),
    h('span', { class: 'grow' }, label),
    opts.hint ? h('span', { class: 'hint' }, opts.hint) : null);
}
const menuSep = () => h('div', { class: 'msep' });
const menuCap = t => h('div', { class: 'mcap' }, t);
// selectMenu(options, current, onPick[, opts]) — themed <select> replacement.
// options: [{value, label, hint}]. Shows a check on the chosen row.
function selectMenu(options, current, onPick, opts = {}) {
  const cur = options.find(o => o.value === current);
  const trigger = h('button', { class: 'btn selbtn' + (opts.block ? ' block' : '') + (opts.small ? ' small' : ''), type: 'button' },
    h('span', { class: 'selval' },
      opts.label ? h('span', { class: 'sellbl' }, opts.label) : null,
      h('span', { class: 'val' }, cur ? cur.label : (opts.placeholder || '—'))),
    h('span', { class: 'caret' }, ico(ICON.chevron, 15)));
  const items = options.map(o => menuItem(o.label, () => onPick(o.value), { check: true, sel: o.value === current, hint: o.hint }));
  return dropdown(trigger, items, { right: opts.right, scroll: opts.scroll, empty: opts.empty });
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
function isTouchDevice() { try { return !!(window.matchMedia && matchMedia('(pointer: coarse)').matches); } catch (e) { return false; } }
function fullscreenButton(target) {
  return h('button', { class: 'btn small', onclick: () => {
    if (document.fullscreenElement) document.exitFullscreen();
    else if (target.requestFullscreen) target.requestFullscreen().catch(() => {});
  } }, '⛶ Full screen');
}
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
    const body = danger
      ? h('div', { class: 'row', style: 'align-items:flex-start;gap:12px;flex-wrap:nowrap' },
          h('span', { class: 'warnico' }, ico(ICON.warn, 20)),
          h('p', { style: 'margin:0' }, text))
      : h('p', null, text);
    const m = modal(title, body, [
      h('button', { class: 'btn', onclick: () => finish(false) }, 'Cancel'),
      h('button', { class: 'btn ' + (danger ? 'danger' : 'primary'), onclick: () => finish(true) }, okLabel || 'OK'),
    ]);
    const obs = new MutationObserver(() => { if (!document.body.contains(m.box)) { obs.disconnect(); if (!done) { done = true; resolve(false); } } });
    obs.observe(document.getElementById('modal-root'), { childList: true });
  });
}

function field(label, input, hint) {
  return h('label', { class: 'field' }, h('span', { class: 'fl' }, label), input, hint ? h('span', { class: 'fl' }, hint) : null);
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

// sideItem builds one grouped-sidebar entry. view distinguishes Clients
// sub-items that share the 'clients' tab; owner adds the Settings lock chip.
function sideItem(tab, label, iconKey, opts) {
  opts = opts || {};
  const active = S.tab === tab && (!opts.view || (S.clientView || 'basic') === opts.view);
  const b = h('button', { class: 'side-item' + (active ? ' on' : ''), 'data-tab': tab, 'data-view': opts.view || '', onclick: () => switchTab(tab, opts.view) });
  b.append(ico(ICON[iconKey]), h('span', { class: 'grow' }, label));
  if (opts.owner) { const lk = h('span', { class: 'side-lock', title: 'Owner account required' }); lk.append(ico(ICON.lock, 12), document.createTextNode('owner')); b.append(lk); }
  return b;
}

function renderShell() {
  const nav = h('div', { class: 'side-nav' });
  const groups = [
    ['MANAGE', [sideItem('pcs', 'PCs', 'pcs'), sideItem('screens', 'Screens', 'screens'), sideItem('files', 'File sharing', 'files'), sideItem('log', 'Activity log', 'log')]],
    ['CLIENTS', [sideItem('clients', 'Basic clients', 'clients', { view: 'basic' }), sideItem('clients', 'Authorized clients', 'clients', { view: 'auth' })]],
    ['BOOKINGS', [sideItem('bookings', 'Book a PC', 'bookings')]],
    ['FINANCE', [sideItem('shift', 'Shift & cash', 'shift'), sideItem('reports', 'Reports', 'reports')]],
  ];
  for (const [title, items] of groups) nav.append(h('div', { class: 'side-group' }, title), ...items);

  const brand = h('div', { class: 'brand' }, h('span', { class: 'logo' }, 'N'), h('span', null, 'NextGaming'));
  const acct = h('div', { class: 'side-acct' },
    h('span', { class: 'av' }, (S.me.username || '?').slice(0, 1).toUpperCase()),
    h('div', null, h('div', { class: 'nm' }, S.me.username), h('div', { class: 'rl' }, S.me.role)));
  const logout = h('button', { class: 'btn small', style: 'margin:8px 12px 0', onclick: async () => { await api('POST', '/api/logout').catch(() => {}); S.me = null; renderLogin(); } }, 'Log out');
  const sidebar = h('aside', { class: 'sidebar' }, brand, nav,
    h('div', { class: 'side-bottom' }, sideItem('settings', 'Settings', 'settings', { owner: true }), acct, logout));
  const content = h('div', { class: 'content' },
    h('header', { class: 'top', id: 'hdr' }),
    h('div', { id: 'callbar' }),
    h('main', { id: 'main' }, h('p', { class: 'muted' }, 'Connecting…')));
  document.getElementById('app').replaceChildren(h('div', { class: 'app-shell' }, sidebar, content));
}

function renderTabs() {
  document.querySelectorAll('.sidebar .side-item').forEach(b => {
    const view = b.dataset.view;
    b.classList.toggle('on', S.tab === b.dataset.tab && (!view || (S.clientView || 'basic') === view));
  });
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
      : h('button', { class: 'btn small', onclick: () => switchTab('shift') }, 'Open a shift'),
    h('span', { class: 'grow' }),
    h('span', { class: 'conn' + (S.live ? '' : ' bad') }, S.live ? 'Live' : 'Reconnecting…'));
}

function render() {
  if (!S.me || !document.getElementById('main')) return;
  renderHeader();
  updateCallBar();
  if (!S.snap) return;
  const main = document.getElementById('main');
  if (S.tab !== 'screens') screensStop();
  if (S.tab !== 'files') filesStop();
  // The view key folds the Clients sub-view in, so switching Basic/Authorized
  // (same tab) still re-renders the content.
  const key = S.tab + (S.tab === 'clients' ? ':' + (S.clientView || 'basic') : '');
  // Keep forms on Settings/Shift/Reports stable while snapshots stream in.
  if (S.tab === 'pcs') main.replaceChildren(renderPCs());
  else if (main.dataset.tab !== key) {
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
  main.dataset.tab = key;
}

// Re-render only the view (used after actions on non-live tabs).
function rerender() { const m = document.getElementById('main'); if (m) m.dataset.tab = ''; render(); }

// switchTab changes the active tab (and Clients sub-view) and refreshes the nav.
function switchTab(id, view) { S.tab = id; if (view) S.clientView = view; renderTabs(); render(); }

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
  // Checkout: the player wants to pay — show the amount owed, not the clock.
  if (s.checkoutRequested && s.mode === 'open') return h('div', { class: 'timer', style: 'color:var(--amber)' }, money(s.due) + ' due');
  if (s.status === 'paused') return h('div', { class: 'timer' }, s.mode === 'prepaid' ? dur(s.remainingSec) : dur(s.elapsedSec));
  if (s.mode === 'prepaid') return h('div', { class: 'timer' + (s.remainingSec < 300 ? ' low' : ''), 'data-rem': s.remainingSec }, dur(s.remainingSec));
  return h('div', { class: 'timer', 'data-el': s.elapsedSec }, dur(s.elapsedSec));
}

function tileSub(pc) {
  const s = pc.session;
  if (!s) return pc.zoneName || '';
  if (s.checkoutRequested) return `${s.clientName || (s.mode === 'open' ? 'Open session' : s.tariffName)} · ${dur(s.elapsedSec)}`;
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

  const endingSoon = pcs.filter(p => p.session && p.session.mode === 'prepaid' && p.session.status === 'active' && p.session.remainingSec < 600).length;
  const selectBtn = h('button', { class: 'btn' + (S.selecting ? ' primary' : ''), onclick: () => {
    S.selecting = !S.selecting; if (!S.selecting) S.selected.clear(); rerenderPCs();
  } }, S.selecting ? 'Done selecting' : 'Select PCs');

  // At-a-glance count pills (status colour on a dot, always with a label).
  const cpill = (label, colorVar, n, cls) => n ? h('span', { class: 'cpill' + (cls ? ' ' + cls : '') }, h('i', { style: `background:var(--${colorVar})` }), h('b', null, String(n)), ' ' + label) : null;
  const countRow = h('div', { class: 'countrow' },
    cpill('free', 'green', counts.free || 0),
    cpill('in session', 'blue', counts.busy || 0),
    cpill('ending soon', 'amber', endingSoon),
    cpill('needs checkout', 'amber', counts.checkout || 0, 'attn'),
    cpill('paused', 'gray', counts.paused || 0),
    cpill('staff', 'purple', counts.staff || 0),
    cpill('offline', 'gray', counts.offline || 0),
    h('span', { class: 'grow' }),
    selectBtn);
  const toolbar = h('div', { class: 'toolbar' },
    h('button', { class: 'btn small', onclick: e => bulk(e.target, 'wake') }, ico(ICON.bolt, 14), 'Wake all offline'),
    h('button', { class: 'btn small', onclick: e => bulk(e.target, 'shutdown_free') }, ico(ICON.power, 14), 'Shut down free'),
    h('button', { class: 'btn small', onclick: () => messageModal(null) }, ico(ICON.msg, 14), 'Message all'));

  if (!pcs.length) {
    return h('div', null, toolbar, h('div', { class: 'card' },
      h('h2', null, 'No PCs yet'),
      h('p', null, 'Install the NextGaming agent on each gaming PC. They appear here automatically as soon as they connect.'),
      isOwner() ? h('button', { class: 'btn primary', onclick: () => { S.tab = 'settings'; renderTabs(); rerender(); } }, 'Show install instructions') : null));
  }

  const grid = h('div', { class: 'grid' }, pcs.map(pc => {
    const on = S.selected.has(pc.id);
    // Single click opens the dialog; double click is a shortcut (free → start an
    // open session, in a session → go to settle & end). The short delay lets a
    // double click cancel the dialog so it doesn't flash open first.
    let clickT = null;
    const tile = h('div', { class: 'tile st-' + pc.state + (S.selecting && on ? ' selected' : ''), onclick: () => {
      if (S.selecting) { on ? S.selected.delete(pc.id) : S.selected.add(pc.id); rerenderPCs(); return; }
      if (clickT) return;
      clickT = setTimeout(() => { clickT = null; pcModal(pc.id); }, 220);
    }, ondblclick: () => {
      if (S.selecting) return;
      if (clickT) { clearTimeout(clickT); clickT = null; }
      quickTileAction(pc);
    } },
      S.selecting ? h('div', { class: 'tickbox' }, on ? '✓' : '') : null,
      h('div', { class: 'name' },
        h('span', { class: 'nm' }, pc.name, pc.note ? h('span', { class: 'noteflag', title: pc.note }, ico(ICON.note, 13)) : null),
        h('span', { class: 'namebadges' }, wolBadge(pc), statusBadge(pc.state))),
      tileTimer(pc),
      h('div', { class: 'sub' }, tileSub(pc)),
      pc.nextBooking ? h('div', { class: 'booked' }, ico(ICON.bookings, 13), ` Booked ${timeStr(pc.nextBooking.at)} — ${pc.nextBooking.who}`) : null,
      pc.controlled ? h('div', { class: 'controlled' }, ico(ICON.mouse, 13), ' Being controlled') : null,
      pc.runtime.online && !pc.runtime.clientAlive ? h('div', { class: 'warn' }, 'Lock screen not running') : null,
      !pc.runtime.online && pc.session ? h('div', { class: 'warn' }, 'Offline during session') : null);
    return tile;
  }));

  return h('div', null, countRow, toolbar, S.selecting ? selectionBar(pcs) : null, grid);
}

// quickTileAction handles a double click on a PC tile: a free PC starts an open
// (pay-at-the-end) session straight away; a PC in a paid session jumps to the
// settle & end dialog. Everything else falls back to the full dialog.
function quickTileAction(pc) {
  if (pc.session && pc.session.mode !== 'free') { endModal(pc.id); return; }
  if (pc.state === 'free' && pc.runtime.online) {
    api('POST', `/api/pcs/${pc.id}/start`, { mode: 'open' })
      .then(() => toast(`${pc.name}: open session started (pay at the end)`, 'ok'))
      .catch(e => toast(e.message, 'error'));
    return;
  }
  pcModal(pc.id);
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

  const B = (label, cls, fn) => h('button', { class: 'btn' + (cls ? ' ' + cls : ''), onclick: fn }, label);

  // --- session summary card ---
  let card = null;
  if (s) {
    const paused = s.status === 'paused';
    let big, prog = null;
    if (s.mode === 'prepaid') {
      const total = Math.max(1, s.elapsedSec + s.remainingSec);
      const pct = Math.max(0, Math.min(100, Math.round(100 * s.elapsedSec / total)));
      big = h('div', { class: 'row', style: 'align-items:baseline;gap:10px;margin-top:8px' },
        h('div', Object.assign({ class: 'sess-big' + (s.remainingSec < 300 ? ' low' : '') }, paused ? {} : { 'data-rem': s.remainingSec }), dur(s.remainingSec)),
        h('span', { class: 'muted small' }, 'left of ' + dur(total)));
      prog = h('div', { class: 'sess-prog' }, h('i', { style: `width:${pct}%` }));
    } else {
      big = h('div', { class: 'row', style: 'align-items:baseline;gap:10px;margin-top:8px' },
        h('div', Object.assign({ class: 'sess-big' }, paused ? {} : { 'data-el': s.elapsedSec }), dur(s.elapsedSec)),
        h('span', { class: 'muted small' }, 'used'));
    }
    card = h('div', { class: 'sess-card' },
      h('div', { class: 'row', style: 'justify-content:space-between;align-items:baseline' },
        h('span', { class: 'muted small' }, `${s.tariffName} · started ${timeStr(s.startedAt)}`),
        h('span', { class: 'muted small' }, s.mode === 'open' ? ['Due ', h('b', null, money(s.due))] : ['Paid ', h('b', null, money(s.paid))])),
      big, prog,
      s.clientName ? h('div', { class: 'row small', style: 'margin-top:12px;gap:8px' }, h('span', { class: 'muted' }, 'Credited to'), h('b', null, s.clientName)) : null,
      paused ? h('div', { class: 'small', style: 'margin-top:10px;color:var(--amber)' }, 'Paused — the clock is stopped') : null);
  }

  // --- primary actions ---
  const primary = [];
  if (pc.state === 'free') {
    primary.push(B('Start session', 'primary', then(() => startModal(pc.id))));
    primary.push(B('Unlock free (staff)', '', run(() => api('POST', `/api/pcs/${pc.id}/unlock-free`, {}), 'Unlocked for staff (not billed)')));
  }
  if (s) {
    if (s.checkoutRequested) primary.push(B('Settle & end', 'primary', then(() => endModal(pc.id))));
    if (s.mode === 'prepaid') primary.push(B('Add time', s.checkoutRequested ? '' : 'primary', then(() => timeModal(pc.id))));
    if (s.mode !== 'free' && s.status === 'active') primary.push(B('Pause', '', run(() => api('POST', `/api/pcs/${pc.id}/pause`), 'Paused')));
    if (s.status === 'paused') primary.push(B('Resume', '', run(() => api('POST', `/api/pcs/${pc.id}/resume`), 'Resumed')));
    if (s.mode === 'free') primary.push(B('Lock', 'primary', run(() => api('POST', `/api/pcs/${pc.id}/lock`), 'Locked')));
    else if (!s.checkoutRequested) primary.push(B('End session', '', then(() => endModal(pc.id))));
  }

  // --- LIVE icon row (online only) ---
  let liveRow = null;
  if (online) {
    const lb = (iconInner, label, fn, disabled, title) => {
      const b = h('button', { class: 'live-btn', disabled: !!disabled, title: title || '' }, ico(iconInner, 18), h('span', null, label));
      if (!disabled) b.onclick = fn;
      return b;
    };
    liveRow = h('div', null, h('div', { class: 'live-lbl' }, 'LIVE'),
      h('div', { class: 'live-grid' },
        lb(ICON.control, 'Take control', then(() => controlModal(pc.id, pc.name)), pc.runtime.locked, pc.runtime.locked ? 'Unlock to control' : ''),
        lb(ICON.screens, 'Watch', then(() => openScreenModal(pc.id))),
        lb(ICON.camera, 'Snapshot', then(() => hdSnapshot(pc.id, pc.name))),
        lb(ICON.msg, 'Message', then(() => messageModal(pc.id)))));
  }

  // --- details + move, power ---
  const moreRow = h('div', { class: 'row2' },
    h('button', { class: 'btn', onclick: () => pcDetailsModal(pc.id) }, ico(ICON.info, 15), ' Details'),
    s ? h('button', { class: 'btn', onclick: then(() => moveModal(pc.id)) }, ico(ICON.move, 15), ' Move to another PC') : null);
  const powerRow = online
    ? h('div', { class: 'row2' },
        h('button', { class: 'btn', onclick: e => powerAction(e.target, pc, 'restart', m) }, 'Restart'),
        h('button', { class: 'btn danger', onclick: e => powerAction(e.target, pc, 'shutdown', m) }, 'Shut down'))
    : h('div', { class: 'row2' },
        h('button', { class: 'btn primary', disabled: !pc.mac, onclick: run(() => api('POST', `/api/pcs/${pc.id}/wake`), 'Wake signal sent. The PC should appear online within a minute.') }, 'Turn on (Wake-on-LAN)'));

  // --- credit-to-client + note ---
  let clientSection = null;
  if (s && s.mode !== 'free') {
    const csel = clientSelectEl(s.clientId || 0, '— no client —');
    const csave = h('button', { class: 'btn' }, 'Save');
    csave.onclick = e => act(e.target, () => api('POST', `/api/pcs/${pc.id}/session/client`, { clientId: Number(csel.value) || 0 }), 'Updated').then(() => m.close()).catch(() => {});
    clientSection = h('div', null, h('div', { class: 'muted small', style: 'margin-bottom:4px' }, 'Credit hours to client'), h('div', { class: 'row' }, csel, csave));
  }
  const noteInput = h('input', { type: 'text', maxlength: 500, value: pc.note || '', placeholder: 'Add a note…', style: 'flex:1' });
  const noteBtn = h('button', { class: 'btn', onclick: e => act(e.target, () => api('POST', `/api/pcs/${pc.id}/note`, { note: noteInput.value }), 'Note saved').catch(() => {}) }, 'Save');
  const noteSection = h('div', null, h('div', { class: 'muted small', style: 'margin-bottom:4px' }, 'PC note'), h('div', { class: 'row' }, noteInput, noteBtn));

  const sections = [];
  if (!s) sections.push(h('div', { class: 'muted small' }, online ? (STATE_LABEL[pc.state] + (pc.zoneName ? ' · ' + pc.zoneName : '')) : `Offline · last seen ${dateTimeStr(pc.runtime.lastSeen)}`));
  if (card) sections.push(card);
  if (primary.length) sections.push(h('div', { class: 'row2' }, primary));
  if (liveRow) sections.push(liveRow);
  sections.push(moreRow, powerRow);
  if (clientSection) sections.push(clientSection);
  sections.push(noteSection);

  m = modal(pc.name, h('div', { class: 'pcm' }, sections));
}

// pcDetailsModal is the separate "Details" window: network, hardware, lock
// screen and Wake-on-LAN. Opened from the Details button in the PC dialog.
function pcDetailsModal(id) {
  const pc = pcById(id);
  if (!pc) return;
  const online = pc.runtime.online;
  const chip = v => h('span', { class: 'dtl-chip' }, v || '—');
  const row = (k, v) => h('div', { class: 'dtl-row' }, h('span', { class: 'k' }, k), h('span', { class: 'v' }, v));
  const zoneRate = pc.zoneId && zoneById(pc.zoneId) ? ` · ${money(zoneById(pc.zoneId).hourlyRate)}/h` : '';
  const rows = [
    row('Zone', (pc.zoneName || '—') + zoneRate),
    row('IP address', chip(pc.ip)),
    row('MAC', chip(pc.mac)),
    row('Computer', `${pc.hostname || '—'} · ${[pc.boardMaker, pc.boardProduct].filter(Boolean).join(' ') || 'board unknown'}`),
  ];
  if (online) rows.push(row('Lock screen', h('span', { class: 'dtl-live' },
    h('span', { class: 'dot', style: `background:var(--${pc.runtime.clientAlive ? 'green' : 'red'})` }),
    pc.runtime.clientAlive ? 'Running' : h('b', { style: 'color:var(--red)' }, 'Not running'))));
  let m = modal(pc.name + ' — details', h('div', { class: 'dtl' },
    h('div', { class: 'dtl-list' }, rows),
    wolSection(pc, () => m.close())));
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

const WOL_DOT = { ok: 'green', info: 'blue', warn: 'amber', bad: 'red', '': 'gray' };

function wolSection(pc, closeParent) {
  const w = pc.wol;
  const [cls, label] = WOL_LABEL[w.status] || ['', w.status];
  const online = pc.runtime.online;
  const kids = [
    h('div', { class: 'wol-head' },
      h('h3', null, 'Remote power-on (Wake-on-LAN)'),
      h('span', { class: 'grow' }),
      h('span', { class: 'wol-pill ' + cls }, h('span', { class: 'dot', style: `background:var(--${WOL_DOT[cls] || 'gray'})` }), label)),
    w.status === 'testing' && w.stage ? h('p', null, w.stage) : null,
    w.detail ? h('p', null, w.detail) : null,
  ];
  if (w.report && w.report.settings && w.report.settings.length) {
    kids.push(h('details', null, h('summary', null, 'Windows settings checked ' + dateTimeStr(w.checkedAt)),
      h('ul', { class: 'setlist' }, w.report.settings.map(st =>
        h('li', { class: st.ok ? '' : 'bad' }, `${st.name}: ${st.value}${st.fixed ? ' (fixed)' : ''}`, st.ok || !st.advice ? '' : h('div', { class: 'muted small' }, st.advice))))));
  }
  if ((w.status === 'bios_needed' || (w.status === 'windows_settings' && w.testedAt && new Date(w.testedAt).getFullYear() > 2000)) && w.guide) {
    kids.push(h('div', { class: 'wol-guide' },
      h('b', null, `${pc.name}${w.guide.vendor ? ' (' + w.guide.vendor + ')' : ''} can't be turned on remotely.`),
      h('ol', { class: 'steps' }, w.guide.steps.map(s => h('li', null, s))),
      h('p', { class: 'muted small' }, w.guide.note)));
  }
  const btns = [];
  if (online) {
    btns.push(h('button', { class: 'btn small', onclick: e => act(e.target, () => api('POST', `/api/pcs/${pc.id}/wol-check`, { fix: true }), 'Check finished') }, ico(ICON.settings, 14), 'Check & fix Windows settings'));
    const force = w.status === 'windows_settings';
    btns.push(h('button', { class: 'btn small', disabled: !!pc.session || w.status === 'testing', onclick: async e => {
      const extra = force ? ' The Windows check reported problems, so the test may fail — but if the check is wrong, this shows it.' : '';
      if (!await confirmBox('Test Wake-on-LAN', `${pc.name} will shut down, then NextGaming sends the wake signal and waits for it to start again. This takes 1–4 minutes.${extra}`, 'Start test')) return;
      try {
        await act(e.target, () => api('POST', `/api/pcs/${pc.id}/wol-test`, { force }), 'Test started — watch the PC tile');
        closeParent();
      } catch (err) { /* shown */ }
    } }, ico(ICON.power, 14), force ? 'Test anyway' : 'Test Wake-on-LAN'));
  }
  if (btns.length) kids.push(h('div', { class: 'actions' }, btns));
  return h('div', { class: 'wol' }, kids);
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
  let mode = 'open', choice = null;
  const priceEl = h('div', { class: 'price' });
  const note = h('input', { type: 'text', maxlength: 500, placeholder: 'optional — e.g. tournament seat, or paid by a friend' });
  const clientSel = clientSelectEl(0, '— no client —');
  const pickerBox = h('div');
  const startBtn = h('button', { class: 'btn primary' }, 'Start');
  const modeOrder = ['open', 'prepaid'];
  const modeBtns = [['open', 'Open (pay at the end)'], ['prepaid', 'Prepaid (fixed time)']].map(([k, l]) =>
    h('button', { type: 'button', onclick: () => { mode = k; draw(); } }, l));
  const modeSeg = h('div', { class: 'segment wide', style: 'margin-bottom:14px' }, modeBtns);
  function draw() {
    modeBtns.forEach((b, i) => b.classList.toggle('on', modeOrder[i] === mode));
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
    modeSeg, pickerBox, priceEl,
    field('Credit to client (optional)', clientSel), field('Note', note)),
  [h('button', { class: 'btn', onclick: () => m.close() }, 'Cancel'), startBtn]);
  startBtn.onclick = async () => {
    const body = { mode, note: note.value, clientId: Number(clientSel.value) || 0 };
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
  const modeOrder = ['open', 'prepaid'];
  const modeBtns = [['open', 'Open (pay at the end)'], ['prepaid', 'Prepaid (fixed time)']].map(([k, l]) =>
    h('button', { type: 'button', onclick: () => { mode = k; draw(); } }, l));
  const modeSeg = h('div', { class: 'segment wide', style: 'margin-bottom:10px' }, modeBtns);
  function draw() {
    modeBtns.forEach((b, i) => b.classList.toggle('on', modeOrder[i] === mode));
    startBtn.style.display = mode ? '' : 'none';
    if (mode === null) {
      pickerBox.replaceChildren(h('p', { class: 'muted' }, 'Choose Open or Prepaid above.'));
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
    h('div', { class: 'muted small', style: 'margin-bottom:6px' }, 'Session type'),
    modeSeg, pickerBox, priceEl),
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
  const left = s.status === 'paused' ? 'paused' : `${mins(s.remainingSec)} left now`;
  const m = modal(`Add time — ${pc.name}`, h('div', null,
    h('p', { class: 'muted small', style: 'margin:0 0 12px' }, left),
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
  const quicks = ['Your time is almost up — come to the front desk to add more.', 'Please come to the front desk.', 'Please lower the volume.', 'We close in 15 minutes.'];
  const chips = h('div', { class: 'row', style: 'margin-top:8px;gap:6px' },
    quicks.map(q => h('button', { type: 'button', class: 'btn small', onclick: () => { text.value = q; } }, q.length > 26 ? q.slice(0, 24) + '…' : q)));
  const body = h('div', null,
    field('Text shown on screen', text),
    h('div', { class: 'muted small', style: 'margin-bottom:2px' }, 'Quick messages'),
    chips);
  const btn = h('button', { class: 'btn primary' }, 'Send');
  const m = modal(pc ? `Message to ${pc.name}` : 'Message to all online PCs', body, [h('button', { class: 'btn', onclick: () => m.close() }, 'Cancel'), btn]);
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
    // Control (left) opens remote control; Watch (right) enlarges. Both stop the
    // click from bubbling to the tile (whose own click also enlarges).
    const canControl = pc.state !== 'offline' && !pc.runtime.locked;
    const ctrlBtn = h('button', { class: 'btn small', title: pc.runtime.locked ? 'Unlock the PC to control it' : 'Take control', disabled: !canControl, onclick: e => { e.stopPropagation(); controlModal(pc.id, pc.name); } }, 'Control');
    const watchBtn = h('button', { class: 'btn small', onclick: e => { e.stopPropagation(); openScreenModal(pc.id); } }, 'Watch');
    const tile = h('div', { class: 'screen-tile', title: 'Click to enlarge', onclick: () => openScreenModal(pc.id) },
      h('div', { class: 'screen-frame' }, img, waiting),
      h('div', { class: 'screen-cap' }, h('span', { class: 'screen-name' }, pc.name), badge, ctrlBtn, watchBtn));
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
  if (!pc) return;
  // Opened from the PCs tab (no Screens grid running): drive its own capture
  // lifecycle so it works inline without switching tabs.
  const standalone = !S.screens;
  const focusOn = () => {
    if (standalone) api('POST', '/api/screens/state', { watching: true, focus: id }).catch(() => {});
    else { S.screens.focus = id; screenPost(true); }
  };
  focusOn(); // ask the server for a sharper, faster frame on this PC
  const hb = standalone ? setInterval(focusOn, 4000) : null; // keep capture alive (8s TTL)
  const img = h('img', { class: 'screen-big', alt: '' });
  const waiting = h('div', { class: 'screen-wait' }, 'Waiting for image…');
  const snapBtn = h('button', { class: 'btn small', onclick: () => hdSnapshot(id, pc.name) }, ico(ICON.camera, 14), ' HD snapshot');
  const bar = h('div', { class: 'screen-bigbar' }, snapBtn, controlControl(id, pc));
  const bigWrap = h('div', { class: 'screen-big-wrap' }, img, waiting);
  bar.append(fullscreenButton(bigWrap));
  const body = h('div', null, bar, bigWrap);
  const m = modal(pc.name + ' — live screen', body);
  // Seed from the thumbnail immediately, then refresh at full rate.
  refreshScreen(id, img, waiting, SCREEN_FOCUS_MS * 3);
  let stopped = false;
  const stop = () => {
    if (stopped) return; stopped = true;
    clearInterval(timer);
    if (hb) clearInterval(hb);
    if (img.dataset.url) URL.revokeObjectURL(img.dataset.url);
    if (standalone) api('POST', '/api/screens/state', { watching: false, focus: 0 }).catch(() => {});
    else if (S.screens) { S.screens.focus = 0; screenPost(true); }
  };
  const timer = setInterval(() => {
    if (!document.body.contains(m.box)) { stop(); return; }
    refreshScreen(id, img, waiting, SCREEN_FOCUS_MS * 3);
  }, SCREEN_FOCUS_MS);
  const baseClose = m.close; m.close = () => { stop(); baseClose(); };
  if (!standalone) S.screens.focusModal = m;
}

// hdSnapshot asks the PC for one full-resolution frame and shows it frozen
// (not streamed). The server only serves a shot captured after this request,
// so the admin's clock doesn't matter; we poll until it arrives.
function hdSnapshot(id, name) {
  const img = h('img', { class: 'screen-big', alt: '' });
  const status = h('div', { class: 'screen-wait' }, 'Capturing a sharp image…');
  const open = h('a', { class: 'btn small', target: '_blank', style: 'display:none' }, 'Open full size ↗');
  const again = h('button', { class: 'btn small' }, 'New snapshot');
  const m = modal(name + ' — HD snapshot', h('div', null,
    h('div', { class: 'screen-bigbar' }, again, open),
    h('div', { class: 'screen-big-wrap' }, img, status)));
  const baseClose = m.close;
  m.close = () => { if (img.dataset.url) URL.revokeObjectURL(img.dataset.url); baseClose(); };
  again.onclick = () => run();
  async function run() {
    status.style.display = 'flex'; status.textContent = 'Capturing a sharp image…'; img.classList.remove('has');
    try { await api('POST', `/api/pcs/${id}/snapshot`); }
    catch (e) { status.textContent = e.message; return; }
    const deadline = Date.now() + 12000;
    const tick = async () => {
      if (!document.body.contains(m.box)) return;
      try {
        const res = await fetch(`/api/pcs/${id}/snapshot.jpg?t=${Date.now()}`, { credentials: 'same-origin' });
        if (res.status === 200) {
          const blob = await res.blob();
          if (blob.size) {
            const url = URL.createObjectURL(blob);
            const old = img.dataset.url;
            img.src = url; img.dataset.url = url; img.classList.add('has');
            open.href = url; open.style.display = '';
            status.style.display = 'none';
            if (old) URL.revokeObjectURL(old);
            return;
          }
        }
      } catch (e) { /* retry */ }
      if (Date.now() < deadline) setTimeout(tick, 500);
      else status.textContent = 'Couldn’t get a snapshot (the PC may be busy or a game is in exclusive fullscreen). Try again.';
    };
    setTimeout(tick, 400);
  }
  run();
}

// controlControl decides what the enlarged view shows for remote control:
// the button on a computer with an unlocked online PC, otherwise a short note
// (phones can't drive a PC; a locked PC has nothing useful to control).
function controlControl(id, pc) {
  if (isTouchDevice()) return h('span', { class: 'muted small' }, 'Remote control needs a computer with a mouse & keyboard.');
  if (!pc.runtime || !pc.runtime.online) return h('span', { class: 'muted small' }, 'PC is offline.');
  if (pc.runtime.locked) return h('span', { class: 'muted small' }, 'Unlock the PC to take control.');
  return h('button', { class: 'btn small primary', onclick: () => controlModal(id, pc.name) }, '🖱 Take control');
}

// Windows virtual-key codes for the common keys + modifiers we forward. Keys
// not here are left to the browser (exotic keys aren't supported, by design).
const CONTROL_VK = {
  Backspace: 8, Tab: 9, Enter: 13, Escape: 27, Space: 32, CapsLock: 0x14,
  ShiftLeft: 0xA0, ShiftRight: 0xA1, ControlLeft: 0xA2, ControlRight: 0xA3,
  AltLeft: 0xA4, AltRight: 0xA5,
  // The Windows key (Meta) is deliberately NOT forwarded: Windows swallows its
  // key-up (the Start menu steals focus), so the key-up never reaches us and the
  // PC would think Win is still held — turning every later key into a Win+shortcut.
  ArrowLeft: 0x25, ArrowUp: 0x26, ArrowRight: 0x27, ArrowDown: 0x28,
  Home: 0x24, End: 0x23, PageUp: 0x21, PageDown: 0x22, Insert: 0x2D, Delete: 0x2E,
  Minus: 0xBD, Equal: 0xBB, BracketLeft: 0xDB, BracketRight: 0xDD, Backslash: 0xDC,
  Semicolon: 0xBA, Quote: 0xDE, Backquote: 0xC0, Comma: 0xBC, Period: 0xBE, Slash: 0xBF,
};
function controlVk(code) {
  if (CONTROL_VK[code] != null) return CONTROL_VK[code];
  if (/^Key[A-Z]$/.test(code)) return code.charCodeAt(3);          // A..Z -> 0x41..
  if (/^Digit[0-9]$/.test(code)) return code.charCodeAt(5);        // 0..9 -> 0x30..
  if (/^F([1-9]|1[0-2])$/.test(code)) return 0x70 + (+code.slice(1) - 1); // F1..F12
  if (/^Numpad[0-9]$/.test(code)) return 0x60 + (code.charCodeAt(6) - 48);
  return 0;
}

// controlModal shows the PC at ~0.5s and relays your mouse and keyboard to it.
// Closing releases control.
function controlModal(id, name) {
  const img = h('img', { class: 'control-img', alt: '', draggable: false });
  const waiting = h('div', { class: 'screen-wait' }, 'Connecting…');
  const surface = h('div', { class: 'control-surface' }, img, waiting);
  const m = modal(name + ' — remote control', h('div', null,
    h('div', { class: 'screen-bigbar' }, fullscreenButton(surface),
      h('span', { class: 'muted small' }, 'Your mouse and keyboard drive this PC (about half a second behind). The player’s mouse & keyboard are blocked while you control. Close to release.')),
    surface));

  let running = true;
  let buf = [], pendingMove = null;
  const norm = e => { const r = img.getBoundingClientRect(); return { x: (e.clientX - r.left) / r.width, y: (e.clientY - r.top) / r.height }; };
  const btnName = b => (b === 2 ? 'right' : (b === 1 ? 'middle' : 'left'));
  const sendNow = () => {
    if (pendingMove) { buf.push({ kind: 'move', x: pendingMove.x, y: pendingMove.y }); pendingMove = null; }
    if (!buf.length) return;
    const events = buf; buf = [];
    api('POST', `/api/pcs/${id}/control/input`, { events }).catch(() => {});
  };
  img.addEventListener('mousemove', e => { pendingMove = norm(e); });
  img.addEventListener('mousedown', e => { e.preventDefault(); const p = norm(e); buf.push({ kind: 'down', button: btnName(e.button), x: p.x, y: p.y }); sendNow(); });
  img.addEventListener('mouseup', e => { e.preventDefault(); const p = norm(e); buf.push({ kind: 'up', button: btnName(e.button), x: p.x, y: p.y }); sendNow(); });
  img.addEventListener('contextmenu', e => e.preventDefault());
  img.addEventListener('wheel', e => { e.preventDefault(); const p = norm(e); buf.push({ kind: 'wheel', dir: e.deltaY > 0 ? -1 : 1, x: p.x, y: p.y }); sendNow(); }, { passive: false });

  // Keyboard: forward mapped keys to the PC (capture phase so they reach the PC
  // instead of the browser/modal — e.g. Esc goes to the PC, close with the X).
  const onKey = e => {
    const vk = controlVk(e.code);
    if (!vk) return; // unmapped key stays with the browser
    e.preventDefault();
    e.stopImmediatePropagation();
    buf.push({ kind: 'key', vk, keyDown: e.type === 'keydown' });
    sendNow();
  };
  document.addEventListener('keydown', onKey, true);
  document.addEventListener('keyup', onKey, true);

  // If focus leaves this window mid-press (Alt+Tab, clicking away, the Windows
  // key opening the Start menu), a key-up can be lost and leave a modifier stuck
  // "down" on the PC. Releasing every modifier on blur prevents that.
  const releaseMods = () => {
    for (const vk of [0xA0, 0xA1, 0xA2, 0xA3, 0xA4, 0xA5, 0x5B, 0x5C]) buf.push({ kind: 'key', vk, keyDown: false });
    sendNow();
  };
  window.addEventListener('blur', releaseMods);

  const flush = setInterval(sendNow, 50);
  const beat = setInterval(() => { if (running) api('POST', `/api/pcs/${id}/control`, { active: true }).catch(() => {}); }, 3000);
  const poll = setInterval(async () => {
    if (!running) return;
    try {
      const res = await fetch(`/api/pcs/${id}/screen.jpg?control=1&t=${Date.now()}`, { credentials: 'same-origin' });
      if (res.status !== 200) return;
      const blob = await res.blob();
      if (!blob.size) return;
      const url = URL.createObjectURL(blob);
      const old = img.dataset.url;
      img.src = url; img.dataset.url = url; img.classList.add('has'); waiting.style.display = 'none';
      if (old) URL.revokeObjectURL(old);
    } catch (e) { /* retry next tick */ }
  }, 500);

  const stop = () => {
    if (!running) return;
    running = false;
    clearInterval(flush); clearInterval(beat); clearInterval(poll);
    document.removeEventListener('keydown', onKey, true);
    document.removeEventListener('keyup', onKey, true);
    window.removeEventListener('blur', releaseMods);
    if (img.dataset.url) URL.revokeObjectURL(img.dataset.url);
    api('POST', `/api/pcs/${id}/control`, { active: false }).catch(() => {});
  };
  const baseClose = m.close;
  m.close = () => { stop(); baseClose(); };
  const obs = new MutationObserver(() => { if (!document.body.contains(m.box)) { obs.disconnect(); stop(); } });
  obs.observe(document.getElementById('modal-root'), { childList: true });

  // Start control; if the server refuses (locked/offline), close.
  api('POST', `/api/pcs/${id}/control`, { active: true }).catch(e => { toast(e.message, 'error'); m.close(); });
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
  // localSel: Map(path -> name) of chosen front-desk files; pcSel: Set(name) of
  // chosen shared-folder files. Multi-select: click a file to toggle it.
  S.files = { pcId: 0, localPath: '', localSel: new Map(), pcRel: '', pcSel: new Set(), timer: null };
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
    h('p', { class: 'muted small' }, 'Copy files between this front-desk PC and a gaming PC. Tick one or more files, then use the Send / Get buttons. The gaming PC only shares one folder; this PC can browse anywhere. Local network only.'),
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
    const files = cur.entries.filter(e => !e.isDir);
    const allOn = files.length > 0 && files.every(e => f.localSel.has(e.path));
    const selAll = files.length ? h('button', { class: 'btn small fs-selall', onclick: () => {
      if (allOn) files.forEach(e => f.localSel.delete(e.path)); else files.forEach(e => f.localSel.set(e.path, e.name)); paint();
    } }, allOn ? 'Clear' : 'Select all') : null;
    head.replaceChildren(
      h('b', null, 'This PC'),
      h('button', { class: 'btn small', disabled: cur.atRoot, onclick: () => go(cur.parent || '') }, ico(ICON.up, 14), 'Up'),
      h('span', { class: 'fs-path', title: cur.path || 'Drives' }, cur.path || 'Drives'),
      selAll);
    const rows = cur.entries.map(e => {
      if (e.isDir) return h('div', { class: 'fs-row dir', onclick: () => go(e.path) }, h('span', { class: 'fs-ic dir' }, ico(ICON.folder, 16)), h('span', { class: 'fs-nm' }, e.name));
      const on = f.localSel.has(e.path);
      const toggle = () => { if (f.localSel.has(e.path)) f.localSel.delete(e.path); else f.localSel.set(e.path, e.name); paint(); };
      return h('div', { class: 'fs-row file' + (on ? ' sel' : ''), onclick: toggle },
        h('input', { type: 'checkbox', class: 'fs-ck', checked: on, onclick: e2 => { e2.stopPropagation(); toggle(); } }),
        h('span', { class: 'fs-ic' }, ico(ICON.file, 16)), h('span', { class: 'fs-nm' }, e.name), h('span', { class: 'fs-sz' }, fmtBytes(e.size)));
    });
    body.replaceChildren(cur.entries.length ? h('div', null, rows) : h('p', { class: 'muted' }, 'Empty folder.'));
    paintActions();
  };
  const go = async (path) => {
    const f = S.files; if (!f) return;
    try { cur = await api('GET', `/api/fs/local?path=${encodeURIComponent(path)}`); }
    catch (e) { body.replaceChildren(h('p', { class: 'error' }, e.message)); return; }
    if (!S.files) return;
    f.localPath = cur.path; f.localSel.clear();
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
    const options = online.map(p => ({ value: p.id, label: p.name }));
    return selectMenu(options, S.files.pcId, v => { S.files.pcId = Number(v); S.files.pcRel = ''; S.files.pcSel.clear(); load(); },
      { placeholder: online.length ? 'Choose a PC' : 'No PCs online', small: true, scroll: true, empty: 'No PCs online' });
  };
  const paint = () => {
    const f = S.files; if (!f) return;
    const files = cur ? cur.entries.filter(e => !e.isDir) : [];
    const allOn = files.length > 0 && files.every(e => f.pcSel.has(e.name));
    const selAll = files.length ? h('button', { class: 'btn small fs-selall', onclick: () => {
      if (allOn) files.forEach(e => f.pcSel.delete(e.name)); else files.forEach(e => f.pcSel.add(e.name)); paint();
    } }, allOn ? 'Clear' : 'Select all') : null;
    head.replaceChildren(
      picker(),
      h('button', { class: 'btn small', disabled: !cur || !f.pcRel, onclick: () => nav(relParent(f.pcRel)) }, ico(ICON.up, 14), 'Up'),
      h('span', { class: 'fs-path', title: f.pcRel || 'Shared' }, f.pcRel ? 'Shared/' + f.pcRel : 'Shared'),
      selAll);
    if (!cur) { body.replaceChildren(h('p', { class: 'muted' }, f.pcId ? 'Loading…' : 'Pick a PC to see its shared folder.')); paintActions(); return; }
    const rows = cur.entries.map(e => {
      if (e.isDir) return h('div', { class: 'fs-row dir', onclick: () => nav(relJoin(f.pcRel, e.name)) }, h('span', { class: 'fs-ic dir' }, ico(ICON.folder, 16)), h('span', { class: 'fs-nm' }, e.name));
      const on = f.pcSel.has(e.name);
      const toggle = () => { if (f.pcSel.has(e.name)) f.pcSel.delete(e.name); else f.pcSel.add(e.name); paint(); };
      return h('div', { class: 'fs-row file' + (on ? ' sel' : ''), onclick: toggle },
        h('input', { type: 'checkbox', class: 'fs-ck', checked: on, onclick: e2 => { e2.stopPropagation(); toggle(); } }),
        h('span', { class: 'fs-ic' }, ico(ICON.file, 16)), h('span', { class: 'fs-nm' }, e.name), h('span', { class: 'fs-sz' }, fmtBytes(e.size)));
    });
    body.replaceChildren(cur.entries.length ? h('div', null, rows) : h('p', { class: 'muted' }, 'This folder is empty. Files dropped here (or sent from the left) appear here.'));
    paintActions();
  };
  const nav = async (rel) => {
    const f = S.files; if (!f) return;
    f.pcRel = rel; f.pcSel.clear();
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
  const nLocal = f.localSel.size, nPc = f.pcSel.size;
  const canSend = nLocal > 0 && !!f.pcId;
  const canGet = nPc > 0 && !!f.pcId && f.localPath !== '';
  const plural = n => n === 1 ? '1 file' : n + ' files';
  const sendLabel = nLocal === 1 ? `Send “${[...f.localSel.values()][0]}”`
    : (nLocal ? `Send ${plural(nLocal)}` : 'Send to PC');
  const getLabel = nPc === 1 ? `Get “${[...f.pcSel][0]}”`
    : (nPc ? `Get ${plural(nPc)}` : 'Get from PC');
  const get = h('button', { class: 'btn primary', disabled: !canGet, onclick: e => doGet(e.target) }, ico(ICON.download, 15), getLabel);
  const send = h('button', { class: 'btn primary', disabled: !canSend, onclick: e => doSend(e.target) }, sendLabel, ico(ICON.send, 15));
  const hint = !f.pcId ? 'Pick a PC on the right.'
    : (!nLocal && !nPc ? 'Tick one or more files in either pane.'
      : (nPc && !f.localPath ? 'Open a folder on the left to receive the files.' : ''));
  bar.replaceChildren(get, send, hint ? h('span', { class: 'muted small' }, hint) : null);
}

async function doSend(btn) {
  const f = S.files; if (!f || !f.localSel.size || !f.pcId) return;
  const items = [...f.localSel.entries()]; // [path, name]
  const pcId = f.pcId, destRel = f.pcRel;
  await act(btn, async () => {
    for (const [path] of items) await api('POST', '/api/fs/send', { pcId, srcPath: path, destRel });
  }, 'Sending…').then(() => { if (S.files) { S.files.localSel.clear(); paintActions(); S.files.loadXfers(); setTimeout(() => S.files && S.files.refreshPC(), 400); } }).catch(() => {});
}

async function doGet(btn) {
  const f = S.files; if (!f || !f.pcSel.size || !f.pcId || !f.localPath) return;
  const names = [...f.pcSel];
  const pcId = f.pcId, srcRel = f.pcRel, destDir = f.localPath;
  await act(btn, async () => {
    for (const name of names) await api('POST', '/api/fs/get', { pcId, srcRel, name, destDir });
  }, 'Getting…').then(() => { if (S.files) { S.files.pcSel.clear(); paintActions(); S.files.loadXfers(); setTimeout(() => S.files && S.files.refreshLocal(), 400); } }).catch(() => {});
}

// ---------- Incoming-call prompts ----------

// updateCallBar renders the persistent prompts for answered incoming calls. It
// runs on every snapshot, so prompts appear on any tab and survive reconnects.
function updateCallBar() {
  const bar = document.getElementById('callbar');
  if (!bar) return;
  const calls = (S.snap && S.snap.pendingCalls) || [];
  bar.replaceChildren(...calls.map(callPromptCard));
}

function callPromptCard(call) {
  const known = call.clientId > 0;
  const book = h('button', { class: 'btn small primary', onclick: () => startBookingForCall(call) }, known ? 'Book a PC' : 'Add & book');
  const dismiss = h('button', { class: 'btn small', onclick: e => act(e.target, () => api('POST', `/api/calls/${call.id}/dismiss`)).catch(() => {}) }, 'Dismiss');
  return h('div', { class: 'callprompt' + (known ? ' known' : '') },
    h('span', { class: 'callicon' }, '📞'),
    h('span', { class: 'callwho' }, known ? h('b', null, call.clientName) : h('b', null, 'New caller'),
      known ? ' wants to book' : ' — not a client yet'),
    h('span', { class: 'callnum' }, call.number),
    h('span', { class: 'grow' }),
    book, dismiss);
}

// startBookingForCall drives the call → booking flow. A known caller goes
// straight to a pre-filled booking; an unknown one is added as a client first.
function startBookingForCall(call) {
  const reload = () => switchTab('bookings');
  if (call.clientId > 0) {
    api('POST', `/api/calls/${call.id}/dismiss`).catch(() => {});
    bookingModal(null, reload, { clientId: call.clientId });
  } else {
    addClientModal(call.number, (newId) => {
      api('POST', `/api/calls/${call.id}/dismiss`).catch(() => {});
      bookingModal(null, reload, { clientId: newId });
    });
  }
}

// addClientModal adds a client with the number pre-filled (the name is all the
// operator has to type), then hands the new id back to continue into booking.
function addClientModal(number, onCreated) {
  const name = h('input', { type: 'text', placeholder: 'Caller name' });
  const phone = h('input', { type: 'text', value: number });
  const note = h('input', { type: 'text', placeholder: 'e.g. VIP, regular' });
  const btn = h('button', { class: 'btn primary' }, 'Add & continue');
  const m = modal('New caller', h('div', null,
    h('p', { class: 'muted small' }, 'This number isn\'t a client yet. Add a name to book for them.'),
    field('Name', name), field('Phone', phone), field('Note (optional)', note)),
    [h('button', { class: 'btn', onclick: () => m.close() }, 'Cancel'), btn]);
  btn.onclick = async () => {
    if (!name.value.trim()) { toast('Name is required.', 'error'); return; }
    try {
      btn.disabled = true;
      const r = await api('POST', '/api/clients', { name: name.value, phone: phone.value, note: note.value });
      m.close();
      onCreated(r.id);
    } catch (e) { toast(e.message, 'error'); btn.disabled = false; }
  };
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
  // Delete removes the booking from the schedule entirely. A started booking's
  // running sessions keep going; only the reservation record is removed.
  const delMsg = b.status === 'started'
    ? `Delete ${b.who}'s booking? The PCs keep their running sessions — only the reservation is removed. To stop the sessions, use Cancel instead.`
    : `Delete ${b.who}'s booking? This removes it from the schedule for good.`;
  actions.push(h('button', { class: 'btn small danger', onclick: async e => { if (await confirmBox('Delete booking', delMsg, 'Delete', true)) act(e.target, () => api('DELETE', `/api/bookings/${b.id}`), 'Booking deleted').then(reload).catch(() => {}); } }, 'Delete'));
  return h('tr', null,
    h('td', null, `${timeStr(b.startAt)}–${timeStr(b.endAt)}`),
    h('td', null, b.who, b.phone ? h('div', { class: 'muted small' }, b.phone) : null, b.kind === 'group' ? h('div', { class: 'muted small' }, `group · ${b.members} member(s)`) : null, b.note ? h('div', { class: 'muted small' }, b.note) : null),
    h('td', { class: 'small' }, (b.pcNames || []).join(', ')),
    h('td', null, h('span', { class: 'badge ' + cls }, lbl)),
    h('td', null, h('div', { class: 'row' }, actions)));
}

async function bookingModal(existing, reload, preset) {
  let clients = [], groups = [];
  try { [clients, groups] = await Promise.all([api('GET', '/api/clients'), api('GET', '/api/groups')]); }
  catch (e) { toast(e.message, 'error'); return; }

  const presetClient = (!existing && preset && preset.clientId) ? Number(preset.clientId) : 0;
  const start = existing ? new Date(existing.startAt) : (() => { const d = new Date(); d.setMinutes(0, 0, 0); d.setHours(d.getHours() + 1); return d; })();
  const end = existing ? new Date(existing.endAt) : new Date(start.getTime() + 2 * 3600000);

  const groupsForClient = cid => groups.filter(g => (g.members || []).some(m => m.id === cid));

  let mode = existing && existing.kind === 'group' ? 'group' : 'client';
  const modeBtns = [['client', 'Individual client'], ['group', 'Group']].map(([k, l]) =>
    h('button', { type: 'button', class: 'btn', onclick: () => { mode = k; drawPicker(); } }, l));
  const pickerBox = h('div');
  const hintBox = h('div', { class: 'grouphints' });
  let picker = null;
  function drawPicker(selectId) {
    modeBtns.forEach((btn, i) => btn.classList.toggle('primary', ['client', 'group'][i] === mode));
    if (mode === 'client') {
      picker = h('select', null, h('option', { value: 0 }, '— choose a client —'), clients.map(c => h('option', { value: c.id }, `${c.name} · ${c.phone}`)));
      if (selectId) picker.value = String(selectId);
      else if (existing && existing.clientId) picker.value = String(existing.clientId);
      else if (presetClient) picker.value = String(presetClient);
      picker.onchange = drawHints;
      pickerBox.replaceChildren(clients.length ? picker : h('p', { class: 'muted small' }, 'No clients yet — add one in the Clients tab first.'));
      drawHints();
    } else {
      picker = h('select', null, h('option', { value: 0 }, '— choose a group —'), groups.map(g => h('option', { value: g.id }, `${g.name} (${g.members.length})`)));
      if (selectId) picker.value = String(selectId);
      else if (existing && existing.groupId) picker.value = String(existing.groupId);
      picker.onchange = () => onGroupChosen();
      pickerBox.replaceChildren(groups.length ? picker : h('p', { class: 'muted small' }, 'No groups yet — make one in the Clients tab first.'));
      hintBox.replaceChildren();
      onGroupChosen();
    }
  }

  // In client mode, offer the groups this client belongs to as one-tap options.
  function drawHints() {
    if (mode !== 'client') { hintBox.replaceChildren(); return; }
    const cid = Number(picker && picker.value);
    const gs = cid ? groupsForClient(cid) : [];
    if (!gs.length) { hintBox.replaceChildren(); return; }
    hintBox.replaceChildren(
      h('span', { class: 'muted small' }, 'Booking for a team? '),
      ...gs.map(g => h('button', { type: 'button', class: 'btn small', onclick: () => { mode = 'group'; drawPicker(g.id); } }, `${g.name} (${g.members.length})`)));
  }

  // When a group is chosen, pre-tick that many free PCs as a starting point
  // (only if nothing is ticked yet — the operator can change it freely).
  function onGroupChosen() {
    const gid = Number(picker && picker.value);
    const g = groups.find(x => x.id === gid);
    if (g && chosenPCs().length === 0) autoTick((g.members || []).length);
  }

  const dateIn = h('input', { type: 'date', value: isoOf(start) });
  const startIn = h('input', { type: 'time', value: timeInputVal(start) });
  const endIn = h('input', { type: 'time', value: timeInputVal(end) });
  const note = h('input', { type: 'text', value: existing ? existing.note || '' : '', placeholder: 'optional' });

  const pcs = (S.snap ? S.snap.pcs : []);
  const checks = pcs.map(pc => {
    const cb = h('input', { type: 'checkbox', value: pc.id });
    if (existing && (existing.pcIds || []).includes(pc.id)) cb.checked = true;
    const tag = h('span', { class: 'pctag' });
    return { pc, cb, tag, booked: false, el: h('label', { class: 'pcpick' }, cb, h('span', { class: 'pcnm' }, pc.name), tag) };
  });
  const pcBox = h('div', { class: 'pcpicks' }, checks.map(c => c.el));
  const availBox = h('div', { class: 'availnote muted small' });

  const chosenPCs = () => checks.filter(c => c.cb.checked).map(c => c.pc.id);
  function autoTick(n) {
    let left = n;
    for (const c of checks) { if (left <= 0) break; if (!c.booked && !c.cb.checked) { c.cb.checked = true; left--; } }
  }

  // windowFromInputs returns the chosen booking window in unix seconds, or null.
  function windowFromInputs() {
    const s = new Date(`${dateIn.value}T${startIn.value}`);
    let e2 = new Date(`${dateIn.value}T${endIn.value}`);
    if (isNaN(s) || isNaN(e2)) return null;
    if (e2 <= s) e2 = new Date(e2.getTime() + 86400000);
    return { from: Math.floor(s.getTime() / 1000), to: Math.floor(e2.getTime() / 1000) };
  }

  // recomputeAvail marks which PCs are already booked for the chosen window.
  async function recomputeAvail() {
    const win = windowFromInputs();
    if (!win) { availBox.replaceChildren(); return; }
    let list = [];
    try { list = await api('GET', `/api/bookings?from=${win.from - 86400}&to=${win.to}`); } catch (e) { return; }
    const booked = new Set();
    for (const b of list) {
      if (existing && b.id === existing.id) continue;
      if (b.status !== 'scheduled' && b.status !== 'started') continue;
      const bs = new Date(b.startAt).getTime() / 1000, be = new Date(b.endAt).getTime() / 1000;
      if (bs < win.to && be > win.from) (b.pcIds || []).forEach(id => booked.add(id));
    }
    let free = 0;
    for (const c of checks) {
      c.booked = booked.has(c.pc.id);
      c.el.classList.toggle('booked', c.booked);
      c.tag.textContent = c.booked ? 'booked' : '';
      if (!c.booked) free++;
    }
    availBox.replaceChildren(`${free} of ${checks.length} PCs free for ${startIn.value}–${endIn.value}` + (free < checks.length ? ' (booked ones are marked)' : ''));
  }
  [dateIn, startIn, endIn].forEach(el => el.addEventListener('change', recomputeAvail));

  const btn = h('button', { class: 'btn primary' }, existing ? 'Save booking' : 'Create booking');
  const m = modal(existing ? 'Edit booking' : 'New booking', h('div', null,
    h('div', { class: 'muted small', style: 'margin-bottom:4px' }, 'Booked for'),
    h('div', { class: 'row', style: 'margin-bottom:6px' }, modeBtns), pickerBox, hintBox,
    h('div', { class: 'section' },
      h('div', { class: 'row' }, field('Day', dateIn), field('From', startIn), field('To', endIn)),
      h('div', { class: 'muted small' }, 'If "To" is earlier than "From", it counts as overnight (next day).')),
    h('div', { class: 'section' }, h('div', { class: 'muted small', style: 'margin-bottom:4px' }, 'PCs'), availBox, pcBox),
    field('Note', note)),
  [h('button', { class: 'btn', onclick: () => m.close() }, 'Cancel'), btn]);
  drawPicker();
  recomputeAvail();

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

// showMemberPassword presents a one-time generated password. It's shown once and
// only the hash is stored, so the operator must pass it on now (SMS later).
function showMemberPassword(name, pw) {
  const code = h('div', { class: 'pw-code' }, pw || '(none)');
  const copy = h('button', { class: 'btn small', onclick: () => { if (navigator.clipboard) navigator.clipboard.writeText(pw).then(() => toast('Copied', 'ok'), () => {}); } }, 'Copy');
  let m = modal('Member password — ' + name, h('div', null,
    h('p', { class: 'muted small' }, `Give this password to ${name}. It is shown once and can't be recovered — if it's lost, use “Reset password”.`),
    code,
    h('div', { class: 'row', style: 'margin-top:8px' }, copy)),
    [h('button', { class: 'btn primary', onclick: () => m.close() }, 'Done')]);
}

// clientSelectEl returns a themed client picker (members marked ★). It reads
// like a <select>: `.value` is the chosen client id as a string, "0" for none.
// The list is populated asynchronously.
function clientSelectEl(selectedId, placeholder) {
  wireDropdowns();
  const ph = placeholder || '— no client —';
  let clients = [], selId = selectedId || 0;
  const label = id => { const c = clients.find(x => x.id === id); return c ? `${c.name} · ${c.phone}` : ph; };

  const valSpan = h('span', { class: 'val' }, label(selId));
  const trigger = h('button', { type: 'button', class: 'btn selbtn block' },
    h('span', { class: 'selval' }, valSpan), h('span', { class: 'caret' }, ico(ICON.chevron, 15)));
  const menu = h('div', { class: 'menu scroll' });
  const dd = h('span', { class: 'dd block' }, trigger, menu);
  Object.defineProperty(dd, 'value', { get: () => String(selId) });

  const pick = id => { selId = id; valSpan.textContent = label(id); build(); };
  const build = () => menu.replaceChildren(
    menuItem(ph, () => pick(0), { check: true, sel: selId === 0 }),
    ...clients.map(c => menuItem(`${c.name} · ${c.phone}`, () => pick(c.id), { check: true, sel: c.id === selId, hint: c.authorized ? '★' : null })));

  trigger.addEventListener('click', ev => { ev.stopPropagation(); toggleDD(dd); });
  menu.addEventListener('click', ev => ev.stopPropagation());
  build();
  api('GET', '/api/clients').then(list => { clients = list; valSpan.textContent = label(selId); build(); }).catch(() => {});
  return dd;
}

function renderClients() {
  const view = S.clientView || 'basic';
  if (view === 'auth') return clientsCard(null, 'auth');
  const wrap = h('div');
  const groups = groupsCard();
  wrap.append(clientsCard(() => groups.reload(), 'basic'), groups.el);
  return wrap;
}

// clientsCard renders the clients table. mode 'basic' shows non-members plus the
// add form; 'auth' shows only members (no add form).
function clientsCard(onChange, mode) {
  mode = mode || 'basic';
  const box = h('div', null, h('p', { class: 'muted' }, 'Loading…'));
  const search = h('input', { type: 'text', placeholder: 'Search by name or phone…', style: 'flex:1' });
  let timer = null;
  const load = async () => {
    try {
      const all = await api('GET', `/api/clients?q=${encodeURIComponent(search.value.trim())}`);
      const list = all.filter(c => mode === 'auth' ? c.authorized : !c.authorized);
      box.replaceChildren(list.length ? h('div', { style: 'overflow-x:auto' }, h('table', null,
        h('tr', null, ['Name', 'Phone', 'Note', 'Member', 'Hours', ''].map(t => h('th', null, t))),
        list.map(c => {
          const name = h('input', { type: 'text', value: c.name, style: 'width:140px' });
          const phone = h('input', { type: 'text', value: c.phone, style: 'width:120px' });
          const note = h('input', { type: 'text', value: c.note || '', placeholder: 'e.g. VIP, banned', style: 'width:160px' });
          const save = h('button', { class: 'btn small primary', onclick: e => act(e.target, () => api('PUT', `/api/clients/${c.id}`, { name: name.value, phone: phone.value, note: note.value }), 'Saved').catch(() => {}) }, 'Save');
          const del = h('button', { class: 'btn small danger', onclick: async e => { if (await confirmBox('Delete client', `Delete ${c.name}? Booking history is kept.`, 'Delete', true)) { await act(e.target, () => api('DELETE', `/api/clients/${c.id}`), 'Deleted').then(() => { load(); onChange && onChange(); }).catch(() => {}); } } }, 'Delete');
          const memberCell = c.authorized
            ? h('span', null, h('span', { class: 'badge ok' }, 'Member'), c.registeredAt ? h('div', { class: 'muted small' }, 'since ' + dateTimeStr(c.registeredAt)) : null, h('div', { class: 'muted small' }, 'Ratings: not available yet'))
            : h('span', { class: 'muted small' }, 'Basic');
          const authBtns = c.authorized
            ? [h('button', { class: 'btn small', onclick: e => act(e.target, () => api('POST', `/api/clients/${c.id}/reset-password`)).then(r => showMemberPassword(c.name, r.password)).catch(() => {}) }, 'Reset password'),
               h('button', { class: 'btn small danger', onclick: async e => { if (await confirmBox('Remove membership', `Remove ${c.name}'s member login? Their played hours are kept.`, 'Remove', true)) { await act(e.target, () => api('POST', `/api/clients/${c.id}/deauthorize`), 'Membership removed').then(load).catch(() => {}); } } }, 'Remove')]
            : [h('button', { class: 'btn small primary', onclick: e => act(e.target, () => api('POST', `/api/clients/${c.id}/authorize`)).then(r => { showMemberPassword(c.name, r.password); load(); }).catch(() => {}) }, 'Authorize')];
          return h('tr', null, h('td', null, name), h('td', null, phone), h('td', null, note), h('td', null, memberCell), h('td', null, mins(Math.floor((c.hoursSeconds || 0) / 60))), h('td', null, h('div', { class: 'row', style: 'gap:4px' }, save, del, ...authBtns)));
        }))) : h('p', { class: 'muted' }, search.value.trim() ? 'No clients match.' : (mode === 'auth' ? 'No members yet. Authorize a client in Basic clients.' : 'No clients yet.')));
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
  if (mode === 'auth') {
    return h('div', { class: 'card' }, h('h2', null, 'Authorized clients'),
      h('p', { class: 'muted small' }, 'Members can log into the mobile app with their phone number + password. Hours are counted automatically; ratings arrive with the app phase.'),
      h('div', { class: 'row', style: 'margin-bottom:10px' }, search),
      box);
  }
  return h('div', { class: 'card' }, h('h2', null, 'Basic clients'),
    h('p', { class: 'muted small' }, 'Contacts for bookings and caller matching. Phone must be a valid Turkmen number and is unique; names may repeat. Authorize a client to make them a member.'),
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
  wrap.append(clubCard(), appearanceCard(), dataCard(), installCard(), zonesCard(), packagesCard(), pcsCard(), staffCard(), simulateCallCard());
  return wrap;
}

// simulateCallCard lets you test the incoming-call → booking flow without a
// phone: type a number and it appears as a prompt exactly as a real call would.
function simulateCallCard() {
  const num = h('input', { type: 'text', placeholder: 'e.g. 65 12 34 56', style: 'width:160px' });
  const go = h('button', { class: 'btn' }, 'Simulate incoming call');
  go.onclick = () => act(go, () => api('POST', '/api/calls/simulate', { number: num.value }), 'Call prompt sent').then(() => { num.value = ''; }).catch(() => {});
  return h('div', { class: 'card' }, h('h2', null, 'Testing: simulate an incoming call'),
    h('p', { class: 'muted small' }, 'Pretends the operator phone received a call from this number. The prompt appears at the top on any tab — known numbers offer to book, unknown ones to add the caller first. For trying the call-to-booking flow without the phone.'),
    h('div', { class: 'row' }, num, go));
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

// Upload a raw image to an appearance slot (desktop | lock_bg).
async function uploadAppearance(kind, file) {
  const res = await fetch(`/api/settings/appearance/${kind}`, {
    method: 'PUT', credentials: 'same-origin',
    headers: { 'X-NextGaming': '1', 'Content-Type': file.type || 'application/octet-stream' }, body: file,
  });
  let data = null; try { data = await res.json(); } catch (e) { /* empty */ }
  if (!res.ok) throw new Error((data && data.error) || `Error ${res.status}`);
  return data;
}

// Build a full settings PUT body from the current snapshot plus overrides.
function settingsBody(st, over) {
  return Object.assign({
    clubName: st.clubName, currency: st.currency, graceSeconds: st.graceSeconds, minChargeMinutes: st.minChargeMinutes,
    warnMinutes: st.warnMinutes || [], autoFixWol: st.autoFixWol, requireShift: st.requireShift, namePosition: st.namePosition || 'center',
  }, over || {});
}

function appearanceCard() {
  const st = S.snap.settings;
  const slot = (kind, label, hint) => {
    const ver = kind === 'desktop' ? st.desktopWallpaperVer : st.lockScreenVer;
    const img = h('img', { class: 'wp-prev', alt: '' });
    const empty = h('div', { class: 'wp-empty' }, 'No image set');
    if (ver > 0) { img.src = `/api/settings/appearance/${kind}?t=${ver}`; empty.style.display = 'none'; img.onerror = () => { img.style.display = 'none'; empty.style.display = 'flex'; }; }
    else img.style.display = 'none';
    const file = h('input', { type: 'file', accept: 'image/png,image/jpeg', style: 'display:none' });
    const up = h('button', { class: 'btn small' }, ver > 0 ? 'Replace image' : 'Upload image');
    up.onclick = () => file.click();
    file.onchange = () => { if (file.files[0]) act(up, () => uploadAppearance(kind, file.files[0]), 'Image sent to the PCs').catch(() => {}); };
    const rm = ver > 0 ? h('button', { class: 'btn small danger', onclick: e => act(e.target, () => api('DELETE', `/api/settings/appearance/${kind}`), 'Image removed').catch(() => {}) }, 'Remove') : null;
    return h('div', { class: 'wp-item' },
      h('div', { class: 'wp-prevwrap' }, img, empty),
      h('div', { class: 'wp-meta' }, h('b', null, label), hint ? h('div', { class: 'muted small' }, hint) : null,
        h('div', { class: 'row', style: 'margin-top:8px' }, up, rm), file));
  };
  const pos = selectMenu(
    [{ value: 'center', label: 'Center' }, { value: 'top-left', label: 'Top-left' }],
    st.namePosition || 'center',
    v => api('PUT', '/api/settings', settingsBody(st, { namePosition: v })).then(() => toast('Lock-screen name position saved', 'ok')).catch(e => toast(e.message, 'error')),
    { label: '' });
  return h('div', { class: 'card' }, h('h2', null, 'Appearance on the gaming PCs'),
    h('p', { class: 'muted small' }, 'PNG or JPEG, up to 6 MB. Images are pushed to every PC and take effect within a few seconds — the lock-screen image is dimmed slightly so the text stays readable.'),
    h('div', { class: 'wp-grid' },
      slot('desktop', 'Desktop wallpaper', 'The Windows desktop background each player sees.'),
      slot('lock_bg', 'Lock-screen background', 'Behind the club name and timer on the lock screen.')),
    h('div', { class: 'field', style: 'max-width:240px;margin-top:14px' }, h('span', { class: 'fl' }, 'PC name on the lock screen'), pos));
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
