import { h, clear, pageHead, panel, table, vmStatePill, loading, toastError } from '../ui.js';
import { get, wsURL, session } from '../api.js';

export function termTheme() {
  const css = getComputedStyle(document.documentElement);
  return {
    background: css.getPropertyValue('--term-bg').trim() || '#1d3a44',
    foreground: '#eef3f5', cursor: '#e8604c', cursorAccent: '#1d3a44',
    selectionBackground: 'rgba(160,205,215,.45)',
    black: '#1b2b31', red: '#e8604c', green: '#7cc8a4', yellow: '#e6c27a', blue: '#7fb4e6', magenta: '#c49ae0', cyan: '#8fd0dc', white: '#dfe7ea',
    brightBlack: '#5f7a84', brightRed: '#ff8a76', brightGreen: '#9de0bf', brightYellow: '#f3d796', brightBlue: '#a3cbf2', brightMagenta: '#d9b8ef', brightCyan: '#b2e3ec', brightWhite: '#ffffff',
  };
}

// openTerminal verbindet xterm.js mit einem WebSocket-Endpunkt.
function openTerminal(main, { title, meta, path, back, shared }) {
  const host = h('div', { class: 'term-host' });
  const dot = h('span', { class: 'dot', 'aria-hidden': 'true' });
  const status = h('span', { class: 'meta', role: 'status' }, 'verbinde …');
  const reconnect = h('button', { class: 'mini-btn', type: 'button', hidden: true }, 'Neu verbinden');
  const paste = h('button', { class: 'mini-btn', type: 'button' }, 'Einfügen');
  const ctrlc = h('button', { class: 'mini-btn', type: 'button', title: 'Sendet Strg+C' }, '^C');
  const full = h('button', { class: 'mini-btn', type: 'button', 'aria-pressed': 'false' }, 'Vollbild');
  const shell = h('section', { class: 'term-shell', 'aria-label': title },
    h('div', { class: 'term-bar glass' },
      h('div', { class: 'title' }, dot, h('span', null, title), h('span', { class: 'meta' }, meta)),
      status, ctrlc, paste, full, reconnect, h('a', { class: 'mini-btn', href: back }, 'Zurück')),
    host);
  main.append(shell);

  if (!window.Terminal) {
    clear(host, h('div', { class: 'note bad' }, 'xterm.js konnte nicht geladen werden.'));
    return () => {};
  }
  const term = new window.Terminal({
    cursorBlink: true, fontFamily: 'ui-monospace, SFMono-Regular, "SF Mono", Menlo, monospace', fontSize: 14,
    scrollback: 5000, theme: termTheme(), allowProposedApi: false, macOptionIsMeta: true,
  });
  const fit = new window.FitAddon.FitAddon();
  term.loadAddon(fit);
  term.open(host);
  const enc = new TextEncoder();
  let ws = null, closedByUs = false;

  const setState = (s, text) => {
    dot.className = 'dot' + (s === 'live' ? ' live' : s === 'dead' ? ' dead' : '');
    status.textContent = text;
    reconnect.hidden = s !== 'dead';
  };

  const sendResize = () => {
    try { fit.fit(); } catch { /* unsichtbar */ }
    if (ws && ws.readyState === 1) ws.send(JSON.stringify({ type: 'resize', cols: term.cols, rows: term.rows }));
  };

  function connect() {
    try { fit.fit(); } catch { /* egal */ }
    const sep = path.includes('?') ? '&' : '?';
    ws = new WebSocket(wsURL(`${path}${sep}cols=${term.cols}&rows=${term.rows}`));
    ws.binaryType = 'arraybuffer';
    setState('', 'verbinde …');
    ws.onopen = () => { setState('live', shared ? 'verbunden · geteilt' : 'verbunden'); sendResize(); term.focus(); };
    ws.onmessage = e => {
      if (typeof e.data === 'string') {
        const m = JSON.parse(e.data);
        if (m.type === 'info') term.writeln(`\x1b[2m── ${m.message}\x1b[0m`);
        if (m.type === 'closed') term.writeln(`\r\n\x1b[2m── ${m.message}\x1b[0m`);
        return;
      }
      term.write(new Uint8Array(e.data));
    };
    ws.onclose = e => {
      if (closedByUs) return;
      setState('dead', e.code === 1006 ? 'Verbindung verloren' : 'getrennt');
    };
  }

  term.onData(d => ws && ws.readyState === 1 && ws.send(enc.encode(d)));
  term.onBinary(d => ws && ws.readyState === 1 && ws.send(Uint8Array.from(d, c => c.charCodeAt(0))));
  ctrlc.addEventListener('click', () => { ws && ws.readyState === 1 && ws.send(enc.encode('\x03')); term.focus(); });
  paste.addEventListener('click', async () => {
    try {
      const t = await navigator.clipboard.readText();
      if (t && ws && ws.readyState === 1) ws.send(enc.encode(t.replace(/\r?\n/g, '\r')));
    } catch { toastError(new Error('Kein Zugriff auf die Zwischenablage – Einfügen mit Strg/Cmd+V im Terminal.')); }
    term.focus();
  });
  full.addEventListener('click', async () => {
    if (document.fullscreenElement) await document.exitFullscreen();
    else await shell.requestFullscreen();
  });
  const onFs = () => { full.setAttribute('aria-pressed', String(!!document.fullscreenElement)); setTimeout(sendResize, 50); };
  document.addEventListener('fullscreenchange', onFs);
  reconnect.addEventListener('click', connect);
  const ro = new ResizeObserver(() => sendResize());
  ro.observe(host);
  const onTheme = () => { term.options.theme = termTheme(); };
  window.addEventListener('themechange', onTheme);
  connect();

  return () => {
    closedByUs = true;
    ro.disconnect();
    document.removeEventListener('fullscreenchange', onFs);
    window.removeEventListener('themechange', onTheme);
    if (ws) ws.close();
    term.dispose();
  };
}

export default {
  title: 'Konsolen',
  async render(main, parts) {
    if (parts[0] === 'vm' && parts[1]) {
      this.title = `Konsole ${parts[1]}`;
      return openTerminal(main, {
        title: parts[1], meta: 'SERIELLE KONSOLE · VMCTL', path: `/ws/console/vm/${encodeURIComponent(parts[1])}`, back: '#/console', shared: true,
      });
    }
    if (parts[0] === 'shell') {
      this.title = 'Host-Shell';
      return openTerminal(main, { title: `${session.user}@${session.hostname}`, meta: 'LOGIN-SHELL', path: '/ws/console/shell', back: '#/console' });
    }
    this.title = 'Konsolen';
    const body = h('div', { class: 'content' }, loading());
    main.append(pageHead('INVENTAR', 'Konsolen', 'Serielle Konsolen der laufenden VMs (wie vmctl console) und eine Login-Shell auf dem Host – direkt im Browser.',
      h('a', { class: 'cta', href: '#/console/shell' }, 'Host-Shell öffnen')), body);
    const d = await get('/api/vms');
    const rows = d.overview.vms.map(v => h('tr', null,
      h('td', { class: 'name' }, v.name),
      h('td', null, vmStatePill(v.state)),
      h('td', { class: 'mono' }, v.state === 'running' ? `/dev/${v.tty}` : '–'),
      h('td', { class: 'num' }, d.viewers['vm:' + v.name] || 0),
      h('td', { class: 'actions' }, v.state === 'running'
        ? h('a', { class: 'mini-btn', href: `#/console/vm/${encodeURIComponent(v.name)}` }, 'Konsole öffnen')
        : h('a', { class: 'mini-btn', href: '#/vms' }, 'Zur VM'))));
    clear(body,
      panel('VM-Konsolen', 'VMD · 115200 BAUD', h('div', { class: 'panel-body flush' }, table(['VM', 'Status', 'Gerät', { label: 'Zuschauer', num: true }, ''], rows, 'Keine VMs vorhanden.'))),
      h('div', { class: 'note' }, 'Konsolen werden geteilt: Mehrere Administratoren sehen dieselbe Ausgabe, neue Zuschauer erhalten die letzten 64 KB Verlauf. Die Host-Shell läuft als ',
        h('strong', null, session.user), ' (su -l); für Root-Rechte doas verwenden.'));
  },
};
