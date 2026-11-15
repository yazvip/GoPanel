import { api } from '../api.js';
import { t } from '../i18n.js';
import {
  h, pageHeader, card, badge, dataTable, emptyState, button, iconButton,
  confirmDialog, input, loadingBlock, toast, toastError,
  formatBytes, formatDuration, percent, statCard,
} from '../ui.js';
import { canWrite } from '../main.js';

/* Loading, empty and error all read as the same centred block. */
function stateBlock(title, text, action, iconName, tone) {
  const el = emptyState(title, text, action, iconName);
  if (tone) el.classList.add(`adm-state-${tone}`);
  return el;
}

/* systemd speaks in its own vocabulary; the panel says it in the operator's. */
function stateLabel(state) {
  const value = String(state || '').toLowerCase();
  if (value === 'active') return t('status.active');
  if (value === 'inactive') return t('status.inactive');
  if (value === 'failed') return t('status.failed');
  if (value === 'running') return t('status.running');
  if (value === 'exited') return t('services.stateExited');
  if (value === 'dead') return t('services.stateDead');
  return t('common.unknown');
}

function stateTone(state) {
  const value = String(state || '').toLowerCase();
  if (value === 'active' || value === 'running') return 'green';
  if (value === 'failed' || value === 'dead') return 'red';
  return 'gray';
}

function loadLabel(state) {
  const value = String(state || '').toLowerCase();
  if (value === 'loaded') return t('services.stateLoaded');
  if (value === 'not-found') return t('services.stateNotFound');
  return state || '—';
}

export async function render(ctx) {
  const { outlet } = ctx;
  const page = h('div', { class: 'page admin-page' });
  outlet.replaceChildren(page);
  let tab = 'services';
  let services = [];
  let processes = [];
  let host = null;
  let live = {};
  let filter = '';
  const content = h('div', {});
  page.append(
    pageHeader(t('services.title'), t('services.subtitle'),
      button(t('common.refresh'), { icon: 'refresh', onClick: load })),
    h('div', { class: 'tabs' },
      h('button', { class: 'tab active', type: 'button', dataset: { tab: 'services' }, onClick: () => switchTab('services') }, t('services.services')),
      h('button', { class: 'tab', type: 'button', dataset: { tab: 'processes' }, onClick: () => switchTab('processes') }, t('services.processes')),
      h('button', { class: 'tab', type: 'button', dataset: { tab: 'host' }, onClick: () => switchTab('host') }, t('services.host'))),
    content,
  );

  // The process card (and its search input) is created once; repaints only
  // replace the table body so typing never loses focus.
  const searchInput = h('input', {
    class: 'input search-input',
    type: 'search',
    placeholder: t('services.searchProcess'),
    'aria-label': t('services.searchProcess'),
    onInput: (ev) => { filter = ev.target.value; paintProcesses(); },
  });
  const procCount = badge('0', 'gray');
  const procBody = h('div', {});
  const procCard = card({
    class: 'adm-t-processes',
    flush: true,
    title: t('services.processes'),
    actions: h('div', { class: 'row gap-sm' }, procCount, searchInput),
    body: procBody,
  });

  function switchTab(next) {
    tab = next;
    document.querySelectorAll('.tab').forEach((el) => el.classList.toggle('active', el.dataset.tab === next));
    paint();
  }

  async function load() {
    if (!services.length) content.replaceChildren(loadingBlock(t('services.loading')));
    try {
      const [svc, procs, hostInfo, dashboard] = await Promise.all([
        api.get('/services'),
        api.get('/processes').catch(() => []),
        api.get('/host').catch(() => null),
        api.get('/dashboard/summary').catch(() => null),
      ]);
      services = svc || [];
      processes = procs || [];
      host = hostInfo;
      live = dashboard && dashboard.overview ? (dashboard.overview.live || {}) : {};
      paint();
    } catch (err) {
      content.replaceChildren(card({
        body: stateBlock(t('services.loadFailed'), err.message,
          button(t('common.retry'), { icon: 'refresh', onClick: load }), 'alert', 'error'),
      }));
    }
  }

  function paint() {
    if (tab === 'processes') return paintProcesses();
    if (tab === 'host') return paintHost();
    paintServices();
  }

  function paintServices() {
    if (!services.length) {
      content.replaceChildren(card({
        body: stateBlock(t('services.noServices'), t('services.noServicesHint'), null, 'server'),
      }));
      return;
    }
    content.replaceChildren(card({
      class: 'adm-t-services',
      flush: true,
      body: dataTable([
        {
          label: t('services.name'),
          className: 'adm-col-name',
          render: (svc) => h('div', {},
            h('div', { class: 'cell-name' }, h('b', { class: 'mono' }, svc.name)),
            h('span', { class: 'cell-sub' }, svc.description || '')),
        },
        {
          label: t('services.state'),
          className: 'adm-col-state',
          render: (svc) => badge(stateLabel(svc.active), stateTone(svc.active)),
        },
        {
          label: t('services.loadState'),
          className: 'adm-col-load',
          render: (svc) => h('span', { class: 'muted small mono' }, loadLabel(svc.load)),
        },
        {
          label: t('common.actions'),
          className: 'col-actions adm-col-actions',
          render: (svc) => (canWrite()
            ? h('div', { class: 'row-actions' },
              svc.active === 'active'
                ? iconButton('restart', t('services.restart'), () => action(svc, 'restart'))
                : iconButton('play', t('services.start'), () => action(svc, 'start')),
              svc.active === 'active'
                ? iconButton('pause', t('services.stop'), () => action(svc, 'stop'))
                : null)
            : null),
        },
      ], services),
    }));
  }

  function paintProcesses() {
    const list = processes.filter((proc) => {
      if (!filter) return true;
      const q = filter.toLowerCase();
      return String(proc.name || '').toLowerCase().includes(q)
        || String(proc.command || '').toLowerCase().includes(q)
        || String(proc.user || '').toLowerCase().includes(q);
    });
    searchInput.value = filter;
    procCount.textContent = String(list.length);
    procBody.replaceChildren(list.length ? dataTable([
      {
        label: t('services.pid'),
        className: 'col-num adm-col-pid',
        render: (p) => h('span', { class: 'mono' }, p.pid),
      },
      {
        label: t('services.name'),
        className: 'adm-col-name',
        render: (p) => h('div', {},
          h('div', { class: 'cell-name' }, h('b', {}, p.name)),
          h('span', { class: 'cell-sub mono' }, p.command || '')),
      },
      {
        label: t('services.user'),
        className: 'adm-col-user',
        render: (p) => h('span', { class: 'muted small' }, p.user || '—'),
      },
      {
        label: t('services.cpu'),
        className: 'col-num adm-col-cpu',
        render: (p) => `${(Number(p.cpu) || 0).toFixed(1)}%`,
      },
      {
        label: t('services.mem'),
        className: 'col-num adm-col-mem',
        render: (p) => formatBytes(p.mem),
      },
      {
        label: t('common.actions'),
        className: 'col-actions adm-col-actions',
        render: (p) => (canWrite()
          ? h('div', { class: 'row-actions' },
            iconButton('xCircle', t('services.kill'), () => killProcess(p), 'danger'))
          : null),
      },
    ], list) : stateBlock(t('services.noProcesses'), t('services.noProcessesHint'), null, 'activity'));
    if (content.firstChild !== procCard) content.replaceChildren(procCard);
  }

  function paintHost() {
    if (!host) {
      content.replaceChildren(card({
        body: stateBlock(t('services.noServices'), t('services.noServicesHint'), null, 'server'),
      }));
      return;
    }
    const memTotal = live.mem_total || host.mem_total || 0;
    const memUsed = live.mem_used || 0;
    const diskTotal = live.disk_total || host.disk_total || 0;
    const diskUsed = live.disk_used || 0;
    const memPct = percent(memUsed, memTotal);
    const diskPct = percent(diskUsed, diskTotal);
    const mounts = host.mounts || [];
    content.replaceChildren(
      h('div', { class: 'grid stats-grid' },
        statCard({ label: t('services.cpus'), value: host.cpus ?? '—', meta: host.arch || '', icon: 'cpu', tone: 'blue' }),
        statCard({ label: t('services.memory'), value: formatBytes(memTotal), meta: memUsed ? `${formatBytes(memUsed)} / ${formatBytes(memTotal)}` : formatBytes(memTotal), icon: 'memory', tone: 'violet', meter: memPct }),
        statCard({ label: t('services.disk'), value: formatBytes(diskTotal), meta: diskUsed ? `${formatBytes(diskUsed)} / ${formatBytes(diskTotal)}` : formatBytes(diskTotal), icon: 'disk', tone: 'amber', meter: diskPct }),
        statCard({ label: t('services.uptime'), value: formatDuration(host.uptime || 0), meta: host.platform || '', icon: 'clock', tone: 'green' })),
      h('div', { class: 'grid cols-2 mt3' },
        card({
          title: t('services.host'),
          icon: 'server',
          body: h('dl', { class: 'kv' },
            h('dt', {}, t('services.hostname')), h('dd', { class: 'mono' }, host.hostname || '—'),
            h('dt', {}, t('services.os')), h('dd', {}, host.os || '—'),
            h('dt', {}, t('services.kernel')), h('dd', { class: 'mono small' }, host.kernel || '—'),
            h('dt', {}, t('services.arch')), h('dd', { class: 'mono' }, host.arch || '—'),
            h('dt', {}, t('services.localIp')), h('dd', { class: 'mono' }, host.local_ip || '—'),
            h('dt', {}, t('services.publicIp')), h('dd', { class: 'mono' }, host.public_ip || '—')),
        }),
        card({
          class: 'adm-t-mounts',
          title: t('services.mounts'),
          icon: 'hardDrive',
          actions: badge(String(mounts.length), 'gray'),
          flush: true,
          body: mounts.length ? dataTable([
            {
              label: t('services.mountpoint'),
              className: 'adm-col-mount',
              render: (m) => h('div', {},
                h('div', { class: 'cell-name' }, h('b', { class: 'mono small' }, m.mountpoint)),
                h('span', { class: 'cell-sub mono' }, m.device || '')),
            },
            {
              label: t('services.filesystem'),
              className: 'adm-col-fs',
              render: (m) => h('span', { class: 'muted small' }, m.fstype || '—'),
            },
            {
              label: t('services.used'),
              className: 'adm-col-used',
              render: (m) => h('div', {},
                h('div', { class: 'small mono' }, `${formatBytes(m.used)} / ${formatBytes(m.total)}`),
                h('div', { class: 'progress-inline' }, h('span', { style: { width: `${percent(m.used, m.total)}%` } }))),
            },
          ], mounts) : stateBlock(t('services.noMounts'), '', null, 'hardDrive'),
        })),
    );
  }

  async function action(svc, name) {
    try {
      services = (await api.post(`/services/${encodeURIComponent(svc.name)}/${name}`, {})) || services;
      toast(name === 'start' ? t('services.serviceStarted') : name === 'stop' ? t('services.serviceStopped') : t('services.serviceRestarted'), 'success');
      paintServices();
    } catch (err) {
      toastError(err);
    }
  }

  async function killProcess(proc) {
    const ok = await confirmDialog({
      title: t('services.killTitle', { pid: proc.pid }),
      message: t('services.killConfirm'),
      note: proc.command || proc.name,
      confirmText: t('services.kill'),
      danger: true,
    });
    if (!ok) return;
    try {
      await api.post(`/processes/${proc.pid}/kill`, {});
      toast(t('services.processKilled'), 'success');
      processes = processes.filter((p) => p.pid !== proc.pid);
      paintProcesses();
    } catch (err) {
      toastError(err);
    }
  }

  await load();
  return page;
}
