// Gemeinsame VM-Dialoge für die VM-Liste und die Detailseite.
import { h, toast, confirm, formDialog, field } from './ui.js';
import { put, del, post } from './api.js';

const enc = encodeURIComponent;

export function act(name, action, opt) {
  return post(`/api/vms/${enc(name)}/${action}`, opt).then(r => { toast(r.message); return r; });
}

export function startFromISODialog(name, isos, onDone) {
  const sel = h('select', { required: true }, isos.map(i => h('option', { value: i.path }, i.name)));
  formDialog({
    title: `${name} von ISO starten`, meta: 'EINMALIG', text: 'Startet die VM einmalig mit dem gewählten Medium als CD-ROM und bootet davon (vmctl start -r … -B cdrom).',
    fields: [h('div', { class: 'wide' }, field('Installationsmedium', sel))], submit: 'Starten',
    onSubmit: async () => { await act(name, 'start', { cdrom: sel.value }); onDone?.(); },
  });
}

export function editBlockDialog(vm, onDone) {
  const ta = h('textarea', { rows: 14, spellcheck: 'false', value: vm.conf.raw.trim() });
  formDialog({
    title: `Konfiguration von ${vm.name}`, meta: '/ETC/VM.CONF', wide: true,
    text: 'Block aus vm.conf(5). Vor dem Speichern prüft vmd -n die Syntax; danach folgt vmctl reload. Laufende VMs übernehmen Änderungen beim nächsten Start.',
    fields: [h('div', { class: 'wide' }, field('vm-Block', ta))],
    onSubmit: async () => { const r = await put(`/api/vms/${enc(vm.name)}/config`, { block: ta.value }); toast(r.message); onDone?.(); },
  });
}

// Liefert true, wenn die VM gelöscht wurde.
export async function removeVMDialog(vm) {
  const ok = await confirm({ title: `${vm.name} löschen?`, text: `Der Block wird aus vm.conf entfernt. ${vm.conf.disks.length ? 'Die Disk-Images bleiben erhalten, außer du löschst sie unten mit.' : ''}`, ok: 'VM löschen', danger: true, typeToConfirm: vm.name });
  if (!ok) return false;
  let withDisks = false;
  if (vm.conf.disks.length) {
    withDisks = await confirm({ title: 'Disk-Images ebenfalls löschen?', text: vm.conf.disks.join('\n'), ok: 'Ja, Images löschen', danger: true });
  }
  const r = await del(`/api/vms/${enc(vm.name)}${withDisks ? '?disks=1' : ''}`);
  toast(r.message);
  return true;
}

export async function hardKillDialog(name) {
  return confirm({ title: `${name} hart beenden?`, text: 'Entspricht dem Ziehen des Netzsteckers. Nicht gespeicherte Daten in der VM gehen verloren.', ok: 'Hart beenden', danger: true });
}
