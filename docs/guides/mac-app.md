# helmstudio for Mac

[← Docs index](../README.md) · design: [plan 31](../plan/31-mac-app.md)

The app runs the whole studio on this Mac: the services in a small VM of their own, the runners on the Mac's GPU, and
the studio in one window. It runs beside the dev and prod stacks (its own ports: the studio is on
`http://127.0.0.1:8310`).

## Before the first open

- A Mac with Apple Silicon, macOS 13 or later, about 6 GB of free disk.
- The prod images in this Mac's Docker (OrbStack): run `make prod` once. The app copies them into its VM; nothing is
  downloaded from a registry.
- The internet, for the first open only (the VM's system disk, about 320 MB, and the runners' Python packages, about
  2 GB).

## First open

1. Open `/Applications/helmstudio.app`. It is ad-hoc signed: a copy from another Mac needs right-click › **Open** the first time.
2. A step list runs: Check this Mac, Start the VM, Load the images, Make the secrets, Set up the runners, Start.
   It takes about **2 minutes** here (about 5 on a Mac that has never downloaded the VM disk or the runner packages).
3. **Admin account:** the step list asks for an email and a password (12 characters or more, no quote). That is
   the studio's sign-in. It is kept only in the app's folder.
4. **Models folder:** it asks where the models live (default `~/helmstudio-models`; Choose… may pick an existing
   folder, such as `~/GenAI/models`).
5. The studio window opens. Sign in with the admin account.

If a step fails, the screen says which; **Show log** opens the log, **Try again** goes on from that step.

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
- **Quit**: stops the runners, the services and the VM (about 25 s). Your takes and data stay; the next open
  starts it all again.

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
