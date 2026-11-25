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

### 38.3 Docker for Windows and Linux; the Mac app for Mac (owner, 2026-11-25)

The owner: "or we ship the docker images and a docker file, the end user will install docker desktop and run the
docker file?", then "for mac wwe already have the app we dont need docker desktop", then "yes do this way". So:

- **Mac:** the Mac app (.dmg) as today: its own VM, the Apple GPU through the native runners; no Docker Desktop.
- **Windows and Linux:** Docker (Docker Desktop on Windows, with WSL2; Docker Engine or Desktop on Linux) and one
  `compose.yml` attached to each release: `docker compose up -d`, then http://localhost:8300. With an NVIDIA card:
  `docker compose --profile nvidia up -d` adds the GPU runner (NVIDIA Container Toolkit on Linux; Docker Desktop on
  WSL2 passes the card through on Windows).
- **No install scripts:** a first-start container makes the secrets into a Docker volume once (the same values as
  `.tools/make-secrets`), and an init step creates the admin from `HELMSTUDIO_ADMIN_EMAIL` and
  `HELMSTUDIO_ADMIN_PASSWORD` given on the first `up` (or a `.env` beside the file), then never again. Ports on
  127.0.0.1. Data and keys live in named volumes; an update is a new version in the file, `docker compose pull`,
  `docker compose up -d`.
- **From source** stays for everyone (`make up`), as today.
- Install pages on the docs site: Mac (the app), Windows (Docker Desktop), Linux (Docker), From source.

### 38.4 Checks

Owner, 2026-11-25: "then check at that only every build". On every push, pull request and tag, a smoke test on GitHub's
Intel/AMD and ARM Linux runners runs the release `compose.yml` with that run's images: first `up`, healthy, sign in as
the admin, read one authenticated route, `down -v`; a tag publishes only after both pass. GitHub's Windows and Mac
machines cannot run the Linux VM, so Windows (Docker Desktop) and the Mac app are checked by hand once per release
(the owner's Check for updates on the Mac). Not covered: GPU generation on an NVIDIA machine.
