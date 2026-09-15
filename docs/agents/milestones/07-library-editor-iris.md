# M7 — Library, editor and iris

**Effort:** 6–8 days. **Machine-bound demo:** wrap a repository that ships no manifest.

> **Scoped, not specified.** This milestone is expanded to M0-level detail at
> its own kickoff, when the two things a good brief needs both exist: the
> contract it builds against, and the previous milestone's review findings.
> Expanding it now would mean guessing, and a guessed brief is worse than none
> because it reads as authority. Expand it with the stronger model, and carry
> forward the review focus that earlier milestones actually produced.

## Shape

Selectable weights; the three-source manifest library; the in-app manifest
editor; manifest import and export; the approval screen for a studio a user
added themselves.

## Reads

`docs/design/05-sdk-and-custom-studios.md`, `schema/manifest.json`,
`docs/decisions.md` (manifest).

## Known at freeze

- The manifest lives in the studio's **own repo** as `helmstudio.yaml`; the
  registry holds pointers. Two copies drift; one does not.
- **Three resolution sources, first match wins:** local file → in-repo
  `helmstudio.yaml` → registry inline pointer. Most repositories will never
  ship a manifest, so anyone must be able to write one. A manifest is a
  description of a repository, not a file only its author can provide.
- The library is a thing a user browses, chooses from and adds to — not a
  config file.
- The approval screen is the last point before a user's machine runs someone
  else's build steps. What it shows is a security decision, not a layout one.

## Review focus

- Does first-match-wins actually short-circuit, and is the chosen source
  visible to the user?
- Does the editor produce manifests that `helm validate` accepts — the same
  validator, not a second implementation?
- Can an imported manifest reference a path outside the studio root?
- Does the approval screen show the build commands that will run, verbatim?
