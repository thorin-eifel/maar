// Kleine Live-Vorschau der seriellen VM-Konsole (4:3, nur lesend). Ein Klick öffnet die
// Remote-Sitzung in einem neuen Browsertab.
import { h } from '../ui.js';
import { wsURL } from '../api.js';
import { icon } from '../icons.js';
import { termTheme } from './console.js';

const COLS = 80, ROWS = 30;

export function consolePreview(name) {
  const href = `#/console/vm/${encodeURIComponent(name)}`;
  const dot = h('span', { class: 'dot', 'aria-hidden': 'true' });
  const state = h('span', { class: 'meta', role: 'status' }, 'verbinde …');
  const host = h('div', { class: 'cp-term' });
  const open = h('a', { class: 'cp-open', href, target: '_blank', rel: 'noopener', 'aria-label': `Remote-Sitzung zu ${name} in neuem Tab öffnen` },
    h('span', null, icon('external'), 'Remote-Sitzung öffnen'));
  const stage = h('div', { class: 'cp-stage' }, host, open);
  const el = h('div', { class: 'cp' }, stage, h('div', { class: 'cp-bar' }, dot, state));

  let term = null, ws = null, dead = false, timer = null, ro = null;

  if (!window.Terminal) {
    stage.replaceChildren(h('div', { class: 'empty' }, 'xterm.js konnte nicht geladen werden.'));
    return { el, destroy() {} };
  }
  term = new window.Terminal({ cols: COLS, rows: ROWS, fontSize: 12, scrollback: 0, disableStdin: true, cursorBlink: false, cursorStyle: 'underline',
    fontFamily: 'ui-monospace, SFMono-Regular, "SF Mono", Menlo, monospace', theme: termTheme() });
  term.open(host);
  // Die Terminalgröße ist fest (80×30); auf die Kachelbreite herunterskalieren.
  const fit = () => {
    const w = host.firstElementChild?.offsetWidth || host.offsetWidth;
    if (w) host.style.transform = `scale(${stage.clientWidth / w})`;
  };
  ro = new ResizeObserver(fit);
  ro.observe(stage);
  requestAnimationFrame(fit);

  const set = (s, text) => { dot.className = 'dot' + (s === 'live' ? ' live' : s === 'dead' ? ' dead' : ''); state.textContent = text; };
  function connect() {
    if (dead) return;
    ws = new WebSocket(wsURL(`/ws/console/vm/${encodeURIComponent(name)}?watch=1`));
    ws.binaryType = 'arraybuffer';
    set('', 'verbinde …');
    ws.onopen = () => set('live', 'live · nur lesend');
    ws.onmessage = e => {
      if (typeof e.data === 'string') return;
      term.write(new Uint8Array(e.data));
    };
    ws.onclose = () => {
      if (dead) return;
      set('dead', 'getrennt – neuer Versuch …');
      timer = setTimeout(connect, 5000);
    };
  }
  connect();
  const onTheme = () => { term.options.theme = termTheme(); };
  window.addEventListener('themechange', onTheme);

  return {
    el,
    destroy() {
      dead = true;
      clearTimeout(timer);
      window.removeEventListener('themechange', onTheme);
      ro.disconnect();
      if (ws) ws.close();
      term.dispose();
    },
  };
}
