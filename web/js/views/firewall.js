import { h, clear, pageHead, panel, table, pill, loading, toast, toastError, confirm, formDialog, field, busy } from '../ui.js';
import { get, post } from '../api.js';
import { fwTabs } from './pfrules.js';

export default {
  title: 'Paketfilter',
  async render(main, parts) {
    if (parts && parts[0] === 'rules') return (await import('./pfrules.js')).default.render(main);
    const body = h('div', { class: 'content' }, loading());
    const editBtn = h('a', { class: 'cta', href: '#/files?path=/etc/pf.conf' }, 'pf.conf als Text (Experte)');
    main.append(pageHead('11 / NETZWERK', 'Paketfilter', 'pf(4) über pfctl(8): Status, geladene Regeln, Tabellen und aktive Verbindungen. Änderungen am Regelsatz werden vor dem Laden mit pfctl -n geprüft.', editBtn), fwTabs('overview'), body);

    const tableBox = h('div', { class: 'panel-body flush' }, h('div', { class: 'empty' }, 'Tabelle wählen.'));
    let current = null;

    async function act(a, btn, conf) {
      if (conf && !await confirm(conf)) return;
      await busy(btn, async () => { const r = await post(`/api/pf/${a}`); toast(r.message, r.output); await load(); });
    }

    async function showTable(name) {
      current = name;
      const addrs = await get(`/api/pf/tables/${encodeURIComponent(name)}`);
      const add = h('button', { class: 'mini-btn', type: 'button' }, 'Adresse hinzufügen');
      add.addEventListener('click', () => {
        const a = h('input', { required: true, placeholder: '203.0.113.7 oder 10.0.0.0/24', class: 'mono' });
        formDialog({ title: `Zu <${name}> hinzufügen`, meta: 'PFCTL -T ADD', submit: 'Hinzufügen', fields: [h('div', { class: 'wide' }, field('Adresse oder Netz', a, 'Gilt bis zum nächsten Laden des Regelsatzes, außer die Tabelle ist persist und wird nicht neu befüllt.'))],
          onSubmit: async () => { await post(`/api/pf/tables/${encodeURIComponent(name)}`, { op: 'add', addr: a.value.trim() }); toast('Hinzugefügt.'); await showTable(name); } });
      });
      clear(tableBox,
        h('div', { class: 'panel-head' }, h('div', null, h('span', { class: 'meta' }, `${addrs.length} EINTRÄGE`), h('h2', null, `<${name}>`)), add),
        table(['Adresse', ''], addrs.map(a => h('tr', null, h('td', { class: 'mono' }, a), h('td', { class: 'actions' },
          h('button', { class: 'mini-btn danger', type: 'button', onclick: () => post(`/api/pf/tables/${encodeURIComponent(name)}`, { op: 'delete', addr: a.replace(/^!/, '') }).then(() => showTable(name)).catch(toastError) }, 'Entfernen')))), 'Tabelle ist leer.'));
    }

    async function load() {
      const pf = await get('/api/pf');
      const btn = (label, a, cls, conf) => { const b = h('button', { class: cls, type: 'button' }, label); b.addEventListener('click', () => act(a, b, conf).catch(toastError)); return b; };
      const statesBox = h('div', { class: 'panel-body flush' }, h('div', { class: 'empty' }, h('button', { class: 'mini-btn', type: 'button', onclick: async () => {
        const s = await get('/api/pf/states');
        clear(statesBox, h('pre', { class: 'codeblock' }, s.join('\n') || 'Keine States.'));
      } }, 'States laden')));
      const tables = pf.tables.map(t => h('button', { class: 'mini-btn', type: 'button', 'aria-pressed': String(t === current), onclick: () => showTable(t).catch(toastError) }, `<${t}>`));
      clear(body,
        h('div', { class: 'metrics' },
          h('div', { class: 'metric' }, h('span', { class: 'meta' }, 'Status'), h('strong', null, pf.enabled ? 'Aktiv' : 'Deaktiviert'), h('span', { class: 'sub' }, pf.enabled ? pill('ok', 'filtert') : pill('bad', 'kein Schutz'))),
          h('div', { class: 'metric' }, h('span', { class: 'meta' }, 'Regeln'), h('strong', null, pf.rules.length), h('span', { class: 'sub' }, 'nach Makro-Expansion')),
          h('div', { class: 'metric' }, h('span', { class: 'meta' }, 'States'), h('strong', null, pf.states), h('span', { class: 'sub' }, 'aktive Verbindungen')),
          h('div', { class: 'metric' }, h('span', { class: 'meta' }, 'Tabellen'), h('strong', null, pf.tables.length), h('span', { class: 'sub' }, pf.tables.map(t => `<${t}>`).join(' ')))),
        panel('Steuerung', 'PFCTL', h('div', { class: 'panel-body toolbar' },
          btn('Regelsatz neu laden', 'reload', 'cta'),
          pf.enabled
            ? btn('pf deaktivieren', 'disable', 'secondary danger', { title: 'Paketfilter deaktivieren?', text: 'Der Host ist danach ungefiltert erreichbar, NAT für VMs funktioniert nicht mehr.', ok: 'Deaktivieren', danger: true, typeToConfirm: 'pfctl -d' })
            : btn('pf aktivieren', 'enable', 'secondary'),
          btn('States leeren', 'flush-states', 'secondary', { title: 'Alle States verwerfen?', text: 'Bestehende Verbindungen (auch diese Sitzung) müssen neu aufgebaut werden.', ok: 'Leeren', danger: true }))),
        panel('Geladene Regeln', 'PFCTL -SR', h('div', { class: 'panel-body flush' }, h('pre', { class: 'codeblock' }, pf.rules.map((r, i) => `@${i} ${r}`).join('\n') || 'Keine Regeln geladen.'))),
        h('div', { class: 'grid-2' },
          panel('Tabellen', 'PFCTL -T', tableBox, ...tables),
          panel('Aktive Verbindungen', 'PFCTL -SS', statesBox)),
        panel('Statistik', 'PFCTL -SI', h('div', { class: 'panel-body flush' }, h('pre', { class: 'codeblock' }, pf.info))));
      if (current) showTable(current).catch(() => {});
    }
    await load();
  },
};
