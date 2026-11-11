import { api } from '../api.js';
import { t } from '../i18n.js';
import { h, pageHeader, emptyState, button, toast, toastError } from '../ui.js';
import { getState, isAdmin } from '../main.js';
import { TerminalEmulator } from '../terminal-emulator.js';

const API_PREFIX = '/api/v1';
const FONT_MIN = 11;
const FONT_MAX = 20;
const MAX_RETRIES = 3;

const SPECIAL_KEYS = {
  Enter: '\r',
  Backspace: '\x7f',
  Tab: '\t',
  Escape: '\x1b',
  ArrowUp: '\x1b[A',
  ArrowDown: '\x1b[B',
  ArrowRight: '\x1b[C',
  ArrowLeft: '\x1b[D',
  Home: '\x1b[H',
  End: '\x1b[F',
  Insert: '\x1b[2~',
  Delete: '\x1b[3~',
  PageUp: '\x1b[5~',
  PageDown: '\x1b[6~',
  F1: '\x1bOP',
  F2: '\x1bOQ',
  F3: '\x1bOR',
  F4: '\x1bOS',
  F5: '\x1b[15~',
  F6: '\x1b[17~',
  F7: '\x1b[18~',
  F8: '\x1b[19~',
  F9: '\x1b[20~',
  F10: '\x1b[21~',
  F11: '\x1b[23~',
  F12: '\x1b[24~',
};

function controlChar(key) {
  if (key === ' ' || key === '@') return '\x00';
  if (key >= 'a' && key <= 'z') return String.fromCharCode(key.charCodeAt(0) - 96);
  if (key >= 'A' && key <= 'Z') return String.fromCharCode(key.charCodeAt(0) - 64);
  const map = { '[': '\x1b', '\\': '\x1c', ']': '\x1d', '^': '\x1e', '_': '\x1f' };
  return Object.prototype.hasOwnProperty.call(map, key) ? map[key] : null;
}

function accessDenied(reason) {
  return h('div', { class: 'page' },
    pageHeader(t('terminal.title'), t('terminal.subtitle')),
    emptyState(reason, '', null, 'terminal'));
}

export async function render(ctx) {
  if (!isAdmin()) return accessDenied(t('terminal.adminOnly'));
  if (!getState().capabilities.terminal) return accessDenied(t('terminal.disabled'));

  const { outlet } = ctx;
  const page = h('div', { class: 'page' });
  outlet.replaceChildren(page);

  const emulator = new TerminalEmulator({
    cols: 100,
    rows: 30,
    onData: (data) => sendData(data),
    onResize: () => sendResize(),
  });
  const screen = emulator.element;
  screen.tabIndex = 0;

  const keyboard = h('textarea', {
    class: 'term-hidden-input',
    autocomplete: 'off',
    autocapitalize: 'off',
    autocorrect: 'off',
    spellcheck: 'false',
    'aria-label': 'Terminal input',
    tabindex: '-1',
  });

  const statusDot = h('span', { class: 'dot' });
  const statusText = h('span', {}, t('terminal.connecting'));
  const statusEl = h('div', { class: 'status' }, statusDot, statusText);

  let fontSize = 13;
  const changeFont = (delta) => {
    fontSize = Math.max(FONT_MIN, Math.min(FONT_MAX, fontSize + delta));
    screen.style.fontSize = `${fontSize}px`;
    emulator.fit(screen);
  };

  const toolbar = h('div', { class: 'term-toolbar' },
    statusEl,
    h('span', { class: 'spacer' }),
    h('button', {
      class: 'icon-btn', type: 'button', title: t('terminal.fontSize'),
      onClick: () => changeFont(-1),
    }, 'A−'),
    h('button', {
      class: 'icon-btn', type: 'button', title: t('terminal.fontSize'),
      onClick: () => changeFont(1),
    }, 'A+'),
    button(t('terminal.clear'), { icon: 'trash', onClick: () => { emulator.clear(); focusKeyboard(); } }),
    button(t('terminal.newSession'), { icon: 'plus', onClick: () => newSession() }));

  const body = h('div', { style: { position: 'relative' } }, screen, keyboard);

  const note = h('div', { class: 'term-note' },
    h('div', {}, t('terminal.hint')),
    h('div', {}, `${t('terminal.copyHint')} ${t('terminal.pasteHint')}`));

  page.append(
    pageHeader(t('terminal.title'), t('terminal.subtitle')),
    h('div', { class: 'terminal-wrap' }, toolbar, body, note));

  const encoder = new TextEncoder();
  let decoder = new TextDecoder('utf-8');
  let ws = null;
  let disposed = false;
  let connecting = false;
  let retries = 0;
  let retryTimer = 0;
  let fitFrame = 0;
  let composing = false;

  function setStatus(kind, label) {
    statusText.textContent = label;
    statusEl.classList.toggle('connected', kind === 'connected');
    statusDot.style.background = kind === 'connected'
      ? '#2ecc71'
      : kind === 'disconnected' ? '#e74c3c' : '#e79a20';
  }

  function sendData(text) {
    if (!text || !ws || ws.readyState !== WebSocket.OPEN) return;
    ws.send(encoder.encode(text));
  }

  function sendResize() {
    if (!ws || ws.readyState !== WebSocket.OPEN) return;
    ws.send(JSON.stringify({ type: 'resize', cols: emulator.cols, rows: emulator.rows }));
  }

  function focusKeyboard() {
    if (disposed) return;
    try {
      keyboard.focus({ preventScroll: true });
    } catch {
      keyboard.focus();
    }
  }

  function closeSocket(reason) {
    if (!ws) return;
    ws.__closing = true;
    try {
      ws.close(1000, reason);
    } catch {
      /* the socket is already gone */
    }
    ws = null;
  }

  async function connect() {
    if (disposed || connecting) return;
    clearTimeout(retryTimer);
    connecting = true;
    setStatus('connecting', retries ? t('terminal.reconnecting') : t('terminal.connecting'));
    try {
      const ticket = await api.post('/terminal/ticket', {});
      if (disposed) return;
      openSocket(ticket.token);
    } catch (err) {
      connecting = false;
      setStatus('disconnected', t('terminal.startFailed'));
      toastError(err);
    }
  }

  function openSocket(token) {
    const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
    const query = new URLSearchParams({
      ticket: token,
      cols: String(emulator.cols),
      rows: String(emulator.rows),
    });
    decoder = new TextDecoder('utf-8');
    let socket;
    try {
      socket = new WebSocket(`${proto}//${location.host}${API_PREFIX}/terminal/ws?${query}`);
    } catch (err) {
      connecting = false;
      setStatus('disconnected', t('terminal.startFailed'));
      toastError(err);
      return;
    }
    socket.binaryType = 'arraybuffer';
    ws = socket;

    socket.addEventListener('open', () => {
      connecting = false;
      retries = 0;
      setStatus('connected', t('terminal.connected'));
      sendResize();
      focusKeyboard();
    });

    socket.addEventListener('message', (event) => {
      if (typeof event.data === 'string') {
        emulator.write(event.data);
        return;
      }
      const text = decoder.decode(new Uint8Array(event.data), { stream: true });
      if (text) emulator.write(text);
    });

    socket.addEventListener('close', (event) => {
      if (ws === socket) ws = null;
      connecting = false;
      if (disposed || socket.__closing) return;
      if (event.code === 1000 || event.code === 1008) {
        setStatus('disconnected', t('terminal.sessionEnded'));
        return;
      }
      if (retries < MAX_RETRIES) {
        retries += 1;
        setStatus('connecting', t('terminal.reconnecting'));
        retryTimer = setTimeout(connect, 500 * (2 ** (retries - 1)));
        return;
      }
      setStatus('disconnected', t('terminal.wsError'));
    });
  }

  function newSession() {
    clearTimeout(retryTimer);
    retries = 0;
    closeSocket('new session');
    emulator.clear();
    connect();
  }

  keyboard.addEventListener('keydown', (event) => {
    if (event.isComposing) return;
    const { key } = event;
    const altGraph = typeof event.getModifierState === 'function' && event.getModifierState('AltGraph');

    if ((event.ctrlKey || event.metaKey) && event.shiftKey && (key === 'c' || key === 'C')) {
      const selection = window.getSelection();
      const selected = selection ? String(selection) : '';
      if (selected && navigator.clipboard && navigator.clipboard.writeText) {
        event.preventDefault();
        navigator.clipboard.writeText(selected)
          .then(() => toast(t('common.copied'), 'success', 2000))
          .catch(() => toast(t('common.copyFailed'), 'error', 3000));
        return;
      }
    }

    if (event.ctrlKey && event.shiftKey && (key === 'v' || key === 'V')) {
      return;
    }

    if (SPECIAL_KEYS[key]) {
      event.preventDefault();
      sendData(SPECIAL_KEYS[key]);
      return;
    }

    if ((event.ctrlKey || event.metaKey) && !event.altKey && !altGraph) {
      const sequence = controlChar(key);
      if (sequence !== null) {
        event.preventDefault();
        sendData(sequence);
        return;
      }
    }

    if (event.altKey && !event.ctrlKey && !event.metaKey && !altGraph && key.length === 1) {
      event.preventDefault();
      sendData(`\x1b${key}`);
    }
  });

  keyboard.addEventListener('input', () => {
    if (composing || disposed) return;
    const value = keyboard.value;
    keyboard.value = '';
    if (value) sendData(value);
  });

  keyboard.addEventListener('compositionstart', () => { composing = true; });
  keyboard.addEventListener('compositionend', () => {
    composing = false;
    const value = keyboard.value;
    keyboard.value = '';
    if (value) sendData(value);
  });

  keyboard.addEventListener('paste', (event) => {
    const text = event.clipboardData ? event.clipboardData.getData('text') : '';
    if (!text) return;
    event.preventDefault();
    sendData(text.replace(/\r\n/g, '\r'));
  });

  screen.addEventListener('click', () => {
    const selection = window.getSelection();
    if (!selection || selection.isCollapsed) focusKeyboard();
  });

  screen.addEventListener('wheel', (event) => {
    const up = event.deltaY < 0;
    if (!up && emulator.viewOffset === 0) return;
    if (up && emulator.scrollback.length === 0 && emulator.viewOffset === 0) return;
    event.preventDefault();
    emulator.scrollLines(up ? 3 : -3);
  }, { passive: false });

  const observer = new ResizeObserver(() => {
    if (disposed) return;
    cancelAnimationFrame(fitFrame);
    fitFrame = requestAnimationFrame(() => {
      if (!disposed) emulator.fit(screen);
    });
  });

  ctx.setCleanup(() => {
    disposed = true;
    clearTimeout(retryTimer);
    cancelAnimationFrame(fitFrame);
    observer.disconnect();
    closeSocket('leaving');
    emulator.dispose();
  });

  requestAnimationFrame(() => {
    if (disposed) return;
    emulator.fit(screen);
    observer.observe(screen);
    focusKeyboard();
    connect();
  });

  return page;
}
