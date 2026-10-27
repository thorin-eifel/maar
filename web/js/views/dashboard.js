import { h, clear, pageHead, panel, metric, table, bytes, duration, pct, pill, vmStatePill, loading } from '../ui.js';
import { get, session } from '../api.js';

export default {
  title: 'Übersicht',
  async render(main) {
    const body = h('div', { class: 'content' }, loading());
    const refreshBtn = h('button', { class: 'secondary', type: 'button' }, 'Aktualisieren');
    main.append(pageHead('ÜBERSICHT', session.hostname, 'Zustand des Hosts, der virtuellen Maschinen und der wichtigsten Dienste auf einen Blick.', refreshBtn), body);

    async function load() {
      const d = await get('/api/overview');
      const s = d.system;
      const memPct = s.memTotal ? s.memActive / s.memTotal * 100 : 0;
      const vms = d.vms || [];
      const running = vms.filter(v => v.state === 'running').length;
      const failed = d.servicesFailed || [];

      const sys = h('dl', { class: 'kv' },
        h('dt', null, 'System'), h('dd', null, `${s.os} ${s.release} (${s.machine})`),
        h('dt', null, 'Kernel'), h('dd', { class: 'mono' }, s.version),
        h('dt', null, 'Prozessor'), h('dd', null, `${s.model} · ${s.ncpu} Kerne online`),
        h('dt', null, 'Arbeitsspeicher'), h('dd', null, bytes(s.physmem)),
        h('dt', null, 'Gestartet'), h('dd', null, s.boottime ? new Date(s.boottime * 1000).toLocaleString('de-DE') : '–'),
        h('dt', null, 'Prozesse'), h('dd', null, s.procs || '–'),
        h('dt', null, 'Gateway'), h('dd', { class: 'mono' }, d.gateway || '–'));

      const vmRows = vms.map(v => h('tr', null,
        h('td', null, h('a', { class: 'name', href: '#/vms' }, v.name)),
        h('td', null, vmStatePill(v.state)),
        h('td', { class: 'num' }, v.maxmem || '–'),
        h('td', { class: 'mono' }, v.tty !== '-' ? v.tty : '–'),
        h('td', { class: 'actions' }, v.state === 'running' ? h('a', { class: 'mini-btn', href: `#/console/vm/${encodeURIComponent(v.name)}` }, 'Konsole') : null)));

      const disks = (d.storage || []).map(m => h('tr', null,
        h('td', { class: 'mono' }, m.mount),
        h('td', { class: 'num' }, bytes(m.used)),
        h('td', { class: 'num' }, bytes(m.size)),
        h('td', null, (() => { const i = h('i'); i.style.width = m.percent + '%'; return h('div', { class: m.percent > 90 ? 'bar crit' : m.percent > 75 ? 'bar warn' : 'bar', 'aria-label': `${m.percent} % belegt` }, i); })()),
        h('td', { class: 'num' }, m.percent + ' %')));

      const ifs = (d.interfaces || []).filter(i => !['lo0', 'enc0', 'pflog0'].includes(i.name)).map(i => h('tr', null,
        h('td', { class: 'name mono' }, i.name, i.description ? h('span', { class: 'sub' }, i.description) : null),
        h('td', null, i.up ? (i.status && i.status !== 'active' ? pill('warn', i.status) : pill('ok', 'aktiv')) : pill('off', 'down')),
        h('td', { class: 'mono' }, i.addrs.filter(a => !a.addr.startsWith('fe80')).map(a => h('div', null, a.addr)))));

      clear(body,
        h('div', { class: 'metrics' },
          metric('CPU-Last', pct(s.cpuBusy), `Load ${(s.load || []).join(' · ')}`, s.cpuBusy),
          metric('Speicher aktiv', bytes(s.memActive), `von ${bytes(s.memTotal)} · Cache ${bytes(s.memCache)}`, memPct),
          metric('Laufzeit', duration(s.uptime), s.swapTotal ? `Swap ${bytes(s.swapUsed)} / ${bytes(s.swapTotal)}` : ''),
          metric('VMs aktiv', `${running} / ${vms.length}`, d.vmdRunning ? 'vmd läuft' : 'vmd ist nicht gestartet')),
        (d.alerts || []).length ? h('div', { class: 'note bad', role: 'alert' }, h('strong', null, d.alerts.length === 1 ? '1 aktiver Alarm: ' : `${d.alerts.length} aktive Alarme: `), d.alerts.map(a => a.name).join(', '), ' – ', h('a', { href: '#/performance?alarme' }, 'zu den Alarmen')) : null,
        failed.length ? h('div', { class: 'note bad', role: 'alert' }, h('strong', null, 'Ausgefallene Dienste: '), failed.join(', '), ' – ', h('a', { href: '#/services' }, 'zu den Diensten')) : null,
        h('div', { class: 'grid-2' },
          panel('System', '01 / HOST', h('div', { class: 'panel-body' }, sys)),
          panel('Virtuelle Maschinen', '02 / VMD', h('div', { class: 'panel-body flush' }, table(['Name', 'Status', { label: 'RAM', num: true }, 'TTY', ''], vmRows, 'Keine VMs definiert.')), h('a', { class: 'mini-btn', href: '#/vms' }, 'Alle VMs'))),
        h('div', { class: 'grid-2' },
          panel('Dateisysteme', '03 / SPEICHER', h('div', { class: 'panel-body flush' }, table(['Einhängepunkt', { label: 'Belegt', num: true }, { label: 'Größe', num: true }, '', { label: '%', num: true }], disks))),
          panel('Netzwerk & Paketfilter', '04 / NETZ', h('div', { class: 'panel-body flush' },
            d.pf ? h('div', { class: 'panel-body' }, h('div', { class: 'toolbar' }, d.pf.enabled ? pill('ok', 'pf aktiv') : pill('bad', 'pf deaktiviert'), h('span', { class: 'tag' }, `${d.pf.rules} Regeln`), h('span', { class: 'tag' }, `${d.pf.states} States`), h('span', { class: 'tag' }, `${d.servicesRunning || 0} Dienste laufen`))) : null,
            table(['Interface', 'Status', 'Adressen'], ifs)))));
    }

    refreshBtn.addEventListener('click', () => load().catch(() => {}));
    await load();
    const t = setInterval(() => load().catch(() => {}), 15000);
    return () => clearInterval(t);
  },
};
