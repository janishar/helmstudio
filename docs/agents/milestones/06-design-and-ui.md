# M6 — Design and UI

**Effort:** 10–12 days. **Machine-bound demo:** the theme toggle re-themes running studios.

> **Scoped, not specified.** This milestone is expanded to M0-level detail at
> its own kickoff, when the two things a good brief needs both exist: the
> contract it builds against, and the previous milestone's review findings.
> Expanding it now would mean guessing, and a guessed brief is worse than none
> because it reads as authority. Expand it with the stronger model, and carry
> forward the review focus that earlier milestones actually produced.

## Shape

`helm-css` as the token layer; the fourteen screens; the `helm-ui-sdk`
components — gallery, player, terminal, timeline editor; and h3's migration
from its 45 CSS variables and 64 raw colour literals onto the tokens.

Its shape depends on what the API actually looks like after M4.

## Reads

`docs/design/03-design-system.md`, `04-packages.md`, `08-h3-dry-run.md`.

## Known at freeze

- Three packages, one-way dependency: `helm-css` → `helm-runtime-sdk` →
  `helm-ui-sdk`. **The arrow never reverses.**
- **Only the runtime SDK knows the wire format. Components receive a client;
  they never construct one.** An API change then regenerates one package and
  leaves every component untouched.
- Accent yellow `#ffc700` dark, `#f5b800` light. Warning is orange — it stopped
  being yellow when the accent became yellow, and a warning that reads as the
  accent is not a warning.
- IBM Plex Sans and IBM Plex Mono.
- h3 carries 64 raw colour literals. Migrating them is the milestone's proof
  that the token layer is sufficient; a literal left behind is a token missing.

## Review focus

- Does any component import the wire format, construct a client, or know a URL?
- Did any token get renamed to suit a component?
- Are both themes covered by visual regression, and does the gate run it?
- Does the theme toggle reach a studio that is already running, or only a
  reload?
