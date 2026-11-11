import { api } from '../api.js';
import { t } from '../i18n.js';
import {
  h, pageHeader, card, statCard, badge, statusBadge, dataTable, emptyState,
  formatBytes, formatDate, relativeTime, formatDuration, percent, iconSpan,
  loadingBlock, toastError, button,
} from '../ui.js';
import { getState, canWrite } from '../main.js';
import { navigate } from '../router.js';

function sampleValue(overview, key) {
  if (!overview || !overview.live) return undefined;
  return overview.live[key];
}

function serviceTone(svc) {
  return svc.active === 'active' ? 'green' : svc.active === 'failed' ? 'red' : 'gray';
}

export async function render(ctx) {
  const { outlet, setCleanup } = ctx;
  const root = h('div', { class: 'page' });
  outlet.replaceChildren(root);
  let timer = null;
  let disposed = false;
  setCleanup(() => {
    disposed = true;
    if (timer) clearInterval(timer);
  });

  async function load(first) {
    if (disposed) return;
    let data;
    try {
      data = await api.get('/dashboard/summary');
    } catch (err) {
      if (first) {
        root.replaceChildren(
          pageHeader(t('dashboard.title'), t('dashboard.subtitle')),
          card({ body: h('div', { class: 'alert alert-danger' }, iconSpan('alert', 16), h('div', {}, err.message)) }),
        );
      } else {
        toastError(err);
      }
      return;
    }
    if (disposed) return;
    paint(data);
  }

  function paint(data) {
    const overview = data.overview || {};
    const live = overview.live || {};
    const host = overview.host || {};
    const counts = data.counts || {};
    const services = data.services || [];
    const jobs = data.jobs || [];
    const backups = data.backups || [];

    const memPct = percent(live.mem_used, live.mem_total);
    const diskPct = percent(live.disk_used, live.disk_total);
    const cpuPct = Math.round((Number(live.cpu) || 0) * 10) / 10;

    const stats = h('div', { class: 'grid stats-grid' },
      statCard({
        label: t('dashboard.cpu'), value: `${cpuPct.toFixed(1)}%`,
        meta: t('dashboard.cores', { n: host.cpus || '—' }), icon: 'cpu', tone: 'blue', meter: cpuPct,
      }),
      statCard({
        label: t('dashboard.memory'),
        value: formatBytes(live.mem_used || 0),
        meta: t('dashboard.memoryUsage', { used: formatBytes(live.mem_used || 0), total: formatBytes(live.mem_total || 0) }),
        icon: 'memory', tone: 'violet', meter: memPct,
      }),
      statCard({
        label: t('dashboard.disk'), value: formatBytes(live.disk_used || 0),
        meta: `${formatBytes(live.disk_used || 0)} / ${formatBytes(live.disk_total || 0)}`,
        icon: 'disk', tone: 'amber', meter: diskPct,
      }),
      statCard({
        label: t('dashboard.sites'), value: counts.sites ?? 0,
        meta: `${counts.domains ?? 0} ${t('dashboard.domains').toLowerCase()}`,
        icon: 'globe', tone: 'green',
      }),
    );

    const netRx = Number(live.net_rx_rate || 0);
    const netTx = Number(live.net_tx_rate || 0);

    const hostCard = card({
      title: t('dashboard.systemInfo'), icon: 'server',
      body: h('dl', { class: 'kv' },
        h('dt', {}, t('dashboard.hostname')), h('dd', {}, host.hostname || '—'),
        h('dt', {}, t('dashboard.os')), h('dd', {}, [host.os, host.platform].filter(Boolean).join(' ') || '—'),
        h('dt', {}, t('dashboard.kernel')), h('dd', {}, host.kernel || '—'),
        h('dt', {}, t('dashboard.uptime')), h('dd', {}, host.uptime ? formatDuration(host.uptime) : '—'),
        h('dt', {}, t('dashboard.load')), h('dd', {}, (Number(live.load1) || 0).toFixed(2)),
        h('dt', {}, t('dashboard.localIp')), h('dd', { class: 'mono' }, host.local_ip || '—'),
        h('dt', {}, t('dashboard.publicIp')), h('dd', { class: 'mono' }, host.public_ip || '—'),
        h('dt', {}, t('dashboard.network')),
        h('dd', {}, `${t('dashboard.rx')} ${formatBytes(netRx)}/s · ${t('dashboard.tx')} ${formatBytes(netTx)}/s`),
      ),
      foot: h('div', { class: 'muted small' }, `${t('dashboard.refreshHint')} · ${formatDate(Math.floor(Date.now() / 1000))}`),
    });

    const serviceRows = services.slice(0, 6);
    const servicesCard = card({
      title: t('dashboard.services'), icon: 'activity',
      actions: h('button', { class: 'link-btn', type: 'button', onClick: () => navigate('/services') }, t('dashboard.viewAll')),
      body: serviceRows.length
        ? h('div', { class: 'service-list' }, serviceRows.map((svc) => h('div', { class: 'service-row' },
          h('span', { class: `dot ${svc.active === 'active' ? 'green' : svc.active === 'failed' ? 'red' : 'gray'}` }),
          h('div', { class: 'grow' },
            h('div', { class: 'service-name' }, svc.name),
            h('div', { class: 'muted small ellipsis' }, svc.description || '')),
          badge(svc.active || svc.sub || 'unknown', serviceTone(svc)))))
        : emptyState(t('common.empty'), ''),
    });

    const jobsCard = card({
      title: t('dashboard.recentJobs'), icon: 'zap', flush: true,
      actions: h('button', { class: 'link-btn', type: 'button', onClick: () => navigate('/jobs') }, t('dashboard.viewAll')),
      body: jobs.length
        ? dataTable([
          { label: t('jobs.type'), render: (job) => h('div', {},
            h('div', { style: { fontWeight: '600' } }, job.type),
            h('div', { class: 'muted small' }, relativeTime(job.created_at))) },
          { label: t('jobs.status'), render: (job) => statusBadge(job.status) },
          { label: t('jobs.progress'), className: 'w-120', render: (job) => h('div', { class: 'progress-inline' },
            h('span', { style: { width: `${Math.max(0, Math.min(100, job.progress || 0))}%` } })) },
        ], jobs.slice(0, 6))
        : emptyState(t('dashboard.noJobs'), ''),
    });

    const backupsCard = card({
      title: t('dashboard.recentBackups'), icon: 'archive', flush: true,
      actions: h('button', { class: 'link-btn', type: 'button', onClick: () => navigate('/backups') }, t('dashboard.viewAll')),
      body: backups.length
        ? dataTable([
          { label: t('common.name'), render: (b) => h('div', {},
            h('div', { style: { fontWeight: '600' } }, b.ref || b.id),
            h('div', { class: 'muted small' }, `${b.kind} · ${formatBytes(b.size)}`)) },
          { label: t('backups.status'), render: (b) => statusBadge(b.status) },
          { label: t('common.created'), render: (b) => h('span', { class: 'muted small' }, formatDate(b.created_at)) },
        ], backups)
        : emptyState(t('dashboard.noBackups'), ''),
    });

    const quick = card({
      title: t('dashboard.quickActions'), icon: 'zap',
      body: h('div', { class: 'quick-actions' },
        quickAction('globe', t('dashboard.addSite'), canWrite() ? '/sites' : null),
        quickAction('archive', t('dashboard.createBackup'), canWrite() ? '/backups' : null),
        quickAction('folder', t('dashboard.uploadFiles'), '/files'),
        quickAction('clock', t('dashboard.manageCron'), canWrite() ? '/cron' : null),
      ),
    });

    root.replaceChildren(
      pageHeader(t('dashboard.title'), t('dashboard.subtitle'), h('div', { class: 'page-actions' },
        h('span', { class: 'live-pill' }, h('span', { class: 'dot green' }), t('dashboard.live')),
        button(t('common.refresh'), { icon: 'refresh', onClick: () => load(false) }))),
      stats,
      h('div', { class: 'grid cols-2' }, hostCard, servicesCard),
      h('div', { class: 'grid cols-2' }, jobsCard, backupsCard),
      quick,
    );
  }

  function quickAction(iconName, label, path) {
    return h('button', {
      class: 'quick-action', type: 'button', disabled: !path,
      onClick: path ? () => navigate(path) : undefined,
    }, h('span', { class: 'quick-icon' }, iconSpan(iconName, 19)), h('span', {}, label));
  }

  root.replaceChildren(pageHeader(t('dashboard.title'), t('dashboard.subtitle')), loadingBlock());
  await load(true);
  timer = setInterval(() => load(false), 30000);
  return root;
}
