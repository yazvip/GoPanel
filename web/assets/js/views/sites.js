import { api } from '../api.js';
import { t } from '../i18n.js';
import {
  h, pageHeader, card, badge, statusBadge, dataTable, emptyState, button,
  iconButton, openModal, confirmDialog, field, input, textarea, select,
  checkbox, formRow, loadingBlock, toast, toastError, iconSpan, formatDate,
  copyText,
} from '../ui.js';
import { canWrite } from '../main.js';
import { navigate } from '../router.js';

const TYPES = [
  { value: 'php', labelKey: 'sites.typePHP' },
  { value: 'static', labelKey: 'sites.typeStatic' },
  { value: 'proxy', labelKey: 'sites.typeProxy' },
];

function typeLabel(type) {
  const found = TYPES.find((item) => item.value === type);
  return found ? t(found.labelKey) : type || '—';
}

function primaryDomain(site) {
  const domains = site.domains || [];
  const primary = domains.find((d) => d.is_primary);
  return primary ? primary.domain : (domains[0] ? domains[0].domain : '');
}

export async function render(ctx) {
  const { outlet } = ctx;
  const page = h('div', { class: 'page' });
  outlet.replaceChildren(page);

  let sites = [];
  let phpVersions = [];
  let acmeEmail = '';
  const body = h('div', {});

  page.append(
    pageHeader(t('sites.title'), t('sites.subtitle'),
      canWrite() ? button(t('sites.create'), { variant: 'primary', icon: 'plus', onClick: () => openSiteForm(null) }) : null),
    body,
  );

  async function load() {
    body.replaceChildren(loadingBlock());
    try {
      const [list, versions, settings] = await Promise.all([
        api.get('/sites'),
        api.get('/php/versions').catch(() => []),
        api.get('/settings').catch(() => null),
      ]);
      sites = list || [];
      phpVersions = versions || [];
      acmeEmail = settings && settings.settings ? (settings.settings.acme_email || '') : '';
      paint();
    } catch (err) {
      body.replaceChildren(card({
        body: h('div', { class: 'alert alert-danger' }, iconSpan('alert', 16), h('div', {}, err.message)),
      }));
    }
  }

  function paint() {
    const rows = sites.map((site) => h('article', { class: `site-card${site.enabled ? '' : ' is-off'}` },
      h('div', { class: 'site-card-head' },
        h('span', { class: `site-dot ${site.enabled ? 'on' : 'off'}` }),
        h('b', { class: 'site-name', title: site.name }, site.name),
        badge(typeLabel(site.type), site.type === 'proxy' ? 'violet' : site.type === 'php' ? 'blue' : 'gray'),
        site.ssl_enabled ? badge('SSL', 'green') : null),
      h('div', { class: 'site-domains' },
        (site.domains || []).map((domain) => h('span', { class: 'chip' },
          domain.is_primary ? iconSpan('globe', 12) : null, domain.domain)),
        !(site.domains || []).length ? h('span', { class: 'muted small' }, t('sites.noDomains')) : null),
      h('div', { class: 'site-meta' },
        h('span', { class: 'mono' }, site.root_path || '—'),
        h('span', { class: 'muted' }, '·'),
        h('span', {}, site.ssl_enabled && site.ssl_expires_at
          ? `${t('sites.sslExpires')} ${formatDate(site.ssl_expires_at, false)}`
          : t('sites.sslOff'))),
      h('div', { class: 'site-card-foot' },
        h('span', { class: 'site-state' },
          h('span', { class: `site-dot ${site.enabled ? 'on' : 'off'}` }),
          site.enabled ? t('status.enabled') : t('status.disabled')),
        h('div', { class: 'site-actions' },
          h('button', {
            class: 'icon-btn', type: 'button', title: t('sites.overview'), 'aria-label': t('sites.overview'),
            onClick: () => openManage(site),
          }, iconSpan('settings', 17)),
          canWrite() ? h('button', {
            class: 'icon-btn', type: 'button',
            title: site.enabled ? t('sites.disable') : t('sites.enable'),
            'aria-label': site.enabled ? t('sites.disable') : t('sites.enable'),
            onClick: () => toggleSite(site),
          }, iconSpan(site.enabled ? 'pause' : 'play', 16)) : null,
          canWrite() ? iconButton('trash', t('sites.delete'), () => deleteSite(site), 'danger') : null))));

    if (!sites.length) {
      body.replaceChildren(card({
        body: emptyState(t('sites.noSites'), t('sites.noSitesHint'),
          canWrite() ? button(t('sites.create'), { variant: 'primary', icon: 'plus', onClick: () => openSiteForm(null) }) : null,
          'globe'),
      }));
      return;
    }
    body.replaceChildren(h('div', { class: 'site-grid' }, rows));
  }

  async function toggleSite(site) {
    try {
      await api.post(`/sites/${site.id}/${site.enabled ? 'disable' : 'enable'}`, {});
      toast(site.enabled ? t('sites.disabledMsg') : t('sites.enabledMsg'), 'success');
      await load();
    } catch (err) {
      toastError(err);
    }
  }

  async function deleteSite(site, closeFn) {
    const ok = await confirmDialog({
      title: t('sites.delete'),
      message: t('sites.deleteConfirm', { name: site.name }),
      confirmText: t('common.delete'),
      danger: true,
      requireText: site.name,
      note: t('sites.nginxReloadFailed'),
    });
    if (!ok) return;
    try {
      await api.del(`/sites/${site.id}`, { confirm: site.name, delete_files: true });
      toast(t('sites.deleted'), 'success');
      if (closeFn) closeFn();
      await load();
    } catch (err) {
      toastError(err);
    }
  }

  function openSiteForm(site) {
    const editing = Boolean(site);
    const name = input({ value: site ? site.name : '', placeholder: 'example.com', required: true });
    const type = select(TYPES.map((item) => ({ value: item.value, label: t(item.labelKey) })), { value: site ? site.type : 'php' });
    const domains = textarea({
      rows: '3',
      placeholder: 'example.com\nwww.example.com',
      value: site && site.domains ? site.domains.map((d) => d.domain).join('\n') : '',
    });
    const rootPath = input({ value: site ? site.root_path : '', placeholder: '/var/www/example' });
    const phpVersion = select(
      [{ value: '', label: t('common.none') }, ...phpVersions.map((v) => ({ value: v, label: `PHP ${v}` }))],
      { value: site ? site.php_version || '' : (phpVersions[0] || '') },
    );
    const upstream = input({ value: site ? site.upstream || '' : '', placeholder: '127.0.0.1:3000' });
    const indexFile = input({ value: site ? site.index_file || '' : 'index.php index.html' });
    const maxBody = input({ type: 'number', min: '1024', value: site ? String(site.client_max_body || '') : '67108864' });
    const accessLog = checkbox(t('sites.accessLog'), site ? site.access_log : true);

    const phpField = field(t('sites.phpVersion'), phpVersion, phpVersions.length ? '' : t('sites.phpNotInstalled'));
    const upstreamField = field(t('sites.upstream'), upstream, t('sites.upstreamHint'));
    const syncType = () => {
      phpField.style.display = type.value === 'php' ? '' : 'none';
      upstreamField.style.display = type.value === 'proxy' ? '' : 'none';
    };
    type.addEventListener('change', syncType);

    const nginxBox = h('div', { class: 'alert alert-warn mt2', hidden: true });

    const bodyEl = h('div', {},
      field(t('sites.name'), name, t('sites.nameHint'), true),
      field(t('sites.type'), type),
      field(t('sites.domains'), domains, t('sites.domainsHint'), !editing),
      phpField,
      upstreamField,
      formRow(
        field(t('sites.rootPath'), rootPath, t('sites.rootPathHint')),
        field(t('sites.indexFile'), indexFile, t('sites.indexHint'))),
      formRow(
        field(t('sites.clientMaxBody'), maxBody),
        h('div', { class: 'field' }, h('div', { class: 'label' }, ' '), accessLog)),
      nginxBox,
    );
    syncType();

    const showNginxMissing = () => {
      nginxBox.replaceChildren(
        iconSpan('alert', 16),
        h('div', { class: 'grow' },
          h('div', {}, t('sites.nginxMissing')),
          h('div', { class: 'small mt1' }, t('sites.nginxMissingHint')),
          canWrite() ? h('div', { class: 'mt2' },
            button(t('nav.apps'), {
              variant: 'primary',
              icon: 'box',
              onClick: () => { modal.close(); navigate('/apps'); },
            })) : null),
      );
      nginxBox.hidden = false;
      nginxBox.scrollIntoView({ block: 'nearest' });
    };

    const modal = openModal({
      title: editing ? t('sites.editTitle') : t('sites.createTitle'),
      body: bodyEl,
      footer: [
        button(t('common.cancel'), { onClick: () => modal.close() }),
        button(editing ? t('common.save') : t('common.create'), {
          variant: 'primary',
          onClick: async () => {
            const payload = {
              name: name.value.trim(),
              type: type.value,
              root_path: rootPath.value.trim(),
              php_version: type.value === 'php' ? phpVersion.value : '',
              upstream: type.value === 'proxy' ? upstream.value.trim() : '',
              index_file: indexFile.value.trim(),
              client_max_body: Number(maxBody.value) || 0,
              access_log: accessLog.querySelector('input').checked,
              domains: domains.value.split('\n').map((d) => d.trim()).filter(Boolean),
            };
            try {
              if (editing) await api.patch(`/sites/${site.id}`, payload);
              else await api.post('/sites', payload);
              toast(editing ? t('sites.updated') : t('sites.createSuccess'), 'success');
              modal.close();
              await load();
            } catch (err) {
              if (err.code === 'sites.nginx_missing') showNginxMissing();
              else toastError(err);
            }
          },
        }),
      ],
    });
  }

  function openManage(site) {
    let current = site;
    const content = h('div', {});
    const modal = openModal({
      title: `${current.name}`,
      size: 'modal-lg',
      body: content,
      footer: button(t('common.close'), { onClick: () => modal.close() }),
    });

    function paintManage() {
      const domainsBox = h('div', { class: 'domain-list' });
      for (const domain of current.domains || []) {
        domainsBox.append(h('div', { class: 'domain-row' },
          h('div', { class: 'grow' },
            h('span', { class: 'mono' }, domain.domain),
            domain.is_primary ? badge(t('sites.primary'), 'green') : null),
          !domain.is_primary && canWrite()
            ? h('button', { class: 'link-btn', type: 'button', onClick: () => domainAction('primary', domain) }, t('sites.setPrimary'))
            : null,
          canWrite()
            ? iconButton('trash', t('common.remove'), () => domainAction('remove', domain), 'danger')
            : null));
      }
      if (!(current.domains || []).length) {
        domainsBox.append(h('div', { class: 'muted small' }, t('sites.noDomains')));
      }
      const newDomain = input({ placeholder: 'example.com' });
      if (canWrite()) {
        domainsBox.append(h('div', { class: 'domain-add' }, newDomain,
          button(t('sites.addDomain'), {
            icon: 'plus',
            onClick: async () => {
              const value = newDomain.value.trim();
              if (!value) return;
              try {
                current = await api.post(`/sites/${current.id}/domains`, { domain: value });
                toast(t('sites.domainAdded'), 'success');
                refreshAll();
              } catch (err) {
                toastError(err);
              }
            },
          })));
      }

      const sslBox = current.ssl_enabled
        ? h('div', {},
          h('div', { class: 'alert alert-success' }, iconSpan('shieldCheck', 16),
            h('div', {}, `${t('sites.sslOn')}${current.ssl_expires_at ? ` · ${t('sites.sslExpires')} ${formatDate(current.ssl_expires_at, false)}` : ''}`)),
          h('div', { class: 'row gap' },
            canWrite() ? button(t('sites.renewSSL'), {
              icon: 'rotate',
              onClick: async () => {
                try {
                  await api.post(`/sites/${current.id}/ssl/renew`, { email: acmeEmail });
                  toast(t('sites.sslIssuing'), 'success');
                  modal.close();
                  navigate('/jobs');
                } catch (err) {
                  toastError(err);
                }
              },
            }) : null,
            canWrite() ? button(t('sites.disableSSL'), {
              variant: 'danger',
              onClick: async () => {
                const ok = await confirmDialog({
                  title: t('sites.disableSSL'), message: `${current.name} — ${t('sites.sslOff')}?`,
                  confirmText: t('sites.disableSSL'), danger: true,
                });
                if (!ok) return;
                try {
                  await api.del(`/sites/${current.id}/ssl`, {});
                  toast(t('sites.sslDisabled'), 'success');
                  modal.close();
                  await load();
                } catch (err) {
                  toastError(err);
                }
              },
            }) : null))
        : (() => {
          const email = input({ value: acmeEmail, placeholder: 'admin@example.com', type: 'email' });
          const staging = checkbox(t('sites.sslStaging'), false);
          return h('div', {},
            h('div', { class: 'alert alert-info' }, iconSpan('info', 16), h('div', {}, t('sites.sslHint'))),
            field(t('sites.sslEmail'), email, acmeEmail ? t('sites.sslEmailHint') : t('sites.sslNotConfigured')),
            h('div', { class: 'field' }, staging, h('div', { class: 'hint' }, t('sites.sslStagingHint'))),
            canWrite() ? button(t('sites.issueSSL'), {
              variant: 'primary', icon: 'shieldCheck',
              onClick: async () => {
                try {
                  await api.post(`/sites/${current.id}/ssl`, {
                    email: email.value.trim(),
                    staging: staging.querySelector('input').checked,
                  });
                  toast(t('sites.sslIssuing'), 'success');
                  modal.close();
                  navigate('/jobs');
                } catch (err) {
                  toastError(err);
                }
              },
            }) : null);
        })();

      content.replaceChildren(
        h('div', { class: 'manage-grid' },
          h('div', {},
            h('div', { class: 'section-head' }, t('sites.domainsTitle')),
            domainsBox),
          h('div', {},
            h('div', { class: 'section-head' }, t('sites.ssl')),
            sslBox)),
        h('div', { class: 'manage-grid mt3' },
          h('div', {},
            h('div', { class: 'section-head' }, t('sites.logs')),
            h('div', { class: 'row gap' },
              button(t('sites.errorLog'), { icon: 'filetext', onClick: () => openLogs('error') }),
              button(t('sites.accessLogTab'), { icon: 'list', onClick: () => openLogs('access') }))),
          h('div', {},
            h('div', { class: 'section-head' }, t('sites.overview')),
            h('dl', { class: 'kv' },
              h('dt', {}, t('sites.rootPath')), h('dd', { class: 'mono' }, current.root_path || '—'),
              h('dt', {}, t('sites.type')), h('dd', {}, typeLabel(current.type)),
              h('dt', {}, t('common.status')), h('dd', {}, statusBadge(current.enabled ? 'enabled' : 'disabled'))))),
        canWrite() ? h('div', { class: 'danger-zone mt3' },
          h('div', {},
            h('div', { class: 'danger-title' }, t('sites.dangerZone')),
            h('div', { class: 'muted small' }, t('sites.deleteConfirm', { name: current.name }))),
          button(t('sites.delete'), { variant: 'danger', icon: 'trash', onClick: () => deleteSite(current, () => modal.close()) })) : null,
      );
    }

    async function domainAction(action, domain) {
      if (action === 'remove') {
        const ok = await confirmDialog({
          title: t('sites.removeDomain'),
          message: domain.domain,
          confirmText: t('common.remove'), danger: true,
        });
        if (!ok) return;
        try {
          current = await api.del(`/sites/${current.id}/domains/${domain.id}`, {});
          toast(t('sites.domainRemoved'), 'success');
          refreshAll();
        } catch (err) {
          toastError(err);
        }
        return;
      }
      try {
        current = await api.post(`/sites/${current.id}/domains/${domain.id}/primary`, {});
        toast(t('sites.primarySet'), 'success');
        refreshAll();
      } catch (err) {
        toastError(err);
      }
    }

    async function refreshAll() {
      try {
        current = await api.get(`/sites/${current.id}`);
        paintManage();
        await load();
      } catch (err) {
        toastError(err);
      }
    }

    async function openLogs(kind) {
      const box = h('pre', { class: 'log-view' }, t('sites.loadingLogs'));
      const logModal = openModal({
        title: `${kind === 'access' ? t('sites.accessLogTab') : t('sites.errorLog')} · ${current.name}`,
        size: 'modal-lg',
        body: h('div', {},
          h('div', { class: 'row gap mb2' },
            button(t('common.refresh'), {
              icon: 'refresh',
              onClick: () => fetchLog(kind, box, current.id),
            }),
            button(t('common.copy'), { icon: 'copy', onClick: () => copyText(box.textContent || '') })),
          box),
        footer: button(t('common.close'), { onClick: () => logModal.close() }),
      });
      fetchLog(kind, box, current.id);
    }

    paintManage();
  }

  async function fetchLog(kind, box, siteId) {
    try {
      const data = await api.get(`/sites/${siteId}/logs?type=${kind}&lines=500`);
      box.textContent = data.content || t('common.none');
      box.scrollTop = box.scrollHeight;
    } catch (err) {
      box.textContent = err.message;
    }
  }

  await load();
  return page;
}
