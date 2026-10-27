// Ereignisse: Anmeldungen, Aktionen, Aufträge und Ablehnungen aller Konten.
import { h, clear, pageHead } from '../ui.js';
import { eventsPanel } from './access.js';

export default {
  title: 'Ereignisse',
  async render(main) {
    const body = h('div', { class: 'content' });
    main.append(pageHead('13 / ÜBERWACHUNG', 'Ereignisse', 'Wer hat wann was getan: Anmeldungen, verändernde Aktionen, Aufträge und verweigerte Zugriffe. Die letzten 2000 Einträge bleiben erhalten.'), body);
    const ev = eventsPanel();
    clear(body, ev.el);
    const t = setInterval(() => ev.reload().catch(() => {}), 20000);
    return () => clearInterval(t);
  },
};
