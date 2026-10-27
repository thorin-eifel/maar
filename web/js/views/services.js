import { h, clear, pageHead, panel, table, pill, loading, toast, toastError, formDialog, field, busy, confirm } from '../ui.js';
import { get, post, put } from '../api.js';

export default {
  title: 'Systemdienste',
  async render(main) {
    const body = h('div', { class: 'content' }, loading());
    const refreshBtn = h('button', { class: 'secondary', type: 'button' }, 'Aktualisieren');
    main.append(pageHead('DIENSTE', 'Systemdienste', 'rc.d(8)-Dienste über rcctl(8) starten, stoppen, beim Systemstart aktivieren und mit Flags versehen.', refreshBtn), body);

    let list = [], filter = 'on', term = '';
    const search = h('input', { class: 'input', type: 'search', placeholder: 'Dienst suchen', 'aria-label': 'Dienst suchen' });
    const seg = h('div', { class: 'segment', role: 'group', 'aria-label': 'Filter' });
    const filters = [['on', 'Aktiviert'], ['running', 'Laufend'], ['failed', 'Ausgefallen'], ['all', 'Alle']];
    const tableBox = h('div', { class: 'panel-body flush' });

    async function load() {
      list = await get('/api/services');
      draw();
    }

    async function action(s, a, btn) {
      if ((a === 'stop' || a === 'disable') && ['sshd', 'pf'].includes(s.name)) {
        if (!await confirm({ title: `${s.name} wirklich ${a === 'stop' ? 'stoppen' : 'deaktivieren'}?`, text: 'Damit kann der Fernzugriff auf diesen Host verloren gehen.', ok: 'Fortfahren', danger: true })) return;
      }
      await busy(btn, async () => {
        const r = await post(`/api/services/${s.name}/${a}`);
        toast(r.message, r.output);
        await load();
      });
    }

    async function editFlags(s) {
      const d = await get(`/api/services/${s.name}`);
      const flags = h('input', { value: d.flags === 'NO' ? '' : d.flags, spellcheck: 'false', autocomplete: 'off', class: 'mono' });
      formDialog({
        title: `Flags für ${s.name}`, meta: 'RCCTL SET', submit: 'Speichern',
        text: 'Setzt ' + s.name + '_flags in /etc/rc.conf.local und aktiviert den Dienst. Leer = Standardflags.',
        fields: [h('div', { class: 'wide' }, field('Flags', flags)),
          h('dl', { class: 'kv wide' }, h('dt', null, 'Klasse'), h('dd', null, d.class || '–'), h('dt', null, 'Benutzer'), h('dd', null, d.user || '–'), h('dt', null, 'Timeout'), h('dd', null, d.timeout ? d.timeout + ' s' : '–'))],
        onSubmit: async () => { const r = await put(`/api/services/${s.name}/flags`, { flags: flags.value }); toast(r.message); await load(); },
      });
    }

    function draw() {
      clear(seg, filters.map(([k, t]) => {
        const n = list.filter(s => k === 'all' || (k === 'on' && s.enabled) || (k === 'running' && s.running) || (k === 'failed' && s.failed)).length;
        const b = h('button', { type: 'button', 'aria-pressed': String(filter === k) }, `${t} (${n})`);
        b.addEventListener('click', () => { filter = k; draw(); });
        return b;
      }));
      const rows = list
        .filter(s => filter === 'all' || (filter === 'on' && s.enabled) || (filter === 'running' && s.running) || (filter === 'failed' && s.failed))
        .filter(s => !term || s.name.includes(term))
        .map(s => {
          const btn = (label, a, danger) => {
            const b = h('button', { class: 'mini-btn' + (danger ? ' danger' : ''), type: 'button' }, label);
            b.addEventListener('click', () => action(s, a, b).catch(toastError));
            return b;
          };
          const status = s.special ? (s.enabled ? pill('ok', 'aktiv') : pill('off', 'aus'))
            : s.failed ? pill('bad', 'ausgefallen') : s.running ? pill('ok', 'läuft') : pill('off', 'gestoppt');
          const acts = s.special ? [] : [
            s.running ? btn('Neu starten', 'restart') : btn('Starten', 'start'),
            s.running ? btn('Neu laden', 'reload') : null,
            s.running ? btn('Stoppen', 'stop', true) : null,
          ];
          const flagsBtn = s.special ? null : h('button', { class: 'mini-btn', type: 'button', onclick: () => editFlags(s).catch(toastError) }, 'Flags');
          return h('tr', null,
            h('td', { class: 'name mono' }, s.name),
            h('td', null, status),
            h('td', null, s.enabled ? pill('info', 'beim Start') : pill('off', 'manuell')),
            h('td', { class: 'actions' }, acts, flagsBtn, s.enabled ? btn('Deaktivieren', 'disable') : btn('Aktivieren', 'enable')));
        });
      clear(tableBox, table(['Dienst', 'Zustand', 'Autostart', ''], rows, 'Keine Dienste für diesen Filter.'));
    }

    search.addEventListener('input', () => { term = search.value.trim().toLowerCase(); draw(); });
    refreshBtn.addEventListener('click', () => busy(refreshBtn, load).catch(toastError));
    clear(body,
      h('div', { class: 'toolbar' }, seg, h('div', { class: 'grow search' }, search)),
      panel('rc.d-Dienste', 'RCCTL LS', tableBox));
    await load();
  },
};
