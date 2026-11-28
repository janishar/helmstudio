// helmstudio for Mac (docs/plan/31-mac-app.md): first open sets everything up, every open starts it, then one window
// on the app's Kong. Dock icon plus a menu-bar item; quitting keeps the background running (decision 2), Quit
// stops the runners, the stack and the VM.
// HELMSTUDIO_HOME moves the app's folder (a scratch run); HELMSTUDIO_FAKE=1 fakes every command (no VM, no Docker),
// HELMSTUDIO_FAKE_FAIL=<word> then fails one of them (the failed-step screen); HELMSTUDIO_UPDATE_URL (tests) points
// the update check at a fake releases list; HELMSTUDIO_HIDE=1 (tests) never shows a window.
const path = require('node:path');
const fs = require('node:fs');
const { app, BrowserWindow, Tray, Menu, Notification, nativeImage, shell, dialog, ipcMain, powerMonitor, session } = require('electron');
const { StepRunner, exec, pipe } = require('./steps');
const { layout, loadSettings, stackSteps, Runners, openMode, tailOf } = require('./stack');
const updates = require('./update');
const { spellingOf, isWord } = require('./spelling');
const { localPath, tilde } = require('./folders');
const rules = require('./rules');
const cfg = require('../stack.json');

const home = process.env.HELMSTUDIO_HOME || path.join(app.getPath('appData'), 'helmstudio');
if (process.env.HELMSTUDIO_HOME) app.setPath('userData', path.join(home, 'electron'));
// the bundle's Resources when packaged (checked by path: `make run` is Electron from node_modules), else
// build/Resources (make resources) for `make run`
const res = __dirname.startsWith(process.resourcesPath) ? process.resourcesPath : path.join(__dirname, '..', 'build', 'Resources');
const fake = process.env.HELMSTUDIO_FAKE === '1';
let fakeFail = process.env.HELMSTUDIO_FAKE_FAIL; // fake mode: the first command line holding this word fails once
const p = layout(home, res);
const settings = loadSettings(p);
rules.applyPorts(cfg, settings.ports); // 2c: the ports picked when another app held one
const logFile = path.join(p.logs, 'app.log');
const studioOrigin = () => new URL(cfg.studioUrl).origin;

const fakeExec = async (cmd, args, { onLine = () => {}, signal } = {}) => {
  if (cmd === '/usr/bin/sw_vers') return exec(cmd, args);
  const line = `fake: ${path.basename(cmd)} ${args.join(' ')}`;
  onLine(line);
  if (fakeFail && line.includes(fakeFail)) {
    fakeFail = null; // once: Try again goes on
    throw new Error(`${line}: failed (HELMSTUDIO_FAKE_FAIL)`);
  }
  await new Promise((r, j) => {
    const t = setTimeout(r, 800);
    signal?.addEventListener('abort', () => (clearTimeout(t), j(new Error('cancelled'))));
  });
  return '';
};
const run = fake ? fakeExec : exec;
const runners = fake
  ? {
      start() {},
      stop: async () => {},
      restart: async () => {},
      waitReady: async () => {},
      healthy: async () => true,
      state: () => Object.fromEntries(cfg.runners.map((r) => [r.env, 'run'])),
      issues: {},
      restartOne() {},
      note() {},
      logOf: (env) => path.join(p.logs, `runner-${env}.log`),
    }
  : new Runners({ p, cfg, settings, onChange: () => (refresh(), runnerIssues()) });
const stack = stackSteps({ p, cfg, settings, runners });

let phase = 'starting'; // starting | failed | ready | restarting | stopping
let status = { dot: 'wait', text: 'Starting…', sub: '' };
let mode = null;
let runner = null;
let setupWin = null;
let studioWin = null;
let tray = null;
let trayMenu = null;
let quitting = false;
// the error states (board Desktop.dc.html "Error states", 2a–2h)
const outage = new rules.Outage(); // 2a: the stack check's misses and notifications
let stackUp = true; // the last check
let lowDisk = ''; // 2e: "Only 4 GB free" while under 10 GB (checked every 10 minutes)
let studioIssue = null; // 2a {kind: 'down', at, rows} | 2g {kind: 'load', code, url, crashed}: the studio window's page
let studioPage = false; // the studio window shows our page (setup.html: 2a, 2g, a restart's start list), not the studio
let issueWin = null; // 2b: a runner that keeps stopping or didn't start
let runnerIssue = null; // {kind: 'runner', env, what, cause, lines, busy}
let note = null; // 2a's notification (kept, else it is collected before a click)
const quitSignal = new AbortController(); // Quit stops 2b's venv set up again
// the update (board frames 8–9): state checking | none | checkfail | available | download | verify | ready | installing
// | dlfail | sumfail | installfail; release: the newest one found
let upd = { state: 'none', release: null, got: 0, error: null };
let updWin = null;
let updAbort = null;
const shownUpdates = new Set(); // versions the check at start already showed
const version = app.getVersion();
// the bundle to replace: only the built app (make run is Electron from node_modules)
const bundle = res === process.resourcesPath ? path.resolve(process.execPath, '..', '..', '..') : null;

if (!app.requestSingleInstanceLock()) app.exit(0);

// an error no code path caught goes to the log, never to a dialog over the user's screen
const logError = (e) => {
  try {
    fs.mkdirSync(p.logs, { recursive: true });
    fs.appendFileSync(logFile, `${new Date().toISOString()} uncaught: ${e?.stack || e}\n`);
  } catch {}
};
process.on('uncaughtException', logError);
process.on('unhandledRejection', logError);

// ---------- status and the menu-bar item

const COLORS = { ok: [0x3f, 0xd2, 0x6b], wait: [0xf2, 0xc1, 0x4e], bad: [0xff, 0x8a, 0x80], upd: [0x4a, 0xa8, 0xff] };

// logo A as a template image: macOS tints it to the menu bar (white over a dark wallpaper even in light mode), as it
// does every other icon there. A template has no colour, so the status dot is the title beside it, in ANSI colour
const trayIcon = () => {
  const i = nativeImage.createFromPath(path.join(__dirname, '..', 'assets', 'trayTemplate.png'));
  i.setTemplateImage(true);
  return i;
};
const DOT_TITLE = { ok: '\x1b[32m⦁\x1b[0m', wait: '\x1b[33m⦁\x1b[0m', bad: '\x1b[31m⦁\x1b[0m' }; // ⦁: a 5 px dot in the menu bar font (• is 3, ● 11)
// an update waits: a small blue dot raised at the top right of the icon (˙ sits at cap height), before the status dot
const UPD_TITLE = '\x1b[34m˙\x1b[0m';
const updWaits = () => upd.release && !['none', 'checking', 'checkfail'].includes(upd.state);

function dotImage(dot) {
  const s = 16;
  const buf = Buffer.alloc(s * s * 4);
  const [r, g, b] = COLORS[dot];
  for (let i = 0; i < s * s; i++) if (Math.hypot((i % s) - 7.5, Math.floor(i / s) - 7.5) <= 6) buf.set([b, g, r, 255], i * 4);
  return nativeImage.createFromBitmap(buf, { width: s, height: s, scaleFactor: 2 });
}

function setStatus(dot, text, sub = '') {
  status = { dot, text, sub };
  if (!tray) return;
  tray.setTitle((updWaits() ? UPD_TITLE : '') + DOT_TITLE[dot]);
  const login = app.getLoginItemSettings().openAtLogin;
  tray.setContextMenu(
    (trayMenu = Menu.buildFromTemplate([
      { label: text, icon: dotImage(dot), enabled: false },
      ...(sub ? [{ label: sub, enabled: false }] : []),
      { type: 'separator' },
      ...(updWaits()
        ? [
            {
              label: upd.state === 'download' ? `Updating… ${Math.floor((100 * upd.got) / (upd.release.dmg.size || 1))}%` : upd.state === 'verify' || upd.state === 'installing' ? 'Updating…' : `Update to ${upd.release.version}`,
              icon: dotImage('upd'),
              click: showUpdate,
            },
            { type: 'separator' },
          ]
        : []),
      { label: 'Open', click: showApp },
      { label: 'Restart stack', enabled: phase === 'ready', click: restartStack },
      { label: 'Restart runners', enabled: phase === 'ready', click: restartRunners },
      { label: 'Open logs', click: () => (fs.mkdirSync(p.logs, { recursive: true }), shell.openPath(p.logs)) },
      { type: 'separator' },
      {
        label: 'Keep running',
        type: 'checkbox',
        checked: settings.keepRunning,
        click: (i) => ((settings.keepRunning = i.checked), settings.save()),
      },
      {
        label: 'Open at login',
        type: 'checkbox',
        checked: login,
        click: (i) => (app.setLoginItemSettings({ openAtLogin: i.checked }), setStatus(...Object.values(status))),
      },
      { type: 'separator' },
      { label: 'Check for updates…', click: () => checkUpdates(true) },
      { label: 'Quit', click: () => askQuit('menu') },
    ])),
  );
}

// 2h: while ready, the tray in the web status tile's words: Studio down, Runner down (restarting in n s, keeps
// stopping, didn't start), else Ready with a low disk or an unplugged models drive as its sub line
function refresh() {
  if (phase !== 'ready') return;
  const st = runners.state();
  const [env, s] = cfg.runners.map((r) => [r.env, st[r.env]]).find(([, x]) => x !== 'run') || [];
  if (!stackUp) return setStatus('bad', 'Studio down', 'The studio isn’t answering');
  if (s?.wait != null) return setStatus('wait', 'Runner down', `${env} runner restarting in ${Math.max(1, Math.ceil(s.wait / 1000))} s`);
  if (s) return setStatus('bad', 'Runner down', { keeps: `The ${env} runner keeps stopping`, nostart: `The ${env} runner didn’t start` }[s.kind] || `The ${env} runner stopped`);
  const drive = rules.missingVolume(settings.modelsDir, fs.existsSync);
  setStatus('ok', 'Ready', lowDisk || (drive ? `Models folder on ${drive} not connected` : ''));
}

const probe = (url) => fetch(url, { signal: AbortSignal.timeout(3000), redirect: 'manual' }).then((r) => r.status, () => null);
const hhmm = (t) => new Date(t).toTimeString().slice(0, 5);

// 2a: every 20 s (5 s after a miss): Kong and the web page (/), and studio through Kong (its one keyless path: 401
// is an answer, Kong's 502 or 503 is not). 3 misses in a row: the studio window's page says what is down, and one
// notification per outage. Back: the studio again by itself
let checking = false;
async function check() {
  if (phase !== 'ready' || fake || checking) return;
  checking = true;
  try {
    const [web, api] = (await Promise.all([probe(`${cfg.studioUrl}/`), probe(`${cfg.studioUrl}/v1/studio/plugin/takes`)])).map(rules.answers);
    stackUp = web && api;
    const o = outage.record(stackUp, Date.now());
    if (stackUp && studioIssue) reloadStudio();
    if (o.down) {
      const live = Object.values(runners.state()).filter((x) => x === 'run').length;
      const vm = await stack.vmUp();
      const row = (title, ok, yes, no) => ({ title, ok, state: ok ? yes : no });
      showPage({
        kind: 'down',
        at: hhmm(outage.since),
        rows: [
          row('VM', vm, 'running', 'stopped'),
          row('Studio', api, 'answering', 'not answering'),
          row('Web and Kong', web, 'answering', 'not answering'),
          { title: 'Runners', ok: live === cfg.runners.length, state: `${live} of ${cfg.runners.length}` },
        ],
      });
      if (o.notify && Notification.isSupported()) {
        note = new Notification({ title: 'helmstudio', body: 'helmstudio stopped answering. Open it to see what is down.' });
        note.on('click', showApp);
        note.show();
      }
    }
    refresh();
  } finally {
    checking = false;
  }
}

async function checkLoop() {
  await check().catch(logError);
  setTimeout(checkLoop, studioIssue ? 5000 : outage.every);
}

// 2e's sub line, every 10 minutes
function checkDisk() {
  const d = stack.freeDisk();
  lowDisk = d && d.free < 10 ? `Only ${Math.floor(d.free)} GB free` : '';
  refresh();
}

// ---------- windows

function setupWindow() {
  if (setupWin && !setupWin.isDestroyed()) return setupWin;
  const [width, height] = SIZES[mode] || [672, 500];
  setupWin = new BrowserWindow({
    width,
    height,
    resizable: false,
    fullscreenable: false,
    title: 'helmstudio',
    backgroundColor: '#16201b',
    show: false,
    webPreferences: { preload: path.join(__dirname, 'ui', 'preload.js'), contextIsolation: true, nodeIntegration: false, sandbox: true },
  });
  setupWin.webContents.on('will-navigate', (e) => e.preventDefault());
  setupWin.webContents.setWindowOpenHandler(() => ({ action: 'deny' }));
  setupWin.on('close', (e) => {
    if (!quitting && phase !== 'ready') (e.preventDefault(), hideAll());
  });
  setupWin.once('ready-to-show', () => reveal(setupWin));
  setupWin.loadFile(path.join(__dirname, 'ui', 'setup.html'));
  return setupWin;
}

const openExternal = (url) => /^https?:\/\//.test(url) && shell.openExternal(url);

function studioWindow() {
  if (studioWin && !studioWin.isDestroyed()) return studioWin;
  studioWin = new BrowserWindow({
    width: 1440,
    height: 900,
    ...settings.bounds,
    minWidth: 960,
    minHeight: 600,
    title: 'helmstudio',
    backgroundColor: '#0e1512',
    show: false,
    // the page is the web app as is: no Node, its own persistent session; the preload only passes the spelling of a
    // right-clicked word to the page's own menu (alpha.5 item 17)
    webPreferences: {
      contextIsolation: true,
      nodeIntegration: false,
      sandbox: true,
      partition: 'persist:studio',
      preload: path.join(__dirname, 'ui', 'studio-preload.js'),
    },
  });
  const wc = studioWin.webContents;
  // a right-click in a text field: the word under it and up to three suggestions, for the page's menu (it draws its
  // own; Electron shows none)
  wc.on('context-menu', (_e, p) => {
    if (p.isEditable) wc.send('spelling', spellingOf(p));
  });
  wc.on('will-navigate', (e, url) => {
    if (new URL(url).origin !== studioOrigin()) (e.preventDefault(), openExternal(url));
  });
  // 2g: the studio page failed to load (not a load we replaced: -3), crashed or hangs for 10 s; while the stack is down
  // (3 misses) 2a's page shows instead
  const failed = (_e, code, desc, url, main) => main && code !== -3 && !studioPage && loadFailed({ code: desc || `${code}`, url: url || cfg.studioUrl });
  wc.on('did-fail-load', failed);
  wc.on('did-fail-provisional-load', failed);
  let hang = null;
  let hung = false;
  wc.on('unresponsive', () => (clearTimeout(hang), (hang = setTimeout(() => ((hung = true), wc.forcefullyCrashRenderer()), 10000))));
  wc.on('responsive', () => clearTimeout(hang));
  wc.on('render-process-gone', (_e, d) => {
    clearTimeout(hang);
    if (d.reason !== 'clean-exit' && !quitting) loadFailed({ code: hung ? 'unresponsive' : d.reason, url: cfg.studioUrl, crashed: true });
    hung = false;
  });
  wc.setWindowOpenHandler(({ url }) => (openExternal(url), { action: 'deny' })); // target=_blank: the default browser
  // downloads: with no save path set, Electron asks with the standard save dialog
  let t;
  const keep = () => {
    clearTimeout(t);
    t = setTimeout(() => !studioWin.isDestroyed() && ((settings.bounds = studioWin.getNormalBounds()), settings.save()), 500);
  };
  studioWin.on('resize', keep);
  studioWin.on('move', keep);
  studioWin.on('close', (e) => {
    if (!quitting) (e.preventDefault(), hideAll());
  });
  studioWin.once('ready-to-show', () => reveal(studioWin));
  studioPage = !!studioIssue; // the stack went down before this window opened: 2a's page first
  if (studioPage) studioWin.loadFile(path.join(__dirname, 'ui', 'setup.html'));
  else studioWin.loadURL(cfg.studioUrl);
  return studioWin;
}

const pageSnap = () => ({ ...(snapshot() || {}), issue: studioIssue });

// the studio window's own page (2a, 2g, or with no issue a restart's start list); a window not opened yet shows it
// when it opens. It never pops up by itself: the notification and the menu bar's Open show it
function showPage(issue) {
  studioIssue = issue;
  if (!studioWin || studioWin.isDestroyed()) return;
  if (!studioPage) {
    studioPage = true;
    studioWin.loadFile(path.join(__dirname, 'ui', 'setup.html'));
  } else studioWin.webContents.send('update', pageSnap());
}

function loadFailed(issue) {
  fs.mkdirSync(p.logs, { recursive: true });
  fs.appendFileSync(logFile, `${new Date().toISOString()} studio page: ${issue.code} ${issue.url}\n`);
  if (outage.misses < 3) showPage({ kind: 'load', ...issue });
}

function reloadStudio() {
  studioIssue = null;
  studioPage = false;
  if (studioWin && !studioWin.isDestroyed()) studioWin.loadURL(cfg.studioUrl);
}

// 2b: a runner given up opens its window once (while ready; at start the step's failure says it)
function runnerIssues() {
  if (phase !== 'ready') return;
  for (const [env, i] of Object.entries(runners.issues)) {
    if (i.shown) continue;
    i.shown = true;
    showRunnerIssue({ kind: 'runner', env, what: i.kind, cause: i.cause, lines: i.lines, busy: false });
  }
}

function showRunnerIssue(issue) {
  runnerIssue = issue;
  if (issueWin && !issueWin.isDestroyed()) return (issueWin.webContents.send('update', { issue }), reveal(issueWin));
  issueWin = new BrowserWindow({
    width: 460,
    height: 330,
    resizable: false,
    fullscreenable: false,
    minimizable: false,
    title: 'helmstudio',
    backgroundColor: '#16201b',
    show: false,
    webPreferences: { preload: path.join(__dirname, 'ui', 'preload.js'), contextIsolation: true, nodeIntegration: false, sandbox: true },
  });
  issueWin.webContents.on('will-navigate', (e) => e.preventDefault());
  issueWin.webContents.setWindowOpenHandler(() => ({ action: 'deny' }));
  issueWin.once('ready-to-show', () => reveal(issueWin));
  issueWin.on('closed', () => ((issueWin = null), (runnerIssue = null))); // closing leaves it stopped
  issueWin.loadFile(path.join(__dirname, 'ui', 'setup.html'));
}

// 2b's Restart runner: the count reset and the runner started now; one that didn't start has its venv set up again
// first (uv sync, in the runner's log)
async function restartRunner() {
  const i = runnerIssue;
  if (!i || i.busy) return;
  const r = cfg.runners.find((x) => x.env === i.env);
  if (i.what === 'nostart') {
    showRunnerIssue({ ...i, busy: true });
    try {
      await stack.setupVenv(r.venv, (cmd, args, o) => run(cmd, args, { ...o, signal: quitSignal.signal, onLine: (l) => runners.note(i.env, l) }));
    } catch (e) {
      if (quitting) return;
      runners.note(i.env, `setup failed: ${e.message}`);
      return showRunnerIssue({ ...i, busy: false, cause: `Setting it up again failed: ${e.message}`, lines: tailOf(runners.logOf(i.env), 4) });
    }
  }
  if (quitting) return; // Quit while it was set up: nothing starts after the stop
  issueWin?.close();
  runners.restartOne(i.env);
}

// HELMSTUDIO_HIDE=1 (test runs): windows are never shown, so a driven run stays off the screen
function reveal(w) {
  if (process.env.HELMSTUDIO_HIDE === '1' || w.isDestroyed()) return;
  app.dock?.show();
  w.show();
  w.focus();
}

function hideAll() {
  for (const w of BrowserWindow.getAllWindows()) w.hide();
  app.dock?.hide();
}

function showApp() {
  reveal(phase === 'ready' || phase === 'restarting' ? studioWindow() : setupWindow());
}

// ---------- the steps

function send() {
  if (setupWin && !setupWin.isDestroyed()) setupWin.webContents.send('update', snapshot());
  if (studioPage && !studioIssue && studioWin && !studioWin.isDestroyed()) studioWin.webContents.send('update', pageSnap());
}

// the setup window's size for the start list, the first open, a failure, or a check that asks (2c, 2d, 2e)
const SIZES = { start: [420, 340], restart: [420, 340], update: [460, 420] };
const ASKS = new Set(['port', 'drive', 'disk']);
function fit(snap) {
  if (!setupWin || setupWin.isDestroyed() || mode === 'first') return;
  const [w, h] = snap.error ? [672, 500] : snap.ask && ASKS.has(snap.ask.kind) ? [460, 330] : SIZES[mode];
  const [cw, ch] = setupWin.getSize();
  if (cw !== w || ch !== h) setupWin.setSize(w, h);
}
const snapshot = () => runner && { mode, version, ...runner.snapshot() };

// again: 'restart' (2a's Restart, the menu's Restart stack), a new start list over a running app
async function startUp(again) {
  if (!runner || again) {
    mode = again || openMode(settings, version);
    runner = new StepRunner({
      steps: { first: stack.first, update: stack.update, start: stack.every, restart: stack.restart }[mode],
      stateFile: mode === 'first' ? p.state : null,
      logFile,
      exec: run,
      pipe: fake ? (a, b, o) => fakeExec(a.cmd, [...a.args, '|', b.cmd, ...b.args], o) : pipe,
    });
    runner.on('update', (snap) => {
      const cur = snap.steps.find((s) => s.status === 'run' || s.status === 'ask');
      if (phase === 'starting') setStatus('wait', 'Starting…', cur ? [cur.title, cur.detail].filter(Boolean).join(' · ') : '');
      fit(snap);
      send();
    });
  }
  phase = 'starting';
  setStatus('wait', 'Starting…');
  // a restart over the open studio window shows its start list there (2a: the rows turn to the start list)
  if (!(studioPage && !studioIssue && studioWin?.isVisible())) setupWindow();
  const result = await runner.run();
  if (result === 'done') {
    if (!settings.installed || settings.version !== version) Object.assign(settings, { installed: true, version }).save();
    phase = 'ready';
    stackUp = true;
    outage.record(true, Date.now());
    checkDisk();
    const visible = setupWin?.isVisible();
    setupWin?.destroy();
    setupWin = null;
    if (studioPage) reloadStudio(); // a restart over the studio window: the studio again
    if (visible) {
      const had = studioWin && !studioWin.isDestroyed();
      const w = studioWindow(); // a new one shows itself when ready
      if (had) reveal(w);
    }
  } else if (result === 'failed') {
    phase = 'failed';
    setStatus('bad', 'Something is down', runner.error?.title);
  }
}

async function restartStack() {
  if (phase !== 'ready') return;
  issueWin?.close();
  if (studioWin && !studioWin.isDestroyed() && studioWin.isVisible()) showPage(null);
  else studioIssue = null;
  startUp('restart');
}

async function restartRunners() {
  issueWin?.close(); // the stop counts start again
  phase = 'restarting'; // check() waits: the runners' exits and spawns would flip the dot to down, then to Ready early
  setStatus('wait', 'Starting…', 'Runners');
  await runners.restart();
  await runners.waitReady().catch(() => {});
  if (phase !== 'restarting') return; // Quit meanwhile
  phase = 'ready';
  refresh();
  runnerIssues(); // one given up meanwhile
  check();
}

// 2f: the runs going (the caller's queued and running ones, and everyone else's queued), asked of studio with the
// studio window's sign-in; 0 when it cannot tell (the stack down, signed out)
async function runsGoing() {
  if (fake || phase !== 'ready') return 0;
  const ask = session
    .fromPartition('persist:studio')
    .fetch(`${cfg.studioUrl}/api/v1/studio/queue`, { credentials: 'include' })
    .then((r) => (r.ok ? r.json() : null));
  const q = await Promise.race([ask, new Promise((r) => setTimeout(r, 3000))]).catch(() => null);
  return (q?.runs?.length || 0) + (q?.hosts || []).reduce((n, h) => n + (h.others_queued || 0), 0);
}

// 2f: via menu (the menu bar's Quit, 2a's Quit) or cmdq (⌘Q, the Dock's Quit); Keep running is the default (Return)
let askingQuit = false;
async function askQuit(via) {
  if (phase === 'stopping' || askingQuit) return;
  if (via === 'cmdq' && settings.keepRunning) return hideAll();
  askingQuit = true;
  try {
    const runs = await runsGoing();
    if (rules.quitAsks({ via, keepRunning: settings.keepRunning, runs })) {
      const { response } = await dialog.showMessageBox({
        message: `${runs} ${runs === 1 ? 'run is' : 'runs are'} going. Quit and stop them?`,
        detail: 'They show as failed in Create; Retry starts them again.',
        buttons: ['Keep running', 'Quit'],
        defaultId: 0,
        cancelId: 0,
      });
      if (response !== 1) return;
    }
  } finally {
    askingQuit = false;
  }
  quitEntirely();
}

// then: what runs after the stack stopped, before the app exits (the update's swap)
async function quitEntirely(then = () => {}) {
  if (phase === 'stopping') return;
  updAbort?.abort();
  phase = 'stopping';
  quitting = true;
  quitSignal.abort();
  setStatus('wait', 'Stopping…');
  runner?.cancel();
  const log = (l) => runner?.log(l);
  await stack.stopAll((cmd, args, o) => run(cmd, args, { ...o, onLine: log }));
  then();
  tray?.destroy();
  app.exit(0);
}

// ---------- updates (board frames 8–9): check, download on Install, SHA256 check, swap on Restart

function setUpd(next) {
  upd = { ...upd, ...next };
  setStatus(...Object.values(status)); // the menu's update item and the blue dot
  if (updWin && !updWin.isDestroyed()) updWin.webContents.send('upd', updSnap());
}
const updSnap = () => ({ ...upd, current: version });

// manual: opens the window and shows the outcome; the check at start and every 24 h is quiet unless it finds one
async function checkUpdates(manual = false) {
  if (['download', 'verify', 'installing'].includes(upd.state)) return manual && showUpdate();
  if (manual) (setUpd({ state: 'checking' }), showUpdate());
  try {
    const release = await updates.check(version);
    if (upd.state === 'ready' && release?.version === upd.release?.version) return; // downloaded already
    setUpd({ state: release ? 'available' : 'none', release, got: 0, error: null });
    if (release && !manual && !shownUpdates.has(release.version)) (shownUpdates.add(release.version), showUpdate());
  } catch (e) {
    fs.mkdirSync(p.logs, { recursive: true });
    fs.appendFileSync(logFile, `${new Date().toISOString()} update check: ${e.message}\n`);
    if (manual) setUpd({ state: 'checkfail' });
  }
}

function showUpdate() {
  if (!updWin || updWin.isDestroyed()) {
    updWin = new BrowserWindow({
      width: 420,
      height: 320,
      resizable: false,
      fullscreenable: false,
      minimizable: false,
      title: 'helmstudio',
      backgroundColor: '#16201b',
      show: false,
      webPreferences: { preload: path.join(__dirname, 'ui', 'preload.js'), contextIsolation: true, nodeIntegration: false, sandbox: true },
    });
    updWin.webContents.on('will-navigate', (e) => e.preventDefault());
    updWin.webContents.setWindowOpenHandler(() => ({ action: 'deny' }));
    updWin.once('ready-to-show', () => reveal(updWin));
    updWin.loadFile(path.join(__dirname, 'ui', 'update.html'));
  } else reveal(updWin);
}

const dmgFile = () => path.join(p.home, 'updates', upd.release.dmg.name);

// Install: download (Cancel stops it and deletes the part), then the SHA256 against the release's SHA256SUMS
async function downloadUpdate() {
  const why = updates.installProblem(bundle);
  if (why) return setUpd({ state: 'installfail', error: why });
  const { release } = upd;
  const file = dmgFile();
  fs.rmSync(path.dirname(file), { recursive: true, force: true }); // an earlier download
  fs.mkdirSync(path.dirname(file), { recursive: true });
  updAbort = new AbortController();
  const signal = updAbort.signal;
  setUpd({ state: 'download', got: 0, error: null });
  let last = 0;
  try {
    await updates.download(release.dmg.url, file, {
      signal,
      onBytes: (n) => {
        upd.got += n;
        if (Date.now() - last > 250) ((last = Date.now()), setUpd({}));
      },
    });
  } catch (e) {
    fs.appendFileSync(logFile, `${new Date().toISOString()} update download: ${e.message}\n`);
    return setUpd({ state: signal.aborted ? 'available' : 'dlfail', got: 0 });
  }
  setUpd({ state: 'verify' });
  try {
    const want = updates.sumFor(await (await fetch(release.sums, { headers: { 'User-Agent': 'helmstudio-app' }, signal })).text(), release.dmg.name);
    const got = await updates.sha256(file);
    if (signal.aborted) throw new Error('cancelled');
    if (want !== got) {
      fs.rmSync(file, { force: true });
      fs.appendFileSync(logFile, `${new Date().toISOString()} update check: ${release.dmg.name} is ${got}, SHA256SUMS says ${want}\n`);
      return setUpd({ state: 'sumfail' });
    }
  } catch (e) {
    fs.rmSync(file, { force: true });
    return setUpd({ state: signal.aborted ? 'available' : 'dlfail', got: 0 });
  }
  setUpd({ state: 'ready' });
}

// Restart: stop it all the way Quit does, then the detached script swaps the bundle and opens it again
function installUpdate() {
  const why = updates.installProblem(bundle);
  if (why) return setUpd({ state: 'installfail', error: why });
  setUpd({ state: 'installing' });
  const dmg = dmgFile();
  quitEntirely(() => updates.install({ dmg, bundle, logFile: path.join(p.logs, 'update.log') }));
}

ipcMain.handle('upd-hello', () => updSnap());
ipcMain.on('upd', (_e, action) => {
  if (action === 'install' && ['available', 'dlfail', 'sumfail'].includes(upd.state)) downloadUpdate();
  else if (action === 'cancel') updAbort?.abort();
  else if (action === 'restart' && upd.state === 'ready') installUpdate();
  else if (action === 'recheck') checkUpdates(true);
  else if (action === 'notes' && upd.release) openExternal(upd.release.page);
  else if (action === 'close') updWin?.close();
});

// ---------- the app

// a page asks for its state: the studio window's page (2a, 2g, a restart), 2b's window, else the setup window's steps
ipcMain.handle('hello', (e) => {
  if (studioWin && !studioWin.isDestroyed() && e.sender === studioWin.webContents) return pageSnap();
  if (issueWin && !issueWin.isDestroyed() && e.sender === issueWin.webContents) return { issue: runnerIssue };
  return snapshot();
});
// the error states' buttons, only from our own pages (file:), never from the studio's web page
ipcMain.on('act', (e, name) => {
  if (!e.senderFrame?.url.startsWith('file:') || quitting) return;
  const logs = () => fs.mkdirSync(p.logs, { recursive: true });
  ({
    restart: restartStack,
    quit: () => askQuit('menu'),
    reload: reloadStudio,
    'restart-runner': restartRunner,
    'log-folder': () => (logs(), fs.appendFileSync(logFile, ''), shell.showItemInFolder(logFile)),
    'runner-log': () => runnerIssue && (logs(), fs.appendFileSync(runners.logOf(runnerIssue.env), ''), shell.openPath(runners.logOf(runnerIssue.env))),
    'open-storage': () => shell.openExternal('x-apple.systempreferences:com.apple.settings.Storage'),
  })[name]?.();
});
// the studio page's spelling items: only from the studio window, only a word
const fromStudio = (e, word) => studioWin && !studioWin.isDestroyed() && e.sender === studioWin.webContents && isWord(word);
ipcMain.on('replace-misspelling', (e, word) => fromStudio(e, word) && e.sender.replaceMisspelling(word));
ipcMain.on('add-to-dictionary', (e, word) => fromStudio(e, word) && e.sender.session.addWordToSpellCheckerDictionary(word));
ipcMain.on('answer', (e, kind, value) => e.senderFrame?.url.startsWith('file:') && runner?.answer(kind, value)); // our pages only
ipcMain.on('retry', () => phase === 'failed' && startUp());
ipcMain.on('show-log', () => (fs.mkdirSync(p.logs, { recursive: true }), fs.appendFileSync(logFile, ''), shell.openPath(logFile)));
ipcMain.handle('choose-folder', async (e, current) => {
  const r = await dialog.showOpenDialog(BrowserWindow.fromWebContents(e.sender), {
    defaultPath: current?.replace(/^~/, app.getPath('home')),
    properties: ['openDirectory', 'createDirectory'],
    buttonLabel: 'Choose',
  });
  return r.canceled ? null : r.filePaths[0];
});
// the studio page's model folders (plan 40 P5): its Browse… (a folder for a path field, written ~/…) and Show / Open in
// Finder (shown, never opened or run), only from the studio window, only a checked path that exists
const isStudio = (e) => studioWin && !studioWin.isDestroyed() && e.sender === studioWin.webContents;
ipcMain.handle('studio-choose-folder', async (e, current) => {
  if (!isStudio(e)) return null;
  const home = app.getPath('home');
  const r = await dialog.showOpenDialog(studioWin, {
    defaultPath: localPath(current, home) ?? undefined,
    properties: ['openDirectory', 'createDirectory'],
    buttonLabel: 'Choose',
  });
  return r.canceled ? null : tilde(r.filePaths[0], home);
});
// ponytail: any existing local path the page names is shown; the host's folder list lives in studio, not here
ipcMain.handle('studio-reveal', (e, p) => {
  const abs = isStudio(e) && localPath(p, app.getPath('home'));
  if (!abs || !fs.existsSync(abs)) return false;
  shell.showItemInFolder(abs);
  return true;
});

app.on('second-instance', showApp);
app.on('activate', showApp);
app.on('window-all-closed', () => {}); // the menu-bar item stays
app.on('before-quit', (e) => {
  if (quitting) return;
  e.preventDefault(); // Cmd+Q, the Dock's Quit, Quit in the app menu
  askQuit('cmdq');
});

// the end-to-end run's hook (Playwright's _electron evaluates in this process): the menu-bar menu and the state
if (process.env.HELMSTUDIO_TEST === '1') global.helmTest = { menu: () => trayMenu, title: () => tray?.getTitle(), state: () => ({ phase, mode, status, upd: updSnap(), studioIssue, runnerIssue }) };

app.whenReady().then(() => {
  Menu.setApplicationMenu(
    Menu.buildFromTemplate([{ role: 'appMenu' }, { role: 'editMenu' }, { role: 'viewMenu' }, { role: 'windowMenu' }]),
  );
  // log out, restart, shut down: let the quit through (else before-quit cancels the Mac's log out), stop the runners
  powerMonitor.on('shutdown', () => ((quitting = true), runners.stop()));
  tray = new Tray(trayIcon());
  tray.setToolTip('helmstudio');
  setStatus('wait', 'Starting…');
  setTimeout(checkLoop, 20000);
  setInterval(checkDisk, 10 * 60 * 1000);
  // 2h: "mlx runner restarting in 8 s" counts down
  setInterval(() => Object.values(runners.state()).some((x) => x?.wait != null) && refresh(), 1000);
  startUp();
  setTimeout(checkUpdates, 5000);
  setInterval(checkUpdates, 24 * 3600 * 1000);
});
