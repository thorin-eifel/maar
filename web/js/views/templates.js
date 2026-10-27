import { h, clear, pageHead, panel, bytes, dateTime, loading, toast, toastError, confirm, formDialog, field, busy } from '../ui.js';
import { get, put, del, post } from '../api.js';
import { watchJob, onJobDone } from '../jobs.js';
import { deployDialog, osLabel } from '../deploy.js';

const IMPORTS = [
  ['Debian 13 (genericcloud)', 'https://cloud.debian.org/images/cloud/trixie/latest/debian-13-genericcloud-amd64.qcow2', 'linux'],
  ['Ubuntu 24.04 LTS (cloud image)', 'https://cloud-images.ubuntu.com/releases/noble/release/ubuntu-24.04-server-cloudimg-amd64.img', 'linux'],
];

export default {
  title: 'Templates',
  async render(main) {
    const body = h('div', { class: 'content' }, loading());
    const importBtn = h('button', { class: 'cta', type: 'button' }, 'Template importieren');
    main.append(pageHead('04 / INVENTAR', 'Templates', 'Goldene Images für neue VMs. Bereitstellung als verknüpfter Klon in Sekunden oder als Vollkopie, mit Gastanpassung beim ersten Start.', importBtn), body);
    let data = null;

    async function load() {
      data = await get('/api/templates');
      draw();
    }

    function edit(t) {
      const desc = h('input', { value: t.description });
      const os = h('select', null, ['openbsd', 'linux', 'other'].map(o => h('option', { value: o, selected: o === t.os }, osLabel(o))));
      const mem = h('input', { value: t.memory, pattern: '[0-9]{1,6}[KMGT]?', required: true });
      const cpus = h('input', { type: 'number', min: '1', max: '64', value: t.cpus, required: true });
      formDialog({
        title: `${t.name} bearbeiten`, meta: 'TEMPLATE', submit: 'Speichern',
        fields: [h('div', { class: 'wide' }, field('Beschreibung', desc)), field('Betriebssystem', os), field('Standard-Speicher', mem), field('Standard-vCPUs', cpus)],
        onSubmit: async () => {
          const r = await put(`/api/templates/${encodeURIComponent(t.name)}`, { description: desc.value.trim(), os: os.value, memory: mem.value.trim(), cpus: Number(cpus.value) });
          toast(r.message);
          await load();
        },
      });
    }

    function importDialog() {
      const preset = h('select', null, h('option', { value: '' }, '– eigene Adresse oder vorhandenes Image –'), IMPORTS.map(([l, u]) => h('option', { value: u }, l)));
      const name = h('input', { required: true, pattern: '[A-Za-z0-9][A-Za-z0-9._\\-]{0,63}', autocomplete: 'off' });
      const url = h('input', { type: 'url', placeholder: 'https://…/image.qcow2' });
      const path = h('input', { placeholder: '/var/vmd/vorhanden.qcow2', class: 'mono' });
      const os = h('select', null, ['linux', 'openbsd', 'other'].map(o => h('option', { value: o }, osLabel(o))));
      const desc = h('input');
      const mem = h('input', { value: '1G', pattern: '[0-9]{1,6}[KMGT]?' });
      preset.addEventListener('change', () => {
        const p = IMPORTS.find(x => x[1] === preset.value);
        if (!p) return;
        url.value = p[1];
        os.value = p[2];
        desc.value = p[0];
        if (!name.value) name.value = p[0].toLowerCase().replace(/\(.*\)/, '').trim().replace(/[^a-z0-9]+/g, '-');
      });
      formDialog({
        title: 'Template importieren', meta: 'FTP · VMCTL CREATE -I', wide: true, submit: 'Import starten',
        text: 'Lädt ein Disk-Image (qcow2 oder raw) herunter oder übernimmt eines aus dem VM-Verzeichnis und wandelt es in ein schreibgeschütztes Basisimage um. Linux-Cloud-Images bringen cloud-init bereits mit.',
        fields: [h('div', { class: 'wide' }, field('Vorlage', preset)), field('Name', name), field('Betriebssystem', os),
          h('div', { class: 'wide' }, field('Adresse', url)), h('div', { class: 'wide' }, field('… oder vorhandenes Image', path, `unterhalb von ${data.vmDir}`)),
          field('Beschreibung', desc), field('Standard-Speicher', mem)],
        onSubmit: async () => {
          if (!url.value.trim() === !path.value.trim()) throw new Error('Entweder eine Adresse oder ein vorhandenes Image angeben.');
          const r = await post('/api/templates/import', { name: name.value.trim(), os: os.value, description: desc.value.trim(), memory: mem.value.trim(), url: url.value.trim(), path: path.value.trim() });
          watchJob(r.job);
        },
      });
    }

    function card(t) {
      const btn = (label, fn, cls = 'mini-btn') => { const b = h('button', { class: cls, type: 'button' }, label); b.addEventListener('click', () => busy(b, fn).catch(toastError)); return b; };
      return h('article', { class: 'vm-card', 'aria-labelledby': `tpl-${t.name}` },
        h('header', null,
          h('div', null, h('span', { class: 'meta' }, osLabel(t.os)), h('h3', { id: `tpl-${t.name}` }, t.name)),
          h('span', { class: 'pill info' }, t.clones.length === 1 ? '1 Klon' : `${t.clones.length} Klone`)),
        h('dl', { class: 'kv' },
          t.description ? [h('dt', null, 'Beschreibung'), h('dd', null, t.description)] : null,
          h('dt', null, 'Standard'), h('dd', null, `${t.memory} · ${t.cpus} vCPU${t.cpus > 1 ? 's' : ''}`),
          h('dt', null, 'Image'), h('dd', { class: 'mono' }, `${bytes(t.size)}`),
          h('dt', null, 'Quelle'), h('dd', { class: 'mono' }, t.source || '–'),
          h('dt', null, 'Erstellt'), h('dd', null, t.created ? dateTime(t.created) : '–'),
          t.clones.length ? [h('dt', null, 'Klone'), h('dd', null, t.clones.map(c => h('span', { class: 'tag' }, c)))] : null),
        h('footer', null,
          btn('VM bereitstellen', () => deployDialog({ source: 'template', from: t.name, os: t.os, memory: t.memory, cpus: t.cpus }), 'mini-btn'),
          btn('Bearbeiten', async () => edit(t)),
          btn('Löschen', async () => {
            if (!await confirm({ title: `Template ${t.name} löschen?`, text: `${t.disk} wird entfernt. Vollkopien sind davon unabhängig.`, ok: 'Löschen', danger: true, typeToConfirm: t.name })) return;
            const r = await del(`/api/templates/${encodeURIComponent(t.name)}`);
            toast(r.message);
            await load();
          }, 'mini-btn danger')));
    }

    function draw() {
      clear(body,
        data.templates.length ? h('div', { class: 'vm-grid' }, data.templates.map(card))
          : h('div', { class: 'panel empty' }, 'Noch keine Templates. Importiere ein Cloud-Image oder speichere eine vorbereitete VM als Template.'),
        h('div', { class: 'grid-2' },
          panel('OpenBSD-Gäste vorbereiten', 'MAAR-GUEST', h('div', { class: 'panel-body' },
            h('p', null, 'OpenBSD hat kein cloud-init. Das kleine Skript maar-guest liest beim Start die Seed-ISO und setzt Hostname, Netz, Benutzer, Passwort-Hash, SSH-Schlüssel und neue Hostschlüssel. Einmal im Template installieren:'),
            h('pre', { class: 'codeblock' }, `ftp -o /usr/local/libexec/maar-guest \\\n    ${location.origin}/guest/maar-guest.sh\nchmod 0555 /usr/local/libexec/maar-guest\necho /usr/local/libexec/maar-guest >> /etc/rc.local`),
            h('div', { class: 'toolbar' }, h('a', { class: 'mini-btn', href: '/guest/maar-guest.sh', download: 'maar-guest.sh' }, 'Skript herunterladen')))),
          panel('Linux-Gäste', 'CLOUD-INIT · NOCLOUD', h('div', { class: 'panel-body' },
            h('p', null, 'Cloud-Images von Debian, Ubuntu, Alpine und Co. werten die Seed-ISO (Label CIDATA) automatisch aus: Hostname, Netz (netplan v2), Benutzer mit sudo, Passwort und SSH-Schlüssel.'),
            h('div', { class: 'note warn' }, 'vmd zeigt nur die serielle Konsole. Images mit Kernel-Ausgabe auf ttyS0 wählen (bei Cloud-Images Standard).')))));
    }

    importBtn.addEventListener('click', () => data && importDialog());
    await load();
    return onJobDone(() => load().catch(() => {}));
  },
};
