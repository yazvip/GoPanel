import { api } from '../api.js';
import { t } from '../i18n.js';
import {
  h, pageHeader, card, badge, statusBadge, dataTable, emptyState, button, iconButton,
  openModal, input, loadingBlock, toast, toastError, formatDate, relativeTime, formatDuration,
  copyText,
} from '../ui.js';
import { watchJob } from '../jobwatch.js';
import { navigate } from '../router.js';

const STATUSES = ['', 'running', 'success', 'failed'];

function typeLabel(type) {
  const key = `jobs.type.${String(type || '').replace(/[.-]/g, '_')}`;
  const label = t(key);
  return label === key ? (type || '—') : label;
}

export async function render(ctx) {
  const { outlet, setCleanup } = ctx;
  const page = h('div', { class: 'page' });
  outlet.replaceChildren(page);
  let jobs = [];
  let status = '';
  let query = '';
  let poll = null;
  const content = h('div', {});
  const chips = h('div', { class: 'chips' });
  const search = input({ type: 'search', placeholder: t('jobs.searchPlaceholder'), class: 'input search-input' });

  page.append(
    pageHeader(t('jobs.title'), t('jobs.subtitle'),
      button(t('common.refresh'), { icon: 'refresh', onClick: load })),
    h('div', { class: 'toolbar' }, chips, search),
    content,
  );

  setCleanup(() => { if (poll) clearInterval(poll); });

  function paintChips() {
    chips.replaceChildren(...STATUSES.map((value) => h('button', {
      type: 'button',
      class: `chip-btn ${status === value ? 'active' : ''}`,
      onClick: () => { status = value; load(); },
    }, value ? t(`status.${value}`) : t('jobs.all'),
      h('span', { class: 'chip-count' }, String(value ? jobs.filter((j) => j.status === value).length : jobs.length)))));
  }

  async function load() {
    if (!jobs.length) content.replaceChildren(loadingBlock());
    try {
      jobs = (await api.get('/jobs?limit=200')) || [];
      paintChips();
      paint();
    } catch (err) {
      content.replaceChildren(card({ body: h('div', { class: 'alert alert-danger' }, err.message) }));
    }
    if (!poll) poll = setInterval(() => { if (jobs.some((job) => job.status === 'running')) load(); }, 5000);
  }

  let searchTimer = null;
  search.addEventListener('input', () => {
    if (searchTimer) clearTimeout(searchTimer);
    searchTimer = setTimeout(() => { query = search.value.trim().toLowerCase(); paint(); }, 200);
  });

  function filtered() {
    return jobs.filter((job) => {
      if (status && job.status !== status) return false;
      if (!query) return true;
      return [job.id, job.type, job.created_by, job.message, job.error]
        .some((value) => String(value || '').toLowerCase().includes(query));
    });
  }

  function duration(job) {
    if (!job.started_at) return '—';
    if (!job.finished_at) return t('jobs.running');
    const start = new Date(Number(job.started_at) * 1000).getTime();
    const end = new Date(Number(job.finished_at) * 1000).getTime();
    if (!Number.isFinite(start) || !Number.isFinite(end) || end < start) return '—';
    return formatDuration(Math.round((end - start) / 1000));
  }

  function paint() {
    const list = filtered();
    if (!list.length) {
      content.replaceChildren(card({
        body: emptyState(t('jobs.noJobs'), status || query ? t('jobs.noJobsFilter') : t('jobs.noJobsHint'), null, 'zap'),
      }));
      return;
    }
    content.replaceChildren(card({
      flush: true,
      body: dataTable([
        { label: t('jobs.id'), render: (job) => h('div', {},
          h('code', { class: 'mono small' }, job.id.slice(0, 8)),
          h('div', { class: 'muted small ellipsis', style: { maxWidth: '260px' }, title: job.id }, job.id)) },
        { label: t('jobs.type'), render: (job) => h('div', {},
          h('b', {}, typeLabel(job.type)),
          h('div', { class: 'muted small' }, job.created_by || '—')) },
        { label: t('common.status'), render: (job) => h('div', {},
          statusBadge(job.status),
          job.status === 'running'
            ? h('div', { class: 'progress-inline mt1' }, h('span', { style: { width: `${Math.max(0, Math.min(100, job.progress || 0))}%` } }))
            : null) },
        { label: t('jobs.duration'), render: (job) => h('span', { class: 'muted small' }, duration(job)) },
        { label: t('common.created'), render: (job) => h('span', { class: 'muted small', title: formatDate(job.created_at) },
          relativeTime(job.created_at)) },
        { label: t('common.actions'), className: 'col-actions', render: (job) => h('div', { class: 'row-actions' },
          iconButton('eye', t('jobs.view'), () => openDetail(job))) },
      ], list),
    }));
  }

  function openDetail(job) {
    const live = job.status === 'running';
    const bar = h('div', { class: 'progress-inline big mt1' }, h('span', { style: { width: `${job.progress || 0}%` } }));
    const message = h('div', { class: 'muted small' }, job.message || '');
    const output = h('pre', { class: 'log-view' }, job.output || (live ? t('jobs.waitingOutput') : t('common.none')));
    const errorBox = job.error ? h('div', { class: 'alert alert-danger small mt2' }, job.error) : null;
    let stop = () => {};

    const modal = openModal({
      title: `${typeLabel(job.type)} · ${job.id.slice(0, 8)}`,
      size: 'modal-lg',
      body: h('div', {},
        h('dl', { class: 'kv' },
          h('dt', {}, t('jobs.id')), h('dd', { class: 'mono small' }, job.id),
          h('dt', {}, t('jobs.type')), h('dd', {}, job.type),
          h('dt', {}, t('common.status')), h('dd', {}, statusBadge(job.status)),
          h('dt', {}, t('jobs.createdBy')), h('dd', {}, job.created_by || '—'),
          h('dt', {}, t('common.created')), h('dd', {}, formatDate(job.created_at)),
          h('dt', {}, t('jobs.started')), h('dd', {}, job.started_at ? formatDate(job.started_at) : '—'),
          h('dt', {}, t('jobs.finished')), h('dd', {}, job.finished_at ? formatDate(job.finished_at) : '—'),
          h('dt', {}, t('jobs.duration')), h('dd', {}, duration(job))),
        h('div', { class: 'mt3' }, h('div', { class: 'label' }, t('jobs.progress')), bar, message),
        errorBox,
        h('div', { class: 'row between mt3' },
          h('div', { class: 'card-title small' }, t('jobs.output')),
          button(t('common.copy'), { size: 'sm', icon: 'copy', onClick: () => copyText(output.textContent) })),
        output),
      onClose: () => stop(),
      footer: [
        button(t('common.close'), { onClick: () => modal.close() }),
      ],
    });

    if (live) {
      stop = watchJob(job.id, {
        onSnapshot: (snap) => applySnapshot(snap),
        onProgress: (ev) => {
          bar.firstChild.style.width = `${ev.progress || 0}%`;
          message.textContent = ev.message || '';
        },
        onLog: (line) => {
          if (output.textContent === t('jobs.waitingOutput')) output.textContent = '';
          output.textContent += line.endsWith('\n') ? line : `${line}\n`;
          output.scrollTop = output.scrollHeight;
        },
        onDone: (snap) => {
          applySnapshot(snap);
          toast(snap.status === 'failed' ? (snap.error || t('status.failed')) : t('common.done'),
            snap.status === 'failed' ? 'error' : 'success');
          load();
        },
        onError: (ev) => { message.textContent = ev.message || ev.error || 'disconnected'; },
      });
    }

    function applySnapshot(snap) {
      if (!snap) return;
      bar.firstChild.style.width = `${snap.progress || 0}%`;
      message.textContent = snap.message || '';
      if (snap.output) {
        output.textContent = snap.output;
        output.scrollTop = output.scrollHeight;
      }
    }
  }

  await load();
  return page;
}
