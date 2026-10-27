// Hintergrundaufgaben (pkg_add, syspatch, Downloads …) mit Live-Ausgabe.
import { h, clear, dateTime, pill, toast } from './ui.js';
import { get, post } from './api.js';

let drawer = null, list = null, badge = null, timer = null;
const streams = new Map(); // job-id → { es, pre }
const listeners = new Set();

export function initJobs(badgeEl) {
  badge = badgeEl;
  refresh();
}

export function onJobDone(fn) { listeners.add(fn); return () => listeners.delete(fn); }

const stateText = { running: ['warn', 'läuft'], done: ['ok', 'fertig'], failed: ['bad', 'fehlgeschlagen'], canceled: ['off', 'abgebrochen'] };

async function refresh() {
  clearTimeout(timer);
  let jobs = [];
  try { jobs = await get('/api/jobs'); } catch { /* abgemeldet */ }
  const running = jobs.filter(j => j.state === 'running').length;
  if (badge) {
    badge.hidden = running === 0;
    badge.textContent = running;
  }
  if (drawer && list) render(jobs);
  if (running || drawer) timer = setTimeout(refresh, running ? 2000 : 5000);
}

let lastSig = '';

function render(jobs) {
  // Nur bei Zustandsänderungen neu zeichnen, damit der Tastaturfokus erhalten bleibt.
  const sig = jobs.map(j => j.id + j.state).join();
  if (sig === lastSig && list.childElementCount) return;
  lastSig = sig;
  if (!jobs.length) {
    clear(list, h('div', { class: 'empty' }, 'Noch keine Aufgaben. Paketinstallationen, Patches und Downloads erscheinen hier.'));
    return;
  }
  const keep = new Set(jobs.map(j => j.id));
  for (const [id, s] of streams) if (!keep.has(id)) { s.es && s.es.close(); streams.delete(id); }
  clear(list, jobs.map(j => {
    let s = streams.get(j.id);
    if (!s) {
      s = { pre: h('pre', { class: 'codeblock wrap', 'aria-live': 'off' }), open: j.state === 'running' };
      streams.set(j.id, s);
    }
    const [k, t] = stateText[j.state] || ['off', j.state];
    const toggle = h('button', { class: 'mini-btn', type: 'button', 'aria-expanded': String(s.open) }, s.open ? 'Ausgabe ausblenden' : 'Ausgabe');
    const cancel = j.state === 'running' ? h('button', { class: 'mini-btn danger', type: 'button', onclick: () => post(`/api/jobs/${j.id}/cancel`).then(refresh) }, 'Abbrechen') : null;
    const body = h('div', { hidden: !s.open }, s.pre);
    toggle.addEventListener('click', () => {
      s.open = !s.open;
      body.hidden = !s.open;
      toggle.textContent = s.open ? 'Ausgabe ausblenden' : 'Ausgabe';
      toggle.setAttribute('aria-expanded', String(s.open));
      if (s.open) stream(j.id);
    });
    if (s.open) stream(j.id);
    return h('article', { class: 'job' },
      h('div', { class: 'job-head' },
        h('div', null, h('strong', null, j.title), h('div', { class: 'sub' }, `${j.user} · ${dateTime(j.started)} · ${j.command}`)),
        h('div', { class: 'toolbar' }, pill(k, t), cancel, toggle)),
      body);
  }));
}

function stream(id) {
  const s = streams.get(id);
  if (!s || s.es || s.done) return;
  s.pre.textContent = '';
  const es = new EventSource(`/api/jobs/${id}/stream`);
  s.es = es;
  es.onmessage = e => {
    const atEnd = s.pre.scrollTop + s.pre.clientHeight >= s.pre.scrollHeight - 8;
    s.pre.textContent += JSON.parse(e.data);
    if (atEnd) s.pre.scrollTop = s.pre.scrollHeight;
  };
  es.addEventListener('end', e => {
    const [state, err] = JSON.parse(e.data).split('|');
    es.close();
    s.es = null;
    s.done = true;
    for (const fn of listeners) fn(id, state);
    refresh();
    if (state === 'failed') toast('Aufgabe fehlgeschlagen', err, true);
  });
  es.onerror = () => { es.close(); s.es = null; };
}

export function toggleJobs(force) {
  const open = force ?? !drawer;
  if (!open) {
    if (drawer) drawer.remove();
    for (const s of streams.values()) { s.es && s.es.close(); s.es = null; }
    drawer = list = null;
    document.getElementById('jobs-btn')?.setAttribute('aria-expanded', 'false');
    return;
  }
  if (!drawer) {
    lastSig = '';
    list = h('div', { class: 'jobs-list' });
    const close = h('button', { class: 'mini-btn', type: 'button', onclick: () => toggleJobs(false) }, 'Schließen');
    drawer = h('aside', { class: 'jobs-drawer glass', 'aria-label': 'Aufgaben', role: 'region' },
      h('header', null, h('h2', null, 'Aufgaben'), close), list);
    drawer.addEventListener('keydown', e => { if (e.key === 'Escape') toggleJobs(false); });
    document.body.appendChild(drawer);
    document.getElementById('jobs-btn')?.setAttribute('aria-expanded', 'true');
    close.focus();
  }
  refresh();
}

// watchJob öffnet die Aufgabenleiste mit der Ausgabe des neuen Jobs.
export function watchJob(id) {
  const s = { pre: h('pre', { class: 'codeblock wrap' }), open: true };
  streams.set(id, s);
  toggleJobs(true);
}
