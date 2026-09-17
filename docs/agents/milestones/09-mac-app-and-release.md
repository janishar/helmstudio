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

Native shell, daemon handshake and adoption, browser auth and CSRF, signing,
notarisation, the `.dmg`.

(Amended 2026-09-16, M8 Q5, Q17, Q19, Q20. M9 also:
- builds the launcher's Timeline screen, with the Gallery that M6 Q11 moved
  here, once the cookie exists
- makes `POST /timeline/{id}:open` reach a connected launcher page
- decides who owns an export the launcher starts
- bundles the pinned LGPL ffmpeg, checked before `PATH`)

(Amended 2026-09-18, delivery: **the shell is a native Swift bundle around a
`WKWebView`, not Electron.** `docs/decisions.md`, 2026-09-18. Everything this
brief asks for is unchanged except where marked below — R73 kept the shell empty
of business logic, so what moves is the window and nothing behind it. Read
"Electron" as "the shell" wherever it survives in this file.)

## Reads

`docs/design/01-prd.md`, `docs/decisions.md` (delivery).

## Known at freeze

- **The browser at `127.0.0.1:8700` is the reference implementation; the shell
  is a window plus native chrome.** No feature may be reachable only from the
  app.
- The shell spawns the daemon as a sidecar; the handshake is JSON on stdout;
  `~/.helmstudio/daemon.json` is what makes adoption possible after a restart.
- ~~The token is injected via `onBeforeSendHeaders`.~~ **Amended 2026-09-18:
  WebKit has no equivalent** — a `WKWebView` cannot rewrite the headers of an
  ordinary http request, so this mechanism has to be replaced rather than
  translated. The shell reads the nonce from the handshake, loads a one-time
  URL, and the daemon answers `Set-Cookie`, `HttpOnly`, `SameSite=Strict`, then
  redirects to `/`. **The renderer still never sees the token**, and it is the
  same exchange the browser path needs, so M9 ships one auth flow rather than
  two. Why not the alternatives: a `WKURLSchemeHandler` makes the page's origin
  `helm://`, which `internal/api/api.go`'s Origin check would refuse and which
  leaves the studio frame mixed-scheme; a `WKScriptMessageHandler` bridge puts
  request handling in the shell, against R73.
- **Single machine only.** Loopback alone is not sufficient: `Host` and
  `Origin` checks plus a `SameSite=Strict` cookie are what close the browser
  CSRF hole. A page on another site can reach `127.0.0.1` in the user's
  browser; that is the threat being closed.
- **Added 2026-09-18.** Three `WKWebView` settings are part of done, because
  Electron granted the first two by default and the third does not exist there:
  `isElementFullscreenEnabled`, without which a studio's frame cannot go full
  screen even though it carries `allow="fullscreen"` and helmstudio asks on its
  behalf (03 §7a, `web/embed.js`); `isInspectable`, without which the launcher
  cannot be debugged inside the app at all; and `NSAllowsLocalNetworking` in the
  bundle's plist, since App Transport Security decides whether the app may load
  `http://127.0.0.1:8700` in the first place.
- **Added 2026-09-18.** The menu bar item and the Dock's progress (01 R70) read
  the daemon over `URLSession` and SSE, hand-written against `api/openapi.yaml`.
  There is no Swift runtime SDK and the generator gains no fourth target. This
  is the part of the shell that is more work than Electron's would have been.
- **Added 2026-09-18.** Sparkle with an EdDSA-signed appcast serves R72, and 03
  §14's table now names the AppKit call behind each affordance.

## Review focus

- Is there any feature reachable only from the app?
- **(2026-09-18) A cookie is not port-scoped, and every studio serves on a port
  of the same host.** RFC 6265 leaves the port out of a cookie's scope, so a
  session cookie set on `127.0.0.1:8700` rides along on every request to
  `127.0.0.1:<any port>` — which is every studio's own server. A studio could
  read it out of its own inbound requests and then use it from its own process,
  where the Origin check does not apply, because a request with no `Origin` is
  allowed so `curl` works. **Answer this before building the cookie**; it is
  raised in `docs/decisions.md` (2026-09-18) with three directions and none
  chosen. It is not a consequence of the native shell — it lands on the browser
  path identically — but M9 is where the cookie arrives.
- Can the renderer obtain the token by any route — devtools, a preload leak, an
  error message, a log line? (2026-09-18: there is no preload. Confirm there is
  no `WKScriptMessageHandler` and no `WKURLSchemeHandler` registered either, so
  that there is no bridge to leak through.)
- Are `Host` and `Origin` both checked, and what is the behaviour on a missing
  `Origin`?
- What happens when the app is killed and the daemon survives, and when the
  daemon dies and the app survives?
- Does anything bundled in the `.dmg` conflict with the ffmpeg licensing
  decision closed in M8?
