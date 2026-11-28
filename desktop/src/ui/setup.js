// Renders the main process's step snapshot as the board's cells: 1 steps, 2 admin account, 3 models folder,
// 4 a failed step, 5 the every-open start screen, 10 the first open after an update; and the error states (2a the
// studio stopped answering, 2b a runner, 2c a port, 2d the models drive, 2e low disk, 2g the page didn't load).
// Text goes in with textContent only.
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

// ---------- the error states (board Desktop.dc.html "Error states")

const bigLogo = () => {
  const logo = el('span', 'logo lg');
  logo.innerHTML = LOGO.replace(/width="26" height="26"/, 'width="36" height="36"');
  return logo;
};

function actions(...buttons) {
  const act = el('div', 'act');
  act.append(...buttons);
  return act;
}

// 2a: what is down, one row per part; Restart, Show log (the folder at app.log), Quit
function downPage(i) {
  const d = el('div', 'down');
  const rows = el('ol', 'steps mini');
  for (const r of i.rows) {
    const li = el('li', `st ${r.ok ? 'done' : 'fail'}`);
    const dot = el('span', 'dot');
    dot.innerHTML = r.ok ? TICK : CROSS;
    li.append(dot, el('span', 'sn', r.title), el('span', 'sd', r.state));
    rows.append(li);
  }
  d.append(
    bigLogo(),
    el('h2', 'd', 'helmstudio stopped answering'),
    el('p', 'big', `The studio stopped answering at ${i.at}. What you made is saved; runs that were going show as failed.`),
    rows,
    actions(
      button('ghost', 'Quit', () => window.helm.act('quit')),
      button('ghost', 'Show log', () => window.helm.act('log-folder')),
      button('pri', 'Restart', () => window.helm.act('restart')),
    ),
    el('p', 'mut sm', 'Checking again every 5 s. The studio comes back here by itself when it answers.'),
  );
  return d;
}

// 2g: the browser's error code and the address; Reload
function loadPage(i) {
  const d = el('div', 'down lo');
  d.append(
    el('h2', 'd', i.crashed ? 'The studio page stopped.' : 'The studio page didn’t load.'),
    el('p', 'mono sm mut', `${i.code} · ${i.url}`),
    actions(button('ghost', 'Show log', () => window.helm.showLog()), button('pri', 'Reload', () => window.helm.act('reload'))),
    el('p', 'mut sm', 'It reloads by itself when the studio answers.'),
  );
  return d;
}

function lastLines(lines) {
  const log = el('pre', 'log', lines.join('\n'));
  log.setAttribute('aria-label', 'Last log lines');
  requestAnimationFrame(() => (log.scrollTop = log.scrollHeight));
  return log;
}

// 2b: keeps stopping (5 in 10 minutes) or didn't start (the cause in one sentence); Restart runner, Show log
function runnerPane(i) {
  const u = el('div', 'upd');
  const keeps = i.what === 'keeps';
  const again = button('pri', i.busy ? 'Setting up…' : 'Restart runner', () => window.helm.act('restart-runner'));
  again.disabled = !!i.busy;
  u.append(
    el('h3', 'd err', keeps ? `The ${i.env} runner keeps stopping` : `The ${i.env} runner didn’t start`),
    el('p', 'big', keeps ? 'It stopped 5 times in 10 minutes, so it is not restarted again until you say so.' : `${i.cause} Restart runner sets it up again (about 2 GB, needs the internet).`),
    lastLines(i.lines || []),
    actions(button('ghost', 'Show log', () => window.helm.act('runner-log')), again),
  );
  return u;
}

// 2c: the app holding the port, the next free one; Try again starts with the port in the field
function portPane(d, cls) {
  const u = el('div', cls);
  const port = input('text', 'in mono');
  port.inputMode = 'numeric';
  port.value = d.port;
  const pick = el('div', 'pick');
  pick.append(port);
  if (d.suggest) pick.append(button('ghost', `Use ${d.suggest}`, () => (port.value = d.suggest)));
  const f = el('div', 'fld');
  const l = el('label', '', `Port for ${d.label}`);
  l.htmlFor = 'd-port';
  port.id = 'd-port';
  f.append(l, pick);
  u.append(
    el('h3', 'd err', `Port ${d.port} is in use by ${d.holder}`),
    el('p', 'big', 'Quit it, or pick another port.'),
    f,
    actions(button('ghost', 'Show log', () => window.helm.showLog()), button('pri', 'Try again', () => window.helm.answer('port', port.value.trim()))),
  );
  return u;
}

// 2d: Continue with what is there, or Choose folder…; it goes on by itself when the drive is back
function drivePane(d, cls) {
  const u = el('div', cls);
  u.append(
    el('h3', 'd', `Your models folder on ${d.volume} isn’t connected`),
    el('p', 'big', 'Plug it in, or keep going with the models on this Mac.'),
    el('p', 'mono sm mut', d.dir),
    el('p', 'mut sm', `It starts by itself when ${d.volume} is plugged in.`),
    actions(
      button('ghost', 'Choose folder…', async () => {
        const dir = await window.helm.chooseFolder(d.dir);
        if (dir) window.helm.answer('drive', { dir });
      }),
      button('pri', 'Continue', () => window.helm.answer('drive', 'continue')),
    ),
  );
  return u;
}

// 2e: under 10 GB, at most once a day
function diskPane(d, cls) {
  const u = el('div', cls);
  u.append(
    el('h3', 'd err', `Only ${d.free} GB free`),
    el('p', 'big', 'Runs may fail; free some space.'),
    el('p', 'mut sm', `On ${d.disk}. helmstudio needs about 10 GB free to make takes and load models.`),
    actions(button('ghost', 'Open storage', () => window.helm.act('open-storage')), button('pri', 'Continue anyway', () => window.helm.answer('disk', 'continue'))),
  );
  return u;
}

const CHECKS = { port: portPane, drive: drivePane, disk: diskPane };

function render(snap) {
  if (!snap) return;
  if (snap.issue) {
    const key = `issue ${JSON.stringify(snap.issue)}`;
    if (paneKey === key) return;
    paneKey = key;
    const page = { down: downPage, load: loadPage, runner: runnerPane }[snap.issue.kind](snap.issue);
    root.replaceChildren(page);
    return;
  }
  const first = snap.mode === 'first';
  const cur = snap.steps.findIndex((s) => s.status !== 'done' && s.status !== 'todo');

  // a start's checks (2c, 2d, 2e) fill the window; at the first open they are the pane beside the steps
  if (snap.ask && CHECKS[snap.ask.kind] && !first) {
    const key = `check ${snap.ask.kind} ${JSON.stringify(snap.ask.data)}`;
    if (paneKey === key) return; // keep what is typed
    paneKey = key;
    root.replaceChildren(CHECKS[snap.ask.kind](snap.ask.data, 'upd'));
    return;
  }

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
    const pane = snap.error
      ? failPane(snap)
      : CHECKS[snap.ask.kind]
        ? CHECKS[snap.ask.kind](snap.ask.data, 'pane')
        : snap.ask.kind === 'admin'
          ? adminPane(snap.ask)
          : modelsPane(snap.ask);
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
    const logo = bigLogo();
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
