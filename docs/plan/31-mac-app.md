# helmstudio-next plan — The Mac app: a standalone prototype

[← Plan index](../README.md)

Status: **decided by the owner (2026-11-25, decisions below); board [Desktop](../mock/Desktop.dc.html) approved by the
owner (2026-11-25, "board approved"). A fast prototype: essential tests
only, robust testing later.** Not in a milestone, like plans 25–30. It narrows [09 §12](09-scope-and-risks.md) ("a
desktop app shell and a one-click installer … deliberately not in v1"): a Mac app that installs everything is now in,
as a prototype.

The owner's asks, 2026-11-25: "now plan a mac desktop app quicker path"; then the VM route, installed all together;
then "it should be simpler. take a vm that can run on mac, install the docker images from the repositories already
there, run the web app inside chromium. why we need so much rewriting that already available for us to use"; then
"more quicker smaller tests only essential less regression. fast prototype for this later robust testing". So the
prototype reuses what exists (Colima, Docker images, compose, `.tools/make-secrets`, uv and our lockfiles) and adds only
the app around them. The repo-based launcher (the first draft) is skipped: the app goes straight to a standalone
install.

## 35. The Mac app

### 35.1 The parts (all existing, wrapped)

1. **VM: Colima inside the app** (MIT; Lima underneath, Apple's virtualization). Its binaries ship in the app;
   `COLIMA_HOME` (and `LIMA_HOME`) point at `~/.helmstudio-vm` (as built, 35.8: Lima's socket paths are too long
   under Application Support; the rest is in `~/Library/Application Support/helmstudio/`), so the VM, its Docker socket
   and its disks belong to the app only: nothing in `/usr/local/bin`, the user's Docker or OrbStack untouched.
   `colima start` fetches its own Linux image and Docker; we build no VM image. Memory about 2 GB.
2. **Images: pulled, not built.** Postgres and NATS from Docker Hub; ours (web, studio, pipelines, auth, files, kong,
   seaweedfs) pushed once per release to GitHub's registry by one script (`.tools/push-images`, arm64, tagged with
   the app version). The app carries one compose file naming those images: `deploy/docker-compose.yml` with image
   tags, no test-only overlays (no fake runner, no fake models).
3. **Window: Electron** (Chromium) on the stack's Kong (35.3).
4. **Secrets:** the app runs `.tools/make-secrets` (shipped inside) into its own folder on first open.
5. **Runners: native, as today** (they need the Mac's GPU). The app ships the runner code, its lockfiles, the built
   `h3` and `zonos3` and `uv`; first open runs `uv sync` per env (torch, mlx; about 2 GB); the app starts and stops
   them the way `make runners` does. The user picks the models folder (default `~/helmstudio-models`).

Ports: the app's stack uses its own host ports (Kong 8310, NATS, S3, runner health 8191–8193), so it can run beside the
dev stack (8300) on the owner's Mac. All on 127.0.0.1.

### 35.2 First open, then every open

First open (one screen, a step list with progress; a failed step says so in plain words with **Show log** and **Try
again**, and Try again resumes from that step):

1. Check the Mac: Apple Silicon, macOS version, about 6 GB free.
2. Start the VM (Colima's first start downloads its Linux image).
3. Pull the images (about 2 GB).
4. Make the secrets; ask for the admin email and password.
5. Set up the runners (`uv sync`, about 2 GB) and pick the models folder.
6. Start the stack and the runners; show the studio.

Every later open: start the VM if it is stopped, `compose up -d --wait`, start the runners, show the studio; the
same step screen while it waits.

A menu-bar item shows one status dot (ready / starting / something down) and a menu: **Open**, **Restart runners**,
**Open logs**, **Keep running after quit** (a tick, on), **Open at login** (a tick, off), **Quit entirely**.

Quitting the app (Cmd+Q, the Dock's Quit, or closing the window) keeps the background running: the stack and the
runners stay up, the window and Dock icon go, the menu-bar item stays, so opening again is instant. **Quit entirely**
stops the runners, the stack (`compose down`, data stays) and the VM, then the app.

### 35.3 The window

- One `BrowserWindow` on the app's Kong (`http://127.0.0.1:8310`), its own persistent session.
- No Node in the page: `contextIsolation` on, `nodeIntegration` off, no preload API; the page is the web app as is.
- It never leaves the studio: other origins and `target=_blank` links open in the default browser.
- Downloads use the standard save dialog. Size and position kept. `Cmd+R` reloads; the usual Edit menu.
- Icon: logo A from the [Logo](../mock/Logo.dc.html) board, as `.icns`.

### 35.4 Code and essential checks only

- A new top-level folder `desktop/`, isolated like a service (CLAUDE.md): its own `package.json`, lockfile,
  `Makefile`, `.env.example`; no Dockerfile. It copies what it ships (compose file, make-secrets, runner code and
  lockfiles) at build time; it never imports another service's code.
- Electron, its packager and Colima are pinned in [14-dependencies](14-dependencies.md) at build time.
- **Essential checks (prototype):** `make -C desktop lint`, a few unit tests of the step runner (commands faked), and
  one manual end-to-end install on this Mac (first open to the studio showing, a take generated, quit and reopen,
  Quit entirely). Nothing else; the repo's other gates run only if a change touches another service (the shipped
  compose file and the image push script should not).
- **Later, robust testing:** a clean-Mac install in a fresh macOS VM, update and uninstall paths, an Electron smoke
  test in CI, failure injection per step.
- `make -C desktop app` builds `helmstudio.app` (ad-hoc signed, with the virtualization entitlement Colima needs);
  `make -C desktop dmg` packs it.

### 35.5 Board first

A Desktop board on the canvas before code: the first-open screen (each step, a failed step, the admin account and
models folder asks), the every-open start screen, and the menu-bar menu in each status. The owner approves, then the
build.

### 35.6 Cost and size

About a week: a day for the board, three for the app and first-open steps, a day for the image push and packaging,
a day for the manual install run and fixes. Download about 150 MB; after setup about 4–5 GB without models.

### 35.7 Not in the prototype

Auto-update (a new version is a new download; the data stays in the app's Colima disk), backup and export, an
uninstall tool, a Homebrew tap or install command, notarization (Developer ID), Intel Macs, Windows and Linux.

### 35.8 As built: the install run on this Mac (2026-11-25)

Stage 1 (commit 19c1cd5) built `desktop/`; stage 2 wired it and ran a real install of `/Applications/helmstudio.app`
on the owner's Mac, driven headless by Playwright's `_electron` (the app's test hook `HELMSTUDIO_TEST=1` exposes the
menu-bar menu and the state), with a test admin and the models folder `~/GenAI/models`.

**Wiring.** No app overlay file was needed: everything goes through `stack.json`'s `composeEnv` on base +
`compose/prod.yml`: ports Kong 8310, Postgres 5466, NATS 4325, S3 8436/8446 (all 127.0.0.1, as the base file
publishes them), `HELM_NET=172.30.100` (dev 97, test 98, prod 99), `HELM_SECRETS=secrets` inside the app's own copy
of `deploy/`, `PUBLIC_BASE_URL=http://127.0.0.1:8310`, `COMPOSE_PROFILES=s3local`. The image list equals
`docker compose … config --images` of base + prod.yml. Memory measured in the VM with the stack up and one take made:
containers about 750 MiB, the VM 870 MiB used of 2 GB with 1.1 GB available, so the VM stays at 2 GB and prod.yml's
limits are kept as they are.

**What broke, and the fix:**

1. *VM step:* Lima refused the instance: its socket path under `~/Library/Application Support/helmstudio/vm` was 110
   characters (limit 104). The VM now lives in `~/.helmstudio-vm` (`COLIMA_HOME`; not `~/.helmstudio`, which the
   reference app already uses). The rest of the app's data stays in Application Support.
2. *Start step:* SeaweedFS did not start (profile `s3local` comes from the git-ignored `deploy/.env`), so the
   runners could not reach S3 and never answered on their health ports. `COMPOSE_PROFILES=s3local` in `composeEnv`;
   Try again then resumed at Start and finished.
3. *Shipped files:* the one-shots' scripts `.tools/nats-setup` and `.tools/storage-setup` were not copied into the
   app; `make resources` copies them now.
4. *Runner envs:* `uv sync` picked Homebrew's Python 3.14, which a `brew upgrade` would break; `--managed-python`
   puts uv's own Python in the app's folder.
5. *Restart runners:* the status dot went to "Something is down" and back to Ready before the runners answered; the
   periodic check now waits while the runners restart.

**Proven:** first open from nothing to the studio window on 127.0.0.1:8310; resume (Try again on a failed step,
and a relaunch after the app was killed mid-step, both resumed at the failed step); sign-in as the test admin;
Admin › Hosts showing `mac` with envs torch, mlx and native; one Qwen Image Turbo take (512×512, 8 steps, 26 s
including the model load) shown in the gallery; closing the window and Cmd+Q keep the stack and runners up and
reopening is instant (the window shows in under 5 ms); Quit entirely stops the runners, `compose down` and the VM
(about 25 s); the next open shows the Starting… screen and keeps the data (the take was still there); the menu-bar
items Open, Restart runners, Open logs, Keep running after quit and Open at login (both toggled and back).

**Times (first open, warm caches):** check under 0.1 s; VM 20 s (the very first start adds about 2.7 min to download
Colima's 317 MB disk image, cached after in `~/Library/Caches/colima`); images 20 s (2.2 GB); secrets and admin 2 s
plus typing; runners 15 s (uv's cache on this Mac was warm: a Mac without it downloads about 2 GB); Start 42 s
(compose up 23 s, runners ready about 18 s). About 1 min 40 s in all. Every later open: VM 30 s, services 24 s,
runners 6 s, about 1 min.

**Sizes:** the app 484 MB; the VM folder 3.3 GB (data disk with the images 2.2 GB, system disk 1.1 GB, disk
limit 60 GB); Application Support 1.8 GB (runner venv 838 MB, mlx venv 833 MB, Python 71 MB, runner code 67 MB).

**Known limits (prototype):** images come only from this Mac's Docker (`make prod` first); a user name longer than
about 30 characters would again overflow Lima's socket path; Open logs opens Finder; the menu-bar items were clicked
through the test hook, not with a real mouse; deleting the app leaves `~/.helmstudio-vm` and its Application Support
folder (no uninstall tool, 35.7).

### Decided by the owner, 2026-11-25 (asked one by one)

1. **Shell: Electron** (Chromium, the engine the e2e suite uses).
2. **On quit: keep running in the background**; **Quit entirely** in the menu-bar menu stops everything (owner,
   2026-11-25: "have option to keep running the background process when we quit the app. it can be quit entirely
   with menu bar"; this replaces the first answer, stop everything).
3. **Open at login: off**, with an Open at login tick in the menu-bar menu.
4. **Dock icon plus menu-bar item.**
5. **The VM route, all installed together on first open**, with existing parts only (Colima, pulled images, compose,
   make-secrets, uv); no own VM image; distributed without the App Store and without a Developer ID (ad-hoc signed).
6. **A fast prototype:** essential checks only; robust testing later.
7. **Registry: private for now, public at the public release** (owner, 2026-11-25: "private for now but when i
   release to public i will need public"). The app tries an anonymous pull first and asks for a read-only GitHub
   token only when the images are private, so making them public later needs no app change.
8. **The board as drawn**, its open points settled as drawn: the admin account is asked in the step list; Choose…
   may pick an existing folder; the start screen shows on every open; the menu-bar icon is logo A in one colour.
9. **Image access step (3b) approved** (owner, 2026-11-25, "yes approved"): shown only when the anonymous pull is
   refused; a read-only GitHub token (read:packages), kept only on this Mac.
10. **The MCP works with the app:** `helm` (cli/) ships inside the app at `Contents/Resources/helm` (as built: `Contents/Resources/bin/helm`); the user runs
    `helm login --url http://127.0.0.1:8310 …` once and `claude mcp add -s user helmstudio -- <app>/Contents/Resources/helm mcp`;
    docs/guides/mcp.md gets these lines. No screen change. `helm` keeps one address, so it points at one stack at a time.
11. **No upload for the prototype** (owner, 2026-11-25: "no upload for now, later public release"; private GitHub
    packages beyond the free 500 MB would be refused or billed, and the owner pays nothing extra). On this Mac the app
    loads the images straight from the local Docker into its VM (`docker save` | the VM's `docker load`), tagged
    prod from `make prod`'s build. The registry push (35.1 item 2) waits for the public release, where public
    packages are free. Step 3b (image access) stays on the board but is not built.
