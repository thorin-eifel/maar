import { h, clear, pageHead, panel, table, loading, toast, toastError, confirm, formDialog, field, debounce } from '../ui.js';
import { get, post, put, session } from '../api.js';

export default {
  title: 'Host & Betrieb',
  async render(main) {
    const body = h('div', { class: 'content' }, loading());
    main.append(pageHead('HOST', 'Host & Betrieb', 'Kernel-Parameter (sysctl), Neustart und Ausschalten des Hosts.'), body);

    let all = [], term = 'kern.';
    const box = h('div', { class: 'panel-body flush' });
    const search = h('input', { class: 'input', type: 'search', placeholder: 'z. B. net.inet.ip.forwarding', 'aria-label': 'sysctl filtern', value: term });

    function draw() {
      const t = term.trim().toLowerCase();
      const list = all.filter(s => !t || s.name.includes(t) || s.value.toLowerCase().includes(t)).slice(0, 300);
      clear(box, table(['Name', 'Wert', ''], list.map(s => h('tr', null,
        h('td', { class: 'mono name' }, s.name),
        h('td', { class: 'mono' }, s.value),
        h('td', { class: 'actions' }, h('button', { class: 'mini-btn', type: 'button', onclick: () => edit(s) }, 'Ändern')))), 'Keine Treffer.'));
    }

    function edit(s) {
      const v = h('input', { value: s.value, class: 'mono', required: true });
      formDialog({
        title: s.name, meta: 'SYSCTL', submit: 'Setzen',
        text: 'Gilt sofort bis zum nächsten Neustart. Dauerhaft wird der Wert über /etc/sysctl.conf.',
        fields: [h('div', { class: 'wide' }, field('Neuer Wert', v))],
        onSubmit: async () => {
          const r = await put('/api/sysctl', { name: s.name, value: v.value.trim() });
          toast(r.message);
          all = await get('/api/sysctl');
          draw();
        },
      });
    }

    async function power(action) {
      const texts = {
        reboot: { title: 'Host neu starten?', text: 'shutdown -r +1: Alle laufenden VMs werden beendet, Verbindungen getrennt. Abbruch innerhalb einer Minute möglich.', ok: 'Neu starten' },
        halt: { title: 'Host ausschalten?', text: 'shutdown -p +1: Der Host fährt herunter und schaltet sich aus. Ein Einschalten über diese Oberfläche ist danach nicht möglich.', ok: 'Ausschalten' },
      };
      if (!await confirm({ ...texts[action], danger: true, typeToConfirm: session.hostname })) return;
      try { const r = await post(`/api/power/${action}`); toast(r.message); } catch (e) { toastError(e); }
    }

    search.addEventListener('input', debounce(() => { term = search.value; draw(); }, 150));
    const cancel = h('button', { class: 'secondary', type: 'button' }, 'Geplanten Shutdown abbrechen');
    cancel.addEventListener('click', () => post('/api/power/cancel').then(r => toast(r.message)).catch(toastError));
    clear(body,
      panel('Energie', 'SHUTDOWN(8)', h('div', { class: 'panel-body' },
        h('p', null, 'Beide Aktionen werden mit einer Minute Vorlauf geplant und an alle angemeldeten Benutzer gemeldet.'),
        h('div', { class: 'toolbar' },
          h('button', { class: 'cta', type: 'button', onclick: () => power('reboot') }, 'Neu starten'),
          h('button', { class: 'secondary danger', type: 'button', onclick: () => power('halt') }, 'Ausschalten'),
          cancel))),
      panel('Kernel-Parameter', 'SYSCTL -A', box, h('div', { class: 'search' }, search)),
      h('div', { class: 'note' }, 'Dauerhafte Einstellungen: ', h('a', { href: '#/files?path=/etc/sysctl.conf' }, '/etc/sysctl.conf'), ', ', h('a', { href: '#/files?path=/etc/rc.conf.local' }, '/etc/rc.conf.local'), ', ', h('a', { href: '#/files?path=/etc/doas.conf' }, '/etc/doas.conf'), '.'));
    all = await get('/api/sysctl');
    draw();
  },
};
