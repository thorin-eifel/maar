import { h, clear, pageHead, panel, bytes, dateTime, loading, toast, toastError, confirm, formDialog, field, busy, pill } from '../ui.js';
import { get, post, put, del } from '../api.js';
import { watchJob, onJobDone } from '../jobs.js';

export default {
  title: 'Snapshots',
  async render(main, vm, embedded = false) {
    const body = h('div', { class: 'content' }, loading());
    const newBtn = h('button', { class: 'cta', type: 'button' }, 'Snapshot anlegen');
    const back = h('a', { class: 'secondary', href: '#/vms' }, 'Zu den VMs');
    if (embedded) main.append(h('div', { class: 'toolbar' }, h('p', { class: 'hint' }, 'Offline-Snapshots als qcow2-Kette. Zurücksetzen verzweigt den Baum; Zusammenführen hängt Nachfolger auf den Vorgänger um.'), newBtn), body);
    else main.append(pageHead('INVENTAR', `Snapshots · ${vm}`, 'Offline-Snapshots als qcow2-Kette. Zurücksetzen verzweigt den Baum; Zusammenführen hängt Nachfolger auf den Vorgänger um.', back, newBtn), body);
    let t = null;
    const base = `/api/vms/${encodeURIComponent(vm)}/snapshots`;

    async function load() {
      t = await get(base);
      newBtn.disabled = !t.supported || t.running;
      draw();
    }

    function create() {
      const name = h('input', { required: true, maxlength: '80', placeholder: 'z. B. vor Update auf 8.0', autocomplete: 'off' });
      const desc = h('input', { maxlength: '500' });
      formDialog({
        title: 'Snapshot anlegen', meta: 'VMCTL CREATE -B', submit: 'Anlegen',
        text: 'Friert den aktuellen Zustand aller Disks ein. Dauert Sekunden, unabhängig von der Diskgröße.',
        fields: [h('div', { class: 'wide' }, field('Name', name)), h('div', { class: 'wide' }, field('Beschreibung', desc))],
        onSubmit: async () => { const r = await post(base, { name: name.value, description: desc.value }); toast(r.message); await load(); },
      });
    }

    function revert(s) {
      const save = h('input', { type: 'checkbox', checked: !t.snapshots.some(x => x.current && x.id === s.id) });
      const saveName = h('input', { value: `vor Zurücksetzen auf „${s.name}“`, maxlength: '80' });
      formDialog({
        title: `Auf „${s.name}“ zurücksetzen?`, meta: 'REVERT', submit: 'Zurücksetzen',
        text: 'Der aktuelle Zustand der Disks wird verworfen. Sichere ihn vorher als Snapshot, wenn du ihn noch brauchst.',
        fields: [h('label', { class: 'check wide' }, save, 'Aktuellen Zustand vorher als Snapshot sichern'), h('div', { class: 'wide' }, field('Name der Sicherung', saveName))],
        onSubmit: async () => {
          const r = await post(`${base}/${encodeURIComponent(s.id)}/revert`, { saveAs: save.checked ? saveName.value : '' });
          toast(r.message);
          await load();
        },
      });
    }

    async function remove(s) {
      const merge = s.children.length > 0 || s.current;
      if (!await confirm({
        title: merge ? `„${s.name}“ zusammenführen?` : `„${s.name}“ löschen?`,
        text: merge
          ? 'Die Nachfolger übernehmen den Inhalt dieses Snapshots (qemu-img rebase), danach wird er entfernt. Das kopiert Daten und kann dauern.'
          : 'Dieser Snapshot hat keine Nachfolger und wird direkt gelöscht.',
        ok: merge ? 'Zusammenführen' : 'Löschen', danger: !merge,
      })) return;
      const r = await del(`${base}/${encodeURIComponent(s.id)}`);
      watchJob(r.job);
    }

    function rename(s) {
      const name = h('input', { required: true, maxlength: '80', value: s.name });
      const desc = h('input', { maxlength: '500', value: s.description });
      formDialog({
        title: 'Snapshot umbenennen', meta: 'METADATEN', submit: 'Speichern',
        fields: [h('div', { class: 'wide' }, field('Name', name)), h('div', { class: 'wide' }, field('Beschreibung', desc))],
        onSubmit: async () => { await put(`${base}/${encodeURIComponent(s.id)}`, { name: name.value, description: desc.value }); await load(); },
      });
    }

    function node(s, byID) {
      const locked = t.running || !t.supported;
      const btn = (label, fn, cls = 'mini-btn') => { const b = h('button', { class: cls, type: 'button', disabled: locked }, label); b.addEventListener('click', () => busy(b, fn).catch(toastError)); return b; };
      const needsMerge = s.children.length > 0 || s.current;
      const kids = s.children.map(id => byID[id]).filter(Boolean).sort((a, b) => a.created - b.created);
      return h('li', { class: 'snap-node' },
        h('div', { class: 'snap-card' + (s.current ? ' is-current' : '') },
          h('div', { class: 'snap-main' },
            h('span', { class: 'snap-name' }, s.name),
            h('span', { class: 'sub' }, `${dateTime(s.created)} · ${bytes(s.size)}${s.description ? ' · ' + s.description : ''}`)),
          h('div', { class: 'toolbar' },
            btn('Zurücksetzen', async () => revert(s)),
            btn('Umbenennen', async () => rename(s)),
            btn(needsMerge ? 'Zusammenführen' : 'Löschen', () => remove(s), needsMerge ? 'mini-btn' : 'mini-btn danger'))),
        kids.length || s.current ? h('ul', { class: 'snap-tree' },
          kids.map(k => node(k, byID)),
          s.current ? nowNode() : null) : null);
    }

    function nowNode() {
      return h('li', { class: 'snap-node' },
        h('div', { class: 'snap-card snap-now', 'aria-current': 'true' },
          h('div', { class: 'snap-main' }, h('span', { class: 'snap-name' }, 'Aktueller Zustand'), h('span', { class: 'sub' }, t.running ? 'VM läuft – Disk wird beschrieben' : 'VM gestoppt')),
          t.running ? pill('ok', 'läuft') : pill('off', 'gestoppt')));
    }

    function draw() {
      const byID = Object.fromEntries(t.snapshots.map(s => [s.id, s]));
      const roots = t.snapshots.filter(s => !s.parent);
      const consolidate = h('button', { class: 'secondary', type: 'button', disabled: !t.snapshots.length || t.running || !t.supported }, 'Alle konsolidieren');
      consolidate.addEventListener('click', () => busy(consolidate, async () => {
        if (!await confirm({
          title: 'Alle Snapshots konsolidieren?', danger: true, ok: 'Konsolidieren', typeToConfirm: vm,
          text: t.qemuImg
            ? 'Der aktuelle Zustand wird zusammengeführt, alle Snapshots – auch andere Zweige – werden gelöscht. Ein Template bleibt als Basis erhalten.'
            : 'Ohne qemu-img wird die Disk vollständig kopiert und ist danach eigenständig (auch ohne Template-Basis). Alle Snapshots werden gelöscht.',
        })) return;
        const r = await post(`${base}/consolidate`);
        watchJob(r.job);
      }).catch(toastError));

      clear(body,
        !t.supported ? h('div', { class: 'note bad' }, t.reason) : null,
        t.running ? h('div', { class: 'note warn' }, 'Die VM läuft. Snapshots sind offline: VM herunterfahren, dann sind alle Aktionen verfügbar.') : null,
        !t.qemuImg ? h('div', { class: 'note' }, 'Zum Zusammenführen von Snapshots mit Nachfolgern wird qemu-img benötigt. ',
          h('button', { class: 'mini-btn', type: 'button', onclick: async () => { const r = await post('/api/packages/install', { names: ['qemu'] }); watchJob(r.job); } }, 'Paket qemu installieren'),
          ' Anlegen, Zurücksetzen und Löschen von Blättern funktionieren ohne.') : null,
        panel('Snapshot-Baum', `${t.snapshots.length} SNAPSHOTS`, h('div', { class: 'panel-body' },
          h('ul', { class: 'snap-tree root', 'aria-label': `Snapshots von ${vm}` },
            h('li', { class: 'snap-node' },
              h('div', { class: 'snap-card snap-base' },
                h('div', { class: 'snap-main' }, h('span', { class: 'snap-name' }, t.base ? 'Basisimage' : 'Ursprung'), h('span', { class: 'sub mono' }, t.base || 'eigenständige Disk'))),
              h('ul', { class: 'snap-tree' },
                roots.sort((a, b) => a.created - b.created).map(r => node(r, byID)),
                !t.current ? nowNode() : null)))),
          consolidate));
    }

    newBtn.addEventListener('click', () => t && create());
    await load();
    return onJobDone(() => load().catch(() => {}));
  },
};
