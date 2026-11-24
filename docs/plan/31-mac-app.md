# helmstudio-next plan — The Mac app: a launcher, the quick path

[← Plan index](../README.md)

Status: **decided by the owner (2026-11-25, decisions below); board not drawn yet.** Not in a milestone, like plans 25–30. It
narrows [09 §12](09-scope-and-risks.md) ("a desktop app shell … deliberately not in v1"): a personal launcher is now
in, an installer for other Macs is still out.

The owner's ask, 2026-11-25: "now plan a mac desktop app quicker path". Earlier answers (2026-11-23) set the shape: the
same web app in its own window, plus starting and stopping the stack and the Mac runners; Electron picked over Tauri
and SwiftUI because it renders with Chromium, the engine the e2e suite tests in.

## 35. The launcher

### 35.1 What it is

`helmstudio.app` on this Mac, opened from the Dock or Spotlight. On open it:

1. Starts OrbStack if it is not running (`open -a OrbStack`), waits for `docker info` (up to 60 s).
2. Starts the dev stack without a rebuild: a new root target `make start` (as `make up`, minus `--build`: secrets,
   envs, the nats reload, `up -d --wait`). About 10–30 s when the images exist; `make up` stays as is.
3. Starts the Mac runners: `make runners` (already a no-op for a runner that runs).
4. Waits for Kong `127.0.0.1:8300` and the runners' `/health` (8091–8093), showing each step on a small start
   screen; then shows the studio at `http://127.0.0.1:8300` in its window. A step that fails says so in plain words
   with **Show log** and **Try again**.

A menu-bar item shows one status dot (ready / starting / something down) and a menu: **Open**, **Restart runners**,
**Rebuild and restart** (`make up` then `make runners`), **Open logs**, **Keep running after quit** (a tick, on),
**Open at login** (a tick, off), **Quit entirely**.

Quitting the app (Cmd+Q, the Dock's Quit, or closing the window) keeps the background running: the dev stack and the
runners stay up, the window and Dock icon go, and the menu-bar item stays, so opening the app again is instant. A
**Keep running after quit** tick in the menu-bar menu (on by default) turns this off.

**Quit entirely** in the menu-bar menu stops everything: the runners (`make runners-stop`), then the dev stack
(`make down`; data volumes stay), then the app and its menu-bar item. The next open starts both again.

### 35.2 What it is not (the quick path)

- It runs from this repo checkout (it calls `make` in it; the path is set when the app is built). No copy of the
  services, images or models inside the app.
- Built for this Mac only: Apple Silicon, ad-hoc signed, copied to `/Applications`. A locally built app is not
  quarantined, so no Developer ID is needed.
- Later, not now: a signed and notarized DMG for other Macs (Apple Developer ID, $99 a year), auto-update, first-run
  setup without the repo (pulled images, secrets made by the app), Windows and Linux.

### 35.3 The window

- One `BrowserWindow` on `http://127.0.0.1:8300`, its own persistent session (sign in once, like a browser profile).
- No Node in the page: `contextIsolation` on, `nodeIntegration` off, no preload API; the page is the web app as is.
- It never leaves the studio: other origins and `target=_blank` links open in the default browser.
- Downloads (exports, takes) use the standard save dialog. Web notifications work as in Chrome.
- Size and position are kept between opens. `Cmd+R` reloads; the app menu has the usual Edit items so copy and paste
  work in the page.
- Icon: logo A from the [Logo](../mock/Logo.dc.html) board, as `.icns`.

### 35.4 Code and checks

- A new top-level folder `desktop/`, isolated like a service (CLAUDE.md): its own `package.json`, lockfile,
  `Makefile`, `.env.example` (the repo path, the URL); no Dockerfile (it is not a container). Never imports another
  service's code.
- Electron and the packager are pinned in [14-dependencies](14-dependencies.md) at build time (Node as the web's).
- `make -C desktop test lint`: unit tests for the start steps and status logic (commands faked), and one Playwright
  `_electron` smoke test that opens the app against the **test** stack (`127.0.0.1:8390`) with the start commands
  faked, never the dev stack.
- `make -C desktop app` builds `helmstudio.app`; `make -C desktop install` copies it to `/Applications`.
- Size: about 200 MB on disk, 200–400 MB of RAM while open.

### 35.5 Board first

A Desktop board on the canvas before code: the start screen (each step, a failed step with Show log / Try again),
the menu-bar menu in each status, and the window's title bar. The owner approves it, then the build.

### 35.6 Cost

About a day: half for the board and the start screen, half for the app, tests and packaging.

### Decided by the owner, 2026-11-25 (asked one by one)

1. **Shell: Electron** (Chromium, the engine the e2e suite uses).
2. **On quit: keep running in the background**; **Quit entirely** in the menu-bar menu stops everything (owner,
   2026-11-25: "have option to keep running the background process when we quit the app. it can be quit entirely
   with menu bar"; this replaces the first answer, stop everything).
3. **Open at login: off**, with an Open at login tick in the menu-bar menu.
4. **Dock icon plus menu-bar item.**
