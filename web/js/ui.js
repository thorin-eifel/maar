import { icon } from './icons.js';

// Kleine DOM-Helfer. Inhalte werden ausschließlich als Text gesetzt (kein innerHTML),
// damit Ausgaben des Systems nie als Markup interpretiert werden.

export function h(tag, attrs, ...children) {
  const el = document.createElement(tag);
  if (attrs) {
    for (const [k, v] of Object.entries(attrs)) {
      if (v === undefined || v === null || v === false) continue;
      if (k === 'class') el.className = v;
      else if (k === 'text') el.textContent = v;
      else if (k.startsWith('on') && typeof v === 'function') el.addEventListener(k.slice(2), v);
      else if (k === 'style' && typeof v === 'object') Object.assign(el.style, v);
      else if (k === 'value') el.value = v;
      else if (k === 'checked') el.checked = !!v;
      else if (v === true) el.setAttribute(k, '');
      else el.setAttribute(k, v);
    }
  }
  append(el, children);
  return el;
}

function append(el, children) {
  for (const c of children) {
    if (c === null || c === undefined || c === false) continue;
    if (Array.isArray(c)) append(el, c);
    else if (c instanceof Node) el.appendChild(c);
    else el.appendChild(document.createTextNode(String(c)));
  }
}

export function clear(el, ...children) {
  el.replaceChildren();
  append(el, children);
  return el;
}

// ---- Formatierung ----

export function bytes(n) {
  if (!n && n !== 0) return '–';
  const u = ['B', 'KB', 'MB', 'GB', 'TB'];
  let i = 0;
  while (n >= 1024 && i < u.length - 1) { n /= 1024; i++; }
  return (i === 0 ? n : n.toFixed(n < 10 ? 1 : 0)).toString().replace('.', ',') + ' ' + u[i];
}

export function duration(sec) {
  if (!sec) return '–';
  const d = Math.floor(sec / 86400), hh = Math.floor(sec % 86400 / 3600), m = Math.floor(sec % 3600 / 60);
  if (d) return `${d} T ${hh} Std`;
  if (hh) return `${hh} Std ${m} Min`;
  return `${m} Min`;
}

export function dateTime(v) {
  const d = typeof v === 'number' ? new Date(v * 1000) : new Date(v);
  if (isNaN(d)) return '–';
  return d.toLocaleString('de-DE', { dateStyle: 'medium', timeStyle: 'short' });
}

export function pct(n) { return (Math.round(n * 10) / 10).toString().replace('.', ',') + ' %'; }

// ---- Bausteine ----

const PILL_ICON = { ok: 'check', bad: 'x-circle', warn: 'warn', info: 'info', off: 'minus-circle' };
export function pill(kind, text) { return h('span', { class: 'pill ' + kind }, icon(PILL_ICON[kind] || 'info', { cls: 'pill-icon' }), text); }

export function vmStatePill(state) {
  const map = { running: ['ok', 'läuft'], stopped: ['off', 'gestoppt'], paused: ['info', 'pausiert'], stopping: ['warn', 'stoppt'], starting: ['warn', 'startet'], disabled: ['off', 'deaktiviert'] };
  const [k, t] = map[state] || ['warn', state];
  return pill(k, t);
}

export function pageHead(index, title, text, ...actions) {
  return h('header', { class: 'page-head' },
    h('div', null,
      h('span', { class: 'section-index' }, index),
      h('h1', { id: 'page-title' }, title),
      text ? h('p', null, text) : null),
    actions.length ? h('div', { class: 'head-actions' }, actions) : null);
}

// Symbol zu einer Überschrift: erste passende Regel gewinnt, spezifische vor allgemeinen.
const TITLE_ICONS = [
  [/Netzwerk & (Firewall|Paketfilter)|VM-Anschl.*Firewall|Firewall|Geladene Regeln|Paketfilter/i, 'firewall'],
  [/Portgruppe|Switch|VM-Netze/i, 'switch'], [/Schnittstelle|Interface|Adressen/i, 'ethernet'], [/Namensaufl|DNS|Resolver/i, 'globe'], [/Route/i, 'network'], [/Netzwerk|Netz\b/i, 'network'],
  [/Aktive Verbindungen|States/i, 'activity'], [/Statistik/i, 'chart'], [/Tabelle/i, 'layers'],
  [/iSCSI|Fibre|\bFC\b|HBA/i, 'hba'], [/NVMe/i, 'nvme'], [/Datastore/i, 'database'], [/Disk|LUN|Datenträger|Dateisystem|Image|Installationsmedien|Gerät/i, 'disk'],
  [/Wiederherstellen/i, 'undo'], [/Sicherungsziel/i, 'folder'], [/Sicherung|Backup/i, 'backup'], [/Snapshot/i, 'camera'],
  [/Template|Gäste/i, 'template'], [/Konsole/i, 'terminal'], [/VM-Hardware|Hardware/i, 'cpu'], [/Virtuelle Maschine|\bVMs?\b/i, 'vm'],
  [/CPU|Prozessor/i, 'cpu'], [/Speicher/i, 'memory'], [/Load|Last/i, 'gauge'], [/Prozess/i, 'tasks'],
  [/Ereignis|Meldung/i, 'events'], [/Benachrichtigung|Mail/i, 'mail'], [/Regeln|Schwellwert|Alarm/i, 'bell'], [/Verlauf|Zeitquelle|Zeitdienst|Geplante|Zeitplan|Laufzeit|Uhr/i, 'clock'], [/Zeit-Sensor|Sensor/i, 'gauge'],
  [/Constraint/i, 'balance'], [/Energie|Herunterfahren/i, 'power'], [/Kernel|Parameter|Steuerung|Allgemein|Konfiguration|Einstellung/i, 'sliders'],
  [/Token|Schlüssel|Key/i, 'key'], [/Rollen|Zugriff|Sicherheit|Authentif/i, 'shield-check'], [/Konten|Gruppen|Benutzer/i, 'users'],
  [/Systempatch|Wartung|Patch/i, 'wrench'], [/Paket installieren|Download/i, 'download'], [/Paket/i, 'package'], [/Dienst/i, 'toggle'],
  [/Erzeugte|Vorschau|Datei(en)?\b/i, 'code'], [/Verwendet von/i, 'link'], [/Hosts?\b|Peers?/i, 'datacenter'], [/System|Status/i, 'activity'], [/log\b|Protokoll/i, 'logs'], [/^[\w-]+(\.[\w-]+)+$/, 'globe'],
];
export const titleIcon = title => TITLE_ICONS.find(([re]) => re.test(String(title)))?.[1] || null;

export function panel(title, meta, body, ...tools) {
  return h('section', { class: 'panel' },
    h('div', { class: 'panel-head' },
      h('div', null, meta ? h('span', { class: 'meta' }, meta) : null, h('h2', null, titleIcon(title) ? icon(titleIcon(title), { cls: 'title-icon' }) : null, title)),
      tools.length ? h('div', { class: 'toolbar' }, tools) : null),
    body);
}

export function table(cols, rows, empty = 'Keine Einträge.') {
  if (!rows.length) return h('div', { class: 'empty' }, empty);
  return h('div', { class: 'table-wrap' },
    h('table', { class: 'table' },
      h('thead', null, h('tr', null, cols.map(c => h('th', { class: c.num ? 'num' : null, scope: 'col' }, c.label ?? c)))),
      h('tbody', null, rows)));
}

const METRIC_ICONS = [[/CPU|Prozessor|vCPU/i, 'cpu'], [/Speicher aktiv|RAM|Arbeitsspeicher/i, 'memory'], [/Kapazität|Belegt|Datastores?|iSCSI|Frei/i, 'database'], [/Laufzeit|Uptime/i, 'clock'], [/VMs|Maschinen/i, 'vm'], [/Hosts?\b/i, 'server'],
  [/Meldung|Alarm|Fehler/i, 'bell'], [/Snapshot/i, 'camera'], [/Netz|Adressen|Dienste/i, 'network'], [/FC|HBA/i, 'hba'], [/Disk|Platte|Image/i, 'disk'], [/Status/i, 'activity'], [/Regeln|Tabellen|States/i, 'firewall'], [/Speicher/i, 'memory'], [/Sicherung|Backup/i, 'backup']];

export function metric(meta, value, sub, barPct) {
  let bar = null;
  if (barPct !== undefined) {
    const cls = barPct > 90 ? 'bar crit' : barPct > 75 ? 'bar warn' : 'bar';
    const i = h('i');
    i.style.width = Math.max(0, Math.min(100, barPct)) + '%';
    bar = h('div', { class: cls, role: 'meter', 'aria-valuemin': '0', 'aria-valuemax': '100', 'aria-valuenow': String(Math.round(barPct)), 'aria-label': meta }, i);
  }
  const mi = METRIC_ICONS.find(([re]) => re.test(meta));
  return h('div', { class: 'metric' }, h('span', { class: 'meta' }, mi ? icon(mi[1], { cls: 'metric-icon' }) : null, meta), h('strong', null, value), sub ? h('span', { class: 'sub' }, sub) : null, bar);
}

export function field(label, input, hint) {
  return h('label', { class: 'field' }, label, input, hint ? h('span', { class: 'hint' }, hint) : null);
}

export function loading(text = 'Lädt …') { return h('div', { class: 'empty', role: 'status' }, text); }

// ---- Toasts ----

export function toast(title, detail, bad = false) {
  const box = document.getElementById('toasts');
  const t = h('div', { class: 'toast glass' + (bad ? ' bad' : ''), role: bad ? 'alert' : 'status' },
    h('strong', null, icon(bad ? 'x-circle' : 'check-circle', { cls: 'toast-icon' }), title), detail ? h('pre', null, detail) : null);
  box.appendChild(t);
  setTimeout(() => t.remove(), bad ? 12000 : 5000);
}

export function toastError(err) {
  toast(err.message || String(err), err.check || err.output || '', true);
}

// ---- Dialoge (nativ, mit Fokusrückgabe) ----

export function dialog({ title, meta, text, body, actions, wide }) {
  const prev = document.activeElement;
  const d = h('dialog', { class: 'glass-dialog' + (wide ? ' wide' : ''), 'aria-labelledby': 'dlg-title' },
    meta ? h('span', { class: 'meta' }, meta) : null,
    h('h3', { id: 'dlg-title' }, titleIcon(title) ? icon(titleIcon(title), { cls: 'title-icon' }) : null, title),
    text ? h('p', null, text) : null,
    body ? h('div', { class: 'dialog-body' }, body) : null,
    h('div', { class: 'dialog-actions' }, actions));
  d.addEventListener('close', () => { d.remove(); if (prev && prev.focus) prev.focus(); });
  document.body.appendChild(d);
  d.showModal();
  return d;
}

// confirm: optional muss ein Wort abgetippt werden (bei gefährlichen Aktionen).
export function confirm({ title, text, ok = 'Bestätigen', danger = false, typeToConfirm, meta }) {
  return new Promise(resolve => {
    let input = null;
    const okBtn = h('button', { class: danger ? 'cta' : 'secondary', type: 'button' }, ok);
    const cancel = h('button', { class: 'secondary', type: 'button' }, 'Abbrechen');
    let body = null;
    if (typeToConfirm) {
      input = h('input', { autocomplete: 'off', spellcheck: 'false' });
      okBtn.disabled = true;
      input.addEventListener('input', () => { okBtn.disabled = input.value !== typeToConfirm; });
      body = field(`Zur Bestätigung „${typeToConfirm}“ eingeben`, input);
    }
    let result = false;
    const d = dialog({ title, text, meta, body, actions: [cancel, okBtn] });
    cancel.addEventListener('click', () => d.close());
    okBtn.addEventListener('click', () => { result = true; d.close(); });
    d.addEventListener('close', () => resolve(result));
    (input || cancel).focus();
  });
}

// formDialog: baut ein Formular, ruft onSubmit(form) und schließt bei Erfolg.
export function formDialog({ title, meta, text, fields, submit = 'Speichern', onSubmit, wide, danger }) {
  const form = h('form', { class: 'form-grid' }, fields);
  const okBtn = h('button', { class: danger === false ? 'secondary' : 'cta', type: 'submit' }, submit);
  const cancel = h('button', { class: 'secondary', type: 'button' }, 'Abbrechen');
  const err = h('p', { class: 'login-err wide', role: 'alert' });
  form.appendChild(err);
  const d = dialog({ title, meta, text, body: form, actions: [cancel, okBtn], wide });
  cancel.addEventListener('click', () => d.close());
  okBtn.addEventListener('click', e => { e.preventDefault(); form.requestSubmit(); });
  form.addEventListener('submit', async e => {
    e.preventDefault();
    if (!form.reportValidity()) return;
    okBtn.disabled = true;
    err.textContent = '';
    try {
      await onSubmit(form);
      d.close();
    } catch (ex) {
      err.textContent = ex.message + (ex.check ? '\n' + ex.check : '');
    } finally {
      okBtn.disabled = false;
    }
  });
  const first = form.querySelector('input,select,textarea');
  if (first) first.focus();
  return d;
}

// busy führt eine Aktion aus und sperrt dabei den Knopf.
export async function busy(btn, fn) {
  btn.disabled = true;
  btn.setAttribute('aria-busy', 'true');
  try { return await fn(); } finally { btn.disabled = false; btn.removeAttribute('aria-busy'); }
}

export function debounce(fn, ms = 200) {
  let t;
  return (...a) => { clearTimeout(t); t = setTimeout(() => fn(...a), ms); };
}
