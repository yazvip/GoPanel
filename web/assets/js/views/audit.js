import { api, qs } from '../api.js';
import { t } from '../i18n.js';
import {
  h, pageHeader, card, badge, dataTable, emptyState, button, loadingBlock,
  input, select, toastError, formatDate,
} from '../ui.js';

/* Loading, empty and error all read as the same centred block. */
function stateBlock(title, text, action, iconName, tone) {
  const el = emptyState(title, text, action, iconName);
  if (tone) el.classList.add(`adm-state-${tone}`);
  return el;
}

export async function render(ctx) {
  const { outlet } = ctx;
  const page = h('div', { class: 'page admin-page' });
  outlet.replaceChildren(page);
  let events = [];
  let total = 0;
  let currentPage = 1;
  let perPage = 50;
  let resultFilter = '';
  let query = '';
  const content = h('div', {});
  const search = input({
    type: 'search',
    placeholder: t('audit.searchPlaceholder'),
    class: 'input search-input',
    'aria-label': t('audit.searchLabel'),
  });
  const resultSelect = select([
    { value: '', label: t('audit.allResults') },
    { value: 'success', label: t('status.success') },
    { value: 'failed', label: t('status.failed') },
  ], { value: '', 'aria-label': t('audit.resultFilter') });
  const perPageSelect = select([
    { value: '25', label: '25' },
    { value: '50', label: '50' },
    { value: '100', label: '100' },
    { value: '200', label: '200' },
  ], { value: '50', 'aria-label': t('audit.perPage') });
  page.append(
    pageHeader(t('audit.title'), t('audit.subtitle'),
      button(t('common.refresh'), { icon: 'refresh', onClick: () => load(currentPage) })),
    h('div', { class: 'toolbar' },
      search, resultSelect,
      h('div', { class: 'row gap-sm' }, h('span', { class: 'muted small' }, t('audit.perPage')), perPageSelect)),
    content,
  );

  let searchTimer = null;
  search.addEventListener('input', () => {
    if (searchTimer) clearTimeout(searchTimer);
    searchTimer = setTimeout(() => { query = search.value.trim().toLowerCase(); paint(); }, 200);
  });
  resultSelect.addEventListener('change', () => { resultFilter = resultSelect.value; paint(); });
  perPageSelect.addEventListener('change', () => {
    perPage = Number(perPageSelect.value) || 50;
    load(1);
  });

  async function load(targetPage = 1) {
    content.replaceChildren(loadingBlock(t('common.loading')));
    try {
      const data = await api.get(`/audit${qs({ page: targetPage, per_page: perPage })}`);
      events = data.events || [];
      total = data.total || 0;
      currentPage = data.page || targetPage;
      paint();
    } catch (err) {
      content.replaceChildren(card({
        body: stateBlock(t('audit.loadFailed'), err.message,
          button(t('common.retry'), { icon: 'refresh', onClick: () => load(currentPage) }), 'alert', 'error'),
      }));
    }
  }

  function filtered() {
    return events.filter((ev) => {
      if (resultFilter && ev.result !== resultFilter) return false;
      if (!query) return true;
      return [ev.action, ev.username, ev.ip, ev.target, ev.detail]
        .some((value) => String(value || '').toLowerCase().includes(query));
    });
  }

  function paint() {
    const list = filtered();
    const pages = Math.max(1, Math.ceil(total / perPage));
    const body = list.length ? dataTable([
      {
        label: t('audit.time'),
        className: 'adm-col-time',
        render: (ev) => h('span', { class: 'muted small mono' }, formatDate(ev.ts)),
      },
      {
        label: t('audit.user'),
        className: 'adm-col-user',
        render: (ev) => h('div', {},
          h('div', { class: 'cell-name' }, h('b', {}, ev.username || '—')),
          h('span', { class: 'cell-sub mono' }, ev.ip || '')),
      },
      {
        label: t('audit.action'),
        className: 'adm-col-action',
        render: (ev) => h('div', {},
          h('code', { class: 'mono small' }, ev.action),
          ev.target ? h('span', { class: 'cell-sub', title: ev.target }, ev.target) : null),
      },
      {
        label: t('audit.detail'),
        className: 'adm-col-detail',
        render: (ev) => (ev.detail
          ? h('span', { class: 'muted small', title: ev.detail }, ev.detail)
          : h('span', { class: 'muted small' }, '—')),
      },
      {
        label: t('audit.result'),
        className: 'adm-col-result',
        render: (ev) => badge(ev.result === 'success' ? t('status.success') : ev.result === 'failed' ? t('status.failed') : t('common.unknown'),
          ev.result === 'success' ? 'green' : ev.result === 'failed' ? 'red' : 'gray'),
      },
    ], list) : stateBlock(t('audit.noEvents'),
      query || resultFilter ? t('audit.noEventsFilter') : t('audit.noEventsHint'),
      null, 'activity');
    const pager = total ? h('div', { class: 'pager' },
      h('span', { class: 'muted small' },
        t('audit.showing', {
          from: (currentPage - 1) * perPage + 1,
          to: Math.min(currentPage * perPage, total),
          total,
        })),
      h('div', { class: 'row gap-sm' },
        button(t('common.prev'), { size: 'sm', disabled: currentPage <= 1, onClick: () => load(currentPage - 1) }),
        h('span', { class: 'muted small' }, t('audit.pageOf', { page: currentPage, pages })),
        button(t('common.next'), { size: 'sm', disabled: currentPage >= pages, onClick: () => load(currentPage + 1) }))) : null;
    content.replaceChildren(card({
      class: 'adm-t-audit',
      flush: true,
      body,
      foot: pager,
    }));
  }

  await load(1);
  return page;
}
