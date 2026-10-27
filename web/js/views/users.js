import { h, clear, pageHead, panel, table, pill, loading, toast, toastError, confirm, formDialog, field, busy } from '../ui.js';
import { get, post, put, del, session } from '../api.js';

const SHELLS = ['/bin/ksh', '/bin/sh', '/bin/csh', '/usr/local/bin/bash', '/usr/local/bin/zsh', '/usr/local/bin/fish', '/sbin/nologin'];

export default {
  title: 'Benutzer & Gruppen',
  async render(main) {
    const body = h('div', { class: 'content' }, loading());
    const newBtn = h('button', { class: 'cta', type: 'button' }, 'Benutzer anlegen');
    main.append(pageHead('HOST', 'Benutzer & Gruppen', 'Konten mit useradd(8) und usermod(8) verwalten. Mitglieder der Gruppe wheel dürfen sich an dieser Oberfläche anmelden und doas nutzen.', newBtn), body);

    let data = null, showSystem = false;
    const userBox = h('div', { class: 'panel-body flush' });
    const groupBox = h('div', { class: 'panel-body flush' });
    const cronBox = h('div', { class: 'panel-body' });

    async function load() {
      data = await get('/api/users');
      draw();
    }

    function groupPicker(selected) {
      const boxes = data.groups.filter(g => g.gid < 32766).map(g => ({ g, cb: h('input', { type: 'checkbox', checked: selected.includes(g.name) }) }));
      const important = ['wheel', 'operator', 'staff', '_vmd'];
      boxes.sort((a, b) => (important.includes(b.g.name) - important.includes(a.g.name)) || a.g.name.localeCompare(b.g.name));
      return {
        el: h('fieldset', { class: 'wide field' }, h('legend', null, 'Gruppen'),
          h('div', { class: 'toolbar' }, boxes.map(({ g, cb }) => h('label', { class: 'check' }, cb, g.name)))),
        value: () => boxes.filter(x => x.cb.checked).map(x => x.g.name),
      };
    }

    function userForm(u) {
      const name = h('input', { required: true, pattern: '[a-z_][a-z0-9_\\-]{0,30}', value: u?.name || '', disabled: !!u, autocomplete: 'off' });
      const comment = h('input', { value: u?.comment || '', autocomplete: 'off' });
      const shell = h('select', null, SHELLS.map(s => h('option', { value: s, selected: (u?.shell || '/bin/ksh').replace(/-$/, '') === s }, s)));
      const pw = h('input', { type: 'password', minlength: '8', autocomplete: 'new-password' });
      const groups = groupPicker(u ? u.groups.slice(1) : []);
      formDialog({
        title: u ? `${u.name} bearbeiten` : 'Neuer Benutzer', meta: u ? 'USERMOD' : 'USERADD -M', wide: true, submit: u ? 'Speichern' : 'Anlegen',
        fields: [field('Benutzername', name), field('Vollständiger Name', comment), field('Shell', shell),
          u ? null : field('Passwort', pw, 'mindestens 8 Zeichen; leer = Konto ohne Passwort (nur SSH-Schlüssel)'), groups.el],
        onSubmit: async () => {
          const spec = { name: name.value.trim(), comment: comment.value.trim(), shell: shell.value, groups: groups.value(), password: pw.value };
          const r = u ? await put(`/api/users/${u.name}`, spec) : await post('/api/users', spec);
          toast(r.message);
          await load();
        },
      });
    }

    function setPassword(u) {
      const pw = h('input', { type: 'password', minlength: '8', required: true, autocomplete: 'new-password' });
      const pw2 = h('input', { type: 'password', minlength: '8', required: true, autocomplete: 'new-password' });
      formDialog({
        title: `Passwort für ${u.name}`, meta: 'ENCRYPT · USERMOD -P', submit: 'Passwort setzen',
        fields: [field('Neues Passwort', pw), field('Wiederholen', pw2)],
        onSubmit: async () => {
          if (pw.value !== pw2.value) throw new Error('Die Passwörter stimmen nicht überein.');
          const r = await post(`/api/users/${u.name}/password`, { password: pw.value });
          toast(r.message);
        },
      });
    }

    async function userAction(u, a, btn) {
      if (a === 'delete' && !await confirm({ title: `${u.name} löschen?`, text: `userdel -r entfernt das Konto und das Heimatverzeichnis ${u.home}.`, ok: 'Löschen', danger: true, typeToConfirm: u.name })) return;
      if (a === 'lock' && !await confirm({ title: `${u.name} sperren?`, text: 'Das Konto kann sich danach nicht mehr anmelden (auch nicht per SSH-Schlüssel).', ok: 'Sperren' })) return;
      await busy(btn, async () => { const r = await post(`/api/users/${u.name}/${a}`); toast(r.message); await load(); });
    }

    async function loadCron(user) {
      const d = await get(`/api/cron/${user}`);
      const ta = h('textarea', { class: 'code', spellcheck: 'false', 'aria-label': `crontab von ${user}`, value: d.content });
      ta.style.minHeight = '260px';
      const save = h('button', { class: 'cta', type: 'button' }, 'Speichern');
      save.addEventListener('click', () => busy(save, async () => { const r = await put(`/api/cron/${user}`, { content: ta.value }); toast(r.message); }).catch(toastError));
      const sel = h('select', { class: 'input', 'aria-label': 'Benutzer' }, data.users.filter(u => !u.system).map(u => h('option', { value: u.name, selected: u.name === user }, u.name)));
      sel.style.width = 'auto';
      sel.addEventListener('change', () => loadCron(sel.value).catch(toastError));
      clear(cronBox, h('div', { class: 'toolbar' }, sel, h('span', { class: 'grow' }), save), h('div', { style: { marginTop: '14px', border: '1px solid var(--line-soft)' } }, ta));
    }

    function draw() {
      const users = data.users.filter(u => showSystem || !u.system);
      clear(userBox, table(['Benutzer', 'UID', 'Gruppen', 'Shell', 'Status', ''], users.map(u => {
        const btn = (label, fn, danger) => { const b = h('button', { class: 'mini-btn' + (danger ? ' danger' : ''), type: 'button' }, label); b.addEventListener('click', () => fn(b).catch(toastError)); return b; };
        const self = u.name === session.user;
        return h('tr', null,
          h('td', null, h('span', { class: 'name' }, u.name), h('span', { class: 'sub' }, u.comment || u.home)),
          h('td', { class: 'num mono' }, u.uid),
          h('td', null, u.groups.map(g => h('span', { class: 'tag' }, g))),
          h('td', { class: 'mono' }, u.shell),
          h('td', null, u.locked ? pill('off', u.shell.endsWith('nologin') ? 'kein Login' : 'gesperrt') : pill('ok', 'aktiv'), u.groups.includes('wheel') ? [' ', pill('info', 'admin')] : null),
          h('td', { class: 'actions' }, u.system ? null : [
            btn('Bearbeiten', async () => userForm(u)),
            btn('Passwort', async () => setPassword(u)),
            u.name === 'root' || self ? null : (u.locked && u.shell.endsWith('-') ? btn('Entsperren', b => userAction(u, 'unlock', b)) : btn('Sperren', b => userAction(u, 'lock', b))),
            u.name === 'root' || self ? null : btn('Löschen', b => userAction(u, 'delete', b), true)]));
      })));
      clear(groupBox, table(['Gruppe', 'GID', 'Mitglieder', ''], data.groups.filter(g => showSystem || g.gid >= 1000 || ['wheel', 'operator', 'staff', '_vmd'].includes(g.name)).map(g => {
        const rm = g.gid >= 1000 && g.gid < 32766 && !data.users.some(u => u.gid === g.gid)
          ? h('button', { class: 'mini-btn danger', type: 'button', onclick: async () => {
            if (!await confirm({ title: `Gruppe ${g.name} löschen?`, ok: 'Löschen', danger: true })) return;
            del(`/api/groups/${g.name}`).then(r => { toast(r.message); return load(); }).catch(toastError);
          } }, 'Löschen') : null;
        return h('tr', null, h('td', { class: 'name mono' }, g.name), h('td', { class: 'num mono' }, g.gid), h('td', null, g.members.map(m => h('span', { class: 'tag' }, m))), h('td', { class: 'actions' }, rm));
      })));
    }

    const sysToggle = h('input', { type: 'checkbox' });
    sysToggle.addEventListener('change', () => { showSystem = sysToggle.checked; draw(); });
    const newGroup = h('button', { class: 'mini-btn', type: 'button' }, 'Gruppe anlegen');
    newGroup.addEventListener('click', () => {
      const name = h('input', { required: true, pattern: '[a-z_][a-z0-9_\\-]{0,30}' });
      formDialog({ title: 'Neue Gruppe', meta: 'GROUPADD', submit: 'Anlegen', fields: [field('Name', name)],
        onSubmit: async () => { const r = await post('/api/groups', { name: name.value.trim() }); toast(r.message); await load(); } });
    });
    newBtn.addEventListener('click', () => data && userForm(null));

    clear(body,
      panel('Konten', '/ETC/PASSWD', userBox, h('label', { class: 'check' }, sysToggle, 'Systemkonten zeigen')),
      h('div', { class: 'grid-2' },
        panel('Gruppen', '/ETC/GROUP', groupBox, newGroup),
        panel('Geplante Aufgaben', 'CRONTAB(5)', cronBox)));
    await load();
    loadCron('root').catch(e => clear(cronBox, h('div', { class: 'note warn' }, e.message)));
  },
};
