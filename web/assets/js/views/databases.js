import { api } from '../api.js';
import { t } from '../i18n.js';
import {
  h, pageHeader, card, badge, dataTable, emptyState, button, iconButton,
  openModal, confirmDialog, field, input, select, formRow, loadingBlock,
  toast, toastError, iconSpan, formatBytes, formatDate, copyText,
  showCredentials, statCard,
} from '../ui.js';
import { canWrite } from '../main.js';
import { navigate } from '../router.js';

function randomPassword(length = 24) {
  const alphabet = 'ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789!@#$%^&*';
  const bytes = new Uint8Array(length);
  crypto.getRandomValues(bytes);
  return Array.from(bytes, (b) => alphabet[b % alphabet.length]).join('');
}

/* Tie a field label to the control it describes. */
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

/* The database module can be switched off, missing its client, or simply
   unreachable — each one deserves its own sentence. */
function describeError(err) {
  const code = err && err.code;
  if (code === 'databases.client_missing') {
    return { title: t('databases.clientMissing'), text: t('databases.clientMissingHint'), icon: 'database' };
  }
  if (code === 'databases.unavailable') {
    return { title: t('databases.moduleMissing'), text: t('databases.moduleMissingHint'), icon: 'database' };
  }
  if (code === 'databases.unreachable') {
    return { title: t('databases.unreachable'), text: (err && err.message) || '', icon: 'alert', tone: 'error' };
  }
  return { title: t('databases.loadFailed'), text: (err && err.message) || '', icon: 'alert', tone: 'error' };
}

export async function render(ctx) {
  const { outlet } = ctx;
  const page = h('div', { class: 'page admin-page' });
  outlet.replaceChildren(page);
  let databases = [];
  let users = [];
  let tab = 'databases';
  let dbError = null;
  let userError = null;
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
    content.replaceChildren(loadingBlock(t('common.loading')));
    dbError = null;
    userError = null;
    const [dbs, dbUsers] = await Promise.all([
      api.get('/databases').then((v) => ({ value: v })).catch((err) => ({ error: err })),
      api.get('/databases/users').then((v) => ({ value: v })).catch((err) => ({ error: err })),
    ]);
    if (dbs.error) {
      dbError = dbs.error;
      databases = [];
    } else {
      databases = dbs.value || [];
    }
    if (dbUsers.error) {
      userError = dbUsers.error;
      users = [];
    } else {
      users = dbUsers.value || [];
    }
    paint();
  }

  function paint() {
    if (createAction) createAction.hidden = !!(tab === 'databases' && dbError);
    if (tab === 'users') {
      paintUsers();
      return;
    }
    paintDatabases();
  }

  function paintDatabases() {
    if (dbError) {
      const info = describeError(dbError);
      const action = dbError.code === 'databases.client_missing' && canWrite()
        ? button(t('databases.installClient'), { variant: 'primary', icon: 'box', onClick: () => navigate('/apps') })
        : (info.tone === 'error'
          ? button(t('common.retry'), { icon: 'refresh', onClick: load })
          : null);
      content.replaceChildren(card({
        body: stateBlock(info.title, info.text, action, info.icon, info.tone),
      }));
      return;
    }
    if (!databases.length) {
      content.replaceChildren(card({
        body: stateBlock(t('databases.noDatabases'), t('databases.noDatabasesHint'),
          canWrite() ? button(t('databases.create'), { variant: 'primary', icon: 'plus', onClick: openCreate }) : null,
          'database'),
      }));
      return;
    }
    const totalSize = databases.reduce((sum, db) => sum + (Number(db.size_bytes) || 0), 0);
    const totalTables = databases.reduce((sum, db) => sum + (Number(db.tables) || 0), 0);
    const table = card({
      class: 'adm-t-databases',
      flush: true,
      body: dataTable([
        {
          label: t('databases.name'),
          className: 'adm-col-name',
          render: (db) => h('div', { class: 'cell-name' },
            h('b', { class: 'mono' }, db.name),
            db.present ? null : badge(t('databases.missing'), 'red')),
        },
        {
          label: t('databases.username'),
          className: 'adm-col-user',
          render: (db) => h('span', { class: 'mono muted' }, db.username || '—'),
        },
        {
          label: t('databases.size'),
          className: 'col-num adm-col-size',
          render: (db) => formatBytes(db.size_bytes),
        },
        {
          label: t('databases.tables'),
          className: 'col-num adm-col-tables',
          render: (db) => (db.tables ?? '—'),
        },
        {
          label: t('common.created'),
          className: 'adm-col-created',
          render: (db) => h('span', { class: 'muted small' }, formatDate(db.created_at)),
        },
        {
          label: t('common.actions'),
          className: 'col-actions adm-col-actions',
          render: (db) => h('div', { class: 'row-actions' },
            canWrite() ? iconButton('archive', t('databases.backup'), () => backup(db)) : null,
            canWrite() ? iconButton('key', t('databases.resetPassword'), () => resetPassword(db)) : null,
            canWrite() ? iconButton('trash', t('common.delete'), () => remove(db), 'danger') : null),
        },
      ], databases),
    });
    content.replaceChildren(
      h('div', { class: 'grid stats-grid' },
        statCard({ label: t('databases.title'), value: String(databases.length), icon: 'database', tone: 'blue' }),
        statCard({ label: t('databases.users'), value: String(users.length), icon: 'users', tone: 'violet' }),
        statCard({ label: t('databases.totalSize'), value: formatBytes(totalSize), icon: 'disk', tone: 'amber' }),
        statCard({ label: t('databases.tables'), value: String(totalTables), icon: 'layers', tone: 'green' })),
      table,
    );
  }

  function paintUsers() {
    if (userError) {
      const info = describeError(userError);
      const action = info.tone === 'error'
        ? button(t('common.retry'), { icon: 'refresh', onClick: load })
        : null;
      content.replaceChildren(card({
        body: stateBlock(info.title, info.text, action, info.icon, info.tone),
      }));
      return;
    }
    if (!users.length) {
      content.replaceChildren(card({
        body: stateBlock(t('databases.noUsers'), t('databases.noUsersHint'),
          canWrite() ? button(t('databases.createUser'), { variant: 'primary', icon: 'plus', onClick: openCreateUser }) : null,
          'users'),
      }));
      return;
    }
    content.replaceChildren(card({
      class: 'adm-t-dbusers',
      flush: true,
      title: t('databases.users'),
      actions: canWrite()
        ? h('div', { class: 'row gap-sm' },
          badge(String(users.length), 'gray'),
          button(t('databases.createUser'), { size: 'sm', icon: 'plus', onClick: openCreateUser }))
        : badge(String(users.length), 'gray'),
      body: dataTable([
        {
          label: t('databases.username'),
          className: 'adm-col-user',
          render: (u) => h('b', { class: 'mono' }, u.user),
        },
        {
          label: t('databases.host'),
          className: 'adm-col-host',
          render: (u) => h('span', { class: 'mono muted', title: u.host ? '' : t('databases.anyHost') }, u.host || '%'),
        },
        {
          label: t('common.actions'),
          className: 'col-actions adm-col-actions',
          render: (u) => (canWrite()
            ? h('div', { class: 'row-actions' },
              iconButton('trash', t('databases.deleteUser'), () => removeUser(u), 'danger'))
            : null),
        },
      ], users),
    }));
  }

  function openCreate() {
    const name = input({ placeholder: 'app_db', required: true });
    const username = input({ placeholder: 'app_user' });
    const password = input({ type: 'text', class: 'input mono' });
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
        labelled(t('databases.name'), name, t('databases.nameHint'), true),
        labelled(t('databases.username'), username, t('databases.userHint')),
        labelled(t('databases.password'), h('div', { class: 'input-group' }, password, generate), t('databases.passwordHint')),
      ],
      footer: [
        button(t('common.cancel'), { onClick: () => modal.close() }),
        button(t('databases.create'), {
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
    modal.el.classList.add('adm-modal');
  }

  function openCreateUser() {
    const username = input({ placeholder: 'app_user', required: true });
    const password = input({ type: 'text', class: 'input mono' });
    const database = select(
      [{ value: '', label: t('databases.grantAll') }, ...databases.map((db) => ({ value: db.name, label: db.name }))],
      { value: databases[0] ? databases[0].name : '' },
    );
    const modal = openModal({
      title: t('databases.createUserTitle'),
      size: 'modal-sm',
      body: [
        labelled(t('databases.username'), username, '', true),
        labelled(t('databases.password'), h('div', { class: 'input-group' }, password,
          button(t('databases.generate'), { icon: 'zap', onClick: () => { password.value = randomPassword(); } })), t('databases.passwordHint')),
        labelled(t('databases.userFor'), database),
      ],
      footer: [
        button(t('common.cancel'), { onClick: () => modal.close() }),
        button(t('databases.createUser'), {
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
    modal.el.classList.add('adm-modal');
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
        h('div', { class: 'cell-sub mono mb2' }, db.name),
        labelled(t('databases.newPassword'), h('div', { class: 'input-group' }, password,
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
                database: (res && res.database) || db.name,
                username: db.username || '',
                password: (res && res.password) || password.value,
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
