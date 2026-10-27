// VM-Netze: Portgruppen auf veb, feste Adressen mit DHCP/DNS, pf-Regeln je VM.
import { h, clear, pageHead, panel, table, pill, loading, toast, toastError, confirm, formDialog, field, busy } from '../ui.js';
import { get, post, put, del } from '../api.js';
import { watchJob, onJobDone } from '../jobs.js';

const enc = encodeURIComponent;

function checkbox(label, checked, hint) {
  const input = h('input', { type: 'checkbox', checked });
  return { input, el: h('label', { class: 'check' }, input, h('span', null, label, hint ? h('span', { class: 'hint' }, hint) : null)) };
}

// ---- Portgruppe anlegen / ändern ----

export async function portGroupDialog(pg, onDone) {
  const net = await get('/api/vnet');
  const edit = !!pg;
  pg = pg || { nat: true, dhcp: true, dns: true, filter: true, locked: true, vlan: 0 };
  const name = h('input', { required: true, pattern: '[A-Za-z0-9][A-Za-z0-9._\\-]{0,63}', value: pg.name || '', disabled: edit, placeholder: 'dmz' });
  const descr = h('input', { value: pg.description || '', maxlength: 60, placeholder: 'Öffentliche Dienste' });
  const uplink = h('select', null, h('option', { value: '' }, '— nur intern, kein Uplink —'),
    net.uplinks.map(u => h('option', { value: u.name, selected: u.name === pg.uplink },
      `${u.name}${u.description ? ' – ' + u.description : ''}${u.hasAddress ? ' (hat IP: nur mit VLAN)' : ''}${u.status === 'no carrier' ? ' · kein Link' : ''}`)));
  const vlan = h('input', { type: 'number', min: 0, max: 4094, value: pg.vlan || 0 });
  const gw = h('input', { value: pg.gateway || '', placeholder: '10.20.0.1/24', class: 'mono' });
  const nat = checkbox('NAT ins Uplink-Netz (egress)', pg.nat);
  const dhcp = checkbox('DHCP-Server auf dem Gateway', pg.dhcp, 'Feste Adressen der VMs werden als Reservierung eingetragen.');
  const from = h('input', { value: pg.rangeFrom || '', placeholder: '10.20.0.200', class: 'mono' });
  const to = h('input', { value: pg.rangeTo || '', placeholder: '10.20.0.250', class: 'mono' });
  const dns = checkbox('DNS über unbound auf dem Gateway', pg.dns, 'A- und PTR-Einträge für alle VMs mit fester Adresse.');
  const domain = h('input', { value: pg.domain || '', placeholder: 'dmz.ctw.lan', class: 'mono' });
  const filter = checkbox('pf auch zwischen VMs dieses Switches (link1)', pg.filter, 'Ohne diese Option filtert pf nur Verkehr über Gateway und Uplink.');
  const locked = checkbox('Gefälschte Absender-MACs verwerfen (locked lladdr)', pg.locked);
  const sync = () => {
    const l3 = gw.value.trim() !== '';
    for (const c of [nat, dhcp, dns]) c.input.disabled = !l3;
    from.disabled = to.disabled = !l3 || !dhcp.input.checked;
    domain.disabled = !l3 || !dns.input.checked;
    vlan.disabled = !uplink.value;
  };
  for (const el of [gw, dhcp.input, dns.input, uplink]) el.addEventListener('input', sync);
  sync();
  formDialog({
    title: edit ? `Portgruppe ${pg.name}` : 'Portgruppe anlegen', meta: 'VEB · VLAN · VPORT', wide: true, submit: edit ? 'Übernehmen' : 'Anlegen',
    text: 'Erzeugt hostname.if-Dateien für Switch, VLAN und Gateway, den Switch in vm.conf sowie DHCP-, DNS- und pf-Einträge. Laufende VMs bleiben verbunden.',
    fields: [
      field('Name (Switch in vm.conf)', name), field('Beschreibung', descr),
      field('Uplink', uplink, 'Physisches Interface. Ungetaggt nur, wenn es keine eigene IP-Adresse hat.'),
      field('VLAN-ID', vlan, '0 = ungetaggt. Sonst entsteht vlan<ID> auf dem Uplink.'),
      h('div', { class: 'wide' }, field('Gateway-Adresse (vport)', gw, 'Leer lassen für ein reines L2-Netz ohne Host-Adresse.')),
      nat.el, dhcp.el, field('DHCP-Bereich von', from, 'Leer = nur feste Adressen.'), field('bis', to),
      dns.el, field('DNS-Domain', domain),
      filter.el, locked.el,
    ],
    onSubmit: async () => {
      const body = {
        name: name.value.trim(), description: descr.value.trim(), uplink: uplink.value, vlan: uplink.value ? Number(vlan.value) || 0 : 0,
        gateway: gw.value.trim(), nat: nat.input.checked && !nat.input.disabled, dhcp: dhcp.input.checked && !dhcp.input.disabled,
        rangeFrom: from.disabled ? '' : from.value.trim(), rangeTo: to.disabled ? '' : to.value.trim(),
        dns: dns.input.checked && !dns.input.disabled, domain: domain.disabled ? '' : domain.value.trim(),
        filter: filter.input.checked, locked: locked.input.checked,
      };
      const r = edit ? await put(`/api/vnet/portgroups/${enc(pg.name)}`, body) : await post('/api/vnet/portgroups', body);
      watchJob(r.job);
      onDone?.();
    },
  });
}

// ---- Anschlüsse einer VM ----

export async function nicDialog(vm, onDone) {
  const net = await get('/api/vnet');
  if (!net.portGroups.length) throw new Error('Es gibt noch keine Portgruppe. Lege zuerst unter „VM-Netze“ eine an.');
  const rows = h('div', { class: 'nic-list wide' });
  const addRow = n => {
    const pgSel = h('select', { 'aria-label': 'Portgruppe' }, net.portGroups.map(p => h('option', { value: p.name, selected: p.name === n.switch }, `${p.name}${p.subnet ? ' · ' + p.subnet : ''}`)));
    const mode = h('select', { 'aria-label': 'Adresse' }, h('option', { value: 'auto' }, 'Fest, automatisch'), h('option', { value: 'fixed' }, 'Fest, manuell'), h('option', { value: 'dynamic' }, 'Dynamisch'));
    const ip = h('input', { class: 'mono', placeholder: '10.20.0.15', 'aria-label': 'IPv4-Adresse', value: n.ip || '' });
    const mac = h('input', { class: 'mono', placeholder: 'automatisch', 'aria-label': 'MAC-Adresse', value: n.lladdr || '' });
    const host = h('input', { class: 'mono', placeholder: 'aus VM-Name', 'aria-label': 'Hostname', value: n.hostname || '' });
    mode.value = n.ip ? 'fixed' : n.switch ? 'dynamic' : 'auto';
    const sync = () => {
      const g = net.portGroups.find(p => p.name === pgSel.value);
      const l3 = !!(g && g.gateway);
      mode.options[0].disabled = mode.options[1].disabled = !l3;
      if (!l3) mode.value = 'dynamic';
      ip.hidden = mode.value !== 'fixed';
      host.disabled = mode.value === 'dynamic';
    };
    pgSel.addEventListener('change', sync);
    mode.addEventListener('change', sync);
    const rm = h('button', { class: 'mini-btn danger', type: 'button', onclick: () => row.remove() }, 'Entfernen');
    const row = h('div', { class: 'nic-row' },
      field('Portgruppe', pgSel), field('Adresse', h('div', { class: 'stack' }, mode, ip)), field('Hostname', host), field('MAC', mac), rm);
    row.spec = () => ({ switch: pgSel.value, lladdr: mac.value.trim(), addr: mode.value === 'fixed' ? ip.value.trim() : mode.value, hostname: host.disabled ? '' : host.value.trim() });
    sync();
    rows.append(row);
  };
  net.nics.filter(n => n.vm === vm).forEach(addRow);
  const add = h('button', { class: 'mini-btn', type: 'button', onclick: () => addRow({}) }, 'Anschluss hinzufügen');
  formDialog({
    title: `Netzwerk von ${vm}`, meta: 'VM.CONF · DHCP · DNS', wide: true, submit: 'Speichern',
    text: 'Anschlüsse an Portgruppen mit fester MAC. Feste Adressen werden als DHCP-Reservierung und DNS-Eintrag angelegt; der Gast bezieht sie per DHCP. Änderungen gelten ab dem nächsten Start der VM.',
    fields: [rows, h('div', { class: 'wide' }, add)],
    onSubmit: async () => {
      const r = await put(`/api/vnet/vms/${enc(vm)}/nics`, { nics: [...rows.children].map(r => r.spec()) });
      watchJob(r.job);
      onDone?.();
    },
  });
}

// ---- Firewall einer VM ----

export async function firewallDialog(vm, onDone) {
  const net = await get('/api/vnet');
  const pol = net.policies[vm] || { in: 'allow', out: 'allow' };
  const ips = net.nics.filter(n => n.vm === vm && n.ip).map(n => n.ip);
  const polSel = (v, a, d) => { const s = h('select', null, h('option', { value: 'allow' }, a), h('option', { value: 'deny' }, d)); s.value = v; return s; };
  const pin = polSel(pol.in, 'Alles erlauben', 'Alles sperren, außer Regeln');
  const pout = polSel(pol.out, 'Alles erlauben', 'Alles sperren, außer Regeln');
  const list = h('div', { class: 'rule-list wide' });
  const addRule = r => {
    const sel = (opts, v, label) => { const s = h('select', { 'aria-label': label }, opts.map(([k, t]) => h('option', { value: k }, t))); s.value = v; return s; };
    const dir = sel([['in', 'eingehend'], ['out', 'ausgehend']], r.dir || 'in', 'Richtung');
    const act = sel([['pass', 'erlauben'], ['block', 'sperren']], r.action || 'pass', 'Aktion');
    const proto = sel([['tcp', 'TCP'], ['udp', 'UDP'], ['icmp', 'ICMP'], ['any', 'alle']], r.proto || 'tcp', 'Protokoll');
    const peer = h('input', { class: 'mono', value: r.peer && r.peer !== 'any' ? r.peer : '', placeholder: 'any, 10.0.0.0/24, <lan>, VM', 'aria-label': 'Gegenstelle', list: 'vnet-peers' });
    const port = h('input', { class: 'mono', value: r.port || '', placeholder: '443 · 80,443 · 8000:8080', 'aria-label': 'Port' });
    const log = h('input', { type: 'checkbox', checked: r.log, 'aria-label': 'protokollieren' });
    const descr = h('input', { class: 'rule-descr', value: r.description || '', maxlength: 60, placeholder: 'Beschreibung (erscheint als pf-Label)', 'aria-label': 'Beschreibung' });
    const syncPort = () => { port.disabled = proto.value === 'any' || proto.value === 'icmp'; };
    proto.addEventListener('change', syncPort);
    syncPort();
    const up = h('button', { class: 'mini-btn', type: 'button', 'aria-label': 'nach oben', onclick: () => row.previousElementSibling && list.insertBefore(row, row.previousElementSibling) }, '↑');
    const rm = h('button', { class: 'mini-btn danger', type: 'button', onclick: () => row.remove() }, 'Entfernen');
    const row = h('div', { class: 'rule-row' }, dir, act, proto, peer, port, h('label', { class: 'check' }, log, 'Log'), descr, h('div', { class: 'toolbar' }, up, rm));
    row.rule = () => ({ dir: dir.value, action: act.value, proto: proto.value, peer: peer.value.trim() || 'any', port: port.disabled ? '' : port.value.trim(), log: log.checked, description: descr.value.trim() });
    list.append(row);
  };
  (net.rules[vm] || []).forEach(addRule);
  const add = h('button', { class: 'mini-btn', type: 'button', onclick: () => addRule({}) }, 'Regel hinzufügen');
  const peers = h('datalist', { id: 'vnet-peers' }, h('option', { value: 'any' }), net.vms.filter(v => v !== vm).map(v => h('option', { value: v })));
  formDialog({
    title: `Firewall von ${vm}`, meta: `PF-ANKER MAAR${ips.length ? ' · ' + ips.join(', ') : ''}`, wide: true, submit: 'Speichern und laden',
    text: ips.length
      ? 'Regeln gelten in der angegebenen Reihenfolge (die erste passende entscheidet), danach die Grundregel. Antworten auf erlaubte Verbindungen sind immer erlaubt.'
      : 'Diese VM hat noch keine feste Adresse – Regeln werden gespeichert, greifen aber erst, wenn unter „Netzwerk“ eine feste Adresse vergeben ist.',
    fields: [field('Eingehend (zur VM)', pin), field('Ausgehend (von der VM)', pout), list, h('div', { class: 'wide' }, add, peers)],
    onSubmit: async () => {
      const r = await put(`/api/vnet/vms/${enc(vm)}/firewall`, { policy: { in: pin.value, out: pout.value }, rules: [...list.children].map(x => x.rule()) });
      toast(r.message, r.output);
      onDone?.();
    },
  });
}

// ---- Seite ----

function services(p) {
  const t = [];
  if (p.vlan) t.push(`VLAN ${p.vlan}`);
  if (p.nat) t.push('NAT');
  if (p.dhcp) t.push(p.rangeFrom ? `DHCP ${p.rangeFrom.split('.').pop()}–${p.rangeTo.split('.').pop()}` : 'DHCP fest');
  if (p.dns) t.push(`DNS ${p.domain || ''}`.trim());
  if (p.filter) t.push('pf zwischen VMs');
  if (p.locked) t.push('MAC gesperrt');
  return t.map(x => h('span', { class: 'tag' }, x));
}

function fwSummary(net, vm) {
  const p = net.policies[vm] || { in: 'allow', out: 'allow' };
  const n = (net.rules[vm] || []).length;
  if (p.in === 'allow' && p.out === 'allow' && !n) return h('span', { class: 'sub' }, 'offen');
  return [pill(p.in === 'deny' ? 'ok' : 'off', `ein: ${p.in === 'deny' ? 'gesperrt' : 'offen'}`), ' ', pill(p.out === 'deny' ? 'ok' : 'off', `aus: ${p.out === 'deny' ? 'gesperrt' : 'offen'}`), h('span', { class: 'sub' }, n === 1 ? '1 Regel' : `${n} Regeln`)];
}

export default {
  title: 'VM-Netze',
  async render(main) {
    const body = h('div', { class: 'content' }, loading());
    const newBtn = h('button', { class: 'cta', type: 'button' }, 'Portgruppe anlegen');
    const syncBtn = h('button', { class: 'secondary', type: 'button' }, 'Neu abgleichen');
    main.append(pageHead('NETZWERK', 'VM-Netze', 'Portgruppen auf veb(4) mit VLAN-Uplink und vport-Gateway. Feste Adressen der VMs landen automatisch in dhcpd, unbound und im pf-Anker „maar“.', syncBtn, newBtn), body);
    let net = null, tab = 'pf';
    const genBox = h('div', { class: 'panel-body flush' });
    const tabs = h('div', { class: 'toolbar' });

    async function showGen() {
      const p = await get('/api/vnet/preview');
      const views = { pf: ['pf-Anker', p.pf], dhcpd: ['dhcpd.conf', p.dhcpd], unbound: ['unbound', p.unbound], files: ['Interfaces', Object.entries(p.files).sort().map(([k, v]) => `# ${k}\n${v}`).join('\n')] };
      clear(tabs, Object.entries(views).map(([k, [label]]) => h('button', { class: 'mini-btn', type: 'button', 'aria-pressed': String(k === tab), onclick: () => { tab = k; showGen().catch(toastError); } }, label)));
      clear(genBox, h('pre', { class: 'codeblock' }, views[tab][1]));
    }

    async function load() {
      net = await get('/api/vnet');
      const pgRows = net.portGroups.map(p => h('tr', null,
        h('td', null, h('a', { class: 'name', href: `#/portgroup/${enc(p.name)}` }, p.name), p.description ? h('span', { class: 'sub' }, p.description) : null),
        h('td', { class: 'mono' }, p.bridge, h('span', { class: 'sub' }, p.vlan ? `vlan${p.vlan} auf ${p.uplink}` : p.uplink ? `Uplink ${p.uplink}` : 'nur intern')),
        h('td', { class: 'mono' }, p.subnet || '–', p.gateway ? h('span', { class: 'sub' }, `Gateway ${p.gateway.split('/')[0]} · ${p.vport}`) : h('span', { class: 'sub' }, 'reines L2')),
        h('td', null, services(p)),
        h('td', null, p.vms.length ? p.vms.join(', ') : h('span', { class: 'sub' }, 'keine'), p.free >= 0 && p.subnet ? h('span', { class: 'sub' }, `${p.free} feste Adressen frei`) : null),
        h('td', null, p.present ? pill('ok', 'aktiv') : pill('warn', 'fehlt'), p.managed ? null : h('span', { class: 'sub' }, 'vorgefunden')),
        h('td', { class: 'actions' },
          h('button', { class: 'mini-btn', type: 'button', onclick: () => portGroupDialog(p, load).catch(toastError) }, 'Bearbeiten'),
          h('button', { class: 'mini-btn danger', type: 'button', disabled: p.vms.length > 0, title: p.vms.length ? 'Noch VMs angeschlossen' : null, onclick: async () => {
            if (!await confirm({ title: `Portgruppe ${p.name} löschen?`, text: `${p.bridge}${p.vport ? ', ' + p.vport : ''}${p.vlan ? ', vlan' + p.vlan : ''} werden zerstört, die hostname.if-Dateien gesichert und entfernt.`, ok: 'Löschen', danger: true, typeToConfirm: p.name })) return;
            watchJob((await del(`/api/vnet/portgroups/${enc(p.name)}`)).job);
          } }, 'Löschen'))));

      const byVM = new Map(net.vms.map(v => [v, []]));
      net.nics.forEach(n => byVM.get(n.vm)?.push(n));
      const nicRows = [...byVM].map(([vm, nics]) => h('tr', null,
        h('td', null, h('span', { class: 'name' }, vm)),
        h('td', null, nics.length ? nics.map(n => h('div', null, n.switch, ' ', h('span', { class: 'tag' }, n.lladdr || 'MAC zufällig'))) : h('span', { class: 'sub' }, 'kein Switch-Anschluss')),
        h('td', { class: 'mono' }, nics.map(n => h('div', null, n.ip || h('span', { class: 'sub' }, 'dynamisch')))),
        h('td', { class: 'mono' }, nics.map(n => h('div', null, n.fqdn || n.hostname || '–'))),
        h('td', null, fwSummary(net, vm)),
        h('td', { class: 'actions' },
          h('button', { class: 'mini-btn', type: 'button', onclick: () => nicDialog(vm, load).catch(toastError) }, 'Netzwerk'),
          h('button', { class: 'mini-btn', type: 'button', onclick: () => firewallDialog(vm, load).catch(toastError) }, 'Firewall'))));

      const notes = [];
      if (!net.status.pfAnchor) notes.push(h('div', { class: 'note warn' }, 'pf.conf lädt den Anker „maar“ noch nicht. „Neu abgleichen“ ergänzt ihn und lädt den Regelsatz.'));
      if (net.warnings.length) notes.push(h('div', { class: 'note warn', role: 'status' }, net.warnings.map(w => h('div', null, w))));

      clear(body, notes,
        panel('Portgruppen', `${net.portGroups.length} SWITCHES`, h('div', { class: 'panel-body flush' },
          table(['Name', 'Switch', 'Netz', 'Dienste', 'VMs', 'Status', ''], pgRows, 'Noch keine Portgruppe. Lege eine an, um VMs zu vernetzen.'))),
        panel('VM-Anschlüsse & Firewall', 'VM.CONF · DHCPD · UNBOUND · PF', h('div', { class: 'panel-body flush' },
          table(['VM', 'Portgruppe / MAC', 'Adresse', 'DNS-Name', 'Firewall', ''], nicRows, 'Keine VMs definiert.'))),
        panel('Erzeugte Konfiguration', 'VORSCHAU', genBox, tabs));
      await showGen();
    }

    newBtn.addEventListener('click', () => portGroupDialog(null, load).catch(toastError));
    syncBtn.addEventListener('click', () => busy(syncBtn, async () => watchJob((await post('/api/vnet/sync')).job)).catch(toastError));
    const off = onJobDone(() => setTimeout(() => load().catch(() => {}), 300));
    await load();
    return off;
  },
};
