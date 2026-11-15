# Prompt templates of the agent nodes (judge/v1, write/v1, decide/v1)

The user turns of docs/reference-notes/llm.md 5.5, with `{name}` slots; `system.txt` is the system turn of all three.
Pipelines fills them (it owns the templates and their schemas: contracts/README.md "The engine") and sends the result
as an ask run's `prompt`; the runner appends the schema (`core.ask.SCHEMA_HINT`) and never edits the text. A change
here is a new version (`judge.v2.txt`): the replay key covers the filled prompt.

The live versions are v2 (pipelines_service/agents.py, contracts/README.md "The engine"): v1 with every untrusted text
fenced as a `<label>:\n<<<\n…\n>>>` data block and trimmed first, so the instructions and the re-ask always fit.

- judge: `{brief}` the step's prompt, `{n}` takes, `{takes}` "take 1 ... take N", `{keyframes}` 3, `{criteria}`
  "1. [must] <text> 2. [nice] <text>", `{memory}` "The user's past picks for this pipeline: kept - "<note>"; rejected
  - "<note>". " (at most 8, newest first) or empty.
- write: `{instruction}`, `{text}` from[0], `{reasons}` the judge's reasons as a list, `{mentions}` "@<element> ...
  look:<id> ...".
- decide: `{question}`, `{context}` named state values with the judge's verdict and reasons, `{routes}` '"<label>":
  <meaning> ...'.
