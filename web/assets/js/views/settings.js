import { api } from '../api.js';
import { t, setLocale, loadLocale } from '../i18n.js';
import {
  h, pageHeader, card, badge, button, field, input, select, iconButton,
  switchControl, formRow, emptyState, loadingBlock, toast, toastError,
  formatDate, copyText, repoLink,
} from '../ui.js';
import { isAdmin, applyTheme, setPanelName } from '../main.js';
import { navigate } from '../router.js';

const THEMES = [
  { value: 'system', labelKey: 'settings.themeSystem' },
  { value: 'light', labelKey: 'settings.themeLight' },
  { value: 'dark', labelKey: 'settings.themeDark' },
];

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

/* A path stays in monospace and can be copied without leaving the row. */
function copyValue(value) {
  if (!value) return h('dd', { class: 'mono small' }, '—');
  return h('dd', {}, h('span', { class: 'row gap-sm adm-copy-row' },
    h('span', { class: 'mono small ellipsis' }, value),
    iconButton('copy', t('common.copy'), () => copyText(value))));
}

export async function render(ctx) {
  const { outlet } = ctx;
  const page = h('div', { class: 'page admin-page' });
  outlet.replaceChildren(page);
  let data = null;
  const content = h('div', {});
  page.append(
    pageHeader(t('settings.title'), t('settings.subtitle'),
      button(t('common.refresh'), { icon: 'refresh', onClick: load })),
    content,
  );

  async function load() {
    content.replaceChildren(loadingBlock(t('common.loading')));
    try {
      data = await api.get('/settings');
      paint();
    } catch (err) {
      content.replaceChildren(card({
        body: stateBlock(t('settings.loadFailed'), err.message,
          button(t('common.retry'), { icon: 'refresh', onClick: load }), 'alert', 'error'),
      }));
    }
  }

  function paint() {
    const settings = data.settings || {};
    const admin = isAdmin();
    const panelName = input({ value: settings.panel_name || '', maxlength: '64', disabled: !admin });
    const locale = select([
      { value: 'id', label: 'Bahasa Indonesia' },
      { value: 'en', label: 'English' },
    ], { value: settings.locale || 'id', disabled: !admin });
    const theme = select(THEMES.map((item) => ({ value: item.value, label: t(item.labelKey) })),
      { value: settings.theme || 'system', disabled: !admin });
    const saveGeneral = async () => {
      try {
        data = await api.patch('/settings', {
          settings: { panel_name: panelName.value.trim(), locale: locale.value, theme: theme.value },
        });
        setPanelName(panelName.value.trim() || 'GoPanel');
        applyTheme(theme.value);
        if (locale.value !== document.documentElement.lang) {
          setLocale(locale.value);
          await loadLocale(locale.value);
          navigate('/settings');
          return;
        }
        toast(t('settings.savedGeneral'), 'success');
        paint();
      } catch (err) {
        toastError(err);
      }
    };
    const generalCard = card({
      title: t('settings.general'),
      icon: 'settings',
      body: h('div', {},
        formRow(
          labelled(t('settings.panelName'), panelName, t('settings.panelNameHint')),
          labelled(t('settings.language'), locale)),
        labelled(t('settings.theme'), theme, t('settings.themeHint')),
        admin
          ? h('div', { class: 'form-actions' },
            button(t('settings.saveGeneral'), { variant: 'primary', icon: 'save', onClick: saveGeneral }))
          : h('div', { class: 'hint' }, t('settings.readOnly'))),
    });
    const acmeEmail = input({ value: settings.acme_email || '', type: 'email', placeholder: 'admin@example.com', disabled: !admin });
    const staging = switchControl(t('settings.acmeStaging'), settings.acme_staging === 'true', { disabled: !admin });
    const saveAcme = async () => {
      try {
        data = await api.patch('/settings', {
          settings: {
            acme_email: acmeEmail.value.trim(),
            acme_staging: staging.querySelector('input').checked ? 'true' : 'false',
          },
        });
        toast(t('settings.savedAcme'), 'success');
        paint();
      } catch (err) {
        toastError(err);
      }
    };
    const acmeCard = card({
      title: t('settings.acme'),
      icon: 'shieldCheck',
      body: h('div', {},
        h('div', { class: 'muted small mb2' }, t('settings.acmeHint')),
        labelled(t('settings.acmeEmail'), acmeEmail, t('settings.acmeEmailHint')),
        h('div', { class: 'field field-switch' }, staging,
          h('div', { class: 'hint' }, t('settings.acmeStagingHint'))),
        admin
          ? h('div', { class: 'form-actions' },
            button(t('settings.saveAcme'), { variant: 'primary', icon: 'save', onClick: saveAcme }))
          : h('div', { class: 'hint' }, t('settings.readOnly'))),
    });
    const info = card({
      title: t('settings.about'),
      icon: 'info',
      class: 'card-about',
      body: h('dl', { class: 'kv kv-rows' },
        h('dt', {}, t('settings.version')), h('dd', {}, h('span', { class: 'mono' }, data.version || '—')),
        h('dt', {}, t('settings.started')), h('dd', {}, formatDate(Math.floor(new Date(data.started).getTime() / 1000))),
        h('dt', {}, t('settings.panelUrl')), copyValue(data.panel_url),
        h('dt', {}, t('settings.listen')), h('dd', { class: 'mono' }, data.listen || '—'),
        h('dt', {}, t('settings.dataDir')), copyValue(data.data_dir),
        h('dt', {}, t('settings.backupDir')), copyValue(data.backup_dir),
        h('dt', {}, t('settings.logDir')), copyValue(data.log_dir),
        h('dt', {}, t('settings.sessionIdle')), h('dd', {}, `${data.session_idle_hours ?? '—'} ${t('settings.hours')}`),
        h('dt', {}, t('settings.sessionAbsolute')), h('dd', {}, `${data.session_absolute_hours ?? '—'} ${t('settings.hours')}`)),
      foot: repoLink(),
    });
    const tls = data.tls || {};
    const certInput = h('input', { type: 'file', accept: '.crt,.pem,.cer', class: 'input' });
    const keyInput = h('input', { type: 'file', accept: '.key,.pem', class: 'input' });
    const uploadTLS = async () => {
      const cert = certInput.files && certInput.files[0];
      const key = keyInput.files && keyInput.files[0];
      if (!cert || !key) { toast(t('settings.tlsMissing'), 'warn'); return; }
      const form = new FormData();
      form.append('cert', cert, cert.name);
      form.append('key', key, key.name);
      try {
        data = await api.upload('/settings/tls', form);
        toast(t('settings.tlsUploaded'), 'success');
        paint();
      } catch (err) {
        toastError(err);
      }
    };
    const tlsCard = card({
      title: t('settings.tls'),
      icon: 'lock',
      actions: tls.installed ? badge(t('settings.tlsInstalled'), 'green') : badge(t('settings.tlsNone'), 'gray'),
      body: h('div', {},
        h('dl', { class: 'kv' },
          h('dt', {}, t('settings.certFile')), h('dd', { class: 'mono small' }, tls.cert_file || '—'),
          h('dt', {}, t('settings.keyFile')), h('dd', { class: 'mono small' }, tls.key_file || '—')),
        admin
          ? h('div', { class: 'mt3' },
            formRow(
              labelled(t('settings.certificate'), certInput),
              labelled(t('settings.privateKey'), keyInput)),
            h('div', { class: 'form-actions' },
              button(t('settings.uploadTls'), { variant: 'primary', icon: 'upload', onClick: uploadTLS })))
          : null,
        h('div', { class: 'muted small mt2' }, t('settings.tlsHint'))),
    });
    content.replaceChildren(
      h('div', { class: 'settings-layout' },
        h('div', { class: 'stack' }, generalCard, acmeCard, tlsCard),
        h('aside', { class: 'settings-aside' }, info)),
    );
  }

  await load();
  return page;
}
