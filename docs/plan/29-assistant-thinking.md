# helmstudio-next plan — Assistant thinking: "Thinking… 8 s" while the model thinks

[← Plan index](../README.md)

Status: **decided by the owner (2026-11-24); board approved by the owner (2026-11-24, "Approve"); build after plan 28.** API version: **1.54.0** (main is
at 1.53.0, plan 28; if another plan takes 1.54.0 first, this takes the next free one at build time). Not in a
milestone, like plans 25, 27 and 28.

The owner's ask, 2026-11-24: "when thinking show in the ui". The doubts were asked one by one (below).

Board: [Assistant](../mock/Assistant.dc.html), board 4 · Small states, two new cells at the end: "Waiting for the
model · it does not think" (today's `Working` chip, unchanged) and "Waiting for the model · it thinks" (the chip
reads `Thinking… 8 s` with a small spinner). The board keeps its height; canvas.json is unchanged.

Facts come from a read on 2026-11-24 of plan 18 (22.3, the turn loop and `evt.assistant.step`), plan 28 (thinking
per model, the snapshot's `thinking`), `contracts/openapi.yaml` (`AssistantStatus`, `AssistantThread.state`) and
`web_service/components/assistant/assistant.tsx` (the header chip).

## 33. Assistant thinking

### Decided by the owner, 2026-11-24

1. **Indicator only**: "Thinking… 8 s" while the model thinks. The reasoning text is never shown.
2. **Assistant drawer only**: not the director chat, not pipeline steps.
3. **Live only**: it shows only while waiting and goes when the reply comes. No "Thought for 8 s" line is kept.

### 33.1 What exists today

- While a turn's model call runs, the thread's `state` is `working` ("the agent is thinking"). The drawer shows
  nothing in the thread; the header chip says `Working` (also while actions run or the turn is parked).
- The browser learns the result only when `evt.assistant.step` lands (one step per model call; no token stream).
- Plan 28 gives each model a thinking level (Off / Low / Medium / High, or "Always thinks"). The Assistant's
  provider and model come from Admin › AI agents. A non-admin cannot read either.

### 33.2 Design

- **How the drawer knows**: `AssistantStatus` (what every user may know) gains `thinks: boolean`. Studio sets it
  from the Assistant's provider and model in the AI agents settings: true when the resolved level (plan 28's
  snapshot rule) is not Off, or the model always thinks. No level, no rule, no provider: false.
- **The chip**: when `thinks` is true and the thread is `working` (the model call, not actions), the header chip
  reads `Thinking… N s` with a quiet spinner, in place of `Working`. Otherwise today's chip stays as it is
  (`Working`, `Waiting for you`, `Read-only`). The phone sheet has the same header; its one-line bar shows only
  while actions run, so it is unchanged.
- **The timer** is the browser's: it starts at 0 when this tab sees the thread go to `working` (its own send, a
  resume after actions or a card) and stops when the step event lands (the state leaves `working`). It counts
  whole seconds. A reload mid-call starts again at 0 (small, accepted; no server time needed).
- **Tool calls in between**: server reads inside one `working` stretch (studio reads, memory) are quick and stay
  under the same count. Screen actions and cards park the turn (`waiting`): the chip goes back to today's, the
  lines show as now; when the turn resumes, a new `Thinking… 0 s` starts.
- **Read-only tabs** keep `Read-only`. No thinking text, tokens or "Thought for" line is stored or sent.

### 33.3 Contract change (1.54.0, back-compatible)

- `AssistantStatus.thinks: boolean`, required (studio always fills it). Fixtures with true and false.
- No message change: pipelines does not need to tell anyone; the level is already in the snapshot.

### 33.4 Tests

- `make contracts-check`: the new field and fixtures.
- **studio**: `thinks` from the settings: level High, Off, always thinks, a model without a rule, no provider.
- **web** (vitest): chip `Thinking… 0 s` then `… 3 s` with fake timers while `working` and `thinks`; `Working` when
  `thinks` is false; back to the chip of the state on `waiting`; gone when the step lands; read-only unchanged.
- **Contract suite** (`helmstudio-test`): `GET` status carries `thinks`.

### 33.5 Phases

| Phase | What | Hours |
|---|---|---|
| 1 | Contracts: `AssistantStatus.thinks`, 1.54.0, fixtures | 0.5 |
| 2 | studio: `thinks` from the AI agents settings and plan 28's rule; tests | 1 |
| 3 | web: the chip and the timer; tests | 1 |
| 4 | Gates and a dev-stack look with a thinking model | 0.5 |
| | Total | 3 |
