// Renders the main process's step snapshot as the board's cells: 1 steps, 2 admin account, 3 models folder,
// 4 a failed step, 5 the every-open start screen, 10 the first open after an update. Text goes in with textContent only.
const LOGO = '<svg aria-hidden="true" width="26" height="26" viewBox="0 0 24 24"><path d="M8 16C8 12.4 10.4 10.6 13.3 10.6S18.4 12.6 18.4 16V21" fill="none" stroke="currentColor" stroke-width="3.2" stroke-linecap="round"/><path d="M8 7V21" stroke="currentColor" stroke-width="3.2" stroke-linecap="round"/><path d="M5.3 1.6H10.7A.9.9 0 0 1 11.6 2.5V5L8 8.4L4.4 5V2.5A.9.9 0 0 1 5.3 1.6Z" fill="currentColor"/></svg>';
const TICK = '<svg aria-hidden="true" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3.2" stroke-linecap="round" stroke-linejoin="round"><path d="m5 12 5 5 9-10"/></svg>';
const CROSS = '<svg aria-hidden="true" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3.2" stroke-linecap="round"><path d="M6 6l12 12M18 6 6 18"/></svg>';

const root = document.getElementById('root');
let paneKey = null;
let typedEmail = ''; // kept across a re-ask

function el(tag, cls, text) {
  const e = document.createElement(tag);
  if (cls) e.className = cls;
  if (text !== undefined) e.textContent = text;
  return e;
}

function brand(title, sub, big) {
  const b = el('div', 'brand');
  const logo = el('span', big ? 'logo lg' : 'logo');
  logo.innerHTML = LOGO;
  const t = el('div');
  t.append(el('h2', 'd', title));
  if (sub) t.append(el('p', 'sub', sub));
  b.append(logo, t);
  return b;
}

function steps(snap, cls, numbered) {
  const ol = el('ol', `steps ${cls}`);
  snap.steps.forEach((s, i) => {
    const li = el('li', `st ${s.status}`);
    if (['run', 'ask', 'fail'].includes(s.status)) li.setAttribute('aria-current', 'step');
    const dot = el('span', 'dot');
    if (s.status === 'done') dot.innerHTML = TICK;
    else if (s.status === 'fail') dot.innerHTML = CROSS;
    else if (numbered) dot.textContent = i + 1;
    const sn = el('span', 'sn', s.title);
    if (s.status === 'run' && s.progress != null) {
      const bar = el('span', 'bar');
      const fill = el('span');
      fill.style.width = `${Math.round(s.progress * 100)}%`;
      bar.append(fill);
      sn.append(bar);
    }
    li.append(dot, sn, el('span', 'sd', s.detail || ''));
    ol.append(li);
  });
  return ol;
}

function button(cls, text, onClick) {
  const b = el('button', cls, text);
  b.addEventListener('click', onClick);
  return b;
}

function field(id, label, input) {
  const f = el('div', 'fld');
  const l = el('label', '', label);
  l.htmlFor = id;
  input.id = id;
  f.append(l, input);
  return f;
}

function input(type, cls = 'in') {
  const i = el('input', cls);
  i.type = type;
  return i;
}

function adminPane(ask) {
  const pane = el('form', 'pane');
  const prev = typedEmail;
  const email = input('email');
  email.value = prev;
  email.placeholder = 'you@example.com';
  email.autocomplete = 'username';
  const pass = input('password');
  pass.autocomplete = 'new-password';
  const pass2 = input('password');
  pass2.autocomplete = 'new-password';
  pane.append(
    el('h3', 'd', 'Admin account'),
    el('p', 'mut', 'The first account. It creates every other account in Admin.'),
    field('d-email', 'Email', email),
    field('d-pass', 'Password', pass),
    field('d-pass2', 'Repeat password', pass2),
    el('p', ask.data?.error ? 'sm bad' : 'mut sm', ask.data?.error || 'At least 12 characters. Kept only on this Mac.'),
  );
  const act = el('div', 'act');
  const go = el('button', 'pri', 'Continue');
  go.type = 'submit';
  act.append(go);
  pane.append(act);
  pane.addEventListener('submit', (e) => {
    e.preventDefault();
    typedEmail = email.value.trim();
    window.helm.answer('admin', { email: typedEmail, password: pass.value, repeat: pass2.value });
  });
  setTimeout(() => (prev ? pass : email).focus());
  return pane;
}

function modelsPane(ask) {
  const pane = el('div', 'pane');
  const dir = input('text', 'in mono');
  dir.readOnly = true;
  dir.value = ask.data?.dir || '~/helmstudio-models';
  const pick = el('div', 'pick');
  pick.append(
    dir,
    button('ghost', 'Choose…', async () => {
      const chosen = await window.helm.chooseFolder(dir.value);
      if (chosen) dir.value = chosen;
    }),
  );
  const f = el('div', 'fld');
  const l = el('label', '', 'Folder');
  l.htmlFor = 'd-dir';
  dir.id = 'd-dir';
  f.append(l, pick);
  const act = el('div', 'act');
  act.append(button('pri', 'Continue', () => window.helm.answer('models', dir.value)));
  pane.append(
    el('h3', 'd', 'Models folder'),
    el('p', 'mut', 'Where model weights live. Big: plan for 50 GB or more.'),
    f,
    el('p', 'note', 'Models are downloaded later, from Models in the studio. Nothing is downloaded now.'),
    el('p', 'mut sm', 'Then the runners are set up: about 2 GB, a few minutes.'),
    act,
  );
  return pane;
}

function failPane(snap) {
  const pane = el('div', 'pane');
  const i = snap.steps.findIndex((s) => s.status === 'fail');
  const step = snap.steps[i];
  pane.append(el('h3', 'd err', snap.error.title), el('p', 'big', snap.error.message));
  if (step?.detail) pane.append(el('p', 'mut sm', `Stopped at ${step.detail}.`));
  const log = el('pre', 'log', snap.tail.slice(-4).join('\n'));
  log.setAttribute('aria-label', 'Last log lines');
  const act = el('div', 'act');
  act.append(button('ghost', 'Show log', () => window.helm.showLog()), button('pri', 'Try again', () => window.helm.retry()));
  const again = el('p', 'mut sm', `Try again resumes here, at step ${i + 1}.`);
  again.style.textAlign = 'right';
  pane.append(log, act, again);
  requestAnimationFrame(() => (log.scrollTop = log.scrollHeight)); // the last lines
  return pane;
}

function render(snap) {
  if (!snap) return;
  const first = snap.mode === 'first';
  const cur = snap.steps.findIndex((s) => s.status !== 'done' && s.status !== 'todo');

  if (snap.error || snap.ask) {
    const key = snap.error ? `fail ${snap.error.step} ${snap.tail.join()}` : `ask ${snap.ask.kind} ${snap.ask.data?.error}`;
    if (paneKey === key) {
      root.querySelector('.steps')?.replaceWith(steps(snap, 'compact', true));
      return; // keep what is typed
    }
    paneKey = key;
    const split = el('div', 'split');
    const side = el('div', 'side');
    side.append(
      brand(first ? 'Setting up helmstudio' : snap.mode === 'update' ? 'Updating helmstudio' : 'Starting helmstudio', `Step ${cur + 1} of ${snap.steps.length}`),
      steps(snap, 'compact', true),
    );
    const pane = snap.error ? failPane(snap) : snap.ask.kind === 'admin' ? adminPane(snap.ask) : modelsPane(snap.ask);
    split.append(side, pane);
    root.replaceChildren(split);
    return;
  }
  paneKey = null;

  if (first) {
    const one = el('div', 'one');
    const foot = el('div', 'foot');
    foot.append(el('span', 'mut', 'You can leave this window; it keeps going.'), button('ghost', 'Show log', () => window.helm.showLog()));
    one.append(brand('Setting up helmstudio', 'This takes about 10 minutes the first time.'), steps(snap, '', true), foot);
    root.replaceChildren(one);
  } else {
    const start = el('div', 'start');
    const logo = el('span', 'logo lg');
    logo.innerHTML = LOGO.replace(/width="26" height="26"/, 'width="36" height="36"');
    const upd = snap.mode === 'update';
    start.append(
      logo,
      el('h2', 'd', upd ? 'Updating…' : 'Starting…'),
      steps(snap, 'mini', false),
      el('span', 'mut sm', upd ? `First open of ${snap.version}. Only this once.` : 'Usually under a minute.'),
    );
    root.replaceChildren(start);
  }
}

window.helm.onUpdate(render);
window.helm.hello().then(render);
