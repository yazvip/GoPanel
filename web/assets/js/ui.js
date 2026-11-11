import { icon } from './icons.js';
import { t } from './i18n.js';
import { errorMessage } from './api.js';

export function h(tag, attrs, ...children) {
  const el = document.createElement(tag);
  if (attrs && typeof attrs === 'object' && !(attrs instanceof Node) && !Array.isArray(attrs)) {
    for (const [key, value] of Object.entries(attrs)) {
      if (value === undefined || value === null || value === false) continue;
      if (key === 'class' || key === 'className') el.className = value;
      else if (key === 'html') el.innerHTML = value;
      else if (key === 'text') el.textContent = value;
      else if (key === 'style' && typeof value === 'object') Object.assign(el.style, value);
      else if (key === 'dataset') Object.assign(el.dataset, value);
      else if (key.startsWith('on') && typeof value === 'function') el.addEventListener(key.slice(2).toLowerCase(), value);
      else if (key === 'value' || key === 'checked' || key === 'disabled' || key === 'selected') el[key] = value;
      else el.setAttribute(key, value === true ? '' : value);
    }
  } else if (attrs !== undefined && attrs !== null) {
    children.unshift(attrs);
  }
  append(el, children);
  return el;
}

function append(el, children) {
  for (const child of children) {
    if (child === null || child === undefined || child === false) continue;
    if (Array.isArray(child)) append(el, child);
    else if (child instanceof Node) el.append(child);
    else el.append(document.createTextNode(String(child)));
  }
}

export function iconSpan(name, size = 18, cls = '') {
  const span = document.createElement('span');
  span.className = 'icon-inline';
  span.innerHTML = icon(name, size, cls);
  return span.firstElementChild;
}

export function clear(el) {
  while (el.firstChild) el.removeChild(el.firstChild);
  return el;
}

export function pageHeader(title, subtitle, actions) {
  return h('div', { class: 'page-head' },
    h('div', { class: 'page-head-text' },
      h('h1', { class: 'page-title' }, title),
      subtitle ? h('div', { class: 'page-sub' }, subtitle) : null),
    actions ? h('div', { class: 'page-actions' }, actions) : null);
}

export function card({ title, icon, actions, body, flush = false, foot = null, class: cls = '' }) {
  const el = h('div', { class: `card ${cls}` });
  if (title || actions) {
    el.append(h('div', { class: 'card-head' },
      h('div', { class: 'card-title' }, icon ? iconSpan(icon, 17) : null, title || ''),
      actions ? h('div', { class: 'card-actions' }, actions) : null));
  }
  const bodyEl = h('div', { class: `card-body${flush ? ' flush' : ''}` });
  if (body !== undefined) {
    if (Array.isArray(body)) append(bodyEl, body);
    else if (body instanceof Node) bodyEl.append(body);
    else bodyEl.textContent = body;
  }
  el.append(bodyEl);
  if (foot) el.append(h('div', { class: 'card-foot' }, foot));
  return el;
}

export function statCard({ label, value, meta, icon: iconName, tone = 'green', meter = null }) {
  const body = h('div', { class: 'stat-body' },
    h('div', { class: 'stat-label' }, label),
    h('div', { class: 'stat-value' }, value),
    meta ? h('div', { class: 'stat-meta' }, meta) : null);
  if (meter !== null && meter !== undefined) {
    const pct = Math.max(0, Math.min(100, Number(meter) || 0));
    body.append(h('div', { class: `meter ${pct >= 90 ? 'red' : pct >= 70 ? 'amber' : ''}` },
      h('span', { style: { width: `${pct}%` } })));
  }
  return h('div', { class: 'stat' },
    iconName ? h('div', { class: `stat-icon ${tone}` }, iconSpan(iconName, 20)) : null,
    body);
}

export function badge(text, tone = 'gray') {
  return h('span', { class: `badge badge-${tone}` }, text);
}

export function statusBadge(status) {
  const map = {
    success: ['green', t('status.success')],
    failed: ['red', t('status.failed')],
    running: ['blue', t('status.running')],
    queued: ['gray', t('status.queued')],
    active: ['green', t('status.active')],
    inactive: ['gray', t('status.inactive')],
    enabled: ['green', t('status.enabled')],
    disabled: ['gray', t('status.disabled')],
    installed: ['green', t('status.installed')],
  };
  const entry = map[status] || ['gray', status || '—'];
  return badge(entry[1], entry[0]);
}

export function emptyState(title, text, action = null, iconName = 'box') {
  return h('div', { class: 'empty' },
    h('div', { class: 'empty-icon' }, iconSpan(iconName, 22)),
    h('div', { class: 'empty-title' }, title),
    text ? h('div', { class: 'empty-text' }, text) : null,
    action || null);
}

export function loadingBlock(text) {
  return h('div', { class: 'loading-block' }, h('span', { class: 'spinner' }), text || t('common.loading'));
}

export function dataTable(columns, rows, { empty, rowClass } = {}) {
  const table = h('table', { class: 'table' });
  const thead = h('thead', {}, h('tr', {}, columns.map((col) =>
    h('th', { class: col.className || '' }, col.label || ''))));
  const tbody = h('tbody', {});
  if (!rows || rows.length === 0) {
    tbody.append(h('tr', {}, h('td', { colspan: String(columns.length) },
      empty || emptyState(t('common.empty'), t('common.emptyHint')))));
  } else {
    for (const row of rows) {
      const tr = h('tr', { class: rowClass ? rowClass(row) : '' });
      for (const col of columns) {
        const td = h('td', { class: col.className || '' });
        if (col.render) {
          const value = col.render(row);
          if (Array.isArray(value)) append(td, value);
          else if (value instanceof Node) td.append(value);
          else td.textContent = value === undefined || value === null ? '—' : String(value);
        } else {
          const value = row[col.key];
          td.textContent = value === undefined || value === null || value === '' ? '—' : String(value);
        }
        tr.append(td);
      }
      tbody.append(tr);
    }
  }
  table.append(thead, tbody);
  return h('div', { class: 'table-wrap' }, table);
}

export function iconButton(name, title, onClick, cls = '') {
  return h('button', { class: `icon-btn ${cls}`, title, 'aria-label': title, onClick }, iconSpan(name, 16));
}

export function button(label, { variant = '', size = '', icon: iconName, onClick, type = 'button', disabled = false, class: cls = '' } = {}) {
  return h('button', {
    class: `btn ${variant ? `btn-${variant}` : ''} ${size ? `btn-${size}` : ''} ${cls}`.trim(),
    type,
    disabled,
    onClick,
  }, iconName ? iconSpan(iconName, 15) : null, label);
}

export function field(label, control, hint, required = false) {
  return h('div', { class: 'field' },
    label ? h('label', { class: 'label' }, label, required ? h('span', { class: 'req' }, ' *') : null) : null,
    control,
    hint ? h('div', { class: 'hint' }, hint) : null);
}

export function input(attrs = {}) {
  return h('input', { class: 'input', ...attrs });
}

export function textarea(attrs = {}) {
  return h('textarea', { class: 'textarea', ...attrs });
}

export function select(options, attrs = {}) {
  const el = h('select', { class: 'select', ...attrs });
  for (const opt of options) {
    const o = h('option', { value: opt.value }, opt.label);
    if (opt.disabled) o.disabled = true;
    el.append(o);
  }
  if (attrs.value !== undefined) el.value = attrs.value;
  return el;
}

export function checkbox(label, checked = false, attrs = {}) {
  return h('label', { class: 'checkbox' },
    h('input', { type: 'checkbox', checked, ...attrs }), h('span', {}, label));
}

export function switchControl(label, checked = false, attrs = {}) {
  return h('label', { class: 'switch' },
    h('input', { type: 'checkbox', checked, ...attrs }),
    h('span', { class: 'track' }),
    h('span', {}, label));
}

export function formRow(...children) {
  return h('div', { class: 'form-row' }, children);
}

/* ---------------- modal ---------------- */
export function openModal({ title, body, footer, size = '', onClose, closeOnBackdrop = true }) {
  const backdrop = h('div', { class: 'modal-backdrop' });
  const modal = h('div', { class: `modal ${size}`.trim() });
  let closed = false;
  const close = () => {
    if (closed) return;
    closed = true;
    document.removeEventListener('keydown', onKey);
    backdrop.remove();
    if (!document.querySelector('.modal-backdrop')) document.body.style.overflow = '';
    if (onClose) onClose();
  };
  const onKey = (ev) => {
    if (ev.key === 'Escape') close();
  };
  const head = h('div', { class: 'modal-head' },
    h('div', { class: 'modal-title' }, title || ''),
    h('button', { class: 'modal-close', type: 'button', 'aria-label': t('common.close'), onClick: close }, iconSpan('x', 17)));
  const bodyEl = h('div', { class: 'modal-body' });
  if (body !== undefined) {
    if (Array.isArray(body)) append(bodyEl, body);
    else if (body instanceof Node) bodyEl.append(body);
    else bodyEl.textContent = body;
  }
  modal.append(head, bodyEl);
  if (footer) {
    const footEl = h('div', { class: 'modal-foot' });
    if (Array.isArray(footer)) append(footEl, footer);
    else if (footer instanceof Node) footEl.append(footer);
    modal.append(footEl);
  }
  backdrop.append(modal);
  if (closeOnBackdrop) {
    backdrop.addEventListener('mousedown', (ev) => {
      if (ev.target === backdrop) close();
    });
  }
  document.body.append(backdrop);
  document.body.style.overflow = 'hidden';
  document.addEventListener('keydown', onKey);
  const autofocus = modal.querySelector('[autofocus], input:not([type=hidden]), textarea, select');
  if (autofocus) setTimeout(() => autofocus.focus(), 30);
  return { el: modal, body: bodyEl, close, backdrop };
}

export function confirmDialog({ title, message, confirmText, cancelText, danger = false, requireText = '', note = '' }) {
  return new Promise((resolve) => {
    let settled = false;
    const finish = (value) => {
      if (settled) return;
      settled = true;
      resolve(value);
    };
    const confirmBtn = h('button', {
      class: `btn ${danger ? 'btn-danger' : 'btn-primary'}`, type: 'button', disabled: Boolean(requireText),
      onClick: () => { finish(true); modal.close(); },
    }, confirmText || t('common.confirm'));
    const confirmInput = requireText ? input({ placeholder: requireText, autofocus: true }) : null;
    if (confirmInput) {
      confirmInput.addEventListener('input', () => {
        confirmBtn.disabled = confirmInput.value.trim() !== requireText;
      });
      confirmInput.addEventListener('keydown', (ev) => {
        if (ev.key === 'Enter' && !confirmBtn.disabled) confirmBtn.click();
      });
    }
    const modal = openModal({
      title: title || t('common.confirm'),
      size: 'modal-sm',
      body: [
        note ? h('div', { class: `modal-note ${danger ? 'danger' : 'info'}` }, iconSpan('alert', 16), h('div', {}, note)) : null,
        h('div', { style: { fontSize: '13px' } }, message || ''),
        confirmInput ? field(t('common.typeToConfirm', { value: requireText }), confirmInput) : null,
      ],
      footer: [
        h('button', { class: 'btn', type: 'button', onClick: () => { finish(false); modal.close(); } }, cancelText || t('common.cancel')),
        confirmBtn,
      ],
      onClose: () => finish(false),
    });
  });
}

export function promptDialog({ title, label, placeholder = '', value = '', type = 'text', hint = '', confirmText, options = null }) {
  return new Promise((resolve) => {
    let settled = false;
    const finish = (val) => { if (!settled) { settled = true; resolve(val); } };
    const control = options
      ? select(options, { value })
      : input({ value, placeholder, type, autofocus: true });
    const submit = () => {
      finish(control.value.trim());
      modal.close();
    };
    control.addEventListener('keydown', (ev) => { if (ev.key === 'Enter') submit(); });
    const modal = openModal({
      title: title || t('common.prompt'),
      size: 'modal-sm',
      body: field(label || '', control, hint),
      footer: [
        h('button', { class: 'btn', type: 'button', onClick: () => { finish(null); modal.close(); } }, t('common.cancel')),
        h('button', { class: 'btn btn-primary', type: 'button', onClick: submit }, confirmText || t('common.ok')),
      ],
      onClose: () => finish(null),
    });
  });
}

export function showCredentials({ title, username, password, database, note }) {
  const copy = (text) => copyText(text);
  return openModal({
    title: title || t('common.credentials'),
    size: 'modal-sm',
    body: [
      note ? h('div', { class: 'alert alert-info mb2' }, iconSpan('info', 16), h('div', {}, note)) : null,
      database ? h('div', { class: 'field' }, h('div', { class: 'label' }, t('databases.name')),
        h('div', { class: 'input-group' }, input({ value: database, readonly: true }),
          h('button', { class: 'btn', type: 'button', onClick: () => copy(database) }, iconSpan('copy', 15)))) : null,
      h('div', { class: 'field' }, h('div', { class: 'label' }, t('common.username')),
        h('div', { class: 'input-group' }, input({ value: username, readonly: true }),
          h('button', { class: 'btn', type: 'button', onClick: () => copy(username) }, iconSpan('copy', 15)))),
      h('div', { class: 'field' }, h('div', { class: 'label' }, t('common.password')),
        h('div', { class: 'input-group' }, input({ value: password, readonly: true, class: 'input mono' }),
          h('button', { class: 'btn', type: 'button', onClick: () => copy(password) }, iconSpan('copy', 15)))),
      h('div', { class: 'hint' }, t('common.credentialsOnce')),
    ],
    footer: h('button', { class: 'btn btn-primary', type: 'button', onClick: () => {
      const el = document.querySelector('.modal-backdrop .modal-close');
      if (el) el.click();
    } }, t('common.done')),
  });
}

/* ---------------- toasts ---------------- */
function toastHost() {
  let host = document.querySelector('.toasts');
  if (!host) {
    host = h('div', { class: 'toasts' });
    document.body.append(host);
  }
  return host;
}

export function toast(message, type = 'info', timeout = 0) {
  const icons = { success: 'check', error: 'alert', warn: 'alert', info: 'info' };
  const el = h('div', { class: `toast ${type}` },
    h('span', { class: 'toast-icon' }, iconSpan(icons[type] || 'info', 16)),
    h('span', { class: 'grow' }, message),
    h('button', { type: 'button', 'aria-label': t('common.close'), onClick: () => el.remove() }, iconSpan('x', 14)));
  toastHost().append(el);
  const ms = timeout || (type === 'error' ? 9000 : 4500);
  setTimeout(() => el.remove(), ms);
  return el;
}

export function toastError(err) {
  toast(errorMessage(err), 'error');
}

export async function copyText(text) {
  try {
    await navigator.clipboard.writeText(text);
    toast(t('common.copied'), 'success', 2000);
    return true;
  } catch {
    const ta = h('textarea', { style: { position: 'fixed', left: '-9999px' } });
    ta.value = text;
    document.body.append(ta);
    ta.select();
    try { document.execCommand('copy'); toast(t('common.copied'), 'success', 2000); }
    catch { toast(t('common.copyFailed'), 'error'); }
    ta.remove();
    return false;
  }
}

/* ---------------- formatting ---------------- */
export function formatBytes(bytes, digits = 1) {
  const n = Number(bytes);
  if (!n || n < 0) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB'];
  let i = 0;
  let value = n;
  while (value >= 1024 && i < units.length - 1) {
    value /= 1024;
    i += 1;
  }
  const fixed = i === 0 ? 0 : digits;
  return `${value.toFixed(fixed).replace(/\.0+$/, '')} ${units[i]}`;
}

export function formatNumber(n) {
  return new Intl.NumberFormat(document.documentElement.lang || 'en').format(Number(n) || 0);
}

export function formatDate(ts, withTime = true) {
  if (!ts) return '—';
  const date = new Date(Number(ts) * 1000);
  if (Number.isNaN(date.getTime())) return '—';
  const opts = withTime
    ? { year: 'numeric', month: 'short', day: '2-digit', hour: '2-digit', minute: '2-digit' }
    : { year: 'numeric', month: 'short', day: '2-digit' };
  return new Intl.DateTimeFormat(document.documentElement.lang === 'id' ? 'id-ID' : 'en-GB', opts).format(date);
}

export function relativeTime(ts) {
  if (!ts) return '—';
  const diff = Math.round(Date.now() / 1000 - Number(ts));
  if (Math.abs(diff) < 60) return t('time.justNow');
  if (diff > 0) {
    if (diff < 3600) return t('time.minutesAgo', { n: Math.floor(diff / 60) });
    if (diff < 86400) return t('time.hoursAgo', { n: Math.floor(diff / 3600) });
    if (diff < 2592000) return t('time.daysAgo', { n: Math.floor(diff / 86400) });
  }
  return formatDate(ts, false);
}

export function formatDuration(seconds) {
  const s = Math.max(0, Math.floor(Number(seconds) || 0));
  const days = Math.floor(s / 86400);
  const hours = Math.floor((s % 86400) / 3600);
  const mins = Math.floor((s % 3600) / 60);
  if (days > 0) return `${days}d ${hours}h`;
  if (hours > 0) return `${hours}h ${mins}m`;
  if (mins > 0) return `${mins}m ${s % 60}s`;
  return `${s}s`;
}

export function percent(used, total) {
  if (!total) return 0;
  return Math.min(100, Math.round((used / total) * 1000) / 10);
}

export function fileKind(entry) {
  if (entry.is_dir) return 'dir';
  const name = (entry.name || '').toLowerCase();
  if (name.endsWith('.php')) return 'php';
  if (/\.(png|jpe?g|gif|webp|svg|ico|bmp)$/.test(name)) return 'img';
  if (/\.(js|ts|css|html?|json|go|py|sh|yml|yaml|sql|md|txt|conf)$/.test(name)) return 'code';
  if (/\.(zip|tar|gz|tgz|bz2|xz|rar|7z)$/.test(name)) return 'archive';
  return 'file';
}

export function fileIcon(entry) {
  const kind = fileKind(entry);
  const names = { dir: 'folder', php: 'code', img: 'eye', code: 'filetext', archive: 'archive', file: 'file' };
  return h('span', { class: `file-icon ${kind}` }, iconSpan(names[kind] || 'file', 17));
}

export function debounce(fn, ms = 250) {
  let timer = null;
  return (...args) => {
    clearTimeout(timer);
    timer = setTimeout(() => fn(...args), ms);
  };
}

export function sectionTitle(text) {
  return h('h4', { style: { fontSize: '12px', textTransform: 'uppercase', letterSpacing: '.6px', color: 'var(--muted)', marginBottom: '10px' } }, text);
}

export function kvList(items) {
  const dl = h('dl', { class: 'kv' });
  for (const [key, value] of items) {
    if (value === undefined || value === null) continue;
    dl.append(h('dt', {}, key), h('dd', {}, value));
  }
  return dl;
}

export function toneClass(status) {
  return `status-${status || 'queued'}`;
}
