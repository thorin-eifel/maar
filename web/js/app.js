// maar – Einstieg, Anmeldung, Navigation.
import { h, clear, toastError } from './ui.js';
import { session, get, post, setUnauthorizedHandler } from './api.js';
import { initJobs, toggleJobs } from './jobs.js';
import { createInventory } from './inventory.js';
import { icon } from './icons.js';
import { startDecorating } from './decorate.js';
import { aboutDialog } from './about.js';

const NAV = [
  ['Übersicht', 'home', [['Übersicht', '#/', 'dashboard', 'home']]],
  ['Inventar', 'datacenter', [['Hosts', '#/hosts', 'hosts', 'server'], ['Virtuelle Maschinen', '#/vms', 'vms', 'vm'], ['Templates', '#/templates', 'templates', 'template'], ['Konsolen', '#/console', 'console', 'terminal']]],
  ['Speicher', 'database', [['Geteilter Speicher', '#/san', 'san', 'database'], ['Lokale Datenträger', '#/storage', 'storage', 'disk'], ['Backup', '#/backup', 'backup', 'backup']]],
  ['Netzwerk', 'network', [['VM-Netze', '#/vnet', 'vnet', 'switch'], ['Adapter & Routen', '#/network', 'network', 'ethernet'], ['Paketfilter', '#/firewall', 'firewall', 'firewall']]],
  ['Überwachung', 'activity', [['Leistung & Alarme', '#/performance', 'performance', 'chart'], ['Ereignisse', '#/events', 'events', 'events'], ['Prozesse', '#/processes', 'processes', 'cpu'], ['Protokolle', '#/logs', 'logs', 'logs']]],
  ['Host', 'server', [['Host & Betrieb', '#/system', 'system', 'gauge'], ['System-Einstellungen', '#/hostconf', 'hostconf', 'sliders'], ['Pakete & Updates', '#/packages', 'packages', 'package'], ['Benutzer & Gruppen', '#/users', 'users', 'users']]],
  ['Dienste', 'toggle', [['Systemdienste', '#/services', 'services', 'toggle'], ['Web, Mail & Lastverteilung', '#/webmail', 'webmail', 'globe']]],
  ['Verwaltung', 'shield-check', [['Zugriffssteuerung', '#/access', 'access', 'shield-check'], ['Konfigurationsdateien', '#/files', 'files', 'code']]],
];

const app = document.getElementById('app');
let main = null, crumb = null, cleanup = null, inventory = null, pageEl = null;
// Seiten, neben denen der Inventarbaum steht (wie die Inventaransichten im vSphere Client).

function setTheme(btn) {
  const root = document.documentElement;
  const dark = root.dataset.theme === 'dark' || (!root.dataset.theme && matchMedia('(prefers-color-scheme: dark)').matches);
  btn.setAttribute('aria-pressed', String(dark));
  btn.replaceChildren(icon(dark ? 'sun' : 'moon'), dark ? 'Hell' : 'Dunkel');
}

function themeToggle() {
  const b = h('button', { class: 'mini-btn', type: 'button' });
  setTheme(b);
  b.addEventListener('click', () => {
    const root = document.documentElement;
    const dark = b.getAttribute('aria-pressed') !== 'true';
    root.dataset.theme = dark ? 'dark' : 'light';
    try { localStorage.setItem('maar.theme', root.dataset.theme); } catch { /* privat */ }
    setTheme(b);
    window.dispatchEvent(new Event('themechange'));
  });
  return b;
}

// Eingeklappte Menügruppen merkt sich der Browser.
function loadCollapsed() {
  try { return new Set(JSON.parse(localStorage.getItem('maar.nav.collapsed')) || []); } catch { return new Set(); }
}

function setGroup(head, list, open, persist = true) {
  head.setAttribute('aria-expanded', String(open));
  list.hidden = !open;
  if (!persist) return;
  const set = loadCollapsed();
  open ? set.delete(head.dataset.group) : set.add(head.dataset.group);
  try { localStorage.setItem('maar.nav.collapsed', JSON.stringify([...set])); } catch { /* privat */ }
}

function shell() {
  if (inventory) inventory.destroy();
  inventory = createInventory();
  const collapsed = loadCollapsed();
  const nav = h('nav', { class: 'side-nav-wrap', 'aria-label': 'Hauptnavigation' },
    NAV.map(([label, gi, items], gix) => {
      const id = `nav-g${gix}`, open = !collapsed.has(label);
      // Im Inventar ersetzt der Baum (Hosts → VMs, Templates, Datastores …) die flachen Links;
      // diese bleiben nur für die schmale Ansicht und das Aufklappen der Gruppe.
      const inv = label === 'Inventar';
      const list = h('div', { class: 'side-nav', id, hidden: !open },
        inv ? inventory.els.inv : label === 'Speicher' ? inventory.els.ds : label === 'Netzwerk' ? inventory.els.pg : null,
        items.filter(([, , key]) => session.role === 'admin' || (key !== 'files' && key !== 'access')).map(([t, href, key, ic]) =>
          h('a', { href, 'data-key': key, 'aria-label': t, class: inv ? 'nav-flat' : null }, icon(ic, { cls: 'nav-icon' }), h('span', { class: 'nav-text' }, t))));
      const head = h('button', { class: 'nav-label', type: 'button', 'aria-expanded': String(open), 'aria-controls': id, 'data-group': label },
        icon(gi, { cls: 'label-icon' }), h('span', { class: 'nav-label-text' }, label), icon('chevron-down', { cls: 'chev' }));
      head.addEventListener('click', () => setGroup(head, list, head.getAttribute('aria-expanded') !== 'true'));
      return h('div', { class: 'nav-group' }, head, list);
    }));
  const logout = h('button', { class: 'mini-btn', type: 'button' }, icon('logout'), 'Abmelden');
  logout.addEventListener('click', async () => {
    try { await post('/api/logout'); } catch { /* egal */ }
    session.user = null;
    location.hash = '#/';
    renderLogin();
  });
  const badge = h('span', { class: 'badge', hidden: true });
  const jobsBtn = h('button', { class: 'mini-btn', type: 'button', id: 'jobs-btn', 'aria-expanded': 'false', onclick: () => toggleJobs() }, icon('tasks'), 'Aufgaben', badge);
  crumb = h('span', { class: 'meta' });
  main = h('main', { id: 'inhalt', tabindex: '-1' });
  clear(app, h('div', { class: 'layout' },
    h('aside', { class: 'sidebar', 'aria-label': 'Navigation' },
      h('a', { href: '#/', 'aria-label': 'Zur Übersicht' }, h('img', { class: 'logo', src: '/img/ctw-logo.png', alt: 'CTW' })),
      h('p', { class: 'side-version' }, `OpenBSD Verwaltung · ${session.hostname}${session.demo ? ' · Demo' : ''}`),
      nav,
      h('div', { class: 'side-bottom' },
        h('div', null, 'Angemeldet als ', h('strong', null, session.user), session.role && session.role !== 'admin' ? ` (${session.role})` : ''),
        h('div', null, logout),
        h('button', { class: 'version-link mono', type: 'button', title: 'Über maar', onclick: () => aboutDialog() }, `maar ${session.version}`))),
    pageEl = h('div', { class: 'page' },
      h('div', { class: 'topline' }, crumb, h('div', { class: 'top-actions' }, jobsBtn, themeToggle())),
      main)));
  initJobs(badge);
}

// Setzt das Symbol des Menüpunkts vor die Bereichszeile im Seitenkopf.
function headIcon(key) {
  const item = NAV.flatMap(g => g[2]).find(i => i[2] === (key || 'dashboard'));
  const idx = main.querySelector('.page-head .section-index');
  if (item && idx && !idx.querySelector('svg')) idx.prepend(icon(item[3], { cls: 'head-icon' }));
}

// Der Inventarbaum sitzt im Menü; hier nur Auswahl und Zustand aktualisieren.
function applyTree() {
  inventory.start();
  inventory.redraw();
}

const routes = {
  '': () => import('./views/dashboard.js'),
  performance: () => import('./views/performance.js'),
  vms: () => import('./views/vms.js'),
  vm: () => import('./views/vm.js'),
  templates: () => import('./views/templates.js'),
  backup: () => import('./views/backup.js'),
  console: () => import('./views/console.js'),
  services: () => import('./views/services.js'),
  packages: () => import('./views/packages.js'),
  users: () => import('./views/users.js'),
  processes: () => import('./views/processes.js'),
  storage: () => import('./views/storage.js'),
  system: () => import('./views/system.js'),
  vnet: () => import('./views/vnet.js'),
  network: () => import('./views/network.js'),
  iface: () => import('./views/iface.js'),
  firewall: () => import('./views/firewall.js'),
  files: () => import('./views/files.js'),
  logs: () => import('./views/logs.js'),
  access: () => import('./views/access.js'),
  hostconf: () => import('./views/hostconf.js'),
  webmail: () => import('./views/webmail.js'),
  events: () => import('./views/events.js'),
  hosts: () => import('./views/hosts.js'),
  san: () => import('./views/san.js'),
  datastore: () => import('./views/datastore.js'),
  portgroup: () => import('./views/portgroup.js'),
};

let routeSeq = 0, routeKey = '';

async function route() {
  if (!session.user) return;
  const [path, query] = location.hash.replace(/^#\/?/, '').split('?');
  const parts = path.split('/').map(decodeURIComponent);
  const key = routes[parts[0]] ? parts[0] : '';
  routeKey = key;
  const seq = ++routeSeq;
  const navKey = { vm: 'vms', datastore: 'san', portgroup: 'vnet', iface: 'network' }[key] || key;
  if (cleanup) { try { cleanup(); } catch { /* egal */ } cleanup = null; }
  for (const a of app.querySelectorAll('.side-nav a[data-key]')) {
    if ((a.dataset.key === 'dashboard' && key === '') || a.dataset.key === navKey) {
      // Datastore, Portgruppe und VM-Konsole markiert der Baum selbst; der Menüpunkt bleibt dann unmarkiert.
      if (key !== 'datastore' && key !== 'portgroup' && !(key === 'console' && parts[1] === 'vm')) a.setAttribute('aria-current', 'page');
      const list = a.closest('.side-nav'), head = list?.previousElementSibling;
      if (list?.hidden && head) setGroup(head, list, true, false);
    }
    else a.removeAttribute('aria-current');
  }
  const mod = (await routes[key]()).default;
  if (seq !== routeSeq) return;
  clear(main);
  window.scrollTo(0, 0);
  let done = null;
  try {
    done = await mod.render(main, parts.slice(1), new URLSearchParams(query || '')) || null;
  } catch (e) {
    toastError(e);
  }
  if (seq !== routeSeq) {
    // Inzwischen wurde weiternavigiert: diese Ansicht sofort aufräumen.
    if (done) done();
    return;
  }
  cleanup = done;
  applyTree();
  headIcon(navKey);
  crumb.textContent = `${session.hostname} / ${mod.title}`;
  document.title = `${mod.title} · ${session.hostname}`;
  if (seq === routeSeq && document.activeElement === document.body) main.focus({ preventScroll: true });
}

function renderLogin(msg) {
  const user = h('input', { name: 'user', autocomplete: 'username', required: true, autocapitalize: 'none', spellcheck: 'false' });
  const pass = h('input', { name: 'password', type: 'password', autocomplete: 'current-password', required: true });
  const err = h('p', { class: 'login-err', role: 'alert' }, msg || '');
  const btn = h('button', { class: 'cta', type: 'submit' }, 'Anmelden');
  const form = h('form', null,
    h('label', { class: 'field' }, 'Benutzer', user),
    h('label', { class: 'field' }, 'Passwort', pass),
    err, btn);
  form.addEventListener('submit', async e => {
    e.preventDefault();
    btn.disabled = true;
    err.textContent = '';
    try {
      Object.assign(session, await post('/api/login', { user: user.value.trim(), password: pass.value }));
      start();
    } catch (ex) {
      err.textContent = ex.message;
      pass.value = '';
      pass.focus();
    } finally {
      btn.disabled = false;
    }
  });
  clear(app, h('main', { class: 'login', id: 'inhalt' },
    h('section', { class: 'login-card glass', 'aria-labelledby': 'login-title' },
      h('img', { class: 'logo', src: '/img/ctw-logo.png', alt: 'CTW' }),
      h('div', { class: 'toolbar' }, h('span', { class: 'meta' }, 'OpenBSD · maar'), h('span', { class: 'signal', 'aria-hidden': 'true' }, h('i'), h('i'), h('i'))),
      h('h1', { id: 'login-title' }, 'Anmelden.'),
      h('p', null, 'Mit einem Systemkonto der Gruppe wheel. Die Prüfung erfolgt über BSD Auth auf dem Host.'),
      form)));
  document.title = 'Anmelden · OpenBSD Verwaltung';
  user.focus();
}

function start() {
  shell();
  route();
  startDecorating();
}

setUnauthorizedHandler(() => {
  if (!session.user) return;
  session.user = null;
  toggleJobs(false);
  renderLogin('Die Sitzung ist abgelaufen. Bitte erneut anmelden.');
});

window.addEventListener('hashchange', route);

(async () => {
  try {
    Object.assign(session, await get('/api/session'));
    start();
  } catch (e) {
    renderLogin();
    if (e.status === 401 && e.demo) {
      document.querySelector('.login-card p').textContent = 'Demo-Modus mit simuliertem OpenBSD-Host: Anmeldung mit demo / demo.';
    }
  }
})();
