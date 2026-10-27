// System-Einstellungen: Formulare für ntpd.conf, resolv.conf, hosts, sshd_config, doas.conf und sysctl.conf.
// Die Auswahl kommt vom Host; die Dateien schreibt maar im Hintergrund (mit Prüfung und Sicherung).
import { h, clear, pageHead, panel, table, pill, loading, toast, toastError, dialog, field, busy } from '../ui.js';
import { get, put } from '../api.js';
import { icon } from '../icons.js';

const TABS = [['ntp', 'Zeit (ntpd)', 'clock'], ['dns', 'Namensauflösung', 'globe'], ['hosts', 'Hosts-Datei', 'link'], ['ssh', 'SSH-Zugang', 'key'], ['doas', 'Rechte (doas)', 'lock'], ['sysctl', 'Kernel (sysctl)', 'cpu']];
const num = (v, d = 0) => Number.isFinite(Number(v)) && v !== '' ? Number(v) : d;

// Eine Liste bearbeitbarer Zeilen mit „Hinzufügen“.
export function rows(items, mk, { add = 'Hinzufügen', max = 20, fresh = () => ({}) } = {}) {
  const box = h('div', { class: 'cf-rows' });
  const addBtn = h('button', { class: 'mini-btn', type: 'button' }, add);
  const draw = () => {
    box.replaceChildren(...items.map((it, i) => {
      const rm = h('button', { class: 'mini-btn danger', type: 'button', 'aria-label': 'Entfernen', onclick: () => { items.splice(i, 1); draw(); } }, 'Entfernen');
      const up = h('button', { class: 'mini-btn', type: 'button', disabled: i === 0, 'aria-label': 'nach oben', onclick: () => { [items[i - 1], items[i]] = [items[i], items[i - 1]]; draw(); } }, '↑');
      return h('div', { class: 'cf-row' }, mk(it, i, draw), h('div', { class: 'toolbar' }, up, rm));
    }), h('div', null, addBtn));
    addBtn.disabled = items.length >= max;
  };
  addBtn.addEventListener('click', () => { items.push(fresh()); draw(); });
  draw();
  return box;
}

export const sel = (opts, value, on) => { const s = h('select', null, opts.map(([v, l]) => h('option', { value: v }, l))); s.value = String(value ?? ''); s.addEventListener('change', () => on(s.value)); return s; };
export const inp = (value, on, attrs = {}) => { const i = h('input', { value: value ?? '', ...attrs }); i.addEventListener('input', () => on(i.value)); return i; };
export const chk = (label, value, on) => { const c = h('input', { type: 'checkbox', checked: !!value }); c.addEventListener('change', () => on(c.checked)); return h('label', { class: 'check' }, c, label); };

function showResult(r, extra) {
  const msg = [r.applied, r.output && r.output !== 'Keine Änderung.' ? r.output : (r.output || '')].filter(Boolean).join(' ');
  toast('Gespeichert', msg);
  return r.warnings || [];
}

export default {
  title: 'System-Einstellungen',
  async render(main, parts) {
    let pending = null;
    let tab = TABS.some(t => t[0] === parts[0]) ? parts[0] : 'ntp';
    const tabsEl = h('div', { class: 'tabs', role: 'tablist', 'aria-label': 'Bereiche' }), body = h('div', { class: 'content' }, loading());
    main.append(pageHead('HOST', 'System-Einstellungen', 'Zeitsynchronisation, DNS, Hosts-Datei, SSH-Server, doas-Rechte und Kernel-Parameter als Formulare. Zur Auswahl steht nur, was es auf diesem Host gibt; maar schreibt die Dateien im Hintergrund und prüft sie vorher.'), tabsEl, body);

    function drawTabs() {
      clear(tabsEl, TABS.map(([k, l, ic]) => {
        const b = h('button', { class: 'tab', type: 'button', role: 'tab', 'aria-selected': String(k === tab) }, icon(ic), l);
        b.addEventListener('click', () => { tab = k; history.replaceState(null, '', `#/hostconf/${k}`); drawTabs(); load(); });
        return b;
      }));
    }

    // Gemeinsame Leiste: Vorschau, Speichern, Zurücksetzen
    function actions(kind, payload, { onSaved, extra = null } = {}) {
      const preview = h('button', { class: 'secondary', type: 'button' }, 'Vorschau');
      const save = h('button', { class: 'cta', type: 'button' }, 'Speichern und anwenden');
      const reset = h('button', { class: 'secondary', type: 'button' }, 'Zurücksetzen');
      preview.addEventListener('click', () => busy(preview, async () => {
        const r = await put(`/api/conf/${kind}?preview=1`, payload());
        const close = h('button', { class: 'secondary', type: 'button' }, 'Schließen');
        const d = dialog({ title: 'So würde die Datei aussehen', meta: kind.toUpperCase(), wide: true, body: h('div', null, r.warnings?.length ? h('div', { class: 'note warn' }, r.warnings.map(w => h('div', null, w))) : null, h('pre', { class: 'codeblock' }, r.content || '(leer)')), actions: [close] });
        close.addEventListener('click', () => d.close());
      }).catch(toastError));
      save.addEventListener('click', () => busy(save, async () => {
        const r = await put(`/api/conf/${kind}`, payload());
        pending = { warns: showResult(r), applied: r.applied };
        await onSaved?.();
      }).catch(toastError));
      reset.addEventListener('click', () => load());
      return h('div', { class: 'toolbar' }, extra, preview, reset, save);
    }

    async function load() {
      clear(body, loading());
      try {
        const v = await get(`/api/conf/${tab === 'dns' ? 'resolv' : tab}`);
        await ({ ntp: drawNTP, dns: drawDNS, hosts: drawHosts, ssh: drawSSH, doas: drawDoas, sysctl: drawSysctl })[tab](v);
        if (pending) {
          body.prepend(h('div', { class: 'content-stack' }, pending.applied ? h('div', { class: 'note ok', role: 'status' }, pending.applied) : null,
            pending.warns.length ? h('div', { class: 'note warn', role: 'status' }, h('strong', null, 'Bitte beachten: '), pending.warns.map(w => h('div', null, w))) : null));
          pending = null;
        }
      } catch (e) { clear(body); toastError(e); }
    }

    // ---- NTP ----
    function drawNTP(v) {
      const c = JSON.parse(JSON.stringify(v.conf));
      const activate = { on: !v.enabled && !v.running };
      const servers = rows(c.servers, s => h('div', { class: 'cf-fields' },
        field('Server', inp(s.address, x => { s.address = x.trim(); }, { list: 'ntp-sug', placeholder: 'pool.ntp.org', class: 'mono' })),
        field('Art', sel([['false', 'Einzelner Server'], ['true', 'Pool (alle Adressen)']], s.pool, x => { s.pool = x === 'true'; })),
        field('Gewicht', sel(Array.from({ length: 10 }, (_, i) => [i + 1, i + 1]), s.weight || 1, x => { s.weight = Number(x); })),
        chk('vertrauenswürdig', s.trusted, x => { s.trusted = x; })), { add: 'Zeitserver hinzufügen', fresh: () => ({ address: '', pool: false, weight: 1, trusted: false }) });
      const sensorOpts = [['*', 'Alle Zeit-Sensoren (*)'], ...[...new Set([...v.availableSensors, ...c.sensors.map(s => s.device)])].filter(d => d !== '*').map(d => [d, d + (v.availableSensors.includes(d) ? '' : ' (nicht vorhanden)')])];
      const sensors = rows(c.sensors, s => h('div', { class: 'cf-fields' },
        field('Sensor', sel(sensorOpts, s.device, x => { s.device = x; })), field('Gewicht', sel(Array.from({ length: 10 }, (_, i) => [i + 1, i + 1]), s.weight || 1, x => { s.weight = Number(x); })), chk('vertrauenswürdig', s.trusted, x => { s.trusted = x; })),
        { add: 'Sensor hinzufügen', max: 10, fresh: () => ({ device: sensorOpts[1]?.[0] || '*', weight: 1, trusted: false }) });
      const cons = rows(c.constraints, k => h('div', { class: 'cf-fields' },
        field('Quelle (HTTPS)', inp(k.url, x => { k.url = x.trim(); }, { list: 'ntp-con', class: 'mono', placeholder: 'https://www.example.org/' })),
        field('Art', sel([['false', 'Einzelne Adresse'], ['true', 'Median über alle Adressen']], k.pool, x => { k.pool = x === 'true'; }))), { add: 'Constraint hinzufügen', max: 10, fresh: () => ({ url: '', pool: false }) });
      const listenSw = { on: c.listen.length > 0 };
      const addrOpts = [['*', 'Alle Adressen (*)'], ...[...new Set([...v.addresses, ...c.listen])].filter(a => a !== '*').map(a => [a, a])];
      const listenBox = h('div');
      const drawListen = () => clear(listenBox, listenSw.on ? rows(c.listen, (a, i) => field('Adresse', sel(addrOpts, a.value ?? a, x => { c.listen[i] = x; })), { add: 'Adresse hinzufügen', max: 20, fresh: () => addrOpts[0][0] }) : null);
      const listenChk = chk('Zeit für andere Geräte bereitstellen (listen on)', listenSw.on, x => { listenSw.on = x; if (x && !c.listen.length) c.listen.push(addrOpts[0][0]); if (!x) c.listen.length = 0; drawListen(); });
      drawListen();
      clear(body,
        panel('Status', 'NTPCTL', h('div', { class: 'panel-body flush' },
          h('div', { class: 'panel-body toolbar' }, v.running ? pill('ok', 'ntpd läuft') : v.enabled ? pill('bad', 'ntpd gestoppt') : pill('off', 'ntpd aus')),
          v.status ? h('pre', { class: 'codeblock' }, v.status) : h('div', { class: 'empty' }, 'ntpd läuft nicht – kein Status verfügbar.'))),
        panel('Zeitquellen', 'SERVER · SERVERS', h('div', { class: 'panel-body' }, h('datalist', { id: 'ntp-sug' }, v.suggestions.map(s => h('option', { value: s }))), servers,
          h('p', { class: 'hint' }, '„Pool“ nutzt alle Adressen hinter dem Namen (servers), „Einzelner Server“ nur die erste (server). Ein höheres Gewicht zählt stärker.'))),
        panel('Zeit-Sensoren', 'SENSOR', h('div', { class: 'panel-body' }, v.availableSensors.length ? null : h('p', { class: 'hint' }, 'Auf diesem Host meldet kein Sensor eine Zeitabweichung (z. B. DCF77 oder GPS).'), sensors)),
        panel('Constraints', 'CONSTRAINT FROM', h('div', { class: 'panel-body' }, h('datalist', { id: 'ntp-con' }, ['https://9.9.9.9', 'https://www.google.com/', 'https://www.cloudflare.com/'].map(s => h('option', { value: s }))), cons,
          h('p', { class: 'hint' }, 'ntpd fragt zusätzlich HTTPS-Server und verwirft Zeitquellen, die stark abweichen.'))),
        panel('Zeitdienst für andere', 'LISTEN ON', h('div', { class: 'panel-body' }, listenChk, listenBox)),
        v.preserved.length ? h('div', { class: 'note' }, 'Diese Zeilen kennt das Formular nicht und lässt sie unverändert: ', h('code', { class: 'mono' }, v.preserved.join(' · '))) : null,
        actions('ntp', () => ({ conf: { servers: c.servers.map(s => ({ ...s, weight: s.weight || 1 })), sensors: c.sensors, constraints: c.constraints, listen: c.listen }, activate: activate.on }), {
          onSaved: load, extra: !v.enabled && !v.running ? chk('ntpd aktivieren und starten', true, x => { activate.on = x; }) : null }));
    }

    // ---- DNS ----
    function drawDNS(v) {
      const c = JSON.parse(JSON.stringify(v.conf));
      const nsItems = c.nameservers.map(a => ({ a }));
      const nsBox = rows(nsItems, it => {
        const input = inp(it.a, x => { it.a = x.trim(); }, { class: 'mono', placeholder: '192.168.50.1', 'aria-label': 'Nameserver' });
        const pick = sel([['', 'Vorschlag wählen …'], ...v.suggestions.map(x => [x.address, `${x.address} – ${x.label}`])], '', x => { if (x) { it.a = x; input.value = x; pick.value = ''; } });
        return h('div', { class: 'cf-fields' }, field('Nameserver', input), field('Vorschlag', pick));
      }, { add: 'Nameserver hinzufügen', max: 5, fresh: () => ({ a: '' }) });
      const searchItems = c.search.map(d => ({ d }));
      const searchBox = rows(searchItems, it => field('Suchdomäne', inp(it.d, x => { it.d = x.trim().toLowerCase(); }, { class: 'mono', placeholder: 'ctw.lan' })), { add: 'Suchdomäne hinzufügen', max: 6, fresh: () => ({ d: '' }) });
      const LOOKUP = [['bind,file', 'Erst DNS, dann Hosts-Datei (bind file) – Standard'], ['file,bind', 'Erst Hosts-Datei, dann DNS (file bind)'], ['bind', 'Nur DNS'], ['file', 'Nur Hosts-Datei']];
      const FAMILY = [['', 'Standard (IPv4 zuerst, dann IPv6)'], ['inet4', 'Nur IPv4'], ['inet6', 'Nur IPv6'], ['inet6,inet4', 'IPv6 zuerst']];
      const lookup = sel(LOOKUP, (c.lookup.length ? c.lookup : ['bind', 'file']).join(','), x => { c.lookup = x.split(','); });
      const family = sel(FAMILY, c.family.join(','), x => { c.family = x ? x.split(',') : []; });
      const domain = inp(c.domain, x => { c.domain = x.trim().toLowerCase(); }, { class: 'mono', placeholder: 'wird aus dem Hostnamen abgeleitet' });
      const ndots = sel([['0', 'Standard (1)'], ...[1, 2, 3, 4, 5].map(n => [n, String(n)])], c.ndots, x => { c.ndots = Number(x); });
      clear(body,
        v.resolvd ? h('div', { class: 'note warn', role: 'status' }, 'resolvd ist aktiv und kann resolv.conf selbst umschreiben (z. B. bei DHCP-Änderungen).') : null,
        panel('Nameserver', 'NAMESERVER', h('div', { class: 'panel-body' }, nsBox, h('p', { class: 'hint' }, 'Es gelten bis zu fünf, in der Reihenfolge von oben nach unten. Die Vorschläge kommen von diesem Host (laufender Resolver, Gateway).'))),
        panel('Suchdomänen', 'SEARCH · DOMAIN', h('div', { class: 'panel-body' }, searchBox, field('Lokale Domain (nur ohne Suchdomänen)', domain, 'Mit Suchdomänen entfällt sie – search hat Vorrang.'))),
        panel('Verhalten', 'LOOKUP · FAMILY · OPTIONS', h('div', { class: 'panel-body form-grid' }, field('Auflösungsreihenfolge', lookup), field('Adressfamilie', family),
          h('div', { class: 'wide toolbar' }, chk('EDNS0 (nur wenn alle Nameserver es können)', c.edns0, x => { c.edns0 = x; }), chk('TCP erzwingen', c.tcp, x => { c.tcp = x; }), chk('Authenticated-Data-Flag vertrauen (trust-ad)', c.trustAd, x => { c.trustAd = x; })),
          field('Punkte für „absolute Namen“ (ndots)', ndots))),
        v.preserved.length || v.otherOptions.length ? h('div', { class: 'note' }, 'Unverändert bleiben: ', h('code', { class: 'mono' }, [...v.preserved, ...(v.otherOptions.length ? ['options ' + v.otherOptions.join(' ')] : [])].join(' · '))) : null,
        actions('resolv', () => ({ ...c, nameservers: nsItems.map(i => i.a).filter(Boolean), search: searchItems.map(i => i.d).filter(Boolean) }), { onSaved: load }));
    }

    // ---- hosts ----
    async function drawHosts(v) {
      const items = v.entries.map(e => ({ ...e, namesText: e.names.join(' ') }));
      let vmNics = [];
      try { vmNics = (await get('/api/vnet')).nics.filter(n => n.ip); } catch { /* Netz nicht verfügbar */ }
      const listBox = h('div');
      const filter = h('input', { type: 'search', placeholder: 'Filtern …', 'aria-label': 'Filtern' });
      function draw() {
        const q = filter.value.trim().toLowerCase();
        clear(listBox, table(['Adresse', 'Namen (durch Leerzeichen getrennt)', 'Kommentar', ''], items.map((it, i) => {
          if (q && !(`${it.ip} ${it.namesText} ${it.comment}`).toLowerCase().includes(q)) return null;
          const loop = it.ip === '127.0.0.1' || it.ip === '::1';
          return h('tr', null, h('td', null, inp(it.ip, x => { it.ip = x.trim(); }, { class: 'mono', 'aria-label': 'Adresse' })), h('td', null, inp(it.namesText, x => { it.namesText = x; }, { class: 'mono', 'aria-label': 'Namen' })),
            h('td', null, inp(it.comment, x => { it.comment = x; }, { 'aria-label': 'Kommentar' })), h('td', { class: 'actions' }, h('button', { class: 'mini-btn danger', type: 'button', disabled: loop, title: loop ? 'Loopback-Eintrag bleibt' : null, onclick: () => { items.splice(i, 1); draw(); } }, 'Entfernen')));
        }).filter(Boolean), 'Keine Einträge.'));
      }
      filter.addEventListener('input', draw);
      const add = h('button', { class: 'mini-btn', type: 'button', onclick: () => { items.push({ id: 0, ip: '', names: [], namesText: '', comment: '' }); filter.value = ''; draw(); } }, 'Eintrag hinzufügen');
      const have = new Set(items.map(i => i.ip));
      const vmSel = vmNics.filter(n => !have.has(n.ip)).length ? sel([['', 'VM-Adresse übernehmen …'], ...vmNics.filter(n => !have.has(n.ip)).map(n => [n.ip, `${n.ip} – ${n.fqdn || n.vm}`])], '', x => {
        const n = vmNics.find(y => y.ip === x); if (!n) return; items.push({ id: 0, ip: n.ip, names: [], namesText: [n.fqdn, n.hostname].filter(Boolean).join(' '), comment: `VM ${n.vm}` }); draw();
      }) : null;
      draw();
      clear(body, panel('Hosts-Datei', '/ETC/HOSTS', h('div', { class: 'panel-body flush' }, h('div', { class: 'panel-body toolbar' }, filter, add, vmSel), listBox, h('div', { class: 'note' }, 'Die Zuordnung 127.0.0.1/::1 → localhost bleibt immer erhalten. Kommentare und Leerzeilen in der Datei werden nicht angefasst.'))),
        actions('hosts', () => ({ entries: items.map(i => ({ id: i.id, ip: i.ip, names: i.namesText.split(/\s+/).filter(Boolean), comment: i.comment })) }), { onSaved: load }));
    }

    // ---- SSH ----
    function drawSSH(v) {
      const c = JSON.parse(JSON.stringify(v.conf)), d = v.defaults;
      const dflt = (key, opts) => [['', `Standard (${d[key]})`], ...opts];
      const yn = key => dflt(key, [['yes', 'ja'], ['no', 'nein']]);
      const portItems = c.ports.map(p => ({ p }));
      const portBox = rows(portItems, it => field('Port', inp(it.p, x => { it.p = num(x); }, { type: 'number', min: 1, max: 65535 })), { add: 'Port hinzufügen', max: 5, fresh: () => ({ p: 2222 }) });
      const listenOpts = [...new Set([...v.addresses, ...c.listenAddress])].map(a => [a, a]);
      const listenItems = c.listenAddress.map(a => ({ a }));
      const listenBox = rows(listenItems, it => field('Nur auf dieser Adresse lauschen', sel(listenOpts, it.a, x => { it.a = x; })), { add: 'Adresse hinzufügen', max: 10, fresh: () => ({ a: listenOpts[0]?.[0] || '' }) });
      const checks = (list, items, label) => h('div', { class: 'checks' }, items.map(([name, hint]) => chk(hint ? `${name} (${hint})` : name, list.includes(name), x => { const i = list.indexOf(name); if (x && i < 0) list.push(name); if (!x && i >= 0) list.splice(i, 1); })));
      const keyHint = u => v.authorizedKeys[u] ? `${v.authorizedKeys[u]} Schlüssel` : 'kein Schlüssel';
      const num5 = (cur, key, opts) => sel([['0', `Standard (${d[key]})`], ...opts.map(n => [n, String(n)])], cur, x => { c[key] = Number(x); });
      clear(body,
        v.matchBlocks ? h('div', { class: 'note warn' }, `${v.matchBlocks} Match-Block/Blöcke in der Datei bleiben unverändert und können diese Einstellungen für bestimmte Konten oder Adressen übersteuern.`) : null,
        h('div', { class: 'note' }, h('strong', null, 'Sicherheitsnetz: '), 'maar lehnt Einstellungen ab, die dich selbst per SSH aussperren würden (du fehlst in AllowUsers/AllowGroups, Passwort aus ohne eigenen Schlüssel, keine Methode übrig). Bestehende Sitzungen bleiben nach dem Neuladen offen.'),
        panel('Anmeldung', 'AUTHENTICATION', h('div', { class: 'panel-body form-grid' },
          field('Root-Anmeldung', sel(dflt('permitRootLogin', [['yes', 'erlaubt'], ['prohibit-password', 'nur mit Schlüssel'], ['forced-commands-only', 'nur für erzwungene Befehle'], ['no', 'verboten']]), c.permitRootLogin, x => { c.permitRootLogin = x; })),
          field('Anmeldung per Schlüssel', sel(yn('pubkeyAuthentication'), c.pubkeyAuthentication, x => { c.pubkeyAuthentication = x; })),
          field('Passwort-Anmeldung', sel(yn('passwordAuthentication'), c.passwordAuthentication, x => { c.passwordAuthentication = x; })),
          field('Tastatur-interaktiv (Passwort über BSD Auth)', sel(yn('kbdInteractiveAuthentication'), c.kbdInteractiveAuthentication, x => { c.kbdInteractiveAuthentication = x; }), 'Wer nur „Passwort“ abschaltet, kann sich mit dieser Methode weiter per Passwort anmelden.'),
          field('Fehlversuche je Verbindung', num5(c.maxAuthTries, 'maxAuthTries', [2, 3, 4, 5, 6, 8, 10])),
          field('Zeit für die Anmeldung (Sekunden)', num5(c.loginGraceTime, 'loginGraceTime', [15, 30, 60, 120, 300])))),
        panel('Wer darf sich anmelden', 'ALLOWUSERS · ALLOWGROUPS', h('div', { class: 'panel-body' },
          h('p', { class: 'hint' }, 'Nichts angekreuzt = alle Konten dürfen (soweit sonst erlaubt). Sobald ein Konto angekreuzt ist, dürfen nur noch die angekreuzten.'),
          h('strong', null, 'Konten'), checks(c.allowUsers, v.users.map(u => [u, keyHint(u)])),
          h('strong', { style: { display: 'block', marginTop: '14px' } }, 'Gruppen'), checks(c.allowGroups, v.groups.map(g => [g, '']))),
        ),
        panel('Netzwerk', 'PORT · LISTENADDRESS', h('div', { class: 'panel-body form-grid' }, h('div', null, portBox, h('p', { class: 'hint' }, 'Leer = Standardport 22. Ein anderer Port muss im Paketfilter erlaubt sein.')), h('div', null, listenBox, h('p', { class: 'hint' }, 'Leer = auf allen Adressen.')))),
        panel('Weiterleitung und Protokoll', 'FORWARDING · LOGLEVEL', h('div', { class: 'panel-body form-grid' },
          field('X11-Weiterleitung', sel(yn('x11Forwarding'), c.x11Forwarding, x => { c.x11Forwarding = x; })),
          field('TCP-Weiterleitung', sel(dflt('allowTcpForwarding', [['yes', 'ja'], ['no', 'nein'], ['local', 'nur lokal (-L)'], ['remote', 'nur entfernt (-R)']]), c.allowTcpForwarding, x => { c.allowTcpForwarding = x; })),
          field('Verbindung prüfen alle … Sekunden (ClientAliveInterval)', num5(c.clientAliveInterval, 'clientAliveInterval', [30, 60, 120, 300, 600])),
          field('… bis zum Trennen nach … Fehlschlägen', num5(c.clientAliveCountMax, 'clientAliveCountMax', [1, 2, 3, 5, 10])),
          field('Protokollstufe', sel(dflt('logLevel', ['QUIET', 'FATAL', 'ERROR', 'INFO', 'VERBOSE', 'DEBUG'].map(x => [x, x])), c.logLevel, x => { c.logLevel = x; })))),
        v.preserved.length ? h('div', { class: 'note' }, 'Unverändert bleiben: ', h('code', { class: 'mono' }, v.preserved.join(' · '))) : null,
        actions('ssh', () => ({ ...c, ports: portItems.map(i => i.p).filter(Boolean), listenAddress: listenItems.map(i => i.a).filter(Boolean) }), { onSaved: load }));
    }


    // ---- doas ----
    function drawDoas(v) {
      const c = JSON.parse(JSON.stringify(v.conf));
      const who = [...v.users.map(u => [u, `Benutzer ${u}`]), ...v.groups.map(g => [`:${g}`, `Gruppe ${g}`])];
      const asOpts = [['', 'root (Standard)'], ...v.users.filter(u => u !== 'root').map(u => [u, u])];
      const describe = r => {
        if (r.raw) return 'Erweiterte Regel – das Formular lässt sie unverändert.';
        const wer = r.identity.startsWith(':') ? `Gruppe ${r.identity.slice(1)}` : `Benutzer ${r.identity}`;
        const was = r.cmd ? `${r.cmd}${r.args?.length ? ' ' + r.args.join(' ') : ''}` : 'beliebige Befehle';
        const opts = [r.nopass ? 'ohne Passwort' : r.persist ? 'Passwort wird einige Minuten gemerkt' : 'mit Passwort', r.keepenv ? 'Umgebung bleibt' : null, r.nolog ? 'nicht protokolliert' : null].filter(Boolean).join(', ');
        return `${wer} ${r.action === 'permit' ? 'darf' : 'darf nicht'} ${was} als ${r.as || 'root'} ausführen (${opts}).`;
      };
      const box = h('div', { class: 'cf-rows' });
      const draw = () => box.replaceChildren(...c.rules.map((r, i) => {
        const text = h('p', { class: 'hint rule-line' }, describe(r));
        const upd = () => { text.textContent = describe(r); };
        const move = d => () => { const j = i + d; if (j < 0 || j >= c.rules.length) return; [c.rules[i], c.rules[j]] = [c.rules[j], c.rules[i]]; draw(); };
        const ctl = h('div', { class: 'stack' }, h('div', { class: 'toolbar' }, h('button', { class: 'mini-btn', type: 'button', disabled: i === 0, 'aria-label': 'nach oben', onclick: move(-1) }, '↑'),
          h('button', { class: 'mini-btn', type: 'button', disabled: i === c.rules.length - 1, 'aria-label': 'nach unten', onclick: move(1) }, '↓'), h('button', { class: 'mini-btn danger', type: 'button', onclick: () => { c.rules.splice(i, 1); draw(); } }, 'Entfernen')));
        if (r.raw) return h('div', { class: 'cf-row' }, h('div', { class: 'cf-fields' }, h('code', { class: 'mono' }, r.raw), text), ctl);
        const cmdKnown = !r.cmd || v.commands.includes(r.cmd);
        const cmdSel = sel([['', 'Alle Befehle'], ...v.commands.map(x => [x, x]), ['__other', 'Anderer Pfad …']], cmdKnown ? r.cmd : '__other', x => { if (x === '__other') { cmdIn.hidden = false; r.cmd = cmdIn.value.trim(); } else { cmdIn.hidden = true; r.cmd = x; } if (!r.cmd) { r.args = []; argIn.value = ''; } argIn.disabled = !r.cmd; upd(); });
        const cmdIn = h('input', { class: 'mono', value: cmdKnown ? '' : r.cmd, hidden: cmdKnown, placeholder: '/usr/local/bin/programm', 'aria-label': 'Befehlspfad' });
        cmdIn.addEventListener('input', () => { r.cmd = cmdIn.value.trim(); upd(); });
        const argIn = h('input', { class: 'mono', value: (r.args || []).join(' '), disabled: !r.cmd, placeholder: 'nur diese Argumente, z. B. status', 'aria-label': 'Argumente' });
        argIn.addEventListener('input', () => { r.args = argIn.value.split(/\s+/).filter(Boolean); upd(); });
        return h('div', { class: 'cf-row' }, h('div', { class: 'cf-fields' },
          field('Aktion', sel([['permit', 'erlauben'], ['deny', 'verbieten']], r.action, x => { r.action = x; upd(); })),
          field('Wer', sel(who, r.identity, x => { r.identity = x; upd(); })), field('Als', sel(asOpts, r.as, x => { r.as = x; upd(); })),
          field('Befehl', h('div', { class: 'stack' }, cmdSel, cmdIn)), field('Argumente', argIn),
          h('div', { class: 'stack' }, chk('ohne Passwort (nopass)', r.nopass, x => { r.nopass = x; if (x) r.persist = false; draw(); }), chk('Passwort merken (persist)', r.persist, x => { r.persist = x; if (x) r.nopass = false; draw(); }),
            chk('Umgebung behalten (keepenv)', r.keepenv, x => { r.keepenv = x; upd(); }), chk('nicht protokollieren (nolog)', r.nolog, x => { r.nolog = x; upd(); })),
          field('Beschreibung', h('input', { value: r.comment, maxlength: 200, 'aria-label': 'Beschreibung', oninput: e => { r.comment = e.target.value; } })), text), ctl);
      }), h('div', null, h('button', { class: 'mini-btn', type: 'button', onclick: () => { c.rules.push({ action: 'permit', identity: v.groups.includes('wheel') ? ':wheel' : v.users[0], as: '', cmd: '', args: [], nopass: false, nolog: false, persist: true, keepenv: false, comment: '' }); draw(); } }, 'Regel hinzufügen')));
      draw();
      clear(body,
        h('div', { class: 'note' }, h('strong', null, 'So wertet doas aus: '), 'von oben nach unten, es gilt ', h('strong', null, 'die letzte passende Regel'), '. Ohne passende Regel ist alles verboten. Regeln mit setenv oder anderer Sonderschreibweise bleiben als Rohtext unverändert stehen.'),
        panel('Rechte', 'DOAS.CONF', h('div', { class: 'panel-body' }, box)),
        actions('doas', () => ({ rules: c.rules }), { onSaved: load }));
    }

    // ---- sysctl ----
    function drawSysctl(v) {
      const items = v.entries.map(e => ({ ...e }));
      const cat = Object.fromEntries(v.catalog.map(c => [c.key, c]));
      const st = { now: true };
      const box = h('div');
      const draw = () => clear(box, h('div', { class: 'table-wrap' }, h('table', { class: 'table' },
        h('thead', null, h('tr', null, ['Parameter', 'Wert', 'Läuft gerade', ''].map(t => h('th', { scope: 'col' }, t)))),
        h('tbody', null, items.map((e, i) => {
          if (e.raw) return h('tr', null, h('td', { colSpan: 3 }, h('code', { class: 'mono' }, e.raw), h('span', { class: 'sub' }, 'Das Formular versteht diese Zeile nicht und lässt sie unverändert.')), h('td', { class: 'actions' }, h('button', { class: 'mini-btn danger', type: 'button', onclick: () => { items.splice(i, 1); draw(); } }, 'Entfernen')));
          const c = cat[e.key];
          const control = !c ? inp(e.value, x => { e.value = x.trim(); draw2(); }, { class: 'mono', 'aria-label': 'Wert' })
            : c.kind === 'bool' ? sel([['0', 'aus (0)'], ['1', 'an (1)']], e.value, x => { e.value = x; draw2(); })
            : c.kind === 'enum' ? sel(c.options.map(o => [o, o]), e.value, x => { e.value = x; draw2(); })
            : inp(e.value, x => { e.value = x.trim(); draw2(); }, { type: 'number', min: c.min, max: c.max, class: 'mono', 'aria-label': 'Wert' });
          const live = h('td', { class: 'mono' });
          const draw2 = () => { const cur = v.current[e.key]; live.replaceChildren(cur ?? '–', ' ', cur !== undefined && cur !== e.value ? pill('warn', 'weicht ab') : ''); };
          draw2();
          return h('tr', null, h('td', null, h('span', { class: 'name mono' }, e.key), c ? h('span', { class: 'sub' }, `${c.label} – ${c.description}`) : null, c?.risk ? h('span', { class: 'sub' }, '⚠ ' + c.risk) : null),
            h('td', null, control), live, h('td', { class: 'actions' }, h('button', { class: 'mini-btn danger', type: 'button', onclick: () => { items.splice(i, 1); draw(); } }, 'Entfernen')));
        }).filter(Boolean)))));
      draw();
      const used = () => new Set(items.map(i => i.key));
      const pick = h('select', { 'aria-label': 'Häufigen Parameter hinzufügen' }, h('option', { value: '' }, 'Häufigen Parameter hinzufügen …'),
        ...[...new Set(v.catalog.map(c => c.group))].map(g => h('optgroup', { label: g }, v.catalog.filter(c => c.group === g).map(c => h('option', { value: c.key }, `${c.label} (${c.key})`)))));
      pick.addEventListener('change', () => { const c = cat[pick.value]; if (!c) return; if (used().has(c.key)) { toast('Schon in der Liste.'); } else { items.push({ id: 0, key: c.key, value: c.current }); v.current[c.key] = c.current; draw(); } pick.value = ''; });
      const other = h('input', { list: 'sysctl-all', placeholder: 'Anderer Parameter, z. B. net.inet.ip.mtudisc', class: 'mono', 'aria-label': 'Anderer Parameter' });
      const addOther = h('button', { class: 'mini-btn', type: 'button' }, 'Hinzufügen');
      addOther.addEventListener('click', () => {
        const k = other.value.trim();
        if (!v.all.includes(k)) { toastError(new Error(`„${k}“ gibt es in diesem Kernel nicht.`)); return; }
        if (used().has(k)) { toast('Schon in der Liste.'); return; }
        // aktueller Wert aus der Systemliste ist hier nicht geladen – Wert wird beim Hinzufügen abgefragt
        get('/api/sysctl').then(list => { const cur = list.find(s => s.name === k)?.value ?? ''; v.current[k] = cur; items.push({ id: 0, key: k, value: cur }); other.value = ''; draw(); }).catch(toastError);
      });
      clear(body,
        h('div', { class: 'note' }, h('strong', null, 'So funktioniert es: '), 'Einträge in /etc/sysctl.conf werden bei jedem Start gesetzt. Mit „Sofort anwenden“ setzt maar die geänderten Werte zusätzlich im laufenden Kernel. Ein Parameter muss schreibbar sein – nur lesbare lehnt maar ab, bevor etwas geschrieben wird.'),
        panel('Kernel-Parameter', '/ETC/SYSCTL.CONF', h('div', { class: 'panel-body' }, h('div', { class: 'toolbar' }, pick, other, addOther), h('datalist', { id: 'sysctl-all' }, v.all.map(k => h('option', { value: k }))), box)),
        actions('sysctl', () => ({ entries: items.map(e => ({ id: e.id, key: e.key || '', value: e.value || '', raw: e.raw })), applyNow: st.now }), { onSaved: load, extra: chk('Geänderte Werte sofort anwenden', true, x => { st.now = x; }) }));
    }

    drawTabs();
    await load();
  },
};
