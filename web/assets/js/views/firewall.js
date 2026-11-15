import { api } from '../api.js';
import { t } from '../i18n.js';
import {
  h, pageHeader, card, badge, dataTable, emptyState, button, iconButton,
  openModal, confirmDialog, field, input, select, formRow, loadingBlock,
  toast, toastError,
} from '../ui.js';
import { canWrite } from '../main.js';

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

function policyLabel(value) {
  const key = String(value || '').toLowerCase() === 'deny' ? 'firewall.deny' : 'firewall.allow';
  return t(key);
}

function actionLabel(value) {
  return policyLabel(value);
}

function actionTone(value) {
  return String(value || '').toLowerCase() === 'deny' ? 'red' : 'green';
}

/* ufw prints "Anywhere" for an open port or source; the panel says it in the
   operator's own words. */
function portLabel(value) {
  const text = String(value || '').trim();
  if (!text || text.toLowerCase() === 'anywhere' || text.toLowerCase() === 'any') {
    return t('firewall.any');
  }
  return text;
}

export async function render(ctx) {
  const { outlet } = ctx;
  const page = h('div', { class: 'page admin-page' });
  outlet.replaceChildren(page);
  let status = null;
  const content = h('div', {});
  const addRule = canWrite()
    ? button(t('firewall.addRule'), { variant: 'primary', icon: 'plus', onClick: openAddRule })
    : null;
  page.append(
    pageHeader(t('firewall.title'), t('firewall.subtitle'),
      h('div', { class: 'row gap-sm' }, addRule,
        button(t('common.refresh'), { icon: 'refresh', onClick: load }))),
    content,
  );

  async function load() {
    content.replaceChildren(loadingBlock(t('firewall.loading')));
    try {
      status = await api.get('/firewall');
      paint();
    } catch (err) {
      content.replaceChildren(card({
        body: stateBlock(t('firewall.loadFailed'), err.message,
          button(t('common.retry'), { icon: 'refresh', onClick: load }), 'alert', 'error'),
      }));
    }
  }

  function paint() {
    if (!status) return;
    if (!status.supported) {
      content.replaceChildren(card({
        body: stateBlock(t('firewall.unsupported'), t('firewall.statusHint'), null, 'shield'),
      }));
      return;
    }
    const active = status.active;
    const statusCard = card({
      title: t('firewall.status'),
      icon: 'shieldCheck',
      actions: canWrite()
        ? (active
          ? button(t('firewall.disable'), { variant: 'danger', size: 'sm', onClick: disableFirewall })
          : button(t('firewall.enable'), { variant: 'primary', size: 'sm', onClick: enableFirewall }))
        : null,
      body: h('div', { class: 'fw-status' },
        h('div', { class: `fw-badge ${active ? 'on' : 'off'}` },
          h('span', { class: 'dot' }), active ? t('firewall.active') : t('firewall.inactive')),
        h('div', { class: 'kv-grid' },
          h('div', {},
            h('div', { class: 'muted small' }, t('firewall.defaultIncoming')),
            h('b', {}, policyLabel(status.default_incoming))),
          h('div', {},
            h('div', { class: 'muted small' }, t('firewall.defaultOutgoing')),
            h('b', {}, policyLabel(status.default_outgoing))))),
      foot: h('div', { class: 'muted small' }, t('firewall.statusHint')),
    });
    const rules = status.rules || [];
    const rulesCard = card({
      class: 'adm-t-rules',
      title: t('firewall.rules'),
      icon: 'server',
      actions: badge(String(rules.length), 'gray'),
      flush: true,
      body: rules.length ? dataTable([
        {
          label: '#',
          className: 'adm-col-idx',
          render: (rule) => h('span', { class: 'muted mono' }, rule.number),
        },
        {
          label: t('firewall.port'),
          className: 'adm-col-port',
          render: (rule) => h('span', { class: 'mono' }, portLabel(rule.to)),
        },
        {
          label: t('firewall.proto'),
          className: 'adm-col-proto',
          render: (rule) => h('span', { class: 'muted small' }, rule.proto || '—'),
        },
        {
          label: t('firewall.from'),
          className: 'adm-col-from',
          render: (rule) => h('span', { class: 'mono muted' }, portLabel(rule.from)),
        },
        {
          label: t('firewall.action'),
          className: 'adm-col-action',
          render: (rule) => badge(actionLabel(rule.action), actionTone(rule.action)),
        },
        {
          label: t('firewall.comment'),
          className: 'adm-col-comment',
          render: (rule) => (rule.comment
            ? h('span', { class: 'muted small' }, rule.comment)
            : h('span', { class: 'muted small' }, '—')),
        },
        {
          label: t('common.actions'),
          className: 'col-actions adm-col-actions',
          render: (rule) => (canWrite()
            ? h('div', { class: 'row-actions' },
              iconButton('trash', t('common.delete'), () => removeRule(rule), 'danger'))
            : null),
        },
      ], rules) : stateBlock(t('firewall.noRules'), t('firewall.noRulesHint'),
        canWrite() ? button(t('firewall.addRule'), { variant: 'primary', icon: 'plus', onClick: openAddRule }) : null,
        'shield'),
    });
    content.replaceChildren(statusCard, rulesCard);
  }

  async function enableFirewall() {
    const ok = await confirmDialog({
      title: t('firewall.enableTitle'),
      message: t('firewall.enableConfirm'),
      note: t('firewall.statusHint'),
      confirmText: t('firewall.enable'),
    });
    if (!ok) return;
    try {
      status = await api.post('/firewall/enable', {});
      toast(t('firewall.enabled'), 'success');
      paint();
    } catch (err) {
      toastError(err);
    }
  }

  async function disableFirewall() {
    const ok = await confirmDialog({
      title: t('firewall.disableTitle'),
      message: t('firewall.disableConfirm'),
      confirmText: t('firewall.disable'),
      danger: true,
    });
    if (!ok) return;
    try {
      status = await api.post('/firewall/disable', {});
      toast(t('firewall.disabled'), 'success');
      paint();
    } catch (err) {
      toastError(err);
    }
  }

  function openAddRule() {
    const port = input({ placeholder: '8080', class: 'input mono', required: true });
    const proto = select([
      { value: 'tcp', label: t('firewall.tcp') },
      { value: 'udp', label: t('firewall.udp') },
    ], { value: 'tcp' });
    const action = select([
      { value: 'allow', label: t('firewall.allow') },
      { value: 'deny', label: t('firewall.deny') },
    ], { value: 'allow' });
    const from = input({ placeholder: t('firewall.any'), value: 'any' });
    const comment = input({ placeholder: t('firewall.commentHint') });
    const modal = openModal({
      title: t('firewall.addRule'),
      size: 'modal-sm',
      body: [
        formRow(
          labelled(t('firewall.port'), port, t('firewall.portHint'), true),
          labelled(t('firewall.proto'), proto)),
        formRow(
          labelled(t('firewall.action'), action),
          labelled(t('firewall.from'), from, t('firewall.fromHint'))),
        labelled(t('firewall.comment'), comment),
      ],
      footer: [
        button(t('common.cancel'), { onClick: () => modal.close() }),
        button(t('firewall.addRule'), {
          variant: 'primary',
          onClick: async () => {
            try {
              status = await api.post('/firewall/rules', {
                port: port.value.trim(),
                proto: proto.value,
                action: action.value,
                from: from.value.trim() || 'any',
                comment: comment.value.trim(),
              });
              modal.close();
              toast(t('firewall.ruleAdded'), 'success');
              paint();
            } catch (err) {
              toastError(err);
            }
          },
        }),
      ],
    });
    modal.el.classList.add('adm-modal');
  }

  async function removeRule(rule) {
    const protectedRule = /ssh/i.test(rule.comment || '');
    if (protectedRule) {
      toast(t('firewall.sshWarning'), 'warn');
      return;
    }
    const ok = await confirmDialog({
      title: t('firewall.deleteTitle'),
      message: t('firewall.deleteConfirm', { num: rule.number }),
      note: [rule.to, rule.proto, rule.from].filter(Boolean).join(' · '),
      confirmText: t('common.delete'),
      danger: true,
    });
    if (!ok) return;
    try {
      status = await api.del(`/firewall/rules/${rule.number}`, {});
      toast(t('firewall.ruleDeleted'), 'success');
      paint();
    } catch (err) {
      toastError(err);
    }
  }

  await load();
  return page;
}
