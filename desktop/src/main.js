// helmstudio for Mac (docs/plan/31-mac-app.md): first open sets everything up, every open starts it, then one window
// on the app's Kong. Dock icon plus a menu-bar item; quitting keeps the background running (decision 2), Quit entirely
// stops the runners, the stack and the VM.
// HELMSTUDIO_HOME moves the app's folder (a scratch run); HELMSTUDIO_FAKE=1 fakes every command (no VM, no Docker),
// HELMSTUDIO_FAKE_FAIL=<word> then fails one of them (the failed-step screen).
const path = require('node:path');
const fs = require('node:fs');
const { app, BrowserWindow, Tray, Menu, nativeImage, nativeTheme, shell, dialog, ipcMain } = require('electron');
const { StepRunner, exec, pipe } = require('./steps');
const { layout, loadSettings, stackSteps, Runners } = require('./stack');
const cfg = require('../stack.json');

const home = process.env.HELMSTUDIO_HOME || path.join(app.getPath('appData'), 'helmstudio');
if (process.env.HELMSTUDIO_HOME) app.setPath('userData', path.join(home, 'electron'));
// the bundle's Resources when packaged (the executable keeps Electron's name, so app.isPackaged says no), else
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

let phase = 'starting'; // starting | failed | ready | stopping
let status = { dot: 'wait', text: 'Starting…', sub: '' };
let mode = null;
let runner = null;
let setupWin = null;
let studioWin = null;
let tray = null;
let quitting = false;

if (!app.requestSingleInstanceLock()) app.exit(0);

// ---------- status and the menu-bar item

const COLORS = { ok: [0x3f, 0xd2, 0x6b], wait: [0xf2, 0xc1, 0x4e], bad: [0xff, 0x8a, 0x80] };

// logo A in one colour (white on a dark menu bar) with the status dot at its corner, drawn into the @2x bitmap
function trayImage(dot) {
  const src = nativeImage.createFromPath(path.join(__dirname, '..', 'assets', 'trayTemplate.png'));
  const buf = Buffer.from(src.toBitmap({ scaleFactor: 2 })); // BGRA, square
  const w = Math.sqrt(buf.length / 4);
  const h = w;
  const ink = nativeTheme.shouldUseDarkColors ? 255 : 0;
  const [r, g, b] = COLORS[dot];
  for (let y = 0; y < h; y++) {
    for (let x = 0; x < w; x++) {
      const i = (y * w + x) * 4;
      const d = Math.hypot(x - (w - 6.5), y - (h - 6.5));
      if (d <= 4.5) buf.set([b, g, r, 255], i);
      else if (d <= 6.5) buf.set([0, 0, 0, 0], i); // a gap around the dot
      else buf.set([(ink * buf[i + 3]) / 255, (ink * buf[i + 3]) / 255, (ink * buf[i + 3]) / 255], i);
    }
  }
  return nativeImage.createFromBitmap(buf, { width: w, height: h, scaleFactor: 2 });
}

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
  tray.setImage(trayImage(dot));
  const login = app.getLoginItemSettings().openAtLogin;
  tray.setContextMenu(
    Menu.buildFromTemplate([
      { label: text, icon: dotImage(dot), enabled: false },
      ...(sub ? [{ label: sub, enabled: false }] : []),
      { type: 'separator' },
      { label: 'Open', click: showApp },
      { label: 'Restart runners', enabled: phase === 'ready', click: restartRunners },
      { label: 'Open logs', click: () => (fs.mkdirSync(p.logs, { recursive: true }), shell.openPath(p.logs)) },
      { type: 'separator' },
      {
        label: 'Keep running after quit',
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
      { label: 'Quit entirely', click: quitEntirely },
    ]),
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
  const small = mode === 'start';
  setupWin = new BrowserWindow({
    width: small ? 420 : 672,
    height: small ? 340 : 500,
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
  setupWin.once('ready-to-show', () => setupWin.show());
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
  studioWin.once('ready-to-show', () => studioWin.show());
  studioWin.loadURL(cfg.studioUrl);
  return studioWin;
}

function hideAll() {
  for (const w of BrowserWindow.getAllWindows()) w.hide();
  app.dock?.hide();
}

function showApp() {
  app.dock?.show();
  const w = phase === 'ready' ? studioWindow() : setupWindow();
  w.show();
  w.focus();
}

// ---------- the steps

function send() {
  if (setupWin && !setupWin.isDestroyed()) setupWin.webContents.send('update', snapshot());
}
const snapshot = () => runner && { mode, ...runner.snapshot() };

async function startUp() {
  if (!runner) {
    mode = settings.installed ? 'start' : 'first';
    runner = new StepRunner({
      steps: mode === 'first' ? stack.first : stack.every,
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
    if (mode === 'first') ((settings.installed = true), settings.save());
    phase = 'ready';
    setStatus('ok', 'Ready');
    const visible = setupWin?.isVisible();
    setupWin?.destroy();
    setupWin = null;
    if (visible) studioWindow();
  } else if (result === 'failed') {
    phase = 'failed';
    setStatus('bad', 'Something is down', runner.error?.title);
    if (mode === 'start') setupWin?.setContentSize(672, 500); // the failed step's pane needs the room
  }
}

async function restartRunners() {
  setStatus('wait', 'Starting…', 'Runners');
  await runners.restart();
  await runners.waitReady().catch(() => {});
  setStatus('ok', 'Ready');
  check();
}

async function quitEntirely() {
  if (phase === 'stopping') return;
  phase = 'stopping';
  quitting = true;
  setStatus('wait', 'Stopping…');
  runner?.cancel();
  const log = (l) => runner?.log(l);
  await stack.stopAll((cmd, args, o) => run(cmd, args, { ...o, onLine: log }));
  tray?.destroy();
  app.exit(0);
}

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

app.whenReady().then(() => {
  Menu.setApplicationMenu(
    Menu.buildFromTemplate([{ role: 'appMenu' }, { role: 'editMenu' }, { role: 'viewMenu' }, { role: 'windowMenu' }]),
  );
  tray = new Tray(trayImage('wait'));
  tray.setToolTip('helmstudio');
  nativeTheme.on('updated', () => setStatus(...Object.values(status)));
  setStatus('wait', 'Starting…');
  setInterval(check, 20000);
  startUp();
});
