# helmstudio plan — Release builds on GitHub Actions, and Docker for Windows and Linux

[← Plan index](../README.md)

Status: **asked by the owner for the next release (2026-11-25: "the add this is next release through github
actions")**; 0.1.0-alpha.4 items 3 and 4 ([release list](../release/0.1.0-alpha.4.md)).

## 38. Images for every machine, and installs without building

### 38.1 Today

Release images are built on the owner's Mac by `.tools/push-images`: linux/arm64 only, so they run on Apple Silicon
(the Mac app's VM) and nowhere else. Linux and Windows people must build from source (`make up`).

### 38.2 GitHub Actions builds the release images

- A workflow `.github/workflows/release-images.yml`: on a pushed tag `v*`, build the 9 images (kong, auth, studio,
  pipelines, files, web, seaweedfs, media-tools, setup) for **linux/amd64 and linux/arm64**, on GitHub's native runners of
  each kind (ubuntu-24.04 and ubuntu-24.04-arm, free for a public repo), then publish one multi-arch tag per image
  to `ghcr.io/janishar/helmstudio-<name>:<version>` (each machine pulls its own). Sign-in by the workflow's own
  `GITHUB_TOKEN` (packages: write); no personal token.
- **Only on a tag, only from main** (owner, 2026-11-25: "i dont want this build-and-smoke-test, only on tag push on
  main only"; "release only from main"): no run on branch pushes or pull requests. The first job checks the tagged
  commit is on main (`git merge-base --is-ancestor "$GITHUB_SHA" origin/main`); if not, every other job is skipped
  (owner: "can we not rather fail one job"), so nothing is built or published. The order of a release: merge
  the alpha branch into main, push main, delete the merged branches, then tag the commit on main; the workflow runs
  only for that tag. (v0.1.0-alpha.4 was the last tag built from a branch.)
- `.tools/push-images` stays for a release by hand; the release checklist names the workflow first.
- The Mac app needs no change: it pulls the same names, and gets the arm64 half.
- **The Mac app too** (owner, 2026-11-25: "also build mac desktop binary on there only"): a job on a macOS Apple Silicon
  runner builds the .dmg (ad-hoc signed, as today) and a SHA256SUMS covering it and compose.yml; on a tag they are
  attached to the release. Releases no longer need the owner's Mac.
- **Checked on every build** (owner, 2026-11-25: "then check at that only every build"): 38.4. On a tag, nothing is
  published until both smoke tests pass: then each image is pushed by digest from the run's build cache and merged
  into the multi-arch tag, and the files go to the release (only if the GitHub release already exists; otherwise they
  stay the run's artifact `release-<version>`).
- Later, not now: the contract suite on every push.

### 38.3 Docker for Windows and Linux; the Mac app for Mac (owner, 2026-11-25)

The owner: "or we ship the docker images and a docker file, the end user will install docker desktop and run the
docker file?", then "for mac wwe already have the app we dont need docker desktop", then "yes do this way". So:

- **Mac:** the Mac app (.dmg) as today: its own VM, the Apple GPU through the native runners; no Docker Desktop.
- **Windows and Linux:** Docker (Docker Desktop on Windows, with WSL2; Docker Engine or Desktop on Linux) and one
  `compose.yml` attached to each release: `docker compose up -d`, then http://localhost:8300. With an NVIDIA card:
  no CUDA image ships (owner, 2026-11-25); the runners run on the host from source on its installed driver
  (`make up GPU_HOSTS=<h>` and `make runners-nvidia`, docs/guides/nvidia-linux.md).
- **No install scripts:** a first-start container makes the secrets into a Docker volume once (the same values as
  `.tools/make-secrets`), and an init step creates the admin from `HELMSTUDIO_ADMIN_EMAIL` and
  `HELMSTUDIO_ADMIN_PASSWORD` given on the first `up` (or a `.env` beside the file), then never again. Ports on
  127.0.0.1. Data and keys live in named volumes; an update is a new version in the file, `docker compose pull`,
  `docker compose up -d`.
- **From source** stays for everyone (`make up`), as today.
- Install pages on the docs site: Mac (the app), Windows (Docker Desktop), Linux (Docker), From source.

### 38.4 Checks

Owner, 2026-11-25: "then check at that only every build", then "only on tag push on main only". On every release
tag (a commit on main), a smoke test on GitHub's
Intel/AMD and ARM Linux runners runs the release `compose.yml` with that run's images: first `up`, healthy, sign in as
the admin, read one authenticated route, `down -v`; a tag publishes only after both pass. GitHub's Windows and Mac
machines cannot run the Linux VM, so Windows (Docker Desktop) and the Mac app are checked by hand once per release
(the owner's Check for updates on the Mac). GPU generation (host runners, make runners-nvidia) is checked by hand on
the GCP GPU machine; not automated.

### 38.5 Built (branch alpha4-release-builds)

- `deploy/release/compose.yml` (README.md beside it): the release images, no build, no fakes, only Kong published
  (127.0.0.1:8300), `KONG_NGINX_WORKER_PROCESSES=2`, the prod limits and restarts. Volumes `keys` (secrets), `config`,
  `pgdata`, `natsdata`, `seaweeddata`, `models`, `runner-cache`.
- `secrets` (image `helmstudio-setup`, `deploy/release/setup.Dockerfile`): runs `.tools/make-secrets` (GPU host `gpu`)
  into `keys` on every `up` (it never overwrites), saves the admin's email and password on the first start only, gives
  each service only its own files at `/run/secrets` (volume subpaths: Compose 2.26 / Docker 26 or later) and copies
  this version's nats.conf, postgres init.sh, the one-shots' scripts and the built-in plugins into `config`.
- `admin`: `auth create-admin` from those files; on later starts the account exists and nothing changes (auth keeps
  them too: without ADMIN_EMAIL it would demote every admin).
- NVIDIA (owner, 2026-11-25): the `nvidia` profile and the `runner-cuda` job are gone; no CUDA image ships. Host
  runners from source instead (`make runners-nvidia`, docs/guides/nvidia-linux.md); SGLang is found, not shipped
  (SGLANG_BIN, else `sglang` on PATH).
- The workflow: the `compose` job bakes the version in; `smoke` on both runners; the release gets the .dmg, compose.yml and SHA256SUMS. The server pack and its scripts
  (dc20b878) are gone.
