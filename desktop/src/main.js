// helmstudio for Mac (docs/plan/31-mac-app.md): first open sets everything up, every open starts it, then one window
// on the app's Kong. Dock icon plus a menu-bar item; quitting keeps the background running (decision 2), Quit
// stops the runners, the stack and the VM.
// HELMSTUDIO_HOME moves the app's folder (a scratch run); HELMSTUDIO_FAKE=1 fakes every command (no VM, no Docker),
// HELMSTUDIO_FAKE_FAIL=<word> then fails one of them (the failed-step screen); HELMSTUDIO_UPDATE_URL (tests) points
// the update check at a fake releases list; HELMSTUDIO_HIDE=1 (tests) never shows a window.
const path = require('node:path');
const fs = require('node:fs');
const { app, BrowserWindow, Tray, Menu, nativeImage, shell, dialog, ipcMain, powerMonitor } = require('electron');
const { StepRunner, exec, pipe } = require('./steps');
const { layout, loadSettings, stackSteps, Runners, openMode } = require('./stack');
const updates = require('./update');
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
const logFile = path.join(p.logs, 'app.log');
const studioOrigin = new URL(cfg.studioUrl).origin;

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
  ? { start() {}, stop: async () => {}, restart: async () => {}, waitReady: async () => {}, alive: () => true, healthy: async () => true }
  : new Runners({ p, cfg, settings, onChange: () => phase === 'ready' && check() });
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
      { label: 'Quit', click: () => quitEntirely() },
    ])),
  );
}

// while ready: the studio answers and every runner runs, else "Something is down"
async function check() {
  if (phase !== 'ready' || fake) return;
  const web = await fetch(cfg.studioUrl, { signal: AbortSignal.timeout(3000) }).then((r) => r.status < 500, () => false);
  const ok = web && runners.alive();
  if (ok && status.dot !== 'ok') setStatus('ok', 'Ready');
  if (!ok) setStatus('bad', 'Something is down', web ? 'A runner stopped' : 'The studio does not answer');
}

// ---------- windows

function setupWindow() {
  if (setupWin && !setupWin.isDestroyed()) return setupWin;
  const [width, height] = { start: [420, 340], update: [460, 420] }[mode] || [672, 500];
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
    // the page is the web app as is: no Node, no preload, its own persistent session
    webPreferences: { contextIsolation: true, nodeIntegration: false, sandbox: true, partition: 'persist:studio' },
  });
  const wc = studioWin.webContents;
  wc.on('will-navigate', (e, url) => {
    if (new URL(url).origin !== studioOrigin) (e.preventDefault(), openExternal(url));
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
  studioWin.loadURL(cfg.studioUrl);
  return studioWin;
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
}
const snapshot = () => runner && { mode, version, ...runner.snapshot() };

async function startUp() {
  if (!runner) {
    mode = openMode(settings, version);
    runner = new StepRunner({
      steps: { first: stack.first, update: stack.update, start: stack.every }[mode],
      stateFile: mode === 'first' ? p.state : null,
      logFile,
      exec: run,
      pipe: fake ? (a, b, o) => fakeExec(a.cmd, [...a.args, '|', b.cmd, ...b.args], o) : pipe,
    });
    runner.on('update', (snap) => {
      const cur = snap.steps.find((s) => s.status === 'run' || s.status === 'ask');
      if (phase === 'starting') setStatus('wait', 'Starting…', cur ? [cur.title, cur.detail].filter(Boolean).join(' · ') : '');
      send();
    });
  }
  phase = 'starting';
  setStatus('wait', 'Starting…');
  setupWindow();
  const result = await runner.run();
  if (result === 'done') {
    if (!settings.installed || settings.version !== version) Object.assign(settings, { installed: true, version }).save();
    phase = 'ready';
    setStatus('ok', 'Ready');
    const visible = setupWin?.isVisible();
    setupWin?.destroy();
    setupWin = null;
    if (visible) studioWindow();
  } else if (result === 'failed') {
    phase = 'failed';
    setStatus('bad', 'Something is down', runner.error?.title);
    if (mode !== 'first') setupWin?.setContentSize(672, 500); // the failed step's pane needs the room
  }
}

async function restartRunners() {
  phase = 'restarting'; // check() waits: the runners' exits and spawns would flip the dot to down, then to Ready early
  setStatus('wait', 'Starting…', 'Runners');
  await runners.restart();
  await runners.waitReady().catch(() => {});
  if (phase !== 'restarting') return; // Quit meanwhile
  phase = 'ready';
  setStatus('ok', 'Ready');
  check();
}

// then: what runs after the stack stopped, before the app exits (the update's swap)
async function quitEntirely(then = () => {}) {
  if (phase === 'stopping') return;
  updAbort?.abort();
  phase = 'stopping';
  quitting = true;
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

ipcMain.handle('hello', () => snapshot());
ipcMain.on('answer', (_e, kind, value) => runner?.answer(kind, value));
ipcMain.on('retry', () => phase === 'failed' && startUp());
ipcMain.on('show-log', () => (fs.mkdirSync(p.logs, { recursive: true }), fs.appendFileSync(logFile, ''), shell.openPath(logFile)));
ipcMain.handle('choose-folder', async (_e, current) => {
  const r = await dialog.showOpenDialog(setupWin, {
    defaultPath: current?.replace(/^~/, app.getPath('home')),
    properties: ['openDirectory', 'createDirectory'],
    buttonLabel: 'Choose',
  });
  return r.canceled ? null : r.filePaths[0];
});

app.on('second-instance', showApp);
app.on('activate', showApp);
app.on('window-all-closed', () => {}); // the menu-bar item stays
app.on('before-quit', (e) => {
  if (quitting) return;
  e.preventDefault(); // Cmd+Q, the Dock's Quit, Quit in the app menu
  if (settings.keepRunning) hideAll();
  else quitEntirely();
});

// the end-to-end run's hook (Playwright's _electron evaluates in this process): the menu-bar menu and the state
if (process.env.HELMSTUDIO_TEST === '1') global.helmTest = { menu: () => trayMenu, title: () => tray?.getTitle(), state: () => ({ phase, mode, status, upd: updSnap() }) };

app.whenReady().then(() => {
  Menu.setApplicationMenu(
    Menu.buildFromTemplate([{ role: 'appMenu' }, { role: 'editMenu' }, { role: 'viewMenu' }, { role: 'windowMenu' }]),
  );
  // log out, restart, shut down: let the quit through (else before-quit cancels the Mac's log out), stop the runners
  powerMonitor.on('shutdown', () => ((quitting = true), runners.stop()));
  tray = new Tray(trayIcon());
  tray.setToolTip('helmstudio');
  setStatus('wait', 'Starting…');
  setInterval(check, 20000);
  startUp();
  setTimeout(checkUpdates, 5000);
  setInterval(checkUpdates, 24 * 3600 * 1000);
});
