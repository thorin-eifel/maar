// Portgruppen-Detailseite: Netz, „Verwendet von“, Konfiguration und die erzeugten Dateien.
import { h, clear, pageHead, panel, table, pill, metric, vmStatePill, loading, toast, toastError, confirm, busy } from '../ui.js';
import { get, del, session } from '../api.js';
import { watchJob, onJobDone } from '../jobs.js';
import { portGroupDialog } from './vnet.js';

const enc = encodeURIComponent;
const onOff = (v, a = 'an', b = 'aus') => pill(v ? 'ok' : 'off', v ? a : b);

export default {
  title: 'Portgruppe',
  async render(main, parts) {
    const name = parts[0];
    const isAdmin = session.role === 'admin';
    const head = h('div'), body = h('div', { class: 'content' }, loading());
    main.append(head, body);
    let d = null;

    async function load() {
      d = await get(`/api/vnet/portgroups/${enc(name)}`);
      const acts = [];
      if (isAdmin) {
        const edit = h('button', { class: 'secondary', type: 'button' }, 'Bearbeiten');
        edit.addEventListener('click', () => portGroupDialog(d, () => {}).catch(toastError));
        const rm = h('button', { class: 'secondary danger', type: 'button', disabled: d.uses.length + d.hidden > 0, title: d.uses.length + d.hidden ? 'Noch VMs angeschlossen' : null }, 'Löschen');
        rm.addEventListener('click', () => busy(rm, async () => {
          if (!await confirm({ title: `Portgruppe ${name} löschen?`, text: 'Switch, Gateway und VLAN werden zerstört; die hostname.if-Dateien werden gesichert und entfernt.', ok: 'Löschen', danger: true, typeToConfirm: name })) return;
          watchJob((await del(`/api/vnet/portgroups/${enc(name)}`)).job);
          location.hash = '#/vnet';
        }).catch(toastError));
        acts.push(edit, rm);
      }
      clear(head, pageHead('09 / NETZWERK', name, `${d.description ? d.description + ' · ' : ''}${d.bridge}${d.vlan ? ' · VLAN ' + d.vlan : ''}`, d.present ? pill('ok', 'aktiv') : pill('warn', 'Switch fehlt'), ...acts));
      draw();
    }

    function draw() {
      const total = d.uses.length + d.hidden;
      const svc = [d.nat ? 'NAT' : null, d.dhcp ? (d.rangeFrom ? `DHCP ${d.rangeFrom.split('.').pop()}–${d.rangeTo.split('.').pop()}` : 'DHCP (nur fest)') : null, d.dns ? `DNS ${d.domain}` : null].filter(Boolean);
      const files = Object.entries(d.files).sort().map(([p, c]) => `# ${p}\n${c}`).join('\n') + `\n# /etc/vm.conf\n${d.switchBlock}`;
      clear(body,
        !d.present ? h('div', { class: 'note bad', role: 'alert' }, `Das Interface ${d.bridge} existiert nicht. „Neu abgleichen“ auf der Seite VM-Netze legt es an.`) : null,
        d.uplink && d.uplinkInfo && d.uplinkInfo.status === 'no carrier' ? h('div', { class: 'note warn' }, `Der Uplink ${d.uplink} hat keinen Link – VMs erreichen das Netzwerk außerhalb des Hosts nicht.`) : null,
        h('div', { class: 'metrics' },
          metric('Netz', d.subnet || 'reines L2', d.gateway ? `Gateway ${d.gateway.split('/')[0]} (${d.vport})` : 'ohne Host-Adresse'),
          metric('VMs', String(total), d.hidden ? `${d.hidden} ausgeblendet` : `${d.uses.length} Anschlüsse`),
          metric('Feste Adressen', String(d.reserved), d.free >= 0 && d.subnet ? `${d.free} frei` : 'als DHCP-Reservierung'),
          metric('Dienste', svc.length ? String(svc.length) : '–', svc.join(' · ') || 'keine')),
        panel('Verwendet von', 'VM-ANSCHLÜSSE', h('div', { class: 'panel-body flush' }, table(['VM', 'Status', 'MAC', 'Adresse', 'DNS-Name', 'Firewall', ''], d.uses.map(u => h('tr', null,
          h('td', null, h('a', { class: 'name', href: `#/vm/${enc(u.vm)}` }, u.vm), h('span', { class: 'sub' }, `Anschluss ${u.index}`)), h('td', null, vmStatePill(u.state)),
          h('td', { class: 'mono' }, u.lladdr || 'zufällig'), h('td', { class: 'mono' }, u.ip || h('span', { class: 'sub' }, 'dynamisch')), h('td', { class: 'mono' }, u.fqdn || '–'),
          h('td', null, u.in === 'deny' || u.out === 'deny' || u.rules ? [pill(u.in === 'deny' ? 'ok' : 'off', `ein ${u.in === 'deny' ? 'gesperrt' : 'offen'}`), ' ', pill(u.out === 'deny' ? 'ok' : 'off', `aus ${u.out === 'deny' ? 'gesperrt' : 'offen'}`), h('span', { class: 'sub' }, `${u.rules} Regel${u.rules === 1 ? '' : 'n'}`)] : h('span', { class: 'sub' }, 'offen')),
          h('td', { class: 'actions' }, h('a', { class: 'mini-btn', href: `#/vm/${enc(u.vm)}/config` }, 'Konfigurieren')))), 'Keine VM an dieser Portgruppe.'))),
        h('div', { class: 'grid-2' },
          panel('Konfiguration', 'VEB · VPORT · VLAN', h('div', { class: 'panel-body' }, h('dl', { class: 'kv' },
            h('dt', null, 'Switch'), h('dd', { class: 'mono' }, d.bridge, h('span', { class: 'sub' }, `Interface-Gruppe ${d.group}`)),
            h('dt', null, 'Uplink'), h('dd', null, d.uplink ? [h('span', { class: 'mono' }, d.vlan ? `vlan${d.vlan} auf ${d.uplink}` : d.uplink), ' ', d.uplinkInfo ? (d.uplinkInfo.status === 'no carrier' ? pill('warn', 'kein Link') : pill('ok', 'Link')) : null] : 'nur intern'),
            h('dt', null, 'Gateway'), h('dd', { class: 'mono' }, d.gateway || '–'),
            h('dt', null, 'NAT'), h('dd', null, onOff(d.nat)), h('dt', null, 'DHCP'), h('dd', null, onOff(d.dhcp), d.rangeFrom ? h('span', { class: 'sub mono' }, `${d.rangeFrom} – ${d.rangeTo}`) : null),
            h('dt', null, 'DNS'), h('dd', null, onOff(d.dns), d.domain ? h('span', { class: 'sub mono' }, d.domain) : null),
            h('dt', null, 'pf zwischen VMs'), h('dd', null, onOff(d.filter)), h('dt', null, 'Gefälschte MACs'), h('dd', null, d.locked ? pill('ok', 'verworfen') : pill('off', 'erlaubt')),
            h('dt', null, 'Mitglieder'), h('dd', null, d.members.length ? d.members.map(m => h('span', { class: 'tag mono' }, m)) : '–')))),
          panel('Erzeugte Dateien', 'HOSTNAME.IF · VM.CONF', h('div', { class: 'panel-body flush' }, h('pre', { class: 'codeblock' }, files)))),
        d.managed ? null : h('div', { class: 'note' }, 'Diese Portgruppe wurde in vm.conf vorgefunden. Beim ersten Speichern schreibt maar ihre Dateien neu.'));
    }

    await load();
    const off = onJobDone(() => setTimeout(() => load().catch(() => {}), 600));
    const t = setInterval(() => { if (!document.querySelector('dialog')) load().catch(() => {}); }, 20000);
    return () => { clearInterval(t); off(); };
  },
};
