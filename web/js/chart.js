// Zeitreihen-Diagramm als SVG: 2px-Linien, Fläche mit 10 % Deckkraft bei einer Reihe,
// Haarlinien-Raster, Fadenkreuz mit Tooltip (Maus und Pfeiltasten), Tabellenansicht.
import { h, clear, bytes } from './ui.js';

const NS = 'http://www.w3.org/2000/svg';
const svg = (tag, attrs = {}) => {
  const el = document.createElementNS(NS, tag);
  for (const [k, v] of Object.entries(attrs)) el.setAttribute(k, v);
  return el;
};

export function fmtValue(v, unit) {
  if (v === null || v === undefined || Number.isNaN(v)) return '–';
  switch (unit) {
    case 'pct': return v.toFixed(v < 10 ? 1 : 0).replace('.', ',') + ' %';
    case 'bytes': return bytes(v);
    case 'bps': return bytes(v) + '/s';
    default: return v.toFixed(2).replace('.', ',');
  }
}

function niceMax(max, unit) {
  if (unit === 'pct') return max > 50 || max === 0 ? 100 : Math.ceil(max / 10) * 10;
  if (max <= 0) return 1;
  if (unit === 'bytes' || unit === 'bps') {
    // In Binärschritten runden, damit Viertel-Marken glatt sind (512 MB, 1 GB …).
    const base = Math.pow(1024, Math.floor(Math.log(max) / Math.log(1024)));
    for (const m of [1, 2, 4, 8, 16, 32, 64, 128, 256, 512, 1024]) if (m * base >= max) return m * base;
  }
  const exp = Math.pow(10, Math.floor(Math.log10(max)));
  for (const m of [1, 1.5, 2, 2.5, 3, 4, 5, 6, 8, 10]) if (m * exp >= max) return m * exp;
  return 10 * exp;
}

function fmtTime(t, span) {
  const d = new Date(t * 1000);
  if (span > 2 * 86400) return d.toLocaleDateString('de-DE', { day: '2-digit', month: '2-digit' });
  return d.toLocaleTimeString('de-DE', { hour: '2-digit', minute: '2-digit' });
}

// lineChart({ series: [{ key, label }], unit, threshold? }) → { el, update(data, from, to), loading(on) }
export function lineChart({ series, unit = 'num', threshold, height = 190, label }) {
  const wrap = h('div', { class: 'chart', tabindex: '0', role: 'img', 'aria-label': label || series.map(s => s.label).join(', ') });
  const plot = h('div', { class: 'chart-plot' });
  const tip = h('div', { class: 'chart-tip glass', hidden: true, role: 'status' });
  const legend = series.length > 1 ? h('div', { class: 'chart-legend' }, series.map((s, i) => h('span', null, h('i', { class: `key s${i + 1}` }), s.label))) : null;
  const tableBtn = h('button', { class: 'chart-table-btn', type: 'button', 'aria-pressed': 'false' }, 'Tabelle');
  const tableBox = h('div', { class: 'chart-table', hidden: true });
  wrap.append(legend || h('span'), plot, tip);
  const box = h('div', { class: 'chart-box' }, h('div', { class: 'chart-tools' }, tableBtn), wrap, tableBox);

  let data = {}, from = 0, to = 0, idx = -1, xs = [], geom = null;
  tableBtn.addEventListener('click', () => {
    const on = tableBox.hidden;
    tableBox.hidden = !on;
    tableBtn.setAttribute('aria-pressed', String(on));
    if (on) renderTable();
  });

  function renderTable() {
    const times = xs.length > 60 ? xs.filter((_, i) => i % Math.ceil(xs.length / 60) === 0) : xs;
    clear(tableBox, h('div', { class: 'table-wrap' }, h('table', { class: 'table' },
      h('thead', null, h('tr', null, h('th', null, 'Zeit'), series.map(s => h('th', { class: 'num' }, s.label)))),
      h('tbody', null, [...times].reverse().map(t => h('tr', null,
        h('td', null, new Date(t * 1000).toLocaleString('de-DE', { dateStyle: 'short', timeStyle: 'short' })),
        series.map(s => h('td', { class: 'num' }, fmtValue(valueAt(s.key, t), unit)))))))));
  }

  function valueAt(key, t) {
    const pts = data[key] || [];
    let lo = 0, hi = pts.length - 1;
    while (lo < hi) { const m = (lo + hi) >> 1; if (pts[m].t < t) lo = m + 1; else hi = m; }
    return pts[lo] && Math.abs(pts[lo].t - t) <= 1 ? pts[lo].v : null;
  }

  function draw() {
    const W = plot.clientWidth || 600, H = height;
    const m = { l: 56, r: 12, t: 10, b: 24 };
    const iw = W - m.l - m.r, ih = H - m.t - m.b;
    const all = series.flatMap(s => (data[s.key] || []).map(p => p.v));
    const ymax = niceMax(Math.max(threshold ?? 0, ...all, 0), unit);
    const x = t => m.l + (to === from ? 0 : (t - from) / (to - from) * iw);
    const y = v => m.t + ih - Math.min(v, ymax) / ymax * ih;
    const s = svg('svg', { width: W, height: H, viewBox: `0 0 ${W} ${H}`, 'aria-hidden': 'true' });

    // Raster und y-Achse
    for (let i = 0; i <= 4; i++) {
      const v = ymax * i / 4, yy = Math.round(y(v)) + 0.5;
      s.append(svg('line', { x1: m.l, x2: W - m.r, y1: yy, y2: yy, class: i ? 'grid' : 'axis' }));
      const tx = svg('text', { x: m.l - 8, y: yy + 4, 'text-anchor': 'end', class: 'tick' });
      tx.textContent = fmtValue(v, unit).replace(',0 ', ' ');
      s.append(tx);
    }
    // x-Achse: eine Marke je ~110 px, damit sich Beschriftungen nie berühren
    const span = to - from;
    const n = Math.max(2, Math.min(6, Math.floor(iw / 110)));
    for (let i = 0; i <= n; i++) {
      const t = from + span * i / n;
      const tx = svg('text', { x: x(t), y: H - 6, 'text-anchor': i === 0 ? 'start' : i === n ? 'end' : 'middle', class: 'tick' });
      tx.textContent = fmtTime(t, span);
      s.append(tx);
    }
    if (threshold !== undefined) {
      const yy = Math.round(y(threshold)) + 0.5;
      s.append(svg('line', { x1: m.l, x2: W - m.r, y1: yy, y2: yy, class: 'threshold' }));
    }

    // Lücken (fehlende Messungen) unterbrechen die Linie.
    const times = new Set();
    series.forEach((se, i) => {
      const pts = data[se.key] || [];
      if (!pts.length) return;
      const steps = pts.slice(1).map((p, j) => p.t - pts[j].t).sort((a, b) => a - b);
      const gap = (steps[steps.length >> 1] || 10) * 3;
      let d = '', area = '', segStart = null;
      pts.forEach((p, j) => {
        times.add(p.t);
        const brk = j === 0 || p.t - pts[j - 1].t > gap;
        if (brk && segStart !== null && series.length === 1) area += `L${x(pts[j - 1].t)},${y(0)}L${x(segStart)},${y(0)}Z`;
        if (brk) segStart = p.t;
        d += `${brk ? 'M' : 'L'}${x(p.t).toFixed(1)},${y(p.v).toFixed(1)}`;
        if (series.length === 1) area += `${brk ? 'M' + x(p.t).toFixed(1) + ',' + y(0) + 'L' : 'L'}${x(p.t).toFixed(1)},${y(p.v).toFixed(1)}`;
      });
      if (series.length === 1 && segStart !== null) area += `L${x(pts[pts.length - 1].t)},${y(0)}L${x(segStart)},${y(0)}Z`;
      if (area) s.append(svg('path', { d: area, class: `area s${i + 1}` }));
      s.append(svg('path', { d, class: `line s${i + 1}` }));
    });
    xs = [...times].sort((a, b) => a - b);
    const cross = svg('line', { y1: m.t, y2: m.t + ih, class: 'cross', visibility: 'hidden' });
    const dots = series.map((_, i) => svg('circle', { r: 4, class: `dot s${i + 1}`, visibility: 'hidden' }));
    s.append(cross, ...dots);
    geom = { x, y, m, iw, cross, dots, W };
    plot.replaceChildren(s);
    if (!xs.length) plot.append(h('div', { class: 'chart-empty' }, 'Noch keine Messwerte für diesen Zeitraum.'));
    if (idx >= 0) show(Math.min(idx, xs.length - 1));
    if (!tableBox.hidden) renderTable();
  }

  function show(i) {
    if (!geom || !xs.length) return;
    idx = Math.max(0, Math.min(i, xs.length - 1));
    const t = xs[idx], px = geom.x(t);
    geom.cross.setAttribute('x1', px); geom.cross.setAttribute('x2', px); geom.cross.setAttribute('visibility', 'visible');
    const rows = series.map((se, k) => {
      const v = valueAt(se.key, t);
      const dot = geom.dots[k];
      if (v === null) dot.setAttribute('visibility', 'hidden');
      else { dot.setAttribute('cx', px); dot.setAttribute('cy', geom.y(v)); dot.setAttribute('visibility', 'visible'); }
      return h('div', { class: 'tip-row' }, h('i', { class: `key s${k + 1}` }), h('strong', null, fmtValue(v, unit)), h('span', null, se.label));
    });
    clear(tip, h('div', { class: 'tip-time' }, new Date(t * 1000).toLocaleString('de-DE', { dateStyle: 'short', timeStyle: 'medium' })), rows);
    tip.hidden = false;
    const left = px + 14 + 180 > geom.W ? px - 194 : px + 14;
    tip.style.left = left + 'px';
  }

  function hide() {
    idx = -1;
    tip.hidden = true;
    if (geom) { geom.cross.setAttribute('visibility', 'hidden'); geom.dots.forEach(d => d.setAttribute('visibility', 'hidden')); }
  }

  plot.addEventListener('pointermove', e => {
    if (!geom || !xs.length) return;
    const r = plot.getBoundingClientRect(), px = e.clientX - r.left;
    let best = 0, bd = Infinity;
    xs.forEach((t, i) => { const dd = Math.abs(geom.x(t) - px); if (dd < bd) { bd = dd; best = i; } });
    show(best);
  });
  plot.addEventListener('pointerleave', hide);
  wrap.addEventListener('keydown', e => {
    if (!xs.length) return;
    if (e.key === 'ArrowLeft' || e.key === 'ArrowRight') {
      e.preventDefault();
      show((idx < 0 ? xs.length - 1 : idx) + (e.key === 'ArrowLeft' ? -1 : 1) * (e.shiftKey ? 10 : 1));
    } else if (e.key === 'Escape') hide();
  });
  wrap.addEventListener('blur', hide);

  const ro = new ResizeObserver(() => draw());
  ro.observe(plot);
  window.addEventListener('themechange', draw);

  return {
    el: box,
    update(d, f, t) { data = d; from = f; to = t; wrap.classList.remove('is-loading'); draw(); },
    loading(on) { wrap.classList.toggle('is-loading', on); },
    destroy() { ro.disconnect(); window.removeEventListener('themechange', draw); },
  };
}
