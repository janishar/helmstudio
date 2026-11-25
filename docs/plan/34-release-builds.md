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
- Later, not now: the .dmg on a macOS runner; the contract suite on every push.

### 38.3 The server pack (Linux, and Windows through WSL2)

A release asset `helmstudio-server-<version>.tar.gz`, made by the same workflow:

- `compose.yml`: the base stack with the release images (no build, no fakes), ports on 127.0.0.1 by default.
- `install.sh`: checks Docker, makes the secrets (as `.tools/make-secrets`), asks for the admin email and password
  (or reads them from the environment), pulls the images, starts the stack and waits until healthy, then prints the
  address (`http://localhost:8300`).
- `update.sh`: switches to a newer release's images and restarts; data volumes stay. `uninstall.sh` stops it and
  says how to remove the data (never removes it by itself).
- A short README in the pack, and a page in the docs (Install on Linux, Install on Windows with WSL2).
- GPU: a Linux or WSL2 machine with an NVIDIA card adds the GPU runner as today (by hand; a setup script later).

### 38.4 Checks

- The workflow runs on a branch push (build only) before it is trusted with a tag.
- The pack is tried on a clean Linux machine (a GCP spot VM, the owner's rule) from download to first sign-in.
- Windows through WSL2 is documented as untested until someone runs it.
