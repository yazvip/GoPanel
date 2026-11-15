import { api, qs } from './api.js';
import { t } from './i18n.js';
import { h, openModal, button, loadingBlock, toast, toastError, formatBytes, formatDate } from './ui.js';

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

// openFileEditor shows the file in the panel's editor and saves it back.
export async function openFileEditor(root, path, name) {
  let data;
  try {
    data = await api.get(`/files/content${qs({ root, path })}`);
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

  const saveBtn = button(t('files.save'), {
    variant: 'primary', icon: 'save',
    onClick: () => save(),
  });
  saveBtn.disabled = true; // enabled once the editor is ready

  const modal = openModal({
    title: `${name || data.path}`,
    size: 'modal-xl',
    body: h('div', {},
      h('div', { class: 'editor-bar' },
        h('span', { class: 'mono small' }, data.path || path),
        h('span', { class: 'grow' }),
        status),
      container),
    footer: [
      button(t('common.close'), { onClick: () => modal.close() }),
      saveBtn,
    ],
  });

  // CodeMirror is loaded lazily on the first edit, then kept for the session.
  loadCodeMirrorCore().then(async (CodeMirror) => {
    const lang = detectLang(name || path);
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
    saveBtn.disabled = false;
  }).catch((err) => {
    toastError(err);
    fallback = h('textarea', {
      class: 'textarea code-editor',
      rows: '24',
      spellcheck: 'false',
    });
    fallback.value = data.content || '';
    container.replaceChildren(fallback);
    saveBtn.disabled = false;
  });

  async function save(force = false) {
    const value = cm ? cm.getValue() : (fallback ? fallback.value : null);
    if (value === null) return;
    try {
      const res = await api.put('/files/content', {
        root, path, content: value, mtime: force ? 0 : mtime,
      });
      mtime = res.mtime || mtime;
      toast(t('files.saved'), 'success');
      modal.close();
    } catch (err) {
      if (err.code === 'files.conflict' && !force) {
        const ok = window.confirm(err.message);
        if (ok) await save(true);
        return;
      }
      toastError(err);
    }
  }
}
