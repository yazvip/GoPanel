import { api } from '../api.js';
import { t } from '../i18n.js';
import {
  h, pageHeader, card, badge, dataTable, emptyState, button, iconButton,
  openModal, confirmDialog, input, loadingBlock, toast, toastError, iconSpan,
  formatBytes, formatDuration, percent, statCard,
} from '../ui.js';
import { canWrite } from '../main.js';

export async function render(ctx) {
  const { outlet } = ctx;
  const page = h('div', { class: 'page' });
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
    class: 'input search-input', type: 'search', placeholder: t('services.searchProcess'),
    onInput: (ev) => { filter = ev.target.value; paintProcesses(); },
  });
  const procBody = h('div', {});
  const procCard = card({ flush: true, actions: searchInput, body: procBody });

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
      content.replaceChildren(card({ body: h('div', { class: 'alert alert-danger' }, err.message) }));
    }
  }

  function paint() {
    if (tab === 'processes') return paintProcesses();
    if (tab === 'host') return paintHost();
    paintServices();
  }

  function paintServices() {
    if (!services.length) {
      content.replaceChildren(card({ body: emptyState(t('services.noServices'), '', null, 'server') }));
      return;
    }
    content.replaceChildren(card({
      flush: true,
      body: dataTable([
        { label: t('services.name'), render: (svc) => h('div', {},
          h('div', { class: 'row gap-sm' }, h('b', { class: 'mono' }, svc.name),
            badge(svc.active || 'unknown', svc.active === 'active' ? 'green' : svc.active === 'failed' ? 'red' : 'gray')),
          h('div', { class: 'muted small ellipsis' }, svc.description || '')) },
        { label: t('services.load'), render: (svc) => h('span', { class: 'mono muted small' }, svc.load || '—') },
        { label: t('services.active'), render: (svc) => h('span', { class: 'muted small mono' }, svc.sub || '—') },
        { label: t('services.actions'), className: 'col-actions', render: (svc) => canWrite()
          ? h('div', { class: 'row-actions' },
            svc.active === 'active'
              ? iconButton('restart', t('services.restart'), () => action(svc, 'restart'))
              : iconButton('play', t('services.start'), () => action(svc, 'start')),
            svc.active === 'active'
              ? iconButton('pause', t('services.stop'), () => action(svc, 'stop'))
              : null)
          : null },
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
    procBody.replaceChildren(list.length ? dataTable([
      { label: t('services.pid'), className: 'w-80', render: (p) => h('span', { class: 'mono' }, p.pid) },
      { label: t('services.name'), render: (p) => h('div', {},
        h('b', {}, p.name),
        h('div', { class: 'muted small ellipsis', style: { maxWidth: '420px' } }, p.command || '')) },
      { label: t('services.user'), render: (p) => h('span', { class: 'muted small' }, p.user || '—') },
      { label: t('services.cpu'), className: 'w-80', render: (p) => `${(Number(p.cpu) || 0).toFixed(1)}%` },
      { label: t('services.mem'), className: 'w-80', render: (p) => formatBytes(p.mem) },
      { label: t('common.actions'), className: 'col-actions', render: (p) => canWrite()
        ? h('div', { class: 'row-actions' },
          iconButton('xCircle', t('services.kill'), () => killProcess(p), 'danger'))
        : null },
    ], list) : emptyState(t('services.noProcesses'), '', null, 'activity'));
    if (content.firstChild !== procCard) content.replaceChildren(procCard);
  }

  function paintHost() {
    if (!host) {
      content.replaceChildren(card({ body: emptyState(t('services.noServices'), '', null, 'server') }));
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
            h('dt', {}, t('services.arch')), h('dd', {}, host.arch || '—'),
            h('dt', {}, t('services.localIp')), h('dd', { class: 'mono' }, host.local_ip || '—'),
            h('dt', {}, t('services.publicIp')), h('dd', { class: 'mono' }, host.public_ip || '—')),
        }),
        card({
          title: t('services.mounts'),
          icon: 'hardDrive',
          flush: true,
          body: mounts.length ? dataTable([
            { label: t('services.mountpoint'), render: (m) => h('div', {},
              h('b', { class: 'mono small' }, m.mountpoint),
              h('div', { class: 'muted small' }, m.device || '')) },
            { label: t('services.filesystem'), render: (m) => h('span', { class: 'muted small' }, m.fstype || '—') },
            { label: t('services.used'), render: (m) => h('div', {},
              h('div', { class: 'small' }, `${formatBytes(m.used)} / ${formatBytes(m.total)}`),
              h('div', { class: 'progress-inline' }, h('span', { style: { width: `${percent(m.used, m.total)}%` } }))) },
          ], mounts) : emptyState(t('common.empty'), '', null, 'hardDrive'),
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
