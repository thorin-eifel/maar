// Erzeugt die Screenshots der Dokumentation aus der Demo-Instanz (make demo).
// Aufruf:  node docs/tools/screenshots.mjs [http://127.0.0.1:9443]
// Braucht Google Chrome und Node ≥ 22 (eingebauter WebSocket-Client). Ausgabe: docs/img/*.jpg (2000 px breit).
import { spawn } from 'node:child_process';
import { mkdtempSync, mkdirSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';

const BASE = process.argv[2] || 'http://127.0.0.1:9443';
const OUT = join(dirname(fileURLToPath(import.meta.url)), '..', 'img');
const CHROME = process.env.CHROME || '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome';
const W = 1600, H = 1000, DPR = 1.25;           // 1600 × 1000 CSS-Pixel → 2000 × 1250 Bildpunkte
const PORT = 9333 + Math.floor(Math.random() * 500);
const sleep = ms => new Promise(r => setTimeout(r, ms));

// Jede Aufnahme: Name, Adresse (Hash), optional Vorbereitung (JS im Seitenkontext), Wartezeit, Farbschema.
const click = txt => `[...document.querySelectorAll('main button, main a, main .tab')].find(e => e.textContent.trim() === ${JSON.stringify(txt)})?.click()`;
const SHOTS = [
  ['uebersicht', '#/'],
  ['hosts', '#/hosts'],
  ['host-detail', '#/hosts/kuestenwache'],
  ['vms', '#/vms'],
  ['vm-zusammenfassung', '#/vm/db01', 1800],
  ['vm-ueberwachen', '#/vm/db01/monitor', 1800],
  ['vm-konfigurieren', '#/vm/db01/config'],
  ['vm-snapshots', '#/vm/db01/snapshots'],
  ['vm-sicherung', '#/vm/db01/backup'],
  ['vm-einstellungen', '#/vm/db01/config', 800, click('Bearbeiten')],
  ['vm-anlegen', '#/vms', 800, click('VM anlegen')],
  ['konsole', '#/console/vm/db01', 2500],
  ['templates', '#/templates'],
  ['speicher-san', '#/san'],
  ['datastore', '#/datastore/vm-pool'],
  ['lokale-datentraeger', '#/storage'],
  ['backup', '#/backup'],
  ['vm-netze', '#/vnet'],
  ['portgruppe', '#/portgroup/intern'],
  ['netzwerk', '#/network'],
  ['interface', '#/iface/em0'],
  ['paketfilter', '#/firewall'],
  ['paketfilter-regeln', '#/firewall', 800, click('Regeln bearbeiten')],
  ['leistung', '#/performance', 2200],
  ['ereignisse', '#/events'],
  ['prozesse', '#/processes'],
  ['protokolle', '#/logs'],
  ['host-betrieb', '#/system'],
  ['system-ntp', '#/hostconf/ntp'],
  ['system-ssh', '#/hostconf/ssh'],
  ['system-doas', '#/hostconf/doas'],
  ['system-sysctl', '#/hostconf/sysctl'],
  ['pakete', '#/packages'],
  ['benutzer', '#/users'],
  ['dienste', '#/services'],
  ['web-httpd', '#/webmail/httpd'],
  ['web-smtpd', '#/webmail/smtpd'],
  ['web-relayd', '#/webmail/relayd'],
  ['zugriff', '#/access'],
  ['konfigurationsdateien', '#/files'],
  ['ueber', '#/', 800, `document.querySelector('.version-link').click()`],
  ['uebersicht-dunkel', '#/', 1200, null, 'dark'],
];

mkdirSync(OUT, { recursive: true });
const profile = mkdtempSync(join(tmpdir(), 'maar-shots-'));
const chrome = spawn(CHROME, ['--headless=new', `--remote-debugging-port=${PORT}`, `--user-data-dir=${profile}`, '--no-first-run',
  '--hide-scrollbars', '--force-color-profile=srgb', `--window-size=${W},${H}`, 'about:blank'], { stdio: 'ignore' });

async function connect() {
  for (let i = 0; i < 50; i++) {
    try {
      const list = await (await fetch(`http://127.0.0.1:${PORT}/json`)).json();
      const page = list.find(t => t.type === 'page');
      if (page) return new WebSocket(page.webSocketDebuggerUrl);
    } catch { /* Chrome startet noch */ }
    await sleep(200);
  }
  throw new Error('Chrome nicht erreichbar');
}

const ws = await connect();
await new Promise(r => ws.addEventListener('open', r, { once: true }));
let id = 0; const waiting = new Map();
ws.addEventListener('message', e => { const m = JSON.parse(e.data); if (m.id && waiting.has(m.id)) { waiting.get(m.id)(m); waiting.delete(m.id); } });
const send = (method, params = {}) => new Promise(res => { const i = ++id; waiting.set(i, res); ws.send(JSON.stringify({ id: i, method, params })); });
const evalJS = async expr => (await send('Runtime.evaluate', { expression: expr, awaitPromise: true, returnByValue: true })).result?.result?.value;

try {
  await send('Page.enable');
  await send('Emulation.setDeviceMetricsOverride', { width: W, height: H, deviceScaleFactor: DPR, mobile: false });
  await send('Page.navigate', { url: BASE + '/' });
  await sleep(1500);
  // Anmeldung (Demo: demo / demo)
  await evalJS(`(async () => { const u = document.querySelector('input[name=user]'); if (!u) return;
    u.value = 'demo'; document.querySelector('input[name=password]').value = 'demo'; u.form.requestSubmit(); })()`);
  await sleep(2000);
  await evalJS(`localStorage.setItem('maar.theme', 'light'); localStorage.removeItem('maar.tree.hidden')`);

  for (const [name, hash, wait = 1200, prep = null, theme = 'light'] of SHOTS) {
    await evalJS(`document.documentElement.dataset.theme = ${JSON.stringify(theme)}; location.hash = ${JSON.stringify(hash)}; 1`);
    await evalJS(`new Promise(r => setTimeout(r, 400))`);
    await evalJS(`location.reload(); 1`);
    await sleep(1500);
    await evalJS(`document.documentElement.dataset.theme = ${JSON.stringify(theme)}; 1`);
    await sleep(wait);
    if (prep) { await evalJS(prep); await sleep(1000); }
    const shot = await send('Page.captureScreenshot', { format: 'jpeg', quality: 86 });
    writeFileSync(join(OUT, name + '.jpg'), Buffer.from(shot.result.data, 'base64'));
    console.log('✓', name);
  }
} finally {
  ws.close();
  chrome.kill();
}
