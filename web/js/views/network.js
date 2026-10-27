import { h, clear, pageHead, panel, table, pill, loading, toast, toastError, confirm, formDialog, field, busy, dialog } from '../ui.js';
import { get, post, put } from '../api.js';

const TEMPLATES = {
  dhcp: 'inet autoconf\ninet6 autoconf\nup\n',
  static: 'inet 192.168.1.10 255.255.255.0\nup\n',
  veb: 'add vport0\nup\n',
  vport: 'inet 10.0.0.1 255.255.255.0\nup\n',
  vlan: 'parent em0 vnetid 10\ninet 192.168.10.1 255.255.255.0\nup\n',
  wg: 'wgkey <privater-schlüssel>\nwgport 51820\ninet 10.9.0.1/24\nwgpeer <öffentlicher-schlüssel> wgaip 10.9.0.2/32\nup\n',
};

export default {
  title: 'Netzwerk',
  async render(main) {
    const body = h('div', { class: 'content' }, loading());
    const newBtn = h('button', { class: 'cta', type: 'button' }, 'Interface konfigurieren');
    main.append(pageHead('14 / NETZ', 'Netzwerk', 'Schnittstellen (ifconfig), Routen und Resolver. Dauerhafte Konfiguration über hostname.if(5), angewendet mit netstart.', newBtn), body);
    let data = null;

    async function load() {
      data = await get('/api/network');
      draw();
    }

    async function action(i, a, btn) {
      if ((a === 'down' || a === 'destroy') && i.groups.includes('egress')) {
        if (!await confirm({ title: `${i.name} ist die Uplink-Schnittstelle`, text: 'Wenn du sie deaktivierst, verlierst du wahrscheinlich die Verbindung zu dieser Oberfläche.', ok: 'Trotzdem', danger: true, typeToConfirm: i.name })) return;
      }
      await busy(btn, async () => { const r = await post(`/api/network/${i.name}/${a}`); toast(r.message, r.output); await load(); });
    }

    function showRaw(i) {
      const close = h('button', { class: 'secondary', type: 'button' }, 'Schließen');
      const d = dialog({ title: i.name, meta: 'IFCONFIG', wide: true, body: h('pre', { class: 'codeblock' }, i.raw), actions: [close] });
      close.addEventListener('click', () => d.close());
    }

    function configure() {
      const name = h('input', { required: true, pattern: '[a-z]{1,12}[0-9]{1,4}', placeholder: 'em1, veb0, vport0, vlan10, wg0 …' });
      const tpl = h('select', null, h('option', { value: 'dhcp' }, 'DHCP / SLAAC'), h('option', { value: 'static' }, 'Statische Adresse'), h('option', { value: 'veb' }, 'veb – VM-Switch'), h('option', { value: 'vport' }, 'vport – Gateway für VMs'), h('option', { value: 'vlan' }, 'VLAN'), h('option', { value: 'wg' }, 'WireGuard'));
      const ta = h('textarea', { rows: 8, spellcheck: 'false', value: TEMPLATES.dhcp });
      tpl.addEventListener('change', () => { ta.value = TEMPLATES[tpl.value]; });
      const apply = h('input', { type: 'checkbox', checked: true });
      formDialog({
        title: 'Interface konfigurieren', meta: '/ETC/HOSTNAME.IF', wide: true, submit: 'Speichern',
        text: 'Legt /etc/hostname.<if> an. Existiert die Datei schon, bearbeite sie unter Konfiguration.',
        fields: [field('Schnittstelle', name), field('Vorlage', tpl), h('div', { class: 'wide' }, field('Inhalt', ta)), h('label', { class: 'check wide' }, apply, 'Direkt mit sh /etc/netstart <if> anwenden')],
        onSubmit: async () => {
          const path = `/etc/hostname.${name.value.trim()}`;
          const cur = await get('/api/file?path=' + encodeURIComponent(path));
          if (cur.exists) throw new Error(`${path} existiert bereits.`);
          await put('/api/file', { path, content: ta.value, mtime: '0' });
          if (apply.checked) { const r = await post(`/api/network/${name.value.trim()}/netstart`); toast(`${path} gespeichert und angewendet.`, r.output); } else toast(`${path} gespeichert.`);
          await load();
        },
      });
    }

    function draw() {
      const ifs = data.interfaces.map(i => {
        const btn = (label, a, danger) => { const b = h('button', { class: 'mini-btn' + (danger ? ' danger' : ''), type: 'button' }, label); b.addEventListener('click', () => action(i, a, b).catch(toastError)); return b; };
        const status = !i.up ? pill('off', 'down') : i.status === 'no carrier' ? pill('warn', 'kein Link') : pill('ok', 'up');
        return h('tr', null,
          h('td', null, h('span', { class: 'name mono' }, i.name), i.description ? h('span', { class: 'sub' }, i.description) : null),
          h('td', null, status),
          h('td', { class: 'mono' }, i.addrs.map(a => h('div', null, a.addr + (a.netmask ? ` / ${a.netmask}` : a.prefix ? `/${a.prefix}` : '')))),
          h('td', { class: 'mono' }, i.lladdr || '–', i.media ? h('span', { class: 'sub' }, i.media) : null),
          h('td', null, i.groups.map(g => h('span', { class: 'tag' }, g)), (i.members || []).map(m => h('span', { class: 'tag' }, '↳ ' + m))),
          h('td', { class: 'actions' },
            h('button', { class: 'mini-btn', type: 'button', onclick: () => showRaw(i) }, 'Details'),
            i.hasFile ? h('a', { class: 'mini-btn', href: `#/files?path=${encodeURIComponent('/etc/hostname.' + i.name)}` }, 'Konfig') : null,
            i.hasFile ? btn('Netstart', 'netstart') : null,
            i.name === 'lo0' ? null : i.up ? btn('Down', 'down', true) : btn('Up', 'up')));
      });
      const routes = data.routes.map(r => h('tr', null, h('td', { class: 'mono name' }, r.dest), h('td', { class: 'mono' }, r.gateway), h('td', { class: 'mono' }, r.flags), h('td', { class: 'mono' }, r.iface), h('td', null, r.family === 'inet6' ? 'IPv6' : 'IPv4')));
      clear(body,
        panel('Schnittstellen', 'IFCONFIG -A', h('div', { class: 'panel-body flush' }, table(['Interface', 'Status', 'Adressen', 'MAC / Medium', 'Gruppen', ''], ifs))),
        h('div', { class: 'grid-2' },
          panel('Routen', 'NETSTAT -RN', h('div', { class: 'panel-body flush' }, table(['Ziel', 'Gateway', 'Flags', 'Interface', ''], routes))),
          panel('Namensauflösung', '/ETC/RESOLV.CONF', h('div', { class: 'panel-body' },
            h('dl', { class: 'kv' },
              h('dt', null, 'Standard-Gateway'), h('dd', { class: 'mono' }, data.gateway || '–'),
              h('dt', null, 'Nameserver'), h('dd', { class: 'mono' }, data.resolvers.length ? data.resolvers.map(r => h('div', null, r)) : '–')),
            h('div', { class: 'toolbar', style: { marginTop: '18px' } },
              h('a', { class: 'mini-btn', href: '#/files?path=/etc/resolv.conf' }, 'resolv.conf'),
              h('a', { class: 'mini-btn', href: '#/files?path=/etc/mygate' }, 'mygate'),
              h('a', { class: 'mini-btn', href: '#/files?path=/etc/hosts' }, 'hosts'),
              h('a', { class: 'mini-btn', href: '#/files?path=/etc/myname' }, 'myname'))))));
    }

    newBtn.addEventListener('click', configure);
    await load();
  },
};
