# M8 — Timeline and export

**Effort:** 10–12 days. **Machine-bound demo:** most of it.

> **Scoped, not specified.** This milestone is expanded to M0-level detail at
> its own kickoff, when the two things a good brief needs both exist: the
> contract it builds against, and the previous milestone's review findings.
> Expanding it now would mean guessing, and a guessed brief is worse than none
> because it reads as authority. Expand it with the stronger model, and carry
> forward the review focus that earlier milestones actually produced.

## Shape

The timeline document, the editor, the export pipeline, and golden tests.

Its shape depends on how the media engine behaves in practice after M4.

## Reads

`docs/design/07-platform-services.md`, `docs/decisions.md` (timeline).

## Known at freeze

- **The framework owns the document and the export pipeline; `helm-ui-sdk`
  ships the editor.** A sequence made from four studios is nobody's studio
  document and must survive uninstalling half of it.
- **Conform to a declared target, with a stream-copy fast path when legal.** A
  run of takes from one studio is the common case and should cost seconds, not
  a re-encode.
- ffmpeg licensing is an **open decision** — LGPL + videotoolbox (leaning)
  versus GPL + x264. It gates this milestone and what can be bundled in a
  signed `.dmg`. It is the user's to close, not the implementer's.
- h3's `CombineTimeline` always re-encodes. That is the behaviour being
  replaced, and it is why the fast path exists.

## Review focus

- **Is stream-copy taken only when genuinely legal?** Codec, profile, level,
  timebase, pixel format, colour metadata. A fast path taken illegally produces
  a file that plays on the developer's machine and nowhere else.
- **Do the golden tests compare frame hashes, or only that a file appeared?**
- Does the document survive a studio being uninstalled?
- What happens to an export interrupted halfway — a partial file left where a
  finished one is expected?
