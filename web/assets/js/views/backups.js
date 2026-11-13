import { api } from '../api.js';
import { t } from '../i18n.js';
import {
  h, pageHeader, card, badge, statusBadge, dataTable, emptyState, button, iconButton,
  openModal, confirmDialog, field, input, select, switchControl, formRow, loadingBlock,
  toast, toastError, iconSpan, formatBytes, formatDate, relativeTime, copyText,
} from '../ui.js';
import { canWrite } from '../main.js';
import { navigate } from '../router.js';
import { watchJob, jobProgressModal } from '../jobwatch.js';

export async function render(ctx) {
  const { outlet } = ctx;
  const page = h('div', { class: 'page' });
  outlet.replaceChildren(page);
  let tab = 'backups';
  let backups = [];
  let schedules = [];
  let sites = [];
  let databases = [];
  let dbsAvailable = true;
  const content = h('div', {});

  page.append(
    pageHeader(t('backups.title'), t('backups.subtitle'),
      canWrite() ? button(t('backups.create'), { variant: 'primary', icon: 'archive', onClick: openCreate }) : null),
    h('div', { class: 'tabs' },
      h('button', { class: 'tab active', type: 'button', dataset: { tab: 'backups' }, onClick: () => switchTab('backups') }, t('backups.list')),
      h('button', { class: 'tab', type: 'button', dataset: { tab: 'schedules' }, onClick: () => switchTab('schedules') }, t('backups.schedules'))),
    content,
  );

  function switchTab(next) {
    tab = next;
    document.querySelectorAll('.tab').forEach((el) => el.classList.toggle('active', el.dataset.tab === next));
    paint();
  }

  async function load() {
    content.replaceChildren(loadingBlock(t('backups.loading')));
    try {
      [backups, schedules, sites, databases] = await Promise.all([
        api.get('/backups'),
        api.get('/backup-schedules'),
        api.get('/sites'),
        api.get('/databases').catch((err) => {
          if (err.status === 503) { dbsAvailable = false; return []; }
          throw err;
        }),
      ]);
      backups = backups || [];
      schedules = schedules || [];
      sites = sites || [];
      databases = databases || [];
      paint();
    } catch (err) {
      content.replaceChildren(card({ body: h('div', { class: 'alert alert-danger' }, err.message) }));
    }
  }

  function paint() {
    if (tab === 'schedules') {
      paintSchedules();
      return;
    }
    if (!backups.length) {
      content.replaceChildren(card({
        body: emptyState(t('backups.noBackups'), t('backups.noBackupsHint'),
          canWrite() ? button(t('backups.create'), { variant: 'primary', icon: 'archive', onClick: openCreate }) : null, 'archive'),
      }));
      return;
    }
    content.replaceChildren(card({
      flush: true,
      body: dataTable([
        { label: t('common.name'), render: (b) => h('div', {},
          h('div', { class: 'row gap-sm' },
            h('b', {}, b.ref || b.id),
            b.kind === 'database' ? badge(t('backups.database'), 'violet') : badge(t('backups.site'), 'blue')),
          h('div', { class: 'muted small mono', title: b.id }, b.id.slice(0, 8))) },
        { label: t('backups.size'), render: (b) => formatBytes(b.size) },
        { label: t('backups.status'), render: (b) => h('div', {},
          statusBadge(b.status),
          b.error ? h('div', { class: 'muted small', title: b.error }, b.error.slice(0, 60)) : null) },
        { label: t('common.created'), render: (b) => h('span', { class: 'muted small', title: formatDate(b.created_at) }, relativeTime(b.created_at)) },
        { label: t('common.actions'), className: 'col-actions', render: (b) => h('div', { class: 'row-actions' },
          b.status === 'success' ? iconButton('download', t('backups.download'), () => downloadBackup(b)) : null,
          canWrite() && b.status === 'success' ? iconButton('rotate', t('backups.restore'), () => openRestore(b)) : null,
          canWrite() ? iconButton('trash', t('common.delete'), () => removeBackup(b), 'danger') : null) },
      ], backups),
    }));
  }

  function paintSchedules() {
    if (!schedules.length) {
      content.replaceChildren(card({
        body: emptyState(t('backups.noSchedules'), t('backups.noSchedulesHint'),
          canWrite() ? button(t('backups.addSchedule'), { variant: 'primary', icon: 'plus', onClick: () => openSchedule(null) }) : null, 'clock'),
      }));
      return;
    }
    content.replaceChildren(card({
      flush: true,
      actions: canWrite() ? button(t('backups.addSchedule'), { size: 'sm', icon: 'plus', onClick: () => openSchedule(null) }) : null,
      body: dataTable([
        { label: t('backups.target'), render: (s) => h('div', {},
          h('div', { class: 'row gap-sm' }, h('b', {}, s.ref),
            s.kind === 'database' ? badge(t('backups.database'), 'violet') : badge(t('backups.site'), 'blue')),
          h('div', { class: 'muted small' }, s.enabled ? t('backups.enabled') : t('status.disabled'))) },
        { label: t('backups.cronExpr'), render: (s) => h('code', { class: 'chip mono' }, s.cron_expr) },
        { label: t('backups.retention'), render: (s) => t('backups.keepCount', { n: s.keep }) },
        { label: t('backups.lastRun'), render: (s) => h('span', { class: 'muted small', title: formatDate(s.last_run_at) }, s.last_run_at ? relativeTime(s.last_run_at) : t('common.never')) },
        { label: t('common.actions'), className: 'col-actions', render: (s) => h('div', { class: 'row-actions' },
          canWrite() ? iconButton('edit', t('common.edit'), () => openSchedule(s)) : null,
          canWrite() ? iconButton('trash', t('common.delete'), () => removeSchedule(s), 'danger') : null) },
      ], schedules),
    }));
  }

  function targetOptions(kind) {
    return kind === 'database'
      ? databases.map((db) => ({ value: db.name, label: db.name }))
      : sites.map((site) => ({ value: site.name, label: site.name }));
  }

  function openCreate() {
    const kind = select([
      { value: 'site', label: t('backups.kindSite') },
      { value: 'database', label: t('backups.kindDatabase'), disabled: !dbsAvailable },
    ], { value: 'site' });
    const target = select(targetOptions('site'), {});
    const keep = input({ type: 'number', min: '0', value: '7' });
    if (!sites.length && databases.length) {
      kind.value = 'database';
      target.replaceChildren(...targetOptions('database').map((o) => h('option', { value: o.value }, o.label)));
    }
    kind.addEventListener('change', () => {
      target.replaceChildren(...targetOptions(kind.value).map((o) => h('option', { value: o.value }, o.label)));
    });

    const modal = openModal({
      title: t('backups.createTitle'),
      size: 'modal-sm',
      body: [
        field(t('backups.kind'), kind),
        field(t('backups.target'), target, t('backups.targetHint'), true),
        field(t('backups.keep'), keep, t('backups.keepHint')),
      ],
      footer: [
        button(t('common.cancel'), { onClick: () => modal.close() }),
        button(t('backups.createBtn'), {
          variant: 'primary', icon: 'archive',
          onClick: async () => {
            if (!target.value) { toast(t('backups.targetRequired'), 'warn'); return; }
            try {
              const job = await api.post('/backups', {
                kind: kind.value,
                ref: target.value,
                keep: Number(keep.value) || 0,
              });
              modal.close();
              toast(t('backups.jobStarted'), 'success');
              if (job && job.id) trackJob(job.id, t('backups.createBtn'));
            } catch (err) {
              toastError(err);
            }
          },
        }),
      ],
    });
  }

  function trackJob(jobId, title) {
    const progress = jobProgressModal(title);
    const modal = openModal({
      title,
      size: 'modal-sm',
      body: progress.element,
      footer: [
        button(t('nav.jobs'), { icon: 'zap', onClick: () => { modal.close(); navigate('/jobs'); } }),
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
      onError: (ev) => {
        progress.setError(ev.error || 'disconnected');
        modal.close();
      },
    });
  }

  async function openRestore(backup) {
    const confirm = input({ placeholder: backup.ref, class: 'input mono', autocomplete: 'off' });
    const modal = openModal({
      title: t('backups.restoreTitle'),
      size: 'modal-sm',
      body: [
        h('div', { class: 'alert alert-warn' }, iconSpan('alert', 16),
          h('div', {}, h('div', { style: { fontWeight: '600' } }, backup.ref), h('div', { class: 'small' }, t('backups.restoreWarning')))),
        h('div', { class: 'muted small mb2' }, t('backups.restoreConfirm', { name: backup.ref })),
        confirm,
        h('div', { class: 'muted small mt2' }, t('backups.safetyBackup')),
      ],
      footer: [
        button(t('common.cancel'), { onClick: () => modal.close() }),
        button(t('backups.restore'), {
          variant: 'danger',
          onClick: async () => {
            if (confirm.value.trim() !== backup.ref) {
              toast(t('backups.restoreConfirm', { name: backup.ref }), 'warn');
              return;
            }
            try {
              const job = await api.post(`/backups/${backup.id}/restore`, { confirm: confirm.value.trim() });
              modal.close();
              toast(t('backups.restoreStarted'), 'success');
              if (job && job.id) trackJob(job.id, t('backups.restore'));
            } catch (err) {
              toastError(err);
            }
          },
        }),
      ],
    });
  }

  function openSchedule(schedule) {
    const editing = Boolean(schedule);
    const kind = select([
      { value: 'site', label: t('backups.kindSite') },
      { value: 'database', label: t('backups.kindDatabase'), disabled: !dbsAvailable },
    ], { value: schedule ? schedule.kind : 'site' });
    const target = select(targetOptions(schedule ? schedule.kind : 'site'), { value: schedule ? schedule.ref : '' });
    const cron = input({ value: schedule ? schedule.cron_expr : '0 3 * * *', class: 'input mono', placeholder: '0 3 * * *' });
    const keep = input({ type: 'number', min: '1', value: schedule ? String(schedule.keep) : '7' });
    const enabled = switchControl(t('backups.enabled'), schedule ? schedule.enabled : true);

    kind.addEventListener('change', () => {
      target.replaceChildren(...targetOptions(kind.value).map((o) => h('option', { value: o.value }, o.label)));
    });

    const modal = openModal({
      title: editing ? t('backups.editSchedule') : t('backups.addSchedule'),
      size: 'modal-sm',
      body: [
        formRow(field(t('backups.kind'), kind), field(t('backups.target'), target)),
        field(t('backups.cronExpr'), cron, t('backups.cronHint'), true),
        formRow(field(t('backups.keep'), keep, t('backups.keepHint')), field(t('backups.enabled'), enabled)),
      ],
      footer: [
        button(t('common.cancel'), { onClick: () => modal.close() }),
        button(editing ? t('common.save') : t('common.create'), {
          variant: 'primary',
          onClick: async () => {
            const payload = {
              kind: kind.value,
              ref: target.value,
              cron_expr: cron.value.trim(),
              keep: Number(keep.value) || 7,
              enabled: enabled.querySelector('input').checked,
            };
            try {
              if (editing) await api.patch(`/backup-schedules/${schedule.id}`, payload);
              else await api.post('/backup-schedules', payload);
              modal.close();
              toast(editing ? t('backups.scheduleUpdated') : t('backups.scheduleCreated'), 'success');
              await load();
            } catch (err) {
              toastError(err);
            }
          },
        }),
      ],
    });
  }

  function downloadBackup(backup) {
    const a = h('a', { href: api.downloadURL(`/backups/${backup.id}/download`), download: '' });
    document.body.append(a);
    a.click();
    a.remove();
  }

  async function removeBackup(backup) {
    const ok = await confirmDialog({
      title: t('backups.deleteTitle'),
      message: `${backup.ref} · ${formatBytes(backup.size)}`,
      note: t('backups.deleteConfirm'),
      confirmText: t('common.delete'),
      danger: true,
    });
    if (!ok) return;
    try {
      await api.del(`/backups/${backup.id}`, {});
      toast(t('backups.deleted'), 'success');
      await load();
    } catch (err) {
      toastError(err);
    }
  }

  async function removeSchedule(schedule) {
    const ok = await confirmDialog({
      title: t('common.delete'),
      message: `${schedule.ref} (${schedule.cron_expr})`,
      confirmText: t('common.delete'),
      danger: true,
    });
    if (!ok) return;
    try {
      await api.del(`/backup-schedules/${schedule.id}`, {});
      toast(t('backups.scheduleDeleted'), 'success');
      await load();
    } catch (err) {
      toastError(err);
    }
  }

  await load();
  return page;
}
