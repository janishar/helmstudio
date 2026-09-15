# Working this repository with an agent

The framework is built in eleven milestones against a frozen design. Each
milestone is a written contract: what to build, which files may be touched,
what "done" means, and what a human has to check by hand afterwards.

One agent implements a milestone. A different agent reviews it. They never
run as the same process — an implementer that can also review itself will,
eventually, relax a test rather than fix the code that failed it.

## The loop

1. **Kickoff.** Point the implementer at its brief and the milestone:

       Read docs/agents/implementer.md, then docs/agents/milestones/02-supervision.md.
       Follow both. Begin.

   Nothing else is needed in the prompt. The brief tells it what to read, what
   it may touch, and when to stop.

2. **Implement.** The implementer works only inside the milestone's file list.
   It runs `make gate` itself before it reports. It does not commit.

3. **Report.** It writes `docs/agents/reports/<milestone>.md` from the template
   in `report-template.md`: gate output, what it could not verify, every
   judgement call it made, and any design contradiction it hit.

4. **Review.** A fresh agent, stronger model, reads the diff, the milestone
   doc, the design, the decision log and the report:

       Read docs/agents/reviewer.md, then docs/agents/milestones/02-supervision.md.
       Review the working tree against them. Change nothing.

   It emits BLOCKING / SHOULD-FIX / NOTE findings and edits nothing.

5. **Fix.** Blocking findings go back to the implementer. Repeat 2–4 until
   nothing blocks.

6. **Land.** Commit per `CONTRIBUTING.md` — imperative subject, why in the
   body, no trailers. Append the milestone's decisions to `docs/decisions.md`.

## Model split

Bounded implementation against a written contract is where a fast model is
strong and cheap enough to iterate. Review is judgement-heavy, and a reviewer
that misses a contradiction costs far more than the tokens it saved. Use the
stronger model for step 4 and for expanding a scoped milestone (below).

## Depth

M0–M4 are specified in full. M5 onward are scoped deliberately and are
expanded to full detail at their own kickoff — M5's shape depends on what the
four manifests reveal, M6's on what the API actually looks like, M8's on how
the media engine behaves. Writing them now would mean guessing, and a guessed
brief is worse than no brief because it reads as authority.

Each expansion inherits the previous milestone's review findings. The review
checklists grow from what actually went wrong rather than from what was
imagined at the start.

## Sequence

| | Milestone | Machine-bound demo |
|---|---|---|
| M0 | Contracts | none — which is why it is first |
| M1 | Foundation | Keychain round-trip |
| M2 | Supervision | launch h3, `kill -9` the daemon, re-adopt |
| M3 | Install and weights | clean install, interrupt and resume, link an existing model |
| M4 | API and SDK | h3 records real takes with provenance |
| M5 | Python studios | three studios side by side |
| M6 | Design and UI | theme toggle re-themes running studios |
| M7 | Library, editor, iris | wrap a repo that ships no manifest |
| M8 | Timeline and export | most of it |
| M9 | Mac app and release | all of it |
| M10 | Docs and site | quickstart on a clean machine |

M4 is the most consequential milestone in the list. Review it twice: once when
`api/openapi.yaml` is complete and before a line of it is implemented, then
again at the end.

## h3 studio

`h3c-studio` is the reference implementation and is already complete. The
framework is built *against* it, not around it. If the framework needs a
special case in Go for h3, the schema is wrong — that is a finding, not a
patch.
