# CLAUDE.md

Orientation for anyone — or anything — working in this repository.

## Read first

- `docs/design/` is the **frozen design and the contract**. An implementation that disagrees with it is wrong by definition until the document changes.
- `docs/decisions.md` is what was decided, when, and why, including what was left open.
- `CONTRIBUTING.md` is how work is done here: commit style, branches, the gate.
- `docs/plan/` is the build order and the reasoning behind it.
- `docs/agents/` is how a milestone is actually run: the two briefs, the gate,
  the report template, and one document per milestone. **If you were pointed at
  a milestone, read `docs/agents/implementer.md` first.**

## The rule that matters most

**If a design document is inconsistent, ambiguous or wrong: raise it and stop.** Do not resolve it in code. Several parts of the system are built against the same sections, so a contradiction resolved quietly in one place becomes divergence in four. Quote both sides, say what you would do and why, and wait.

The same applies to a failing test. If a test looks wrong, that is a finding, not an edit.

## Layout

    cmd/helmstudio     the daemon
    cmd/helm           the CLI: validate, dev, upgrade, test, doctor, adopt
    installer/         install.sh and uninstaller.sh, which install and remove helm
    internal/          daemon internals — platform, store, supervisor, install, weights, api
    packages/          helm-css, helm-runtime-sdk (go/py/node), helm-ui-sdk
    schema/            manifest.json — the manifest contract
    api/               openapi.yaml — the platform API contract
    studios/           registry pointers, one per studio
    docs/design/       the frozen design — the contract
    docs/plan/         build order, milestones, how work is delegated
    docs/artifacts/    the same material as rendered pages, for reading only
    docs/agents/       milestone briefs, the gate, report template
    test/conformance/  runs against both the daemon and the embedded provider

## Commands

    make gate     everything that must pass before a commit
    make test     unit tests
    helm validate studios/*.yaml

## What the gate cannot tell you

Nothing that touches a model can be verified in CI. Metal builds, real weights, memory behaviour, ffmpeg on videotoolbox and anything needing a signed binary are checked by hand on an Apple Silicon Mac. Say plainly what you could not verify rather than implying it passed.

## Never do these

- Change `schema/manifest.json` or `api/openapi.yaml` to make an implementation pass.
- Edit generated code by hand — change the generator or its source document and regenerate.
- Add a dependency without saying so and recording why.
- Delete or weaken a failing test.
- Touch anything under a user's models directory, or run reclaim, in a test.
