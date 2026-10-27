// Datastore-Detailseite: Kapazität, „Verwendet von“, Images und das Gerät dahinter.
import { h, clear, pageHead, panel, table, pill, metric, vmStatePill, loading, toast, toastError, confirm, busy, bytes } from '../ui.js';
import { get, post, del, session } from '../api.js';

const enc = encodeURIComponent;
const TRANSPORT = { iscsi: 'iSCSI', fc: 'Fibre Channel', mpath: 'Multipath', local: 'lokal' };

export default {
  title: 'Datastore',
  async render(main, parts) {
    const name = parts[0];
    const isAdmin = session.role === 'admin';
    const head = h('div'), body = h('div', { class: 'content' }, loading());
    main.append(head, body);
    let d = null;

    const act = (label, fn, cls = 'secondary') => { const b = h('button', { class: cls, type: 'button' }, label); b.addEventListener('click', () => busy(b, fn).catch(toastError)); return b; };

    async function load() {
      d = await get(`/api/san/datastores/${enc(name)}`);
      const acts = [];
      if (isAdmin) {
        acts.push(d.mounted
          ? act('Aushängen', async () => { await post(`/api/san/datastores/${enc(name)}/unmount`); toast('Ausgehängt.'); await load(); })
          : act('Einhängen', async () => { await post(`/api/san/datastores/${enc(name)}/mount`); toast('Eingehängt.'); await load(); }, 'cta'));
        acts.push(act('Entfernen', async () => {
          if (!await confirm({ title: `${name} entfernen?`, text: 'Der Datastore wird ausgehängt und aus fstab und maar gelöscht. Die Daten auf der Disk bleiben unberührt.', ok: 'Entfernen', danger: true, typeToConfirm: name })) return;
          await del(`/api/san/datastores/${enc(name)}`); toast('Entfernt.'); location.hash = '#/san';
        }, 'secondary danger'));
      }
      clear(head, pageHead('06 / SPEICHER', name, d.mount, d.mounted ? pill('ok', 'eingehängt') : d.present ? pill('off', 'ausgehängt') : pill('bad', 'Disk fehlt'), ...acts));
      draw();
    }

    function draw() {
      const users = d.users, images = d.images;
      const orphans = images.filter(i => !i.usedBy.length && !i.name.endsWith('.iso') && !i.name.endsWith('.fs'));
      const k = d.disk;
      clear(body,
        !d.mounted && users.length ? h('div', { class: 'note bad', role: 'alert' }, `Der Datastore ist ausgehängt, aber ${users.length === 1 ? 'eine VM hat' : users.length + ' VMs haben'} Disks darauf – sie lassen sich nicht starten.`) : null,
        !d.present ? h('div', { class: 'note bad', role: 'alert' }, `Die Disk mit der DUID ${d.duid} ist nicht vorhanden. Prüfe die iSCSI-Sitzung oder die FC-Anbindung.`) : null,
        d.busy.length ? h('div', { class: 'note warn' }, `Laufende VMs: ${d.busy.join(', ')} – der Datastore lässt sich nicht aushängen.`) : null,
        h('div', { class: 'metrics' },
          metric('Kapazität', bytes(d.size), d.mounted ? 'Dateisystem (FFS)' : 'Größe der Disk'),
          metric('Belegt', d.mounted ? bytes(d.used) : '–', d.mounted ? `${bytes(d.avail)} frei · ${d.percent} %` : 'nicht eingehängt', d.mounted ? d.percent : undefined),
          metric('VMs', String(users.length + d.hidden), d.hidden ? `${d.hidden} ausgeblendet` : 'mit Disks hier'),
          metric('Images', String(images.length), orphans.length ? `${orphans.length} ohne VM` : 'alle in Verwendung')),
        panel('Verwendet von', 'VMS MIT DISKS AUF DIESEM DATASTORE', h('div', { class: 'panel-body flush' }, table(['VM', 'Status', 'Disks', { label: 'Belegt', num: true }], users.map(u => h('tr', null,
          h('td', null, h('a', { class: 'name', href: `#/vm/${enc(u.vm)}` }, u.vm)), h('td', null, vmStatePill(u.state)),
          h('td', { class: 'mono' }, u.disks.map(x => h('div', null, x.name, x.missing ? ' (fehlt)' : ''))), h('td', { class: 'num' }, bytes(u.bytes)))), 'Keine VM nutzt diesen Datastore.'))),
        panel('Images', 'DATEIEN IM DATASTORE', h('div', { class: 'panel-body flush' }, table(['Datei', { label: 'Größe', num: true }, 'Verwendet von'], images.map(i => h('tr', null,
          h('td', null, h('span', { class: 'name mono' }, i.name)), h('td', { class: 'num' }, bytes(i.size)),
          h('td', null, i.usedBy.length ? i.usedBy.map(u => u.startsWith('(') ? h('span', { class: 'tag' }, u) : h('a', { class: 'tag', href: `#/vm/${enc(u)}` }, u)) : pill('warn', 'unbenutzt')))), 'Keine Images.'))),
        panel('Gerät', 'DMESG · DISKLABEL', h('div', { class: 'panel-body' }, k ? h('dl', { class: 'kv' },
          h('dt', null, 'Transport'), h('dd', null, TRANSPORT[k.transport] || k.transport, h('span', { class: 'sub mono' }, k.bus)),
          h('dt', null, 'Disk'), h('dd', { class: 'mono' }, `${k.name} · Partition ${d.part}`),
          h('dt', null, 'Gerät'), h('dd', null, `${k.vendor} ${k.model} ${k.rev}`),
          k.wwn ? [h('dt', null, 'WWN'), h('dd', { class: 'mono' }, k.wwn)] : null,
          h('dt', null, 'DUID'), h('dd', { class: 'mono' }, d.duid),
          h('dt', null, 'Größe'), h('dd', null, bytes(k.size)),
          k.transport === 'iscsi' && d.targets.length ? [h('dt', null, 'iSCSI-Ziele'), h('dd', null, d.targets.map(t => h('a', { class: 'tag', href: '#/san' }, t.name)), h('span', { class: 'sub' }, 'Die Zuordnung der Disk zum Ziel kennt OpenBSD nicht.'))] : null)
          : h('p', { class: 'hint' }, 'Die Disk ist nicht vorhanden; zuletzt als ', h('span', { class: 'mono' }, d.disk || '?'), ' gesehen.'))),
        h('div', { class: 'note' }, 'FFS ist kein Cluster-Dateisystem: Dieser Datastore darf nur auf einem Host eingehängt sein. ', h('a', { href: '#/san' }, 'Zu „Geteilter Speicher“')));
    }

    await load();
    const t = setInterval(() => { if (!document.querySelector('dialog')) load().catch(() => {}); }, 20000);
    return () => clearInterval(t);
  },
};
