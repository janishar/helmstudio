# Milestones and review protocol

The eleven-milestone breakdown of the build plan. The operative briefs are in
`docs/agents/milestones/`; this is the shape of the whole and the protocol that
holds it together.

h3 studio is the reference implementation and is already complete. The
framework is built *against* it, not including it.

## The gate

`make gate` is what must pass before anything lands. It is built in M1 and
grows as the contracts it checks come into existence — see `docs/agents/gate.md`.
Anything it cannot check is listed per-milestone as machine-bound and queues
for an integration window on an Apple Silicon Mac.

## The eleven

| | Milestone | Effort | Machine-bound demo |
|---|---|---|---|
| M0 | Contracts — design into the repo, manifest schema, four manifests, `helm validate`, OpenAPI outline | 2–3d | none, which is why it is first |
| M1 | Foundation — repo, platform seams, directories helper, store and schema v1, the gate | 5–6d | Keychain round-trip |
| M2 | Supervision — loader, ports, substitution, spawn, health, teardown, re-adopt, logs and SSE, plain shelf | 7–8d | launch h3, `kill -9`, re-adopt |
| M3 | Install and weights — clone, build, resume, the HF downloader, linked weights, reclaim | 8–9d | clean install, interrupt and resume, link an existing model |
| M4 | **API and SDK** — OpenAPI, schema v2, endpoints, tokens, media engine, clients, embedded provider, conformance, `helm dev` | 10–12d | h3 records real takes with provenance |
| M5 | Python studios — uv environments, ltx and AuK, the switch dialog | 6–7d | three studios side by side |
| M6 | Design and UI — helm-css, fourteen screens, ui-sdk components, h3's token migration | 10–12d | theme toggle re-themes running studios |
| M7 | Library, editor and iris — selectable weights, the three-source library, the editor, import and export, the approval screen | 6–8d | wrap a repo with no manifest |
| M8 | Timeline and export — the document, the editor, conform and stream-copy, golden tests | 10–12d | **most of it** |
| M9 | Mac app and release — Electron, handshake, browser auth and CSRF, signing, notarisation | 7–9d | **all of it** |
| M10 | Docs and site | 8–10d | quickstart on a clean machine |

**M4 is the most consequential.** Split its review: once when
`api/openapi.yaml` is complete and before any implementation, then again at the
end.

## Review focus highlights

The full list for each milestone is in its own brief. The ones that matter most:

- **M0** — any field that exists for only one studio is a schema smell; h3's
  health timeout is 30 s, not 240.
- **M1** — are WAL and `foreign_keys(ON)` proven by a test rather than asserted
  in a comment; does any path escape the directories helper.
- **M2** — re-adoption must check **all three** of pid, start time and pgid; a
  failed health check must never restart a `main` process.
- **M3** — **can any delete path follow a symlink**; is a 403 on resume treated
  as an expired URL rather than corruption.
- **M4** — was the conformance suite written from the contract or from the
  implementation, and would it catch a deliberate bug; does `:adopt` ever copy;
  can a token reach another studio's namespace.
- **M5** — any special case in Go for a specific studio means the schema is
  wrong.
- **M8** — is stream-copy taken only when genuinely legal; do the golden tests
  compare frame hashes or only that a file appeared.

## On depth

M0–M4 are fully specified. M5 onward are scoped deliberately: M5's shape
depends on what M0's manifests reveal, M6's on what the API actually looks
like, M8's on how the media engine behaves. Each is expanded to M0-level detail
at its own kickoff, when the two things a good brief needs — the contract it
builds against and the last milestone's findings — both exist.

Review checklists accumulate from findings rather than being guessed at the
start.
