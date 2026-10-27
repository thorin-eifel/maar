// Dialoge zum Bereitstellen (aus Template oder VM) und „Als Template speichern“.
import { h, formDialog, field } from './ui.js';
import { get, post } from './api.js';
import { watchJob } from './jobs.js';

const OS_LABEL = { openbsd: 'OpenBSD', linux: 'Linux (cloud-init)', other: 'Sonstiges' };
export const osLabel = os => OS_LABEL[os] || os;

const sizeOpts = (sel, list) => list.map(m => h('option', { value: m, selected: m === sel }, m));
const cpuOpts = sel => [1, 2, 4, 8, 16, 32, 64].map(n => h('option', { value: n, selected: n === sel }, n === 1 ? '1 vCPU' : `${n} vCPUs`));

// deployDialog: source = 'template' | 'vm'.
export async function deployDialog({ source, from, os = 'other', memory = '1G', cpus = 1 }) {
  const ov = (await get('/api/vms')).overview;
  const name = h('input', { required: true, pattern: '[A-Za-z0-9][A-Za-z0-9._\\-]{0,63}', autocomplete: 'off', placeholder: 'z. B. app01' });
  const mem = h('select', null, sizeOpts(memory, ['512M', '1G', '2G', '4G', '8G', '16G', '32G', '64G']));
  const cpu = h('select', null, cpuOpts(cpus));
  const linked = h('select', null,
    h('option', { value: 'linked' }, 'Verknüpfter Klon – Sekunden, teilt das Basisimage'),
    h('option', { value: 'full' }, 'Vollkopie – eigenständig, braucht Zeit und Platz'));
  const sw = ov.switches.map(s => ({ s, cb: h('input', { type: 'checkbox', checked: ov.switches.length === 1 }) }));
  const local = h('input', { type: 'checkbox', checked: !ov.switches.length });
  const enabled = h('input', { type: 'checkbox', checked: true });
  const startNow = h('input', { type: 'checkbox', checked: true });

  // Gastanpassung
  const custom = h('input', { type: 'checkbox', checked: os !== 'other' });
  const guestOS = h('select', null, ['openbsd', 'linux'].map(o => h('option', { value: o, selected: o === os }, osLabel(o))));
  const hostname = h('input', { autocomplete: 'off', placeholder: 'wie der VM-Name' });
  const net = h('select', null, h('option', { value: 'dhcp' }, 'DHCP'), h('option', { value: 'static' }, 'Statisch'), h('option', { value: 'none' }, 'Nicht anfassen'));
  const addr = h('input', { placeholder: '10.0.0.20/24', autocomplete: 'off' });
  const gw = h('input', { placeholder: '10.0.0.1', autocomplete: 'off' });
  const dns = h('input', { placeholder: '10.0.0.1, 9.9.9.9', autocomplete: 'off' });
  const user = h('input', { placeholder: 'leer = root', autocomplete: 'off', pattern: '[a-z_][a-z0-9_\\-]{0,30}' });
  const pw = h('input', { type: 'password', minlength: '8', autocomplete: 'new-password' });
  const key = h('textarea', { rows: 3, spellcheck: 'false', placeholder: 'ssh-ed25519 AAAA… name@rechner' });

  const addrF = field('Adresse', addr), gwF = field('Gateway', gw), dnsF = field('Nameserver', dns, 'durch Komma getrennt');
  const guestBox = h('div', { class: 'form-grid wide' },
    field('Gastsystem', guestOS, 'OpenBSD braucht maar-guest im Template, Linux cloud-init'),
    field('Hostname', hostname), field('Netz', net), addrF, gwF, dnsF,
    field('Benutzer', user, 'wird angelegt (wheel/sudo); leer = root'), field('Passwort', pw, 'OpenBSD erhält nur den bcrypt-Hash'),
    h('div', { class: 'wide' }, field('SSH-Schlüssel', key)));
  const sync = () => {
    guestBox.hidden = !custom.checked;
    addrF.hidden = gwF.hidden = net.value !== 'static';
    addr.required = custom.checked && net.value === 'static';
  };
  custom.addEventListener('change', sync);
  net.addEventListener('change', sync);
  name.addEventListener('input', () => { hostname.placeholder = name.value || 'wie der VM-Name'; });
  sync();

  formDialog({
    title: source === 'template' ? `VM aus ${from} bereitstellen` : `${from} klonen`,
    meta: source === 'template' ? 'TEMPLATE · VMCTL CREATE -B' : 'VOLLKOPIE · VMCTL CREATE -I',
    text: source === 'vm' ? 'Die erste Disk der gestoppten VM wird vollständig kopiert. Weitere Disks werden nicht übernommen.' : null,
    wide: true, submit: 'Bereitstellen',
    fields: [
      field('Name', name), field('Arbeitsspeicher', mem), field('Prozessoren', cpu),
      source === 'template' ? field('Klonart', linked) : h('div'),
      h('fieldset', { class: 'wide field' }, h('legend', null, 'Netzwerk'),
        h('div', { class: 'toolbar' }, sw.map(({ s, cb }) => h('label', { class: 'check' }, cb, `Switch ${s.name}`)),
          h('label', { class: 'check' }, local, 'Lokale Schnittstelle'))),
      h('div', { class: 'toolbar wide' }, h('label', { class: 'check' }, enabled, 'Beim Systemstart starten'), h('label', { class: 'check' }, startNow, 'Sofort starten')),
      h('label', { class: 'check wide' }, custom, h('span', null, h('strong', null, 'Gastanpassung beim ersten Start'), ' – Hostname, Netz, Zugang und neue SSH-Hostschlüssel über eine Seed-ISO (CIDATA)')),
      guestBox,
    ],
    onSubmit: async () => {
      const spec = {
        name: name.value.trim(), memory: mem.value, cpus: Number(cpu.value), linked: linked.value === 'linked',
        switches: sw.filter(x => x.cb.checked).map(x => x.s.name), local: local.checked, enabled: enabled.checked, startNow: startNow.checked,
      };
      if (custom.checked) {
        spec.guest = {
          os: guestOS.value, hostname: hostname.value.trim() || spec.name, network: net.value,
          address: addr.value.trim(), gateway: gw.value.trim(),
          dns: dns.value.split(/[\s,]+/).filter(Boolean), user: user.value.trim(), password: pw.value, sshKey: key.value.trim(),
        };
      }
      const path = source === 'template' ? `/api/templates/${encodeURIComponent(from)}/deploy` : `/api/vms/${encodeURIComponent(from)}/clone`;
      const r = await post(path, spec);
      watchJob(r.job);
    },
  });
}

export function toTemplateDialog(vm) {
  const name = h('input', { required: true, pattern: '[A-Za-z0-9][A-Za-z0-9._\\-]{0,63}', value: `${vm.name}-template`, autocomplete: 'off' });
  const os = h('select', null, ['openbsd', 'linux', 'other'].map(o => h('option', { value: o }, osLabel(o))));
  const desc = h('input', { placeholder: 'z. B. OpenBSD 8.0 mit httpd und maar-guest' });
  formDialog({
    title: `${vm.name} als Template speichern`, meta: 'VMCTL CREATE -I', submit: 'Template erstellen',
    text: 'Die erste Disk wird als eigenständiges, schreibgeschütztes Basisimage in die Bibliothek kopiert. Die VM bleibt unverändert. Vorher im Gast aufräumen (Logs, Schlüssel, Verlauf) und für OpenBSD maar-guest installieren.',
    fields: [field('Template-Name', name), field('Betriebssystem', os), h('div', { class: 'wide' }, field('Beschreibung', desc))],
    onSubmit: async () => {
      const r = await post(`/api/vms/${encodeURIComponent(vm.name)}/to-template`, {
        name: name.value.trim(), os: os.value, description: desc.value.trim(),
        memory: vm.conf?.memory || '1G', cpus: vm.conf?.cpus || 1,
      });
      watchJob(r.job);
    },
  });
}
