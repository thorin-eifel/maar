// Inventarbaum wie im vSphere Client. Drei Teilbäume im Menü: Hosts → VMs/Templates (Inventar),
// Datastores (Speicher) und Portgruppen (Netzwerk); sie teilen Daten, Suche und Aufklappzustand.
import { h, clear } from './ui.js';
import { get, session } from './api.js';
import { onJobDone } from './jobs.js';
import { icon } from './icons.js';

const enc = encodeURIComponent;
const KEY = 'maar.tree';
const stateText = { running: 'läuft', paused: 'pausiert', stopped: 'gestoppt' };

function loadOpen() {
  try { const raw = localStorage.getItem(KEY); return raw ? new Set(JSON.parse(raw)) : null; } catch { return null; }
}
function saveOpen(set) { try { localStorage.setItem(KEY, JSON.stringify([...set])); } catch { /* privat */ } }

export function createInventory() {
  const open = loadOpen() || new Set(['hosts', 'host:local', 'vms:local', 'ds', 'pg']);
  let hosts = [], filter = '';
  const search = h('input', { type: 'search', placeholder: 'Inventar durchsuchen …', 'aria-label': 'Inventar durchsuchen' });
  const LABEL = { inv: 'Inventar', ds: 'Datastores', pg: 'Portgruppen' };
  const lists = {}, els = {};
  for (const kind of Object.keys(LABEL)) {
    lists[kind] = h('ul', { class: 'tree', role: 'tree', 'aria-label': LABEL[kind] });
    els[kind] = h('aside', { class: 'inv-tree', 'aria-label': LABEL[kind] }, kind === 'inv' ? search : null, lists[kind]);
  }
  const el = els.inv;
  search.addEventListener('input', () => { filter = search.value.trim().toLowerCase(); draw(); });

  const dot = state => h('i', { class: `dot ${state === 'running' ? 'ok' : state === 'paused' ? 'info' : state === 'offline' ? 'bad' : 'off'}`, title: stateText[state] || state, role: 'img', 'aria-label': stateText[state] || state });

  // Knoten: { id, label, href, icon?, children?, count?, hint? }
  function model(kind) {
    if (kind === 'ds' || kind === 'pg') {
      const s = hosts.find(x => x.local)?.summary;
      const names = (kind === 'ds' ? s?.datastores : s?.portGroups) || [];
      if (!names.length) return [];
      const ds = kind === 'ds';
      return [{ id: kind, icon: ds ? 'database' : 'switch', label: LABEL[kind], count: names.length, href: ds ? '#/san' : '#/vnet', folder: true,
        children: names.map(t => ({ id: `${kind}:${t}`, icon: ds ? 'database' : 'switch', label: t, href: ds ? `#/datastore/${enc(t)}` : `#/portgroup/${enc(t)}`, leaf: true })) }];
    }
    const nodes = [];
    for (const x of hosts) {
      const s = x.summary, id = x.id, kids = [];
      if (s) {
        kids.push({ id: `vms:${id}`, icon: 'vm', label: 'Virtuelle Maschinen', count: s.vms.length, href: x.local ? '#/vms' : `#/hosts/${enc(id)}`, folder: true,
          children: s.vms.map(v => ({ id: `vm:${id}:${v.name}`, icon: 'vm', label: v.name, state: v.state, href: x.local ? `#/vm/${enc(v.name)}` : `#/hosts/${enc(id)}`, leaf: true })) });
        if (x.local && s.templates?.length) kids.push({ id: 'tpl', icon: 'template', label: 'Templates', count: s.templates.length, href: '#/templates', folder: true, children: s.templates.map(t => ({ id: `tpl:${t}`, icon: 'template', label: t, href: '#/templates', leaf: true })) });
      }
      nodes.push({ id: `host:${id}`, icon: 'server', label: x.name || id, state: x.status === 'ok' ? 'running' : 'offline', host: true, hint: x.local ? 'dieser Host' : '', href: `#/hosts/${enc(id)}`, children: kids });
    }
    return [{ id: 'hosts', icon: 'datacenter', label: 'Hosts', count: hosts.length, href: '#/hosts', folder: true, children: nodes }];
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
      n.icon ? icon(n.icon, { cls: 'tree-icon' + (n.state ? ' st-' + n.state : '') }) : null, n.state ? dot(n.state) : null, h('span', { class: 'tree-label' }, n.label), n.count !== undefined ? h('span', { class: 'tree-count' }, n.count) : null, n.hint ? h('span', { class: 'tree-hint' }, n.hint) : null);
    const line = h('div', { class: 'tree-row' });
    if (kids) {
      const tg = h('button', { class: 'tree-toggle', type: 'button', tabindex: '-1', 'aria-label': `${n.label} ${expanded ? 'einklappen' : 'ausklappen'}` }, icon(expanded ? 'chevron-down' : 'chevron-right', { cls: 'tree-chev' }));
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
    const inv = cur.startsWith('vm:') || cur.startsWith('tpl');
    const host = cur.startsWith('vm:') ? `host:${cur.split(':')[1]}` : (cur.startsWith('tpl') ? 'host:local' : null);
    let changed = false;
    for (const id of inv ? ['hosts', host, parent] : [parent]) if (id && !open.has(id)) { open.add(id); changed = true; }
    if (changed) saveOpen(open);
  }

  function draw() {
    const cur = currentId();
    if (cur) reveal(cur);
    const active = document.activeElement;
    const keep = active?.closest?.('.inv-tree') ? active.getAttribute('href') : null;
    for (const kind of Object.keys(lists)) {
      const list = lists[kind];
      const roots = model(kind).filter(n => !filter || subtree(n));
      if (kind === 'inv') clear(list, roots.length ? roots.map(n => row(n, 1, cur)) : h('li', { class: 'empty' }, 'Keine Treffer.'));
      else clear(list, roots.map(n => row(n, 1, cur)));
      els[kind].hidden = kind !== 'inv' && !roots.length;
      const links = [...list.querySelectorAll('.tree-link')];
      (links.find(a => a.getAttribute('aria-current')) || links[0])?.setAttribute('tabindex', '0');
      if (keep) links.find(a => a.getAttribute('href') === keep)?.focus();
    }
  }

  // Pfeiltasten wie im WAI-ARIA-Baum: auf/ab bewegen, links/rechts klappen.
  for (const kind of Object.keys(lists)) lists[kind].addEventListener('keydown', e => {
    const list = lists[kind];
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
      if (li.getAttribute('aria-expanded') === 'false') { toggle(treeId(kind, links[i])); return; }
      to = links[i + 1];
    } else if (e.key === 'ArrowLeft') {
      if (li.getAttribute('aria-expanded') === 'true') { toggle(treeId(kind, links[i])); return; }
      to = li.parentElement.closest('li')?.querySelector('.tree-link');
    } else return;
    e.preventDefault();
    if (to) { links.forEach(a => a.setAttribute('tabindex', '-1')); to.setAttribute('tabindex', '0'); to.focus(); }
  });
  // Die Knoten-ID steckt nicht im DOM; über die Reihenfolge wieder auffindbar machen.
  function treeId(kind, a) {
    const flat = []; (function walk(ns) { for (const n of ns) { flat.push(n); if (n.children && (filter || open.has(n.id))) walk(n.children.filter(k => !filter || subtree(k))); } })(model(kind).filter(n => !filter || subtree(n)));
    return flat[[...lists[kind].querySelectorAll('.tree-link')].indexOf(a)]?.id;
  }

  async function refresh() {
    try { hosts = (await get('/api/hosts')).hosts; draw(); } catch { /* abgemeldet oder offline */ }
  }

  let timer = null;
  const off = onJobDone(() => setTimeout(refresh, 800));
  return {
    el, els, refresh, redraw: draw,
    start() { if (!timer) { refresh(); timer = setInterval(() => { if (!document.hidden) refresh(); }, 15000); } },
    stop() { clearInterval(timer); timer = null; },
    destroy() { clearInterval(timer); off(); },
  };
}
