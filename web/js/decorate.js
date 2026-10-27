// Ergänzt Schaltflächen und Links automatisch um passende Symbole, damit jede Aktion überall gleich aussieht.
// Maßgeblich ist der Beschriftungstext; Elemente mit data-noicon oder schon vorhandenem Symbol bleiben unberührt.
import { icon, hasIcon } from './icons.js';

const RULES = [
  [/^(Starten|Von ISO starten|Fortsetzen|Dienst starten|Jetzt ausführen|Ausführen)$/, 'play'],
  [/^(Herunterfahren|Ausschalten)$/, 'power'],
  [/^(Neu starten|Neustart)$/, 'refresh'],
  [/^Pausieren$/, 'pause'],
  [/^(Hart beenden|Stoppen|Stopp)$/, 'stop'],
  [/^(Konsole|Host-Shell)$/, 'terminal'],
  [/^(Löschen|Entfernen|Widerrufen|.* entfernen|.* löschen)$/, 'trash'],
  [/^(Bearbeiten|Einstellungen|Als Text bearbeiten.*|.* bearbeiten)$/, 'pencil'],
  [/^(Speichern|Speichern und anwenden|Übernehmen|Setzen|Setzen und anwenden)$/, 'save'],
  [/^(Auf Probe anwenden|Anwenden|.* bestätigen|Bestätigen|Konfiguration bestätigen|Regeln bestätigen)$/, 'check-circle'],
  [/^(Vorschau|Details|Anzeigen|Ansehen)$/, 'eye'],
  [/^(Zurücksetzen|Zurücknehmen|Wiederherstellen|Revert)$/, 'undo'],
  [/^(Aktualisieren|Neu laden|Neu abgleichen|Neu einlesen)$/, 'refresh'],
  [/^Klonen$/, 'clone'],
  [/^Als Template$/, 'template'],
  [/^(Jetzt sichern|Sicherung starten|Sichern)/, 'backup'],
  [/^Snapshots?( \(\d+\))?$/, 'camera'],
  [/^(Snapshot anlegen)$/, 'camera'],
  [/^Netzwerk$/, 'ethernet'],
  [/^Firewall$/, 'firewall'],
  [/^Einhängen$/, 'mount'],
  [/^Aushängen$/, 'unmount'],
  [/^(Herunterladen|Download|Download starten|Exportieren)$/, 'download'],
  [/^(Hochladen|Importieren)$/, 'upload'],
  [/^(Öffnen|Host öffnen|Zur Übersicht|Zu den VMs)$/, 'external'],
  [/^(Schließen|Abbrechen|Verwerfen)$/, 'x'],
  [/^(Suchen|Suche)$/, 'search'],
  [/^(Testen|Testmail|Testmail senden|Verbindung testen|Test)$/, 'send'],
  [/^(Zusammenführen|Konsolidieren|Alles konsolidieren)$/, 'merge'],
  [/^(Verbinden|Fingerabdruck holen)$/, 'link'],
  [/^(Zurück|← .*)$/, 'arrow-up'],
  [/(hinzufügen|anlegen|Anlegen|erstellen)( …)?$/, 'plus'],
  [/^(Anmelden)$/, 'key'],
];

const SKIP = 'button.mini-btn, button.cta, button.secondary, button.glass-button, a.mini-btn, a.cta, a.secondary';

function decorate(el) {
  if (el.dataset.noicon !== undefined || el.querySelector(':scope > svg.icon') || el.classList.contains('tab')) return;
  const text = [...el.childNodes].filter(n => n.nodeType === 3).map(n => n.textContent).join('').trim();
  if (!text) return;
  for (const [re, name] of RULES) {
    if (re.test(text) && hasIcon(name)) {
      if (name === 'arrow-up' && text.startsWith('←')) { el.prepend(icon('chevron-right', { cls: 'flip' })); return; }
      el.prepend(icon(name));
      return;
    }
  }
}

export function startDecorating(root = document.getElementById('app')) {
  let queued = false;
  const run = () => { queued = false; root.querySelectorAll(SKIP).forEach(decorate); document.querySelectorAll('dialog').forEach(d => d.querySelectorAll(SKIP).forEach(decorate)); };
  const schedule = () => { if (!queued) { queued = true; requestAnimationFrame(run); } };
  new MutationObserver(schedule).observe(document.body, { childList: true, subtree: true });
  schedule();
}
