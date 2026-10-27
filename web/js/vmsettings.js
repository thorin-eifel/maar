// VM-Einstellungen per Auswahl: Alles, was angeboten wird, gibt es auf diesem Host;
// vm.conf wird im Hintergrund geschrieben und von vmd -n geprüft.
import { h, toast, formDialog, field, bytes } from './ui.js';
import { get, post, put } from './api.js';

const enc = encodeURIComponent;
const MEM_PRESETS = ['512M', '1G', '2G', '3G', '4G', '6G', '8G', '12G', '16G', '24G', '32G', '48G', '64G', '96G', '128G', '256G'];
const CPU_PRESETS = [1, 2, 3, 4, 6, 8, 12, 16, 24, 32, 48, 64];
const memBytes = s => ({ M: 2 ** 20, G: 2 ** 30 })[s.slice(-1)] * parseInt(s, 10);

// Arbeitsspeicher: Vorgaben bis zum Hostspeicher, plus der aktuelle Wert und „Andere …“.
export function memoryField(o, current) {
  const list = MEM_PRESETS.filter(m => !o.hostMem || memBytes(m) <= o.hostMem);
  if (current && !list.includes(current)) list.push(current);
  list.sort((a, b) => memBytes(a) - memBytes(b));
  const sel = h('select', null, list.map(m => h('option', { value: m, selected: m === current }, m)), h('option', { value: '__custom' }, 'Andere …'));
  const custom = h('input', { placeholder: 'z. B. 1536M', pattern: '[0-9]{1,6}[MG]', hidden: true, 'aria-label': 'Eigener Wert' });
  sel.addEventListener('change', () => { custom.hidden = sel.value !== '__custom'; custom.required = !custom.hidden; });
  const hint = o.hostMem ? `Host: ${bytes(o.hostMem)}` : '';
  return { el: field('Arbeitsspeicher', h('div', { class: 'stack' }, sel, custom), hint), value: () => sel.value === '__custom' ? custom.value.trim().toUpperCase() : sel.value };
}

export function cpuField(o, current) {
  const max = o.maxCpus || 64;
  const list = [...new Set([...CPU_PRESETS.filter(n => n <= max), current])].sort((a, b) => a - b);
  const sel = h('select', null, list.map(n => h('option', { value: n, selected: n === current }, `${n} vCPU${n === 1 ? '' : 's'}${o.hostCpus && n > o.hostCpus ? ` (mehr als die ${o.hostCpus} Hostkerne)` : ''}`)));
  return { el: field('Prozessoren', sel, `OpenBSD 8.0: bis zu ${max} vCPUs je VM`), value: () => Number(sel.value) };
}

export function sevField(o, current) {
  const sel = h('select', { disabled: !o.sevAvailable && !current }, h('option', { value: '' }, 'aus'), h('option', { value: 'sev', selected: current === 'sev' }, 'SEV'), h('option', { value: 'seves', selected: current === 'seves' }, 'SEV-ES (inkl. SEV)'));
  sel.value = current || '';
  return { el: field('Speicherverschlüsselung', sel, o.sevAvailable ? 'AMD Secure Encrypted Virtualization' : o.sevReason), value: () => sel.value };
}

// Besitzer: Benutzer und Gruppe aus den Konten des Hosts.
export function ownerField(o, current) {
  const [user, group] = (current || '').includes(':') ? current.split(':') : [current || '', ''];
  const mk = (label, items, value, none) => {
    const list = value && !items.includes(value) ? [...items, value] : items;
    const s = h('select', { 'aria-label': label }, h('option', { value: '' }, none), list.map(i => h('option', { value: i, selected: i === value }, items.includes(i) ? i : `${i} (unbekannt)`)));
    s.value = value || '';
    return s;
  };
  const u = mk('Benutzer', o.users, user, '— kein Benutzer —'), g = mk('Gruppe', o.groups, group, '— keine Gruppe —');
  return { el: h('div', { class: 'wide form-grid' }, field('Besitzer (Benutzer)', u, 'darf die VM ohne root per vmctl steuern'), field('Besitzer (Gruppe)', g)),
    value: () => u.value && g.value ? `${u.value}:${g.value}` : u.value || (g.value ? `:${g.value}` : '') };
}

// Datenträger: Reihenfolge, Auswahl aus freien Images, neues Image anlegen.
function disksField(o, current) {
  let disks = [...current];
  const box = h('div', { class: 'disk-edit' });
  const byPath = Object.fromEntries(o.images.map(i => [i.path, i]));
  const label = p => { const i = byPath[p]; return i ? `${i.name} (${bytes(i.size)})` : p.split('/').pop(); };
  let created = [];
  const available = () => [...o.images.map(i => i.path), ...created].filter(p => !disks.includes(p));

  function draw() {
    const rows = disks.map((p, i) => h('div', { class: 'disk-row' },
      h('span', { class: 'mono disk-name', title: p }, i === 0 ? '1 · Boot  ' : `${i + 1}  `, label(p)),
      h('div', { class: 'toolbar' },
        h('button', { class: 'mini-btn', type: 'button', disabled: i === 0, 'aria-label': 'nach oben', onclick: () => { [disks[i - 1], disks[i]] = [disks[i], disks[i - 1]]; draw(); } }, '↑'),
        h('button', { class: 'mini-btn', type: 'button', disabled: i === disks.length - 1, 'aria-label': 'nach unten', onclick: () => { [disks[i + 1], disks[i]] = [disks[i], disks[i + 1]]; draw(); } }, '↓'),
        h('button', { class: 'mini-btn danger', type: 'button', onclick: () => { disks.splice(i, 1); draw(); } }, 'Entfernen'))));
    const av = available();
    const sel = h('select', { 'aria-label': 'Vorhandenes Image' }, av.map(p => h('option', { value: p }, label(p))));
    const add = h('button', { class: 'mini-btn', type: 'button', disabled: !av.length, onclick: () => { disks.push(sel.value); draw(); } }, 'Hinzufügen');
    const mkNew = h('button', { class: 'mini-btn', type: 'button', onclick: () => openNew() }, 'Neues Image …');
    box.replaceChildren(...(rows.length ? rows : [h('p', { class: 'hint' }, 'Keine Disk.')]), h('div', { class: 'toolbar' }, av.length ? sel : h('span', { class: 'sub' }, 'Keine freien Images'), add, mkNew), newBox);
  }

  const newBox = h('div', { class: 'form-grid', hidden: true });
  function openNew() {
    const name = h('input', { placeholder: 'web02-data', pattern: '[A-Za-z0-9][A-Za-z0-9._\\-]{0,63}' });
    const size = h('input', { value: '20G', pattern: '[0-9]{1,6}[KMGT]?' });
    const fmt = h('select', null, h('option', { value: 'qcow2' }, 'qcow2 (dünn belegt)'), h('option', { value: 'raw' }, 'raw'));
    const store = h('select', null, h('option', { value: '' }, `lokal (${o.vmDir})`), o.datastores.map(s => h('option', { value: s }, `Datastore ${s}`)));
    const err = h('p', { class: 'login-err wide', role: 'alert' });
    const go = h('button', { class: 'mini-btn', type: 'button' }, 'Image anlegen');
    const cancel = h('button', { class: 'mini-btn', type: 'button', onclick: () => { newBox.hidden = true; } }, 'Abbrechen');
    go.addEventListener('click', async () => {
      err.textContent = '';
      if (!name.value.trim()) { err.textContent = 'Bitte einen Namen angeben.'; return; }
      go.disabled = true;
      try {
        await post('/api/vm-disks', { name: name.value.trim(), size: size.value.trim(), format: fmt.value, base: '', store: store.value });
        const base = name.value.trim().replace(new RegExp(`\\.${fmt.value}$`), '');
        const path = `${o.vmDir}${store.value ? '/' + store.value : ''}/${base}.${fmt.value}`;
        created.push(path); disks.push(path); byPath[path] = { name: `${base}.${fmt.value}`, size: 0, path };
        newBox.hidden = true; draw();
      } catch (e) { err.textContent = e.message; } finally { go.disabled = false; }
    });
    newBox.replaceChildren(h('strong', { class: 'wide' }, 'Neues Disk-Image'), field('Name', name), field('Größe', size), field('Format', fmt), field('Speicherort', store), err, h('div', { class: 'toolbar wide' }, go, cancel));
    newBox.hidden = false;
    name.focus();
  }
  draw();
  return { el: h('fieldset', { class: 'wide field' }, h('legend', null, 'Festplatten (die erste ist die Boot-Disk)'), box), value: () => disks };
}

export async function vmSettingsDialog(name, { onDone, onExpert } = {}) {
  const o = await get(`/api/vms/${enc(name)}/options`);
  const c = o.Current || o.current;
  const mem = memoryField(o, c.memory), cpus = cpuField(o, c.cpus), sev = sevField(o, c.sev), owner = ownerField(o, c.owner), disks = disksField(o, c.disks);
  const iso = h('select', null, h('option', { value: '' }, '– kein Medium –'), o.isos.map(i => h('option', { value: i.path, selected: i.path === c.cdrom }, i.name)));
  iso.value = c.cdrom || '';
  const boot = h('select', null, o.boot.map(b => h('option', { value: b.value, selected: b.value === c.bootDevice }, b.label)));
  boot.value = c.bootDevice || '';
  const syncBoot = () => { for (const op of boot.options) op.disabled = op.value === 'cdrom' && !iso.value; };
  iso.addEventListener('change', syncBoot); syncBoot();
  const enabled = h('input', { type: 'checkbox', checked: c.enabled }), local = h('input', { type: 'checkbox', checked: c.local });
  const expert = onExpert ? h('button', { class: 'mini-btn', type: 'button' }, 'Als Text bearbeiten (Experte) …') : null;
  const d = formDialog({
    title: `Einstellungen von ${name}`, meta: 'VM.CONF · IM HINTERGRUND', wide: true, submit: 'Speichern',
    text: `${o.running ? 'Die VM läuft: Änderungen gelten ab dem nächsten Start. ' : ''}Zur Auswahl stehen nur Werte, die es auf diesem Host gibt. maar schreibt vm.conf und prüft sie mit vmd -n. Netzwerkadapter pflegst du separat unter „Netzwerk“.`,
    fields: [mem.el, cpus.el, sev.el, h('div'), disks.el, field('CD-ROM / ISO', iso, `ISOs liegen unter ${o.vmDir}/iso`), field('Boot-Gerät', boot, 'Standard = BIOS entscheidet'), owner.el,
      h('div', { class: 'toolbar wide' }, h('label', { class: 'check' }, enabled, 'Beim Systemstart automatisch starten'), h('label', { class: 'check' }, local, 'Lokale Schnittstelle (NAT über vmd, 100.64.0.0/10)')),
      expert ? h('div', { class: 'wide' }, expert) : null],
    onSubmit: async () => {
      const r = await put(`/api/vms/${enc(name)}/settings`, { memory: mem.value(), cpus: cpus.value(), sev: sev.value(), enabled: enabled.checked, disks: disks.value(), cdrom: iso.value, bootDevice: boot.value, owner: owner.value(), local: local.checked });
      toast(r.message);
      onDone?.();
    },
  });
  expert?.addEventListener('click', () => { d.close(); onExpert(); });
  return d;
}
