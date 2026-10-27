// Netzwerk-Interface als Formular (hostname.if). Änderungen gelten auf Probe und werden
// ohne Bestätigung automatisch zurückgenommen, damit man sich nicht aussperrt.
import { h, clear, pageHead, panel, pill, loading, toast, toastError, dialog, field, busy } from '../ui.js';
import { get, put, post } from '../api.js';
import { rows, sel, inp, chk } from './hostconf.js';

const clone = x => JSON.parse(JSON.stringify(x));
const PREFIX4 = Array.from({ length: 25 }, (_, i) => 32 - i);
const maskOf = p => { const m = p === 0 ? 0 : (0xffffffff << (32 - p)) >>> 0; return [m >>> 24, m >> 16 & 255, m >> 8 & 255, m & 255].join('.'); };

export default {
  title: 'Interface',
  async render(main, parts, query) {
    const name = parts[0];
    const head = h('div'), body = h('div', { class: 'content' }, loading());
    main.append(head, body);
    let timer = null, pending = null;

    async function load() {
      clearInterval(timer);
      const v = await get(`/api/conf/iface?name=${encodeURIComponent(name)}`);
      if (!v.exists && name.startsWith('vlan')) {
        v.conf.kind = 'vlan';
        v.conf.vlanId = Number(name.slice(4)) || 0;
        v.conf.vlanParent = query?.get('parent') || v.parents[0] || '';
        v.conf.v4Mode = 'static';
        v.conf.v4 = [{ addr: '', prefix: 24 }];
      }
      draw(v);
    }

    function draw(v) {
      const c = clone(v.conf);
      clear(head, pageHead('NETZWERK', `Interface ${name}`, v.managedBy ? `Verwaltet von ${v.managedBy}` : `${v.link || 'kein Link-Status'}${v.current.length ? ' · ' + v.current.join(', ') : ''}`,
        v.egress ? pill('warn', 'Uplink (Standardroute)') : null, v.exists ? null : pill('info', 'neu')));
      if (!v.editable) {
        clear(body, h('div', { class: 'note warn' }, v.reason), h('div', { class: 'toolbar' }, h('a', { class: 'secondary', href: '#/network' }, 'Zurück'), h('a', { class: 'mini-btn', href: `#/files?path=${encodeURIComponent('/etc/hostname.' + name)}` }, 'Als Text bearbeiten')));
        return;
      }
      const banner = h('div');
      if (v.pending) {
        let left = v.pending.seconds;
        const ok = h('button', { class: 'cta', type: 'button' }, 'Konfiguration bestätigen'), undo = h('button', { class: 'secondary', type: 'button' }, 'Zurücknehmen');
        const count = h('strong', null, `${left} s`);
        ok.addEventListener('click', () => busy(ok, async () => { await post('/api/conf/iface/confirm'); toast('Die neue Interface-Konfiguration ist dauerhaft.'); await load(); }).catch(toastError));
        undo.addEventListener('click', () => busy(undo, async () => { await post('/api/conf/iface/revert'); toast('Zurückgenommen.'); await load(); }).catch(toastError));
        clear(banner, h('div', { class: 'note bad pf-pending', role: 'alert' }, h('div', null, h('strong', null, `Die Änderung an ${v.pending.name} ist aktiv, aber noch nicht bestätigt. `), 'Lädt diese Seite noch, ist die Verbindung in Ordnung – bestätige. Ohne Bestätigung wird in ', count, ' der alte Stand wiederhergestellt.'), h('div', { class: 'toolbar' }, ok, undo)));
        timer = setInterval(() => { left -= 1; count.textContent = `${Math.max(left, 0)} s`; if (left < -2) { clearInterval(timer); load().catch(() => {}); } }, 1000);
      }

      const v4 = h('div'), v6 = h('div'), vlanBox = h('div');
      const prefixSel = (a, on) => sel(PREFIX4.map(p => [p, `/${p} (${maskOf(p)})`]), a.prefix, x => { a.prefix = Number(x); on?.(); });
      function drawV4() {
        clear(v4, field('IPv4', sel([['none', 'keine Adresse'], ['dhcp', 'automatisch per DHCP'], ['static', 'feste Adresse']], c.v4Mode, x => { c.v4Mode = x; if (x === 'static' && !c.v4.length) c.v4 = [{ addr: '', prefix: 24 }]; drawV4(); })),
          c.v4Mode === 'static' ? h('div', null, rows(c.v4, (a, i) => h('div', { class: 'cf-fields' }, field(i === 0 ? 'Hauptadresse' : 'Weitere Adresse (Alias)', inp(a.addr, x => { a.addr = x.trim(); }, { class: 'mono', placeholder: '192.168.50.11' })), field('Netz', prefixSel(a))), { add: 'Adresse hinzufügen', max: 16, fresh: () => ({ addr: '', prefix: 24 }) }),
            field('Standard-Gateway (/etc/mygate)', inp(c.gateway, x => { c.gateway = x.trim(); }, { class: 'mono', placeholder: '192.168.50.1' }), v.egress ? 'Dieses Interface trägt die Standardroute. Leer = bisheriges Gateway behalten.' : 'Nur nötig, wenn dieses Interface die Standardroute trägt.')) : null);
      }
      function drawV6() {
        clear(v6, field('IPv6', sel([['none', 'keine Adresse'], ['auto', 'automatisch (SLAAC)'], ['static', 'feste Adresse']], c.v6Mode, x => { c.v6Mode = x; if (x === 'static' && !c.v6.length) c.v6 = [{ addr: '', prefix: 64 }]; drawV6(); })),
          c.v6Mode === 'static' ? rows(c.v6, (a, i) => h('div', { class: 'cf-fields' }, field(i === 0 ? 'Hauptadresse' : 'Weitere Adresse', inp(a.addr, x => { a.addr = x.trim(); }, { class: 'mono', placeholder: '2001:db8::10' })), field('Präfixlänge', inp(String(a.prefix), x => { a.prefix = Number(x) || 64; }, { type: 'number', min: 8, max: 128 }))), { add: 'Adresse hinzufügen', max: 16, fresh: () => ({ addr: '', prefix: 64 }) }) : null);
      }
      drawV4(); drawV6();
      if (c.kind === 'vlan') {
        clear(vlanBox, h('div', { class: 'form-grid' }, field('Physisches Interface (parent)', sel(v.parents.map(p => [p, p]), c.vlanParent, x => { c.vlanParent = x; })), field('VLAN-ID', inp(String(c.vlanId || ''), x => { c.vlanId = Number(x) || 0; }, { type: 'number', min: 1, max: 4094 }))));
      }
      const mtu = sel([['0', 'Standard'], ['1280', '1280 (IPv6-Minimum)'], ['1492', '1492 (PPPoE)'], ['1500', '1500'], ['9000', '9000 (Jumbo-Frames)'], ...([0, 1280, 1492, 1500, 9000].includes(c.mtu) ? [] : [[String(c.mtu), String(c.mtu)]])], c.mtu, x => { c.mtu = Number(x); });

      const payload = () => ({ ...c, v4: c.v4Mode === 'static' ? c.v4.filter(a => a.addr) : [], v6: c.v6Mode === 'static' ? c.v6.filter(a => a.addr) : [] });
      const preview = h('button', { class: 'secondary', type: 'button' }, 'Vorschau'), reset = h('button', { class: 'secondary', type: 'button' }, 'Zurücksetzen'), apply = h('button', { class: 'cta', type: 'button' }, 'Auf Probe anwenden');
      preview.addEventListener('click', () => busy(preview, async () => {
        const r = await put('/api/conf/iface?preview=1', payload());
        const close = h('button', { class: 'secondary', type: 'button' }, 'Schließen');
        const d = dialog({ title: `So würde /etc/hostname.${name} aussehen`, meta: 'HOSTNAME.IF', wide: true, body: h('div', null, r.warnings?.length ? h('div', { class: 'note warn' }, r.warnings.map(w => h('div', null, w))) : null, h('pre', { class: 'codeblock' }, r.content)), actions: [close] });
        close.addEventListener('click', () => d.close());
      }).catch(toastError));
      reset.addEventListener('click', () => load().catch(toastError));
      apply.addEventListener('click', () => busy(apply, async () => { const r = await put('/api/conf/iface', payload()); if (r.warnings?.length) toast('Bitte beachten', r.warnings.join('\n')); await load(); }).catch(toastError));

      clear(body, banner,
        v.egress ? h('div', { class: 'note warn' }, 'Dieses Interface trägt die Standardroute – vermutlich ist dein Browser darüber mit maar verbunden. Neue Einstellungen gelten deshalb auf Probe: Lädt diese Seite danach noch, bestätigst du; sonst stellt maar nach 60 Sekunden den alten Stand her.') : null,
        c.kind === 'vlan' ? panel('VLAN', 'PARENT · VNETID', h('div', { class: 'panel-body' }, vlanBox)) : null,
        panel('Adressen', 'INET · INET6', h('div', { class: 'panel-body' }, v4, h('div', { style: { height: '14px' } }), v6)),
        panel('Allgemein', 'DESCRIPTION · MTU · UP', h('div', { class: 'panel-body form-grid' }, field('Beschreibung', inp(c.description, x => { c.description = x; }, { maxlength: 60, placeholder: 'z. B. Uplink' })), field('MTU', mtu),
          chk('Interface aktiv (up)', !c.down, x => { c.down = !x; }))),
        v.preserved.length ? h('div', { class: 'note' }, 'Diese Zeilen kennt das Formular nicht und lässt sie unverändert: ', h('code', { class: 'mono' }, v.preserved.join(' · '))) : null,
        h('div', { class: 'toolbar' }, preview, reset, apply, h('a', { class: 'mini-btn', href: `#/files?path=${encodeURIComponent('/etc/hostname.' + name)}` }, 'Als Text bearbeiten (Experte)')));
    }

    await load();
    return () => clearInterval(timer);
  },
};
