import { api } from '../api.js';
import { t } from '../i18n.js';
import {
  h, pageHeader, card, badge, dataTable, emptyState, button, iconButton,
  openModal, confirmDialog, field, input, select, switchControl, formRow,
  loadingBlock, toast, toastError, iconSpan, formatDate, relativeTime, copyText,
  showCredentials,
} from '../ui.js';
import { getState } from '../main.js';

const ROLES = ['admin', 'operator', 'viewer'];

function roleLabel(role) {
  const key = `users.role${role.charAt(0).toUpperCase()}${role.slice(1)}`;
  const label = t(key);
  return label === key ? role : label;
}

function roleTone(role) {
  return role === 'admin' ? 'red' : role === 'operator' ? 'blue' : 'gray';
}

function randomPassword(length = 20) {
  const alphabet = 'ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789!@#$%^&*';
  const bytes = new Uint8Array(length);
  crypto.getRandomValues(bytes);
  return Array.from(bytes, (b) => alphabet[b % alphabet.length]).join('');
}

export async function render(ctx) {
  const { outlet } = ctx;
  const page = h('div', { class: 'page' });
  outlet.replaceChildren(page);
  let users = [];
  const content = h('div', {});
  const me = getState() && getState().user ? getState().user : {};

  page.append(
    pageHeader(t('users.title'), t('users.subtitle'),
      button(t('users.create'), { variant: 'primary', icon: 'plus', onClick: openCreate })),
    content,
  );

  async function load() {
    content.replaceChildren(loadingBlock());
    try {
      users = (await api.get('/users')) || [];
      paint();
    } catch (err) {
      content.replaceChildren(card({ body: h('div', { class: 'alert alert-danger' }, err.message) }));
    }
  }

  function paint() {
    if (!users.length) {
      content.replaceChildren(card({ body: emptyState(t('users.noUsers'), t('users.noUsersHint'), null, 'users') }));
      return;
    }
    content.replaceChildren(card({
      flush: true,
      body: dataTable([
        { label: t('users.username'), render: (user) => h('div', {},
          h('div', { class: 'row gap-sm' },
            h('b', {}, user.username),
            user.id === me.id ? badge(t('users.self'), 'blue') : null),
          h('div', { class: 'muted small' }, user.locale ? user.locale.toUpperCase() : '—')) },
        { label: t('users.role'), render: (user) => badge(roleLabel(user.role), roleTone(user.role)) },
        { label: t('users.twoFactor'), render: (user) => user.totp_enabled
          ? badge(t('users.twoFactorOn'), 'green')
          : h('span', { class: 'muted small' }, t('users.twoFactorOff')) },
        { label: t('common.status'), render: (user) => user.disabled
          ? badge(t('users.disabled'), 'red')
          : badge(t('users.active'), 'green') },
        { label: t('users.lastLogin'), render: (user) => h('span', { class: 'muted small', title: formatDate(user.last_login_at) },
          user.last_login_at ? relativeTime(user.last_login_at) : t('common.never')) },
        { label: t('common.actions'), className: 'col-actions', render: (user) => h('div', { class: 'row-actions' },
          iconButton('edit', t('common.edit'), () => openEdit(user)),
          iconButton('key', t('users.resetPassword'), () => resetPassword(user)),
          user.id === me.id ? null : iconButton('trash', t('common.delete'), () => remove(user), 'danger')) },
      ], users),
    }));
  }

  function openCreate() {
    const username = input({ placeholder: 'johndoe', autocomplete: 'off', required: true });
    const password = input({ type: 'text', class: 'input mono', value: randomPassword() });
    const role = select(ROLES.map((r) => ({ value: r, label: roleLabel(r) })), { value: 'operator' });
    const locale = select([{ value: 'id', label: 'Bahasa Indonesia' }, { value: 'en', label: 'English' }], { value: 'id' });

    const modal = openModal({
      title: t('users.createTitle'),
      size: 'modal-sm',
      body: [
        field(t('users.username'), username, t('users.usernameHint'), true),
        field(t('users.password'), h('div', { class: 'input-group' }, password,
          button(t('databases.generate'), { icon: 'zap', onClick: () => { password.value = randomPassword(); } })), t('users.passwordHint')),
        formRow(field(t('users.role'), role), field(t('users.language'), locale)),
      ],
      footer: [
        button(t('common.cancel'), { onClick: () => modal.close() }),
        button(t('common.create'), {
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
  }

  function openEdit(user) {
    const role = select(ROLES.map((r) => ({ value: r, label: roleLabel(r) })), { value: user.role });
    const locale = select([{ value: 'id', label: 'Bahasa Indonesia' }, { value: 'en', label: 'English' }], { value: user.locale || 'id' });
    const disabled = switchControl(t('users.disable'), user.disabled, { disabled: user.id === me.id });

    const modal = openModal({
      title: `${t('users.editTitle')} · ${user.username}`,
      size: 'modal-sm',
      body: [
        formRow(field(t('users.role'), role), field(t('users.language'), locale)),
        h('div', { class: 'field' }, disabled, user.id === me.id ? h('div', { class: 'hint' }, t('users.cannotDisableSelf')) : null),
        h('dl', { class: 'kv' },
          h('dt', {}, t('users.twoFactor')), h('dd', {}, user.totp_enabled ? t('users.twoFactorOn') : t('users.twoFactorOff')),
          h('dt', {}, t('common.created')), h('dd', {}, formatDate(user.created_at)),
          h('dt', {}, t('users.lastLogin')), h('dd', {}, user.last_login_at ? formatDate(user.last_login_at) : t('common.never'))),
      ],
      footer: [
        button(t('common.cancel'), { onClick: () => modal.close() }),
        button(t('common.save'), {
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
  }

  function resetPassword(user) {
    const password = input({ type: 'text', class: 'input mono', value: randomPassword() });
    const modal = openModal({
      title: `${t('users.resetPassword')} · ${user.username}`,
      size: 'modal-sm',
      body: [
        field(t('users.newPassword'), h('div', { class: 'input-group' }, password,
          button(t('common.copy'), { icon: 'copy', onClick: () => copyText(password.value) }))),
        h('div', { class: 'muted small' }, t('users.resetHint')),
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
