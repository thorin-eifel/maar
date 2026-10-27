// Paketfilter-Regeln als Formular. Neue Regeln gelten zunächst auf Probe: Ohne Bestätigung
// innerhalb der Frist nimmt maar sie zurück, damit man sich nicht aussperrt.
import { h, clear, pageHead, panel, loading, toast, toastError, dialog, field, busy, pill } from '../ui.js';
import { get, put, post } from '../api.js';
import { icon } from '../icons.js';

const ACTIONS = [['pass', 'erlauben'], ['block', 'sperren']];
const DIRS = [['in', 'eingehend'], ['out', 'ausgehend'], ['', 'beide Richtungen']];
const PROTOS = [['', 'alle'], ['tcp', 'TCP'], ['udp', 'UDP'], ['icmp', 'ICMP'], ['icmp6', 'ICMPv6']];

// Spiegelt PFRule.line() des Servers für die Live-Anzeige.
export function ruleLine(r) {
  const p = [r.action];
  if (r.dir) p.push(r.dir);
  if (r.log) p.push('log');
  if (r.quick) p.push('quick');
  if (r.iface) p.push('on', r.iface);
  if (r.proto) p.push('proto', r.proto);
  if (!r.from && !r.to) p.push('all'); else { if (r.from) p.push('from', r.from); if (r.to) p.push('to', r.to); }
  if (r.ports) { const l = r.ports.split(','); p.push('port', l.length === 1 ? l[0] : `{ ${l.join(' ')} }`); }
  return p.join(' ');
}

const sel = (opts, value, on) => { const s = h('select', null, opts.map(([v, l]) => h('option', { value: v }, l))); s.value = value ?? ''; s.addEventListener('change', () => on(s.value)); return s; };
const chk = (label, value, on) => { const c = h('input', { type: 'checkbox', checked: !!value }); c.addEventListener('change', () => on(c.checked)); return h('label', { class: 'check' }, c, label); };

// Adresse: beliebig / dieser Host / Tabelle / eigene Adresse
function addrControl(label, value, tables, on) {
  const opts = [['', 'beliebig'], ['self', 'dieser Host (self)'], ...tables.map(t => [`<${t}>`, `Tabelle <${t}>`]), ['__custom', 'Adresse oder Netz …']];
  const known = opts.some(([v]) => v === value);
  const s = sel(opts, known ? value : '__custom', x => { custom.hidden = x !== '__custom'; if (x !== '__custom') on(x); else on(custom.value.trim()); });
  const custom = h('input', { class: 'mono', placeholder: '10.0.0.0/24', value: known ? '' : value, hidden: known, 'aria-label': `${label} (Adresse)` });
  custom.addEventListener('input', () => on(custom.value.trim()));
  return field(label, h('div', { class: 'stack' }, s, custom));
}

export function fwTabs(active) {
  const mk = (k, l, href, ic) => h('a', { class: 'tab', role: 'tab', href, 'aria-selected': String(k === active) }, icon(ic), l);
  return h('div', { class: 'tabs', role: 'tablist', 'aria-label': 'Paketfilter' }, mk('overview', 'Übersicht', '#/firewall', 'summary'), mk('rules', 'Regeln bearbeiten', '#/firewall/rules', 'sliders'));
}

export default {
  title: 'Paketfilter',
  async render(main) {
    const body = h('div', { class: 'content' }, loading());
    main.append(pageHead('NETZWERK', 'Paketfilter', 'Regeln als Formular: Interfaces und Tabellen kommen vom Host. Neue Regeln gelten auf Probe und werden ohne Bestätigung automatisch zurückgenommen.'), fwTabs('rules'), body);
    let timer = null;

    async function load() {
      clearInterval(timer);
      const v = await get('/api/conf/pf');
      draw(v);
    }

    function draw(v) {
      const c = JSON.parse(JSON.stringify(v.conf));
      const tableNames = () => [...v.tables, ...c.tables.map(t => t.name).filter(Boolean)];
      const rulesBox = h('div', { class: 'cf-rows' });
      const tablesBox = h('div', { class: 'cf-rows' });

      function drawTables() {
        tablesBox.replaceChildren(...c.tables.map((t, i) => {
          const ta = h('textarea', { rows: 3, class: 'mono', spellcheck: 'false', placeholder: '203.0.113.7\n198.51.100.0/24', 'aria-label': `Adressen von ${t.name}` }, (t.addrs || []).join('\n'));
          ta.addEventListener('input', () => { t.addrs = ta.value.split(/[\s,]+/).filter(Boolean); });
          const name = h('input', { value: t.name, class: 'mono', pattern: '[A-Za-z][A-Za-z0-9_]{0,30}', 'aria-label': 'Tabellenname' });
          name.addEventListener('change', () => { t.name = name.value.trim(); drawRules(); });
          return h('div', { class: 'cf-row' }, h('div', { class: 'cf-fields' }, field('Tabelle', name), field('Adressen und Netze (eine pro Zeile)', ta)),
            h('div', { class: 'toolbar' }, h('button', { class: 'mini-btn danger', type: 'button', onclick: () => { c.tables.splice(i, 1); drawTables(); drawRules(); } }, 'Entfernen')));
        }), h('div', null, h('button', { class: 'mini-btn', type: 'button', onclick: () => { c.tables.push({ name: '', addrs: [] }); drawTables(); } }, 'Tabelle hinzufügen')));
      }

      function drawRules() {
        rulesBox.replaceChildren(...c.rules.map((r, i) => {
          const line = h('code', { class: 'mono rule-line' });
          const upd = () => { line.textContent = (r.disabled ? '(aus) ' : '') + ruleLine(r); };
          const portIn = h('input', { class: 'mono', value: r.ports, placeholder: '443 · 80,443 · 8000:8080', disabled: r.proto !== 'tcp' && r.proto !== 'udp', 'aria-label': 'Zielports' });
          portIn.addEventListener('input', () => { r.ports = portIn.value.trim(); upd(); });
          const svc = h('select', { 'aria-label': 'Dienst', disabled: portIn.disabled }, h('option', { value: '' }, 'Dienst wählen …'), v.services.map(s => h('option', { value: s.port }, `${s.name} (${s.port}/${s.proto})`)));
          svc.addEventListener('change', () => { if (svc.value) { r.ports = r.ports ? `${r.ports},${svc.value}` : svc.value; portIn.value = r.ports; svc.value = ''; upd(); } });
          const proto = sel(PROTOS, r.proto, x => { r.proto = x; if (x !== 'tcp' && x !== 'udp') { r.ports = ''; portIn.value = ''; } portIn.disabled = svc.disabled = x !== 'tcp' && x !== 'udp'; upd(); });
          const move = d => () => { const j = i + d; if (j < 0 || j >= c.rules.length) return; [c.rules[i], c.rules[j]] = [c.rules[j], c.rules[i]]; drawRules(); };
          upd();
          return h('div', { class: 'cf-row pf-rule' + (r.disabled ? ' off' : '') },
            h('div', { class: 'cf-fields' },
              field('Aktion', sel(ACTIONS, r.action, x => { r.action = x; upd(); })), field('Richtung', sel(DIRS, r.dir, x => { r.dir = x; upd(); })),
              field('Interface', sel([['', 'alle'], ...v.interfaces.map(n => [n, n])], r.iface, x => { r.iface = x; upd(); })), field('Protokoll', proto),
              addrControl('Von', r.from, tableNames(), x => { r.from = x; upd(); }), addrControl('Nach', r.to, tableNames(), x => { r.to = x; upd(); }),
              field('Zielports', h('div', { class: 'stack' }, portIn, svc)),
              h('div', { class: 'stack' }, chk('sofort entscheiden (quick)', r.quick, x => { r.quick = x; upd(); }), chk('protokollieren (log)', r.log, x => { r.log = x; upd(); }), chk('aktiv', !r.disabled, x => { r.disabled = !x; drawRules(); })),
              field('Beschreibung', h('input', { value: r.description, maxlength: 80, placeholder: 'wozu die Regel dient', 'aria-label': 'Beschreibung', oninput: e => { r.description = e.target.value; } }))),
            h('div', { class: 'stack' }, h('div', { class: 'toolbar' }, h('button', { class: 'mini-btn', type: 'button', disabled: i === 0, 'aria-label': 'nach oben', onclick: move(-1) }, '↑'), h('button', { class: 'mini-btn', type: 'button', disabled: i === c.rules.length - 1, 'aria-label': 'nach unten', onclick: move(1) }, '↓'),
              h('button', { class: 'mini-btn danger', type: 'button', onclick: () => { c.rules.splice(i, 1); drawRules(); } }, 'Entfernen')), line));
        }), h('div', null, h('button', { class: 'mini-btn', type: 'button', onclick: () => { c.rules.push({ action: 'pass', dir: 'in', iface: '', proto: 'tcp', from: '', to: 'self', ports: '', quick: false, log: false, description: '', disabled: false }); drawRules(); } }, 'Regel hinzufügen')));
      }
      drawTables(); drawRules();

      const payload = () => ({ tables: c.tables.filter(t => t.name).map(t => ({ name: t.name, addrs: t.addrs || [] })), rules: c.rules });
      const preview = h('button', { class: 'secondary', type: 'button' }, 'Vorschau');
      const reset = h('button', { class: 'secondary', type: 'button' }, 'Zurücksetzen');
      const apply = h('button', { class: 'cta', type: 'button' }, 'Auf Probe anwenden');
      preview.addEventListener('click', () => busy(preview, async () => {
        const r = await put('/api/conf/pf?preview=1', payload());
        const close = h('button', { class: 'secondary', type: 'button' }, 'Schließen');
        const d = dialog({ title: 'So würde pf.conf aussehen', meta: 'PF.CONF', wide: true, body: h('div', null, r.warnings?.length ? h('div', { class: 'note warn' }, r.warnings.map(w => h('div', null, w))) : null, h('pre', { class: 'codeblock' }, r.content)), actions: [close] });
        close.addEventListener('click', () => d.close());
      }).catch(toastError));
      reset.addEventListener('click', () => load().catch(toastError));
      apply.addEventListener('click', () => busy(apply, async () => {
        const r = await put('/api/conf/pf', payload());
        if (r.warnings?.length) toast('Bitte beachten', r.warnings.join('\n'));
        await load();
      }).catch(toastError));

      // Bestätigungsleiste
      const banner = h('div');
      if (v.pending) {
        let left = v.pending.seconds;
        const ok = h('button', { class: 'cta', type: 'button' }, 'Regeln bestätigen'), undo = h('button', { class: 'secondary', type: 'button' }, 'Zurücknehmen');
        const count = h('strong', null, `${left} s`);
        ok.addEventListener('click', () => busy(ok, async () => { await post('/api/conf/pf/confirm'); toast('Die neuen Regeln sind dauerhaft.'); await load(); }).catch(toastError));
        undo.addEventListener('click', () => busy(undo, async () => { await post('/api/conf/pf/revert'); toast('Zurückgenommen.'); await load(); }).catch(toastError));
        clear(banner, h('div', { class: 'note bad pf-pending', role: 'alert' }, h('div', null, h('strong', null, 'Neue Regeln sind aktiv, aber noch nicht bestätigt. '), 'Funktioniert alles (diese Seite lädt), bestätige sie. Ohne Bestätigung werden sie in ', count, ' automatisch zurückgenommen.'), h('div', { class: 'toolbar' }, ok, undo)));
        timer = setInterval(() => { left -= 1; count.textContent = `${Math.max(left, 0)} s`; if (left < -2) { clearInterval(timer); load().catch(() => {}); } }, 1000);
      }

      clear(body, banner,
        !v.enabled ? h('div', { class: 'note warn' }, 'Der Paketfilter ist ausgeschaltet. Regeln werden geladen, aber nicht angewendet.') : null,
        h('div', { class: 'note' }, h('strong', null, 'So wirken die Regeln: '), 'Sie stehen in pf.conf hinter den bestehenden Regeln und vor dem Anker für die VM-Netze. Bei mehreren passenden Regeln gilt ', h('strong', null, 'die letzte'), ' – außer eine passende Regel ist „sofort entscheiden (quick)“.'),
        h('details', { class: 'expert' }, h('summary', null, `Bestehende Regeln in pf.conf (${v.existing.length} Zeilen, nur lesen)`), h('pre', { class: 'codeblock' }, v.existing.join('\n'))),
        panel('Tabellen', 'TABLE <NAME> PERSIST', h('div', { class: 'panel-body' }, tablesBox, h('p', { class: 'hint' }, `Schon vorhanden und in Regeln wählbar: ${v.tables.map(t => `<${t}>`).join(' ') || 'keine'}`))),
        panel('Regeln', `${c.rules.length} REGELN`, h('div', { class: 'panel-body' }, rulesBox)),
        h('div', { class: 'toolbar' }, preview, reset, apply));
    }

    await load();
    return () => clearInterval(timer);
  },
};
