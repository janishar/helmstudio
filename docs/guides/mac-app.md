# helmstudio for Mac

[← Docs index](../README.md) · design: [plan 31](../plan/31-mac-app.md)

The app runs the whole studio on this Mac: the services in a small VM of their own, the runners on the Mac's GPU, and
the studio in one window. It runs beside the dev and prod stacks (its own ports: the studio is on
`http://127.0.0.1:8310`).

## Install

1. Download `helmstudio.dmg` from the [GitHub releases](https://github.com/janishar/helmstudio/releases) (check it
   against the release's `SHA256SUMS` if you like: `shasum -a 256 helmstudio.dmg`).
2. Open it and drag helmstudio to Applications.
3. The app is ad-hoc signed (no Developer ID): the first time, right-click it › **Open**, then **Open** again.

Needs a Mac with Apple Silicon, macOS 13 or later and about 6 GB of free disk. **No Docker or OrbStack:** the app
ships its own VM and docker tools. The internet is needed on the first open only: the VM's system disk (about
320 MB), the images (pulled from GitHub's registry and Docker Hub, about 2.2 GB once unpacked) and the runners'
Python packages (about 2 GB).

## First open

1. Open helmstudio from Applications.
2. A step list runs: Check this Mac, Start the VM, Load the images, Make the secrets, Set up the runners, Start.
   It takes about **5 minutes** on a Mac that has never downloaded the VM disk, the images or the runner packages.
3. **Admin account:** the step list asks for an email and a password (12 characters or more, no quote). That is
   the studio's sign-in. It is kept only in the app's folder.
4. **Models folder:** it asks where the models live (default `~/helmstudio-models`; Choose… may pick an existing
   folder, such as `~/GenAI/models`).
5. The studio window opens. Sign in with the admin account.

If a step fails, the screen says which; **Show log** opens the log, **Try again** goes on from that step.

## Build it yourself (developers)

`make -C desktop app` builds `desktop/dist.noindex/helmstudio.app` (`make -C desktop dmg` the `.dmg`). That build
pulls the released images named in `desktop/stack.json` (tagged with the app version). To try unreleased images, run
`make prod` once and start the app from a terminal with the switch, on every open
(`HELMSTUDIO_LOCAL_IMAGES=1 desktop/dist.noindex/helmstudio.app/Contents/MacOS/helmstudio`): it then copies
`helmstudio/<svc>:prod` from this Mac's Docker into its VM (`docker save` | `docker load`) and runs those. Releases
push the images with `.tools/push-images <version>`.

## Every later open

The start screen shows for about **1 minute** (VM, services, runners), then the studio window.

## Remote models

Remote models are on in the app: Admin › Model providers offers DeepSeek, OpenAI, Anthropic and the rest. Paste the
key in the provider form (or put the key file in `~/Library/Application Support/helmstudio/stack/deploy/secrets/providers/`).

## The menu bar

The helmstudio icon in the menu bar is tinted by macOS like every other icon there (white on a dark menu bar, in light mode too); the dot beside it: green Ready, yellow Starting…, red Something is down. Its menu:

- **Open**: the studio window.
- **Restart runners**: after a runner stalls or a new model is added.
- **Open logs**: the app's log and one log per runner.
- **Keep running** (on): closing the window or Cmd+Q only hides the app; the studio keeps running and
  reopens at once.
- **Open at login** (off).
- **Check for updates…**: see Updates below.
- **Quit**: stops the runners, the services and the VM (about 25 s). Your takes and data stay; the next open
  starts it all again.

## Updates

The app checks GitHub's releases at start and every 24 h (pre-releases too while the app is an alpha). When a newer
release with a `.dmg` and `SHA256SUMS` is out, a small window says so, the menu shows **Update to <version>** with a
blue dot, and the menu-bar icon gets a small blue dot. **Check for updates…** in the menu checks now.

- Nothing is downloaded until you press **Install**. The `.dmg` goes to the app's folder (`updates/`); **Cancel**
  stops it and deletes the part.
- The download is checked against the release's `SHA256SUMS`; a mismatch deletes it and nothing changes.
- **Restart** stops everything the way Quit does, then a small script waits for the app to exit, copies the new
  `helmstudio.app` out of the `.dmg` beside the old one, swaps the two and opens the app again (its log:
  `logs/update.log`). If anything fails, the old app stays. The app must be somewhere it can change, such as
  Applications: run from the `.dmg` itself, it says so and installs nothing.
- The first open of the new version shows **Updating…**: VM, Update the images, Services, Runners (the new runner
  code and packages), then **Remove old images** once it all runs (our images of older versions, and `helmstudio/*`
  ones from a developer build; never third-party images). If a step fails, the old images stay and the next open
  tries again. Your data, models and settings stay.

Developers can test it against a local fake: `HELMSTUDIO_UPDATE_URL=http://127.0.0.1:<port>/releases.json` points the
check at a file shaped like GitHub's releases list; `HELMSTUDIO_HIDE=1` keeps the windows hidden for a driven run
(with `HELMSTUDIO_FAKE=1` and `HELMSTUDIO_HOME` on a scratch folder, nothing touches a real install).

## Where things are

- `~/Library/Application Support/helmstudio`: settings, logs, secrets, the runners' Python.
- `~/.helmstudio-vm`: the VM (its disks hold the images and the data).
- Deleting the app leaves both folders; delete them by hand (after Quit) to remove everything.

## Use it from Claude (MCP)

`helm` ships inside the app. Sign in once (it asks for the admin password), then add it to Claude Code:

```sh
/Applications/helmstudio.app/Contents/Resources/bin/helm login --url http://127.0.0.1:8310 --email <admin email> \
  --api-key-file ~/Library/Application\ Support/helmstudio/stack/deploy/secrets/helm_api_key
claude mcp add -s user helmstudio -- /Applications/helmstudio.app/Contents/Resources/bin/helm mcp
```

`helm` keeps one address, so it points at one stack at a time: logging in to the app replaces a dev-stack login.
More in [the MCP guide](mcp.md).
