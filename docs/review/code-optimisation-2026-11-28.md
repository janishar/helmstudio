# Code optimisation review: helmstudio (alpha.6)

## Summary
- Healthy: the service boundaries hold. Contracts come first, services never import each other, owner scoping goes through WithUser and RLS, and events follow outbox, NATS, worker, projector, SSE. The tests and fixtures are strong, so moving code around is safe.
- Problem 1: the same code is pasted many times inside each service. Pipelines builds the NATS event envelope by hand in 4 places with 3 retry policies. Studio has 3 tables that turn HTTP status into a tool error code, and they disagree. The runner has 3 job handlers with the same decode and dead-letter steps.
- Problem 2: hand-written copies of things that already exist. Studio's tool-arg tables repeat catalog.json. Go code reinvents `cmp.Or`, `new(v)` and `slices.Contains`. Packs build ffmpeg command lines that `core.media` already builds.
- Problem 3: both Python services set line length to 100 while the code is written to about 120. That leaves about 700 `# fmt: skip` markers and many lines the formatter broke into odd shapes.
- Problem 4: some files are very large: create.tsx 4352 lines, check.py 4946, assistant.py 1880, and the api/studio package 30.8k. The web page also ships a fake-mode board that only tests use.

## Do first
| # | Title | Area | Files | What to change | Est. lines saved | Effort | Risk |
|---|---|---|---|---|---|---|---|
| 1 | Line length 120, drop `# fmt: skip` | pipelines, runner | pipelines_service/pyproject.toml:34, runner_service/pyproject.toml:50, every *.py | Set `line-length = 120`, delete the ~700 `# fmt: skip` markers, run `ruff format` once, commit it alone as a format-only commit | ~400 | S | low |
| 2 | Tool arg checks read from the catalog | studio | api/studio/assistant_tools.go:264-315, assistant_magic.go:32-115, gen/tools/catalog.json | Add `Input{Properties, Required}` to catalogTool. Write one `checkArgs` that does the unknown-key, required and uuid checks. Delete `toolInputs`, `magicTools` and the `uuids` map. Keep the per-tool meaning checks | ~60 | S | low |
| 3 | One envelope and send per service | pipelines, runner | pipelines_service/messaging/publisher.py:22-65, assistant.py:1776-1790, names.py:141-156, server.py:166-175; runner_service/messaging/models.py:48-63 | Add `envelope(...)` (with the contract check) and `send(js, subject, msg, msg_id)` (the one retry loop) to each service's own publisher.py, and call them everywhere. server.py's `evt.run.status` gains the retry it lacks today. names.py keeps its log-and-drop when a message is off contract | ~60 | S | low |
| 4 | Stdlib swaps and one status-code table | studio, cli | assistant_tools.go:150, 486-500; assistant_voice.go:280-318; assistant_magic.go:117, 284; pipeline_review.go:360; lora/service.go:305; lora/normalise.go:234; assistant.go:65; magic/send.go:555; timeline/rules.go:241-242; common/payload.go:31; cli/import.go:372, admin.go:409-421, run.go:536 | Keep one of `single`/`orRefused`. Use `cmp.Or` for cmpAny, cmpErr and cmpOr; `new(v)` for intp, f64 and boolp (go 1.27); `slices.Contains` in IsAdmin and cli contains; one generic zero-deref in cli; `magicBase` in magicTool. Put one status-to-code map and its inverse next to toolErr (today apiRefused turns a 403 into `invalid`) | ~65 | S | low |
| 5 | Shared handler error reply | studio | controller.go:224-240, 425; magic_controller.go:173; timeline_controller.go:154-180; assistant_controller.go:122 | Give the 4 `{Detail}` error types a marker method so `reply` sends them as 422 in one place. Delete replyWorkflow and replyTimeline, merge magicIDBody and timelineIDBody, and use `noContent` in the 8 delete handlers written out by hand. Leave withBody's lenient decoding as it is | ~70 | S | low |
| 6 | One responder and one pull loop | pipelines | states.py:23-46, memory.py:127-164, judge_test.py:59-82, assistant_memory.py:286-309, providers.py:541-723, assistant.py:1830-1880, main.py:95-108, 198-205 | Add `request(body, check, subject, code)` and `respond(nc, subject, fn)` to the service's messaging/. Keep `pull` in main.py with a `concurrent` flag and delete assistant.pull. Unsubscribe from one `subs` list in a loop | ~75 | S | low |
| 7 | Assistant model call in one method | pipelines | assistant.py:881-884, 929-1038, 1040-1175, 1183-1218 | Add `_ask_model` (spend, `_call`, CallError and CancelledError handling, `_spent`) and `_provider(d)`, and use them in fold, ask and summarize. Summarize then goes through `_call`, so a Stop cancels it too. Add local `took` and `park` helpers in tools() | ~70 | S | low |
| 8 | `_failed` helper in graph | pipelines | graph.py:419, 457, 951, 1474, 1536; agent_loop.py:92-99 | One `_failed(state, up, nid, kind, part, visit, error)` replaces six 7-line blocks. Move agent_loop._sources into graph and use it at the 3 places that do that check | ~40 | S | low |
| 9 | Runner job delivery helpers | runner | consumer.py:81-124, messaging/models.py:87-153, 354-369, messaging/media.py:70-155, main.py:223-302, 360-372 | Add `decode`, `dead_letter` and `keep_alive`, one generic `pull`, and a `durable(js, name, subject, ack_wait, max_deliver)` called 3 times. Compute node_schemas once. Each handler keeps its own ack logic | ~90 | M | low |
| 10 | Packs call core.media for ffmpeg | runner | core/media.py:323-329, packs/minimax_h3/native.py:243, 256, packs/zonos3/native.py:131, packs/echo/fake.py:218, packs/ltx/common.py:22, 244-249, core/ask.py:78 | Make `media._ffmpeg` public. Packs call it and wrap MediaError in RunError. ltx and ask import FFMPEG/FFPROBE from core.media. Leave the streaming Popen in mmaudio alone | ~45 | S | low |
| 11 | Web shared helpers | web | magic/board.tsx:97-104, 209-218; timeline/editor.tsx:143-184; create/create.tsx:302-404; lib/hydrated.ts:3; lib/runs.ts:88, lib/voices.ts:136, timeline/lanes.tsx:131, lib/timeline-tools.ts:435; the 5 copies of `plural`, 3 of `sleep`, 2 of `deleteCard` | Add `useStoredFlag(key)` plus never-throwing `stored.get/set/json` to lib/hydrated.ts, and export noSubscribe. Move clock, plural and sleep into lib/format.ts, deciding once whether the clock rounds or floors (today 119.6 s shows 1:59 in one place and 2:00 elsewhere). Move deleteCard into lib/assistant.ts | ~120 | S | low |
| 12 | Remove Magic's fake mode from the shipped page | web | components/magic/board.tsx:115-122, use-board.ts:588-832, menus.ts, lib/magic-fixture.ts, tests/components/magic-{board,assets,assistant}.test.tsx | The /magic route always renders StudioBoard; LocalBoard is reached only from 22 test renders. Port those tests to the API mock in magic-studio.test.tsx, then delete LocalBoard, useLocalBoard, Store, `local` and `initial`, and the null branches. Move estText and EST_* into lib/magic.ts | ~300 | M | medium |
| 13 | WithUser takes an owner id | studio | common/tx.go:12-23; api/studio/service.go:88, 266; gallery/service.go:75; lora/service.go:159; plugin_pass.go:95 | All 5 callers pass a Caller with no roles, so the admin branch never runs. Change to `WithUser(ctx, pool, ownerID, fn)` with role 'user', and drop the 3 Caller-building wrappers. This also stops anyone from getting admin-wide reads by passing a full Caller by accident | ~10 | S | low |
| 14 | Fix the messaging section of the services doc | docs | docs/plan/13-services.md:55-60, 188-193 | The doc says 3 projector consumers; the code has 5 (projector.go:13-31). Its subject names are also out of date. Replace the subject table with a pointer to the `## NATS` section of contracts/README.md | ~20 | S | low |

## Then
- Timeline edits have 3 implementations (an owner decision). check.py:3098-3647 keeps a Python copy of the edit rules. Making it work like Magic (compare edit.go and timeline.ts with each case's `expect`) deletes about 500 lines. If the owner keeps all three, at least share one `replay()` helper between check_edit_replay (4051) and check_magic_replay (4085), saving about 30 lines.
- Split contracts/check.py (4946 lines) by topic into sibling modules, as a pure move. check.py re-exports the names contract_tests imports (`pipeline_problems`, `resolve_mentions`).
- contract_tests: 39 test files import helpers from other test modules. Move the shared helpers into helpers.py and conftest.py, and remove the duplicate `detail()`, `palette()` and project-name constant (~60 lines).
- pipelines tests: move the shared helpers in test_stage_b into conftest.py, and rename judge_test.py to judge.py (it is production code; only `testpaths` stops pytest from collecting it).
- Split assistant.py by moving its pure functions out (assistant_check.py, assistant_magic.py). Do it after rows 6-7 so less code moves.
- Break up the long functions as pure moves: magic/ops.go `apply` (~490 lines), pipeline_rules.go `pipelineProblems` (~470), magic/send.go `BuildSend` (~450). The fixture tests are the safety net.
- Move the schema types in studio registry/registry.go (lines 24-751) into registry/types.go.
- Move api/studio (30.8k lines) one feature at a time, Magic first, into its own package, the way gallery and lora are laid out (L, medium risk).
- Pull hooks out of the web god components (Create is 3058 lines in one function): useAddonGates, useDrafts, useTimelineEdits. One PR each, no rewrite (L).
- Add `editTimelineRetry` to lib/api.ts, replacing the two 409 retry loops (editor.tsx:316-349, timeline/assistant.ts:180-221; ~30 lines).
- Move timeline WORDS and `say` into lib/timeline.ts, and move the FormSlot, Started and Want types out of the assistant adapter.
- Give the Mac bridge one type in lib/desktop.ts (it is typed 3 ways in status.tsx, folders.tsx and context-menu.tsx). Rename assistant.tsx's `mac()`.
- Have usePeaks in create/audio.tsx use the cached decoder in lib/timeline-detail.ts (~30 lines).
- runner: add `pcm16` and `wav` to core/media.py. Watch two packs: stable_audio_3 does not clip and ltx truncates instead of rounding, so keep their output the same or re-run their goldens.
- runner: let MediaError and JobError be one class, and keep one Cancelled (the one in core/graph.py).
- runner: move the model-folder helpers out of core/pack.py (282-407) into core/folders.py, which ends the lazy imports that break cycles. Re-export from pack for now (M, medium risk).
- runner: move packs/echo/loader.py to core/loaders_fake.py and delete the sys.modules hack (fake.py:26-28).
- runner: use `hashlib.file_digest` in lora.sha256_file. Pass Node its 13 values by keyword (pack.py:794).
- runner: move ltx's shared canvas, duration and guider constants into ltx/common.py, and delete common.rnd in favour of core.media.rnd.
- runner tests: one session-scoped `torch_values` fixture, plus one `snr` and one `e4m3` helper, in conftest.py (~40 lines).
- Move the root tests/golden into runner_service/tests/golden (test_qwen_golden.py:29 reaches outside its own service), and keep the newer record_qwen3_tts.py.
- Put the Makefile runner supervisor loop (runner-%, runners-nvidia) in one .tools/runner script, and use HEALTH_PORT_* instead of the hard-coded ports.
- Shared HMAC for signed links: the same format lives in studio_service/common/sign.go and files_service/api/file/sign.go. Both already import helmgo, so it could live there. Only do this if the owner counts helmgo as shared library code; otherwise add a shared test vector instead.
- Small fixes: drop KONG_NGINX_WORKER_PROCESSES from deploy/release/compose.yml:67 (kong/Dockerfile:17 already sets it). Fix the cli/Makefile comment, which names gen_test.go (that file does not exist; the drift test is mcp_test.go). Move packs/README.md into runner_service/packs and fix the tree in docs/plan/07-repo-layout.md. Cut the comment in migrations/embed.go down to one line.
- Housekeeping, needs the owner's OK: the 8 worktrees under .claude/worktrees are fully merged (0 commits ahead of alpha.6). Remove them and delete their branches to free about 8.7 GB.

## Dropped
- Bulk removal of the ~1000 `API 1.xx` tags: comment churn only.
- Renaming the 12 test files whose names exist in both tests/ and api/studio/: cosmetic.
- Putting tool step labels into catalog.json: a contract change for little gain.
- Shared owner-scoped store helper and the take-cache "leak": the leak isn't real (take ids are per owner, and other ids get 404), and a helper for two stores is premature.
- Moving the .ast rules out of globals.css and the mask variable: organisation only.
- Removing the ~120 exports used only in their own file: cosmetic.
- Rewriting MediaJobs.handle's branches: about 15 lines, a matter of taste.
- Passing TEST in to resilience.sh: the Makefile and the script use different base paths (deploy/ vs ../deploy/), so it is not a drop-in.
- Building the stack.json image list from `compose config --images`: changes how the shipped Mac app starts up, for 15 lines.
- Splitting desktop stackSteps and Runners: a pure move in a 784-line file.
- Renaming the two desktop `tilde` functions: cosmetic.
- Splitting contracts/README.md: lots of churn, and about 186 code citations point at it.
- A Go message drift test, and moving gen/envelope.go: new scaffolding; the Makefile already documents the hand-kept file.
- Duplicates merged into kept items: the cross-cutting envelope finding (into row 3), the second check.py split, the second HMAC, the second judge_test, runner's line length (into row 1), the localStorage helpers (into row 11), and agent_loop's private helpers (into row 8; dropping the underscores alone is cosmetic).

## Ponytail verdict
- The biggest deletions available: Magic's fake mode (~300 lines from the shipped page), the Python copy of the timeline edit rules if the owner agrees (~500), and the formatter fix (~400 skip markers and broken lines).
- After those: the hand-written tool arg tables (catalog.json already has them), the repeated envelope, responder and pull loops in pipelines, the job-handler copies in the runner, and Go helpers that stdlib already provides.
- No new framework or dependency is needed anywhere. Each item is a deletion or a merge into a helper inside the same service.
