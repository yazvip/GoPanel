import { api, qs } from '../api.js';
import { t } from '../i18n.js';
import {
  h, pageHeader, card, emptyState, button, iconButton, openModal, confirmDialog,
  promptDialog, field, input, select, textarea, loadingBlock, toast, toastError,
  iconSpan, formatBytes, formatDate, copyText, fileKind,
} from '../ui.js';
import { canWrite } from '../main.js';

const ICONS = { dir: 'folder', php: 'code', img: 'image', code: 'code', archive: 'archive', file: 'file' };

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
  sass: { mode: 'text/x-sass', files: ['css', 'sass'] },
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

export async function render(ctx) {
  const { outlet } = ctx;
  const page = h('div', { class: 'page' });
  outlet.replaceChildren(page);

  let roots = [];
  let root = '';
  let path = '';
  let entries = [];
  let searchMode = false;
  const tableBox = h('div', {});
  const crumbBox = h('div', { class: 'crumbs' });
  const searchInput = input({ class: 'input search-input', placeholder: t('files.searchPlaceholder'), type: 'search' });
  const rootSelect = select([], { class: 'select root-select' });
  const uploadInput = h('input', { type: 'file', multiple: true, hidden: true });
  const toolbar = h('div', { class: 'file-toolbar' }, rootSelect, crumbBox,
    h('div', { class: 'grow' }),
    searchInput,
    canWrite() ? button(t('files.upload'), { icon: 'upload', onClick: () => uploadInput.click() }) : null,
    canWrite() ? button(t('files.newFolder'), { icon: 'folderPlus', onClick: newFolder }) : null,
    canWrite() ? button(t('files.newFile'), { icon: 'filePlus', onClick: newFile }) : null,
    button(t('common.refresh'), { icon: 'refresh', onClick: () => loadDir(path) }));

  page.append(
    pageHeader(t('files.title'), t('files.subtitle')),
    card({ body: toolbar, flush: false }),
    h('div', { class: 'mt3' }, tableBox),
    uploadInput,
  );

  uploadInput.addEventListener('change', () => {
    if (uploadInput.files && uploadInput.files.length) uploadFiles(Array.from(uploadInput.files));
    uploadInput.value = '';
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

  async function loadRoots() {
    try {
      roots = await api.get('/files/roots');
    } catch (err) {
      tableBox.replaceChildren(card({ body: h('div', { class: 'alert alert-danger' }, err.message) }));
      return false;
    }
    if (!roots || !roots.length) {
      tableBox.replaceChildren(card({ body: emptyState(t('files.noRoots'), '', null, 'folder') }));
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
    paintCrumbs();
    tableBox.replaceChildren(loadingBlock(t('files.loading')));
    try {
      const data = await api.get(`/files${qs({ root, path })}`);
      entries = data.entries || [];
      paintTable();
    } catch (err) {
      tableBox.replaceChildren(card({ body: h('div', { class: 'alert alert-danger' }, err.message) }));
    }
  }

  async function runSearch(q) {
    searchMode = true;
    tableBox.replaceChildren(loadingBlock(t('common.loading')));
    try {
      const data = await api.get(`/files/search${qs({ root, path, q })}`);
      entries = data.entries || [];
      paintTable(true);
    } catch (err) {
      tableBox.replaceChildren(card({ body: h('div', { class: 'alert alert-danger' }, err.message) }));
    }
  }

  function paintTable(fromSearch = false) {
    if (!entries.length) {
      tableBox.replaceChildren(card({
        body: emptyState(fromSearch ? t('files.noResults') : t('files.emptyFolder'),
          fromSearch ? '' : t('files.emptyFolderHint'),
          canWrite() && !fromSearch ? button(t('files.upload'), { icon: 'upload', onClick: () => uploadInput.click() }) : null,
          fromSearch ? 'search' : 'folder'),
      }));
      return;
    }
    const rows = entries.map((entry) => {
      const kind = fileKind(entry);
      const row = h('tr', { class: 'file-row', dataset: { name: entry.name } },
        h('td', {},
          h('div', { class: 'file-cell' },
            h('span', { class: `file-icon ${kind}` }, iconSpan(ICONS[kind] || 'file', 18)),
            h('button', {
              class: 'file-name', type: 'button', title: entry.name,
              onClick: () => openEntry(entry),
            }, entry.name),
            entry.is_link ? badgeLink() : null)),
        h('td', { class: 'nowrap muted' }, entry.is_dir ? '—' : formatBytes(entry.size)),
        h('td', { class: 'nowrap mono muted' }, entry.mode_oct || entry.mode || '—'),
        h('td', { class: 'nowrap muted' }, formatDate(entry.mtime)),
        h('td', { class: 'col-actions' }, h('div', { class: 'row-actions' },
          entry.is_dir
            ? iconButton('folderOpen', t('common.open'), () => loadDir(entry.path))
            : iconButton('eye', t('files.view'), () => openEntry(entry)),
          !entry.is_dir ? iconButton('download', t('files.download'), () => triggerDownload(entry)) : null,
          canWrite() ? iconButton('edit', t('files.rename'), () => renameEntry(entry)) : null,
          canWrite() ? iconButton('copy', t('files.copy'), () => transferEntry(entry, 'copy')) : null,
          canWrite() ? iconButton('move', t('files.move'), () => transferEntry(entry, 'move')) : null,
          canWrite() ? iconButton('shield', t('files.chmod'), () => chmodEntry(entry)) : null,
          canWrite() ? iconButton('archive', t('files.archive'), () => archiveEntry(entry)) : null,
          canWrite() && !entry.is_dir && /\.(zip|tar\.gz|tgz|tar)$/i.test(entry.name)
            ? iconButton('packageOpen', t('files.extract'), () => extractEntry(entry)) : null,
          canWrite() ? iconButton('trash', t('common.delete'), () => deleteEntry(entry), 'danger') : null)));
      return row;
    });

    tableBox.replaceChildren(card({
      title: fromSearch ? `${t('files.searchResults')} — “${searchInput.value.trim()}”` : undefined,
      icon: fromSearch ? 'search' : 'folder',
      flush: true,
      body: h('div', { class: 'table-scroll' }, h('table', { class: 'table file-table' },
        h('thead', {}, h('tr', {},
          h('th', {}, t('files.name')),
          h('th', {}, t('files.size')),
          h('th', {}, t('files.permissions')),
          h('th', {}, t('files.modified')),
          h('th', { class: 'col-actions' }, t('files.actions')))),
        h('tbody', {}, rows))),
      foot: h('div', { class: 'muted small' }, t('files.folderHint')),
    }));
  }

  function badgeLink() {
    return h('span', { class: 'badge badge-gray', title: t('files.symlink') }, 'link');
  }

  function triggerDownload(entry) {
    const a = h('a', { href: downloadURL(root, entry.path), download: entry.name });
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
      value: parentPath(entry.path), confirmText: t('files.extract'),
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

  async function uploadFiles(files) {
    const bar = h('div', { class: 'progress-inline big' }, h('span', { style: { width: '0%' } }));
    const label = h('div', { class: 'muted small' }, t('files.uploading', { pct: 0 }));
    const modal = openModal({
      title: t('files.uploadFiles'),
      size: 'modal-sm',
      body: h('div', {}, h('div', { class: 'mb2' }, files.map((f) => h('div', { class: 'mono small ellipsis' }, f.name))), bar, label),
    });
    let done = 0;
    try {
      for (const file of files) {
        const form = new FormData();
        form.append('root', root);
        form.append('path', path);
        form.append('overwrite', 'true');
        form.append('file', file, file.name);
        await api.upload('/files/upload', form, (pct) => {
          const overall = Math.round(((done + pct / 100) / files.length) * 100);
          bar.firstChild.style.width = `${overall}%`;
          label.textContent = t('files.uploading', { pct: overall });
        });
        done += 1;
      }
      modal.close();
      toast(t('files.uploadSuccess'), 'success');
      await loadDir(path);
    } catch (err) {
      modal.close();
      toastError(err);
    }
  }

  const ready = await loadRoots();
  if (ready) {
    paintCrumbs();
    await loadDir('');
  }
  return page;
}
