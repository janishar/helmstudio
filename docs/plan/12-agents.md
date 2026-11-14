# helmstudio-next plan — Agent-powered pipelines

[← Plan index](../README.md)

## 16. Pipelines are LangGraph graphs with agent nodes

Decided 2026-11-14: **LangGraph is the pipeline engine.** A pipeline (section
7.2) compiles to a LangGraph `StateGraph`; its nodes generate, decide, write,
remember and talk to LLM agents. Workflows (section 7) are unchanged: they stay
typed generation graphs run by workers. This section owns the engine and the
agent nodes.

### 16.1 What it is for

| Job | Nodes |
|---|---|
| Throw out broken takes and try again | Step → Judge, with a capped retry edge |
| Pick the best of 4 stills before the expensive video step | Step → Judge (keep 1) → Step |
| Rewrite a prompt when the result misses the brief | Judge → Write → Step |
| Turn a brief or script into a shot list and run it | Agent (director) → Step, per shot |
| Take a different path depending on a verdict | Decide |
| Get better at picking the way you pick | Memory (judge examples from your picks) |
| Ask another model or agent for help | Agent, local or remote |
| Steer a run by talking to it | Chat with the pipeline's director agent |

### 16.2 Node kinds in a pipeline

| Node | Does |
|---|---|
| **Inputs** | The batch's rows (section 7.2) |
| **Step** | Runs a workflow and returns its takes |
| **Gate** | Stops for a human pick |
| **Judge** | A vision-language model checks takes against a plain checklist (*must* / *nice to have*), keeps the best *K* of *N*, and gives reasons. Starts in **Suggest** mode: the row parks like a Gate with the judge's pick pre-selected, and each pick records whether you agreed. Switch to **Auto** when you trust it; it then picks without parking. Its `pass` / `fail` edges are the only branch a Judge owns. |
| **Write** | Produces text: prompts from a brief, a shot list from a script, a rewritten prompt from a judge's reasons. It answers with `@element` mentions and look ids, so section 14 still does the expansion. |
| **Decide** | Routes to one of its outgoing edges from a structured answer |
| **Agent** | An LLM agent with tools: search the gallery, read and write memory, read the library, add rows, and route to other nodes. It can be the pipeline's **director**, which you can talk to while it runs. |
| **Memory** | Reads the snapshot taken at submit, or writes for the next batch (section 16.6) |
| **Custom** | A pack's own LangChain runnable or LangGraph subgraph, with a typed input and output |
| **Output** | Names the collection |

Edges may branch and may loop. Every loop declares a cap; a pipeline with an
uncapped cycle does not validate. Decide and Agent nodes route only along
their declared outgoing edges; a jump to any other node is an item failure,
and every route back counts against the cap of the loop it closes.

```json
{"schema": 1, "kind": "pipeline", "id": "still-to-clip",
 "inputs": {"prompt": "Text", "motion": "Text"},
 "nodes": {
   "still":   {"kind": "step", "workflow": "qwen-image", "seeds": 4, "preset": "Draft",
               "bind": {"prompt.text": "$prompt"}},
   "judge":   {"kind": "judge", "takes": "still", "criteria": "clean-image",
               "keep": 1, "mode": "suggest"},
   "rewrite": {"kind": "write", "from": ["$prompt", "judge.reasons"], "to": "$prompt"},
   "review":  {"kind": "gate", "takes": "still"},
   "clip":    {"kind": "step", "workflow": "ltx-image-to-video",
               "bind": {"encode.images[0].image": "judge.picked", "prompt.text": "$motion"}},
   "out":     {"kind": "output", "collection": "still-to-clip"}
 },
 "edges": [
   ["still", "judge"],
   ["judge", {"pass": "clip", "fail": "rewrite", "max": 2, "then": "review"}],
   ["rewrite", "still"],
   ["review", "clip"],
   ["clip", "out"]
 ]}
```

### 16.3 How the engine runs

- **Where**: graphs run in **pipelines_service** (section 17.1), a Python
  service with no GPU and no model weights (`langgraph`, `langchain-core`,
  `langchain-openai`; no numpy, no torch). It consumes `cmd.pipeline.*`,
  following the reference's agents service.
- **One thread per row** (`thread_id = batch:row`). State is small plain JSON:
  the row's inputs, take ids, short text, decision ids and loop counters.
  Never images or latents.
- **Fan-out** (one row into several shots) uses `Send` with a branch id. Each
  branch's values sit in state under that id with a merge reducer, and every
  fan-out declares a maximum, which Submit uses for its worst case. A Step
  with several seeds is one run returning several takes; a Judge reads that
  list. Resumes of one thread are serialised, and when several of its runs
  have finished it is resumed once with all their results.
- **Every local model call is a queue item.** A Step, and any node that needs
  a local model, is two graph nodes. `submit` asks studio_service to create
  a run (`studio.runs.create`) under a key of batch + row + node + loop count +
  branch, and records its id; asking again returns the same run. `wait`
  contains only `interrupt()`; when studio's `run_finished` event for that run
  arrives, the service resumes the thread with its result, whatever the
  outcome. Nothing
  is held while parked, and waiting for a model is the same mechanism as
  waiting for a human.
- **Calls that leave the queue are still controlled.** A local server's calls
  are batch-lane queue items, because they share the machine. Remote calls go
  through a per-provider limit (two in flight by default) and a per-batch cap
  on calls and tokens shown at Submit; Pause and Stop hold and cancel them too.
- **Scheduling belongs to the dispatcher** (section 17.5). Parked threads cost
  nothing; the dispatcher still releases one run at a time per host,
  preferring loaded models, so judge
  calls for many rows run together rather than swapping models per row. Until
  row 1 reaches its Output, fails, or parks for a human, only row 1's items
  run; then every row is released, a bounded window of rows per batch asked
  of studio at a time, so one user's batches add a bounded number of runs
  to the dispatcher's line-up.
- **Rule for node code**: a node that calls `interrupt()` does nothing else
  (a resumed node re-runs from its first line).
- **Loop safety**: caps are counted in state and force the exit edge.
  LangGraph's own recursion limit (10007 by default in 1.x) is set per run from
  the caps as a backstop only.
- **Durability**: `durability="sync"` with LangGraph's async Postgres
  checkpointer and store in the `pipelines` database, `thread_id` = batch and
  row, with the reference's strict serializer over plain-dict state. When a batch finishes or is deleted its threads
  are deleted; the batch record keeps what the UI needs. Checkpoints are never
  migrated: after an update that changes the state shape, older parked batches
  offer "Run again" instead of Resume.
- **A batch runs the pipeline it was submitted with.** Its graph is compiled
  from the snapshot stored on the batch, never from the live
  document, so editing a pipeline affects only new batches.
- **Two records, two jobs**: the checkpointer resumes threads; the batch
  record in the `studio` database is what the run view reads, built by the
  projector from the pipeline's events. The UI never reads LangGraph's
  checkpoints.
- **Resume after a crash**: the batch waits for the user's Resume. Then queue
  items left interrupted are queued again under the same key; items whose
  result was stored but not delivered resume their thread with it; threads
  that were mid-node continue. If a pack's code changed, Resume asks whether
  to continue with the new code or cancel.
- **Submit** walks every branch of the compiled graph: validation, memory
  admission for the worst-case set of models, and a time and disk estimate
  given as a range, the top end from the loop caps.

### 16.4 Talking to LLMs and agents

| Where the model is | How | Notes |
|---|---|---|
| **Local, in a worker** (default) | An `llm` pack: a local vision-language model as a component under the residency manager, called as a queue item | Where a family already has one (Qwen Image's text encoder is a vision-language model), the judge reuses it at no extra load. Temperature 0, fixed seed. |
| **Local server you run** | Any OpenAI-compatible endpoint (Ollama, LM Studio, `llama-server`) through `ChatOpenAI(base_url=…)` | Its memory is outside the app's control; you declare its size and the app reserves it. |
| **Remote model or agent** | A LangChain chat model for the provider, or a remote agent endpoint | Off by default, enabled per pipeline. Submit shows the provider, the model and exactly what leaves the machine. Keys are service secrets, set by the admin. Network failures are retried and counted apart from verdicts. |

- A judge is sent derived thumbnails (images at about 1024 px, videos as a
  few keyframes), content-addressed and recorded, never the full take.
- Structured answers use JSON-schema output; a parse failure after one retry
  is an item failure.
- LangSmith tracing is forced off unless you turn it on.
- **Chat**: a pipeline with a director agent has a chat panel. The director
  has its own thread and never waits on runs itself; it wakes on a message
  from you or on a row finishing. It can answer, and add or change rows. A row
  it adds or changes goes through the same Submit checks before it runs, and a
  rejected row is answered in chat with the reason.

### 16.5 Custom nodes

A pack registers a runnable or subgraph with typed pydantic input and output.
Custom nodes run in pipelines_service, so they are orchestration only: plain Python
on state, the app's LLM and Step services, and the built-in nodes. Anything
that needs a model goes through a Step or the `llm` pack. No
`langchain-community`, no in-process model loading. A custom node that calls
the network counts as a remote model: off by default and shown at Submit.

### 16.6 Memory

Backed by LangGraph's Postgres store (namespaces `<owner>/pipeline/<id>`,
`<owner>/project/<id>`),
key-value and filter search in v1; semantic search waits for a local embedding
model that goes through admission.

| Memory | Lives | Used for |
|---|---|---|
| **Run memory** | The row's thread state | The judge's reasons carry into the retry and the rewrite |
| **Judge examples** | Per pipeline | Takes you kept or rejected at review, with your note. This is how a judge learns your taste. |
| **Notes** | Per pipeline or project | Short facts an agent writes or you type |
| **Conversation** | The director's thread | What you and the director said |

- **In a batch, every read uses the snapshot taken at submit**, an Agent's
  memory tool included, so results do not depend on the order rows run in.
  Writes are keyed by batch, row, node and loop count, so a re-run overwrites
  rather than duplicates, and they are visible from the next batch. Only the
  director's chat reads live.
- Every read and write is in the decision record. The Memory panel lists what
  is stored, where it came from, and lets you delete it.
- Not memory: characters (elements), looks and stars, settings.

### 16.7 Decisions are recorded and replayed

- Each decision stores the model, the checklist or prompt, the thumbnails it
  saw, the structured answer and the reasons, on the batch item and on the
  takes it affected.
- Resume reuses stored decisions. "Run again" asks:
  same decisions, or decide again. Remote answers are never treated as
  reproducible.
- Where decisions show: the run view (section 8.3) and the Explain drawer.

### 16.8 Execution view

An icon on the pipeline graph opens the execution view of one row: what
LangGraph actually did, step by step.

- **The graph, as run**: nodes the row has passed are marked, the edge taken at
  each branch is drawn, each loop shows how many times it went round, and the
  node the row is parked on is highlighted.
- **The step list**: one line per graph step in order — the node, what
  happened in plain words, which part of the state changed, and whether it
  finished, resumed after waiting, or is where the row is now.
- **A step's details**: for a Judge, each check with pass or fail, the
  thumbnails it saw, the model, and what it read from memory; for a Write, the
  text before and after; for a Step, the run and its takes; for an Agent, its
  messages and tool calls.
- **State after a step**: the row's state at that checkpoint, read-only.
- **Retry row from here** re-runs the row from the chosen step, reusing
  everything before it.

Where it comes from: the step list and details are built from the pipeline's
events, stored by studio with the batch (section 17.2). The state at a
checkpoint is fetched on demand from pipelines_service through studio, for the
row's owner only; it is the one place a checkpoint is read, and never by the
browser directly. The view is available for a batch's lifetime; when its
threads are deleted (section 16.3) the step list remains and the state does not.

### 16.9 Versions

Version numbers are in section 18. Only current LangGraph APIs are used
(`StateGraph`, `Command`, `Send`, `interrupt`, `retry_policy`, `durability`
from `langgraph.types`); no `langgraph.prebuilt` agents, `NodeInterrupt` or
`MessageGraph`. Provider packages for remote models are optional extras.
