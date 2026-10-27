import { h, clear, pageHead, panel, table, loading, toast, toastError, confirm, formDialog, field, busy, pill, dateTime } from '../ui.js';
import { get, post, put, del, q } from '../api.js';
import { lineChart, fmtValue } from '../chart.js';

const RANGES = [['1h', '1 Std'], ['6h', '6 Std'], ['24h', '24 Std'], ['7d', '7 Tage']];

const METRICS = [
  ['host.cpu', 'Host: CPU-Auslastung', 'pct'], ['host.mem', 'Host: aktiver Speicher', 'pct'], ['host.load', 'Host: Load (1 min)', 'num'],
  ['host.net.rx', 'Host: Netz empfangen', 'bps'], ['host.net.tx', 'Host: Netz gesendet', 'bps'], ['host.fs.*', 'Jedes Dateisystem: Belegung', 'pct'],
  ['vm.*.cpu', 'Jede VM: CPU (100 % = ein Kern)', 'pct'], ['vm.*.mem', 'Jede VM: Speicher', 'bytes'],
  ['vm.*.rx', 'Jede VM: Netz empfangen', 'bps'], ['vm.*.tx', 'Jede VM: Netz gesendet', 'bps'],
];
const unitOf = key => (METRICS.find(([k]) => k === key || new RegExp('^' + k.replace(/\./g, '\\.').replace('*', '[^.]+') + '$').test(key)) || [, , 'num'])[2];
const metricLabel = key => (METRICS.find(([k]) => k === key) || [, key])[1];
const fmtDur = s => !s ? 'sofort' : s < 3600 ? `${Math.round(s / 60)} Min` : `${(s / 3600).toString().replace('.', ',')} Std`;

export default {
  title: 'Leistung',
  async render(main) {
    const body = h('div', { class: 'content' });
    main.append(pageHead('02 / ÜBERBLICK', 'Leistung & Alarme', 'Verlauf von CPU, Speicher und Netz für Host und VMs – zwei Stunden in 10-Sekunden-Schritten, sieben Tage als 5-Minuten-Mittel. Alarme per Mail über den lokalen smtpd.'), body);

    let range = '1h', object = 'host', charts = [], timer = null, alertData = null;
    const objSel = h('select', { class: 'input', 'aria-label': 'Objekt' }, h('option', { value: 'host' }, 'Host'));
    objSel.style.width = 'auto';
    const seg = h('div', { class: 'segment', role: 'group', 'aria-label': 'Zeitraum' });
    const grid = h('div', { class: 'grid-2' });
    const fsBox = h('div', { class: 'panel-body' });
    const alertBox = h('div', { class: 'content-stack' });

    function drawSeg() {
      clear(seg, RANGES.map(([k, l]) => {
        const b = h('button', { type: 'button', 'aria-pressed': String(k === range) }, l);
        b.addEventListener('click', () => { range = k; drawSeg(); refresh(); schedule(); });
        return b;
      }));
    }

    function chartPanel(title, meta, cfg, nowKeys) {
      const c = lineChart({ ...cfg, label: title });
      const now = h('span', { class: 'stat-now' }, '–');
      charts.push({ c, keys: cfg.series.map(s => s.key), now, nowKeys, unit: cfg.unit });
      return panel(title, meta, h('div', { class: 'panel-body' }, c.el), now);
    }

    function threshold(metric) {
      const r = alertData?.config.rules.find(x => x.enabled && x.metric === metric && x.op === '>');
      return r ? r.threshold : undefined;
    }

    function build() {
      charts.forEach(x => x.c.destroy());
      charts = [];
      if (object === 'host') {
        clear(grid,
          chartPanel('CPU-Auslastung', 'HOST · KERN.CP_TIME', { series: [{ key: 'host.cpu', label: 'CPU' }], unit: 'pct', threshold: threshold('host.cpu') }),
          chartPanel('Aktiver Speicher', 'HOST · VMSTAT', { series: [{ key: 'host.mem', label: 'Speicher' }], unit: 'pct', threshold: threshold('host.mem') }),
          chartPanel('Netzwerk', 'PHYSISCHE SCHNITTSTELLEN', { series: [{ key: 'host.net.rx', label: 'Empfangen' }, { key: 'host.net.tx', label: 'Gesendet' }], unit: 'bps' }),
          chartPanel('Load-Average', 'VM.LOADAVG · 1 MIN', { series: [{ key: 'host.load', label: 'Load' }], unit: 'num' }));
      } else {
        const vm = object;
        clear(grid,
          chartPanel('CPU', `VM ${vm} · 100 % = EIN KERN`, { series: [{ key: `vm.${vm}.cpu`, label: 'CPU' }], unit: 'pct' }),
          chartPanel('Speicher', `VM ${vm} · RSS`, { series: [{ key: `vm.${vm}.mem`, label: 'Speicher' }], unit: 'bytes' }),
          chartPanel('Netzwerk', `VM ${vm} · TAP`, { series: [{ key: `vm.${vm}.rx`, label: 'Empfangen' }, { key: `vm.${vm}.tx`, label: 'Gesendet' }], unit: 'bps' }),
          panel('Hinweis', 'VMD', h('div', { class: 'panel-body' }, h('p', null, 'Die CPU-Last ist die des vmd-Prozesses der VM: 100 % entsprechen einem ausgelasteten Kern, eine VM mit vier vCPUs kann bis 400 % erreichen. Disk-I/O pro VM stellt vmd nicht bereit.'))));
      }
    }

    async function refresh() {
      charts.forEach(x => x.c.loading(true));
      const keys = [...new Set(charts.flatMap(x => x.keys))];
      try {
        const [r, fs] = await Promise.all([
          get('/api/metrics' + q({ keys: keys.join(','), range })),
          get('/api/metrics' + q({ keys: 'host.fs.*', range: '1h' })),
        ]);
        for (const x of charts) {
          x.c.update(r.series, r.from, r.to);
          x.now.textContent = x.keys.map(k => fmtValue((r.series[k] || []).at(-1)?.v, x.unit)).join(' / ');
        }
        const rows = Object.entries(fs.series).map(([k, pts]) => [k.slice(8), pts.at(-1)?.v ?? 0]).sort((a, b) => b[1] - a[1]);
        clear(fsBox, rows.length ? rows.map(([mnt, v]) => {
          const i = h('i'); i.style.width = v + '%';
          return h('div', { class: 'fs-row' }, h('span', { class: 'mono' }, mnt),
            h('div', { class: v > 90 ? 'bar crit' : v > 75 ? 'bar warn' : 'bar', role: 'meter', 'aria-valuenow': String(Math.round(v)), 'aria-valuemin': '0', 'aria-valuemax': '100', 'aria-label': `${mnt} belegt` }, i),
            h('span', { class: 'num' }, Math.round(v) + ' %'));
        }) : h('div', { class: 'empty' }, 'Noch keine Werte.'));
      } catch (e) { charts.forEach(x => x.c.loading(false)); }
    }

    function schedule() {
      clearInterval(timer);
      timer = setInterval(refresh, range === '1h' ? 10000 : 60000);
    }

    // ---- Alarme ----

    function ruleDialog(r) {
      const name = h('input', { required: true, maxlength: '60', value: r?.name || '' });
      const known = METRICS.some(([k]) => k === r?.metric);
      const metric = h('select', null, METRICS.map(([k, l]) => h('option', { value: k, selected: r?.metric === k }, l)), r && !known ? h('option', { value: r.metric, selected: true }, r.metric) : null);
      const op = h('select', null, h('option', { value: '>', selected: (r?.op || '>') === '>' }, 'größer als'), h('option', { value: '<', selected: r?.op === '<' }, 'kleiner als'));
      const thr = h('input', { type: 'number', step: 'any', required: true, value: r?.threshold ?? 90 });
      const unitHint = h('span', { class: 'hint' });
      const syncUnit = () => { unitHint.textContent = { pct: 'in Prozent', bps: 'in Bytes pro Sekunde (1 MB/s = 1048576)', bytes: 'in Bytes (1 GB = 1073741824)', num: 'Zahl' }[unitOf(metric.value)]; };
      metric.addEventListener('change', syncUnit);
      syncUnit();
      const dur = h('select', null, [[0, 'sofort'], [60, '1 Minute'], [300, '5 Minuten'], [900, '15 Minuten'], [1800, '30 Minuten'], [3600, '1 Stunde']].map(([v, l]) => h('option', { value: v, selected: (r?.for ?? 300) === v }, l)));
      const sev = h('select', null, h('option', { value: 'warning', selected: r?.severity !== 'critical' }, 'Warnung'), h('option', { value: 'critical', selected: r?.severity === 'critical' }, 'Kritisch'));
      const en = h('input', { type: 'checkbox', checked: r ? r.enabled : true });
      const thrF = field('Schwellwert', thr);
      thrF.append(unitHint);
      formDialog({
        title: r ? `Regel „${r.name}“` : 'Alarmregel anlegen', meta: 'SCHWELLWERT', wide: true, submit: 'Speichern',
        fields: [field('Name', name), field('Messwert', metric), field('Bedingung', op), thrF, field('Mindestdauer', dur, 'so lange muss die Bedingung ununterbrochen gelten'), field('Schwere', sev), h('label', { class: 'check wide' }, en, 'Aktiv')],
        onSubmit: async () => {
          const res = await post('/api/alerts/rules', { id: r?.id || '', name: name.value, metric: metric.value, op: op.value, threshold: Number(thr.value), for: Number(dur.value), severity: sev.value, enabled: en.checked });
          toast(res.message);
          await loadAlerts();
          build(); refresh();
        },
      });
    }

    async function loadAlerts() {
      alertData = await get('/api/alerts');
      const a = alertData;
      const sevPill = s => s === 'critical' ? pill('bad', 'kritisch') : s === 'info' ? pill('info', 'info') : pill('warn', 'warnung');
      const rules = a.config.rules.map(r => {
        const rm = h('button', { class: 'mini-btn danger', type: 'button' }, 'Löschen');
        rm.addEventListener('click', () => busy(rm, async () => {
          if (!await confirm({ title: `Regel „${r.name}“ löschen?`, ok: 'Löschen', danger: true })) return;
          await del(`/api/alerts/rules/${encodeURIComponent(r.id)}`);
          await loadAlerts();
        }).catch(toastError));
        return h('tr', null,
          h('td', { class: 'name' }, r.name),
          h('td', null, metricLabel(r.metric), h('span', { class: 'sub mono' }, r.metric)),
          h('td', null, `${r.op} ${fmtValue(r.threshold, unitOf(r.metric))}`, h('span', { class: 'sub' }, fmtDur(r.for))),
          h('td', null, sevPill(r.severity)),
          h('td', null, r.enabled ? pill('ok', 'aktiv') : pill('off', 'aus')),
          h('td', { class: 'actions' }, h('button', { class: 'mini-btn', type: 'button', onclick: () => ruleDialog(r) }, 'Bearbeiten'), rm));
      });
      const rcpt = h('input', { value: a.config.recipients.join(', '), placeholder: 'root, admin@example.org', autocomplete: 'off' });
      const evs = Object.entries(a.eventKinds).map(([k, l]) => ({ k, cb: h('input', { type: 'checkbox', checked: !!a.config.events[k] }), l }));
      const save = h('button', { class: 'cta', type: 'button' }, 'Speichern');
      save.addEventListener('click', () => busy(save, async () => {
        const r = await put('/api/alerts/settings', { recipients: rcpt.value.split(/[\s,]+/).filter(Boolean), events: Object.fromEntries(evs.map(e => [e.k, e.cb.checked])) });
        toast(r.message);
        await loadAlerts();
      }).catch(toastError));
      const test = h('button', { class: 'secondary', type: 'button' }, 'Testmail senden');
      test.addEventListener('click', () => busy(test, async () => { const r = await post('/api/alerts/test'); toast(r.message); }).catch(toastError));
      const hist = [...a.history].reverse().slice(0, 30).map(e => h('tr', null,
        h('td', null, dateTime(e.time)),
        h('td', null, e.kind === 'firing' ? pill('bad', 'Alarm') : e.kind === 'resolved' ? pill('ok', 'behoben') : pill('warn', 'Ereignis')),
        h('td', null, h('span', { class: 'name' }, e.name), h('span', { class: 'sub' }, e.message)),
        h('td', { class: 'mono' }, e.subject),
        h('td', null, e.mailed ? 'verschickt' : e.mailError ? h('span', { title: e.mailError }, 'Fehler') : '–')));

      clear(alertBox,
        h('div', { class: 'section-title', id: 'alarme' }, h('div', null, h('span', { class: 'section-index' }, 'ALARME'), h('h2', null, a.active.length === 1 ? '1 aktiver Alarm' : a.active.length ? `${a.active.length} aktive Alarme` : 'Keine aktiven Alarme'))),
        a.active.length ? h('div', { class: 'note bad', role: 'alert' }, a.active.map(x => h('div', null, h('strong', null, x.name + ': '), `${x.subject} = ${fmtValue(x.value, unitOf(x.subject))} seit ${dateTime(x.since)}`))) : null,
        panel('Regeln', 'SCHWELLWERTE', h('div', { class: 'panel-body flush' }, table(['Regel', 'Messwert', 'Bedingung', 'Schwere', 'Status', ''], rules, 'Keine Regeln.')),
          h('button', { class: 'mini-btn', type: 'button', onclick: () => ruleDialog(null) }, 'Regel anlegen')),
        h('div', { class: 'grid-2' },
          panel('Benachrichtigung', 'MAIL(1) · SMTPD', h('div', { class: 'panel-body' }, h('div', { class: 'form-grid' },
            h('div', { class: 'wide' }, field('Empfänger', rcpt, 'Lokale Konten (z. B. root) oder Mailadressen; Versand über den lokalen smtpd. Gleiche Meldungen höchstens alle 30 Minuten.')),
            h('fieldset', { class: 'wide field' }, h('legend', null, 'Ereignisse melden'), h('div', { class: 'toolbar' }, evs.map(e => h('label', { class: 'check' }, e.cb, e.l)))),
            h('div', { class: 'toolbar wide' }, save, test)))),
          panel('Verlauf', 'LETZTE MELDUNGEN', h('div', { class: 'panel-body flush' }, table(['Zeit', 'Art', 'Meldung', 'Betreff', 'Mail'], hist, 'Noch keine Meldungen.')))));
    }

    objSel.addEventListener('change', () => { object = objSel.value; build(); refresh(); });
    drawSeg();
    clear(body,
      h('div', { class: 'filters' }, objSel, seg),
      grid,
      panel('Dateisysteme', 'BELEGUNG JETZT', fsBox),
      alertBox);
    const [vms] = await Promise.all([get('/api/vms'), loadAlerts().catch(toastError)]);
    for (const v of vms.overview.vms) objSel.append(h('option', { value: v.name }, `VM ${v.name}${v.state === 'running' ? '' : ' (gestoppt)'}`));
    build();
    await refresh();
    schedule();
    // Nach dem Seitenaufbau springen, sonst setzt der Router die Position zurück.
    if (location.hash.includes('alarme')) setTimeout(() => document.getElementById('alarme')?.scrollIntoView({ block: 'start' }), 100);
    return () => { clearInterval(timer); charts.forEach(x => x.c.destroy()); };
  },
};
