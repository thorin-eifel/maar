// VM-Detailseite im Stil des vSphere Clients: Zusammenfassung, Überwachen, Konfigurieren, Snapshots, Sicherung.
import { h, clear, pageHead, panel, table, pill, metric, vmStatePill, loading, toast, toastError, confirm, busy, bytes, pct, dateTime } from '../ui.js';
import { get, post, session, q } from '../api.js';
import { lineChart } from '../chart.js';
import { icon } from '../icons.js';
import { watchJob, onJobDone } from '../jobs.js';
import { deployDialog, toTemplateDialog } from '../deploy.js';
import { backupNowDialog, scheduleText } from './backup.js';
import { nicDialog, firewallDialog } from './vnet.js';
import { vmSettingsDialog } from '../vmsettings.js';
import { act, startFromISODialog, editBlockDialog, removeVMDialog, hardKillDialog } from '../vmactions.js';

const enc = encodeURIComponent;
const TABS = [['summary', 'Zusammenfassung', 'summary'], ['monitor', 'Überwachen', 'chart'], ['config', 'Konfigurieren', 'sliders'], ['snapshots', 'Snapshots', 'camera'], ['backup', 'Sicherung', 'backup']];
const KIND = { aktion: ['info', 'Aktion'], anmeldung: ['off', 'Anmeldung'], auftrag: ['ok', 'Auftrag'], verweigert: ['warn', 'verweigert'] };
const RANGES = [['1h', '1 Std'], ['6h', '6 Std'], ['24h', '24 Std'], ['7d', '7 Tage']];
const MODE = { snapshot: 'Snapshot', offline: 'Offline', pause: 'Pause' };

const eventsTable = (events, empty = 'Keine Ereignisse zu dieser VM.') => table(['Zeit', 'Benutzer', 'Art', 'Ereignis', 'Ergebnis'], events.map(e => h('tr', null,
  h('td', { class: 'mono' }, dateTime(e.time)), h('td', null, e.user), h('td', null, pill(...(KIND[e.kind] || ['off', e.kind]))),
  h('td', { class: 'mono' }, e.text), h('td', null, e.result === 'ok' ? pill('ok', 'ok') : [pill('bad', 'Fehler'), e.error ? h('span', { class: 'sub' }, e.error) : null]))), empty);

const alertsBox = alerts => alerts.length
  ? h('div', { class: 'note bad', role: 'alert' }, alerts.map(a => h('div', null, h('strong', null, a.name), ` – ${a.subject}, seit ${dateTime(a.since)}`)))
  : h('div', { class: 'empty' }, 'Keine aktiven Alarme.');

export default {
  title: 'Virtuelle Maschine',
  async render(main, parts) {
    const name = parts[0];
    let tab = TABS.some(t => t[0] === parts[1]) ? parts[1] : 'summary';
    const isAdmin = session.role === 'admin', canPower = isAdmin || session.role === 'operator';
    const head = h('div'), tabsEl = h('div', { class: 'tabs', role: 'tablist', 'aria-label': 'Bereiche der VM' }), content = h('div', { class: 'content' }, loading());
    main.append(head, tabsEl, content);
    let d = null, cleanups = [], range = '1h';

    const stop = () => { cleanups.forEach(f => { try { f(); } catch { /* egal */ } }); cleanups = []; };

    async function load() {
      d = await get(`/api/vms/${enc(name)}/detail`);
      drawHead();
      draw();
    }

    function drawHead() {
      const vm = d.vm, running = vm.state === 'running', paused = vm.state === 'paused';
      const btn = (label, fn, cls = 'secondary') => { const b = h('button', { class: cls, type: 'button' }, label); b.addEventListener('click', () => busy(b, fn).catch(toastError)); return b; };
      const acts = [];
      if (canPower) {
        if (!running && !paused) acts.push(btn('Starten', async () => { await act(name, 'start'); await load(); }, 'cta'));
        if (running) acts.push(h('a', { class: 'cta', href: `#/console/vm/${enc(name)}` }, 'Konsole'), btn('Herunterfahren', async () => { await act(name, 'stop'); await load(); }), btn('Pausieren', async () => { await act(name, 'pause'); await load(); }));
        if (paused) acts.push(btn('Fortsetzen', async () => { await act(name, 'unpause'); await load(); }, 'cta'));
      }
      if (isAdmin) {
        if (running || paused) acts.push(btn('Hart beenden', async () => { if (await hardKillDialog(name)) { await act(name, 'kill'); await load(); } }, 'secondary danger'));
        if (vm.conf && !running && !paused && d.disks.length) acts.push(
          btn('Klonen', async () => deployDialog({ source: 'vm', from: name, memory: vm.conf.memory || '1G', cpus: vm.conf.cpus || 1 })),
          btn('Als Template', async () => toTemplateDialog(vm)));
        if (vm.conf && !running && !paused) acts.push(btn('Löschen', async () => { if (await removeVMDialog(vm)) location.hash = '#/vms'; }, 'secondary danger'));
      }
      clear(head, pageHead('INVENTAR', name, `${vm.id !== '-' ? `ID ${vm.id} · PID ${vm.pid} · ` : ''}${vm.conf ? `${vm.vcpus || vm.conf.cpus} vCPU · ${vm.conf.memory || vm.maxmem}` : 'nicht in vm.conf definiert'}`, vmStatePill(vm.state), ...acts));
      clear(tabsEl, TABS.map(([k, l, ic]) => {
        const b = h('button', { class: 'tab', type: 'button', role: 'tab', 'aria-selected': String(k === tab), tabindex: k === tab ? '0' : '-1' }, icon(ic), l, k === 'snapshots' && vm.snapshots ? h('span', { class: 'badge' }, vm.snapshots) : null, k === 'summary' && d.alerts.length ? h('span', { class: 'badge bad' }, d.alerts.length) : null);
        b.addEventListener('click', () => { tab = k; history.replaceState(null, '', `#/vm/${enc(name)}/${k}`); drawHead(); draw(); });
        b.addEventListener('keydown', e => {
          const i = TABS.findIndex(t => t[0] === tab), n = e.key === 'ArrowRight' ? i + 1 : e.key === 'ArrowLeft' ? i - 1 : null;
          if (n === null) return;
          tab = TABS[(n + TABS.length) % TABS.length][0]; history.replaceState(null, '', `#/vm/${enc(name)}/${tab}`); drawHead(); draw(); tabsEl.querySelector('[aria-selected=true]').focus();
        });
        return b;
      }));
    }

    function draw() {
      stop();
      content.setAttribute('role', 'tabpanel');
      ({ summary: drawSummary, monitor: drawMonitor, config: drawConfig, snapshots: drawSnapshots, backup: drawBackup })[tab]().catch(toastError);
    }

    // ---- Zusammenfassung ----
    function hardwareList() {
      const c = d.vm.conf || {};
      const disks = d.disks.length ? d.disks.map(k => h('div', { class: 'hw-row' },
        h('span', { class: 'mono' }, k.name), ' ', pill(k.missing ? 'bad' : 'off', k.missing ? 'fehlt' : k.format),
        k.missing ? null : h('span', { class: 'sub' }, `${bytes(k.virtual)} groß · ${bytes(k.size)} belegt${k.store ? ' · Datastore ' + k.store : ''}${k.backing.length ? ' · baut auf ' + (d.template || k.backing[k.backing.length - 1].split('/').pop()) : ''}`))) : '–';
      const nics = [...d.nics.map(n => h('div', { class: 'hw-row' }, h('a', { href: `#/portgroup/${enc(n.switch)}` }, n.switch), ' ', h('span', { class: 'tag mono' }, n.lladdr || 'MAC zufällig'),
        h('span', { class: 'sub mono' }, n.ip ? `${n.ip}${n.fqdn ? ' · ' + n.fqdn : ''}` : 'Adresse dynamisch'))),
        c.local ? h('div', { class: 'hw-row' }, 'lokale Schnittstelle', h('span', { class: 'sub' }, 'NAT, 100.64.0.0/10')) : null];
      return h('dl', { class: 'kv' },
        h('dt', null, 'CPU'), h('dd', null, `${d.vm.vcpus || c.cpus || 1} vCPU`, c.sev ? [' ', h('span', { class: 'tag' }, c.sev.toUpperCase())] : null),
        h('dt', null, 'Speicher'), h('dd', null, d.vm.state === 'running' ? `${d.vm.curmem} von ${d.vm.maxmem}` : (c.memory || d.vm.maxmem || '–')),
        h('dt', null, 'Festplatten'), h('dd', null, disks),
        c.cdrom ? [h('dt', null, 'CD-ROM'), h('dd', { class: 'mono' }, c.cdrom)] : null,
        h('dt', null, 'Netzwerk'), h('dd', null, nics.filter(Boolean).length ? nics : '–'),
        h('dt', null, 'Start'), h('dd', null, c.boot ? `von ${c.boot}` : 'Standard', c.enabled === false ? ' · beim Hostbetrieb nicht automatisch' : ' · automatisch beim Hoststart'),
        h('dt', null, 'Besitzer'), h('dd', { class: 'mono' }, c.owner || d.vm.owner || 'root'));
    }

    async function drawSummary() {
      const vm = d.vm, running = vm.state === 'running';
      const m = running ? await get('/api/metrics' + q({ range: '1h', keys: `vm.${name}.cpu,vm.${name}.mem` })).catch(() => null) : null;
      const last = k => { const p = m?.series?.[k]; return p?.length ? p[p.length - 1].v : null; };
      const cpu = last(`vm.${name}.cpu`), mem = last(`vm.${name}.mem`);
      const cpuMax = (vm.vcpus || 1) * 100;
      clear(content,
        d.alerts.length ? alertsBox(d.alerts) : null,
        h('div', { class: 'metrics' },
          metric('Status', vm.state === 'running' ? 'Läuft' : vm.state === 'paused' ? 'Pausiert' : 'Gestoppt', running ? `${d.viewers} Konsolenzuschauer` : 'ausgeschaltet'),
          metric('CPU', cpu === null ? '–' : pct(cpu), `von ${cpuMax} % (${vm.vcpus || 1} vCPU)`, cpu === null ? undefined : cpu / cpuMax * 100),
          metric('Speicher', mem === null ? '–' : bytes(mem), `zugewiesen ${vm.conf?.memory || vm.maxmem || '–'}`),
          metric('Snapshots', String(d.snapshots), d.backups.length ? `${d.backups.length} Sicherungsjob${d.backups.length === 1 ? '' : 's'}` : 'keine Sicherung geplant')),
        h('div', { class: 'grid-2' },
          panel('VM-Hardware', 'VM.CONF', h('div', { class: 'panel-body' }, hardwareList())),
          h('div', { class: 'content-stack' },
            panel('Netzwerk & Firewall', 'PORTGRUPPEN · PF', h('div', { class: 'panel-body' }, h('dl', { class: 'kv' },
              h('dt', null, 'Portgruppen'), h('dd', null, d.portGroups.length ? d.portGroups.map(g => h('div', null, h('a', { href: `#/portgroup/${enc(g.name)}` }, g.name), h('span', { class: 'sub' }, `${g.vlan ? 'VLAN ' + g.vlan + ' · ' : ''}${g.gateway || 'reines L2'}`))) : '–'),
              h('dt', null, 'Firewall'), h('dd', null, `eingehend ${d.policy.in === 'deny' ? 'gesperrt' : 'offen'} · ausgehend ${d.policy.out === 'deny' ? 'gesperrt' : 'offen'}`, h('span', { class: 'sub' }, `${d.rules.length} Regel${d.rules.length === 1 ? '' : 'n'}`))))),
            panel('Sicherung', 'BACKUP', h('div', { class: 'panel-body' }, d.backups.length ? d.backups.map(j => h('div', { class: 'hw-row' }, h('strong', null, j.name), ' ', j.enabled ? null : pill('off', 'aus'),
              h('span', { class: 'sub' }, `${scheduleText(j.schedule)} · ${MODE[j.mode] || j.mode} · letzter Lauf ${j.lastRun ? dateTime(j.lastRun) : 'noch nie'}${j.lastState ? ' (' + j.lastState + ')' : ''}`))) : h('p', { class: 'hint' }, 'Diese VM ist in keinem Sicherungsjob.')))),
        ),
        panel('Letzte Ereignisse', 'DIESE VM', h('div', { class: 'panel-body flush' }, eventsTable(d.events.slice(0, 8)))));
    }

    // ---- Überwachen ----
    async function drawMonitor() {
      const mk = (title, meta, series, unit) => { const c = lineChart({ series, unit, label: `${name}: ${title}` }); cleanups.push(() => c.destroy()); return { c, el: panel(title, meta, h('div', { class: 'panel-body' }, c.el)), keys: series.map(s => s.key) }; };
      const charts = [mk('CPU', 'EIN KERN = 100 %', [{ key: `vm.${name}.cpu`, label: 'CPU' }], 'pct'), mk('Speicher', 'BELEGT', [{ key: `vm.${name}.mem`, label: 'Speicher' }], 'bytes'),
        mk('Netzwerk', 'EMPFANGEN · GESENDET', [{ key: `vm.${name}.rx`, label: 'empfangen' }, { key: `vm.${name}.tx`, label: 'gesendet' }], 'bps')];
      const seg = h('div', { class: 'segment', role: 'group', 'aria-label': 'Zeitraum' }, RANGES.map(([k, l]) => { const b = h('button', { type: 'button', 'aria-pressed': String(k === range) }, l); b.addEventListener('click', () => { range = k; for (const x of seg.children) x.setAttribute('aria-pressed', String(x === b)); refresh(); }); return b; }));
      async function refresh() {
        const keys = charts.flatMap(c => c.keys).join(',');
        charts.forEach(c => c.c.loading(true));
        try {
          const r = await get('/api/metrics' + q({ range, keys }));
          for (const c of charts) c.c.update(Object.fromEntries(c.keys.map(k => [k, r.series[k] || []])), r.from, r.to);
        } catch (e) { toastError(e); }
      }
      clear(content, h('div', { class: 'toolbar' }, seg), alertsBox(d.alerts), h('div', { class: 'grid-2' }, charts[0].el, charts[1].el), charts[2].el,
        panel('Ereignisse dieser VM', 'AUFGABEN UND EREIGNISSE', h('div', { class: 'panel-body flush' }, eventsTable(d.events))));
      refresh();
      const t = setInterval(refresh, 15000); cleanups.push(() => clearInterval(t));
    }

    // ---- Konfigurieren ----
    async function drawConfig() {
      const vm = d.vm, c = vm.conf;
      if (!c) { clear(content, h('div', { class: 'note warn' }, 'Diese VM läuft, ist aber nicht in /etc/vm.conf definiert – sie lässt sich hier nicht konfigurieren.')); return; }
      const adm = (label, fn) => isAdmin ? h('button', { class: 'mini-btn', type: 'button', onclick: () => fn() }, label) : null;
      const reload = () => load().catch(toastError);
      clear(content,
        panel('Hardware', 'EINSTELLUNGEN', h('div', { class: 'panel-body' }, hardwareList(),
          h('details', { class: 'expert' }, h('summary', null, 'vm.conf anzeigen (Experte)'), h('pre', { class: 'codeblock' }, c.raw.trim()),
            isAdmin ? h('button', { class: 'mini-btn', type: 'button', onclick: () => editBlockDialog(vm, reload) }, 'Als Text bearbeiten …') : null),
          h('p', { class: 'hint' }, 'Laufende VMs übernehmen Änderungen beim nächsten Start.')), adm('Bearbeiten', () => vmSettingsDialog(name, { onDone: reload, onExpert: () => editBlockDialog(vm, reload) }))),
        panel('Festplatten', 'DISK-IMAGES', h('div', { class: 'panel-body flush' }, table(['Image', 'Format', { label: 'Größe', num: true }, { label: 'Belegt', num: true }, 'Speicherort', 'Basis'],
          d.disks.map(k => h('tr', null, h('td', null, h('span', { class: 'name mono' }, k.name), h('span', { class: 'sub mono' }, k.path)), h('td', null, k.missing ? pill('bad', 'fehlt') : pill('off', k.format)),
            h('td', { class: 'num' }, k.missing ? '–' : bytes(k.virtual)), h('td', { class: 'num' }, k.missing ? '–' : bytes(k.size)), h('td', null, k.store ? h('a', { href: `#/datastore/${enc(k.store)}` }, `Datastore ${k.store}`) : 'lokal'),
            h('td', { class: 'mono' }, k.backing.length ? k.backing.map(b => h('div', null, b.split('/').pop())) : '–'))), 'Keine Disk.'))),
        panel('Netzwerk', 'PORTGRUPPEN', h('div', { class: 'panel-body flush' }, table(['Portgruppe', 'MAC', 'Adresse', 'DNS-Name'], d.nics.map(n => h('tr', null, h('td', null, h('a', { class: 'name', href: `#/portgroup/${enc(n.switch)}` }, n.switch)), h('td', { class: 'mono' }, n.lladdr || 'zufällig'),
          h('td', { class: 'mono' }, n.ip || 'dynamisch'), h('td', { class: 'mono' }, n.fqdn || n.hostname || '–'))), 'Kein Switch-Anschluss.')), adm('Bearbeiten', () => nicDialog(name, reload))),
        panel('Firewall', 'PF-ANKER MAAR', h('div', { class: 'panel-body flush' },
          h('div', { class: 'panel-body toolbar' }, pill(d.policy.in === 'deny' ? 'ok' : 'off', `eingehend ${d.policy.in === 'deny' ? 'gesperrt' : 'offen'}`), pill(d.policy.out === 'deny' ? 'ok' : 'off', `ausgehend ${d.policy.out === 'deny' ? 'gesperrt' : 'offen'}`)),
          table(['Richtung', 'Aktion', 'Protokoll', 'Gegenstelle', 'Port', 'Beschreibung'], d.rules.map(r => h('tr', null, h('td', null, r.dir === 'in' ? 'ein' : 'aus'), h('td', null, pill(r.action === 'pass' ? 'ok' : 'bad', r.action === 'pass' ? 'erlauben' : 'sperren')),
            h('td', null, r.proto), h('td', { class: 'mono' }, r.peer), h('td', { class: 'mono' }, r.port || '–'), h('td', null, r.description))), 'Keine eigenen Regeln.')), adm('Bearbeiten', () => firewallDialog(name, reload))));
    }

    // ---- Snapshots ----
    async function drawSnapshots() {
      const box = h('div', { class: 'content-stack' });
      clear(content, box);
      const off = await (await import('./snapshots.js')).default.render(box, name, true);
      if (off) cleanups.push(off);
    }

    // ---- Sicherung ----
    async function drawBackup() {
      const now = isAdmin ? h('button', { class: 'cta', type: 'button' }, 'Jetzt sichern') : null;
      now?.addEventListener('click', () => backupNowDialog([name]).catch(toastError));
      clear(content,
        panel('Sicherungsjobs mit dieser VM', 'BACKUP', h('div', { class: 'panel-body flush' }, table(['Job', 'Zeitplan', 'Modus', 'Ziel', { label: 'Aufbewahrung', num: true }, 'Letzter Lauf'], d.backups.map(j => h('tr', null,
          h('td', null, h('span', { class: 'name' }, j.name), j.enabled ? null : h('span', { class: 'sub' }, 'deaktiviert')), h('td', null, scheduleText(j.schedule)), h('td', null, MODE[j.mode] || j.mode), h('td', null, j.target),
          h('td', { class: 'num' }, j.keep || 'alle'), h('td', null, j.lastRun ? dateTime(j.lastRun) : 'noch nie', j.lastState ? h('span', { class: 'sub' }, j.lastState) : null))), 'Diese VM ist in keinem Sicherungsjob. Lege unter „Backup“ einen an.')), now),
        h('div', { class: 'note' }, 'Vorhandene Sicherungen und die Wiederherstellung (als neue VM oder über diese) findest du unter ', h('a', { href: '#/backup' }, 'Speicher → Backup'), '.'));
    }

    await load();
    const off = onJobDone(() => setTimeout(() => { if (!document.querySelector('dialog')) load().catch(() => {}); }, 800));
    const t = setInterval(() => { if (!document.querySelector('dialog') && tab !== 'snapshots' && tab !== 'monitor') load().catch(() => {}); }, 20000);
    return () => { stop(); off(); clearInterval(t); };
  },
};
