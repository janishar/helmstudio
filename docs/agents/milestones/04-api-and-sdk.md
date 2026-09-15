# M4 — API and SDK

**Effort:** 10–12 days.
**Machine-bound demo:** h3 records real takes with provenance intact.

The most consequential milestone in the list. Everything after it is built on
these shapes, and a name chosen badly here is renamed in three packages and two
generated clients later.

**Review this milestone twice.** Once when `api/openapi.yaml` is complete and
before a line of it is implemented, then again at the end. The first review is
cheap and catches what the second cannot fix.

## Reads

`docs/design/02-data-model.md`, `04-packages.md`, `05-sdk-and-custom-studios.md`,
`06-storage.md`, `07-platform-services.md`.

## Tasks

### 1. `api/openapi.yaml` — complete

Every platform service: kv, sessions, records, assets, gallery, jobs, events,
handoff/inbox. Full schemas, error shapes, pagination.

**Stop here and be reviewed.** Do not implement against a draft.

### 2. Store schema v2

Everything the API needs. Including `item_inputs` — gallery provenance is a
graph, and the reason the data model is relational rather than a pile of JSON
documents.

### 3. Endpoints

Implemented against the OpenAPI document, not the other way round.

### 4. Tokens and namespacing

Each studio gets a token scoped to its own namespace. A token must not reach
another studio's data. There is a test that tries.

### 5. Media engine

Content-addressed blob store plus a human-readable hardlink tree under
`library/`. Same inode, no copy: the CAS path gives integrity, the library path
gives a person somewhere to look.

`:adopt` **hardlinks** an existing file into the store. It never copies. A copy
doubles a 2 GB video silently, and the user discovers it as a full disk.

Bytes live in directories. Never BLOBs — a 2 GB row breaks Range streaming,
breaks backup, and cannot be hardlinked.

### 6. Generated clients

Go, Python, Node, generated from `api/openapi.yaml`. Committed to the repo
deliberately — a studio author should not need a generator to start. The gate
regenerates and fails on a diff, so a hand edit is caught rather than merged.

### 7. Embedded provider

The same contract against `./.helm/helm.db` with no daemon. This is what makes
a studio developable in isolation, and what makes later adoption an import
rather than a translation.

### 8. Conformance suite

One suite, run twice — once against the daemon, once against the embedded
provider.

Write it **from `api/openapi.yaml`**, before or alongside the implementation.
A conformance suite written from the implementation tests that the code does
what the code does. The measure: introduce a deliberate bug in one provider and
confirm the suite catches it. Do this, and report the result.

### 9. `helm dev`

A studio author runs their studio against the embedded provider with one
command, no daemon, no framework install.

## Files you may touch

    api/**, internal/api/**, internal/store/**, internal/media/**
    packages/helm-runtime-sdk/**
    cmd/helm/**
    test/conformance/**
    docs/decisions.md  (append only)

## Definition of done

- Conformance passes against both providers, from one suite.
- A deliberate bug in one provider is caught by the suite — demonstrated, not
  asserted.
- `:adopt` hardlinks; a test compares inode numbers.
- A studio token cannot read another studio's namespace; a test tries.
- Generated clients regenerate with no diff.
- `helm dev` runs h3 with no daemon present.
- h3 writes a real take with its inputs recorded in `item_inputs`.
- `make gate` green, now including drift and conformance.

## Review focus

- Was the conformance suite written from the contract or from the
  implementation? Would it catch a deliberate bug? Ask for the demonstration.
- Does `:adopt` ever copy — including a fallback when hardlinking fails across
  devices? What does it do then, and is that the right answer?
- Can a token reach another studio's namespace by any route: a shared gallery
  read, a job, a handoff, an id guessed from a URL?
- Is anything stored as a BLOB?
- Do the two providers share the code that enforces the contract, or duplicate
  it? Duplicated enforcement drifts.
- Is pagination consistent across every collection endpoint?
- Does the OpenAPI document name a resource with no table, or a table with no
  resource?
