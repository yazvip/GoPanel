import { api } from '../api.js';
import { t } from '../i18n.js';
import {
  h, pageHeader, card, badge, emptyState, button, openModal, confirmDialog,
  input, loadingBlock, toast, toastError, iconSpan,
} from '../ui.js';
import { canWrite } from '../main.js';
import { navigate } from '../router.js';
import { watchJob, jobProgressModal } from '../jobwatch.js';

const CATEGORY_KEY = {
  web: 'apps.web',
  database: 'apps.database',
  cache: 'apps.cache',
  php: 'apps.php',
  node: 'apps.node',
  python: 'apps.python',
  runtime: 'apps.runtime',
  mail: 'apps.mail',
  security: 'apps.security',
  monitoring: 'apps.monitoring',
  tool: 'apps.tools',
  tools: 'apps.tools',
};

const CATEGORY_ICON = {
  web: 'globe',
  database: 'database',
  cache: 'zap',
  runtime: 'code',
  security: 'shield',
  tool: 'packageOpen',
  monitoring: 'activity',
  mail: 'mail',
};

function categoryLabel(category) {
  const key = CATEGORY_KEY[category];
  return key ? t(key) : (category || t('apps.other'));
}

export async function render(ctx) {
  const { outlet } = ctx;
  const page = h('div', { class: 'page' });
  outlet.replaceChildren(page);
  let items = [];
  let category = 'all';
  let query = '';
  const installing = new Map(); // appId -> { jobId, progress, message, bar?, label? }
  const watched = new Set();
  const grid = h('div', {});
  const chips = h('div', { class: 'chips' });
  const search = input({ type: 'search', placeholder: t('apps.searchPlaceholder'), class: 'input search-input' });

  page.append(
    pageHeader(t('apps.title'), t('apps.subtitle'),
      h('div', { class: 'page-actions' },
        search,
        canWrite() ? button(t('apps.updateCache'), { icon: 'refresh', onClick: updateCache, title: t('apps.updateHint') }) : null,
        button(t('common.refresh'), { icon: 'rotate', onClick: load }))),
    chips,
    grid,
  );

  function categoryCount(cat) {
    return cat === 'all' ? items.length : items.filter((item) => item.category === cat).length;
  }

  function paintChips() {
    const cats = ['all', ...Array.from(new Set(items.map((item) => item.category).filter(Boolean))).sort()];
    chips.replaceChildren(...cats.map((cat) => h('button', {
      type: 'button',
      class: `chip-btn ${category === cat ? 'active' : ''}`,
      onClick: () => { category = cat; paintChips(); paintGrid(); },
    }, cat === 'all' ? t('apps.all') : categoryLabel(cat), h('span', { class: 'chip-count' }, String(categoryCount(cat))))));
  }

  async function load() {
    grid.replaceChildren(loadingBlock(t('apps.loading')));
    try {
      let active = [];
      try {
        active = (await api.get('/jobs?type=app-install&limit=50')) || [];
      } catch { active = []; }
      installing.clear();
      for (const job of active) {
        if (job.status !== 'queued' && job.status !== 'running') continue;
        let appIds = [];
        try { appIds = JSON.parse(job.payload || '{}').apps || []; } catch { appIds = []; }
        for (const appId of appIds) {
          installing.set(appId, { jobId: job.id, progress: job.progress || 0, message: job.message || '' });
        }
      }
      items = (await api.get('/apps/catalog')) || [];
      paintChips();
      paintGrid();
      for (const entry of installing.values()) watchInstall(entry.jobId);
    } catch (err) {
      grid.replaceChildren(card({ body: h('div', { class: 'alert alert-danger' }, err.message) }));
    }
  }

  function visibleItems() {
    const q = query.toLowerCase();
    return items.filter((item) => (category === 'all' || item.category === category)
      && (!q || item.name.toLowerCase().includes(q) || item.id.toLowerCase().includes(q)
        || (item.description || '').toLowerCase().includes(q)));
  }

  function paintGrid() {
    const list = visibleItems();
    if (!list.length) {
      grid.replaceChildren(card({ body: emptyState(t('apps.noApps'), t('apps.noAppsHint'), null, 'box') }));
      return;
    }
    grid.replaceChildren(h('div', { class: 'apps-grid' }, list.map(appCard)));
  }

  function appName(id) {
    const found = items.find((item) => item.id === id);
    return found ? found.name : id;
  }

  function appCard(app) {
    const installed = app.installed;
    const entry = installing.get(app.id) || null;
    let bar = null;
    let label = null;
    let action;
    if (entry) {
      bar = h('span', { style: { width: `${entry.progress}%` } });
      label = h('div', { class: 'muted small' }, entry.message || t('apps.installing'));
      action = h('div', { class: 'app-installing' },
        h('div', { class: 'progress-inline' }, bar), label);
    } else if (installed) {
      action = h('span', { class: 'installed-pill' }, iconSpan('checkCircle', 14), t('apps.installed'));
    } else {
      action = button(t('apps.install'), { size: 'sm', icon: 'download', onClick: () => confirmInstall(app) });
    }
    const cardEl = h('div', { class: `app-card cat-${app.category || 'other'} ${installed ? 'installed' : ''} ${entry ? 'busy' : ''}` },
      h('div', { class: 'app-head' },
        h('span', { class: 'app-icon' }, iconSpan(CATEGORY_ICON[app.category] || 'box', 20)),
        h('div', { class: 'grow' },
          h('div', { class: 'app-name' }, app.name, app.popular ? badge(t('apps.popular'), 'amber') : null),
          h('div', { class: 'muted small' }, categoryLabel(app.category))),
        installed ? badge(app.version || t('apps.installed'), 'green') : null),
      h('p', { class: 'app-desc' }, app.description || ''),
      app.warning ? h('div', { class: 'alert alert-warn small' }, iconSpan('alert', 14), h('div', {}, app.warning)) : null,
      h('div', { class: 'app-meta' },
        h('span', { class: 'muted small' }, `${t('apps.packages')}: ${(app.packages || []).join(', ')}`),
        app.service ? h('span', { class: 'muted small' }, `${t('apps.service')}: ${app.service}`) : null,
        (app.conflicts || []).length
          ? h('span', { class: 'muted small app-conflict' }, iconSpan('alert', 12), `${t('apps.conflicts')}: ${app.conflicts.map(appName).join(', ')}`)
          : null),
      canWrite() ? h('div', { class: 'app-actions' }, action) : null,
    );
    if (entry) {
      entry.bar = bar;
      entry.label = label;
    }
    return cardEl;
  }

  let searchTimer = null;
  search.addEventListener('input', () => {
    if (searchTimer) clearTimeout(searchTimer);
    searchTimer = setTimeout(() => { query = search.value.trim(); paintGrid(); }, 200);
  });

  async function confirmInstall(app) {
    const ok = await confirmDialog({
      title: t('apps.installTitle'),
      message: t('apps.installConfirm', { name: app.name }),
      note: (app.packages || []).join(' '),
      confirmText: t('apps.install'),
    });
    if (!ok) return;
    try {
      const job = await api.post('/apps/install', { apps: [app.id] });
      toast(t('apps.installStarted'), 'success');
      if (job && job.id) {
        installing.set(app.id, { jobId: job.id, progress: 0, message: '' });
        paintGrid();
        watchInstall(job.id);
      }
    } catch (err) {
      toastError(err);
    }
  }

  function updateEntries(jobId, data) {
    for (const entry of installing.values()) {
      if (entry.jobId !== jobId) continue;
      if (data.progress !== undefined && data.progress !== null) entry.progress = Number(data.progress) || 0;
      if (data.message) entry.message = data.message;
      if (entry.bar) entry.bar.style.width = `${entry.progress}%`;
      if (entry.label) entry.label.textContent = entry.message || `${entry.progress}%`;
    }
  }

  function watchInstall(jobId) {
    if (!jobId || watched.has(jobId)) return;
    watched.add(jobId);
    watchJob(jobId, {
      onSnapshot: (job) => {
        if (job.status === 'queued' || job.status === 'running') updateEntries(jobId, job);
      },
      onProgress: (ev) => updateEntries(jobId, ev),
      onDone: async (job) => {
        watched.delete(jobId);
        for (const [appId, entry] of [...installing]) {
          if (entry.jobId === jobId) installing.delete(appId);
        }
        if (job.status === 'failed') toastError(new Error(job.error || t('status.failed')));
        else toast(t('apps.installDone'), 'success');
        await load();
      },
      onError: async () => {
        watched.delete(jobId);
        await load();
      },
    });
  }

  async function updateCache() {
    try {
      const job = await api.post('/apps/update-cache', {});
      toast(t('apps.cacheUpdated'), 'success');
      if (job && job.id) trackJob(job.id, t('apps.cacheJob'));
    } catch (err) {
      toastError(err);
    }
  }

  function trackJob(jobId, title) {
    const progress = jobProgressModal(title);
    const modal = openModal({
      title,
      size: 'modal-sm',
      body: progress.element,
      footer: [
        button(t('nav.jobs'), { icon: 'zap', onClick: () => { modal.close(); navigate('/jobs'); } }),
        button(t('common.close'), { onClick: () => modal.close() }),
      ],
    });
    watchJob(jobId, {
      onSnapshot: (job) => progress.setProgress(job.progress, job.message),
      onProgress: (ev) => progress.setProgress(ev.progress, ev.message),
      onLog: (line) => progress.appendLog(line),
      onDone: async (job) => {
        progress.setProgress(100, job.status === 'failed' ? (job.error || t('status.failed')) : t('common.done'));
        await load();
      },
      onError: (ev) => progress.setError(ev.error || 'disconnected'),
    });
  }

  await load();
  return page;
}
