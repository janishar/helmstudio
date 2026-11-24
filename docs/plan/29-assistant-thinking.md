# helmstudio-next plan — Assistant thinking: "Thinking… 8 s" while the model thinks

[← Plan index](../README.md)

Status: **decided by the owner (2026-11-24); board approved by the owner (2026-11-24, "Approve"); built 2026-11-24 (33.6); moved into the chat by the owner (2026-11-24, 33.7), board redrawn and approved by the owner (2026-11-24, "Approve"); building.** API version: **1.54.0** (main is
at 1.53.0, plan 28; if another plan takes 1.54.0 first, this takes the next free one at build time). Not in a
milestone, like plans 25, 27 and 28.

The owner's ask, 2026-11-24: "when thinking show in the ui". The doubts were asked one by one (below).

Board: [Assistant](../mock/Assistant.dc.html), board 4 · Small states, two new cells at the end: "Waiting for the
model · it does not think" (today's drawer, unchanged: `Working` chip, no bubble) and "Waiting for the model · it
thinks" (a quiet `Thinking… 8 s` bubble in the thread under the user's last message; the chip stays `Working`). Both
are drawn at the drawer's width with the real header (Memory, New chat, Close). canvas.json is unchanged.

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
- **The bubble** (33.7 replaces the first design, a header chip): when `thinks` is true and the thread is `working`
  (the model call, not actions), the thread shows a reply-in-progress bubble at the bottom, just under the user's
  last message, where the reply will land: left-aligned like assistant messages, the quiet assistant-bubble look,
  a small spinner and `Thinking… N s` in mono, muted text. The reply replaces it. It scrolls into view like a new
  message. The header chip stays today's (`Working`, `Waiting for you`, `Read-only`) and the title `Assistant`
  shows in full. The phone sheet has the same header; its one-line bar shows only
  while actions run, so it is unchanged.
- **The timer** is the browser's: it starts at 0 when this tab sees the thread go to `working` (its own send, a
  resume after actions or a card) and stops when the step event lands (the state leaves `working`). It counts
  whole seconds. A reload mid-call starts again at 0 (small, accepted; no server time needed).
- **Tool calls in between**: server reads inside one `working` stretch (studio reads, memory) are quick and stay
  under the same count. Screen actions and cards park the turn (`waiting`): the chip goes back to today's, the
  lines show as now; when the turn resumes, a new `Thinking… 0 s` starts.
- **Read-only tabs** keep `Read-only` and show no bubble. No thinking text, tokens or "Thought for" line is stored or sent.

### 33.3 Contract change (1.54.0, back-compatible)

- `AssistantStatus.thinks: boolean`, required (studio always fills it). Fixtures with true and false.
- No message change: pipelines does not need to tell anyone; the level is already in the snapshot.

### 33.4 Tests

- `make contracts-check`: the new field and fixtures.
- **studio**: `thinks` from the settings: level High, Off, always thinks, a model without a rule, no provider.
- **web** (vitest): (33.7: the bubble, not the chip) `Thinking… 0 s` then `… 3 s` with fake timers while `working` and `thinks`; `Working` when
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

### 33.6 As built (all phases; 2026-11-24)

- **Contracts** (API 1.54.0): `AssistantStatus.thinks`, required; `AssistantThreadList` fixtures with true and
  false; README Assistant › Thinks; web `gen/api.ts` regenerated.
- **studio**: `modelThinks(preset, model, saved)` (thinking.go) reads plan 28's own snapshot rule for that one model
  (`thinkingSnapshot`) and is true when the resolved level is not `off`. So a saved level counts; with nothing saved,
  the rule's `default` counts (DeepSeek flash's high, OpenAI's medium, Mistral Large's high: those models think on
  every call without being told, so the chip should say so); always thinks counts (`on`). No rule, a rule without a
  default and nothing saved (Ollama qwen3, Gemini), a `"models"` preset (OpenRouter) with nothing saved, no preset or
  no provider: false. Why the snapshot rule: it is exactly what the call sends and reserves room for, so the chip and
  the call never disagree. Set in `assistantConfig` beside `provider`, so it is false whenever the assistant is off.
- **web**: a `Thinking` chip (assistant.tsx) replaces `Working` when `status.thinks`, the thread is `working` and the
  tab is not running actions; it mounts at 0 and counts whole seconds with `setInterval`, so it starts when this tab
  sees `working` and goes when the state leaves it (the step lands, or the turn parks on actions or a card). A server
  step that keeps the state `working` keeps the same count. Styled as the board: `.st-go` outline, mono 500, an
  11 px spinning `LoaderCircle`. It has `role="status"` (as the board) with `aria-live="off"`, so a screen reader is
  not told every second. Read-only and `Waiting for you` win, as before.
- **Tests**: studio `TestModelThinks` (11 cases: high, off, a default, always thinks, no rule, a rule without a
  default, `"models"` with and without a level, no preset) and the settings test checks `thinks` false; web
  `assistant-drawer` "thinking (plan 29)": `Thinking… 0 s` → `3 s` with fake timers, `Working` while the parked
  actions run, a new `0 s` after they finish, gone on idle; `Working` when `thinks` is false; another tab's chat
  stays `Read-only`; contract `test_assistant.py` checks `thinks` is false for the fake model.
- **Gates**: contracts-check passed; studio `make test lint` (243 tests, DB tests on the test Postgres); web `make
  test lint` (1144 tests, 80 files; one older warning in run-on.test.tsx); contract suite `test_assistant.py` 2
  passed, then the full suite 752 passed, 117 skipped.
- **Deviations**: none from the plan. The dev-stack look with a real thinking model was not done (the dev stack's
  data is not changed by builds); the chip was checked by the tests only.

### 33.7 Moved into the chat (owner, 2026-11-24)

The owner, looking at the live drawer: "thinking is not intuitive its on the top where eye does not go i am focused
on that chat bubble". Decided:

1. **In the chat, not the header.** `Thinking… N s` is a quiet bubble at the bottom of the thread, under the last
   message, where the reply will appear. The reply replaces it. It scrolls into view.
2. **The header goes back to today's**: the chip says `Working` while the model works, and the title `Assistant`
   shows in full again. The rule that hid the title while thinking is dropped.
3. **A model that does not think**: no bubble, as today (the board never showed a typing bubble, so none is added).
4. Unchanged: the timer rules, `AssistantStatus.thinks`, live only, no thinking text, read-only tabs.

Build (web only, no contract change): move the counter from the chip in `assistant.tsx` into a bubble rendered as
the thread's last item (`role="status"`, `aria-live="off"`); the chip reverts to `Working`; the `sr-only` title rule
goes. Tests: the bubble shows and counts in the thread, the chip says `Working`, the title is visible, no bubble
when `thinks` is false or the tab is read-only.
