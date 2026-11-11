import { api } from '../api.js';
import { t } from '../i18n.js';
import {
  h, pageHeader, card, badge, dataTable, emptyState, button, iconButton, field,
  input, openModal, confirmDialog, loadingBlock, toast, toastError, iconSpan,
  formatDate, relativeTime, copyText,
} from '../ui.js';
import { getState, isAdmin } from '../main.js';

function describeAgent(userAgent) {
  const ua = String(userAgent || '');
  if (!ua) return t('profile.unknownDevice');
  const browser = /Edg\//.test(ua) ? 'Edge'
    : /OPR\//.test(ua) ? 'Opera'
      : /Firefox\//.test(ua) ? 'Firefox'
        : /Chrome\//.test(ua) ? 'Chrome'
          : /Safari\//.test(ua) ? 'Safari'
            : '';
  const os = /Windows/.test(ua) ? 'Windows'
    : /Mac OS X|Macintosh/.test(ua) ? 'macOS'
      : /Android/.test(ua) ? 'Android'
        : /iPhone|iPad/.test(ua) ? 'iOS'
          : /Linux/.test(ua) ? 'Linux'
            : '';
  const label = [browser, os].filter(Boolean).join(' · ');
  return label || ua.slice(0, 60);
}

export async function render(ctx) {
  const { outlet } = ctx;
  const page = h('div', { class: 'page' });
  outlet.replaceChildren(page);
  let sessions = [];
  const content = h('div', {});

  page.append(
    pageHeader(t('profile.title'), t('profile.subtitle'),
      button(t('common.refresh'), { icon: 'refresh', onClick: load })),
    content,
  );

  function currentUser() {
    return getState().user || {};
  }

  async function load() {
    content.replaceChildren(loadingBlock());
    try {
      sessions = (await api.get('/auth/sessions')) || [];
    } catch (err) {
      sessions = [];
      toastError(err);
    }
    paint();
  }

  function paint() {
    const user = currentUser();
    const account = card({
      title: t('profile.account'),
      icon: 'user',
      actions: badge(user.role || 'viewer', user.role === 'admin' ? 'red' : user.role === 'operator' ? 'blue' : 'gray'),
      body: h('dl', { class: 'kv' },
        h('dt', {}, t('users.username')), h('dd', { class: 'mono' }, user.username || '—'),
        h('dt', {}, t('users.role')), h('dd', {}, user.role || '—'),
        h('dt', {}, t('users.language')), h('dd', {}, (user.locale || 'id').toUpperCase()),
        h('dt', {}, t('common.created')), h('dd', {}, formatDate(user.created_at)),
        h('dt', {}, t('users.lastLogin')), h('dd', {}, user.last_login_at ? formatDate(user.last_login_at) : t('common.never')),
        h('dt', {}, t('users.mustChangePassword')), h('dd', {}, user.must_change_password ? t('common.yes') : t('common.no'))),
    });

    const password = card({
      title: t('profile.changePassword'),
      icon: 'lock',
      body: passwordForm(),
    });

    const tfa = card({
      title: t('profile.twoFactor'),
      icon: 'shieldCheck',
      actions: user.totp_enabled ? badge(t('users.twoFactorOn'), 'green') : badge(t('users.twoFactorOff'), 'gray'),
      body: twoFactorBody(user),
    });

    const sessionsCard = card({
      title: `${t('profile.sessions')} (${sessions.length})`,
      icon: 'monitor',
      flush: true,
      body: sessions.length ? dataTable([
        { label: t('profile.device'), render: (s) => h('div', {},
          h('div', { class: 'row gap-sm' },
            h('b', {}, describeAgent(s.user_agent)),
            s.current ? badge(t('profile.thisDevice'), 'green') : null),
          h('div', { class: 'muted small mono' }, s.ip || '—')) },
        { label: t('profile.lastSeen'), render: (s) => h('span', { class: 'muted small', title: formatDate(s.last_seen_at) },
          relativeTime(s.last_seen_at)) },
        { label: t('profile.created'), render: (s) => h('span', { class: 'muted small' }, formatDate(s.created_at)) },
        { label: t('profile.expires'), render: (s) => h('span', { class: 'muted small' }, formatDate(s.expires_at)) },
        { label: t('common.actions'), className: 'col-actions', render: (s) => s.current
          ? h('span', { class: 'muted small' }, '—')
          : h('div', { class: 'row-actions' }, iconButton('xCircle', t('profile.revoke'), () => revoke(s), 'danger')) },
      ], sessions) : emptyState(t('profile.noSessions'), '', null, 'monitor'),
    });

    content.replaceChildren(
      h('div', { class: 'grid cols-2' }, account, password),
      h('div', { class: 'grid cols-2 mt3' }, tfa, sessionsCard),
    );
  }

  function passwordForm() {
    const current = input({ type: 'password', autocomplete: 'current-password' });
    const next = input({ type: 'password', autocomplete: 'new-password' });
    const confirm = input({ type: 'password', autocomplete: 'new-password' });
    let strength = null;
    const submit = async () => {
      if (!current.value) { toast(t('auth.passwordRequired'), 'warn'); return; }
      if (next.value !== confirm.value) { toast(t('auth.passwordMismatch'), 'warn'); return; }
      if (next.value.length < 10) { toast(t('auth.weak_password'), 'warn'); return; }
      try {
        await api.put('/auth/password', {
          current_password: current.value,
          new_password: next.value,
        });
        current.value = '';
        next.value = '';
        confirm.value = '';
        if (strength) strength.textContent = '';
        toast(t('profile.passwordChanged'), 'success');
        await load();
      } catch (err) {
        toastError(err);
      }
    };
    return h('div', {},
      field(t('profile.currentPassword'), current),
      field(t('profile.newPassword'), next, t('auth.passwordHint')),
      field(t('profile.confirmPassword'), confirm),
      h('div', { class: 'form-actions' },
        button(t('profile.changePassword'), { variant: 'primary', icon: 'save', onClick: submit })));
  }

  function twoFactorBody(user) {
    if (!user.totp_enabled) {
      return h('div', {},
        h('p', { class: 'muted small' }, t('profile.twoFactorOff')),
        button(t('profile.enableTwoFactor'), { variant: 'primary', icon: 'shield', onClick: startSetup }));
    }
    return h('div', {},
      h('p', { class: 'muted small' }, t('profile.twoFactorOn')),
      h('div', { class: 'row gap-sm' },
        button(t('profile.regenerateCodes'), { icon: 'rotate', onClick: regenerateCodes }),
        button(t('profile.disableTwoFactor'), { variant: 'danger', icon: 'xCircle', onClick: disableTwoFactor })));
  }

  function recoveryModal(codes, title) {
    const list = h('div', { class: 'recovery-grid' }, codes.map((code) => h('code', {}, code)));
    const modal = openModal({
      title: title || t('profile.recoveryCodes'),
      size: 'modal-sm',
      body: [
        h('div', { class: 'alert alert-warn small' }, iconSpan('alert', 16),
          h('div', {}, t('profile.recoveryHint'))),
        list,
        h('div', { class: 'muted small mt2' }, t('common.credentialsOnce')),
      ],
      footer: [
        button(t('profile.recoveryCopy'), {
          icon: 'copy',
          onClick: () => copyText(codes.join('\n')).then(() => toast(t('common.copied'), 'success')),
        }),
        button(t('common.close'), { variant: 'primary', onClick: () => modal.close() }),
      ],
    });
  }

  async function startSetup() {
    let setup;
    try {
      setup = await api.post('/auth/2fa/setup', {});
    } catch (err) {
      toastError(err);
      return;
    }
    const code = input({ placeholder: '123456', class: 'input mono', autocomplete: 'one-time-code', inputmode: 'numeric', maxlength: '6' });
    const secret = input({ value: setup.secret, class: 'input mono', readonly: true });
    const modal = openModal({
      title: t('profile.enableTwoFactor'),
      size: 'modal-sm',
      body: [
        h('p', { class: 'muted small' }, t('profile.scanHint')),
        h('div', { class: 'qr-wrap' }, h('img', { src: setup.qr, alt: 'QR', class: 'qr-image' })),
        field(t('profile.secret'), h('div', { class: 'input-group' }, secret,
          button(t('common.copy'), { icon: 'copy', onClick: () => copyText(setup.secret) }))),
        field(t('profile.code'), code, t('profile.codeHint'), true),
      ],
      footer: [
        button(t('common.cancel'), { onClick: () => modal.close() }),
        button(t('profile.verifyEnable'), {
          variant: 'primary',
          onClick: async () => {
            try {
              const result = await api.post('/auth/2fa/enable', { secret: setup.secret, code: code.value.trim() });
              modal.close();
              toast(t('profile.twoFactorEnabled'), 'success');
              if (result && result.recovery_codes) recoveryModal(result.recovery_codes);
              currentUser().totp_enabled = true;
              await load();
            } catch (err) {
              toastError(err);
            }
          },
        }),
      ],
    });
  }

  function credentialsPrompt(title, confirmLabel, onSubmit) {
    const password = input({ type: 'password', autocomplete: 'current-password' });
    const code = input({ placeholder: '123456', class: 'input mono', inputmode: 'numeric', maxlength: '6', autocomplete: 'one-time-code' });
    const modal = openModal({
      title,
      size: 'modal-sm',
      body: [
        field(t('profile.currentPassword'), password, '', true),
        field(t('profile.code'), code, t('profile.codeHint'), true),
      ],
      footer: [
        button(t('common.cancel'), { onClick: () => modal.close() }),
        button(confirmLabel, {
          variant: 'primary',
          onClick: async () => {
            try {
              await onSubmit(password.value, code.value.trim(), modal);
            } catch (err) {
              toastError(err);
            }
          },
        }),
      ],
    });
  }

  function regenerateCodes() {
    credentialsPrompt(t('profile.regenerateCodes'), t('profile.regenerateCodes'), async (password, code, modal) => {
      const result = await api.post('/auth/2fa/recovery', { password, code });
      modal.close();
      toast(t('profile.codesRegenerated'), 'success');
      if (result && result.recovery_codes) recoveryModal(result.recovery_codes);
    });
  }

  function disableTwoFactor() {
    credentialsPrompt(t('profile.disableTwoFactor'), t('profile.disableTwoFactor'), async (password, code, modal) => {
      await api.post('/auth/2fa/disable', { password, code });
      modal.close();
      toast(t('profile.twoFactorDisabled'), 'success');
      currentUser().totp_enabled = false;
      await load();
    });
  }

  async function revoke(session) {
    const ok = await confirmDialog({
      title: t('profile.revoke'),
      message: t('profile.revokeConfirm'),
      note: `${describeAgent(session.user_agent)} · ${session.ip || ''}`,
      confirmText: t('profile.revoke'),
      danger: true,
    });
    if (!ok) return;
    try {
      await api.del(`/auth/sessions/${session.id}`, {});
      toast(t('profile.sessionRevoked'), 'success');
      await load();
    } catch (err) {
      toastError(err);
    }
  }

  await load();
  return page;
}
