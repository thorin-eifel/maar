import { h, clear, pageHead, panel, table, bytes, loading, toast, toastError, confirm } from '../ui.js';
import { get, post } from '../api.js';

export default {
  title: 'Prozesse',
  async render(main) {
    const body = h('div', { class: 'content' }, loading());
    const auto = h('input', { type: 'checkbox', checked: true });
    main.append(pageHead('ÜBERWACHUNG', 'Prozesse', 'Laufende Prozesse nach CPU-Last (ps -axww). Signale an einzelne Prozesse senden.', h('label', { class: 'check' }, auto, 'Alle 5 s aktualisieren')), body);

    let procs = [], term = '', sort = 'cpu';
    const box = h('div', { class: 'panel-body flush' });
    const search = h('input', { class: 'input', type: 'search', placeholder: 'Befehl, Benutzer oder PID', 'aria-label': 'Prozesse filtern' });
    const sortSel = h('select', { class: 'input', 'aria-label': 'Sortierung' }, h('option', { value: 'cpu' }, 'nach CPU'), h('option', { value: 'mem' }, 'nach Speicher'), h('option', { value: 'pid' }, 'nach PID'));
    sortSel.style.width = 'auto';

    async function load() {
      procs = await get('/api/procs');
      draw();
    }

    async function signal(p, sig) {
      if (!await confirm({ title: `${sig} an PID ${p.pid} senden?`, text: p.command, ok: `${sig} senden`, danger: sig === 'KILL' })) return;
      try { const r = await post(`/api/procs/${p.pid}/kill`, { signal: sig }); toast(r.message); await load(); } catch (e) { toastError(e); }
    }

    function draw() {
      const t = term.toLowerCase();
      let list = procs.filter(p => !t || p.command.toLowerCase().includes(t) || p.user.includes(t) || String(p.pid) === t);
      if (sort === 'mem') list = [...list].sort((a, b) => b.rss - a.rss);
      if (sort === 'pid') list = [...list].sort((a, b) => a.pid - b.pid);
      clear(box, table([{ label: 'PID', num: true }, 'Benutzer', { label: 'CPU', num: true }, { label: 'RSS', num: true }, 'Status', 'Laufzeit', 'Befehl', ''],
        list.slice(0, 400).map(p => h('tr', null,
          h('td', { class: 'num mono' }, p.pid),
          h('td', null, p.user),
          h('td', { class: 'num' }, p.cpu.toFixed(1).replace('.', ',')),
          h('td', { class: 'num' }, bytes(p.rss)),
          h('td', { class: 'mono' }, p.state),
          h('td', { class: 'mono' }, p.time),
          h('td', { class: 'mono' }, h('span', { title: p.command }, p.command.length > 90 ? p.command.slice(0, 90) + '…' : p.command)),
          h('td', { class: 'actions' }, p.pid > 1 ? [
            h('button', { class: 'mini-btn', type: 'button', onclick: () => signal(p, 'TERM') }, 'Beenden'),
            h('button', { class: 'mini-btn danger', type: 'button', onclick: () => signal(p, 'KILL') }, 'Kill')] : null)))));
    }

    search.addEventListener('input', () => { term = search.value.trim(); draw(); });
    sortSel.addEventListener('change', () => { sort = sortSel.value; draw(); });
    clear(body, panel('Prozessliste', 'PS', box, sortSel, h('div', { class: 'search' }, search)));
    await load();
    const timer = setInterval(() => { if (auto.checked && !document.querySelector('dialog[open]')) load().catch(() => {}); }, 5000);
    return () => clearInterval(timer);
  },
};
