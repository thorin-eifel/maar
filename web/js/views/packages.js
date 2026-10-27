import { h, clear, pageHead, panel, table, pill, loading, toastError, confirm, busy, debounce } from '../ui.js';
import { get, post, q } from '../api.js';
import { watchJob, onJobDone } from '../jobs.js';

export default {
  title: 'Pakete & Updates',
  async render(main) {
    const body = h('div', { class: 'content' }, loading());
    main.append(pageHead('HOST', 'Pakete & Updates', 'Pakete mit pkg_add(1) installieren und aktualisieren, Systempatches mit syspatch(8), Firmware mit fw_update(8) und Upgrades mit sysupgrade(8).'), body);

    let pkgs = [], showAll = false, filterTerm = '';
    const instBox = h('div', { class: 'panel-body flush' });
    const searchBox = h('div', { class: 'panel-body flush' }, h('div', { class: 'empty' }, 'Suchbegriff eingeben, z. B. „nginx“ oder „python“.'));
    const patchBox = h('div', { class: 'panel-body' }, loading());

    async function job(action, names, confirmText) {
      if (confirmText && !await confirm(confirmText)) return;
      const r = await post(`/api/packages/${action}`, names ? { names } : undefined);
      watchJob(r.job);
    }

    async function loadInstalled() {
      pkgs = await get('/api/packages');
      drawInstalled();
    }

    function drawInstalled() {
      const list = pkgs.filter(p => (showAll || p.manual) && (!filterTerm || p.name.toLowerCase().includes(filterTerm) || p.comment.toLowerCase().includes(filterTerm)));
      clear(instBox, table(['Paket', 'Version', 'Beschreibung', ''], list.map(p => {
        const rm = h('button', { class: 'mini-btn danger', type: 'button' }, 'Entfernen');
        rm.addEventListener('click', () => job('delete', [p.stem], { title: `${p.stem} entfernen?`, text: `pkg_delete ${p.stem}. Konfigurationsdateien in /etc bleiben erhalten.`, ok: 'Entfernen', danger: true }).catch(toastError));
        return h('tr', null,
          h('td', { class: 'name mono' }, p.stem, p.manual ? null : h('span', { class: 'sub' }, 'Abhängigkeit')),
          h('td', { class: 'mono' }, p.version),
          h('td', null, p.comment),
          h('td', { class: 'actions' }, rm));
      }), 'Keine Pakete installiert.'));
    }

    async function search(term) {
      if (term.length < 2) return;
      clear(searchBox, loading('Suche …'));
      try {
        const hits = await get('/api/packages/search' + q({ q: term }));
        clear(searchBox, table(['Paket', 'Status', ''], hits.map(x => {
          const b = h('button', { class: 'mini-btn', type: 'button', disabled: x.installed }, x.installed ? 'Installiert' : 'Installieren');
          b.addEventListener('click', () => busy(b, () => job('install', [x.name])).catch(toastError));
          return h('tr', null, h('td', { class: 'name mono' }, x.name), h('td', null, x.installed ? pill('ok', 'installiert') : pill('off', 'verfügbar')), h('td', { class: 'actions' }, b));
        }), 'Nichts gefunden.'));
      } catch (e) { clear(searchBox, h('div', { class: 'empty' }, e.message)); }
    }

    async function loadPatches() {
      try {
        const p = await get('/api/patches');
        const apply = h('button', { class: 'cta', type: 'button', disabled: !p.available.length }, p.available.length ? `${p.available.length} Patches einspielen` : 'System ist aktuell');
        apply.addEventListener('click', () => job('syspatch', null, { title: 'Systempatches einspielen?', text: 'syspatch lädt signierte Binärpatches und installiert sie. Kernel-Patches werden erst nach einem Neustart aktiv.', ok: 'Einspielen' }).catch(toastError));
        clear(patchBox,
          h('dl', { class: 'kv' },
            h('dt', null, 'Verfügbar'), h('dd', null, p.available.length ? p.available.map(x => h('span', { class: 'tag' }, x)) : 'keine'),
            h('dt', null, 'Installiert'), h('dd', null, p.installed.length ? p.installed.map(x => h('span', { class: 'tag' }, x)) : 'keine')),
          h('div', { class: 'toolbar', style: { marginTop: '18px' } }, apply));
      } catch (e) { clear(patchBox, h('div', { class: 'note warn' }, 'syspatch: ' + e.message)); }
    }

    const searchInput = h('input', { class: 'input', type: 'search', placeholder: 'Paket im Repository suchen', 'aria-label': 'Paket suchen' });
    searchInput.addEventListener('input', debounce(() => search(searchInput.value.trim()), 400));
    const filterInput = h('input', { class: 'input', type: 'search', placeholder: 'Installierte filtern', 'aria-label': 'Installierte Pakete filtern' });
    filterInput.addEventListener('input', () => { filterTerm = filterInput.value.trim().toLowerCase(); drawInstalled(); });
    const allToggle = h('input', { type: 'checkbox' });
    allToggle.addEventListener('change', () => { showAll = allToggle.checked; drawInstalled(); });

    const btn = (label, fn, cls = 'secondary') => { const b = h('button', { class: cls, type: 'button' }, label); b.addEventListener('click', () => fn().catch(toastError)); return b; };

    clear(body,
      h('div', { class: 'grid-2' },
        panel('Systempatches', 'SYSPATCH', patchBox),
        panel('Wartung', 'PKG_ADD · FW_UPDATE · SYSUPGRADE', h('div', { class: 'panel-body' },
          h('div', { class: 'toolbar' },
            btn('Alle Pakete aktualisieren', () => job('update', null)),
            btn('Verwaiste entfernen', () => job('autoremove', null, { title: 'Verwaiste Abhängigkeiten entfernen?', text: 'pkg_delete -a entfernt Pakete, die nur als Abhängigkeit installiert wurden und nicht mehr gebraucht werden.', ok: 'Entfernen' })),
            btn('Firmware aktualisieren', () => job('fw_update', null))),
          h('div', { class: 'note warn', style: { marginTop: '18px' } },
            h('strong', null, 'Systemupgrade: '), 'sysupgrade -n lädt die nächste Version und bereitet das Upgrade vor, startet aber nicht neu. Den Neustart löst du anschließend unter „System“ aus.'),
          h('div', { class: 'toolbar', style: { marginTop: '14px' } },
            btn('Release-Upgrade vorbereiten', () => job('sysupgrade', null, { title: 'Release-Upgrade vorbereiten?', text: 'Lädt die Installationssets der nächsten Release. Beim nächsten Neustart wird automatisch aktualisiert.', ok: 'Vorbereiten', typeToConfirm: 'upgrade' })),
            btn('Snapshot-Upgrade vorbereiten', () => job('sysupgrade-snapshot', null, { title: 'Auf -current-Snapshot wechseln?', text: 'Snapshots sind Entwicklungsstände. Ein Zurück zur Release ist nur per Neuinstallation möglich.', ok: 'Vorbereiten', danger: true, typeToConfirm: 'snapshot' })))))),
      panel('Paket installieren', 'PKG_INFO -Q', searchBox, h('div', { class: 'search' }, searchInput)),
      panel('Installierte Pakete', 'PKG_INFO', instBox, h('label', { class: 'check' }, allToggle, 'Abhängigkeiten zeigen'), h('div', { class: 'search' }, filterInput)));

    await Promise.all([loadInstalled(), loadPatches()]);
    const off = onJobDone(() => { loadInstalled().catch(() => {}); loadPatches(); });
    return off;
  },
};
