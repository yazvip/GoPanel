import { api } from '../api.js';
import { t, setLocale, loadLocale } from '../i18n.js';
import {
  h, pageHeader, card, badge, button, openModal, field, input, select,
  switchControl, formRow, loadingBlock, toast, toastError, iconSpan, formatDate,
  copyText,
} from '../ui.js';
import { isAdmin, applyTheme, setPanelName } from '../main.js';
import { navigate } from '../router.js';

const THEMES = [
  { value: 'system', labelKey: 'settings.themeSystem' },
  { value: 'light', labelKey: 'settings.themeLight' },
  { value: 'dark', labelKey: 'settings.themeDark' },
];

const REPO_URL = 'https://github.com/yazvip/GoPanel';

export async function render(ctx) {
  const { outlet } = ctx;
  const page = h('div', { class: 'page' });
  outlet.replaceChildren(page);
  let data = null;
  const content = h('div', {});

  page.append(
    pageHeader(t('settings.title'), t('settings.subtitle'),
      button(t('common.refresh'), { icon: 'refresh', onClick: load })),
    content,
  );

  async function load() {
    content.replaceChildren(loadingBlock());
    try {
      data = await api.get('/settings');
      paint();
    } catch (err) {
      content.replaceChildren(card({ body: h('div', { class: 'alert alert-danger' }, err.message) }));
    }
  }

  function paint() {
    const settings = data.settings || {};
    const admin = isAdmin();

    const panelName = input({ value: settings.panel_name || '', maxlength: '64', disabled: !admin });
    const locale = select([{ value: 'id', label: 'Bahasa Indonesia' }, { value: 'en', label: 'English' }],
      { value: settings.locale || 'id', disabled: !admin });
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
        toast(t('settings.saved'), 'success');
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
          field(t('settings.panelName'), panelName, t('settings.panelNameHint')),
          field(t('settings.language'), locale)),
        field(t('settings.theme'), theme),
        admin ? h('div', { class: 'form-actions' }, button(t('common.save'), { variant: 'primary', icon: 'save', onClick: saveGeneral })) : null),
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
        toast(t('settings.saved'), 'success');
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
        field(t('settings.acmeEmail'), acmeEmail, t('settings.acmeEmailHint')),
        h('div', { class: 'field' }, staging, h('div', { class: 'hint' }, t('settings.acmeStagingHint'))),
        admin ? h('div', { class: 'form-actions' }, button(t('common.save'), { variant: 'primary', icon: 'save', onClick: saveAcme })) : null),
    });

    const info = card({
      title: t('settings.about'),
      icon: 'info',
      class: 'card-about',
      body: h('dl', { class: 'kv kv-rows' },
        h('dt', {}, t('settings.version')), h('dd', {}, h('span', { class: 'mono' }, data.version || '—')),
        h('dt', {}, t('settings.started')), h('dd', {}, formatDate(Math.floor(new Date(data.started).getTime() / 1000))),
        h('dt', {}, t('settings.panelUrl')), h('dd', { class: 'mono small' }, data.panel_url || '—'),
        h('dt', {}, t('settings.listen')), h('dd', { class: 'mono' }, data.listen || '—'),
        h('dt', {}, t('settings.dataDir')), h('dd', { class: 'mono small' }, data.data_dir || '—'),
        h('dt', {}, t('settings.backupDir')), h('dd', { class: 'mono small' }, data.backup_dir || '—'),
        h('dt', {}, t('settings.logDir')), h('dd', { class: 'mono small' }, data.log_dir || '—'),
        h('dt', {}, t('settings.sessionIdle')), h('dd', {}, `${data.session_idle_hours ?? '—'} h`),
        h('dt', {}, t('settings.sessionAbsolute')), h('dd', {}, `${data.session_absolute_hours ?? '—'} h`)),
      foot: h('a', {
        class: 'repo-link',
        href: REPO_URL,
        target: '_blank',
        rel: 'noopener noreferrer',
        title: t('settings.repository'),
      },
        h('span', { class: 'repo-icon' }, iconSpan('github', 18)),
        h('span', { class: 'repo-text' },
          h('b', {}, 'GoPanel'),
          h('span', { class: 'small' }, 'yazvip/GoPanel')),
        iconSpan('external', 14)),
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
        admin ? h('div', { class: 'mt3' },
          formRow(field(t('settings.certificate'), certInput), field(t('settings.privateKey'), keyInput)),
          h('div', { class: 'form-actions' },
            button(t('settings.uploadTls'), { variant: 'primary', icon: 'upload', onClick: uploadTLS })) ) : null,
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
