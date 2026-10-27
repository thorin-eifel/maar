// „Über maar“: erst die Software, darunter der Anbieter (Angaben aus dem Impressum von ctw-computer.de).
import { h, dialog } from './ui.js';
import { session } from './api.js';
import { icon } from './icons.js';

const link = (href, text) => h('a', { href, target: '_blank', rel: 'noopener noreferrer' }, text, icon('external', { cls: 'ext-icon' }));
const kv = rows => h('dl', { class: 'kv about-kv' }, rows.filter(Boolean).flatMap(([k, v]) => [h('dt', null, k), h('dd', null, v)]));

export function aboutDialog() {
  const software = h('section', { class: 'about-sec' },
    h('span', { class: 'meta' }, 'Software'),
    h('h4', null, `maar ${session.version}`),
    h('p', null, 'Webbasierte Verwaltung für OpenBSD: virtuelle Maschinen (vmd), Speicher, Netzwerk, Dienste und Host in einer Oberfläche, aufgebaut nach dem Vorbild des vSphere Clients. Ein einzelnes statisches Go-Binary, das Konfigurationsdateien aus Formularen schreibt, prüft und sichert.'),
    kv([
      ['Version', h('code', null, session.version)],
      ['Dieser Host', session.hostname + (session.demo ? ' (Demo)' : '')],
      ['Lizenz', link('https://github.com/thorin-eifel/maar/blob/main/LICENSE', 'ISC · © 2026 IT-Haus CTW-Computer Irrel GmbH')],
      ['Quellcode', link('https://github.com/thorin-eifel/maar', 'github.com/thorin-eifel/maar')],
      ['Releases', link('https://github.com/thorin-eifel/maar/releases', 'Versionen und Änderungen')],
    ]));

  const vendor = h('section', { class: 'about-sec' },
    h('span', { class: 'meta' }, 'Anbieter'),
    h('h4', null, 'IT-Haus CTW-Computer Irrel GmbH'),
    kv([
      ['Anschrift', 'Niederweiser Str. 31, 54666 Irrel'],
      ['Telefon', '+49 (0) 6525 933 318 0'],
      ['Telefax', '+49 (0) 6525 933 318 2'],
      ['E-Mail', link('mailto:info@ctw-computer.de', 'info@ctw-computer.de')],
      ['Web', link('https://www.ctw-computer.de', 'www.ctw-computer.de')],
      ['Geschäftsführung', 'Karl Heinz Hartmann'],
      ['Register', 'Amtsgericht Wittlich, HRB 42276'],
      ['USt-IdNr.', 'DE286158701'],
      ['Kammer', 'Industrie- und Handelskammer Trier'],
    ]),
    h('p', { class: 'hint' }, 'Vollständige Angaben, Haftung und Urheberrecht: ', link('https://ctw-computer.de/impressum/', 'Impressum')));

  const close = h('button', { class: 'secondary', type: 'button' }, 'Schließen');
  const d = dialog({ title: 'Über maar', body: h('div', { class: 'about' }, software, vendor), actions: [close], wide: false });
  close.addEventListener('click', () => d.close());
  return d;
}
