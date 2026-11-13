import { api, errorMessage } from './api.js';
import { h, iconSpan, button, openModal, copyText, relativeTime, toastError } from './ui.js';
import { t } from './i18n.js';

const CHECK_DELAY = 2500;
const RECHECK_INTERVAL = 6 * 60 * 60 * 1000;
const PROGRESS_TIMEOUT = 8 * 60 * 1000;
const POLL_INTERVAL = 2500;
const RELOAD_AFTER = 10000;

let lastStatus = null;

export function lastUpdateStatus() {
  return lastStatus;
}

export async function checkForUpdate(force = false) {
  const status = await api.get(`/update${force ? '?force=1' : ''}`);
  lastStatus = status;
  return status;
}

/** Adds the top-right update chip. It polls quietly and only appears when a
 *  newer commit exists on origin/main. */
export function createUpdateBadge() {
  const chip = h('button', {
    class: 'update-chip',
    type: 'button',
    hidden: true,
    'aria-label': t('update.badge'),
    onClick: () => {
      if (lastStatus) openUpdateDialog(lastStatus);
    },
  },
    h('span', { class: 'update-chip-dot' }),
    iconSpan('arrowUpCircle', 15),
    h('span', { class: 'update-chip-label' }, t('update.badge')));

  let timer = null;
  let first = null;

  const apply = (status) => {
    lastStatus = status;
    const show = Boolean(status && status.available && chip.isConnected);
    chip.hidden = !show;
    if (show) {
      const latest = status.latest || t('update.latest');
      chip.title = t('update.badgeHint', { latest });
    }
  };

  const run = async () => {
    if (!chip.isConnected) {
      stop();
      return;
    }
    try {
      apply(await checkForUpdate(false));
    } catch {
      // A failed check stays silent: the panel keeps working offline.
    }
  };

  const start = () => {
    if (timer) return;
    first = setTimeout(run, CHECK_DELAY);
    timer = setInterval(run, RECHECK_INTERVAL);
  };

  const stop = () => {
    if (first) {
      clearTimeout(first);
      first = null;
    }
    if (timer) {
      clearInterval(timer);
      timer = null;
    }
  };

  return { chip, start, stop };
}

export function openUpdateDialog(status) {
  lastStatus = status;
  const body = h('div', { class: 'update-dialog' });

  body.append(h('div', { class: 'update-versions' },
    h('div', { class: 'update-version' },
      h('span', { class: 'update-version-label' }, t('update.current')),
      h('b', { class: 'mono' }, status.current || '—')),
    h('span', { class: 'update-arrow', 'aria-hidden': 'true' }, iconSpan('arrowRight', 16)),
    h('div', { class: 'update-version' },
      h('span', { class: 'update-version-label' }, t('update.latest')),
      h('b', { class: 'mono' }, status.latest || '—'))));

  if (status.behind > 0) {
    body.append(h('div', { class: 'update-behind' },
      iconSpan('gitBranch', 14),
      h('span', {}, t('update.behind', { count: status.behind }))));
  }

  if (status.error) {
    body.append(h('div', { class: 'alert alert-warn mb2' }, iconSpan('alert', 16), h('div', {}, status.error)));
  }

  if (status.available && !status.auto_update) {
    body.append(h('div', { class: 'alert alert-info mb2' }, iconSpan('info', 16), h('div', {}, t('update.manualHint'))));
    if (status.manual_command) {
      body.append(h('div', { class: 'update-command' },
        h('code', {}, status.manual_command),
        button(t('common.copy'), {
          size: 'sm',
          icon: 'copy',
          onClick: () => copyText(status.manual_command),
        })));
    } else {
      body.append(h('div', { class: 'muted small' }, t('update.manualMissing')));
    }
  }

  if (status.available && status.auto_update) {
    body.append(h('div', { class: 'update-note' },
      iconSpan('shieldCheck', 16),
      h('div', {}, t('update.safeNote'))));
  }

  if (status.url) {
    body.append(h('a', {
      class: 'update-link',
      href: status.url,
      target: '_blank',
      rel: 'noopener noreferrer',
    }, iconSpan('external', 14), t('update.viewChanges')));
  }

  if (status.checked_at) {
    body.append(h('div', { class: 'update-checked muted small' },
      t('update.checkedAt', { time: relativeTime(toUnix(status.checked_at)) })));
  }

  const footer = [button(t('common.close'), { onClick: () => modal.close() })];
  if (status.available && status.auto_update) {
    const now = button(t('update.now'), {
      variant: 'primary',
      icon: 'arrowUpCircle',
      onClick: async () => {
        now.disabled = true;
        now.replaceChildren(h('span', { class: 'spinner' }), t('update.starting'));
        try {
          await api.post('/update/apply', {});
          modal.close();
          showUpdateProgress(status);
        } catch (err) {
          now.disabled = false;
          now.replaceChildren(iconSpan('arrowUpCircle', 16), t('update.now'));
          toastError(err);
        }
      },
    });
    footer.push(now);
  }

  const modal = openModal({
    title: t('update.title'),
    size: 'modal-sm',
    body,
    footer,
  });
  return modal;
}

function showUpdateProgress(status) {
  const statusEl = h('div', { class: 'update-progress-status' },
    t('update.progress', { version: status.latest || '' }));
  const detailEl = h('div', { class: 'muted small' }, t('update.progressHint'));
  openModal({
    title: t('update.title'),
    size: 'modal-sm',
    closeOnBackdrop: false,
    body: h('div', { class: 'update-progress' },
      h('span', { class: 'spinner update-spinner' }),
      statusEl,
      detailEl),
  });
  pollRestart(String(status.current || ''), statusEl, detailEl);
}

async function pollRestart(oldVersion, statusEl, detailEl) {
  const started = Date.now();
  let sawDown = false;
  let newSeenAt = 0;
  let recoveredAt = 0;
  let checkedAfterRecovery = false;
  await sleep(3000);
  while (Date.now() - started < PROGRESS_TIMEOUT) {
    const elapsed = Math.round((Date.now() - started) / 1000);
    const health = await readHealth();
    if (!health) {
      sawDown = true;
      statusEl.textContent = t('update.progressWait', { elapsed });
    } else if (health.version && health.version !== oldVersion) {
      if (!newSeenAt) newSeenAt = Date.now();
      if (Date.now() - newSeenAt >= RELOAD_AFTER) {
        statusEl.textContent = t('update.progressReload');
        location.reload();
        return;
      }
      statusEl.textContent = t('update.progressRestarting');
    } else if (sawDown) {
      // Source builds keep the version "dev", so a changed health version is
      // not always available. Once the panel answers again, ask the update
      // endpoint: no pending update means the new binary is running.
      if (!recoveredAt) recoveredAt = Date.now();
      if (!checkedAfterRecovery && Date.now() - recoveredAt >= 5000) {
        checkedAfterRecovery = true;
        const status = await readUpdateStatus();
        if (status && status.available === false) {
          statusEl.textContent = t('update.progressReload');
          location.reload();
          return;
        }
        if (status && status.available) {
          statusEl.textContent = t('update.failed');
          detailEl.textContent = t('update.failedHint');
          return;
        }
        checkedAfterRecovery = false; // inconclusive, retry on the next tick
      }
      statusEl.textContent = t('update.progressWait', { elapsed });
    } else {
      statusEl.textContent = t('update.progressWait', { elapsed });
    }
    await sleep(POLL_INTERVAL);
  }
  statusEl.textContent = t('update.timeout');
  detailEl.textContent = t('update.timeoutHint');
}

async function readHealth() {
  try {
    const res = await fetch('/api/v1/healthz', { cache: 'no-store', credentials: 'same-origin' });
    if (!res.ok) return null;
    return await res.json();
  } catch {
    return null;
  }
}

async function readUpdateStatus() {
  try {
    const res = await fetch('/api/v1/update', { cache: 'no-store', credentials: 'same-origin' });
    if (!res.ok) return null;
    return await res.json();
  } catch {
    return null;
  }
}

function sleep(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

// relativeTime() expects Unix seconds; the API sends an RFC3339 timestamp and
// Go includes nanoseconds, which some browsers refuse to parse.
function toUnix(ts) {
  const ms = Date.parse(String(ts).replace(/(\.\d{3})\d+/, '$1'));
  return Number.isNaN(ms) ? 0 : Math.floor(ms / 1000);
}

/** Manual check block used by the About dialog (admins only). */
export function createAboutUpdateRow() {
  const stateEl = h('div', { class: 'about-update-state muted small' }, t('update.notChecked'));
  const actionSlot = h('div', { class: 'about-update-actions' });

  const check = button(t('update.check'), {
    size: 'sm',
    icon: 'refresh',
    onClick: async () => {
      check.disabled = true;
      check.replaceChildren(h('span', { class: 'spinner' }), t('update.checking'));
      try {
        const status = await checkForUpdate(true);
        renderAboutStatus(actionSlot, stateEl, status);
      } catch (err) {
        stateEl.replaceChildren(h('span', {}, errorMessage(err)));
      } finally {
        if (check.isConnected) {
          check.disabled = false;
          check.replaceChildren(iconSpan('refresh', 16), t('update.check'));
        }
      }
    },
  });

  return h('div', { class: 'about-update' }, stateEl,
    h('div', { class: 'about-update-actions' }, check, actionSlot));
}

function renderAboutStatus(actions, stateEl, status) {
  actions.replaceChildren();
  if (status.available) {
    stateEl.replaceChildren(iconSpan('arrowUpCircle', 15),
      h('span', {}, t('update.availableShort', { latest: status.latest || '' })));
    if (status.auto_update) {
      actions.append(button(t('update.now'), {
        size: 'sm',
        variant: 'primary',
        icon: 'arrowUpCircle',
        onClick: () => openUpdateDialog(status),
      }));
    } else {
      actions.append(button(t('update.showCommand'), {
        size: 'sm',
        variant: 'primary',
        icon: 'terminal',
        onClick: () => openUpdateDialog(status),
      }));
    }
  } else if (status.error) {
    stateEl.replaceChildren(h('span', {}, status.error));
  } else {
    stateEl.replaceChildren(iconSpan('checkCircle', 15), h('span', {}, t('update.upToDate')));
  }
}
