# M9 — Mac app and release

**Effort:** 7–9 days. **Machine-bound demo:** all of it.

> **Scoped, not specified.** This milestone is expanded to M0-level detail at
> its own kickoff, when the two things a good brief needs both exist: the
> contract it builds against, and the previous milestone's review findings.
> Expanding it now would mean guessing, and a guessed brief is worse than none
> because it reads as authority. Expand it with the stronger model, and carry
> forward the review focus that earlier milestones actually produced.

**Taken after M10, 2026-09-16, by the human's decision.** Its brief is
unchanged; the reason and the costs are in `docs/decisions.md`, under M10.

## Shape

Electron shell, daemon handshake and adoption, browser auth and CSRF, signing,
notarisation, the `.dmg`.

(Amended 2026-09-16, M8 Q5, Q17, Q19, Q20. M9 also:
- builds the launcher's Timeline screen, with the Gallery that M6 Q11 moved
  here, once the cookie exists
- makes `POST /timeline/{id}:open` reach a connected launcher page
- decides who owns an export the launcher starts
- bundles the pinned LGPL ffmpeg, checked before `PATH`)

## Reads

`docs/design/01-prd.md`, `docs/decisions.md` (delivery).

## Known at freeze

- **The browser at `127.0.0.1:8700` is the reference implementation; Electron
  is a window plus native chrome.** No feature may be reachable only from the
  app.
- Electron spawns the daemon as a sidecar; the handshake is JSON on stdout;
  `~/.helmstudio/daemon.json` is what makes adoption possible after a restart.
- The token is injected via `onBeforeSendHeaders`. **The renderer never sees
  it.**
- **Single machine only.** Loopback alone is not sufficient: `Host` and
  `Origin` checks plus a `SameSite=Strict` cookie are what close the browser
  CSRF hole. A page on another site can reach `127.0.0.1` in the user's
  browser; that is the threat being closed.

## Review focus

- Is there any feature reachable only from the app?
- Can the renderer obtain the token by any route — devtools, a preload leak, an
  error message, a log line?
- Are `Host` and `Origin` both checked, and what is the behaviour on a missing
  `Origin`?
- What happens when the app is killed and the daemon survives, and when the
  daemon dies and the app survives?
- Does anything bundled in the `.dmg` conflict with the ffmpeg licensing
  decision closed in M8?
