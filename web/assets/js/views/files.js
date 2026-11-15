import { api, qs, getCSRF } from '../api.js';
import { t } from '../i18n.js';
import { h, pageHeader, openModal, button, toastError } from '../ui.js';
import { openFileEditor } from '../editor.js';

const JQUERY_URL = '/assets/vendor/jquery/jquery.min.js';
const ELFINDER_URL = '/assets/vendor/elfinder/js/elfinder.min.js';
const ELFINDER_LOCALE_URL = '/assets/vendor/elfinder/js/i18n/elfinder.id.js';
const CONNECTOR_URL = '/api/v1/files/connector';

let assetsPromise = null;

function loadScript(src) {
  return new Promise((resolve, reject) => {
    if (document.querySelector(`script[data-elfinder="${src}"]`)) {
      resolve();
      return;
    }
    const script = document.createElement('script');
    script.src = src;
    script.dataset.elfinder = src;
    script.onload = () => resolve();
    script.onerror = () => reject(new Error(`failed to load ${src}`));
    document.head.appendChild(script);
  });
}

function loadAssets() {
  if (!assetsPromise) {
    assetsPromise = (async () => {
      await loadScript(JQUERY_URL);
      await loadScript(ELFINDER_URL);
      await loadScript(ELFINDER_LOCALE_URL);
    })().catch((err) => {
      assetsPromise = null;
      throw err;
    });
  }
  return assetsPromise;
}

// The connector packs the volume index and the path into the hash elFinder
// hands back for every file, so the panel can read the path again.
function encodeHash(volume, rel) {
  const bytes = new TextEncoder().encode(`${volume}|${rel}`);
  let binary = '';
  bytes.forEach((byte) => { binary += String.fromCharCode(byte); });
  return window.btoa(binary).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
}

function decodeHash(hash) {
  const padded = String(hash || '').replace(/-/g, '+').replace(/_/g, '/');
  const binary = window.atob(padded + '==='.slice((padded.length + 3) % 4));
  const bytes = Uint8Array.from(binary, (ch) => ch.charCodeAt(0));
  const [volume, ...rest] = new TextDecoder().decode(bytes).split('|');
  return { volume: Number(volume), path: rest.join('|') };
}

function isImage(name) {
  return /\.(png|jpe?g|gif|webp|bmp|ico)$/i.test(name || '');
}

export async function render(ctx) {
  const { outlet, query } = ctx;
  const page = h('div', { class: 'page fm-page' });
  outlet.replaceChildren(page);

  const holder = h('div', { class: 'fm-elfinder' });
  page.append(
    pageHeader(t('files.title'), t('files.subtitle')),
    h('div', { class: 'mt3' }, holder),
  );

  let roots = [];
  try {
    roots = (await api.get('/files/roots')) || [];
  } catch (err) {
    toastError(err);
  }

  try {
    await loadAssets();
  } catch (err) {
    holder.replaceChildren(h('div', { class: 'alert alert-danger' }, err.message));
    return page;
  }

  const $ = window.jQuery;
  $(holder).elfinder({
    url: CONNECTOR_URL,
    customHeaders: { 'X-CSRF-Token': getCSRF() },
    locale: 'id',
    height: '100%',
    requestType: 'post',
    uploadMaxChunkSize: 2147483648,
    onlyMimes: [],
    getFileCallback: (file) => openInPanel(file),
    commandsOptions: {
      upload: { async: true },
    },
  });
  const finder = $(holder).elfinder('instance');

  // The site list opens a folder with #/files?root=…&path=… .
  let deepLinked = false;
  finder.bind('open', () => {
    if (deepLinked) return;
    deepLinked = true;
    const wanted = query ? query.get('root') : '';
    if (!wanted) return;
    const index = roots.findIndex((root) => root.token === wanted);
    if (index < 0) return;
    finder.exec('open', encodeHash(index + 1, query.get('path') || ''));
  });

  function openInPanel(file) {
    if (!file || file.dir) return;
    const { volume, path: rel } = decodeHash(file.hash);
    const root = roots[volume - 1];
    if (!root) return;
    if (isImage(file.name)) {
      const src = api.downloadURL(`/files/preview${qs({ root: root.token, path: rel })}`);
      const modal = openModal({
        title: file.name,
        size: 'modal-lg',
        body: h('div', { class: 'preview-box' }, h('img', { src, alt: file.name, class: 'preview-img' })),
        footer: [button(t('common.close'), { onClick: () => modal.close() })],
      });
      return;
    }
    openFileEditor(root.token, rel, file.name);
  }

  return page;
}
