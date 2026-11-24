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

- [ ] **Images:** build with the release tag and push to `ghcr.io/janishar/…` (plan 31 §35.1: one script, arm64; the
  public registry is free); set each package public; a pull with no sign-in works.
- [ ] **Mac app:** `make -C desktop test lint dmg`; it is ad-hoc signed, so the notes say right-click › Open on first
  launch; it pulls the public images instead of the local Docker (plan 31 decision 7).
- [ ] `shasum -a 256` of every file attached to the release, in a `SHA256SUMS` file.

## 7. README and docs site (every major release)

- [ ] README: pitch, features, the models table, screenshots from the approved boards, quick start, links.
- [ ] Docs site ([helmstudio-docs](https://github.com/janishar/helmstudio-docs), live at https://helmstudio.in):
  install pages match the clean-room run, the API reference is regenerated from `contracts/openapi.yaml`, an
  "alpha" banner, and a release notes page.
- [ ] Every link in both works (a link checker on the built site and the README).

## 8. GitHub release

- [ ] `gh release create v0.1.0-alpha.1 --prerelease --title "helmstudio 0.1.0-alpha.1" --notes-file <notes>` with
  the artifacts and `SHA256SUMS` attached.
- [ ] The repo's description, topics and website (helmstudio.in) are set; issues are on.

## 9. Release notes (draft)

- What it is, in two lines; what you can do; how to install (link).
- Known limits for the alpha: Apple Silicon Macs and Linux NVIDIA hosts only; the Mac app is a prototype (ad-hoc
  signed, no uninstall, no auto-update); NVIDIA host setup is by hand; an assistant steer is lost to "next message"
  if pipelines restarts mid-answer; model settings in the assistant's knowledge are written by hand.

## 10. After

- [ ] Announce (owner's channels); watch issues for the first days; fixes go to `0.1.0-alpha.2`.
- [ ] Rollback if needed: mark the release as draft, delete the tag, keep the images (tags are immutable).
