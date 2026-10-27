// maar – Einstieg, Anmeldung, Navigation.
import { h, clear, toastError } from './ui.js';
import { session, get, post, setUnauthorizedHandler } from './api.js';
import { initJobs, toggleJobs } from './jobs.js';
import { createInventory } from './inventory.js';

const NAV = [
  ['Übersicht', [['01', 'Übersicht', '#/', 'dashboard']]],
  ['Inventar', [['02', 'Hosts', '#/hosts', 'hosts'], ['03', 'Virtuelle Maschinen', '#/vms', 'vms'], ['04', 'Templates', '#/templates', 'templates'], ['05', 'Konsolen', '#/console', 'console']]],
  ['Speicher', [['06', 'Geteilter Speicher', '#/san', 'san'], ['07', 'Lokale Datenträger', '#/storage', 'storage'], ['08', 'Backup', '#/backup', 'backup']]],
  ['Netzwerk', [['09', 'VM-Netze', '#/vnet', 'vnet'], ['10', 'Adapter & Routen', '#/network', 'network'], ['11', 'Paketfilter', '#/firewall', 'firewall']]],
  ['Überwachung', [['12', 'Leistung & Alarme', '#/performance', 'performance'], ['13', 'Ereignisse', '#/events', 'events'], ['14', 'Prozesse', '#/processes', 'processes'], ['15', 'Protokolle', '#/logs', 'logs']]],
  ['Host-Konfiguration', [['16', 'Dienste', '#/services', 'services'], ['17', 'Pakete & Updates', '#/packages', 'packages'], ['18', 'Benutzer & Gruppen', '#/users', 'users'], ['19', 'System', '#/system', 'system'], ['20', 'Zeit, Namen & SSH', '#/hostconf', 'hostconf'], ['21', 'Webserver & Mail', '#/webmail', 'webmail'], ['22', 'Konfigurationsdateien', '#/files', 'files']]],
  ['Verwaltung', [['23', 'Zugriffssteuerung', '#/access', 'access']]],
];

const app = document.getElementById('app');
let main = null, crumb = null, cleanup = null, inventory = null, pageEl = null, treeBtn = null;
// Seiten, neben denen der Inventarbaum steht (wie die Inventaransichten im vSphere Client).
const TREE_KEYS = new Set(['hosts', 'vms', 'vm', 'templates', 'console', 'datastore', 'portgroup', 'san', 'vnet']);
const treeShown = () => { try { return localStorage.getItem('maar.tree.hidden') !== '1'; } catch { return true; } };

function setTheme(btn) {
  const root = document.documentElement;
  const dark = root.dataset.theme === 'dark' || (!root.dataset.theme && matchMedia('(prefers-color-scheme: dark)').matches);
  btn.setAttribute('aria-pressed', String(dark));
  btn.textContent = dark ? 'Hell' : 'Dunkel';
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

function glassToggle() {
  const root = document.documentElement;
  const b = h('button', { class: 'mini-btn', type: 'button', 'aria-pressed': String(root.dataset.glass !== 'off') }, root.dataset.glass !== 'off' ? 'Glas an' : 'Glas aus');
  b.addEventListener('click', () => {
    const on = root.dataset.glass === 'off';
    root.dataset.glass = on ? 'on' : 'off';
    try { localStorage.setItem('maar.glass', root.dataset.glass); } catch { /* privat */ }
    b.setAttribute('aria-pressed', String(on));
    b.textContent = on ? 'Glas an' : 'Glas aus';
  });
  return b;
}

function shell() {
  const nav = h('nav', { class: 'side-nav-wrap', 'aria-label': 'Hauptnavigation' },
    NAV.map(([label, items]) => [
      h('p', { class: 'nav-label' }, label),
      h('div', { class: 'side-nav' }, items.filter(([, , , key]) => session.role === 'admin' || (key !== 'files' && key !== 'access')).map(([n, t, href, key]) => h('a', { href, 'data-key': key }, h('span', null, n), t))),
    ]));
  const logout = h('button', { class: 'mini-btn', type: 'button' }, 'Abmelden');
  logout.addEventListener('click', async () => {
    try { await post('/api/logout'); } catch { /* egal */ }
    session.user = null;
    location.hash = '#/';
    renderLogin();
  });
  const badge = h('span', { class: 'badge', hidden: true });
  const jobsBtn = h('button', { class: 'mini-btn', type: 'button', id: 'jobs-btn', 'aria-expanded': 'false', onclick: () => toggleJobs() }, 'Aufgaben', badge);
  crumb = h('span', { class: 'meta' });
  main = h('main', { id: 'inhalt', tabindex: '-1' });
  if (inventory) inventory.destroy();
  inventory = createInventory();
  treeBtn = h('button', { class: 'mini-btn', type: 'button', id: 'tree-btn', 'aria-pressed': String(treeShown()), hidden: true }, 'Inventar');
  treeBtn.addEventListener('click', () => { const on = !treeShown(); try { localStorage.setItem('maar.tree.hidden', on ? '0' : '1'); } catch { /* privat */ } applyTree(); });
  clear(app, h('div', { class: 'layout' },
    h('aside', { class: 'sidebar', 'aria-label': 'Navigation' },
      h('a', { href: '#/', 'aria-label': 'Zur Übersicht' }, h('img', { class: 'logo', src: '/img/ctw-logo.png', alt: 'CTW' })),
      h('p', { class: 'side-version' }, `OpenBSD Verwaltung · ${session.hostname}${session.demo ? ' · Demo' : ''}`),
      nav,
      h('div', { class: 'side-bottom' },
        h('div', null, 'Angemeldet als ', h('strong', null, session.user), session.role && session.role !== 'admin' ? ` (${session.role})` : ''),
        h('div', null, logout),
        h('div', { class: 'mono' }, `maar ${session.version}`))),
    pageEl = h('div', { class: 'page' },
      h('div', { class: 'topline' }, crumb, h('div', { class: 'top-actions' }, treeBtn, jobsBtn, themeToggle(), glassToggle())),
      main)));
  initJobs(badge);
}

function applyTree() {
  const key = routeKey;
  const on = TREE_KEYS.has(key);
  treeBtn.hidden = !on;
  const show = on && treeShown();
  treeBtn.setAttribute('aria-pressed', String(show));
  pageEl.classList.toggle('has-tree', show);
  if (show) {
    if (inventory.el.parentElement !== pageEl) pageEl.insertBefore(inventory.el, main);
    inventory.start();
    inventory.redraw();
  } else {
    inventory.el.remove();
    inventory.stop();
  }
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
  const navKey = { vm: 'vms', datastore: 'san', portgroup: 'vnet' }[key] || key;
  if (cleanup) { try { cleanup(); } catch { /* egal */ } cleanup = null; }
  for (const a of app.querySelectorAll('.side-nav a')) {
    if ((a.dataset.key === 'dashboard' && key === '') || a.dataset.key === navKey) a.setAttribute('aria-current', 'page');
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
