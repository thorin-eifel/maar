// Webserver (httpd) und Mail (smtpd) als Formulare. Auswahl nur aus dem, was es auf dem Host gibt;
// die Dateien schreibt maar im Hintergrund (Prüfung, Sicherung, Neuladen). location-Blöcke und
// unbekannte Zeilen bleiben unverändert.
import { h, clear, pageHead, panel, pill, loading, toast, toastError, confirm, dialog, field, busy } from '../ui.js';
import { get, put } from '../api.js';
import { rows, sel, inp, chk } from './hostconf.js';

const TABS = [['httpd', 'Webserver (httpd)'], ['smtpd', 'Mail (smtpd)']];
const clone = x => JSON.parse(JSON.stringify(x));

export default {
  title: 'Webserver & Mail',
  async render(main, parts) {
    let tab = TABS.some(t => t[0] === parts[0]) ? parts[0] : 'httpd', pending = null;
    const tabsEl = h('div', { class: 'tabs', role: 'tablist', 'aria-label': 'Dienste' }), body = h('div', { class: 'content' }, loading());
    main.append(pageHead('21 / HOST-KONFIGURATION', 'Webserver & Mail', 'Webseiten und Mail-Zustellung als Formulare. Zur Auswahl steht nur, was es auf diesem Host gibt; maar schreibt die Dateien im Hintergrund, prüft sie und lädt den Dienst neu.'), tabsEl, body);

    function drawTabs() {
      clear(tabsEl, TABS.map(([k, l]) => {
        const b = h('button', { class: 'tab', type: 'button', role: 'tab', 'aria-selected': String(k === tab) }, l);
        b.addEventListener('click', () => { tab = k; history.replaceState(null, '', `#/webmail/${k}`); drawTabs(); load(); });
        return b;
      }));
    }

    function actions(kind, payload, extra) {
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
        toast('Gespeichert', [r.applied, r.output].filter(Boolean).join(' '));
        pending = { warns: r.warnings || [], applied: r.applied };
        await load();
      }).catch(toastError));
      reset.addEventListener('click', () => load());
      return h('div', { class: 'toolbar' }, extra, preview, reset, save);
    }

    async function load() {
      clear(body, loading());
      try {
        const v = await get(`/api/conf/${tab}`);
        await (tab === 'httpd' ? drawHTTPD : drawSMTPD)(v);
        if (pending) {
          body.prepend(h('div', { class: 'content-stack' }, pending.applied ? h('div', { class: 'note ok', role: 'status' }, pending.applied) : null,
            pending.warns.length ? h('div', { class: 'note warn', role: 'status' }, h('strong', null, 'Bitte beachten: '), pending.warns.map(w => h('div', null, w))) : null));
          pending = null;
        }
      } catch (e) { clear(body); toastError(e); }
    }

    const status = v => h('div', { class: 'toolbar' }, v.running ? pill('ok', 'läuft') : v.enabled ? pill('bad', 'gestoppt') : pill('off', 'aus'));
    const activateBox = (v, st) => !v.enabled && !v.running ? chk('Dienst aktivieren und starten', true, x => { st.on = x; }) : null;

    // ---- httpd ----
    function drawHTTPD(v) {
      const c = clone(v.conf), st = { on: !v.enabled && !v.running };
      const cards = h('div', { class: 'content-stack' });
      const roots = s => [...new Set([...v.roots, s.root].filter(Boolean))];
      const addrOpts = [...new Set([...v.addresses])].map(a => [a, a === '*' ? 'Alle Adressen (*)' : a]);
      const MAX = [['0', 'aus'], ['15768000', '6 Monate'], ['31536000', '1 Jahr'], ['63072000', '2 Jahre']];

      function drawCards() {
        cards.replaceChildren(...c.servers.map((s, i) => {
          const hasTLS = () => s.listen.some(l => l.tls);
          const tlsBox = h('div', { class: 'form-grid' });
          const drawTLS = () => clear(tlsBox, field('Zertifikat', sel([['', 'Standard (/etc/ssl/server.crt)'], ...v.certificates.map(x => [x, x])], s.tlsCertificate, x => { s.tlsCertificate = x; })),
            field('Schlüssel', sel([['', 'Standard (/etc/ssl/private/server.key)'], ...v.keys.map(x => [x, x])], s.tlsKey, x => { s.tlsKey = x; })));
          const listen = rows(s.listen, l => h('div', { class: 'cf-fields' },
            field('Adresse', sel(addrOpts.some(([a]) => a === l.addr) ? addrOpts : [...addrOpts, [l.addr, l.addr]], l.addr, x => { l.addr = x; })),
            field('Port', h('div', { class: 'stack' }, sel([['80', '80 (http)'], ['443', '443 (https)'], ['8080', '8080'], ['8443', '8443'], ...(['80', '443', '8080', '8443'].includes(String(l.port)) ? [] : [[String(l.port), String(l.port)]])], String(l.port), x => { l.port = x; }))),
            chk('mit TLS (https)', l.tls, x => { l.tls = x; drawTLS(); })), { add: 'Adresse hinzufügen', max: 10, fresh: () => ({ addr: '*', port: '80', tls: false }) });
          // rows arbeitet auf dem Array – Aliase als Objekte führen und beim Speichern zurückwandeln
          s._aliases = s.aliases.map(a => ({ a }));
          const aliasRows = rows(s._aliases, it => field('Alias', inp(it.a, x => { it.a = x.trim(); }, { class: 'mono', placeholder: 'www.example.org' })), { add: 'Alias hinzufügen', max: 20, fresh: () => ({ a: '' }) });
          drawTLS();
          const redirectUrl = inp(s.redirectUrl, x => { s.redirectUrl = x.trim(); }, { class: 'mono', placeholder: 'https://www.example.org$REQUEST_URI' });
          const rm = h('button', { class: 'mini-btn danger', type: 'button' }, 'Webseite entfernen');
          rm.addEventListener('click', async () => { if (await confirm({ title: `${s.name || 'Webseite'} entfernen?`, text: 'Der Block wird aus httpd.conf gelöscht, einschließlich seiner location-Blöcke. Die Dateien der Webseite bleiben liegen.', ok: 'Entfernen', danger: true })) { c.servers.splice(i, 1); drawCards(); } });
          return panel(s.name || 'Neue Webseite', s.id ? `SERVER · ${s.locations} LOCATION-BLÖCKE` : 'NEU', h('div', { class: 'panel-body' },
            h('div', { class: 'form-grid' }, field('Name (server)', inp(s.name, x => { s.name = x.trim(); }, { class: 'mono', placeholder: 'www.example.org' })),
              field('Wurzelverzeichnis', sel([['', 'Standard (/htdocs)'], ...roots(s).map(r => [r, r])], s.root, x => { s.root = x; }), `unterhalb von ${v.chroot}`)),
            h('strong', null, 'Lauschen auf'), listen, h('strong', { style: { display: 'block', marginTop: '14px' } }, 'Weitere Namen'), aliasRows,
            h('details', { class: 'expert', open: !!(s.tlsCertificate || s.tlsKey) }, h('summary', null, 'TLS-Zertifikat'), tlsBox),
            h('details', { class: 'expert' }, h('summary', null, 'Verzeichnisse, Protokoll, HSTS, Weiterleitung'), h('div', { class: 'form-grid' },
              field('Verzeichnisliste', sel([['', 'Standard (aus)'], ['yes', 'anzeigen'], ['no', 'nie anzeigen']], s.autoIndex, x => { s.autoIndex = x; })),
              field('Indexdatei', inp(s.indexFile, x => { s.indexFile = x.trim(); }, { class: 'mono', placeholder: 'index.html (Standard)' })),
              field('Zugriffsprotokoll', sel([['', 'Standard (common)'], ['common', 'common'], ['combined', 'combined'], ['forwarded', 'forwarded (hinter Proxy)'], ['connection', 'connection']], s.logStyle, x => { s.logStyle = x; })),
              chk('nicht protokollieren (no log)', s.noLog, x => { s.noLog = x; }),
              field('HSTS (nur über https)', sel(MAX, s.hstsMaxAge, x => { s.hstsMaxAge = Number(x); })), chk('auch für Subdomains', s.hstsSubdomains, x => { s.hstsSubdomains = x; }),
              field('Weiterleitung', sel([['0', 'keine'], ['301', '301 dauerhaft'], ['302', '302 vorübergehend'], ['307', '307 (Methode bleibt)'], ['308', '308 dauerhaft (Methode bleibt)']], s.redirectCode, x => { s.redirectCode = Number(x); })), field('Ziel der Weiterleitung', redirectUrl))),
            s.notes.length ? h('p', { class: 'hint' }, 'Bleiben unverändert: ', s.notes.map(n => h('code', { class: 'mono tag' }, n))) : null,
            h('div', { class: 'toolbar' }, rm)));
        }), h('div', { class: 'toolbar' }, h('button', { class: 'mini-btn', type: 'button', onclick: () => { c.servers.push({ id: 0, name: '', aliases: [], listen: [{ addr: '*', port: '80', tls: false }], root: '', tlsCertificate: '', tlsKey: '', autoIndex: '', indexFile: '', logStyle: '', noLog: false, hstsMaxAge: 0, hstsSubdomains: false, redirectCode: 0, redirectUrl: '', notes: [], locations: 0 }); drawCards(); } }, 'Webseite hinzufügen'),
          h('button', { class: 'mini-btn', type: 'button', onclick: () => { c.servers.push({ id: 0, name: '', aliases: [], listen: [{ addr: '*', port: '443', tls: true }], root: v.roots[0] || '', tlsCertificate: v.certificates[0] || '', tlsKey: v.keys[0] || '', autoIndex: '', indexFile: '', logStyle: '', noLog: false, hstsMaxAge: 0, hstsSubdomains: false, redirectCode: 0, redirectUrl: '', notes: [], locations: 0 }); drawCards(); } }, 'Webseite mit HTTPS hinzufügen')));
      }
      drawCards();
      clear(body, status(v),
        panel('Allgemein', 'PREFORK', h('div', { class: 'panel-body' }, field('Arbeitsprozesse (prefork)', sel([['0', 'Standard (3)'], ...Array.from({ length: 16 }, (_, i) => [i + 1, String(i + 1)])], c.prefork, x => { c.prefork = Number(x); }), 'Mehr Prozesse bedienen mehr gleichzeitige Verbindungen.'))),
        cards,
        v.other.length ? h('div', { class: 'note' }, 'Unverändert bleiben außerhalb der Webseiten: ', h('code', { class: 'mono' }, v.other.join(' · '))) : null,
        actions('httpd', () => ({ conf: { prefork: c.prefork, servers: c.servers.map(s => { const { _aliases, ...rest } = s; return { ...rest, aliases: (_aliases || []).map(x => x.a).filter(Boolean), listen: s.listen.map(l => ({ ...l, port: String(l.port) })) }; }) }, activate: st.on }), activateBox(v, st)));
    }

    // ---- smtpd ----
    function drawSMTPD(v) {
      const c = clone(v.conf), st = { on: !v.enabled && !v.running };
      const tableNames = () => c.tables.filter(t => !t.raw).map(t => t.name).filter(Boolean);
      const pkiNames = () => c.pkis.filter(p => !p.raw).map(p => p.name).filter(Boolean);
      const actNames = () => c.actions.filter(a => !a.raw).map(a => a.name).filter(Boolean);
      const rawRow = (it, text) => h('div', { class: 'cf-fields' }, h('code', { class: 'mono' }, it.raw), h('p', { class: 'hint' }, text || 'Das Formular versteht diese Zeile nicht und lässt sie unverändert.'));
      const cmt = it => field('Beschreibung', inp(it.comment || '', x => { it.comment = x; }, { 'aria-label': 'Beschreibung', maxlength: 100 }));
      const tableSel = (label, cur, on) => field(label, sel([['', '–'], ...tableNames().map(t => [t, `<${t}>`])], cur, on));

      const tables = rows(c.tables, t => t.raw ? rawRow(t) : h('div', { class: 'cf-fields' }, field('Name', inp(t.name, x => { t.name = x.trim(); }, { class: 'mono' })),
        field('Art', sel([['file', 'Textdatei (file)'], ['db', 'Datenbank (db)']], t.type, x => { t.type = x; })),
        field('Datei', sel([...new Set([t.path, ...v.tableFiles].filter(Boolean))].map(f => [f, f]), t.path, x => { t.path = x; })), cmt(t)),
        { add: 'Tabelle hinzufügen', max: 30, fresh: () => ({ name: '', type: 'file', path: v.tableFiles[0] || '', comment: '' }) });
      const pkis = rows(c.pkis, p => p.raw ? rawRow(p) : h('div', { class: 'cf-fields' }, field('Name', inp(p.name, x => { p.name = x.trim(); }, { class: 'mono', placeholder: 'mx' })),
        field('Zertifikat', sel([['', '–'], ...v.certificates.map(f => [f, f])], p.cert, x => { p.cert = x; })), field('Schlüssel', sel([['', '–'], ...v.keys.map(f => [f, f])], p.key, x => { p.key = x; }))),
        { add: 'Zertifikat hinzufügen', max: 20, fresh: () => ({ name: '', cert: '', key: '', comment: '' }) });
      const TLS = [['', 'kein TLS'], ['tls', 'STARTTLS (tls)'], ['tls-require', 'STARTTLS erzwingen (tls-require)'], ['smtps', 'Implizites TLS (smtps, Port 465)']];
      const listens = rows(c.listens, l => l.raw ? rawRow(l) : h('div', { class: 'cf-fields' },
        field('Lauschen auf', sel([...new Set([l.on, ...v.interfaces].filter(Boolean))].map(i => [i, i]), l.on, x => { l.on = x; })),
        field('Port', sel([['0', 'Standard'], ['25', '25 (smtp)'], ['465', '465 (smtps)'], ['587', '587 (submission)']], l.port, x => { l.port = Number(x); })),
        field('Verschlüsselung', sel(TLS, l.tls, x => { l.tls = x; })), field('Zertifikat', sel([['', '–'], ...pkiNames().map(n => [n, n])], l.pki, x => { l.pki = x; })),
        field('Anmeldung', sel([['', 'keine'], ['auth', 'verlangen (auth)'], ['auth-optional', 'erlauben (auth-optional)']], l.auth, x => { l.auth = x; })),
        field('Adressfamilie', sel([['', 'beide'], ['inet4', 'nur IPv4'], ['inet6', 'nur IPv6']], l.family, x => { l.family = x; })), cmt(l)),
        { add: 'Schnittstelle hinzufügen', max: 30, fresh: () => ({ on: v.interfaces.includes('lo0') ? 'lo0' : v.interfaces[0], family: '', port: 0, tls: '', pki: '', auth: '', hostname: '', maskSrc: false, comment: '' }) });
      const METHODS = [['mbox', 'Mailbox (mbox, /var/mail)'], ['maildir', 'Maildir'], ['lmtp', 'An LMTP-Server'], ['relay', 'Weiterleiten (relay)'], ['expand-only', 'Nur Aliase auflösen'], ['forward-only', 'Nur .forward auswerten']];
      const actions_ = rows(c.actions, a => {
        if (a.raw) return rawRow(a);
        const extra = h('div', { class: 'cf-fields' });
        const draw = () => clear(extra,
          a.method === 'maildir' ? [field('Pfad', inp(a.path, x => { a.path = x.trim(); }, { class: 'mono', placeholder: '~/Maildir' })), chk('Spam in „Junk“ ablegen', a.junk, x => { a.junk = x; })] : null,
          a.method === 'lmtp' ? field('LMTP-Ziel', inp(a.path, x => { a.path = x.trim(); }, { class: 'mono', placeholder: 'localhost:24 oder /var/dovecot/lmtp' })) : null,
          ['mbox', 'maildir', 'lmtp', 'expand-only', 'forward-only'].includes(a.method) ? [tableSel('Aliase', a.alias, x => { a.alias = x; }), tableSel('Virtuelle Benutzer', a.virtual, x => { a.virtual = x; }), tableSel('Benutzerbasis', a.userbase, x => { a.userbase = x; })] : null,
          a.method === 'relay' ? [field('Relay-Adresse (leer = direkt zustellen)', inp(a.host, x => { a.host = x.trim(); }, { class: 'mono', placeholder: 'smtp+tls://mail.example.org:587' })),
            field('TLS', sel([['', 'wie in der Adresse'], ['tls', 'TLS verlangen'], ['no-verify', 'TLS ohne Zertifikatsprüfung']], a.tls, x => { a.tls = x; })), tableSel('Anmeldedaten', a.auth, x => { a.auth = x; }),
            field('HELO-Name', inp(a.helo, x => { a.helo = x.trim(); }, { class: 'mono', placeholder: 'mail.example.org' })), field('Absenderadresse', inp(a.src, x => { a.src = x.trim(); }, { class: 'mono', placeholder: 'optional' }))] : null);
        draw();
        return h('div', null, h('div', { class: 'cf-fields' }, field('Name', inp(a.name, x => { a.name = x.trim(); }, { class: 'mono', placeholder: 'local_mail' })), field('Zustellart', sel(METHODS, a.method, x => { a.method = x; draw(); })), cmt(a)), extra);
      }, { add: 'Aktion hinzufügen', max: 50, fresh: () => ({ name: '', method: 'mbox', path: '', junk: false, alias: tableNames().includes('aliases') ? 'aliases' : '', virtual: '', userbase: '', host: '', tls: '', auth: '', helo: '', src: '', comment: '' }) });
      const describe = m => m.raw ? '' : `Mail ${({ any: 'von jedem', local: 'von lokalen Benutzern', auth: 'von angemeldeten Benutzern', socket: 'über den Socket', src: `von ${m.fromArg || '…'}` })[m.from] || m.from} ${({ any: 'an beliebige Empfänger', local: 'an lokale Empfänger', domain: `an die Domain ${m.forArg || '…'}` })[m.for] || m.for} → ${m.action === 'reject' ? 'abweisen' : m.action || '…'}`;
      const matches = rows(c.matches, m => {
        if (m.raw) return rawRow(m, 'Diese Regel hat Optionen, die das Formular nicht kennt. Sie bleibt an ihrer Stelle; die Reihenfolge der Regeln ist wichtig.');
        const text = h('p', { class: 'hint rule-line' }, describe(m)), upd = () => { text.textContent = describe(m); };
        const fromArg = inp(m.fromArg, x => { m.fromArg = x.trim(); upd(); }, { class: 'mono', placeholder: '192.168.0.0/16', hidden: m.from !== 'src' });
        const forArg = inp(m.forArg, x => { m.forArg = x.trim(); upd(); }, { class: 'mono', placeholder: 'example.org', hidden: m.for !== 'domain' });
        return h('div', null, h('div', { class: 'cf-fields' },
          field('Absender', h('div', { class: 'stack' }, sel([['local', 'lokale Benutzer'], ['any', 'jeder'], ['auth', 'angemeldete Benutzer'], ['socket', 'über den Socket'], ['src', 'Adresse oder Netz …']], m.from, x => { m.from = x; fromArg.hidden = x !== 'src'; upd(); }), fromArg)),
          field('Empfänger', h('div', { class: 'stack' }, sel([['local', 'lokale Empfänger'], ['any', 'beliebige'], ['domain', 'eine Domain …']], m.for, x => { m.for = x; forArg.hidden = x !== 'domain'; upd(); }), forArg)),
          field('Aktion', (() => { const s = sel([['reject', 'abweisen (reject)'], ...[...new Set([m.action, ...actNames()].filter(x => x && x !== 'reject'))].map(n => [n, n])], m.action, x => { m.action = x; upd(); }); return s; })()), cmt(m)), text);
      }, { add: 'Regel hinzufügen', max: 200, fresh: () => ({ from: 'local', fromArg: '', for: 'local', forArg: '', action: actNames()[0] || 'reject', comment: '' }) });
      clear(body, status(v),
        h('div', { class: 'note' }, h('strong', null, 'So arbeitet smtpd: '), 'Jede Mail wird gegen die Regeln von oben nach unten geprüft, ', h('strong', null, 'die erste passende gewinnt'), '. Regeln, die jedem Absender die Weiterleitung an beliebige Empfänger erlauben (offenes Relay), lehnt maar ab.'),
        c.general.length ? h('div', { class: 'note' }, 'Unverändert bleiben (Makros, Optionen, Filter): ', h('code', { class: 'mono' }, c.general.filter(l => l.trim() && !l.trim().startsWith('#')).join(' · ') || '(nur Kommentare)')) : null,
        panel('Zertifikate', 'PKI', h('div', { class: 'panel-body' }, pkis)), panel('Tabellen', 'TABLE', h('div', { class: 'panel-body' }, tables)),
        panel('Annahme', 'LISTEN ON', h('div', { class: 'panel-body' }, listens)), panel('Zustellung', 'ACTION', h('div', { class: 'panel-body' }, actions_)),
        panel('Regeln', 'MATCH – ERSTE TREFFER GEWINNT', h('div', { class: 'panel-body' }, matches)),
        actions('smtpd', () => ({ conf: { tables: c.tables, pkis: c.pkis, listens: c.listens, actions: c.actions, matches: c.matches }, activate: st.on }), activateBox(v, st)));
    }

    drawTabs();
    await load();
  },
};
