// Ereignisprotokoll und Rollen (admin / operator / viewer, VM-Eingrenzung).
import { h, clear, pageHead, panel, table, pill, loading, toast, toastError, formDialog, field, busy, dateTime, debounce, dialog } from '../ui.js';
import { get, put, post, del, session, q } from '../api.js';

const ROLE = { admin: 'Administrator', operator: 'Operator', viewer: 'Betrachter' };
const ROLE_HELP = {
  admin: 'darf alles',
  operator: 'bedient die ihm zugewiesenen VMs: starten, herunterfahren, pausieren, Konsole, Snapshots anlegen und zurücksetzen',
  viewer: 'liest alles außer Konfigurationsdateien und Schlüsseln, ändert nichts',
};

function eventsPanel() {
  const rows = h('div', { class: 'panel-body flush' }, loading());
  const user = h('input', { placeholder: 'Benutzer', 'aria-label': 'Benutzer', size: 12 });
  const text = h('input', { type: 'search', placeholder: 'Suchen …', 'aria-label': 'Suchen' });
  const kind = h('select', { 'aria-label': 'Art' }, h('option', { value: '' }, 'alle Arten'), h('option', { value: 'aktion' }, 'Aktionen'), h('option', { value: 'anmeldung' }, 'Anmeldungen'), h('option', { value: 'auftrag' }, 'Aufträge'), h('option', { value: 'verweigert' }, 'Verweigert'));
  const result = h('select', { 'aria-label': 'Ergebnis' }, h('option', { value: '' }, 'alle Ergebnisse'), h('option', { value: 'ok' }, 'erfolgreich'), h('option', { value: 'fehler' }, 'Fehler'));
  async function load() {
    const d = await get('/api/events' + q({ user: user.value.trim(), q: text.value.trim(), kind: kind.value, result: result.value, limit: 300 }));
    const kinds = { aktion: ['info', 'Aktion'], anmeldung: ['off', 'Anmeldung'], auftrag: ['ok', 'Auftrag'], verweigert: ['warn', 'verweigert'] };
    clear(rows, table(['Zeit', 'Benutzer', 'Art', 'Ereignis', 'Ergebnis'], d.events.map(e => h('tr', null,
      h('td', { class: 'mono' }, dateTime(e.time)),
      h('td', null, h('span', { class: 'name' }, e.user), h('span', { class: 'sub mono' }, e.addr)),
      h('td', null, pill(...(kinds[e.kind] || ['off', e.kind]))),
      h('td', { class: 'mono' }, e.text),
      h('td', null, e.result === 'ok' ? pill('ok', 'ok') : [pill('bad', 'Fehler'), e.error ? h('span', { class: 'sub' }, e.error) : null]))), 'Keine Ereignisse für diesen Filter.'));
  }
  const reload = debounce(() => load().catch(toastError), 250);
  for (const el of [user, text]) el.addEventListener('input', reload);
  for (const el of [kind, result]) el.addEventListener('change', reload);
  load().catch(toastError);
  return { el: panel('Ereignisse', 'ANMELDUNGEN · AKTIONEN · ABLEHNUNGEN', rows, h('div', { class: 'toolbar' }, user, kind, result, text)), reload: load };
}

async function rolesPanel(onSaved) {
  const d = await get('/api/access');
  let cfg = d.config;
  const box = h('div', { class: 'panel-body flush' });
  const save = async next => { const r = await put('/api/access', next); toast(r.message); cfg = next; draw(); onSaved?.(); };

  function edit(g) {
    const isNew = !g;
    g = g || { user: '', role: 'operator', vms: [] };
    const user = h('input', { required: true, value: g.user, disabled: !isNew, list: 'members', pattern: '[a-z_][a-z0-9_\\-]{0,30}', class: 'mono' });
    const list = h('datalist', { id: 'members' }, d.members.map(m => h('option', { value: m })));
    const role = h('select', null, d.roles.map(r => h('option', { value: r, selected: r === g.role }, ROLE[r])));
    const checks = d.vms.map(v => { const i = h('input', { type: 'checkbox', checked: g.vms.includes(v), value: v }); return [i, h('label', { class: 'check' }, i, v)]; });
    const scope = h('div', { class: 'wide' }, field('Eingrenzung auf VMs', h('div', { class: 'checks' }, checks.map(c => c[1])), 'Nichts angekreuzt = alle VMs. Gilt für Operator und Betrachter.'));
    const hint = h('p', { class: 'hint wide' }, ROLE_HELP[role.value]);
    role.addEventListener('change', () => { hint.textContent = ROLE_HELP[role.value]; scope.hidden = role.value === 'admin'; });
    scope.hidden = role.value === 'admin';
    formDialog({
      title: isNew ? 'Zugriff vergeben' : `Zugriff von ${g.user}`, meta: 'ROLLE · VMS', submit: 'Speichern',
      fields: [field('Benutzer', user), list, field('Rolle', role), hint, scope],
      onSubmit: async () => {
        const entry = { user: user.value.trim(), role: role.value, vms: role.value === 'admin' ? [] : checks.filter(c => c[0].checked).map(c => c[0].value) };
        await save({ ...cfg, users: [...cfg.users.filter(u => u.user !== entry.user), entry] });
      },
    });
  }

  function draw() {
    const defSel = h('select', { 'aria-label': 'Standardrolle' }, d.roles.map(r => h('option', { value: r, selected: r === cfg.default }, ROLE[r])));
    defSel.addEventListener('change', () => save({ ...cfg, default: defSel.value }).catch(e => { toastError(e); defSel.value = cfg.default; }));
    const rows = cfg.users.map(g => h('tr', null,
      h('td', null, h('span', { class: 'name mono' }, g.user), g.user === session.user ? h('span', { class: 'sub' }, 'du') : null),
      h('td', null, pill(g.role === 'admin' ? 'ok' : g.role === 'operator' ? 'info' : 'off', ROLE[g.role])),
      h('td', null, g.role === 'admin' ? 'alles' : g.vms.length ? g.vms.map(v => h('span', { class: 'tag' }, v)) : 'alle VMs'),
      h('td', { class: 'actions' },
        h('button', { class: 'mini-btn', type: 'button', onclick: () => edit(g) }, 'Bearbeiten'),
        h('button', { class: 'mini-btn danger', type: 'button', onclick: () => save({ ...cfg, users: cfg.users.filter(u => u.user !== g.user) }).catch(toastError) }, 'Entfernen'))));
    clear(box,
      h('div', { class: 'panel-body' }, h('div', { class: 'form-grid' },
        field(`Standardrolle für Konten der Gruppe ${d.group || 'wheel'} ohne eigenen Eintrag`, defSel),
        h('p', { class: 'hint' }, 'Die Gruppe entscheidet, wer sich anmelden darf; die Rolle, was er danach darf. Du kannst dir selbst die Administratorrolle nicht nehmen.'))),
      table(['Benutzer', 'Rolle', 'VMs', ''], rows, 'Keine Einzelrechte – alle erhalten die Standardrolle.'));
  }
  draw();
  const add = h('button', { class: 'mini-btn', type: 'button', onclick: () => edit(null) }, 'Zugriff vergeben');
  return panel('Rollen & VM-Zugriff', 'ADMIN · OPERATOR · BETRACHTER', box, add);
}

async function tokensPanel() {
  const box = h('div', { class: 'panel-body flush' });
  async function load() {
    const list = await get('/api/tokens');
    clear(box, table(['Name', 'Angelegt', 'Zuletzt genutzt', ''], list.map(t => h('tr', null,
      h('td', null, h('span', { class: 'name mono' }, t.name)), h('td', null, dateTime(t.created)), h('td', null, t.lastUsed ? dateTime(t.lastUsed) : 'noch nie'),
      h('td', { class: 'actions' }, h('button', { class: 'mini-btn danger', type: 'button', onclick: () => del(`/api/tokens/${t.id}`).then(load).catch(toastError) }, 'Widerrufen')))), 'Keine Token. Lege eines an, damit ein zentraler maard diesen Host lesen kann.'));
  }
  const add = h('button', { class: 'mini-btn', type: 'button', onclick: () => {
    const name = h('input', { required: true, pattern: '[A-Za-z0-9][A-Za-z0-9._\\-]{0,40}', placeholder: 'zentrale' });
    formDialog({ title: 'API-Token anlegen', meta: 'NUR LESEN', submit: 'Anlegen', text: 'Das Token erlaubt nur die Zusammenfassung und Messwerte dieses Hosts. Du siehst es nur jetzt.',
      fields: [h('div', { class: 'wide' }, field('Name (z. B. der zentrale Host)', name))],
      onSubmit: async () => {
        const r = await post('/api/tokens', { name: name.value.trim() });
        const copy = h('button', { class: 'secondary', type: 'button' }, 'Kopieren');
        const close = h('button', { class: 'cta', type: 'button' }, 'Fertig');
        const d = dialog({ title: 'Token ' + r.name, meta: 'EINMALIG', text: 'Trage es auf dem zentralen Host unter „Hosts → Host verbinden“ ein. Danach ist es nicht mehr lesbar.', body: h('pre', { class: 'codeblock wrap' }, r.token), actions: [copy, close] });
        copy.addEventListener('click', () => navigator.clipboard?.writeText(r.token).then(() => toast('Kopiert.')).catch(() => {}));
        close.addEventListener('click', () => d.close());
        await load();
      } });
  } }, 'Token anlegen');
  await load();
  return panel('API-Token', 'MEHRERE HOSTS · NUR LESEN', box, add);
}

export default {
  title: 'Ereignisse & Rollen',
  async render(main) {
    const body = h('div', { class: 'content' });
    main.append(pageHead('18 / WERKZEUGE', 'Ereignisse & Rollen', `Wer hat wann was getan, und wer darf was. Du bist angemeldet als ${session.user} (${ROLE[session.role] || session.role}).`), body);
    const ev = eventsPanel();
    clear(body, ev.el);
    if (session.role === 'admin') body.append(await rolesPanel(() => ev.reload().catch(() => {})), await tokensPanel());
    const t = setInterval(() => ev.reload().catch(() => {}), 20000);
    return () => clearInterval(t);
  },
};
