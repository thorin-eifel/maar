// Inventarbaum wie im vSphere Client: Hosts → Host → VMs, Templates, Datastores, Portgruppen.
import { h, clear } from './ui.js';
import { get, session } from './api.js';
import { onJobDone } from './jobs.js';

const enc = encodeURIComponent;
const KEY = 'maar.tree';
const stateText = { running: 'läuft', paused: 'pausiert', stopped: 'gestoppt' };

function loadOpen() {
  try { const raw = localStorage.getItem(KEY); return raw ? new Set(JSON.parse(raw)) : null; } catch { return null; }
}
function saveOpen(set) { try { localStorage.setItem(KEY, JSON.stringify([...set])); } catch { /* privat */ } }

export function createInventory() {
  const open = loadOpen() || new Set(['hosts', 'host:local', 'vms:local']);
  let hosts = [], filter = '';
  const search = h('input', { type: 'search', placeholder: 'Inventar durchsuchen …', 'aria-label': 'Inventar durchsuchen' });
  const list = h('ul', { class: 'tree', role: 'tree', 'aria-label': 'Inventar' });
  const el = h('aside', { class: 'inv-tree', 'aria-label': 'Inventar' }, h('div', { class: 'inv-head' }, h('span', { class: 'meta' }, 'Inventar')), search, list);
  search.addEventListener('input', () => { filter = search.value.trim().toLowerCase(); draw(); });

  const dot = state => h('i', { class: `dot ${state === 'running' ? 'ok' : state === 'paused' ? 'info' : state === 'offline' ? 'bad' : 'off'}`, title: stateText[state] || state, role: 'img', 'aria-label': stateText[state] || state });

  // Knoten: { id, label, href, icon?, children?, count?, hint? }
  function model() {
    const nodes = [];
    for (const x of hosts) {
      const s = x.summary, id = x.id, kids = [];
      if (s) {
        kids.push({ id: `vms:${id}`, label: 'Virtuelle Maschinen', count: s.vms.length, href: x.local ? '#/vms' : `#/hosts/${enc(id)}`, folder: true,
          children: s.vms.map(v => ({ id: `vm:${id}:${v.name}`, label: v.name, state: v.state, href: x.local ? `#/vm/${enc(v.name)}` : `#/hosts/${enc(id)}`, leaf: true })) });
        if (x.local) {
          if (s.templates?.length) kids.push({ id: 'tpl', label: 'Templates', count: s.templates.length, href: '#/templates', folder: true, children: s.templates.map(t => ({ id: `tpl:${t}`, label: t, href: '#/templates', leaf: true })) });
          if (s.datastores?.length) kids.push({ id: 'ds', label: 'Datastores', count: s.datastores.length, href: '#/san', folder: true, children: s.datastores.map(t => ({ id: `ds:${t}`, label: t, href: `#/datastore/${enc(t)}`, leaf: true })) });
          if (s.portGroups?.length) kids.push({ id: 'pg', label: 'Portgruppen', count: s.portGroups.length, href: '#/vnet', folder: true, children: s.portGroups.map(t => ({ id: `pg:${t}`, label: t, href: `#/portgroup/${enc(t)}`, leaf: true })) });
        }
      }
      nodes.push({ id: `host:${id}`, label: x.name || id, state: x.status === 'ok' ? 'running' : 'offline', host: true, hint: x.local ? 'dieser Host' : '', href: `#/hosts/${enc(id)}`, children: kids });
    }
    return [{ id: 'hosts', label: 'Hosts', count: hosts.length, href: '#/hosts', folder: true, children: nodes }];
  }

  const matches = n => n.label.toLowerCase().includes(filter);
  const subtree = n => matches(n) || (n.children || []).some(subtree);

  function currentId() {
    const [path, a, b] = location.hash.replace(/^#\/?/, '').split('?')[0].split('/').map(decodeURIComponent);
    if (path === 'vm') return `vm:local:${a}`;
    if (path === 'hosts') return a ? `host:${a}` : 'hosts';
    if (path === 'vms') return 'vms:local';
    if (path === 'templates') return 'tpl';
    if (path === 'datastore') return `ds:${a}`;
    if (path === 'portgroup') return `pg:${a}`;
    if (path === 'san') return 'ds';
    if (path === 'vnet') return 'pg';
    return '';
  }

  function row(n, depth, cur) {
    const kids = n.children && n.children.length ? n.children : null;
    const expanded = filter ? true : open.has(n.id);
    const li = h('li', { role: 'treeitem', 'aria-expanded': kids ? String(expanded) : null, 'aria-level': String(depth), 'aria-selected': String(n.id === cur) });
    const link = h('a', { href: n.href, class: 'tree-link', tabindex: '-1', 'aria-current': n.id === cur ? 'page' : null, style: { paddingLeft: `${8 + (depth - 1) * 16 + (kids ? 0 : 18)}px` } },
      n.state ? dot(n.state) : null, h('span', { class: 'tree-label' }, n.label), n.count !== undefined ? h('span', { class: 'tree-count' }, n.count) : null, n.hint ? h('span', { class: 'tree-hint' }, n.hint) : null);
    const line = h('div', { class: 'tree-row' });
    if (kids) {
      const tg = h('button', { class: 'tree-toggle', type: 'button', tabindex: '-1', 'aria-label': `${n.label} ${expanded ? 'einklappen' : 'ausklappen'}` }, expanded ? '▾' : '▸');
      tg.addEventListener('click', e => { e.preventDefault(); toggle(n.id); });
      line.append(tg);
      link.style.paddingLeft = `${4 + (depth - 1) * 16}px`;
    }
    line.append(link);
    li.append(line);
    if (kids && expanded) li.append(h('ul', { role: 'group' }, kids.filter(k => !filter || subtree(k)).map(k => row(k, depth + 1, cur))));
    return li;
  }

  function toggle(id) { open.has(id) ? open.delete(id) : open.add(id); saveOpen(open); draw(); }

  // Den Zweig des aktuellen Objekts aufklappen, damit man es im Baum sieht.
  function reveal(cur) {
    const parent = cur.startsWith('ds:') ? 'ds' : cur.startsWith('pg:') ? 'pg' : cur.startsWith('tpl:') ? 'tpl' : cur.startsWith('vm:') ? `vms:${cur.split(':')[1]}` : null;
    const host = cur.startsWith('vm:') ? `host:${cur.split(':')[1]}` : (parent ? 'host:local' : null);
    let changed = false;
    for (const id of ['hosts', host, parent]) if (id && !open.has(id)) { open.add(id); changed = true; }
    if (changed) saveOpen(open);
  }

  function draw() {
    const cur = currentId();
    if (cur) reveal(cur);
    const keep = document.activeElement?.closest?.('.inv-tree') ? document.activeElement.getAttribute('href') : null;
    const roots = model().filter(n => !filter || subtree(n));
    clear(list, roots.length ? roots.map(n => row(n, 1, cur)) : h('li', { class: 'empty' }, 'Keine Treffer.'));
    const links = [...list.querySelectorAll('.tree-link')];
    (links.find(a => a.getAttribute('aria-current')) || links[0])?.setAttribute('tabindex', '0');
    if (keep) links.find(a => a.getAttribute('href') === keep)?.focus();
  }

  // Pfeiltasten wie im WAI-ARIA-Baum: auf/ab bewegen, links/rechts klappen.
  list.addEventListener('keydown', e => {
    const links = [...list.querySelectorAll('.tree-link')];
    const i = links.indexOf(document.activeElement);
    if (i < 0) return;
    const li = links[i].closest('li');
    let to = null;
    if (e.key === 'ArrowDown') to = links[i + 1];
    else if (e.key === 'ArrowUp') to = links[i - 1];
    else if (e.key === 'Home') to = links[0];
    else if (e.key === 'End') to = links[links.length - 1];
    else if (e.key === 'ArrowRight' && li.hasAttribute('aria-expanded')) {
      if (li.getAttribute('aria-expanded') === 'false') { toggle(treeId(links[i])); return; }
      to = links[i + 1];
    } else if (e.key === 'ArrowLeft') {
      if (li.getAttribute('aria-expanded') === 'true') { toggle(treeId(links[i])); return; }
      to = li.parentElement.closest('li')?.querySelector('.tree-link');
    } else return;
    e.preventDefault();
    if (to) { links.forEach(a => a.setAttribute('tabindex', '-1')); to.setAttribute('tabindex', '0'); to.focus(); }
  });
  // Die Knoten-ID steckt nicht im DOM; über die Reihenfolge wieder auffindbar machen.
  function treeId(a) {
    const flat = []; (function walk(ns) { for (const n of ns) { flat.push(n); if (n.children && (filter || open.has(n.id))) walk(n.children.filter(k => !filter || subtree(k))); } })(model().filter(n => !filter || subtree(n)));
    return flat[[...list.querySelectorAll('.tree-link')].indexOf(a)]?.id;
  }

  async function refresh() {
    try { hosts = (await get('/api/hosts')).hosts; draw(); } catch { /* abgemeldet oder offline */ }
  }

  let timer = null;
  const off = onJobDone(() => setTimeout(refresh, 800));
  return {
    el, refresh, redraw: draw,
    start() { if (!timer) { refresh(); timer = setInterval(() => { if (!document.hidden) refresh(); }, 15000); } },
    stop() { clearInterval(timer); timer = null; },
    destroy() { clearInterval(timer); off(); },
  };
}
