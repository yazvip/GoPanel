import { api } from '../api.js';
import { t } from '../i18n.js';
import {
  h, pageHeader, card, badge, dataTable, emptyState, button, iconButton,
  openModal, confirmDialog, field, input, select, switchControl, formRow,
  loadingBlock, toast, toastError, formatDate, relativeTime, copyText,
  showCredentials, statCard,
} from '../ui.js';
import { getState } from '../main.js';

const ROLES = ['admin', 'operator', 'viewer'];

function roleLabel(role) {
  const key = `users.role${role.charAt(0).toUpperCase()}${role.slice(1)}`;
  const label = t(key);
  return label === key ? role : label;
}

function roleTone(role) {
  return role === 'admin' ? 'brass' : role === 'operator' ? 'blue' : 'gray';
}

function languageName(locale) {
  return locale === 'en' ? 'English' : 'Bahasa Indonesia';
}

function randomPassword(length = 20) {
  const alphabet = 'ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789!@#$%^&*';
  const bytes = new Uint8Array(length);
  crypto.getRandomValues(bytes);
  return Array.from(bytes, (b) => alphabet[b % alphabet.length]).join('');
}

/* Tie a field label to the control it describes, so the association survives
   for screen readers and for click-to-focus. */
function labelled(label, control, hint, required) {
  const wrap = field(label, control, hint, required);
  const lab = wrap.querySelector('label.label');
  const ctl = control && control.matches && control.matches('input, select, textarea')
    ? control
    : wrap.querySelector('input, select, textarea');
  if (lab && ctl) {
    if (!ctl.id) ctl.id = `adm-${Math.random().toString(36).slice(2, 9)}`;
    lab.htmlFor = ctl.id;
  }
  return wrap;
}

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
  let users = [];
  const content = h('div', {});
  const me = getState() && getState().user ? getState().user : {};
  page.append(
    pageHeader(t('users.title'), t('users.subtitle'),
      button(t('users.createAction'), { variant: 'primary', icon: 'plus', onClick: openCreate })),
    content,
  );

  async function load() {
    content.replaceChildren(loadingBlock(t('common.loading')));
    try {
      users = (await api.get('/users')) || [];
      paint();
    } catch (err) {
      content.replaceChildren(card({
        body: stateBlock(t('users.loadFailed'), err.message,
          button(t('common.retry'), { icon: 'refresh', onClick: load }), 'alert', 'error'),
      }));
    }
  }

  function paint() {
    if (!users.length) {
      content.replaceChildren(card({
        body: stateBlock(t('users.noUsers'), t('users.noUsersHint'),
          button(t('users.createAction'), { variant: 'primary', icon: 'plus', onClick: openCreate }), 'users'),
      }));
      return;
    }
    const admins = users.filter((u) => u.role === 'admin').length;
    const operators = users.filter((u) => u.role === 'operator').length;
    const secured = users.filter((u) => u.totp_enabled).length;
    const table = card({
      class: 'adm-t-users',
      flush: true,
      body: dataTable([
        {
          label: t('users.username'),
          className: 'adm-col-user',
          render: (user) => h('div', { class: 'cell-name' },
            h('b', {}, user.username),
            user.id === me.id ? badge(t('users.self'), 'blue') : null),
        },
        {
          label: t('users.role'),
          className: 'adm-col-role',
          render: (user) => badge(roleLabel(user.role), roleTone(user.role)),
        },
        {
          label: t('users.language'),
          className: 'adm-col-lang',
          render: (user) => h('span', {
            class: 'mono muted',
            title: languageName(user.locale),
          }, (user.locale || 'id').toUpperCase()),
        },
        {
          label: t('users.twoFactor'),
          className: 'adm-col-2fa',
          render: (user) => (user.totp_enabled
            ? badge(t('users.twoFactorOn'), 'green')
            : h('span', { class: 'muted small' }, t('users.twoFactorOff'))),
        },
        {
          label: t('common.status'),
          className: 'adm-col-state',
          render: (user) => (user.disabled
            ? badge(t('users.disabled'), 'red')
            : badge(t('users.active'), 'green')),
        },
        {
          label: t('users.lastLogin'),
          className: 'adm-col-login',
          render: (user) => h('span', {
            class: 'muted small',
            title: user.last_login_at ? formatDate(user.last_login_at) : t('common.never'),
          }, user.last_login_at ? relativeTime(user.last_login_at) : t('common.never')),
        },
        {
          label: t('common.actions'),
          className: 'col-actions adm-col-actions',
          render: (user) => h('div', { class: 'row-actions' },
            iconButton('edit', t('common.edit'), () => openEdit(user)),
            iconButton('key', t('users.resetPassword'), () => resetPassword(user)),
            user.id === me.id ? null : iconButton('trash', t('common.delete'), () => remove(user), 'danger')),
        },
      ], users),
    });
    content.replaceChildren(
      h('div', { class: 'grid stats-grid' },
        statCard({ label: t('users.statTotal'), value: String(users.length), icon: 'users', tone: 'blue' }),
        statCard({ label: t('users.statAdmins'), value: String(admins), icon: 'shieldCheck', tone: 'amber' }),
        statCard({ label: t('users.statOperators'), value: String(operators), icon: 'terminal', tone: 'violet' }),
        statCard({ label: t('users.statTwoFactor'), value: String(secured), icon: 'lock', tone: 'green' })),
      table,
    );
  }

  function openCreate() {
    const username = input({ placeholder: 'johndoe', autocomplete: 'off', required: true });
    const password = input({ type: 'text', class: 'input mono', value: randomPassword() });
    const role = select(ROLES.map((r) => ({ value: r, label: roleLabel(r) })), { value: 'operator' });
    const locale = select([
      { value: 'id', label: 'Bahasa Indonesia' },
      { value: 'en', label: 'English' },
    ], { value: 'id' });
    const modal = openModal({
      title: t('users.createTitle'),
      size: 'modal-sm',
      body: [
        labelled(t('users.username'), username, t('users.usernameHint'), true),
        labelled(t('users.password'), h('div', { class: 'input-group' }, password,
          button(t('users.generate'), { icon: 'zap', onClick: () => { password.value = randomPassword(); } })), t('users.passwordAuto')),
        formRow(
          labelled(t('users.role'), role),
          labelled(t('users.language'), locale)),
      ],
      footer: [
        button(t('common.cancel'), { onClick: () => modal.close() }),
        button(t('users.createAction'), {
          variant: 'primary',
          onClick: async () => {
            try {
              await api.post('/users', {
                username: username.value.trim(),
                password: password.value,
                role: role.value,
                locale: locale.value,
              });
              modal.close();
              toast(t('users.created'), 'success');
              showCredentials({
                title: t('users.created'),
                username: username.value.trim(),
                password: password.value,
                note: t('common.credentialsOnce'),
              });
              await load();
            } catch (err) {
              toastError(err);
            }
          },
        }),
      ],
    });
    modal.el.classList.add('adm-modal');
  }

  function openEdit(user) {
    const role = select(ROLES.map((r) => ({ value: r, label: roleLabel(r) })), { value: user.role });
    const locale = select([
      { value: 'id', label: 'Bahasa Indonesia' },
      { value: 'en', label: 'English' },
    ], { value: user.locale || 'id' });
    const disabled = switchControl(t('users.disable'), user.disabled, { disabled: user.id === me.id });
    const modal = openModal({
      title: `${t('users.editTitle')} · ${user.username}`,
      size: 'modal-sm',
      body: [
        formRow(
          labelled(t('users.role'), role),
          labelled(t('users.language'), locale)),
        h('div', { class: 'field field-switch' }, disabled,
          user.id === me.id ? h('div', { class: 'hint' }, t('users.cannotDisableSelf')) : null),
        h('dl', { class: 'kv' },
          h('dt', {}, t('users.twoFactor')), h('dd', {}, user.totp_enabled ? t('users.twoFactorOn') : t('users.twoFactorOff')),
          h('dt', {}, t('common.created')), h('dd', {}, formatDate(user.created_at)),
          h('dt', {}, t('users.lastLogin')), h('dd', {}, user.last_login_at ? formatDate(user.last_login_at) : t('common.never'))),
      ],
      footer: [
        button(t('common.cancel'), { onClick: () => modal.close() }),
        button(t('users.saveChanges'), {
          variant: 'primary',
          onClick: async () => {
            try {
              await api.patch(`/users/${user.id}`, {
                role: role.value,
                locale: locale.value,
                disabled: disabled.querySelector('input').checked,
              });
              modal.close();
              toast(t('users.updated'), 'success');
              await load();
            } catch (err) {
              toastError(err);
            }
          },
        }),
      ],
    });
    modal.el.classList.add('adm-modal');
  }

  function resetPassword(user) {
    const password = input({ type: 'text', class: 'input mono', value: randomPassword() });
    const modal = openModal({
      title: `${t('users.resetPassword')} · ${user.username}`,
      size: 'modal-sm',
      body: [
        labelled(t('users.newPassword'), h('div', { class: 'input-group' }, password,
          button(t('common.copy'), { icon: 'copy', onClick: () => copyText(password.value) }))),
        h('div', { class: 'hint' }, t('users.resetHint')),
      ],
      footer: [
        button(t('common.cancel'), { onClick: () => modal.close() }),
        button(t('users.resetPassword'), {
          variant: 'primary',
          onClick: async () => {
            try {
              await api.post(`/users/${user.id}/password`, { password: password.value });
              modal.close();
              toast(t('users.resetDone'), 'success');
              showCredentials({
                title: t('users.resetDone'),
                username: user.username,
                password: password.value,
                note: t('common.credentialsOnce'),
              });
            } catch (err) {
              toastError(err);
            }
          },
        }),
      ],
    });
    modal.el.classList.add('adm-modal');
  }

  async function remove(user) {
    const ok = await confirmDialog({
      title: t('users.deleteTitle'),
      message: t('users.deleteConfirm', { name: user.username }),
      confirmText: t('common.delete'),
      danger: true,
      requireText: user.username,
    });
    if (!ok) return;
    try {
      await api.del(`/users/${user.id}`, {});
      toast(t('users.deleted'), 'success');
      await load();
    } catch (err) {
      toastError(err);
    }
  }

  await load();
  return page;
}
