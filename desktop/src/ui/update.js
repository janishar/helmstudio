// The update window, board frame 9 (a checking, b up to date, c available, d downloading, e verifying, f ready,
// g download failed, h check failed); the main process owns the state. Text goes in with textContent only.
const LOGO = (s) => `<svg aria-hidden="true" width="${s}" height="${s}" viewBox="0 0 24 24"><path d="M8 16C8 12.4 10.4 10.6 13.3 10.6S18.4 12.6 18.4 16V21" fill="none" stroke="currentColor" stroke-width="3.2" stroke-linecap="round"/><path d="M8 7V21" stroke="currentColor" stroke-width="3.2" stroke-linecap="round"/><path d="M5.3 1.6H10.7A.9.9 0 0 1 11.6 2.5V5L8 8.4L4.4 5V2.5A.9.9 0 0 1 5.3 1.6Z" fill="currentColor"/></svg>`;
const TICK = '<svg aria-hidden="true" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3.2" stroke-linecap="round" stroke-linejoin="round"><path d="m5 12 5 5 9-10"/></svg>';

const root = document.getElementById('root');
const act = (a) => () => window.helm.upd(a);
const mb = (n) => Math.round(n / 1e6);

function el(tag, cls, text) {
  const e = document.createElement(tag);
  if (cls) e.className = cls;
  if (text !== undefined) e.textContent = text;
  return e;
}

function button(cls, text, onClick) {
  const b = el('button', cls, text);
  b.addEventListener('click', onClick);
  return b;
}

function logo(big) {
  const l = el('span', big ? 'logo lg' : 'logo');
  l.innerHTML = LOGO(big ? 36 : 26);
  return l;
}

// rows: [title, status (done | run | ask | todo), detail, progress?]
function steps(rows) {
  const ol = el('ol', 'steps mini');
  rows.forEach(([title, status, detail, progress], i) => {
    const li = el('li', `st ${status}`);
    const dot = el('span', 'dot');
    if (status === 'done') dot.innerHTML = TICK;
    else if (status !== 'run') dot.textContent = i + 1;
    const sn = el('span', 'sn', title);
    if (progress != null) {
      const bar = el('span', 'bar');
      const fill = el('span');
      fill.style.width = `${Math.round(progress * 100)}%`;
      bar.append(fill);
      sn.append(bar);
    }
    li.append(dot, sn, el('span', 'sd', detail || ''));
    ol.append(li);
  });
  return ol;
}

function actions(...buttons) {
  const a = el('div', 'act');
  a.append(...buttons);
  return a;
}

const later = () => button('ghost', 'Later', act('close'));

function failed(u, title, text, retry = 'install') {
  const box = el('div', 'upd');
  box.append(el('h3', 'd err', title), el('p', 'big', text), el('p', 'mut sm', `Nothing changed. You still have ${u.current}.`));
  box.append(retry ? actions(later(), button('pri', 'Try again', act(retry))) : actions(later()));
  return box;
}

function render(u) {
  if (!u) return;
  const r = u.release;
  const size = r && `${mb(r.dmg.size)} MB`;
  let view;
  if (u.state === 'checking' || u.state === 'none') {
    view = el('div', 'start');
    if (u.state === 'checking') view.append(el('span', 'spin'), el('h2', 'd upd-t', 'Checking for updates…'));
    else view.append(logo(true), el('h2', 'd upd-t', 'You’re up to date'), el('p', 'mut', `You have the latest: ${u.current}`), button('ghost', 'OK', act('close')));
  } else if (u.state === 'checkfail') {
    view = failed(u, 'Could not check for updates', 'Could not reach GitHub. Check the internet connection.', 'recheck');
  } else if (u.state === 'available') {
    view = el('div', 'upd');
    const brand = el('div', 'brand');
    const t = el('div');
    t.append(el('h2', 'd', `${r.version} is out`), el('p', 'sub', `You have ${u.current} · ${size}`));
    brand.append(logo(false), t);
    const notes = el('ul', 'rn');
    for (const n of r.notes) notes.append(el('li', '', n));
    const link = el('p', 'sm');
    const a = el('a', 'lnk', 'Release notes');
    a.href = '#';
    a.addEventListener('click', (e) => (e.preventDefault(), window.helm.upd('notes')));
    link.append(a);
    view.append(brand, ...(r.notes.length ? [notes] : []), link, actions(later(), button('pri', 'Install', act('install'))));
  } else if (['download', 'verify', 'ready', 'installing'].includes(u.state)) {
    view = el('div', 'upd');
    const dl = u.state === 'download';
    const ready = u.state === 'ready' || u.state === 'installing';
    view.append(
      el('h3', 'd', ready ? 'Ready to install' : `Updating to ${r.version}`),
      steps([
        ['Download', dl ? 'run' : 'done', dl ? `${mb(u.got)} of ${size}` : size, dl ? Math.min(u.got / (r.dmg.size || 1), 1) : null],
        ['Check', dl ? 'todo' : u.state === 'verify' ? 'run' : 'done', dl ? '' : u.state === 'verify' ? 'SHA256' : 'SHA256 ok'],
        ['Install', u.state === 'installing' ? 'run' : ready ? 'ask' : 'todo', ''],
      ]),
    );
    if (ready) {
      view.append(el('p', 'mut', 'The app quits, swaps itself and opens again. The studio is offline for about a minute.'));
      if (u.state === 'ready') view.append(actions(later(), button('pri', 'Restart', act('restart'))));
    } else view.append(actions(button('ghost', 'Cancel', act('cancel'))));
  } else if (u.state === 'dlfail') {
    view = failed(u, 'Download failed', 'Could not reach GitHub. Check the internet connection.');
  } else if (u.state === 'sumfail') {
    view = failed(u, 'Check failed', 'The download does not match its SHA256. It was deleted.');
  } else if (u.state === 'installfail') {
    view = failed(u, 'Cannot install here', u.error, null);
  } else return;
  root.replaceChildren(view);
}

window.helm.onUpd(render);
window.helm.updHello().then(render);
