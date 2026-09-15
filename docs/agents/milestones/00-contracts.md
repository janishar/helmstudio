# M0 — Contracts

**Effort:** 2–3 days. **Machine-bound demo:** none — which is why this is first.

Nothing here runs a model, spawns a process or touches a network. M0 exists so
that every milestone after it has something to be checked against. If M0 is
wrong, ten milestones are wrong quietly.

## Already done

- `docs/design/*.md` — the eight frozen design documents, exported to the repo.
- `docs/decisions.md` — 28 frozen decisions, 5 open.
- `schema/manifest.json` — the manifest contract, draft 2020-12,
  `additionalProperties: false` throughout. Validated: the h3 manifest passes;
  six deliberate failures are all rejected.

Do not change any of these to make the rest of M0 easier. If one of them is
wrong, that is a finding.

## Remaining

### 1. Four studio manifests

Write `studios/<id>.yaml` for h3c-studio, ltx-2-studio, iris-studio and AuK, by
**reading the real repositories**, not by inference from their names.

    /Users/janisharali/GenAI/minimax-h3-mlx/h3c-studio
    /Users/janisharali/GenAI/ltx/ltx-2-studio
    /Users/janisharali/GenAI/minimax-h3-mlx/iris-studio
    /Users/janisharali/GenAI/audio/AuK

For each, establish from the code — not from a README:

- every process it actually starts, and the role of each
  (`main` / `sidecar` / `worker` / `oneshot`);
- how its port is chosen, and whether it accepts a `--port` flag or insists on
  a fixed one;
- whether it has a health endpoint. **h3 has no `/healthz`** — do not invent
  one for it; use a `tcp` probe and record the gap;
- its build steps, and the shell they assume;
- its `runtime`: `framework` (`native-kernel` / `mlx` / `pytorch` / `ggml` /
  `onnx` / `jax`) and the `backends` it genuinely supports. These are separate
  fields for a reason: PyTorch-on-cuda and PyTorch-on-mps are different
  answers for a given machine;
- whether it is `heavy`;
- its weights, and whether any is user-selectable.

**h3's health timeout is 30 s, not 240.** Liveness is not occupancy: h3 may be
running and holding nothing, or running and holding 21 GB. A long timeout here
is a copied default, and the review will treat it as one.

A field that exists for exactly one studio is a schema smell. Say so rather
than adding it.

### 2. `helm validate`

`cmd/helm` with a `validate` subcommand. It validates a manifest against
`schema/manifest.json` **and** enforces the seven rules JSON Schema cannot
express:

1. every name in a `depends_on` refers to a process declared in the same
   manifest;
2. the dependency graph has no cycle;
3. exactly one process has `role: main`;
4. at most one process is `heavy`;
5. every `{models.x}` substitution resolves to a declared weight — and
   `{models.selected}` only when some weight is marked selectable;
6. every `{ports.x}` refers to a sibling process in the same manifest;
7. `cwd` does not escape the studio root — after resolution, not by string
   inspection.

And the converse of 5: a weight marked selectable but never referenced by
`{models.selected}` in any command is an error, not a warning. A selectable
weight nothing can select is a manifest bug that only surfaces in front of a
user.

Errors carry the file, the JSON pointer or process name, what is wrong and what
was expected. A validator whose output is `invalid manifest` has failed at its
only job.

Exit 0 clean, non-zero on any error. `--json` for machine-readable output.

### 3. Tests

Table-driven, with fixtures under `internal/manifest/testdata/`:

- the four real manifests, valid;
- one fixture per rule above, failing, asserting the **specific** error — not
  merely that validation failed;
- the six schema-level failures that already exist: no `processes`, both port
  modes at once, two health probes on one process, an unknown backend, neither
  `repo` nor `local_path`, an unknown field.

A test that asserts "returns an error" passes when the validator returns the
wrong error. Assert which.

### 4. `api/openapi.yaml` — outline only

Paths, resource names and the shape of each object. No request/response schemas
in full, no examples, no generation. M4 completes it, and M4's first review
happens on that file before anything implements it.

The point of the outline now is to discover a resource the data model has no
table for, while changing it is free.

## Files you may touch

    studios/*.yaml
    cmd/helm/**
    internal/manifest/**
    api/openapi.yaml
    go.mod, go.sum
    docs/decisions.md  (append only)

## Definition of done

- `helm validate studios/*.yaml` exits 0 on all four.
- Every rule above has a fixture that fails for the right reason.
- `go test ./...` green, `gofmt` clean, `go vet` clean.
- Each manifest's `runtime` block matches what the repository actually does.
- The report lists every place a manifest could not express what a studio does.

## Review focus

- Any field that exists for only one studio.
- h3's health timeout: 30 s, and a `tcp` probe rather than an invented
  `/healthz`.
- Is `cwd` escape checked after path resolution, or by looking for `..` in a
  string? The second is not a check.
- Does the cycle detector handle a self-dependency and a three-node cycle?
- Were the manifests written from the repositories or from the design
  documents' examples? Spot-check one against its source.
- Does `api/openapi.yaml` name a resource `docs/design/02-data-model.md` has no
  table for, or vice versa?
