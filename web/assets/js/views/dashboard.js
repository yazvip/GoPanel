import { api } from '../api.js';
import { t } from '../i18n.js';
import {
  h, pageHeader, card, statCard, statusBadge, dataTable, emptyState,
  formatBytes, formatDate, relativeTime, formatDuration, percent, iconSpan,
  loadingBlock, toastError, button,
} from '../ui.js';
import { canWrite } from '../main.js';
import { navigate } from '../router.js';

/* The task queue and the dashboard name jobs the same way. */
function typeLabel(type) {
  const key = `jobs.type.${String(type || '').replace(/[.-]/g, '_')}`;
  const label = t(key);
  return label === key ? (type || '—') : label;
}

function progressTone(job) {
  if (job.status === 'running') return '';
  if (job.status === 'failed') return 'failed';
  return 'done';
}

export async function render(ctx) {
  const { outlet, setCleanup } = ctx;
  const root = h('div', { class: 'page ops-page ops-dashboard' });
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
          card({ body: h('div', { class: 'alert alert-danger' }, iconSpan('alert', 16),
            h('div', {}, h('b', {}, t('common.loadFailed')), h('div', { class: 'small' }, String(err.message || err)))) }),
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

    const stats = h('div', { class: 'grid stats-grid ops-stats' },
      statCard({
        label: t('dashboard.cpu'), value: `${cpuPct.toFixed(1)}%`,
        meta: t('dashboard.cores', { n: host.cpus || '—' }), icon: 'cpu', tone: 'blue', meter: cpuPct,
      }),
      statCard({
        label: t('dashboard.memory'),
        value: formatBytes(live.mem_used || 0),
        meta: t('ops.dash.usage', { used: formatBytes(live.mem_used || 0), total: formatBytes(live.mem_total || 0) }),
        icon: 'memory', tone: 'violet', meter: memPct,
      }),
      statCard({
        label: t('dashboard.disk'), value: formatBytes(live.disk_used || 0),
        meta: t('ops.dash.usage', { used: formatBytes(live.disk_used || 0), total: formatBytes(live.disk_total || 0) }),
        icon: 'disk', tone: 'amber', meter: diskPct,
      }),
      statCard({
        label: t('dashboard.sites'), value: counts.sites ?? 0,
        meta: t('ops.dash.domains', { n: counts.domains ?? 0 }),
        icon: 'globe', tone: 'green',
      }),
    );

    const netRx = Number(live.net_rx_rate || 0);
    const netTx = Number(live.net_tx_rate || 0);

    const hostCard = card({
      title: t('dashboard.systemInfo'), icon: 'server',
      body: h('dl', { class: 'kv' },
        h('dt', {}, t('dashboard.hostname')), h('dd', {}, host.hostname || '—'),
        h('dt', {}, t('dashboard.os')), h('dd', {}, host.os || host.platform || '—'),
        h('dt', {}, t('dashboard.kernel')), h('dd', {}, host.kernel || '—'),
        h('dt', {}, t('dashboard.uptime')), h('dd', {}, host.uptime ? formatDuration(host.uptime) : '—'),
        h('dt', {}, t('dashboard.load')), h('dd', { class: 'mono' }, (Number(live.load1) || 0).toFixed(2)),
        h('dt', {}, t('dashboard.localIp')), h('dd', { class: 'mono' }, host.local_ip || '—'),
        h('dt', {}, t('dashboard.publicIp')), h('dd', { class: 'mono' }, host.public_ip || '—'),
        h('dt', {}, t('dashboard.network')),
        h('dd', {},
          h('span', { class: 'muted' }, `${t('dashboard.rx')} `),
          h('span', { class: 'mono' }, `${formatBytes(netRx)}/s`),
          h('span', { class: 'muted' }, ' · '),
          h('span', { class: 'muted' }, `${t('dashboard.tx')} `),
          h('span', { class: 'mono' }, `${formatBytes(netTx)}/s`)),
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
          statusBadge(svc.active || svc.sub))))
        : emptyState(t('ops.dash.noServices'), t('ops.dash.noServicesHint'), null, 'activity'),
    });

    const jobsCard = card({
      title: t('dashboard.recentJobs'), icon: 'zap', flush: true,
      actions: h('button', { class: 'link-btn', type: 'button', onClick: () => navigate('/jobs') }, t('dashboard.viewAll')),
      body: jobs.length
        ? dataTable([
          { label: t('jobs.type'), className: 'ops-id', render: (job) => h('div', {},
            h('div', { style: { fontWeight: '600' } }, typeLabel(job.type)),
            h('div', { class: 'muted small' }, relativeTime(job.created_at))) },
          { label: t('jobs.status'), render: (job) => statusBadge(job.status) },
          { label: t('jobs.progress'), className: 'ops-progress', render: (job) => h('div', { class: 'ops-progress-cell' },
            h('div', { class: 'progress-inline' }, h('span', {
              class: progressTone(job),
              style: { width: `${Math.max(0, Math.min(100, job.progress || 0))}%` },
            })),
            h('span', { class: 'ops-pct' }, `${Math.max(0, Math.min(100, job.progress || 0))}%`)) },
        ], jobs.slice(0, 6))
        : emptyState(t('jobs.noJobs'), t('jobs.noJobsHint'), null, 'zap'),
    });

    const backupsCard = card({
      title: t('dashboard.recentBackups'), icon: 'archive', flush: true,
      actions: h('button', { class: 'link-btn', type: 'button', onClick: () => navigate('/backups') }, t('dashboard.viewAll')),
      body: backups.length
        ? dataTable([
          { label: t('common.name'), className: 'ops-id', render: (b) => h('div', {},
            h('div', { style: { fontWeight: '600' } }, b.ref || b.id),
            h('div', { class: 'muted small' },
              h('span', {}, b.kind === 'database' ? t('backups.database') : t('backups.site')),
              h('span', {}, ' · '),
              h('span', { class: 'mono' }, formatBytes(b.size)))) },
          { label: t('backups.status'), render: (b) => statusBadge(b.status) },
          { label: t('common.created'), className: 'nowrap', render: (b) => h('span', { class: 'muted small', title: formatDate(b.created_at) },
            relativeTime(b.created_at)) },
        ], backups)
        : emptyState(t('backups.noBackups'), t('backups.noBackupsHint'), null, 'archive'),
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
      pageHeader(t('dashboard.title'), t('dashboard.subtitle'), [
        h('span', { class: 'live-pill' }, h('span', { class: 'dot green' }), t('dashboard.live')),
        button(t('common.refresh'), { icon: 'refresh', onClick: () => load(false) }),
      ]),
      stats,
      h('div', { class: 'grid cols-2 ops-equal' }, hostCard, servicesCard),
      h('div', { class: 'grid grid-main ops-equal' }, jobsCard, h('div', { class: 'stack' }, backupsCard, quick)),
    );
  }

  function quickAction(iconName, label, path) {
    return h('button', {
      class: 'quick-action', type: 'button', disabled: !path,
      onClick: path ? () => navigate(path) : undefined,
    }, h('span', { class: 'quick-icon' }, iconSpan(iconName, 19)), h('span', {}, label));
  }

  root.replaceChildren(pageHeader(t('dashboard.title'), t('dashboard.subtitle')),
    card({ body: loadingBlock() }));
  await load(true);
  timer = setInterval(() => load(false), 30000);
  return root;
}
