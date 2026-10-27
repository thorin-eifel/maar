// Geteilter Speicher: iSCSI, Fibre Channel, NVMe-oF (Hinweis) und Datastores darauf.
import { h, clear, pageHead, panel, table, pill, metric, loading, toast, toastError, confirm, formDialog, field, busy, bytes } from '../ui.js';
import { get, post, put, del } from '../api.js';
import { watchJob, onJobDone } from '../jobs.js';

const enc = encodeURIComponent;
const TRANSPORT = { iscsi: ['info', 'iSCSI'], fc: ['info', 'Fibre Channel'], mpath: ['info', 'Multipath'], local: ['off', 'lokal'] };
const fmtWWPN = w => (w || '').match(/../g)?.join(':') || '–';

function targetDialog(t, onDone) {
  const edit = !!t;
  t = t || { enabled: true, port: 0 };
  const name = h('input', { required: true, value: t.name || '', disabled: edit, pattern: '[A-Za-z0-9][A-Za-z0-9_\\-]{0,63}', placeholder: 'nas-vol1' });
  const addr = h('input', { required: true, value: t.addr || '', class: 'mono', placeholder: '10.0.0.50 oder san.ctw.lan' });
  const port = h('input', { type: 'number', min: 0, max: 65535, value: t.port || '', placeholder: '3260' });
  const iqn = h('input', { value: t.iqn || '', class: 'mono', placeholder: 'iqn.2003-01.org.example:vol1' });
  const init = h('input', { value: t.initiator || '', class: 'mono', placeholder: 'Standard: iqn.1995-11.org.openbsd.iscsid' });
  const disc = h('input', { type: 'checkbox', checked: t.discovery });
  const en = h('input', { type: 'checkbox', checked: t.enabled });
  const sync = () => { iqn.disabled = disc.checked; iqn.required = !disc.checked; };
  disc.addEventListener('input', sync); sync();
  formDialog({
    title: edit ? `iSCSI-Ziel ${t.name}` : 'iSCSI-Ziel anlegen', meta: 'ISCSI.CONF · ISCSID', wide: true, submit: edit ? 'Übernehmen' : 'Anlegen',
    text: 'Trägt das Ziel in /etc/iscsi.conf ein, aktiviert iscsid und lädt die Konfiguration. Die LUNs erscheinen danach als sd-Geräte. CHAP wird von dieser Oberfläche noch nicht gesetzt.',
    fields: [field('Name', name), field('Adresse des Ziels', addr), field('Port', port, 'Leer = 3260'), field('Ziel-IQN', iqn, 'Leer lassen bei Discovery'),
      h('div', { class: 'wide' }, field('Initiator-Name (optional)', init, 'Muss auf dem Array für diesen Host freigegeben sein.')),
      h('label', { class: 'check' }, disc, 'Nur Discovery (Volumes auflisten)'), h('label', { class: 'check' }, en, 'Aktiv (Sitzung aufbauen)')],
    onSubmit: async () => {
      const body = { name: name.value.trim(), addr: addr.value.trim(), port: Number(port.value) || 0, iqn: iqn.value.trim(), initiator: init.value.trim(), discovery: disc.checked, enabled: en.checked };
      const r = edit ? await put(`/api/san/iscsi/${enc(t.name)}`, body) : await post('/api/san/iscsi', body);
      watchJob(r.job);
      onDone();
    },
  });
}

function initDialog(d, onDone) {
  const name = h('input', { required: true, pattern: '[a-z0-9][a-z0-9\\-]{0,30}', placeholder: 'vm-pool', value: '' });
  const ok = h('input', { required: true, autocomplete: 'off', spellcheck: 'false', placeholder: d.name });
  formDialog({
    title: `${d.name} als Datastore anlegen`, meta: 'FDISK · DISKLABEL · NEWFS', wide: true, submit: 'Disk löschen und anlegen', danger: true,
    text: `${d.vendor} ${d.model} · ${bytes(d.size)} · ${d.wwn || 'ohne WWN'}. Die komplette Disk wird neu partitioniert und formatiert (FFS), alle vorhandenen Daten gehen verloren. Ein FFS-Dateisystem darf nie von zwei Hosts gleichzeitig eingehängt sein – bei mehreren Hosts braucht jeder Host eigene LUNs.`,
    fields: [h('div', { class: 'wide' }, field('Name des Datastores', name, 'Wird unter /var/vmd/<name> eingehängt.')),
      d.labeled ? h('div', { class: 'note bad wide', role: 'alert' }, `Die Disk trägt schon ein Label (DUID ${d.duid}) – vermutlich Daten, eventuell von einem anderen Host benutzt.`) : null,
      h('div', { class: 'wide' }, field(`Zur Bestätigung „${d.name}“ eingeben`, ok))],
    onSubmit: async () => {
      if (ok.value.trim() !== d.name) throw new Error(`Bitte „${d.name}“ eingeben.`);
      const r = await post('/api/san/datastores', { disk: d.name, name: name.value.trim() });
      watchJob(r.job);
      onDone();
    },
  });
}

export default {
  title: 'Geteilter Speicher',
  async render(main) {
    const body = h('div', { class: 'content' }, loading());
    const addBtn = h('button', { class: 'cta', type: 'button' }, 'iSCSI-Ziel anlegen');
    main.append(pageHead('06 / SPEICHER', 'Geteilter Speicher', 'Externer Speicher per iSCSI und Fibre Channel, Datastores für VM-Disks. NVMe-oF kennt OpenBSD noch nicht.', addBtn), body);
    let data = null;

    const act = (btn, fn) => btn.addEventListener('click', () => busy(btn, fn).catch(toastError));
    const mini = (label, fn, cls = 'mini-btn') => { const b = h('button', { class: cls, type: 'button' }, label); act(b, fn); return b; };

    async function load() { data = await get('/api/san'); draw(); }

    function drawDatastores() {
      const rows = data.datastores.map(d => h('tr', null,
        h('td', null, h('a', { class: 'name', href: `#/datastore/${enc(d.name)}` }, d.name), h('span', { class: 'sub mono' }, d.mount)),
        h('td', { class: 'mono' }, d.present ? d.disk : h('span', { class: 'sub' }, 'Disk fehlt'), h('span', { class: 'sub mono' }, d.duid)),
        h('td', null, d.mounted ? h('div', { class: d.percent > 90 ? 'bar crit' : d.percent > 75 ? 'bar warn' : 'bar', role: 'meter', 'aria-valuenow': String(d.percent), 'aria-valuemin': '0', 'aria-valuemax': '100', 'aria-label': `${d.name} belegt` }, (() => { const i = h('i'); i.style.width = d.percent + '%'; return i; })()) : null,
          h('span', { class: 'sub' }, d.mounted ? `${bytes(d.used)} von ${bytes(d.size)} · ${d.percent} %` : bytes(d.size))),
        h('td', null, d.mounted ? pill('ok', 'eingehängt') : d.present ? pill('off', 'ausgehängt') : pill('bad', 'nicht vorhanden'), d.busy.length ? h('span', { class: 'sub' }, `VMs: ${d.busy.join(', ')}`) : null),
        h('td', { class: 'actions' },
          d.mounted ? mini('Aushängen', async () => { await post(`/api/san/datastores/${enc(d.name)}/unmount`); toast('Ausgehängt.'); await load(); })
            : mini('Einhängen', async () => { await post(`/api/san/datastores/${enc(d.name)}/mount`); toast('Eingehängt.'); await load(); }),
          mini('Entfernen', async () => {
            if (!await confirm({ title: `${d.name} entfernen?`, text: 'Der Datastore wird ausgehängt und aus fstab und maar gelöscht. Die Daten auf der Disk bleiben unberührt.', ok: 'Entfernen', danger: true, typeToConfirm: d.name })) return;
            await del(`/api/san/datastores/${enc(d.name)}`); toast('Entfernt.'); await load();
          }, 'mini-btn danger'))));
      return panel('Datastores', 'EINGEHÄNGT UNTER /VAR/VMD', h('div', { class: 'panel-body flush' },
        table(['Datastore', 'Disk', 'Belegung', 'Status', ''], rows, 'Noch kein Datastore. Initialisiere unten eine freie SAN-Disk.'),
        h('div', { class: 'note warn' }, 'Datastores sind FFS-Dateisysteme und nur auf einem Host gleichzeitig einhängbar. Nach einem Neustart stehen sie ausgehängt da (iSCSI ist beim Booten noch nicht verfügbar) – hier einhängen.')));
    }

    function drawDisks() {
      const rows = data.disks.map(d => {
        const [k, t] = TRANSPORT[d.transport] || ['off', d.transport];
        return h('tr', null,
          h('td', null, h('span', { class: 'name mono' }, d.name), h('span', { class: 'sub mono' }, d.bus)),
          h('td', null, pill(k, t)),
          h('td', null, `${d.vendor} ${d.model}`, d.wwn ? h('span', { class: 'sub mono' }, d.wwn) : null),
          h('td', { class: 'num' }, bytes(d.size)),
          h('td', null, d.datastore ? [pill('ok', 'Datastore'), h('span', { class: 'sub' }, d.datastore)] : d.canInit ? [pill('info', 'frei'), d.labeled ? h('span', { class: 'sub' }, 'trägt ein Label') : null] : h('span', { class: 'sub' }, d.reason || '–')),
          h('td', { class: 'actions' }, d.canInit ? h('button', { class: 'mini-btn', type: 'button', onclick: () => initDialog(d, load) }, 'Als Datastore …') : null));
      });
      return panel('Disks und LUNs', 'DMESG · SCSIBUS', h('div', { class: 'panel-body flush' }, table(['Disk', 'Transport', 'Gerät', { label: 'Größe', num: true }, 'Verwendung', ''], rows, 'Keine Disks gefunden.')));
    }

    function drawISCSI() {
      const i = data.iscsi;
      const rows = data.targets.map(t => h('tr', null,
        h('td', null, h('span', { class: 'name' }, t.name), h('span', { class: 'sub mono' }, t.discovery ? 'Discovery' : t.iqn)),
        h('td', { class: 'mono' }, `${t.addr}:${t.port || 3260}`),
        h('td', null, t.state === 'angemeldet' ? pill('ok', t.state) : t.state === 'deaktiviert' ? pill('off', t.state) : pill('warn', t.state)),
        h('td', { class: 'actions' },
          h('button', { class: 'mini-btn', type: 'button', onclick: () => targetDialog(t, load) }, 'Bearbeiten'),
          mini(t.enabled ? 'Deaktivieren' : 'Aktivieren', async () => { const r = await put(`/api/san/iscsi/${enc(t.name)}`, { ...t, enabled: !t.enabled }); watchJob(r.job); }),
          mini('Löschen', async () => {
            if (!await confirm({ title: `Ziel ${t.name} löschen?`, text: 'Die Sitzung wird beendet; die LUNs verschwinden von diesem Host. Datastores darauf müssen vorher ausgehängt sein.', ok: 'Löschen', danger: true, typeToConfirm: t.name })) return;
            watchJob((await del(`/api/san/iscsi/${enc(t.name)}`)).job);
          }, 'mini-btn danger'))));
      return panel('iSCSI', 'ISCSID · ISCSICTL', h('div', { class: 'panel-body flush' },
        h('div', { class: 'panel-body toolbar' }, i.running ? pill('ok', 'iscsid läuft') : i.enabled ? pill('bad', 'iscsid gestoppt') : pill('off', 'iscsid aus'), h('span', { class: 'tag' }, `${data.targets.length} Ziele`)),
        data.foreignTargets.length ? h('div', { class: 'note warn' }, `Von Hand in /etc/iscsi.conf: ${data.foreignTargets.join(', ')} – diese bleiben unangetastet und erscheinen hier nicht. `, h('a', { href: '#/files?path=/etc/iscsi.conf' }, 'Datei öffnen')) : null,
        table(['Ziel', 'Adresse', 'Sitzung', ''], rows, 'Kein iSCSI-Ziel konfiguriert.'),
        i.summary ? h('pre', { class: 'codeblock' }, i.summary) : null));
    }

    function drawFC() {
      const rows = data.hbas.map(x => h('tr', null,
        h('td', null, h('span', { class: 'name mono' }, x.name), h('span', { class: 'sub' }, x.model)),
        h('td', { class: 'mono' }, fmtWWPN(x.wwpn)),
        h('td', { class: 'mono' }, x.disks.length ? x.disks.join(', ') : '–')));
      return panel('Fibre Channel', 'QLA · QLW · QLE', h('div', { class: 'panel-body flush' },
        table(['HBA', 'WWPN (Initiator)', 'LUNs'], rows, 'Keine FC-HBA gefunden (unterstützt: qla, qlw, qle).'),
        h('div', { class: 'note' }, 'Gib diese WWPNs im SAN-Switch (Zoning) und am Array (LUN-Masking) für diesen Host frei. LUNs erscheinen danach von selbst als sd-Geräte; eine Konfiguration auf dem Host ist nicht nötig. Mehrere Pfade zur selben LUN zeigt OpenBSD als getrennte Geräte, Multipath-Failover über mpath(4) ist nur für bestimmte Arrays verfügbar.')));
    }

    function drawNVMe() {
      return panel('NVMe over Fabrics', 'NICHT VERFÜGBAR', h('div', { class: 'panel-body' },
        h('div', { class: 'note warn' }, data.nvmeof.note),
        data.nvmeof.local.length ? h('p', { class: 'hint' }, 'Lokale NVMe-Laufwerke: ', data.nvmeof.local.map(l => h('span', { class: 'tag mono' }, l))) : null));
    }

    function draw() {
      const sanDisks = data.disks.filter(d => d.transport !== 'local');
      clear(body,
        h('div', { class: 'metrics' },
          metric('iSCSI-Ziele', String(data.targets.length), data.iscsi.running ? 'iscsid läuft' : 'iscsid gestoppt'),
          metric('FC-HBAs', String(data.hbas.length), data.hbas.map(x => x.name).join(', ') || 'keine'),
          metric('SAN-Disks', String(sanDisks.length), bytes(sanDisks.reduce((n, d) => n + d.size, 0))),
          metric('Datastores', `${data.datastores.filter(d => d.mounted).length} / ${data.datastores.length}`, 'eingehängt')),
        drawDatastores(), drawDisks(), drawISCSI(), drawFC(), drawNVMe());
    }

    addBtn.addEventListener('click', () => targetDialog(null, load));
    const off = onJobDone(() => setTimeout(() => load().catch(() => {}), 500));
    await load();
    const t = setInterval(() => { if (!document.querySelector('dialog')) load().catch(() => {}); }, 20000);
    return () => { clearInterval(t); off(); };
  },
};
