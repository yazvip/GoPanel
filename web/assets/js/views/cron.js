import { api } from '../api.js';
import { t } from '../i18n.js';
import {
  h, pageHeader, card, badge, dataTable, emptyState, button, iconButton,
  openModal, confirmDialog, field, input, textarea, select, checkbox, formRow,
  loadingBlock, toast, toastError, iconSpan, formatDate, relativeTime, copyText,
} from '../ui.js';
import { canWrite } from '../main.js';

const PRESETS = [
  { key: 'everyMinute', expr: '* * * * *' },
  { key: 'every5', expr: '*/5 * * * *' },
  { key: 'every15', expr: '*/15 * * * *' },
  { key: 'hourly', expr: '0 * * * *' },
  { key: 'daily', expr: '0 0 * * *' },
  { key: 'weekly', expr: '0 0 * * 0' },
  { key: 'monthly', expr: '0 0 1 * *' },
];

export async function render(ctx) {
  const { outlet } = ctx;
  const page = h('div', { class: 'page' });
  outlet.replaceChildren(page);
  let jobs = [];
  const content = h('div', {});

  page.append(
    pageHeader(t('cron.title'), t('cron.subtitle'),
      canWrite() ? button(t('cron.create'), { variant: 'primary', icon: 'plus', onClick: () => openForm(null) }) : null),
    content,
  );

  async function load() {
    content.replaceChildren(loadingBlock());
    try {
      jobs = (await api.get('/cron')) || [];
      paint();
    } catch (err) {
      content.replaceChildren(card({ body: h('div', { class: 'alert alert-danger' }, err.message) }));
    }
  }

  function paint() {
    if (!jobs.length) {
      content.replaceChildren(card({
        body: emptyState(t('cron.noJobs'), t('cron.noJobsHint'),
          canWrite() ? button(t('cron.create'), { variant: 'primary', icon: 'plus', onClick: () => openForm(null) }) : null, 'clock'),
      }));
      return;
    }
    content.replaceChildren(card({
      flush: true,
      body: dataTable([
        { label: t('cron.name'), render: (job) => h('div', {},
          h('div', { class: 'row gap-sm' }, h('b', {}, job.name),
            job.enabled ? null : badge(t('status.disabled'), 'gray')),
          h('div', { class: 'muted small mono ellipsis', style: { maxWidth: '420px' } }, job.command)) },
        { label: t('cron.schedule'), render: (job) => h('code', { class: 'chip mono' }, job.schedule) },
        { label: t('common.status'), render: (job) => job.last_status ? badge(job.last_status, job.last_status === 'success' ? 'green' : job.last_status === 'failed' ? 'red' : 'gray') : h('span', { class: 'muted small' }, t('common.never')) },
        { label: t('cron.lastRun'), render: (job) => h('span', { class: 'muted small', title: formatDate(job.last_run_at) }, job.last_run_at ? relativeTime(job.last_run_at) : t('common.never')) },
        { label: t('common.actions'), className: 'col-actions', render: (job) => h('div', { class: 'row-actions' },
          canWrite() ? iconButton('play', t('cron.runNow'), () => runNow(job)) : null,
          iconButton('filetext', t('common.logs'), () => openLog(job)),
          canWrite() ? iconButton('edit', t('common.edit'), () => openForm(job)) : null,
          canWrite() ? iconButton('trash', t('common.delete'), () => remove(job), 'danger') : null) },
      ], jobs),
    }));
  }

  function openForm(job) {
    const editing = Boolean(job);
    const name = input({ value: job ? job.name : '', placeholder: t('cron.namePlaceholder'), required: true });
    const preset = select(
      [...PRESETS.map((p) => ({ value: p.expr, label: `${t(`cron.presets.${p.key}`)} (${p.expr})` })),
        { value: 'custom', label: t('cron.presets.custom') }],
      { value: job && PRESETS.some((p) => p.expr === job.schedule) ? job.schedule : (job ? 'custom' : '0 0 * * *') },
    );
    const schedule = input({ value: job ? job.schedule : '0 0 * * *', placeholder: t('cron.schedulePlaceholder'), class: 'input mono', required: true });
    const command = textarea({ rows: '4', placeholder: t('cron.commandPlaceholder'), value: job ? job.command : '', class: 'textarea mono' });
    const enabled = checkbox(t('cron.enabled'), job ? job.enabled : true);

    preset.addEventListener('change', () => {
      if (preset.value !== 'custom') schedule.value = preset.value;
      schedule.focus();
    });
    schedule.addEventListener('input', () => {
      const match = PRESETS.find((p) => p.expr === schedule.value.trim());
      preset.value = match ? match.expr : 'custom';
    });

    const modal = openModal({
      title: editing ? t('cron.editTitle') : t('cron.createTitle'),
      body: h('div', {},
        formRow(
          field(t('cron.name'), name, t('cron.nameHint'), true),
          field(t('cron.enabled'), h('div', { class: 'field-inline' }, enabled))),
        formRow(
          field(t('cron.preset'), preset),
          field(t('cron.schedule'), schedule, t('cron.scheduleHint'), true)),
        field(t('cron.command'), command, t('cron.rootHint'), true)),
      footer: [
        button(t('common.cancel'), { onClick: () => modal.close() }),
        button(editing ? t('common.save') : t('common.create'), {
          variant: 'primary',
          onClick: async () => {
            const payload = {
              name: name.value.trim(),
              schedule: schedule.value.trim(),
              command: command.value,
              enabled: enabled.querySelector('input').checked,
            };
            try {
              if (editing) await api.patch(`/cron/${job.id}`, payload);
              else await api.post('/cron', payload);
              modal.close();
              toast(editing ? t('cron.updated') : t('cron.created'), 'success');
              await load();
            } catch (err) {
              toastError(err);
            }
          },
        }),
      ],
    });
  }

  async function runNow(job) {
    try {
      await api.post(`/cron/${job.id}/run`, {});
      toast(t('cron.runStarted'), 'success');
      setTimeout(load, 1500);
    } catch (err) {
      toastError(err);
    }
  }

  async function openLog(job) {
    const box = h('pre', { class: 'log-view' }, t('cron.loadingLog'));
    const modal = openModal({
      title: `${t('cron.logTitle')} · ${job.name}`,
      size: 'modal-lg',
      body: h('div', {},
        h('div', { class: 'row gap mb2' },
          button(t('common.refresh'), { icon: 'refresh', onClick: () => fetchLog(job.id, box) }),
          button(t('common.copy'), { icon: 'copy', onClick: () => copyText(box.textContent || '') })),
        box),
      footer: button(t('common.close'), { onClick: () => modal.close() }),
    });
    fetchLog(job.id, box);
  }

  async function fetchLog(id, box) {
    try {
      const data = await api.get(`/cron/${id}/log?lines=500`);
      box.textContent = data.content || t('cron.emptyLog');
      box.scrollTop = box.scrollHeight;
    } catch (err) {
      box.textContent = err.message;
    }
  }

  async function remove(job) {
    const ok = await confirmDialog({
      title: t('common.delete'),
      message: t('cron.deleteConfirm', { name: job.name }),
      confirmText: t('common.delete'),
      danger: true,
    });
    if (!ok) return;
    try {
      await api.del(`/cron/${job.id}`, {});
      toast(t('cron.deleted'), 'success');
      await load();
    } catch (err) {
      toastError(err);
    }
  }

  await load();
  return page;
}
