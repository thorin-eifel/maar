import { h, clear, pageHead, panel, table, bytes, loading, toastError, dialog } from '../ui.js';
import { get } from '../api.js';

export default {
  title: 'Lokale Datenträger',
  async render(main) {
    const body = h('div', { class: 'content' }, loading());
    main.append(pageHead('07 / SPEICHER', 'Lokale Datenträger', 'Eingehängte Dateisysteme (df, mount) und Datenträger mit ihren Disklabels.'), body);
    const st = await get('/api/storage');

    const showLabel = async d => {
      try {
        const r = await get(`/api/disklabel/${d.name}`);
        const close = h('button', { class: 'secondary', type: 'button' }, 'Schließen');
        const dlg = dialog({ title: `Disklabel ${d.name}`, meta: `DUID ${d.duid || '–'}`, wide: true, body: h('pre', { class: 'codeblock' }, r.output), actions: [close] });
        close.addEventListener('click', () => dlg.close());
      } catch (e) { toastError(e); }
    };

    const mounts = st.mounts.map(m => {
      const i = h('i');
      i.style.width = m.percent + '%';
      return h('tr', null,
        h('td', { class: 'name mono' }, m.mount),
        h('td', { class: 'mono' }, m.device),
        h('td', null, (m.options || '').split(', ').filter(Boolean).map(o => h('span', { class: 'tag' }, o))),
        h('td', { class: 'num' }, bytes(m.used)),
        h('td', { class: 'num' }, bytes(m.avail)),
        h('td', { class: 'num' }, bytes(m.size)),
        h('td', null, h('div', { class: m.percent > 90 ? 'bar crit' : m.percent > 75 ? 'bar warn' : 'bar', role: 'meter', 'aria-valuenow': String(m.percent), 'aria-valuemin': '0', 'aria-valuemax': '100', 'aria-label': `${m.mount} ${m.percent} % belegt` }, i)),
        h('td', { class: 'num' }, m.percent + ' %'));
    });
    const disks = st.disks.map(d => h('tr', null,
      h('td', { class: 'name mono' }, d.name),
      h('td', { class: 'mono' }, d.duid || '–'),
      h('td', { class: 'actions' }, d.name.startsWith('cd') ? null : h('button', { class: 'mini-btn', type: 'button', onclick: () => showLabel(d) }, 'Disklabel'))));
    clear(body,
      panel('Dateisysteme', 'DF -KP', h('div', { class: 'panel-body flush' }, table(['Einhängepunkt', 'Gerät', 'Optionen', { label: 'Belegt', num: true }, { label: 'Frei', num: true }, { label: 'Größe', num: true }, 'Auslastung', { label: '%', num: true }], mounts))),
      panel('Datenträger', 'HW.DISKNAMES', h('div', { class: 'panel-body flush' }, table(['Gerät', 'DUID', ''], disks))),
      h('div', { class: 'note' }, 'Partitionierung (disklabel -E, newfs) und /etc/fstab sind bewusst nicht als Ein-Klick-Aktion vorhanden. /etc/fstab lässt sich unter ', h('a', { href: '#/files?path=/etc/fstab' }, 'Konfiguration'), ' bearbeiten; für Partitionen die Host-Shell nutzen.'));
  },
};
