import { h, clear, pageHead, panel, loading, bytes, toastError } from '../ui.js';
import { get, q } from '../api.js';

export default {
  title: 'Protokolle',
  async render(main, _parts, query) {
    const body = h('div', { class: 'content' }, loading());
    main.append(pageHead('ÜBERWACHUNG', 'Protokolle', 'Dateien unter /var/log ansehen, durchsuchen und live verfolgen.'), body);

    const files = await get('/api/logs');
    let es = null, lines = [], filter = '';
    const sel = h('select', { class: 'input', 'aria-label': 'Protokolldatei' }, files.map(f => h('option', { value: f.name }, `${f.name} (${bytes(f.size)})`)));
    sel.style.width = 'auto';
    const count = h('select', { class: 'input', 'aria-label': 'Anzahl Zeilen' }, [200, 500, 2000, 10000].map(n => h('option', { value: n, selected: n === 500 }, `${n} Zeilen`)));
    count.style.width = 'auto';
    const search = h('input', { class: 'input', type: 'search', placeholder: 'Filtern', 'aria-label': 'Zeilen filtern' });
    const follow = h('input', { type: 'checkbox', checked: true });
    const view = h('div', { class: 'log-view', tabindex: '0', role: 'log', 'aria-label': 'Protokollinhalt' });
    const info = h('span', { class: 'meta' });

    function render() {
      const t = filter.toLowerCase();
      const shown = t ? lines.filter(l => l.toLowerCase().includes(t)) : lines;
      const atEnd = view.scrollTop + view.clientHeight >= view.scrollHeight - 20;
      clear(view, shown.map(l => {
        const err = /error|fail|denied|invalid|refused/i.test(l);
        if (!t) return h('div', { class: err ? 'err' : null }, l);
        const i = l.toLowerCase().indexOf(t);
        return h('div', { class: err ? 'err' : null }, l.slice(0, i), h('mark', null, l.slice(i, i + t.length)), l.slice(i + t.length));
      }));
      info.textContent = `${shown.length} von ${lines.length} Zeilen`;
      if (atEnd || !view.dataset.loaded) { view.scrollTop = view.scrollHeight; view.dataset.loaded = '1'; }
    }

    async function load() {
      if (es) { es.close(); es = null; }
      if (!sel.value) { clear(view, 'Keine lesbaren Protokolle.'); return; }
      view.dataset.loaded = '';
      const r = await get('/api/log' + q({ name: sel.value, lines: count.value }));
      lines = r.lines;
      render();
      history.replaceState(null, '', '#/logs?name=' + encodeURIComponent(sel.value));
      if (follow.checked) {
        es = new EventSource('/api/log/follow' + q({ name: sel.value, offset: r.offset }));
        es.onmessage = e => {
          lines.push(JSON.parse(e.data));
          if (lines.length > 20000) lines.splice(0, lines.length - 20000);
          render();
        };
        es.addEventListener('rotated', () => { lines.push('── Datei wurde rotiert ──'); render(); });
      }
    }

    if (query.get('name')) sel.value = query.get('name');
    sel.addEventListener('change', () => load().catch(toastError));
    count.addEventListener('change', () => load().catch(toastError));
    follow.addEventListener('change', () => load().catch(toastError));
    search.addEventListener('input', () => { filter = search.value.trim(); render(); });
    clear(body, panel(sel.value || 'Protokoll', '/VAR/LOG', h('div', null,
      h('div', { class: 'panel-body toolbar' }, sel, count, h('div', { class: 'grow search' }, search), h('label', { class: 'check' }, follow, 'Live verfolgen'), info),
      view)));
    sel.addEventListener('change', () => { body.querySelector('.panel-head h2').textContent = sel.value; });
    await load();
    return () => { if (es) es.close(); };
  },
};
