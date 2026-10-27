// Mehrere Hosts: Überblick über alle verbundenen maard-Instanzen (Peers).
// Verwaltet wird jeder Host lokal über seine eigene Oberfläche.
import { h, clear, pageHead, panel, table, pill, metric, loading, toast, toastError, confirm, formDialog, field, busy, dateTime, bytes, pct, duration } from '../ui.js';
import { get, post, del, session, q } from '../api.js';
import { lineChart } from '../chart.js';

const STATUS = { ok: ['ok', 'verbunden'], offline: ['bad', 'nicht erreichbar'], auth: ['warn', 'Token abgelehnt'], cert: ['bad', 'Zertifikat geändert'] };
const KIND = { aktion: ['info', 'Aktion'], anmeldung: ['off', 'Anmeldung'], auftrag: ['ok', 'Auftrag'], verweigert: ['warn', 'verweigert'] };

function addDialog(onDone) {
  const url = h('input', { required: true, placeholder: 'https://kuestenwache.ctw.lan:9443', class: 'mono' });
  const name = h('input', { required: true, placeholder: 'kuestenwache', maxlength: 40 });
  const token = h('input', { required: true, class: 'mono', autocomplete: 'off', spellcheck: 'false', placeholder: 'maar_…' });
  const fp = h('code', { class: 'mono fp' }, '–');
  const confirmBox = h('input', { type: 'checkbox' });
  const confirmRow = h('label', { class: 'check wide', hidden: true }, confirmBox, 'Der Fingerabdruck stimmt mit dem Zertifikat überein, das der Host auf seiner Seite zeigt.');
  let print = '';
  const probe = h('button', { class: 'mini-btn', type: 'button' }, 'Fingerabdruck holen');
  probe.addEventListener('click', () => busy(probe, async () => {
    const r = await post('/api/peers/probe', { url: url.value.trim() });
    print = r.fingerprint;
    fp.textContent = print ? print.match(/../g).join(':') : 'kein TLS (Loopback)';
    confirmRow.hidden = !print;
  }).catch(toastError));
  formDialog({
    title: 'Host verbinden', meta: 'PEER · API-TOKEN', wide: true, submit: 'Verbinden',
    text: 'Lege auf dem anderen Host unter „Zugriffssteuerung“ ein API-Token an. Es darf nur die Zusammenfassung und Messwerte lesen. Das Zertifikat des Hosts wird über seinen Fingerabdruck festgelegt.',
    fields: [field('Adresse', url), field('Anzeigename', name), h('div', { class: 'wide' }, field('API-Token', token)),
      h('div', { class: 'wide' }, probe, ' ', fp), confirmRow],
    onSubmit: async () => {
      if (print && !confirmBox.checked) throw new Error('Bitte den Fingerabdruck bestätigen.');
      await post('/api/peers', { url: url.value.trim(), name: name.value.trim(), token: token.value.trim(), fingerprint: print });
      toast('Host verbunden.');
      onDone();
    },
  });
}

export default {
  title: 'Hosts',
  async render(main, parts) {
    const body = h('div', { class: 'content' }, loading());
    const addBtn = h('button', { class: 'cta', type: 'button' }, 'Host verbinden');
    const detail = parts[0];
    main.append(pageHead('INVENTAR', detail ? 'Host' : 'Hosts', 'Alle verbundenen maard-Instanzen auf einen Blick. Die Verwaltung eines Hosts bleibt lokal – „Öffnen“ führt zu seiner eigenen Oberfläche.', session.role === 'admin' ? addBtn : null), body);
    let timer = null, charts = [];

    async function load() {
      const d = await get('/api/hosts');
      if (detail) return drawDetail(d);
      drawOverview(d);
    }

    function drawOverview(d) {
      const hosts = d.hosts;
      const ok = hosts.filter(x => x.status === 'ok');
      const running = ok.reduce((n, x) => n + x.summary.vms.filter(v => v.state === 'running').length, 0);
      const total = ok.reduce((n, x) => n + x.summary.vms.length, 0);
      const alerts = ok.reduce((n, x) => n + x.summary.alerts.length + x.summary.failed.length, 0);
      const rows = hosts.map(x => {
        const s = x.summary;
        const [k, t] = STATUS[x.status] || ['warn', x.status];
        return h('tr', null,
          h('td', null, h('a', { class: 'name', href: `#/hosts/${encodeURIComponent(x.id)}` }, x.name || x.id), h('span', { class: 'sub' }, x.local ? 'dieser Host' : (x.url || 'Demo'))),
          h('td', null, pill(k, t), x.error ? h('span', { class: 'sub' }, x.error) : null),
          h('td', { class: 'num' }, s ? pct(s.cpu) : '–'),
          h('td', { class: 'num' }, s ? pct(s.memTotal ? s.memActive / s.memTotal * 100 : 0) : '–'),
          h('td', { class: 'num' }, s ? `${s.vms.filter(v => v.state === 'running').length} / ${s.vms.length}` : '–'),
          h('td', null, s ? (s.alerts.length + s.failed.length ? pill('warn', `${s.alerts.length + s.failed.length} aktiv`) : pill('ok', 'ruhig')) : '–'),
          h('td', { class: 'mono' }, s ? `${s.os} ${s.release} · ${s.version}` : '–'),
          h('td', { class: 'actions' },
            h('a', { class: 'mini-btn', href: `#/hosts/${encodeURIComponent(x.id)}` }, 'Details'),
            x.url ? h('a', { class: 'mini-btn', href: x.url, target: '_blank', rel: 'noopener noreferrer' }, 'Öffnen') : null,
            !x.local && session.role === 'admin' ? h('button', { class: 'mini-btn danger', type: 'button', onclick: async () => {
              if (!await confirm({ title: `${x.name} entfernen?`, text: 'Der Host wird aus dieser Übersicht gelöst. Auf dem Host selbst ändert sich nichts; sein Token bleibt gültig, bis du es dort löschst.', ok: 'Entfernen', danger: true })) return;
              await del(`/api/peers/${encodeURIComponent(x.id)}`); await load();
            } }, 'Entfernen') : null));
      });
      const events = ok.flatMap(x => (x.summary.events || []).map(e => ({ ...e, host: x.name || x.id }))).sort((a, b) => b.time - a.time).slice(0, 40);
      clear(body,
        h('div', { class: 'metrics' },
          metric('Hosts', `${ok.length} / ${hosts.length}`, 'erreichbar'),
          metric('VMs aktiv', `${running} / ${total}`, 'über alle Hosts'),
          metric('Aktive Meldungen', String(alerts), 'Alarme und ausgefallene Dienste'),
          metric('Nicht erreichbar', String(hosts.length - ok.length), hosts.length - ok.length ? 'Verbindung oder Token prüfen' : 'alle Hosts antworten')),
        panel('Hosts', 'PEERS', h('div', { class: 'panel-body flush' }, table(['Host', 'Status', { label: 'CPU', num: true }, { label: 'RAM', num: true }, { label: 'VMs', num: true }, 'Meldungen', 'System', ''], rows))),
        panel('Ereignisse aller Hosts', 'LETZTE 40', h('div', { class: 'panel-body flush' }, table(['Zeit', 'Host', 'Benutzer', 'Art', 'Ereignis', 'Ergebnis'], events.map(e => h('tr', null,
          h('td', { class: 'mono' }, dateTime(e.time)), h('td', null, h('span', { class: 'name' }, e.host)), h('td', null, e.user),
          h('td', null, pill(...(KIND[e.kind] || ['off', e.kind]))), h('td', { class: 'mono' }, e.text),
          h('td', null, e.result === 'ok' ? pill('ok', 'ok') : [pill('bad', 'Fehler'), e.error ? h('span', { class: 'sub' }, e.error) : null]))), 'Keine Ereignisse.'))));
    }

    let range = '1h';
    async function drawDetail(d) {
      const x = d.hosts.find(y => y.id === detail);
      if (!x) { clear(body, h('div', { class: 'empty' }, 'Host nicht gefunden. ', h('a', { href: '#/hosts' }, 'Zur Übersicht'))); return; }
      const s = x.summary;
      const [k, t] = STATUS[x.status] || ['warn', x.status];
      const seg = h('div', { class: 'segment', role: 'group', 'aria-label': 'Zeitraum' }, [['1h', '1 Std'], ['24h', '24 Std'], ['7d', '7 Tage']].map(([r, l]) => {
        const b = h('button', { type: 'button', 'aria-pressed': String(r === range) }, l);
        b.addEventListener('click', () => { range = r; for (const bb of seg.children) bb.setAttribute('aria-pressed', String(bb === b)); drawCharts(); });
        return b;
      }));
      const cpu = lineChart({ series: [{ key: 'host.cpu', label: 'CPU' }], unit: 'pct', label: `${x.name}: CPU-Auslastung` });
      const mem = lineChart({ series: [{ key: 'host.mem', label: 'Speicher aktiv' }], unit: 'pct', label: `${x.name}: aktiver Speicher` });
      charts = [cpu, mem];
      async function drawCharts() {
        if (x.status !== 'ok') return;
        charts.forEach(c => c.loading(true));
        try {
          const m = await get(`/api/hosts/${encodeURIComponent(x.id)}/metrics` + q({ range, keys: 'host.cpu,host.mem' }));
          cpu.update({ 'host.cpu': m.series['host.cpu'] || [] }, m.from, m.to);
          mem.update({ 'host.mem': m.series['host.mem'] || [] }, m.from, m.to);
        } catch (e) { toastError(e); charts.forEach(c => c.loading(false)); }
      }
      const link = x.url ? h('a', { class: 'mini-btn', href: x.url, target: '_blank', rel: 'noopener noreferrer' }, 'Host öffnen') : null;
      clear(body,
        h('div', { class: 'toolbar' }, h('a', { class: 'mini-btn', href: '#/hosts' }, '← Alle Hosts'), pill(k, t), link),
        x.error ? h('div', { class: 'note bad', role: 'alert' }, x.error) : null,
        s ? [
          h('div', { class: 'metrics' },
            metric('CPU-Last', pct(s.cpu), `${s.ncpu} Kerne`, s.cpu),
            metric('Speicher aktiv', bytes(s.memActive), `von ${bytes(s.memTotal)}`, s.memTotal ? s.memActive / s.memTotal * 100 : 0),
            metric('Laufzeit', duration(s.uptime), `${s.os} ${s.release} (${s.machine})`),
            metric('maard', s.version, s.hostname)),
          s.alerts.length || s.failed.length ? h('div', { class: 'note bad', role: 'alert' }, h('strong', null, 'Aktive Meldungen: '), [...s.alerts.map(a => `${a.name} (${a.subject})`), ...s.failed.map(f => `Dienst ${f} ausgefallen`)].join(', ')) : null,
          h('div', { class: 'grid-2' },
            panel('CPU-Auslastung', 'HOST.CPU', h('div', { class: 'panel-body' }, cpu.el), seg),
            panel('Speicher', 'HOST.MEM', h('div', { class: 'panel-body' }, mem.el))),
          panel('Virtuelle Maschinen', `${s.vms.length} VMS`, h('div', { class: 'panel-body flush' }, table(['Name', 'Status', { label: 'vCPUs', num: true }, { label: 'RAM', num: true }],
            s.vms.map(v => h('tr', null, h('td', null, h('span', { class: 'name' }, v.name)), h('td', null, pill(v.state === 'running' ? 'ok' : 'off', v.state === 'running' ? 'läuft' : v.state)), h('td', { class: 'num' }, v.vcpus), h('td', { class: 'num' }, v.maxmem))), 'Keine VMs.'))),
        ] : null);
      drawCharts();
    }

    addBtn.addEventListener('click', () => addDialog(() => load().catch(toastError)));
    await load();
    timer = setInterval(() => { if (!document.querySelector('dialog')) load().catch(() => {}); }, 15000);
    return () => { clearInterval(timer); charts.forEach(c => c.destroy()); };
  },
};
