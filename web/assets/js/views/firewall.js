import { api } from '../api.js';
import { t } from '../i18n.js';
import {
  h, pageHeader, card, badge, dataTable, emptyState, button, iconButton,
  openModal, confirmDialog, field, input, select, formRow, loadingBlock,
  toast, toastError, iconSpan, statusBadge,
} from '../ui.js';
import { canWrite } from '../main.js';

export async function render(ctx) {
  const { outlet } = ctx;
  const page = h('div', { class: 'page' });
  outlet.replaceChildren(page);
  let status = null;
  let loading = true;
  const content = h('div', {});

  page.append(
    pageHeader(t('firewall.title'), t('firewall.subtitle'),
      canWrite() ? h('div', { class: 'page-actions' },
        button(t('firewall.addRule'), { icon: 'plus', onClick: openAddRule }),
        button(t('common.refresh'), { icon: 'refresh', onClick: load })) : button(t('common.refresh'), { icon: 'refresh', onClick: load })),
    content,
  );

  async function load() {
    loading = true;
    if (!status) content.replaceChildren(loadingBlock(t('firewall.loading')));
    try {
      status = await api.get('/firewall');
      paint();
    } catch (err) {
      content.replaceChildren(card({ body: h('div', { class: 'alert alert-danger' }, err.message) }));
    }
    loading = false;
  }

  function paint() {
    if (!status) return;
    if (!status.supported) {
      content.replaceChildren(card({
        body: h('div', { class: 'alert alert-warn' }, iconSpan('alert', 16), h('div', {}, t('firewall.unsupported'))),
      }));
      return;
    }
    const active = status.active;
    const statusCard = card({
      title: t('firewall.status'),
      icon: 'shieldCheck',
      actions: canWrite() ? (active
        ? button(t('firewall.disable'), { variant: 'danger', size: 'sm', onClick: disableFirewall })
        : button(t('firewall.enable'), { variant: 'primary', size: 'sm', onClick: enableFirewall })) : null,
      body: h('div', { class: 'fw-status' },
        h('div', { class: `fw-badge ${active ? 'on' : 'off'}` },
          h('span', { class: 'dot' }), active ? t('firewall.active') : t('firewall.inactive')),
        h('div', { class: 'kv-grid' },
          h('div', {}, h('div', { class: 'muted small' }, t('firewall.defaultIncoming')), h('b', {}, status.default_incoming || '—')),
          h('div', {}, h('div', { class: 'muted small' }, t('firewall.defaultOutgoing')), h('b', {}, status.default_outgoing || '—')))),
      foot: h('div', { class: 'muted small' }, t('firewall.statusHint')),
    });

    const rules = status.rules || [];
    const rulesCard = card({
      title: `${t('firewall.rules')} (${rules.length})`,
      icon: 'server',
      flush: true,
      actions: canWrite() ? button(t('firewall.addRule'), { size: 'sm', icon: 'plus', onClick: openAddRule }) : null,
      body: rules.length ? dataTable([
        { label: '#', className: 'w-60', render: (rule) => h('span', { class: 'muted mono' }, rule.number) },
        { label: t('firewall.to'), render: (rule) => h('div', {},
          h('b', { class: 'mono' }, rule.to || t('firewall.any')),
          rule.port ? null : null) },
        { label: t('firewall.action'), render: (rule) => badge(rule.action, String(rule.action).toLowerCase() === 'allow' ? 'green' : 'red') },
        { label: t('firewall.from'), render: (rule) => h('span', { class: 'mono muted' }, rule.from || t('firewall.any')) },
        { label: t('firewall.proto'), render: (rule) => h('span', { class: 'muted small' }, rule.proto || '—') },
        { label: t('firewall.comment'), render: (rule) => h('span', { class: 'muted small' }, rule.comment || '') },
        { label: t('common.actions'), className: 'col-actions', render: (rule) => canWrite()
          ? h('div', { class: 'row-actions' },
            iconButton('trash', t('common.delete'), () => removeRule(rule), 'danger'))
          : null },
      ], rules) : emptyState(t('firewall.noRules'), t('firewall.statusHint'), null, 'shield'),
    });

    content.replaceChildren(h('div', { class: 'grid cols-2' }, statusCard, rulesCard));
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
        formRow(field(t('firewall.port'), port, t('firewall.portHint'), true), field(t('firewall.proto'), proto)),
        formRow(field(t('firewall.action'), action), field(t('firewall.from'), from, t('firewall.fromHint'))),
        field(t('firewall.comment'), comment),
      ],
      footer: [
        button(t('common.cancel'), { onClick: () => modal.close() }),
        button(t('common.add'), {
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
