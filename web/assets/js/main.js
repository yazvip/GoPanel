import { api, setCSRF, onUnauthorized, errorMessage } from './api.js';
import { loadLocale, savedLocale, setLocale, t, getLang, localeName } from './i18n.js';
import { h, iconSpan, toast, toastError, openModal, field, input, button } from './ui.js';
import { createRouter, navigate } from './router.js';
import { createUpdateBadge, createAboutUpdateRow } from './update.js';

const state = {
  user: null,
  csrf: '',
  capabilities: {},
  panelName: 'GoPanel',
  version: 'dev',
  routeTitle: '',
  theme: 'light',
  health: null,
};

export function getState() {
  return state;
}

export function canWrite() {
  return state.user && (state.user.role === 'admin' || state.user.role === 'operator');
}

export function isAdmin() {
  return state.user && state.user.role === 'admin';
}

const NAV = [
  {
    label: 'nav.groupOverview',
    items: [{ path: '/dashboard', icon: 'dashboard', key: 'nav.dashboard' }],
  },
  {
    label: 'nav.groupHosting',
    items: [
      { path: '/sites', icon: 'globe', key: 'nav.sites' },
      { path: '/databases', icon: 'database', key: 'nav.databases' },
      { path: '/files', icon: 'folder', key: 'nav.files' },
      { path: '/backups', icon: 'archive', key: 'nav.backups' },
    ],
  },
  {
    label: 'nav.groupTools',
    items: [
      { path: '/cron', icon: 'clock', key: 'nav.cron' },
      { path: '/apps', icon: 'box', key: 'nav.apps' },
      { path: '/firewall', icon: 'shield', key: 'nav.firewall' },
      { path: '/services', icon: 'server', key: 'nav.services' },
      { path: '/terminal', icon: 'terminal', key: 'nav.terminal', admin: true, capability: 'terminal' },
    ],
  },
  {
    label: 'nav.groupSystem',
    items: [
      { path: '/jobs', icon: 'activity', key: 'nav.jobs' },
      { path: '/users', icon: 'users', key: 'nav.users', admin: true },
      { path: '/audit', icon: 'filetext', key: 'nav.audit', admin: true },
      { path: '/settings', icon: 'settings', key: 'nav.settings', admin: true },
    ],
  },
];

const ROUTES = [
  { path: '/dashboard', key: 'nav.dashboard', view: (ctx) => import('./views/dashboard.js').then((m) => m.render(ctx)) },
  { path: '/sites', key: 'nav.sites', view: (ctx) => import('./views/sites.js').then((m) => m.render(ctx)) },
  { path: '/databases', key: 'nav.databases', view: (ctx) => import('./views/databases.js').then((m) => m.render(ctx)) },
  { path: '/files', key: 'nav.files', view: (ctx) => import('./views/files.js').then((m) => m.render(ctx)) },
  { path: '/backups', key: 'nav.backups', view: (ctx) => import('./views/backups.js').then((m) => m.render(ctx)) },
  { path: '/cron', key: 'nav.cron', view: (ctx) => import('./views/cron.js').then((m) => m.render(ctx)) },
  { path: '/apps', key: 'nav.apps', view: (ctx) => import('./views/apps.js').then((m) => m.render(ctx)) },
  { path: '/firewall', key: 'nav.firewall', view: (ctx) => import('./views/firewall.js').then((m) => m.render(ctx)) },
  { path: '/services', key: 'nav.services', view: (ctx) => import('./views/services.js').then((m) => m.render(ctx)) },
  { path: '/terminal', key: 'nav.terminal', admin: true, capability: 'terminal', view: (ctx) => import('./views/terminal.js').then((m) => m.render(ctx)) },
  { path: '/jobs', key: 'nav.jobs', view: (ctx) => import('./views/jobs.js').then((m) => m.render(ctx)) },
  { path: '/users', key: 'nav.users', admin: true, view: (ctx) => import('./views/users.js').then((m) => m.render(ctx)) },
  { path: '/audit', key: 'nav.audit', admin: true, view: (ctx) => import('./views/audit.js').then((m) => m.render(ctx)) },
  { path: '/settings', key: 'nav.settings', admin: true, view: (ctx) => import('./views/settings.js').then((m) => m.render(ctx)) },
  { path: '/profile', key: 'nav.profile', view: (ctx) => import('./views/profile.js').then((m) => m.render(ctx)) },
];

let router = null;
let currentLogin = null;

function resolveTheme(pref) {
  if (pref === 'dark' || pref === 'light') return pref;
  return window.matchMedia && window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
}

export function applyTheme(pref, persist = true) {
  const resolved = resolveTheme(pref);
  state.theme = pref;
  document.documentElement.dataset.theme = resolved;
  const meta = document.querySelector('meta[name="theme-color"]');
  if (meta) meta.setAttribute('content', resolved === 'dark' ? '#0a1314' : '#f2f4f2');
  if (persist) {
    try { localStorage.setItem('gopanel.theme', pref); } catch { /* ignore */ }
  }
}

function savedTheme() {
  try { return localStorage.getItem('gopanel.theme') || 'system'; } catch { return 'system'; }
}

export function setPanelName(name) {
  if (name) state.panelName = name;
  const brand = document.querySelector('.brand-name');
  if (brand) brand.textContent = state.panelName;
  document.title = `${state.routeTitle ? `${state.routeTitle} · ` : ''}${state.panelName}`;
}

async function boot() {
  const preferred = savedLocale() || ((navigator.language || 'en').toLowerCase().startsWith('id') ? 'id' : 'en');
  const lang = preferred === 'id' ? 'id' : 'en';
  try {
    await loadLocale(lang);
  } catch {
    await loadLocale('en');
  }
  applyTheme(savedTheme(), false);

  let health = null;
  try { health = await api.get('/healthz'); } catch { /* ignore */ }
  state.health = health;
  if (health && health.version) state.version = health.version;

  onUnauthorized(() => {
    state.user = null;
    setCSRF('');
    showLogin(t('auth.sessionExpired'));
  });

  try {
    const me = await api.get('/auth/me');
    await enterApp(me);
  } catch (err) {
    if (err.status === 401) showLogin();
    else {
      const app = document.getElementById('app');
      app.replaceChildren(h('div', { class: 'boot-screen' },
        h('div', { class: 'boot-name' }, 'GoPanel'),
        h('div', { style: { color: '#9fb0c0', maxWidth: '440px', textAlign: 'center' } }, errorMessage(err)),
        button(t('common.retry'), { variant: 'primary', onClick: () => location.reload() })));
    }
  }
}

async function enterApp(me) {
  state.user = me.user;
  state.csrf = me.csrf_token || '';
  state.capabilities = me.capabilities || {};
  setCSRF(state.csrf);

  try {
    const settings = await api.get('/settings');
    if (settings && settings.settings && settings.settings.panel_name) {
      state.panelName = settings.settings.panel_name;
    }
    if (settings && settings.settings && settings.settings.theme) {
      const stored = localStorage.getItem('gopanel.theme');
      if (!stored) applyTheme(settings.settings.theme, false);
    }
  } catch { /* settings are optional for boot */ }

  renderShell();
  if (state.user.must_change_password && !location.hash.includes('/profile')) {
    toast(t('auth.mustChangePassword'), 'warn', 8000);
    navigate('/profile');
  }
}

let themeMediaBound = false;

function renderShell() {
  if (router) { router.stop(); router = null; }
  const app = document.getElementById('app');
  app.replaceChildren();

  const sidebar = buildSidebar();
  const content = h('main', { class: 'content' });
  const outlet = h('div', { id: 'view' });
  content.append(outlet);
  const topbar = buildTopbar();
  const main = h('div', { class: 'main' }, topbar, content);
  const backdrop = h('div', { class: 'sidebar-backdrop', hidden: true, onClick: closeSidebar });
  const shell = h('div', { class: 'app-shell' }, sidebar, backdrop, main);
  app.append(shell);

  router = createRouter({
    outlet,
    routes: ROUTES,
    guard: (route, path) => {
      if (state.user && state.user.must_change_password && path !== '/profile') {
        return '/profile';
      }
      if (route.admin && !isAdmin()) return { message: t('auth.forbidden') || 'Access denied' };
      if (route.capability && !state.capabilities[route.capability]) {
        return { message: t('auth.forbidden') || 'Access denied' };
      }
      return null;
    },
    onNavigate: (path, route) => {
      state.routeTitle = route ? t(route.key) : '';
      document.title = `${state.routeTitle ? `${state.routeTitle} · ` : ''}${state.panelName}`;
      updateBreadcrumb(route);
      markActive(path);
      closeSidebar();
    },
    onError: (err) => {
      toastError(err);
      outlet.replaceChildren(h('div', { class: 'card' }, h('div', { class: 'card-body' },
        h('div', { class: 'alert alert-danger' }, iconSpan('alert', 16), h('div', {}, errorMessage(err))),
        button(t('common.retry'), { class: 'mt2', onClick: () => router.render() }))));
    },
  });
  router.start();
  startHealthWatch();

  if (!themeMediaBound && window.matchMedia) {
    themeMediaBound = true;
    const mq = window.matchMedia('(prefers-color-scheme: dark)');
    mq.addEventListener('change', () => {
      if (state.theme === 'system') applyTheme('system', false);
    });
  }
}

function buildSidebar() {
  const nav = h('nav', { class: 'nav' });
  for (const group of NAV) {
    const items = group.items.filter((item) => {
      if (item.admin && !isAdmin()) return false;
      if (item.capability && !state.capabilities[item.capability]) return false;
      return true;
    });
    if (!items.length) continue;
    const g = h('div', { class: 'nav-group' }, h('div', { class: 'nav-label' }, t(group.label)));
    for (const item of items) {
      const btn = h('button', {
        class: 'nav-item', type: 'button', dataset: { path: item.path },
        onClick: () => navigate(item.path),
      }, iconSpan(item.icon, 18), h('span', {}, t(item.key)));
      g.append(btn);
    }
    nav.append(g);
  }
  const online = Boolean(state.health);
  const foot = h('div', { class: 'sidebar-foot' },
    h('div', { class: 'side-status' },
      h('span', { class: `pulse-dot${online ? '' : ' off'}` }),
      h('span', { class: 'side-status-label' }, online ? t('common.online') : t('common.offline')),
      h('span', { class: 'side-version' }, versionLabel(state.version))),
    h('div', { class: 'side-user' }, state.user ? `${state.user.username} · ${state.user.role}` : ''));
  return h('aside', { class: 'sidebar', id: 'sidebar' },
    h('div', { class: 'sidebar-head' },
      h('div', { class: 'brand' },
        h('div', { class: 'brand-mark' }, 'G'),
        h('div', { class: 'brand-text' },
          h('div', { class: 'brand-name' }, state.panelName),
          h('div', { class: 'brand-sub' }, 'VPS Control Panel')))),
    nav, foot);
}

function buildTopbar() {
  if (updateBadge) {
    updateBadge.stop();
    updateBadge = null;
  }
  if (isAdmin()) {
    updateBadge = createUpdateBadge();
    updateBadge.start();
  }

  const crumbs = h('div', { class: 'crumbs', id: 'crumbs' },
    iconSpan('home', 15), h('span', { class: 'sep' }, '/'), h('b', {}, t('nav.dashboard')));
  const themeBtn = h('button', {
    class: 'icon-btn', type: 'button', title: t('settings.theme'), 'aria-label': t('settings.theme'),
    onClick: () => {
      const next = resolveTheme(state.theme) === 'dark' ? 'light' : 'dark';
      applyTheme(next);
      updateThemeIcon(themeBtn);
    },
  });
  updateThemeIcon(themeBtn);

  const userBtn = h('button', { class: 'user-chip', type: 'button', onClick: (ev) => openUserMenu(ev.currentTarget) },
    h('div', { class: 'avatar' }, (state.user.username || '?').slice(0, 1).toUpperCase()),
    h('div', { class: 'user-meta' }, h('b', {}, state.user.username), h('span', {}, state.user.role)));

  return h('header', { class: 'topbar' },
    h('div', { class: 'topbar-left' },
      h('button', {
        class: 'icon-btn menu-toggle', type: 'button', 'aria-label': t('common.menu'),
        onClick: () => {
          document.body.classList.add('sidebar-open');
          const bd = document.querySelector('.sidebar-backdrop');
          if (bd) bd.hidden = false;
        },
      }, iconSpan('menu', 19)),
      crumbs),
    h('div', { class: 'topbar-right' },
      updateBadge ? updateBadge.chip : null,
      themeBtn,
      userBtn));
}

function updateThemeIcon(btn) {
  const dark = resolveTheme(state.theme) === 'dark';
  btn.replaceChildren(iconSpan(dark ? 'sun' : 'moon', 18));
}

function updateBreadcrumb(route) {
  const el = document.getElementById('crumbs');
  if (!el) return;
  el.replaceChildren(iconSpan('home', 15), h('span', { class: 'sep' }, '/'),
    h('b', {}, route ? t(route.key) : t('common.notFound')));
}

function markActive(path) {
  document.querySelectorAll('.nav-item').forEach((el) => {
    const active = el.dataset.path === path;
    el.classList.toggle('active', active);
    if (active) el.setAttribute('aria-current', 'page');
    else el.removeAttribute('aria-current');
  });
}

let healthTimer = null;
let updateBadge = null;

function startHealthWatch() {
  if (healthTimer) clearInterval(healthTimer);
  healthTimer = setInterval(async () => {
    if (!document.querySelector('.side-status')) {
      clearInterval(healthTimer);
      healthTimer = null;
      return;
    }
    let health = null;
    try { health = await api.get('/healthz'); } catch { /* treat as offline */ }
    state.health = health;
    if (health && health.version) state.version = health.version;
    updateSidebarStatus();
  }, 45000);
}

function versionLabel(version) {
  const v = String(version || '').trim();
  if (!v) return '';
  return /^\d/.test(v) ? `v${v}` : v;
}

function updateSidebarStatus() {
  const node = document.querySelector('.side-status');
  if (!node) return;
  const online = Boolean(state.health);
  const dot = node.querySelector('.pulse-dot');
  if (dot) dot.classList.toggle('off', !online);
  const label = node.querySelector('.side-status-label');
  if (label) label.textContent = online ? t('common.online') : t('common.offline');
  const ver = node.querySelector('.side-version');
  if (ver) ver.textContent = versionLabel(state.version);
}

function closeSidebar() {
  document.body.classList.remove('sidebar-open');
  const bd = document.querySelector('.sidebar-backdrop');
  if (bd) bd.hidden = true;
}

function openUserMenu(anchor) {
  document.querySelectorAll('.menu-pop').forEach((el) => el.remove());
  const menu = h('div', { class: 'menu-pop' },
    h('button', { type: 'button', onClick: () => { menu.remove(); navigate('/profile'); } },
      iconSpan('user', 16), t('nav.profile')),
    h('button', { type: 'button', onClick: () => { menu.remove(); openAbout(); } },
      iconSpan('info', 16), t('common.about')),
    h('div', { class: 'menu-sep' }),
    h('button', {
      type: 'button', class: 'danger',
      onClick: async () => {
        menu.remove();
        try { await api.post('/auth/logout', {}); } catch { /* ignore */ }
        state.user = null;
        setCSRF('');
        showLogin();
      },
    }, iconSpan('logout', 16), t('auth.logout')));
  document.body.append(menu);
  const rect = anchor.getBoundingClientRect();
  menu.style.top = `${rect.bottom + 6}px`;
  menu.style.right = `${Math.max(12, window.innerWidth - rect.right)}px`;
  setTimeout(() => {
    document.addEventListener('click', function hide(ev) {
      if (!menu.contains(ev.target)) {
        menu.remove();
        document.removeEventListener('click', hide);
      }
    });
  }, 0);
}

function openAbout() {
  openModal({
    title: t('common.about'),
    size: 'modal-sm',
    body: [
      h('div', { style: { display: 'flex', alignItems: 'center', gap: '12px', marginBottom: '14px' } },
        h('div', { class: 'brand-mark', style: { width: '42px', height: '42px', borderRadius: '11px', background: 'var(--primary)', display: 'grid', placeItems: 'center', color: '#fff', fontWeight: '800', fontSize: '22px' } }, 'G'),
        h('div', {}, h('div', { style: { fontWeight: '700', fontSize: '16px' } }, state.panelName),
          h('div', { class: 'muted small' }, `GoPanel ${state.version}`))),
      h('div', { class: 'kv' },
        h('dt', {}, t('common.version')), h('dd', {}, state.version),
        h('dt', {}, t('settings.language')), h('dd', {}, localeName(getLang())),
        h('dt', {}, t('common.role')), h('dd', {}, state.user.role),
        h('dt', {}, t('common.uptime')), h('dd', {}, state.health ? `${Math.floor(state.health.uptime / 3600)}h` : '—')),
      isAdmin() ? createAboutUpdateRow() : null,
    ],
    footer: h('button', { class: 'btn btn-primary', type: 'button', onClick: () => document.querySelector('.modal-close')?.click() }, t('common.close')),
  });
}

/* ---------------- login ---------------- */
function showLogin(notice = '') {
  if (router) { router.stop(); router = null; }
  if (updateBadge) { updateBadge.stop(); updateBadge = null; }
  if (currentLogin) currentLogin.remove();
  const app = document.getElementById('app');
  app.replaceChildren();
  currentLogin = buildLogin(notice);
  app.append(currentLogin);
}

function buildLogin(notice) {
  const errorBox = h('div', { class: 'login-error', hidden: true });
  const form = h('form', { class: 'login-form' });
  let step = 'password';
  let preauth = '';
  let busy = false;

  const setError = (msg) => {
    if (!msg) { errorBox.hidden = true; errorBox.textContent = ''; return; }
    errorBox.textContent = msg;
    errorBox.hidden = false;
  };

  const renderPassword = () => {
    step = 'password';
    form.replaceChildren();
    const user = input({ name: 'username', autocomplete: 'username', placeholder: t('auth.usernamePlaceholder'), autofocus: true, required: true });
    const pass = input({ name: 'password', type: 'password', autocomplete: 'current-password', placeholder: t('auth.passwordPlaceholder'), required: true });
    const submit = h('button', { class: 'btn btn-primary btn-block', type: 'submit' }, t('auth.signIn'));
    const fields = [field(t('auth.username'), user), field(t('auth.password'), pass)];
    if (notice) {
      fields.push(h('div', { class: 'alert alert-info mb2' }, iconSpan('info', 15), h('div', {}, notice)));
    }
    fields.push(submit);
    form.append(...fields);
    form.onsubmit = async (ev) => {
      ev.preventDefault();
      if (busy) return;
      busy = true;
      setError('');
      submit.disabled = true;
      submit.replaceChildren(h('span', { class: 'spinner' }), t('auth.signingIn'));
      try {
        const res = await api.post('/auth/login', { username: user.value.trim(), password: pass.value });
        if (res.two_factor_required) {
          preauth = res.preauth_token;
          renderTOTP();
          return;
        }
        await finishLogin(res);
      } catch (err) {
        setError(errorMessage(err));
      } finally {
        busy = false;
        if (submit.isConnected) {
          submit.disabled = false;
          submit.textContent = t('auth.signIn');
        }
      }
    };
    setTimeout(() => user.focus(), 40);
  };

  const renderTOTP = () => {
    step = 'totp';
    setError('');
    form.replaceChildren();
    const code = input({ name: 'code', inputmode: 'numeric', autocomplete: 'one-time-code', placeholder: '000000', class: 'input twofa-code', maxlength: '6', autofocus: true });
    const recovery = input({ name: 'recovery', autocomplete: 'off', placeholder: t('auth.recoveryPlaceholder'), hidden: true, class: 'input mono' });
    const useRecovery = h('button', { class: 'login-back', type: 'button', style: { marginBottom: '0', marginTop: '10px' } }, t('auth.useRecovery'));
    const submit = h('button', { class: 'btn btn-primary btn-block', type: 'submit' }, t('auth.verify'));
    let mode = 'code';
    useRecovery.addEventListener('click', () => {
      mode = mode === 'code' ? 'recovery' : 'code';
      const isRecovery = mode === 'recovery';
      code.hidden = isRecovery;
      recovery.hidden = !isRecovery;
      useRecovery.textContent = isRecovery ? t('auth.useCode') : t('auth.useRecovery');
      (isRecovery ? recovery : code).focus();
    });
    form.append(
      h('button', { class: 'login-back', type: 'button', onClick: () => { preauth = ''; renderPassword(); } },
        iconSpan('arrowLeft', 14), t('auth.back')),
      h('div', { class: 'mb2' },
        h('div', { style: { fontWeight: '600', marginBottom: '4px' } }, t('auth.twoFactorTitle')),
        h('div', { class: 'muted small' }, t('auth.twoFactorHint'))),
      field(t('auth.code'), code),
      recovery,
      useRecovery,
      submit);
    form.onsubmit = async (ev) => {
      ev.preventDefault();
      if (busy) return;
      busy = true;
      setError('');
      submit.disabled = true;
      submit.replaceChildren(h('span', { class: 'spinner' }), t('auth.verifying'));
      try {
        const payload = { preauth_token: preauth };
        if (mode === 'recovery') payload.recovery_code = recovery.value.trim();
        else payload.code = code.value.trim();
        const res = await api.post('/auth/totp', payload);
        await finishLogin(res);
      } catch (err) {
        setError(errorMessage(err));
        code.value = '';
        code.focus();
      } finally {
        busy = false;
        if (submit.isConnected) {
          submit.disabled = false;
          submit.textContent = t('auth.verify');
        }
      }
    };
    setTimeout(() => code.focus(), 40);
  };

  const finishLogin = async (res) => {
    state.csrf = res.csrf_token || '';
    setCSRF(state.csrf);
    const me = await api.get('/auth/me');
    await enterApp(me);
  };

  const side = h('div', { class: 'login-side' },
    h('div', { class: 'login-brand' },
      h('div', { class: 'brand-mark' }, 'G'), h('span', {}, 'GoPanel')),
    h('div', { class: 'login-hero' },
      h('h1', {}, t('login.heroTitle')),
      h('p', {}, t('login.heroText')),
      h('div', { class: 'login-points' },
        [['globe', t('login.point1')], ['shieldCheck', t('login.point2')], ['zap', t('login.point3')], ['terminal', t('login.point4')]]
          .map(([ic, text]) => h('div', { class: 'login-point' },
            h('span', { class: 'tick' }, iconSpan(ic, 12)), h('span', {}, text))))),
    h('div', { class: 'login-side-foot' }, `GoPanel ${state.version} · Ubuntu / Debian`));

  const themeBtn = h('button', {
    class: 'icon-btn theme-toggle', type: 'button', title: t('settings.theme'),
    onClick: () => {
      const next = resolveTheme(state.theme) === 'dark' ? 'light' : 'dark';
      applyTheme(next);
      updateThemeIcon(themeBtn);
    },
  });
  updateThemeIcon(themeBtn);

  const panel = h('div', { class: 'login-panel' }, themeBtn,
    h('div', { class: 'login-card' },
      h('div', { class: 'login-logo' }, 'G'),
      h('div', { class: 'login-title' }, t('login.title')),
      h('div', { class: 'login-sub' }, t('login.subtitle')),
      errorBox,
      form,
      h('div', { class: 'login-foot' }, t('login.footer'))));

  const page = h('div', { class: 'login-page' }, side, panel);
  renderPassword();
  return page;
}

/* language preference from settings changes needs a reload */
export function switchLanguage(lang) {
  setLocale(lang);
  location.reload();
}

window.addEventListener('DOMContentLoaded', boot);
