import { api, qs } from '../api.js';
import { t } from '../i18n.js';
import {
  h, pageHeader, card, emptyState, button, iconButton, openModal, confirmDialog,
  promptDialog, field, input, select, textarea, loadingBlock, toast, toastError,
  iconSpan, formatBytes, formatDate, copyText, fileKind, openContextMenu,
} from '../ui.js';
import { canWrite } from '../main.js';

const ICONS = { dir: 'folder', php: 'code', img: 'image', code: 'code', archive: 'archive', file: 'file' };

const VIEW_KEY = 'gopanel.files.view';
const SORT_KEY = 'gopanel.files.sort';

// CodeMirror 5 is vendored under /assets/vendor/codemirror (embedded in the
// binary) so the editor works offline and without loosening the panel CSP.
const CM_BASE = '/assets/vendor/codemirror';
let cmCorePromise = null;

// Extensions rendered by the built-in textarea when no mode matches.
const CM_LANGS = {
  php: { mode: 'application/x-httpd-php', files: ['clike', 'xml', 'javascript', 'css', 'htmlmixed', 'php'] },
  phtml: { mode: 'application/x-httpd-php', files: ['clike', 'xml', 'javascript', 'css', 'htmlmixed', 'php'] },
  inc: { mode: 'application/x-httpd-php', files: ['clike', 'xml', 'javascript', 'css', 'htmlmixed', 'php'] },
  js: { mode: 'text/javascript', files: ['javascript'] },
  mjs: { mode: 'text/javascript', files: ['javascript'] },
  cjs: { mode: 'text/javascript', files: ['javascript'] },
  jsx: { mode: 'text/javascript', files: ['javascript'] },
  ts: { mode: 'text/typescript', files: ['javascript'] },
  tsx: { mode: 'text/typescript', files: ['javascript'] },
  go: { mode: 'text/x-go', files: ['go'] },
  html: { mode: 'text/html', files: ['xml', 'javascript', 'css', 'htmlmixed'] },
  htm: { mode: 'text/html', files: ['xml', 'javascript', 'css', 'htmlmixed'] },
  vue: { mode: 'text/x-vue', files: ['xml', 'javascript', 'css', 'htmlmixed', 'vue'] },
  twig: { mode: 'text/x-twig', files: ['xml', 'javascript', 'css', 'htmlmixed', 'twig'] },
  css: { mode: 'text/css', files: ['css'] },
  scss: { mode: 'text/x-scss', files: ['css'] },
  less: { mode: 'text/x-less', files: ['css'] },
  sass: { mode: 'text/x-sass', files: ['css'] },
  json: { mode: 'application/json', files: ['javascript'] },
  json5: { mode: 'application/json', files: ['javascript'] },
  sql: { mode: 'text/x-sql', files: ['sql'] },
  sh: { mode: 'text/x-sh', files: ['shell'] },
  bash: { mode: 'text/x-sh', files: ['shell'] },
  zsh: { mode: 'text/x-sh', files: ['shell'] },
  py: { mode: 'text/x-python', files: ['python'] },
  rb: { mode: 'text/x-ruby', files: ['ruby'] },
  java: { mode: 'text/x-java', files: ['clike'] },
  c: { mode: 'text/x-csrc', files: ['clike'] },
  h: { mode: 'text/x-csrc', files: ['clike'] },
  cpp: { mode: 'text/x-c++src', files: ['clike'] },
  cc: { mode: 'text/x-c++src', files: ['clike'] },
  cxx: { mode: 'text/x-c++src', files: ['clike'] },
  hpp: { mode: 'text/x-c++src', files: ['clike'] },
  rs: { mode: 'text/x-rustsrc', files: ['rust'] },
  yml: { mode: 'text/x-yaml', files: ['yaml'] },
  yaml: { mode: 'text/x-yaml', files: ['yaml'] },
  xml: { mode: 'application/xml', files: ['xml'] },
  svg: { mode: 'application/xml', files: ['xml'] },
  md: { mode: 'text/x-markdown', files: ['xml', 'markdown'] },
  markdown: { mode: 'text/x-markdown', files: ['xml', 'markdown'] },
  toml: { mode: 'text/x-toml', files: ['toml'] },
  ini: { mode: 'text/x-properties', files: ['properties'] },
  conf: { mode: 'text/x-properties', files: ['properties'] },
};

function detectLang(name) {
  const lower = (name || '').toLowerCase();
  if (lower === 'nginx.conf' || lower.endsWith('.nginx')) return { mode: 'text/x-nginx-conf', files: ['nginx'] };
  if (lower === 'dockerfile' || lower.startsWith('dockerfile.')) return { mode: 'text/x-dockerfile', files: ['dockerfile'] };
  if (lower === 'makefile' || lower === 'gnumakefile') return { mode: 'text/x-sh', files: ['shell'] };
  if (!lower.includes('.')) return null;
  return CM_LANGS[lower.split('.').pop()] || null;
}

function loadScriptOnce(src) {
  return new Promise((resolve, reject) => {
    if (document.querySelector(`script[data-cm="${src}"]`)) {
      resolve();
      return;
    }
    const s = document.createElement('script');
    s.src = src;
    s.dataset.cm = src;
    s.onload = () => resolve();
    s.onerror = () => reject(new Error(t('files.editorUnavailable')));
    document.head.appendChild(s);
  });
}

function loadStyleOnce(href) {
  if (document.querySelector(`link[data-cm="${href}"]`)) return;
  const l = document.createElement('link');
  l.rel = 'stylesheet';
  l.href = href;
  l.dataset.cm = href;
  document.head.appendChild(l);
}

function isDark() {
  return document.documentElement.dataset.theme === 'dark';
}

function loadCodeMirrorCore() {
  if (cmCorePromise) return cmCorePromise;
  cmCorePromise = (async () => {
    loadStyleOnce(`${CM_BASE}/codemirror.min.css`);
    loadStyleOnce(`${CM_BASE}/theme/material-darker.min.css`);
    await loadScriptOnce(`${CM_BASE}/codemirror.min.js`);
    await Promise.all([
      loadScriptOnce(`${CM_BASE}/addon/edit/matchbrackets.min.js`),
      loadScriptOnce(`${CM_BASE}/addon/edit/closebrackets.min.js`),
      loadScriptOnce(`${CM_BASE}/addon/selection/active-line.min.js`),
    ]);
    return window.CodeMirror;
  })();
  return cmCorePromise;
}

async function loadLang(lang) {
  if (!lang) return;
  await Promise.all(lang.files.map((f) => loadScriptOnce(`${CM_BASE}/mode/${f}/${f}.min.js`)));
}

function joinPath(dir, name) {
  if (!dir || dir === '.') return name;
  return `${dir.replace(/\/+$/, '')}/${name}`;
}

function relativeUploadPath(file) {
  return (file.webkitRelativePath || file.name).replace(/\\/g, '/').replace(/^\/+/, '');
}

function archiveStem(path) {
  const name = (path || '').split('/').pop();
  const stem = name.replace(/\.(tar\.gz|tgz|zip)$/i, '');
  return !stem || stem === '.' || stem === '..' ? 'extracted' : stem;
}

function parentPath(path) {
  const clean = (path || '').replace(/\/+$/, '');
  const idx = clean.lastIndexOf('/');
  return idx <= 0 ? '' : clean.slice(0, idx);
}

function downloadURL(root, path) {
  return api.downloadURL(`/files/download${qs({ root, path })}`);
}

function isImage(entry) {
  return /\.(png|jpe?g|gif|webp|bmp|ico)$/i.test(entry.name || '');
}

function readPref(key, fallback) {
  try {
    const raw = localStorage.getItem(key);
    return raw === null ? fallback : JSON.parse(raw);
  } catch {
    return fallback;
  }
}

function writePref(key, value) {
  try {
    localStorage.setItem(key, JSON.stringify(value));
  } catch { /* storage is optional */ }
}

export async function render(ctx) {
  const { outlet, setCleanup } = ctx;
  const page = h('div', { class: 'page fm-page' });
  outlet.replaceChildren(page);

  let roots = [];
  let root = '';
  let path = '';
  let entries = [];
  let searchMode = false;
  let view = readPref(VIEW_KEY, 'list');
  if (view !== 'grid') view = 'list';
  let sort = readPref(SORT_KEY, { key: 'name', dir: 'asc' });
  if (!sort || !sort.key) sort = { key: 'name', dir: 'asc' };
  const selected = new Set();
  let anchor = null;
  let focused = null;

  const listing = h('div', { class: 'fm-listing', tabindex: '0' });
  const dropHint = h('div', { class: 'drop-hint' }, iconSpan('upload', 22), h('span', {}, t('files.dropFiles')));
  const dropZone = h('div', { class: 'dropzone fm-drop' }, listing, dropHint);
  const inspector = h('aside', { class: 'fm-inspector' });
  const selectBar = h('div', { class: 'fm-selectbar', hidden: true });
  const main = h('div', { class: 'fm-main' }, dropZone, selectBar);
  const bodyBox = h('div', { class: 'fm-body' }, main, inspector);

  const crumbBox = h('div', { class: 'crumbs fm-crumbs' });
  const searchInput = input({ class: 'input search-input', placeholder: t('files.searchPlaceholder'), type: 'search' });
  const rootSelect = select([], { class: 'select fm-root' });
  const uploadInput = h('input', { type: 'file', multiple: true, hidden: true });
  const folderInput = h('input', { type: 'file', multiple: true, webkitdirectory: true, hidden: true });

  const viewToggle = h('div', { class: 'fm-segment' },
    h('button', { class: 'fm-seg', type: 'button', dataset: { view: 'list' }, onClick: () => setView('list') },
      iconSpan('list', 15), h('span', {}, t('files.viewList'))),
    h('button', { class: 'fm-seg', type: 'button', dataset: { view: 'grid' }, onClick: () => setView('grid') },
      iconSpan('layers', 15), h('span', {}, t('files.viewGrid'))));

  const sortButton = h('button', { class: 'fm-sort', type: 'button', onClick: openSortMenu },
    iconSpan('filter', 14), h('span', { class: 'fm-sort-label' }), iconSpan('chevronDown', 14));

  const toolbar = h('div', { class: 'fm-toolbar' },
    h('div', { class: 'fm-bar' },
      rootSelect,
      iconButton('arrowUpCircle', t('files.parent'), () => loadDir(parentPath(path)), 'fm-up'),
      crumbBox,
      h('div', { class: 'grow' }),
      searchInput),
    h('div', { class: 'fm-bar fm-bar-sub' },
      viewToggle,
      sortButton,
      h('div', { class: 'grow' }),
      canWrite() ? button(t('files.upload'), { icon: 'upload', onClick: () => uploadInput.click() }) : null,
      canWrite() ? button(t('files.uploadFolder'), { icon: 'folderUpload', onClick: () => folderInput.click() }) : null,
      canWrite() ? button(t('files.newFolder'), { icon: 'folderPlus', onClick: newFolder }) : null,
      canWrite() ? button(t('files.newFile'), { icon: 'filePlus', onClick: newFile }) : null,
      button(t('common.refresh'), { icon: 'refresh', onClick: () => loadDir(path) })));

  page.append(
    pageHeader(t('files.title'), t('files.subtitle')),
    card({ body: toolbar, flush: true }),
    h('div', { class: 'mt3' }, bodyBox),
    uploadInput,
    folderInput,
  );

  /* ---------------- selection ---------------- */

  function entryByPath(target) {
    return entries.find((item) => item.path === target) || null;
  }

  function visibleEntries() {
    const dir = sort.dir === 'desc' ? -1 : 1;
    const byName = (a, b) => a.name.localeCompare(b.name, undefined, { numeric: true, sensitivity: 'base' });
    return entries.slice().sort((a, b) => {
      if (a.is_dir !== b.is_dir) return a.is_dir ? -1 : 1;
      let result = 0;
      if (sort.key === 'size') result = a.size - b.size;
      else if (sort.key === 'modified') result = a.mtime - b.mtime;
      else if (sort.key === 'type') result = fileKind(a).localeCompare(fileKind(b));
      else result = byName(a, b);
      if (result === 0) result = byName(a, b);
      return result * dir;
    });
  }

  function selectedPaths() {
    return visibleEntries().map((item) => item.path).filter((p) => selected.has(p));
  }

  function selectOnly(entry) {
    selected.clear();
    if (entry) selected.add(entry.path);
    anchor = entry ? entry.path : null;
    focused = entry ? entry.path : null;
    refreshSelectionState();
  }

  function toggleSelect(entry) {
    if (!entry) return;
    if (selected.has(entry.path)) selected.delete(entry.path);
    else selected.add(entry.path);
    anchor = entry.path;
    focused = entry.path;
    refreshSelectionState();
  }

  function selectRange(entry) {
    const list = visibleEntries();
    const from = list.findIndex((item) => item.path === anchor);
    const to = list.findIndex((item) => item.path === entry.path);
    if (from < 0 || to < 0) {
      selectOnly(entry);
      return;
    }
    const [low, high] = from < to ? [from, to] : [to, from];
    for (let i = low; i <= high; i += 1) selected.add(list[i].path);
    focused = entry.path;
    refreshSelectionState();
  }

  function selectAll() {
    selected.clear();
    for (const entry of visibleEntries()) selected.add(entry.path);
    refreshSelectionState();
  }

  function clearSelection() {
    selected.clear();
    focused = null;
    anchor = null;
    refreshSelectionState();
  }

  function applySelection(entry, ev) {
    if (!entry) return;
    if (ev.shiftKey && anchor) selectRange(entry);
    else if (ev.ctrlKey || ev.metaKey) toggleSelect(entry);
    else selectOnly(entry);
  }

  /* ---------------- painting ---------------- */

  function paint() {
    paintToolbarState();
    paintListing();
    paintInspector();
    paintSelectBar();
  }

  // Selection changes only touch the affected rows, so the listing keeps its
  // scroll position and the rows stay clickable while a range is picked.
  function refreshSelectionState() {
    for (const el of listing.querySelectorAll('[data-path]')) {
      const isSelected = selected.has(el.dataset.path);
      el.classList.toggle('is-selected', isSelected);
      el.classList.toggle('is-focused', focused === el.dataset.path);
      const box = el.querySelector('.fm-box');
      if (box) box.classList.toggle('is-on', isSelected);
    }
    const list = visibleEntries();
    const head = listing.querySelector('.fm-table thead .fm-box');
    if (head) head.classList.toggle('is-on', list.length > 0 && list.every((entry) => selected.has(entry.path)));
    paintInspector();
    paintSelectBar();
  }

  function paintToolbarState() {
    for (const seg of viewToggle.querySelectorAll('.fm-seg')) {
      seg.classList.toggle('is-on', seg.dataset.view === view);
    }
    const label = sortButton.querySelector('.fm-sort-label');
    label.textContent = `${t('files.sortBy')}: ${t(sortLabelKey(sort.key))} ${sort.dir === 'asc' ? '↑' : '↓'}`;
  }

  function paintListing() {
    const list = visibleEntries();
    if (!list.length) {
      listing.replaceChildren(emptyState(
        searchMode ? t('files.noResults') : t('files.emptyFolder'),
        searchMode ? '' : t('files.emptyFolderHint'),
        canWrite() && !searchMode ? h('div', { class: 'fm-empty-actions' },
          button(t('files.upload'), { icon: 'upload', onClick: () => uploadInput.click() }),
          button(t('files.uploadFolder'), { icon: 'folderUpload', onClick: () => folderInput.click() })) : null,
        searchMode ? 'search' : 'folder'));
      return;
    }
    listing.replaceChildren(...(view === 'grid' ? gridView(list) : listView(list)),
      h('div', { class: 'fm-foot' }, t('files.folderHint')));
    scrollFocusedIntoView();
  }

  function listView(list) {
    const allSelected = list.length > 0 && list.every((entry) => selected.has(entry.path));
    const head = h('thead', {}, h('tr', {},
      h('th', { class: 'fm-check' }, h('span', {
        class: `fm-box${allSelected ? ' is-on' : ''}`,
        title: t('files.selectAll'),
        onClick: () => (allSelected ? clearSelection() : selectAll()),
      })),
      sortHeader(t('files.name'), 'name'),
      sortHeader(t('files.size'), 'size'),
      sortHeader(t('files.permissions'), 'perm'),
      sortHeader(t('files.modified'), 'modified'),
      h('th', { class: 'fm-col-actions' })));
    const rows = list.map((entry) => {
      const kind = fileKind(entry);
      const isSelected = selected.has(entry.path);
      return h('tr', {
        class: `fm-row${isSelected ? ' is-selected' : ''}${focused === entry.path ? ' is-focused' : ''}`,
        dataset: { path: entry.path },
      },
        h('td', { class: 'fm-check' }, h('span', { class: `fm-box${isSelected ? ' is-on' : ''}` })),
        h('td', { class: 'fm-name-cell' },
          h('span', { class: `fm-glyph ${kind}` }, iconSpan(ICONS[kind] || 'file', 17)),
          h('span', { class: 'fm-name', title: entry.name }, entry.name),
          entry.is_link ? h('span', { class: 'badge badge-gray', title: t('files.symlink') }, 'link') : null),
        h('td', { class: 'fm-cell mono' }, entry.is_dir ? '—' : formatBytes(entry.size)),
        h('td', { class: 'fm-cell mono' }, entry.mode_oct || entry.mode || '—'),
        h('td', { class: 'fm-cell mono' }, formatDate(entry.mtime)),
        h('td', { class: 'fm-row-actions' }, h('div', { class: 'row-actions' }, rowActions(entry))));
    });
    return [h('div', { class: 'fm-scroll' }, h('table', { class: 'fm-table' }, head, h('tbody', {}, rows)))];
  }

  function gridView(list) {
    return [h('div', { class: 'fm-grid' }, list.map((entry) => {
      const kind = fileKind(entry);
      const isSelected = selected.has(entry.path);
      return h('button', {
        class: `fm-card${isSelected ? ' is-selected' : ''}${focused === entry.path ? ' is-focused' : ''}`,
        type: 'button',
        dataset: { path: entry.path },
      },
        h('span', { class: `fm-card-glyph ${kind}` }, iconSpan(ICONS[kind] || 'file', 22)),
        h('span', { class: 'fm-card-name' }, entry.name),
        h('span', { class: 'fm-card-meta mono' }, entry.is_dir ? t('files.folder') : formatBytes(entry.size)));
    }))];
  }

  function sortHeader(label, key) {
    const active = sort.key === key;
    return h('th', {
      class: `fm-sortable${active ? ' is-on' : ''}`,
      onClick: () => setSort(key),
    }, h('span', {}, label), active ? h('span', { class: 'fm-caret' }, sort.dir === 'asc' ? '↑' : '↓') : null);
  }

  function rowActions(entry) {
    const actions = [];
    if (entry.is_dir) {
      actions.push(iconButton('folderOpen', t('common.open'), () => loadDir(entry.path)));
      actions.push(iconButton('download', t('files.download'), () => triggerDownload(entry)));
    } else {
      actions.push(iconButton('eye', t('files.view'), () => openEntry(entry)));
      actions.push(iconButton('download', t('files.download'), () => triggerDownload(entry)));
    }
    if (!canWrite()) return actions;
    actions.push(iconButton('edit', t('files.rename'), () => renameEntry(entry)));
    actions.push(iconButton('trash', t('common.delete'), () => deleteEntry(entry), 'danger'));
    return actions;
  }

  function paintInspector() {
    const entry = focused ? entryByPath(focused) : null;
    if (!entry) {
      const total = entries.reduce((sum, item) => sum + (item.is_dir ? 0 : item.size), 0);
      inspector.replaceChildren(h('div', { class: 'fm-insp' },
        h('div', { class: 'fm-insp-kicker' }, t('files.contents')),
        h('div', { class: 'fm-insp-name' }, rootLabel()),
        h('div', { class: 'fm-insp-path mono' }, `${rootLabel()}/${path || ''}`),
        h('dl', { class: 'fm-meta' },
          metaRow(t('files.items'), String(entries.length)),
          metaRow(t('files.totalSize'), formatBytes(total)))));
      return;
    }
    const kind = fileKind(entry);
    const many = selected.size > 1;
    inspector.replaceChildren(h('div', { class: 'fm-insp' },
      h('div', { class: `fm-insp-glyph ${kind}` }, iconSpan(ICONS[kind] || 'file', 26)),
      h('div', { class: 'fm-insp-name' }, entry.name),
      h('div', { class: 'fm-insp-path mono' }, `${rootLabel()}/${entry.path}`),
      many ? h('div', { class: 'fm-insp-note' }, t('files.selectedCount', { count: selected.size })) : null,
      h('dl', { class: 'fm-meta' },
        metaRow(t('files.size'), entry.is_dir ? '—' : formatBytes(entry.size)),
        metaRow(t('files.permissions'), entry.mode_oct || entry.mode || '—'),
        metaRow(t('files.modified'), formatDate(entry.mtime)),
        metaRow(t('files.type'), entry.mime || kindLabel(entry))),
      h('div', { class: 'fm-insp-actions' }, inspectorActions(entry))));
  }

  function metaRow(label, value) {
    return h('div', {}, h('dt', {}, label), h('dd', {}, value));
  }

  function inspectorActions(entry) {
    const actions = [];
    if (entry.is_dir) {
      actions.push(button(t('common.open'), { icon: 'folderOpen', onClick: () => loadDir(entry.path) }));
      actions.push(button(t('files.download'), { icon: 'download', onClick: () => triggerDownload(entry) }));
    } else {
      actions.push(button(t('files.view'), { icon: 'eye', onClick: () => openEntry(entry) }));
      actions.push(button(t('files.download'), { icon: 'download', onClick: () => triggerDownload(entry) }));
    }
    if (!canWrite()) return actions;
    actions.push(button(t('files.rename'), { icon: 'edit', onClick: () => renameEntry(entry) }));
    actions.push(button(t('files.chmod'), { icon: 'shield', onClick: () => chmodEntry(entry) }));
    return actions;
  }

  function paintSelectBar() {
    const paths = selectedPaths();
    selectBar.hidden = paths.length === 0;
    if (!paths.length) return;
    const total = paths.reduce((sum, p) => {
      const entry = entryByPath(p);
      return sum + (entry && !entry.is_dir ? entry.size : 0);
    }, 0);
    selectBar.replaceChildren(
      h('div', { class: 'fm-sel-info' },
        h('span', { class: 'fm-sel-count' }, t('files.selectedCount', { count: paths.length })),
        h('span', { class: 'fm-sel-size mono' }, formatBytes(total))),
      h('div', { class: 'grow' }),
      button(t('files.downloadZip'), { icon: 'archive', onClick: downloadSelection }),
      canWrite() ? button(t('files.move'), { icon: 'move', onClick: () => bulkTransfer('move') }) : null,
      canWrite() ? button(t('files.copy'), { icon: 'copy', onClick: () => bulkTransfer('copy') }) : null,
      canWrite() ? button(t('files.chmod'), { icon: 'shield', onClick: bulkChmod }) : null,
      canWrite() ? button(t('common.delete'), { icon: 'trash', onClick: bulkDelete }) : null,
      button(t('files.clearSelection'), { onClick: clearSelection }));
  }

  function kindLabel(entry) {
    const kind = fileKind(entry);
    if (kind === 'dir') return t('files.folder');
    if (kind === 'archive') return t('files.typeArchive');
    if (kind === 'img') return t('files.typeImage');
    if (kind === 'code' || kind === 'php') return t('files.typeCode');
    return t('files.typeFile');
  }

  function scrollFocusedIntoView() {
    if (!focused) return;
    for (const el of listing.querySelectorAll('[data-path]')) {
      if (el.dataset.path === focused) {
        el.scrollIntoView({ block: 'nearest' });
        break;
      }
    }
  }

  /* ---------------- view state ---------------- */

  function setView(next) {
    view = next === 'grid' ? 'grid' : 'list';
    writePref(VIEW_KEY, view);
    paint();
  }

  function setSort(key) {
    if (sort.key === key) sort.dir = sort.dir === 'asc' ? 'desc' : 'asc';
    else sort = { key, dir: 'asc' };
    writePref(SORT_KEY, sort);
    paint();
  }

  function sortLabelKey(key) {
    if (key === 'size') return 'files.sortSize';
    if (key === 'modified') return 'files.sortModified';
    if (key === 'type') return 'files.sortType';
    return 'files.sortName';
  }

  function openSortMenu(ev) {
    const rect = sortButton.getBoundingClientRect();
    const options = ['name', 'size', 'modified', 'type'].map((key) => ({
      label: `${t(sortLabelKey(key))} ${sort.key === key ? (sort.dir === 'asc' ? '↑' : '↓') : ''}`.trim(),
      icon: key === 'name' ? 'filetext' : key === 'size' ? 'database' : key === 'modified' ? 'calendar' : 'layers',
      onClick: () => setSort(key),
    }));
    openMenu = openContextMenu(options, { x: rect.left, y: rect.bottom + 4 });
    ev.stopPropagation();
  }

  /* ---------------- navigation ---------------- */

  async function loadRoots() {
    try {
      roots = await api.get('/files/roots');
    } catch (err) {
      listing.replaceChildren(card({ body: h('div', { class: 'alert alert-danger' }, err.message) }));
      return false;
    }
    if (!roots || !roots.length) {
      listing.replaceChildren(card({ body: emptyState(t('files.noRoots'), '', null, 'folder') }));
      return false;
    }
    rootSelect.replaceChildren(...roots.map((r) => h('option', { value: r.token }, r.label || r.path)));
    root = roots[0].token;
    return true;
  }

  function paintCrumbs() {
    const parts = (path || '').split('/').filter(Boolean);
    const items = [h('button', {
      class: 'crumb', type: 'button', onClick: () => loadDir(''),
    }, iconSpan('home', 14), rootLabel())];
    let acc = '';
    for (const part of parts) {
      acc = acc ? `${acc}/${part}` : part;
      const target = acc;
      items.push(h('span', { class: 'crumb-sep' }, '/'));
      items.push(h('button', { class: 'crumb', type: 'button', onClick: () => loadDir(target) }, part));
    }
    crumbBox.replaceChildren(...items);
  }

  function rootLabel() {
    const found = roots.find((r) => r.token === root);
    return found ? (found.label || found.path) : t('files.root');
  }

  async function loadDir(next) {
    path = next || '';
    searchMode = false;
    selected.clear();
    focused = null;
    anchor = null;
    paintCrumbs();
    listing.replaceChildren(loadingBlock(t('files.loading')));
    try {
      const data = await api.get(`/files${qs({ root, path })}`);
      entries = data.entries || [];
      paint();
    } catch (err) {
      listing.replaceChildren(card({ body: h('div', { class: 'alert alert-danger' }, err.message) }));
    }
  }

  async function runSearch(q) {
    searchMode = true;
    selected.clear();
    focused = null;
    listing.replaceChildren(loadingBlock(t('common.loading')));
    try {
      const data = await api.get(`/files/search${qs({ root, path, q })}`);
      entries = data.entries || [];
      paint();
    } catch (err) {
      listing.replaceChildren(card({ body: h('div', { class: 'alert alert-danger' }, err.message) }));
    }
  }

  /* ---------------- events ---------------- */

  uploadInput.addEventListener('change', () => {
    const files = uploadInput.files ? Array.from(uploadInput.files) : [];
    uploadInput.value = '';
    if (files.length) uploadFiles(files.map((file) => ({ file, rel: file.name })), t('files.uploadFiles'));
  });

  folderInput.addEventListener('change', () => {
    const files = folderInput.files ? Array.from(folderInput.files) : [];
    folderInput.value = '';
    if (!files.length) return;
    const items = files.map((file) => ({ file, rel: relativeUploadPath(file) }));
    uploadFiles(items, t('files.uploadFolder'));
  });

  rootSelect.addEventListener('change', () => {
    root = rootSelect.value;
    path = '';
    searchMode = false;
    searchInput.value = '';
    loadDir('');
  });

  let searchTimer = null;
  searchInput.addEventListener('input', () => {
    if (searchTimer) clearTimeout(searchTimer);
    searchTimer = setTimeout(() => {
      const q = searchInput.value.trim();
      if (q) runSearch(q);
      else { searchMode = false; loadDir(path); }
    }, 350);
  });

  listing.addEventListener('click', (ev) => {
    const node = ev.target.closest('[data-path]');
    if (!node) {
      if (!ev.target.closest('.fm-box')) clearSelection();
      return;
    }
    const entry = entryByPath(node.dataset.path);
    if (!entry) return;
    if (ev.target.closest('.fm-row-actions')) return;
    if (ev.target.closest('.fm-box')) toggleSelect(entry);
    else applySelection(entry, ev);
    listing.focus({ preventScroll: true });
  });

  listing.addEventListener('dblclick', (ev) => {
    const node = ev.target.closest('[data-path]');
    if (!node || ev.target.closest('.fm-row-actions')) return;
    const entry = entryByPath(node.dataset.path);
    if (entry) openEntry(entry);
  });

  listing.addEventListener('contextmenu', (ev) => {
    const node = ev.target.closest('[data-path]');
    if (!node) return;
    ev.preventDefault();
    const entry = entryByPath(node.dataset.path);
    if (!entry) return;
    if (!selected.has(entry.path)) selectOnly(entry);
    openMenu = openContextMenu(contextItems(entry), { x: ev.clientX, y: ev.clientY });
  });

  listing.addEventListener('keydown', (ev) => {
    const list = visibleEntries();
    if (ev.key === 'Escape') {
      clearSelection();
      return;
    }
    if ((ev.ctrlKey || ev.metaKey) && ev.key.toLowerCase() === 'a') {
      ev.preventDefault();
      selectAll();
      return;
    }
    if (!list.length) return;
    const index = focused ? list.findIndex((item) => item.path === focused) : -1;
    if (ev.key === 'ArrowDown' || ev.key === 'ArrowUp') {
      ev.preventDefault();
      const next = ev.key === 'ArrowDown'
        ? Math.min(list.length - 1, index + 1)
        : Math.max(0, index === -1 ? 0 : index - 1);
      selectOnly(list[next]);
      return;
    }
    const entry = focused ? entryByPath(focused) : null;
    if (!entry) return;
    if (ev.key === 'Enter') {
      ev.preventDefault();
      openEntry(entry);
    } else if (ev.key === 'F2') {
      ev.preventDefault();
      renameEntry(entry);
    } else if (ev.key === 'Delete') {
      ev.preventDefault();
      deleteEntry(entry);
    } else if (ev.key === 'Backspace' && path) {
      ev.preventDefault();
      loadDir(parentPath(path));
    }
  });

  // Drag and drop uploads files and folders dropped on the listing.
  const hasFileDrag = (ev) => Boolean(ev.dataTransfer && Array.from(ev.dataTransfer.types || []).includes('Files'));
  let dragDepth = 0;
  dropZone.addEventListener('dragenter', (ev) => {
    if (!hasFileDrag(ev)) return;
    ev.preventDefault();
    dragDepth += 1;
    dropZone.classList.add('dragover');
  });
  dropZone.addEventListener('dragover', (ev) => {
    if (!hasFileDrag(ev)) return;
    ev.preventDefault();
    ev.dataTransfer.dropEffect = 'copy';
  });
  dropZone.addEventListener('dragleave', () => {
    dragDepth = Math.max(0, dragDepth - 1);
    if (!dragDepth) dropZone.classList.remove('dragover');
  });
  dropZone.addEventListener('drop', async (ev) => {
    ev.preventDefault();
    dragDepth = 0;
    dropZone.classList.remove('dragover');
    if (!canWrite()) {
      toast(t('files.readOnly'), 'warn');
      return;
    }
    const dropped = await droppedFiles(ev.dataTransfer);
    if (!dropped.length) return;
    uploadFiles(dropped, t('files.uploadFiles'));
  });

  // Without this the browser opens the dropped file in a new tab.
  const blockBrowserDrop = (ev) => {
    if (hasFileDrag(ev)) ev.preventDefault();
  };
  document.addEventListener('dragover', blockBrowserDrop);
  document.addEventListener('drop', blockBrowserDrop);
  let openMenu = null;
  if (setCleanup) {
    setCleanup(() => {
      document.removeEventListener('dragover', blockBrowserDrop);
      document.removeEventListener('drop', blockBrowserDrop);
      if (openMenu) openMenu.close();
    });
  }

  /* ---------------- actions ---------------- */

  function contextItems(entry) {
    const items = [];
    if (entry.is_dir) {
      items.push({ label: t('common.open'), icon: 'folderOpen', onClick: () => loadDir(entry.path) });
      items.push({ label: t('files.download'), icon: 'download', onClick: () => triggerDownload(entry) });
    } else {
      items.push({ label: t('files.view'), icon: 'eye', onClick: () => openEntry(entry) });
      items.push({ label: t('files.download'), icon: 'download', onClick: () => triggerDownload(entry) });
    }
    if (selected.size > 1) {
      items.push({ separator: true });
      items.push({ label: t('files.downloadZip'), icon: 'archive', onClick: downloadSelection });
    }
    if (!canWrite()) return items;
    items.push({ separator: true });
    items.push({ label: t('files.rename'), icon: 'edit', onClick: () => renameEntry(entry) });
    items.push({ label: t('files.copy'), icon: 'copy', onClick: () => transferEntry(entry, 'copy') });
    items.push({ label: t('files.move'), icon: 'move', onClick: () => transferEntry(entry, 'move') });
    items.push({ label: t('files.chmod'), icon: 'shield', onClick: () => chmodEntry(entry) });
    items.push({ label: t('files.archive'), icon: 'archive', onClick: () => archiveEntry(entry) });
    if (!entry.is_dir && /\.(zip|tar\.gz|tgz)$/i.test(entry.name)) {
      items.push({ label: t('files.extract'), icon: 'packageOpen', onClick: () => extractEntry(entry) });
    }
    items.push({ separator: true });
    items.push({ label: t('common.delete'), icon: 'trash', danger: true, onClick: () => deleteEntry(entry) });
    return items;
  }

  function triggerDownload(entry) {
    const a = h('a', { href: downloadURL(root, entry.path), download: entry.name });
    document.body.append(a);
    a.click();
    a.remove();
  }

  function downloadSelection() {
    const paths = selectedPaths();
    if (!paths.length) return;
    const url = api.downloadURL(`/files/download${qs({ root, paths: paths.join(',') })}`);
    const a = h('a', { href: url, download: '' });
    document.body.append(a);
    a.click();
    a.remove();
  }

  function openEntry(entry) {
    if (entry.is_dir) {
      loadDir(entry.path);
      return;
    }
    if (isImage(entry)) {
      openPreview(entry);
      return;
    }
    openEditor(entry);
  }

  function openPreview(entry) {
    const src = api.downloadURL(`/files/preview${qs({ root, path: entry.path })}`);
    const modal = openModal({
      title: entry.name,
      size: 'modal-lg',
      body: h('div', { class: 'preview-box' },
        h('img', { src, alt: entry.name, class: 'preview-img' })),
      footer: [
        button(t('files.download'), { icon: 'download', onClick: () => triggerDownload(entry) }),
        button(t('common.close'), { onClick: () => modal.close() }),
      ],
    });
  }

  async function openEditor(entry) {
    let data;
    try {
      data = await api.get(`/files/content${qs({ root, path: entry.path })}`);
    } catch (err) {
      if (err.code === 'files.binary' || err.code === 'files.too_large') {
        toast(err.message, 'warn');
        return;
      }
      toastError(err);
      return;
    }

    let cm = null;
    let fallback = null;

    const container = h('div', { class: 'editor-container' }, loadingBlock(t('common.loading')));
    const status = h('div', { class: 'muted small mb1' }, `${formatBytes((data.content || '').length)} · ${formatDate(data.mtime)}`);
    let mtime = data.mtime || 0;

    const saveBtn = canWrite() ? button(t('files.save'), {
      variant: 'primary', icon: 'save',
      onClick: () => save(),
    }) : null;
    if (saveBtn) saveBtn.disabled = true; // enabled once the editor is ready

    const modal = openModal({
      title: `${entry.name}`,
      size: 'modal-xl',
      body: h('div', {},
        h('div', { class: 'editor-bar' },
          h('span', { class: 'mono small' }, data.path || entry.path),
          h('span', { class: 'grow' }),
          status),
        container),
      footer: [
        button(t('common.close'), { onClick: () => modal.close() }),
        canWrite() ? button(t('files.download'), { icon: 'download', onClick: () => triggerDownload(entry) }) : null,
        saveBtn,
      ],
    });

    // CodeMirror is loaded lazily on the first edit, then kept for the session.
    loadCodeMirrorCore().then(async (CodeMirror) => {
      const lang = detectLang(entry.name);
      await loadLang(lang);
      if (!document.body.contains(container)) return;
      container.replaceChildren();
      cm = CodeMirror(container, {
        value: data.content || '',
        mode: lang ? lang.mode : null,
        theme: isDark() ? 'material-darker' : 'default',
        lineNumbers: true,
        lineWrapping: true,
        indentUnit: 4,
        tabSize: 4,
        indentWithTabs: false,
        matchBrackets: true,
        autoCloseBrackets: true,
        styleActiveLine: true,
        viewportMargin: 40,
      });
      cm.setSize('100%', '56vh');
      cm.setCursor(0, 0);
      setTimeout(() => cm.refresh(), 0);
      if (saveBtn) saveBtn.disabled = false;
    }).catch((err) => {
      toastError(err);
      fallback = textarea({
        class: 'textarea code-editor',
        rows: '24',
        spellcheck: 'false',
        value: data.content || '',
      });
      container.replaceChildren(fallback);
      if (saveBtn) saveBtn.disabled = false;
    });

    async function save(force = false) {
      const value = cm ? cm.getValue() : (fallback ? fallback.value : null);
      if (value === null) return;
      try {
        const res = await api.put('/files/content', {
          root, path: entry.path, content: value, mtime: force ? 0 : mtime,
        });
        mtime = res.mtime || mtime;
        toast(t('files.saved'), 'success');
        modal.close();
        await loadDir(path);
      } catch (err) {
        if (err.code === 'files.conflict' && !force) {
          const ok = await confirmDialog({
            title: t('files.save'),
            message: err.message,
            confirmText: t('common.confirm'),
            danger: true,
          });
          if (ok) await save(true);
          return;
        }
        toastError(err);
      }
    }
  }

  async function newFolder() {
    const name = await promptDialog({
      title: t('files.mkdirTitle'), label: t('files.name'), placeholder: 'assets', confirmText: t('common.create'),
    });
    if (!name) return;
    try {
      await api.post('/files/mkdir', { root, path: joinPath(path, name) });
      toast(t('common.done'), 'success');
      await loadDir(path);
    } catch (err) {
      toastError(err);
    }
  }

  async function newFile() {
    const name = await promptDialog({
      title: t('files.newFileTitle'), label: t('files.name'), placeholder: 'index.php', confirmText: t('common.create'),
    });
    if (!name) return;
    try {
      await api.post('/files/create', { root, path: joinPath(path, name) });
      await loadDir(path);
      const created = entries.find((e) => e.name === name);
      if (created) openEditor(created);
    } catch (err) {
      toastError(err);
    }
  }

  async function renameEntry(entry) {
    const name = await promptDialog({
      title: t('files.renameTitle'), label: t('files.newName'), value: entry.name, confirmText: t('files.rename'),
    });
    if (!name || name === entry.name) return;
    try {
      await api.post('/files/rename', { root, from: entry.path, to: joinPath(parentPath(entry.path), name) });
      toast(t('files.renamed'), 'success');
      await loadDir(path);
    } catch (err) {
      toastError(err);
    }
  }

  async function transferEntry(entry, mode) {
    const dest = await promptDialog({
      title: mode === 'copy' ? t('files.copyTitle') : t('files.moveTitle'),
      label: t('files.destination'),
      hint: mode === 'copy' ? t('files.copyHint') : t('files.moveHint'),
      value: entry.path,
      confirmText: mode === 'copy' ? t('files.copy') : t('files.move'),
    });
    if (!dest || dest === entry.path) return;
    try {
      await api.post(`/files/${mode}`, { root, from: entry.path, dest });
      toast(mode === 'copy' ? t('files.copied') : t('files.moved'), 'success');
      await loadDir(path);
    } catch (err) {
      toastError(err);
    }
  }

  async function bulkTransfer(mode) {
    const paths = selectedPaths();
    if (!paths.length) return;
    const dest = await promptDialog({
      title: mode === 'copy' ? t('files.copyTitle') : t('files.moveTitle'),
      label: t('files.destination'),
      hint: mode === 'copy' ? t('files.copyHint') : t('files.moveHint'),
      value: parentPath(path),
      confirmText: mode === 'copy' ? t('files.copy') : t('files.move'),
    });
    if (!dest) return;
    let done = 0;
    for (const from of paths) {
      try {
        await api.post(`/files/${mode}`, { root, from, dest });
        done += 1;
      } catch (err) {
        toastError(err);
      }
    }
    toast(mode === 'copy' ? t('files.copied') : t('files.moved'), 'success');
    clearSelection();
    await loadDir(path);
    if (done === 0) return;
  }

  async function chmodEntry(entry) {
    const mode = await promptDialog({
      title: t('files.chmodTitle'), label: t('files.chmod'), hint: t('files.chmodHint'),
      value: entry.mode_oct || '644', confirmText: t('common.apply'),
    });
    if (!mode) return;
    try {
      await api.post('/files/chmod', { root, path: entry.path, mode });
      toast(t('files.chmodDone'), 'success');
      await loadDir(path);
    } catch (err) {
      toastError(err);
    }
  }

  async function bulkChmod() {
    const paths = selectedPaths();
    if (!paths.length) return;
    const mode = await promptDialog({
      title: t('files.chmodTitle'), label: t('files.chmod'), hint: t('files.chmodHint'),
      value: '644', confirmText: t('common.apply'),
    });
    if (!mode) return;
    for (const target of paths) {
      try {
        await api.post('/files/chmod', { root, path: target, mode });
      } catch (err) {
        toastError(err);
      }
    }
    toast(t('files.chmodDone'), 'success');
    await loadDir(path);
  }

  async function archiveEntry(entry) {
    const format = select([{ value: 'zip', label: 'ZIP' }, { value: 'tar.gz', label: 'TAR.GZ' }], { value: 'zip' });
    const modal = openModal({
      title: t('files.archiveTitle'),
      size: 'modal-sm',
      body: [h('div', { class: 'muted small mb2 mono' }, entry.path), field(t('files.format'), format)],
      footer: [
        button(t('common.cancel'), { onClick: () => modal.close() }),
        button(t('files.archive'), {
          variant: 'primary',
          onClick: async () => {
            try {
              await api.post('/files/archive', { root, path: entry.path, format: format.value });
              modal.close();
              toast(t('files.archived'), 'success');
              await loadDir(path);
            } catch (err) {
              toastError(err);
            }
          },
        }),
      ],
    });
  }

  async function extractEntry(entry) {
    const dest = await promptDialog({
      title: t('files.extractTitle'), label: t('files.destination'), hint: t('files.destHint'),
      value: joinPath(parentPath(entry.path), archiveStem(entry.path)), confirmText: t('files.extract'),
    });
    if (dest === null) return;
    try {
      await api.post('/files/extract', { root, path: entry.path, dest });
      toast(t('files.extracted'), 'success');
      await loadDir(path);
    } catch (err) {
      toastError(err);
    }
  }

  async function deleteEntry(entry) {
    const ok = await confirmDialog({
      title: t('common.delete'),
      message: t('files.deleteTitle', { name: entry.name }),
      note: t('files.deleteConfirm'),
      confirmText: t('common.delete'),
      danger: true,
    });
    if (!ok) return;
    try {
      await api.post('/files/delete', { root, path: entry.path });
      toast(t('files.deleted'), 'success');
      await loadDir(path);
    } catch (err) {
      toastError(err);
    }
  }

  async function bulkDelete() {
    const paths = selectedPaths();
    if (!paths.length) return;
    const names = paths.map((p) => p.split('/').pop());
    const preview = names.slice(0, 3).join(', ') + (names.length > 3 ? '…' : '');
    const ok = await confirmDialog({
      title: t('common.delete'),
      message: t('files.deleteSelected', { count: paths.length, names: preview }),
      note: t('files.deleteConfirm'),
      confirmText: t('common.delete'),
      danger: true,
    });
    if (!ok) return;
    for (const target of paths) {
      try {
        await api.post('/files/delete', { root, path: target });
      } catch (err) {
        toastError(err);
      }
    }
    toast(t('files.deleted'), 'success');
    clearSelection();
    await loadDir(path);
  }

  /* ---------------- uploads ---------------- */

  function uploadProgress(title, items) {
    const bar = h('div', { class: 'progress-inline big' }, h('span', { style: { width: '0%' } }));
    const label = h('div', { class: 'muted small' }, t('files.uploading', { pct: 0 }));
    const shown = items.slice(0, 12);
    const list = h('div', { class: 'mb2' },
      shown.map((item) => h('div', { class: 'mono small ellipsis' }, item.rel)),
      items.length > shown.length ? h('div', { class: 'muted small' }, t('files.moreItems', { count: items.length - shown.length })) : null);
    const modal = openModal({
      title,
      size: 'modal-sm',
      body: h('div', {}, list, bar, label),
    });
    return {
      update(index, pct) {
        const overall = Math.round(((index + pct / 100) / items.length) * 100);
        bar.firstChild.style.width = `${overall}%`;
        label.textContent = t('files.uploading', { pct: overall });
      },
      close() { modal.close(); },
    };
  }

  async function uploadItem(item, overwrite, onProgress) {
    const form = new FormData();
    form.append('root', root);
    form.append('path', path);
    form.append('overwrite', overwrite ? 'true' : 'false');
    form.append('rel', item.rel);
    form.append('file', item.file, item.file.name);
    await api.upload('/files/upload', form, onProgress);
  }

  function conflictDialog(count) {
    return new Promise((resolve) => {
      let settled = false;
      const finish = (value) => {
        if (settled) return;
        settled = true;
        resolve(value);
      };
      const modal = openModal({
        title: t('files.conflictTitle'),
        size: 'modal-sm',
        body: h('div', { style: { fontSize: '13px' } }, t('files.conflictMessage', { count })),
        footer: [
          h('button', { class: 'btn', type: 'button', onClick: () => { finish('skip'); modal.close(); } }, t('files.skip')),
          h('button', { class: 'btn btn-primary', type: 'button', onClick: () => { finish('replace'); modal.close(); } }, t('files.replace')),
        ],
        onClose: () => finish('skip'),
      });
    });
  }

  async function uploadFiles(items, title) {
    const conflicts = [];
    const progress = uploadProgress(title, items);
    let failure = null;
    try {
      for (let i = 0; i < items.length; i += 1) {
        const item = items[i];
        try {
          await uploadItem(item, false, (pct) => progress.update(i, pct));
        } catch (err) {
          if (err && err.code === 'files.exists') {
            conflicts.push(item);
            progress.update(i, 100);
            continue;
          }
          throw err;
        }
      }
    } catch (err) {
      failure = err;
    }
    progress.close();

    if (failure) {
      toastError(failure);
      await loadDir(path);
      return;
    }
    if (!conflicts.length) {
      toast(t('files.uploadSuccess'), 'success');
      await loadDir(path);
      return;
    }

    const choice = await conflictDialog(conflicts.length);
    let replaced = 0;
    if (choice === 'replace') {
      const replaceProgress = uploadProgress(t('files.replacing'), conflicts);
      try {
        for (let i = 0; i < conflicts.length; i += 1) {
          await uploadItem(conflicts[i], true, (pct) => replaceProgress.update(i, pct));
          replaced += 1;
        }
      } catch (err) {
        replaceProgress.close();
        toastError(err);
        await loadDir(path);
        return;
      }
      replaceProgress.close();
    }
    toast(t('files.uploadSummary', {
      uploaded: items.length - conflicts.length,
      replaced,
      skipped: conflicts.length - replaced,
    }), 'success');
    await loadDir(path);
  }

  async function droppedFiles(dataTransfer) {
    const entries = Array.from(dataTransfer.items || [])
      .filter((item) => item.kind === 'file')
      .map((item) => (item.webkitGetAsEntry ? item.webkitGetAsEntry() : null));
    if (entries.length && entries.every(Boolean)) {
      const out = [];
      for (const entry of entries) {
        await collectEntryFiles(entry, '', out);
      }
      return out;
    }
    return Array.from(dataTransfer.files || []).map((file) => ({ file, rel: file.name }));
  }

  async function collectEntryFiles(entry, prefix, out) {
    if (entry.isFile) {
      const file = await new Promise((resolve, reject) => entry.file(resolve, reject));
      out.push({ file, rel: prefix + entry.name });
      return;
    }
    if (!entry.isDirectory) return;
    const children = await readDirectoryEntries(entry);
    for (const child of children) {
      await collectEntryFiles(child, `${prefix}${entry.name}/`, out);
    }
  }

  function readDirectoryEntries(entry) {
    return new Promise((resolve, reject) => {
      const reader = entry.createReader();
      const children = [];
      const readBatch = () => {
        reader.readEntries((batch) => {
          if (!batch.length) {
            resolve(children);
            return;
          }
          children.push(...batch);
          readBatch();
        }, reject);
      };
      readBatch();
    });
  }

  const ready = await loadRoots();
  if (ready) {
    paintCrumbs();
    await loadDir('');
  }
  return page;
}
