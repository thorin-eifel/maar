import { h, clear, pageHead, panel, table, bytes, vmStatePill, pill, loading, toast, toastError, confirm, formDialog, field, busy } from '../ui.js';
import { get, post, put, del, q, upload } from '../api.js';
import { watchJob, onJobDone } from '../jobs.js';
import { deployDialog, toTemplateDialog } from '../deploy.js';
import { backupNowDialog } from './backup.js';
import { vmSettingsDialog, memoryField, cpuField, sevField, ownerField } from '../vmsettings.js';
import { startFromISODialog, editBlockDialog, removeVMDialog, hardKillDialog } from '../vmactions.js';
import { nicDialog, firewallDialog } from './vnet.js';

const OPENBSD_ISO = 'https://cdn.openbsd.org/pub/OpenBSD/8.0/amd64/install80.iso';

export default {
  title: 'Virtuelle Maschinen',
  async render(main, parts) {
    if (parts[0] && parts[1] === 'snapshots') {
      this.title = `Snapshots ${parts[0]}`;
      return (await import('./snapshots.js')).default.render(main, parts[0]);
    }
    this.title = 'Virtuelle Maschinen';
    const body = h('div', { class: 'content' }, loading());
    const newBtn = h('button', { class: 'cta', type: 'button' }, 'VM anlegen');
    const refreshBtn = h('button', { class: 'secondary', type: 'button' }, 'Aktualisieren');
    main.append(pageHead('03 / INVENTAR', 'Virtuelle Maschinen', 'vmd(8) und vm.conf(5): VMs anlegen, starten, stoppen und direkt über die serielle Konsole bedienen.', refreshBtn, newBtn), body);
    let data = null;

    async function load() {
      data = await get('/api/vms');
      draw();
    }

    function act(vm, action, opt) {
      return post(`/api/vms/${encodeURIComponent(vm.name)}/${action}`, opt).then(r => { toast(r.message); return load(); });
    }

    function vmCard(vm) {
      const c = vm.conf || {};
      const running = vm.state === 'running', paused = vm.state === 'paused';
      const viewers = data.viewers['vm:' + vm.name] || 0;
      const btn = (label, fn, cls = 'mini-btn') => {
        const b = h('button', { class: cls, type: 'button' }, label);
        b.addEventListener('click', () => busy(b, fn).catch(toastError));
        return b;
      };
      const actions = [];
      if (!running && !paused) {
        actions.push(btn('Starten', () => act(vm, 'start')));
        if (data.overview.isos.length) actions.push(btn('Von ISO starten', async () => startFromISODialog(vm.name, data.overview.isos, load)));
      }
      if (running) {
        actions.push(h('a', { class: 'mini-btn', href: `#/console/vm/${encodeURIComponent(vm.name)}` }, 'Konsole'));
        actions.push(btn('Herunterfahren', () => act(vm, 'stop')));
        actions.push(btn('Pausieren', () => act(vm, 'pause')));
      }
      if (paused) actions.push(btn('Fortsetzen', () => act(vm, 'unpause')));
      if (running || paused) actions.push(btn('Hart beenden', async () => {
        if (await hardKillDialog(vm.name)) await act(vm, 'kill');
      }, 'mini-btn danger'));
      if (vm.conf) actions.push(btn('Einstellungen', async () => vmSettingsDialog(vm.name, { onDone: load, onExpert: () => editBlockDialog(vm, load) })));
      if (vm.conf) actions.push(btn('Netzwerk', () => nicDialog(vm.name, load)), btn('Firewall', () => firewallDialog(vm.name, load)));
      if (vm.conf && c.disks.length) actions.push(btn('Jetzt sichern', () => backupNowDialog([vm.name])));
      if (vm.conf && c.disks.length) actions.push(h('a', { class: 'mini-btn', href: `#/vms/${encodeURIComponent(vm.name)}/snapshots` }, vm.snapshots ? `Snapshots (${vm.snapshots})` : 'Snapshots'));
      if (vm.conf && !running && !paused && c.disks.length) {
        actions.push(btn('Klonen', () => deployDialog({ source: 'vm', from: vm.name, memory: c.memory || '1G', cpus: c.cpus || 1 })));
        actions.push(btn('Als Template', async () => toTemplateDialog(vm)));
      }
      if (vm.conf && !running && !paused) actions.push(btn('Löschen', async () => { if (await removeVMDialog(vm)) await load(); }, 'mini-btn danger'));

      const nets = (c.interfaces || []).map(i => i.name === 'local' ? 'lokal (100.64/10)' : i.switch ? `Switch ${i.switch}` : i.name || 'tap').join(', ');
      return h('article', { class: 'vm-card', 'aria-labelledby': `vm-${vm.name}` },
        h('header', null,
          h('div', null, h('span', { class: 'meta' }, vm.id !== '-' ? `ID ${vm.id} · PID ${vm.pid}` : 'nicht gestartet'), h('h3', { id: `vm-${vm.name}` }, h('a', { href: `#/vm/${encodeURIComponent(vm.name)}`, class: 'name-link' }, vm.name))),
          vmStatePill(vm.state)),
        h('dl', { class: 'kv' },
          h('dt', null, 'Speicher'), h('dd', null, running ? `${vm.curmem} / ${vm.maxmem}` : (c.memory || vm.maxmem || '–')),
          h('dt', null, 'vCPUs'), h('dd', null, vm.vcpus || c.cpus || 1, c.sev ? [' ', h('span', { class: 'tag' }, c.sev.toUpperCase())] : null),
          h('dt', null, 'Disks'), h('dd', { class: 'mono' }, (c.disks || []).length ? c.disks.map(d => h('div', null, d)) : '–'),
          c.cdrom ? [h('dt', null, 'CD-ROM'), h('dd', { class: 'mono' }, c.cdrom)] : null,
          h('dt', null, 'Netz'), h('dd', null, nets || '–'),
          h('dt', null, 'Autostart'), h('dd', null, vm.conf ? (c.enabled ? 'ja' : 'nein') : 'nicht in vm.conf'),
          running ? [h('dt', null, 'Konsole'), h('dd', { class: 'mono' }, `/dev/${vm.tty}${viewers ? ` · ${viewers} Zuschauer` : ''}`)] : null,
          c.owner ? [h('dt', null, 'Besitzer'), h('dd', null, c.owner)] : null),
        h('footer', null, actions));
    }

    async function createVM() {
      const ov = data.overview;
      const o = await get('/api/vm-options');
      const name = h('input', { required: true, pattern: '[A-Za-z0-9][A-Za-z0-9._\\-]{0,63}', placeholder: 'z. B. web02', autocomplete: 'off' });
      const mem = memoryField(o, '1G'), cpus = cpuField(o, 1), sev = sevField(o, ''), owner = ownerField(o, '');
      const diskMode = h('select', null,
        h('option', { value: 'new' }, 'Neue Disk anlegen'),
        ov.images.length ? h('option', { value: 'base' }, 'Neue Disk auf Basis-Image (qcow2-Overlay)') : null,
        ov.images.length ? h('option', { value: 'existing' }, 'Vorhandenes Image verwenden') : null,
        h('option', { value: 'none' }, 'Keine Disk'));
      const size = h('input', { value: '20G', pattern: '[0-9]{1,6}[KMGT]?' });
      const fmt = h('select', null, h('option', { value: 'qcow2' }, 'qcow2 (dünn belegt)'), h('option', { value: 'raw' }, 'raw'));
      const img = h('select', null, ov.images.map(i => h('option', { value: i.path }, `${i.name} (${bytes(i.size)})`)));
      const iso = h('select', null, h('option', { value: '' }, '– kein Medium –'), ov.isos.map(i => h('option', { value: i.path }, i.name)));
      const bootCD = h('input', { type: 'checkbox', checked: true });
      const sw = ov.switches.map(s => ({ s, cb: h('input', { type: 'checkbox', checked: ov.switches.length === 1 }) }));
      const local = h('input', { type: 'checkbox', checked: !ov.switches.length });
      const enabled = h('input', { type: 'checkbox', checked: true });
      const startNow = h('input', { type: 'checkbox', checked: true });

      const sizeF = field('Größe', size, 'z. B. 20G');
      const fmtF = field('Format', fmt);
      const imgF = field('Image', img);
      const sync = () => {
        const m = diskMode.value;
        sizeF.hidden = fmtF.hidden = m !== 'new';
        imgF.hidden = m !== 'base' && m !== 'existing';
      };
      diskMode.addEventListener('change', sync);
      sync();

      formDialog({
        title: 'Neue virtuelle Maschine', meta: 'VMCTL CREATE · VM.CONF', wide: true, submit: 'VM anlegen',
        text: 'Die VM wird dauerhaft in /etc/vm.conf eingetragen. Für eine Installation ein ISO wählen und von CD-ROM booten; danach Konsole öffnen.',
        fields: [
          field('Name', name), mem.el, cpus.el, sev.el,
          h('div', { class: 'wide' }, field('Festplatte', diskMode)), sizeF, fmtF, imgF,
          field('Installationsmedium', iso, `ISOs liegen unter ${ov.vmDir}/iso`),
          h('label', { class: 'check' }, bootCD, 'Von CD-ROM booten (für die Installation)'),
          h('fieldset', { class: 'wide field' }, h('legend', null, 'Netzwerk'),
            h('div', { class: 'toolbar' }, sw.map(({ s, cb }) => h('label', { class: 'check' }, cb, `Switch ${s.name} (${s.interface})`)),
              h('label', { class: 'check' }, local, 'Lokale Schnittstelle (NAT über vmd, 100.64.0.0/10)'))),
          owner.el,
          h('div', { class: 'toolbar' }, h('label', { class: 'check' }, enabled, 'Beim Systemstart starten'), h('label', { class: 'check' }, startNow, 'Sofort starten')),
        ],
        onSubmit: async () => {
          const m = diskMode.value;
          const spec = {
            name: name.value.trim(), memory: mem.value(), cpus: cpus.value(), sev: sev.value(), owner: owner.value(), enabled: enabled.checked, startNow: startNow.checked,
            cdrom: iso.value, bootCdrom: !!iso.value && bootCD.checked, local: local.checked,
            switches: sw.filter(x => x.cb.checked).map(x => x.s.name),
          };
          if (m === 'new') { spec.diskSize = size.value.trim(); spec.diskFormat = fmt.value; }
          if (m === 'base') spec.baseDisk = img.value;
          if (m === 'existing') spec.diskPath = img.value;
          const r = await post('/api/vms', spec);
          toast(r.message);
          await load();
          if (spec.startNow && spec.bootCdrom) location.hash = `#/console/vm/${encodeURIComponent(spec.name)}`;
        },
      });
    }

    function createDisk() {
      const name = h('input', { required: true, pattern: '[A-Za-z0-9][A-Za-z0-9._\\-]{0,63}', autocomplete: 'off' });
      const size = h('input', { value: '20G', required: true, pattern: '[0-9]{1,6}[KMGT]?' });
      const fmt = h('select', null, h('option', { value: 'qcow2' }, 'qcow2'), h('option', { value: 'raw' }, 'raw'));
      const store = h('select', null, h('option', { value: '' }, `lokal (${data.overview.vmDir})`), (data.overview.datastores || []).map(s => h('option', { value: s }, `Datastore ${s}`)));
      const base = h('select', null, h('option', { value: '' }, '– keines –'), data.overview.images.filter(i => i.name.endsWith('.qcow2')).map(i => h('option', { value: i.path }, i.name)));
      formDialog({
        title: 'Disk-Image anlegen', meta: 'VMCTL CREATE', submit: 'Anlegen',
        fields: [field('Dateiname', name), field('Speicherort', store, 'Datastores kommen aus „Geteilter Speicher“.'), field('Größe', size), field('Format', fmt), field('Basis-Image', base, 'Overlay auf ein bestehendes qcow2 (Größe wird übernommen)')],
        onSubmit: async () => { const r = await post('/api/vm-disks', { name: name.value.trim(), size: size.value.trim(), format: fmt.value, base: base.value, store: store.value }); toast(r.message); await load(); },
      });
    }

    function downloadISO() {
      const url = h('input', { type: 'url', required: true, value: OPENBSD_ISO });
      const name = h('input', { placeholder: 'aus der Adresse übernehmen' });
      formDialog({
        title: 'ISO herunterladen', meta: 'FTP(1)', submit: 'Download starten', wide: true,
        text: 'Lädt ein Installationsmedium direkt auf den Host. Der Fortschritt erscheint unter „Aufgaben“.',
        fields: [h('div', { class: 'wide' }, field('Adresse', url)), field('Dateiname', name)],
        onSubmit: async () => { const r = await post('/api/vm-isos/download', { url: url.value.trim(), name: name.value.trim() }); watchJob(r.job); },
      });
    }

    function uploadISO() {
      const file = h('input', { type: 'file', accept: '.iso,.img,.fs', required: true });
      const prog = h('progress', { max: '1', value: '0', hidden: true });
      formDialog({
        title: 'ISO hochladen', meta: 'UPLOAD', submit: 'Hochladen',
        fields: [h('div', { class: 'wide' }, field('Datei', file)), h('div', { class: 'wide' }, prog)],
        onSubmit: async () => {
          const f = file.files[0];
          prog.hidden = false;
          const r = await upload('/api/vm-isos/upload' + q({ name: f.name }), f, p => { prog.value = p; });
          toast(r.message);
          await load();
        },
      });
    }


    function imageRows(list, kind) {
      return list.map(i => {
        const delBtn = h('button', { class: 'mini-btn danger', type: 'button', disabled: i.usedBy.length > 0, title: i.usedBy.length ? 'wird verwendet' : null }, 'Löschen');
        delBtn.addEventListener('click', async () => {
          if (!await confirm({ title: `${i.name} löschen?`, text: `${i.path} wird endgültig entfernt.`, ok: 'Löschen', danger: true, typeToConfirm: kind === 'disk' ? i.name : undefined })) return;
          busy(delBtn, () => del('/api/vm-images' + q({ path: i.path })).then(r => { toast(r.message); return load(); })).catch(toastError);
        });
        return h('tr', null,
          h('td', null, h('span', { class: 'name mono' }, i.name), h('span', { class: 'sub mono' }, i.path)),
          h('td', { class: 'num' }, bytes(i.size)),
          h('td', null, i.usedBy.length ? i.usedBy.map(u => h('span', { class: 'tag' }, u)) : h('span', { class: 'sub' }, 'frei')),
          h('td', { class: 'actions' }, delBtn));
      });
    }

    function draw() {
      const ov = data.overview;
      const startVmd = h('button', { class: 'cta', type: 'button' }, 'vmd aktivieren und starten');
      startVmd.addEventListener('click', () => busy(startVmd, async () => {
        await post('/api/services/vmd/enable');
        await post('/api/services/vmd/start');
        toast('vmd gestartet.');
        await load();
      }).catch(toastError));
      const sw = ov.switches.map(s => h('tr', null, h('td', { class: 'name' }, s.name), h('td', { class: 'mono' }, s.interface), h('td', null, s.enabled ? pill('ok', 'aktiv') : pill('off', 'deaktiviert'))));

      clear(body,
        !ov.vmdRunning ? h('div', { class: 'note warn toolbar' }, h('span', { class: 'grow' }, ov.vmdEnabled ? 'vmd ist aktiviert, läuft aber nicht.' : 'vmd ist nicht aktiviert. Ohne vmd können keine VMs laufen.'), startVmd) : null,
        ov.confError ? h('div', { class: 'note bad' }, 'vm.conf konnte nicht gelesen werden: ' + ov.confError) : null,
        h('div', { class: 'section-title' }, h('div', null, h('span', { class: 'section-index' }, `${ov.vms.length} VMS`), h('h2', null, 'Maschinen'))),
        ov.vms.length ? h('div', { class: 'vm-grid' }, ov.vms.map(vmCard)) : h('div', { class: 'panel empty' }, 'Noch keine VM definiert. Mit „VM anlegen“ beginnen.'),
        panel('Disk-Images', `${ov.vmDir}`, h('div', { class: 'panel-body flush' }, table(['Image', { label: 'Größe', num: true }, 'Verwendet von', ''], imageRows(ov.images, 'disk'), 'Keine Disk-Images vorhanden.')),
          h('button', { class: 'mini-btn', type: 'button', onclick: createDisk }, 'Disk anlegen')),
        h('div', { class: 'grid-2' },
          panel('Installationsmedien', `${ov.vmDir}/iso`, h('div', { class: 'panel-body flush' }, table(['Datei', { label: 'Größe', num: true }, 'Verwendet von', ''], imageRows(ov.isos, 'iso'), 'Keine ISOs vorhanden.')),
            h('button', { class: 'mini-btn', type: 'button', onclick: downloadISO }, 'Herunterladen'), h('button', { class: 'mini-btn', type: 'button', onclick: uploadISO }, 'Hochladen')),
          panel('Switches', 'VM.CONF · PORTGRUPPEN', h('div', { class: 'panel-body flush' }, table(['Name', 'Schnittstelle', 'Status'], sw, 'Keine Switches. VMs können dann nur die lokale Schnittstelle nutzen.')),
            h('a', { class: 'mini-btn', href: '#/vnet' }, 'Portgruppen verwalten'))));
    }

    newBtn.addEventListener('click', () => data && createVM().catch(toastError));
    refreshBtn.addEventListener('click', () => busy(refreshBtn, load).catch(toastError));
    await load();
    const off = onJobDone(() => load().catch(() => {}));
    const t = setInterval(() => { if (!document.querySelector('dialog[open]')) load().catch(() => {}); }, 10000);
    return () => { clearInterval(t); off(); };
  },
};
