import { h, clear, pageHead, panel, loading, toast, toastError, confirm, busy, dateTime, bytes, dialog } from '../ui.js';
import { get, put, post, q } from '../api.js';

export default {
  title: 'Konfigurationsdateien',
  async render(main, _parts, query) {
    const body = h('div', { class: 'content' }, loading());
    main.append(pageHead('20 / HOST-KONFIGURATION', 'Konfigurationsdateien', 'Freigegebene Systemdateien bearbeiten. Jede Änderung wird vorher mit dem passenden Prüfprogramm validiert und die alte Fassung gesichert.'), body);

    let files = await get('/api/files');
    let cur = null, dirty = false;
    const listEl = h('nav', { class: 'file-list', 'aria-label': 'Dateien' });
    const editor = h('div');

    function drawList() {
      const groups = {};
      for (const f of files) (groups[f.group] ||= []).push(f);
      clear(listEl, Object.entries(groups).map(([g, list]) => [
        h('h3', null, g),
        list.map(f => h('button', { type: 'button', class: f.exists ? '' : 'missing', 'aria-current': String(cur?.path === f.path), onclick: () => open(f.path) },
          f.label, h('span', { class: 'sub' }, f.path + (f.exists ? '' : ' · neu')))),
      ]));
    }

    async function open(path) {
      if (dirty && !await confirm({ title: 'Ungespeicherte Änderungen verwerfen?', ok: 'Verwerfen', danger: true })) return;
      cur = await get('/api/file' + q({ path }));
      dirty = false;
      history.replaceState(null, '', '#/files?path=' + encodeURIComponent(path));
      drawList();
      drawEditor();
    }

    function drawEditor() {
      const ta = h('textarea', { class: 'code', spellcheck: 'false', autocapitalize: 'off', 'aria-label': `Inhalt von ${cur.path}`, value: cur.content });
      const save = h('button', { class: 'cta', type: 'button' }, cur.check ? 'Prüfen und speichern' : 'Speichern');
      const apply = cur.apply ? h('button', { class: 'secondary', type: 'button' }, cur.applyLabel) : null;
      const hist = h('button', { class: 'mini-btn', type: 'button' }, 'Sicherungen');
      const out = h('div', { 'aria-live': 'polite' });
      const status = h('span', { class: 'meta' }, cur.exists ? `${bytes(cur.size)} · geändert ${dateTime(Number(cur.mtime) / 1e9)}` : 'Datei existiert noch nicht');

      ta.addEventListener('input', () => { dirty = true; status.textContent = 'ungespeichert'; });
      ta.addEventListener('keydown', e => {
        if (e.key === 'Tab' && !e.shiftKey && !e.ctrlKey && !e.metaKey && !e.altKey) {
          // Tab fügt ein Tabzeichen ein; Escape gefolgt von Tab verlässt das Feld.
          if (ta.dataset.escape === '1') { ta.dataset.escape = ''; return; }
          e.preventDefault();
          ta.setRangeText('\t', ta.selectionStart, ta.selectionEnd, 'end');
          dirty = true;
        } else if (e.key === 'Escape') { ta.dataset.escape = '1'; } else ta.dataset.escape = '';
        if ((e.metaKey || e.ctrlKey) && e.key === 's') { e.preventDefault(); save.click(); }
      });

      save.addEventListener('click', () => busy(save, async () => {
        clear(out);
        try {
          const r = await put('/api/file', { path: cur.path, content: ta.value, mtime: cur.mtime || '0' });
          dirty = false;
          cur.mtime = r.mtime; cur.content = ta.value; cur.exists = true;
          status.textContent = 'gespeichert ' + new Date().toLocaleTimeString('de-DE');
          toast(`${cur.path} gespeichert.`, r.backup ? `Sicherung: ${r.backup}` : '');
          if (r.check) clear(out, h('div', { class: 'note' }, h('strong', null, 'Prüfung: '), r.check));
          files = await get('/api/files');
          drawList();
        } catch (e) {
          if (e.check) clear(out, h('div', { class: 'note bad' }, h('strong', null, e.message), h('pre', { class: 'codeblock wrap' }, e.check)));
          else toastError(e);
        }
      }));
      apply?.addEventListener('click', () => busy(apply, async () => {
        if (dirty) { toastError(new Error('Bitte zuerst speichern.')); return; }
        if (!await confirm({ title: `${cur.applyLabel}?`, text: `Führt „${cur.apply.join(' ')}“ aus.`, ok: 'Ausführen' })) return;
        const r = await post('/api/file/apply', { path: cur.path });
        toast(r.message, r.output);
      }).catch(toastError));
      hist.addEventListener('click', () => showBackups(ta).catch(toastError));

      clear(editor, panel(cur.label, cur.path, h('div', null,
        h('div', { class: 'panel-body toolbar' }, status, h('span', { class: 'grow' }), cur.check ? h('span', { class: 'tag' }, 'Prüfung: ' + cur.check.join(' ').replace('{}', '<datei>')) : null),
        ta, h('div', { class: 'panel-body' }, out, h('div', { class: 'toolbar' }, save, apply))), hist));
      ta.focus();
    }

    async function showBackups(ta) {
      const list = await get('/api/file/backups' + q({ path: cur.path }));
      const close = h('button', { class: 'secondary', type: 'button' }, 'Schließen');
      const d = dialog({
        title: 'Sicherungen', meta: cur.path, wide: true,
        text: 'Eine Sicherung laden ersetzt den Editorinhalt. Erst „Speichern“ schreibt sie zurück.',
        body: list.length ? h('div', { class: 'table-wrap' }, h('table', { class: 'table' }, h('tbody', null, list.map(b => h('tr', null,
          h('td', null, dateTime(b.time)), h('td', { class: 'num' }, bytes(b.size)),
          h('td', { class: 'actions' }, h('button', { class: 'mini-btn', type: 'button', onclick: async () => {
            const r = await get('/api/file/backup' + q({ path: cur.path, id: b.id }));
            ta.value = r.content; dirty = true; d.close(); toast('Sicherung geladen – noch nicht gespeichert.');
          } }, 'Laden'))))))) : h('div', { class: 'empty' }, 'Noch keine Sicherungen.'),
        actions: [close],
      });
      close.addEventListener('click', () => d.close());
    }

    const beforeUnload = e => { if (dirty) { e.preventDefault(); e.returnValue = ''; } };
    window.addEventListener('beforeunload', beforeUnload);
    clear(body, h('div', { class: 'editor-layout' }, listEl, editor));
    drawList();
    const want = query.get('path');
    if (want) await open(want);
    else clear(editor, h('div', { class: 'panel empty' }, 'Links eine Datei wählen.'));
    return () => window.removeEventListener('beforeunload', beforeUnload);
  },
};
