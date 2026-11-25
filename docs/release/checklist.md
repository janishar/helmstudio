# Release checklist

The steps for every helmstudio release, first written for **0.1.0-alpha.1** (the first public alpha, after the owner's
own run of the platform). Tick each box in a copy of this list for the release (an issue or the release PR).

## 0. Decide first (owner)

- [ ] **Version name.** Proposed: `v0.1.0-alpha.1` (the packages are at 0.1.0; the API keeps its own number, 1.59).
- [ ] **What ships.** Source and a tag only; or also public images on GitHub's registry; or also the Mac app (`.dmg`).
- [ ] **Known limits** to state in the notes (draft in step 9).

## 1. Run the platform for real (owner)

- [ ] Use dev or `make prod` for real work: Create (image, video, voice), the Assistant (steer, Stop), Pipelines,
  Gallery, Timeline (edit, export), Models (download, variants, LoRAs), Admin (users, hosts, keys, providers), the MCP.
- [ ] Log every problem; fix the blockers only, the rest go to [docs/backlog.md](../backlog.md).
- [ ] Freeze: from here only release fixes land on `main`.

## 2. Gates (all green, no skips)

- [ ] Each service: `make -C <service> test lint` (auth, studio, files, pipelines, runner, web, cli, kong, desktop).
- [ ] `make contracts-check`.
- [ ] The full contract suite once: `make test` (about 16 minutes, on `helmstudio-test`).
- [ ] The browser journeys: `make e2e`.
- [ ] The runner golden tests on the Mac (Qwen Image, MiniMax, LTX, ZONOS3).

## 3. Clean-room install

- [ ] Fresh clone into a new folder; follow only the README and the install page of the docs site.
- [ ] `make up` from nothing, sign in, add a user, `make runners`; Hosts shows the Mac's runners.
- [ ] One image (Qwen Image Turbo), one short video (LTX distilled), one voice (Qwen3-TTS or ZONOS3), one export.
- [ ] `make prod` on fresh volumes: the same smoke test on port 8400; a restart keeps the data.
- [ ] `helm` CLI: `make -C cli build`, `helm login`, `helm run`, and the MCP connects in Claude.
- [ ] If the Mac app ships: a first open on a Mac account that never ran it (VM, images, secrets, runners, one image).

## 4. Public hygiene and security

- [ ] No secrets anywhere in history (scan for key patterns and committed `.env` or secret files; only test keys).
- [ ] No home paths or personal details in tracked files (`git grep -n '/Users/'` finds none outside vendored code).
- [ ] `.env.example` files hold placeholders only; no default passwords; the admin is made at first start.
- [ ] Dependency audit, high and critical only: `govulncheck` (Go), `uv pip audit` or `pip-audit` (Python),
  `npm audit --omit=dev` (web, desktop). Fix or note each.
- [ ] `LICENSE` (Apache-2.0) and `NOTICE` are current; each vendored folder keeps its own licence.
- [ ] The docs say which model licences the user accepts (gated LTX weights, the MiniMax licence) and that weights are
  not part of the repository.

## 5. Versions

- [ ] Set the version in `web_service/package.json`, `desktop/package.json`, the `pyproject.toml` files and the CLI.
- [ ] `CHANGELOG.md` at the root: the first entry lists what this release can do, then the known limits.
- [ ] Commit, then tag: `git tag -a v0.1.0-alpha.1 -m "helmstudio 0.1.0-alpha.1"` and push the tag.

## 6. Artifacts (as decided in step 0)

- [ ] **The workflow is the release build** (plan 34; `.github/workflows/release-images.yml`): first a draft release
  for the tag (`gh release create v<version> --draft --prerelease --title "helmstudio <version>" --notes-file <notes>`),
  then push the tag. The workflow builds the 9 images for linux/amd64 and linux/arm64 (and `runner-cuda` for
  linux/amd64), smoke-tests the release `compose.yml` on both (first `up` with a throwaway admin, sign-in, one
  authenticated read, a second `up`, `down -v`), and only then publishes one tag per image to
  `ghcr.io/janishar/helmstudio-<name>:<version>` and attaches `helmstudio-<version>.dmg`, `compose.yml` (Windows and
  Linux with Docker: `deploy/release/README.md`) and `SHA256SUMS` to the draft. Without a release the files are the
  run's artifact `release-<version>` (attach them by hand). The workflow sets the version in `desktop/package.json`,
  the image tags in `desktop/stack.json` and the default version in `compose.yml` from the tag.
- [ ] First release from the workflow only: set each new package public (`helmstudio-setup`, `helmstudio-runner-cuda`
  too); a pull with no sign-in works.
- [ ] By hand once per release: Windows with Docker Desktop (WSL2) runs the release `compose.yml` (`up`, sign in, one
  generation on a host); the Mac app's Check for updates. GPU generation (the nvidia profile) was checked once by hand
  on a GCP GPU machine for 0.1.0-alpha.4; not automated.
- [ ] Manual fallbacks only (the workflow failing, or the owner's Mac on purpose): `.tools/push-images <version>`
  (arm64 images only), `make -C desktop test lint dmg`, then `shasum -a 256` of the .dmg and the compose.yml (the
  version set in it) into `SHA256SUMS`.
- [ ] The Mac app is ad-hoc signed, so the notes say right-click › Open on first launch; it pulls the public images
  instead of the local Docker (plan 31 decision 7). The workflow's .dmg has no native packs' binaries (h3,
  zonos3-server) yet: build the .dmg by hand on the owner's Mac when they must ship.
- [ ] Mac app: set `imagesDiskGB` in `desktop/stack.json` to the release images' size on disk (shown on first open).

## 7. README and docs site (every major release)

- [ ] README: pitch, features, the models table, screenshots from the approved boards, quick start, links.
- [ ] Docs site ([helmstudio-docs](https://github.com/janishar/helmstudio-docs), live at https://helmstudio.in):
  install pages match the clean-room run, the API reference is regenerated from `contracts/openapi.yaml`, an
  "alpha" banner, and a release notes page.
- [ ] Every link in both works (a link checker on the built site and the README).

## 8. GitHub release

- [ ] The draft from step 6 has the artifacts and `SHA256SUMS`; publish it: `gh release edit v<version> --draft=false`.
- [ ] The repo's description, topics and website (helmstudio.in) are set; issues are on.

## 9. Release notes (draft)

- What it is, in two lines; what you can do; how to install (link).
- Known limits for the alpha: Apple Silicon Macs and Linux NVIDIA hosts only; the Mac app is a prototype (ad-hoc
  signed, no uninstall, no auto-update); NVIDIA host setup is by hand; an assistant steer is lost to "next message"
  if pipelines restarts mid-answer; model settings in the assistant's knowledge are written by hand.

## 10. After

- [ ] Announce (owner's channels); watch issues for the first days; fixes go to `0.1.0-alpha.2`.
- [ ] Rollback if needed: mark the release as draft, delete the tag, keep the images (tags are immutable).
