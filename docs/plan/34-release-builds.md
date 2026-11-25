# helmstudio plan — Release builds on GitHub Actions, and a server pack

[← Plan index](../README.md)

Status: **asked by the owner for the next release (2026-11-25: "the add this is next release through github
actions")**; 0.1.0-alpha.4 items 3 and 4 ([release list](../release/0.1.0-alpha.4.md)).

## 38. Images for every machine, and installs without building

### 38.1 Today

Release images are built on the owner's Mac by `.tools/push-images`: linux/arm64 only, so they run on Apple Silicon
(the Mac app's VM) and nowhere else. Linux and Windows people must build from source (`make up`).

### 38.2 GitHub Actions builds the release images

- A workflow `.github/workflows/release-images.yml`: on a pushed tag `v*`, build the 8 images (kong, auth, studio,
  pipelines, files, web, seaweedfs, media-tools) for **linux/amd64 and linux/arm64**, on GitHub's native runners of
  each kind (ubuntu-24.04 and ubuntu-24.04-arm, free for a public repo), then publish one multi-arch tag per image
  to `ghcr.io/janishar/helmstudio-<name>:<version>` (each machine pulls its own). Sign-in by the workflow's own
  `GITHUB_TOKEN` (packages: write); no personal token.
- On a push to a branch or a pull request it builds the same images **without publishing**, so a change that breaks a
  build shows before a release.
- `.tools/push-images` stays for a release by hand; the release checklist names the workflow first.
- The Mac app needs no change: it pulls the same names, and gets the arm64 half.
- **The Mac app too** (owner, 2026-11-25: "also build mac desktop binary on there only"): a job on a macOS Apple Silicon
  runner builds the .dmg (ad-hoc signed, as today) and a SHA256SUMS covering it and the server pack; on a tag they are
  attached to the release. Releases no longer need the owner's Mac.
- **The pack is checked on every build** (owner, 2026-11-25: "then check at that only every build"): a smoke job on
  each kind of runner loads that run's images (never pulled), runs `curl …/install.sh | sh` against the run's own pack
  (into `~/.helmstudio`) with a throwaway admin,
  signs in through Kong, reads `/v1/studio/sessions` (200), then `uninstall.sh` and removes the volumes. On a tag,
  nothing is published until both pass: then each image is pushed by digest from the run's build cache and merged
  into the multi-arch tag, and the files go to the release (only if the GitHub release already exists; otherwise they
  stay the run's artifact `release-<version>`).
- Later, not now: the contract suite on every push.

### 38.3 The server pack (Linux, and Windows through WSL2)

A release asset `helmstudio-server-<version>.tar.gz`, made by the same workflow:

- `compose.yml`: an overlay on the base stack's own files (shipped in the pack, so there is one copy of them):
  the release images in place of builds (`build: !reset null`, so Compose 2.24 or later), no fakes, ports on
  127.0.0.1, Kong with 2 workers. The settings file (below) names the files and the version.
  `.tools/make-server-pack <version>` assembles it with the stack's tracked files, `.tools/make-secrets` as it is and
  the built-in plugins as the Mac app bundles them.
- `install.sh`: checks Docker, makes the secrets (with `.tools/make-secrets`), asks for the admin email and password
  (or reads `HELMSTUDIO_ADMIN_EMAIL` and `HELMSTUDIO_ADMIN_PASSWORD_FILE`), pulls the images, starts the stack and
  waits until healthy, then prints the address (`http://127.0.0.1:8300`).
- **One command** (owner, 2026-11-25: "not get.sh rather install.sh", "~/.helmstudio"): the same `install.sh` is a
  release asset of its own (in `SHA256SUMS`):
  `curl -fsSL https://github.com/janishar/helmstudio/releases/latest/download/install.sh | sh` (or `| sh -s --
  <version>`). With no pack beside it, it finds the newest release from the public releases feed (prereleases too; no
  token) or takes the version, downloads the pack and `SHA256SUMS`, refuses a checksum mismatch, stops a running
  install, puts the pack in `~/helmstudio` in place of the old one and runs the unpacked `install.sh`. POSIX sh, Linux
  x86_64 and arm64, never sudo (no Docker: it prints how to install it and stops).
- **Two folders** (owner, 2026-11-25: "let this be storage and config ony ~/.helmstudio is download is needed use
  ~/helmstudio"): `~/helmstudio` (`HELMSTUDIO_DIR`) holds the downloaded pack, replaced whole by `update.sh`;
  `~/.helmstudio` (`HELMSTUDIO_HOME`) holds what survives updates: `helmstudio.env` (version, ports, address, the
  admin's email; never the password) and `secrets/` (700; `admin.env`, 600, holds the admin's password, which auth
  needs on every start; `providers/`, the bind-mounted provider keys). The pack links to them (`.env`,
  `deploy/secrets`), so the base compose files and `.tools/make-secrets` run unchanged; `compose.yml` reads
  `admin.env` from `HELMSTUDIO_HOME`. The stack's secret files stay 644 as `.tools/make-secrets` makes them (the
  containers read them as their own users); the 700 folder keeps other users out. Docker volumes hold the data.
- `HELMSTUDIO_PACK_URL` points `install.sh` at another folder (the smoke job's own files). GitHub's `releases/latest`
  URL skips prereleases: while every release is one it does not resolve, so the README also gives
  `releases/download/v<version>/install.sh` until a release is marked latest (owner's call).
- `update.sh [version]`: the newest release or the one given, through `install.sh` (download, check, stop, replace
  `~/helmstudio`, start); settings, secrets and volumes stay. `uninstall.sh` stops it and removes nothing else; it
  says how to remove `~/.helmstudio`, the volumes and the pack.
- A short README in the pack, and a page in the docs (Install on Linux, Install on Windows with WSL2).
- GPU: a Linux or WSL2 machine with an NVIDIA card adds the GPU runner as today (by hand; a setup script later).

### 38.4 Checks

- The workflow runs on a branch push (build only) before it is trusted with a tag.
- The smoke job (§38.2) runs the pack on both kinds of machine on every build; not on the owner's Mac.
- The pack is tried on a clean Linux machine (a GCP spot VM, the owner's rule) from download to first sign-in, with
  the published images, once a tag is out.
- The CI .dmg has no native packs' binaries (`runner_service/vendor/h3c/h3`, `zonos3-server`: built by hand on the
  owner's Mac today); until a job builds them, the native packs do not run from it.
- Windows through WSL2 is documented as untested until someone runs it.
