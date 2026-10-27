import { h, clear, pageHead, panel, table, bytes, dateTime, loading, toast, toastError, confirm, formDialog, field, busy, pill, metric } from '../ui.js';
import { get, post, del } from '../api.js';
import { watchJob, onJobDone } from '../jobs.js';

const MODES = {
  snapshot: ['Snapshot', 'Kurzer Neustart, Kopie läuft im Hintergrund (empfohlen)'],
  offline: ['Offline', 'VM bleibt während der Kopie aus'],
  pause: ['Pause', 'Ohne Neustart, VM steht während der Kopie – absturzkonsistent'],
};
const DAYS = ['Sonntag', 'Montag', 'Dienstag', 'Mittwoch', 'Donnerstag', 'Freitag', 'Samstag'];
const TYPES = { local: 'Lokal', nfs: 'NFS', sftp: 'SFTP' };

export function scheduleText(s) {
  switch (s.kind) {
    case 'hourly': return `stündlich um :${s.time.slice(3)}`;
    case 'daily': return `täglich ${s.time} Uhr`;
    case 'weekly': return `${DAYS[s.weekday]}s ${s.time} Uhr`;
  }
  return 'nur manuell';
}

const stateKind = { running: ['warn', 'läuft'], done: ['ok', 'erfolgreich'], failed: ['bad', 'fehlgeschlagen'], canceled: ['off', 'abgebrochen'] };
const statePill = s => { const [k, t] = stateKind[s] || ['off', s]; return pill(k, t); };

// backupNowDialog wird auch von der VM-Seite genutzt.
export async function backupNowDialog(vms) {
  const d = await get('/api/backup');
  if (!d.targets.length) { toastError(new Error('Zuerst unter „Backup“ ein Sicherungsziel anlegen.')); return; }
  const target = h('select', null, d.targets.map(t => h('option', { value: t.id }, `${t.name} – ${t.location}`)));
  const mode = h('select', null, Object.entries(MODES).map(([k, [l, x]]) => h('option', { value: k }, `${l} – ${x}`)));
  const keep = h('input', { type: 'number', min: '0', max: '365', value: '0' });
  formDialog({
    title: `${vms.join(', ')} jetzt sichern`, meta: 'BACKUP', submit: 'Sicherung starten', wide: true,
    fields: [h('div', { class: 'wide' }, field('Ziel', target)), h('div', { class: 'wide' }, field('Modus bei laufender VM', mode)),
      field('Aufbewahrung', keep, '0 = nichts automatisch löschen')],
    onSubmit: async () => { const r = await post('/api/backup/now', { vms, target: target.value, mode: mode.value, keep: Number(keep.value) }); watchJob(r.job); },
  });
}

export default {
  title: 'Backup',
  async render(main) {
    const body = h('div', { class: 'content' }, loading());
    const newJob = h('button', { class: 'cta', type: 'button' }, 'Job anlegen');
    main.append(pageHead('SPEICHER', 'Backup', 'Zeitgesteuerte Sicherung der VMs auf lokale Platten, NFS oder SFTP – mit Aufbewahrung und Wiederherstellung per Klick. Sicherungen sind eigenständige qcow2-Images.', newJob), body);
    let d = null, vmNames = [], restoreTarget = null;
    const restoreBox = h('div', { class: 'panel-body flush' });

    async function load() {
      [d, vmNames] = await Promise.all([get('/api/backup'), get('/api/vms').then(r => r.overview.vms.filter(v => v.conf).map(v => v.name))]);
      if (!restoreTarget || !d.targets.some(t => t.id === restoreTarget)) restoreTarget = d.targets[0]?.id || null;
      draw();
      loadBackups();
    }

    function targetDialog(t) {
      const name = h('input', { required: true, value: t?.name || '', maxlength: '60' });
      const type = h('select', { disabled: !!t }, Object.entries(TYPES).map(([k, l]) => h('option', { value: k, selected: t?.type === k }, l)));
      const path = h('input', { required: true, value: t?.path || '', class: 'mono' });
      const host = h('input', { value: t?.host || '', autocomplete: 'off' });
      const user = h('input', { value: t?.user || '', autocomplete: 'off' });
      const port = h('input', { type: 'number', min: '1', max: '65535', value: t?.port || 22 });
      const keyBox = h('div', { class: 'wide' });
      const pathF = field('Verzeichnis', path), hostF = field('Host', host), userF = field('Benutzer', user), portF = field('Port', port);
      const sync = async () => {
        const k = type.value;
        pathF.firstChild.textContent = k === 'nfs' ? 'NFS-Freigabe' : k === 'sftp' ? 'Entferntes Verzeichnis' : 'Verzeichnis';
        path.placeholder = k === 'nfs' ? 'nas.example.lan:/volume1/vm-backup' : k === 'sftp' ? '/srv/backup/vm' : '/backup/vms';
        hostF.hidden = userF.hidden = portF.hidden = keyBox.hidden = k !== 'sftp';
        host.required = user.required = k === 'sftp';
        if (k === 'sftp' && !keyBox.childElementCount) {
          try {
            const r = await get('/api/backup/pubkey');
            clear(keyBox, h('div', { class: 'note' }, 'Diesen öffentlichen Schlüssel auf dem Ziel in ~/.ssh/authorized_keys eintragen:', h('pre', { class: 'codeblock wrap' }, r.key)));
          } catch (e) { clear(keyBox, h('div', { class: 'note bad' }, e.message)); }
        }
      };
      type.addEventListener('change', sync);
      sync();
      formDialog({
        title: t ? `Ziel „${t.name}“ bearbeiten` : 'Sicherungsziel anlegen', meta: 'ZIEL', wide: true, submit: 'Speichern',
        text: 'Lokale Ziele sollten auf einer eigenen Platte liegen. NFS wird für jeden Lauf eingebunden und danach wieder gelöst. SFTP nutzt einen eigenen SSH-Schlüssel von maar.',
        fields: [field('Name', name), field('Art', type), h('div', { class: 'wide' }, pathF), hostF, userF, portF, keyBox],
        onSubmit: async () => {
          const r = await post('/api/backup/targets', { id: t?.id || '', name: name.value, type: type.value, path: path.value.trim(), host: host.value.trim(), user: user.value.trim(), port: Number(port.value) });
          toast(r.message);
          await load();
        },
      });
    }

    function jobDialog(j) {
      if (!d.targets.length) { toastError(new Error('Zuerst ein Sicherungsziel anlegen.')); return; }
      const name = h('input', { required: true, value: j?.name || '', maxlength: '60', placeholder: 'z. B. Nächtlich' });
      const target = h('select', null, d.targets.map(t => h('option', { value: t.id, selected: j?.target === t.id }, t.name)));
      const mode = h('select', null, Object.entries(MODES).map(([k, [l, x]]) => h('option', { value: k, selected: (j?.mode || 'snapshot') === k }, `${l} – ${x}`)));
      const allVMs = h('input', { type: 'checkbox', checked: !j || !j.vms.length });
      const vmBoxes = vmNames.map(n => ({ n, cb: h('input', { type: 'checkbox', checked: j?.vms.includes(n) }) }));
      const vmList = h('div', { class: 'toolbar' }, vmBoxes.map(({ n, cb }) => h('label', { class: 'check' }, cb, n)));
      const kind = h('select', null, [['daily', 'Täglich'], ['weekly', 'Wöchentlich'], ['hourly', 'Stündlich'], ['manual', 'Nur manuell']].map(([k, l]) => h('option', { value: k, selected: (j?.schedule.kind || 'daily') === k }, l)));
      const time = h('input', { type: 'time', value: j?.schedule.time || '02:15', required: true });
      const day = h('select', null, DAYS.map((l, i) => h('option', { value: i, selected: (j?.schedule.weekday ?? 0) === i }, l)));
      const keep = h('input', { type: 'number', min: '1', max: '365', value: j?.keep || 7, required: true });
      const enabled = h('input', { type: 'checkbox', checked: j ? j.enabled : true });
      const timeF = field('Uhrzeit', time), dayF = field('Wochentag', day);
      const sync = () => { vmList.hidden = allVMs.checked; dayF.hidden = kind.value !== 'weekly'; timeF.hidden = kind.value === 'manual'; };
      allVMs.addEventListener('change', sync);
      kind.addEventListener('change', sync);
      sync();
      formDialog({
        title: j ? `Job „${j.name}“ bearbeiten` : 'Sicherungsjob anlegen', meta: 'ZEITPLAN · AUFBEWAHRUNG', wide: true, submit: 'Speichern',
        fields: [field('Name', name), field('Ziel', target), h('div', { class: 'wide' }, field('Modus bei laufender VM', mode)),
          h('fieldset', { class: 'wide field' }, h('legend', null, 'VMs'), h('label', { class: 'check' }, allVMs, 'Alle VMs (auch künftig angelegte)'), vmList),
          field('Zeitplan', kind), timeF, dayF, field('Aufbewahrung', keep, 'Anzahl Sicherungen je VM auf dem Ziel'),
          h('label', { class: 'check wide' }, enabled, 'Zeitplan aktiv')],
        onSubmit: async () => {
          const vms = allVMs.checked ? [] : vmBoxes.filter(x => x.cb.checked).map(x => x.n);
          if (!allVMs.checked && !vms.length) throw new Error('Mindestens eine VM wählen.');
          const r = await post('/api/backup/jobs', {
            id: j?.id || '', name: name.value, target: target.value, mode: mode.value, vms, keep: Number(keep.value), enabled: enabled.checked,
            schedule: { kind: kind.value, time: kind.value === 'manual' ? '' : time.value, weekday: Number(day.value) },
          });
          toast(r.message);
          await load();
        },
      });
    }

    function restoreDialog(m) {
      const mode = h('select', null, h('option', { value: 'new' }, 'Als neue VM'), h('option', { value: 'overwrite' }, `${m.vm} überschreiben`));
      const name = h('input', { value: `${m.vm}-restore`, pattern: '[A-Za-z0-9][A-Za-z0-9._\\-]{0,63}' });
      const start = h('input', { type: 'checkbox' });
      const noNet = h('input', { type: 'checkbox', checked: true });
      const noNetF = h('label', { class: 'check wide' }, noNet, 'Ohne Netzwerk anlegen – der Gast hat sonst dieselbe IP wie das Original');
      const hint = h('div', { class: 'note wide' });
      const nameF = field('Name der neuen VM', name);
      const sync = () => {
        nameF.hidden = noNetF.hidden = mode.value !== 'new';
        name.required = mode.value === 'new';
        hint.className = 'note wide' + (mode.value === 'overwrite' ? ' warn' : '');
        hint.textContent = mode.value === 'new'
          ? 'Legt eine neue, beim Systemstart deaktivierte VM mit den gesicherten Einstellungen an. Feste MAC-Adressen werden nicht übernommen.'
          : `${m.vm} muss gestoppt sein. Der aktuelle Zustand wird vorher als Snapshot gesichert und bleibt unter „Snapshots“ erreichbar.`;
      };
      mode.addEventListener('change', sync);
      sync();
      formDialog({
        title: `${m.vm} vom ${dateTime(m.created)} wiederherstellen`, meta: `${m.id} · ${bytes(m.disks.reduce((a, x) => a + x.size, 0))}`, wide: true, submit: 'Wiederherstellen',
        fields: [field('Art', mode), nameF, noNetF, h('label', { class: 'check wide' }, start, 'Danach starten'), hint],
        onSubmit: async () => {
          const r = await post(`/api/backup/targets/${restoreTarget}/restore`, { vm: m.vm, backup: m.id, mode: mode.value, name: name.value.trim(), startNow: start.checked, noNetwork: mode.value === 'new' && noNet.checked });
          watchJob(r.job);
        },
      });
    }

    async function loadBackups() {
      if (!restoreTarget) { clear(restoreBox, h('div', { class: 'empty' }, 'Noch kein Sicherungsziel angelegt.')); return; }
      clear(restoreBox, loading('Lese Sicherungen …'));
      try {
        const list = await get(`/api/backup/targets/${restoreTarget}/backups`);
        clear(restoreBox, table(['VM', 'Zeitpunkt', 'Modus', { label: 'Größe', num: true }, 'Disks', ''], list.map(m => {
          const rm = h('button', { class: 'mini-btn danger', type: 'button' }, 'Löschen');
          rm.addEventListener('click', () => busy(rm, async () => {
            if (!await confirm({ title: `Sicherung ${m.vm}/${m.id} löschen?`, ok: 'Löschen', danger: true })) return;
            const r = await del(`/api/backup/targets/${restoreTarget}/backups/${encodeURIComponent(m.vm)}/${encodeURIComponent(m.id)}`);
            toast(r.message);
            await loadBackups();
          }).catch(toastError));
          return h('tr', null,
            h('td', { class: 'name' }, m.vm),
            h('td', null, dateTime(m.created), h('span', { class: 'sub mono' }, m.id)),
            h('td', null, { snapshot: 'Snapshot', offline: 'Offline', pause: 'Pause', stopped: 'VM gestoppt' }[m.mode] || m.mode),
            h('td', { class: 'num' }, bytes(m.disks.reduce((a, x) => a + x.size, 0))),
            h('td', { class: 'num' }, m.disks.length),
            h('td', { class: 'actions' }, h('button', { class: 'mini-btn', type: 'button', onclick: () => restoreDialog(m) }, 'Wiederherstellen'), rm));
        }), 'Auf diesem Ziel liegen noch keine Sicherungen.'));
      } catch (e) {
        clear(restoreBox, h('div', { class: 'panel-body' }, h('div', { class: 'note bad' }, e.message)));
      }
    }

    function draw() {
      const last = d.history.filter(r => r.state !== 'running').at(-1);
      const nexts = d.jobs.filter(j => j.nextRun).map(j => j.nextRun).sort();
      const jobRows = d.jobs.map(j => {
        const run = h('button', { class: 'mini-btn', type: 'button', disabled: !!j.running }, j.running ? 'Läuft …' : 'Jetzt ausführen');
        run.addEventListener('click', () => busy(run, async () => { const r = await post(`/api/backup/jobs/${j.id}/run`); watchJob(r.job); await load(); }).catch(toastError));
        const rm = h('button', { class: 'mini-btn danger', type: 'button' }, 'Löschen');
        rm.addEventListener('click', () => busy(rm, async () => {
          if (!await confirm({ title: `Job „${j.name}“ löschen?`, text: 'Vorhandene Sicherungen bleiben auf dem Ziel erhalten.', ok: 'Löschen', danger: true })) return;
          await del(`/api/backup/jobs/${j.id}`);
          await load();
        }).catch(toastError));
        const lastRun = d.history.filter(r => r.job === j.id).at(-1);
        const tgt = d.targets.find(t => t.id === j.target);
        return h('tr', null,
          h('td', null, h('span', { class: 'name' }, j.name), h('span', { class: 'sub' }, `${MODES[j.mode][0]} · behält ${j.keep}`)),
          h('td', null, j.vms.length ? j.vms.map(v => h('span', { class: 'tag' }, v)) : h('span', { class: 'tag' }, 'alle VMs')),
          h('td', null, tgt ? tgt.name : j.target),
          h('td', null, j.enabled ? scheduleText(j.schedule) : h('span', { class: 'sub' }, 'pausiert'), j.nextRun ? h('span', { class: 'sub' }, 'nächster: ' + dateTime(j.nextRun)) : null),
          h('td', null, lastRun ? [statePill(lastRun.state), h('span', { class: 'sub' }, dateTime(lastRun.started))] : h('span', { class: 'sub' }, 'noch nie')),
          h('td', { class: 'actions' }, run, h('button', { class: 'mini-btn', type: 'button', onclick: () => jobDialog(j) }, 'Bearbeiten'), rm));
      });
      const targetRows = d.targets.map(t => {
        const test = h('button', { class: 'mini-btn', type: 'button' }, 'Testen');
        test.addEventListener('click', () => busy(test, async () => { const r = await post(`/api/backup/targets/${t.id}/test`); toast(r.message); }).catch(toastError));
        const rm = h('button', { class: 'mini-btn danger', type: 'button' }, 'Entfernen');
        rm.addEventListener('click', () => busy(rm, async () => {
          if (!await confirm({ title: `Ziel „${t.name}“ entfernen?`, text: 'Die Sicherungen auf dem Ziel werden nicht gelöscht.', ok: 'Entfernen', danger: true })) return;
          const r = await del(`/api/backup/targets/${t.id}`);
          toast(r.message);
          await load();
        }).catch(toastError));
        return h('tr', null,
          h('td', { class: 'name' }, t.name),
          h('td', null, h('span', { class: 'tag' }, TYPES[t.type])),
          h('td', { class: 'mono' }, t.location),
          h('td', { class: 'actions' }, test, h('button', { class: 'mini-btn', type: 'button', onclick: () => targetDialog(t) }, 'Bearbeiten'), rm));
      });
      const targetSel = h('select', { class: 'input', 'aria-label': 'Ziel für die Wiederherstellung' }, d.targets.map(t => h('option', { value: t.id, selected: t.id === restoreTarget }, t.name)));
      targetSel.style.width = 'auto';
      targetSel.addEventListener('change', () => { restoreTarget = targetSel.value; loadBackups(); });
      const hist = [...d.history].reverse().slice(0, 15).map(r => h('tr', null,
        h('td', null, dateTime(r.started)), h('td', null, r.name), h('td', null, r.trigger), h('td', null, statePill(r.state)),
        h('td', null, r.ended ? `${Math.max(1, Math.round((r.ended - r.started) / 60))} Min` : '–'), h('td', { class: 'sub' }, r.error || '')));

      clear(body,
        h('div', { class: 'metrics' },
          metric('Letzte Sicherung', last ? dateTime(last.started) : '–', last ? (last.state === 'done' ? 'erfolgreich' : 'fehlgeschlagen: ' + (last.error || last.state)) : 'noch keine'),
          metric('Nächste Sicherung', nexts.length ? dateTime(nexts[0]) : '–', nexts.length ? `${nexts.length} Job(s) geplant` : 'kein Zeitplan aktiv'),
          metric('Jobs', String(d.jobs.length), `${d.jobs.filter(j => j.enabled).length} aktiv`),
          metric('Ziele', String(d.targets.length), d.targets.map(t => TYPES[t.type]).join(' · ') || 'keins')),
        panel('Sicherungsjobs', 'ZEITPLAN', h('div', { class: 'panel-body flush' }, table(['Job', 'VMs', 'Ziel', 'Zeitplan', 'Letzter Lauf', ''], jobRows, 'Noch keine Jobs.'))),
        panel('Wiederherstellen', 'SICHERUNGEN', restoreBox, targetSel, h('button', { class: 'mini-btn', type: 'button', onclick: loadBackups }, 'Neu lesen')),
        h('div', { class: 'grid-2' },
          panel('Sicherungsziele', 'LOKAL · NFS · SFTP', h('div', { class: 'panel-body flush' }, table(['Name', 'Art', 'Ort', ''], targetRows, 'Noch keine Ziele.')),
            h('button', { class: 'mini-btn', type: 'button', onclick: () => targetDialog(null) }, 'Ziel anlegen')),
          panel('Verlauf', 'LETZTE LÄUFE', h('div', { class: 'panel-body flush' }, table(['Start', 'Lauf', 'Auslöser', 'Status', 'Dauer', ''], hist, 'Noch keine Läufe.')))),
        h('div', { class: 'note' }, h('strong', null, 'So funktioniert der Snapshot-Modus: '),
          'Die VM wird sauber heruntergefahren, ihre Disks werden als Snapshot eingefroren und sie startet sofort wieder. Gesichert wird aus dem eingefrorenen Stand, während die VM schon läuft. Beim nächsten Lauf wird der vorige Backup-Snapshot im selben kurzen Stopp zusammengeführt (braucht qemu-img, sonst wird offline gesichert).'));
    }

    newJob.addEventListener('click', () => d && jobDialog(null));
    await load();
    // Der Verlauf wird kurz nach Jobende geschrieben – daher leicht verzögert neu laden.
    const off = onJobDone(() => setTimeout(() => load().catch(() => {}), 2500));
    const t = setInterval(() => { if (!document.querySelector('dialog[open]')) get('/api/backup').then(x => { d = x; draw(); }).catch(() => {}); }, 15000);
    return () => { off(); clearInterval(t); };
  },
};
