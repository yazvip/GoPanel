import { api } from '../api.js';
import { t } from '../i18n.js';
import {
  h, pageHeader, card, badge, dataTable, emptyState, button, iconButton,
  openModal, confirmDialog, field, input, select, formRow, loadingBlock,
  toast, toastError, iconSpan, formatBytes, formatDate, copyText, showCredentials,
} from '../ui.js';
import { canWrite } from '../main.js';
import { navigate } from '../router.js';

function randomPassword(length = 24) {
  const alphabet = 'ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789!@#$%^&*';
  const bytes = new Uint8Array(length);
  crypto.getRandomValues(bytes);
  return Array.from(bytes, (b) => alphabet[b % alphabet.length]).join('');
}

export async function render(ctx) {
  const { outlet } = ctx;
  const page = h('div', { class: 'page' });
  outlet.replaceChildren(page);
  let databases = [];
  let users = [];
  let tab = 'databases';
  let loadError = null;
  const content = h('div', {});

  const createAction = canWrite()
    ? button(t('databases.create'), { variant: 'primary', icon: 'plus', onClick: openCreate })
    : null;

  page.append(
    pageHeader(t('databases.title'), t('databases.subtitle'), createAction),
    h('div', { class: 'tabs' },
      h('button', { class: 'tab active', type: 'button', dataset: { tab: 'databases' }, onClick: () => switchTab('databases') }, t('nav.databases')),
      h('button', { class: 'tab', type: 'button', dataset: { tab: 'users' }, onClick: () => switchTab('users') }, t('databases.users'))),
    content,
  );

  function switchTab(next) {
    tab = next;
    document.querySelectorAll('.tab').forEach((el) => el.classList.toggle('active', el.dataset.tab === next));
    paint();
  }

  async function load() {
    content.replaceChildren(loadingBlock());
    loadError = null;
    try {
      const [dbs, dbUsers] = await Promise.all([
        api.get('/databases').catch((err) => {
          if (err.code === 'databases.unavailable') return [];
          throw err;
        }),
        api.get('/databases/users').catch((err) => {
          if (err.code === 'databases.unavailable') return [];
          throw err;
        }),
      ]);
      databases = dbs || [];
      users = dbUsers || [];
      paint();
    } catch (err) {
      loadError = err;
      paint();
    }
  }

  function paintLoadError() {
    if (loadError.code === 'databases.client_missing') {
      content.replaceChildren(card({
        body: emptyState(t('databases.clientMissing'), t('databases.clientMissingHint'),
          canWrite() ? button(t('nav.apps'), { variant: 'primary', icon: 'box', onClick: () => navigate('#/apps') }) : null, 'database'),
      }));
      return;
    }
    content.replaceChildren(card({
      body: h('div', { class: 'alert alert-danger' }, iconSpan('alert', 16), h('div', {}, loadError.message)),
    }));
  }

  function paint() {
    if (loadError) {
      if (createAction) createAction.hidden = true;
      paintLoadError();
      return;
    }
    if (createAction) createAction.hidden = false;
    if (tab === 'users') {
      paintUsers();
      return;
    }
    if (!databases.length) {
      content.replaceChildren(card({
        body: emptyState(t('databases.noDatabases'), t('databases.noDatabasesHint'),
          canWrite() ? button(t('databases.create'), { variant: 'primary', icon: 'plus', onClick: openCreate }) : null, 'database'),
      }));
      return;
    }
    content.replaceChildren(card({
      flush: true,
      body: dataTable([
        { label: t('databases.name'), render: (db) => h('div', {},
          h('div', { class: 'row gap-sm' },
            h('b', {}, db.name),
            db.present ? null : badge(t('databases.missing'), 'red')),
          h('div', { class: 'muted small mono' }, db.username || '—')) },
        { label: t('databases.size'), render: (db) => formatBytes(db.size_bytes) },
        { label: t('databases.tables'), render: (db) => db.tables ?? '—' },
        { label: t('common.created'), render: (db) => h('span', { class: 'muted small' }, formatDate(db.created_at)) },
        { label: t('common.actions'), className: 'col-actions', render: (db) => h('div', { class: 'row-actions' },
          canWrite() ? iconButton('archive', t('databases.backup'), () => backup(db)) : null,
          canWrite() ? iconButton('key', t('databases.resetPassword'), () => resetPassword(db)) : null,
          canWrite() ? iconButton('trash', t('common.delete'), () => remove(db), 'danger') : null) },
      ], databases),
    }));
  }

  function paintUsers() {
    if (!users.length) {
      content.replaceChildren(card({
        body: emptyState(t('databases.noUsers'), t('databases.noUsersHint'),
          canWrite() ? button(t('databases.createUser'), { variant: 'primary', icon: 'plus', onClick: openCreateUser }) : null, 'users'),
      }));
      return;
    }
    content.replaceChildren(card({
      flush: true,
      actions: canWrite() ? button(t('databases.createUser'), { size: 'sm', icon: 'plus', onClick: openCreateUser }) : null,
      body: dataTable([
        { label: t('databases.username'), render: (u) => h('b', { class: 'mono' }, u.user) },
        { label: t('databases.host'), render: (u) => h('span', { class: 'mono muted' }, u.host || '%') },
        { label: t('common.actions'), className: 'col-actions', render: (u) => canWrite()
          ? h('div', { class: 'row-actions' },
            iconButton('trash', t('databases.deleteUser'), () => removeUser(u), 'danger'))
          : null },
      ], users),
    }));
  }

  function openCreate() {
    const name = input({ placeholder: 'app_db', required: true });
    const username = input({ placeholder: 'app_user' });
    const password = input({ type: 'text', class: 'input mono', placeholder: t('databases.passwordHint') });
    const generate = button(t('databases.generate'), {
      icon: 'zap',
      onClick: () => { password.value = randomPassword(); },
    });
    name.addEventListener('input', () => {
      if (!username.dataset.touched) username.value = name.value.trim().replace(/[^A-Za-z0-9_]/g, '_').slice(0, 24);
    });
    username.addEventListener('input', () => { username.dataset.touched = '1'; });

    const modal = openModal({
      title: t('databases.createTitle'),
      size: 'modal-sm',
      body: [
        field(t('databases.name'), name, 'a-z, 0-9 and underscore', true),
        field(t('databases.username'), username, t('databases.userFor') + ': ' + t('common.optional')),
        field(t('databases.password'), h('div', { class: 'input-group' }, password, generate), t('databases.passwordHint')),
      ],
      footer: [
        button(t('common.cancel'), { onClick: () => modal.close() }),
        button(t('common.create'), {
          variant: 'primary',
          onClick: async () => {
            try {
              const created = await api.post('/databases', {
                name: name.value.trim(),
                username: username.value.trim(),
                password: password.value,
              });
              modal.close();
              toast(t('databases.created'), 'success');
              showCredentials({
                title: t('databases.created'),
                database: created.database ? created.database.name : name.value.trim(),
                username: created.username,
                password: created.password,
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

  function openCreateUser() {
    const username = input({ placeholder: 'app_user', required: true });
    const password = input({ type: 'text', class: 'input mono' });
    const database = select(
      [{ value: '', label: t('databases.grantAll') }, ...databases.map((db) => ({ value: db.name, label: db.name }))],
      { value: databases[0] ? databases[0].name : '' },
    );
    const modal = openModal({
      title: t('databases.createUser'),
      size: 'modal-sm',
      body: [
        field(t('databases.username'), username, '', true),
        field(t('databases.password'), h('div', { class: 'input-group' }, password,
          button(t('databases.generate'), { icon: 'zap', onClick: () => { password.value = randomPassword(); } })), t('databases.passwordHint')),
        field(t('databases.userFor'), database),
      ],
      footer: [
        button(t('common.cancel'), { onClick: () => modal.close() }),
        button(t('common.create'), {
          variant: 'primary',
          onClick: async () => {
            try {
              const created = await api.post('/databases/users', {
                username: username.value.trim(),
                password: password.value,
                database: database.value,
              });
              modal.close();
              toast(t('databases.userCreated'), 'success');
              showCredentials({
                title: t('databases.userCreated'),
                database: created.database || database.value,
                username: created.username,
                password: created.password,
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

  async function backup(db) {
    try {
      const job = await api.post(`/databases/${encodeURIComponent(db.name)}/backup`, {});
      toast(t('databases.backupStarted'), 'success');
      if (job && job.id) navigate('/jobs');
    } catch (err) {
      toastError(err);
    }
  }

  function resetPassword(db) {
    const password = input({ type: 'text', class: 'input mono', value: randomPassword() });
    const modal = openModal({
      title: t('databases.resetTitle'),
      size: 'modal-sm',
      body: [
        h('div', { class: 'muted small mb2' }, db.name),
        field(t('databases.newPassword'), h('div', { class: 'input-group' }, password,
          button(t('common.copy'), { icon: 'copy', onClick: () => copyText(password.value) }))),
      ],
      footer: [
        button(t('common.cancel'), { onClick: () => modal.close() }),
        button(t('databases.resetPassword'), {
          variant: 'primary',
          onClick: async () => {
            try {
              const res = await api.post(`/databases/${encodeURIComponent(db.name)}/password`, { password: password.value });
              modal.close();
              toast(t('databases.passwordReset'), 'success');
              showCredentials({
                title: t('databases.passwordReset'),
                database: res.database,
                username: db.username || '',
                password: res.password,
              });
            } catch (err) {
              toastError(err);
            }
          },
        }),
      ],
    });
  }

  async function remove(db) {
    const ok = await confirmDialog({
      title: t('common.delete'),
      message: t('databases.deleteConfirm', { name: db.name }),
      confirmText: t('common.delete'),
      danger: true,
      requireText: db.name,
    });
    if (!ok) return;
    try {
      await api.del(`/databases/${encodeURIComponent(db.name)}`, { confirm: db.name });
      toast(t('databases.deleted'), 'success');
      await load();
    } catch (err) {
      toastError(err);
    }
  }

  async function removeUser(user) {
    const ok = await confirmDialog({
      title: t('databases.deleteUser'),
      message: t('databases.deleteUserConfirm', { name: user.user }),
      confirmText: t('common.delete'),
      danger: true,
    });
    if (!ok) return;
    try {
      await api.del(`/databases/users/${encodeURIComponent(user.user)}`, {});
      toast(t('databases.userDeleted'), 'success');
      await load();
    } catch (err) {
      toastError(err);
    }
  }

  await load();
  return page;
}
