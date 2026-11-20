// Package migrations: goose SQL files (00001 platform: RLS function, outbox, projector dedupe; 00002 sessions, runs,
// run outputs and the per-session event log; 00003 what the runner resolved: a run's resolved values, an output's seed;
// 00004 the dispatcher: runs.lane, nodes, released_at, projector dedupe per attempt; 00005 the gallery: takes, their lineage edges, run_outputs.take_id; 00006 dispatch_owners, each owner's last release; 00007 batches; 00008 takes.etag, take_parents.idx; 00009 user_workflows, runs.workflow_version; 00010 user_pipelines; 00011 library_items, runs.mentions, batches.elements; 00012 loras, runs.loras; 00013 looks: library_items.loras and from_take_id, runs.looks, batches.looks, batch_rows.looks; 00014 takes.video and run_outputs.video: a video take's metadata and poster; 00015 the llm pack and agent nodes: runs.answer and thumbs, batches.bounds, models, memory, again_of, batch_steps.decision_id and memory, decisions; 00016 agents stage B: user_pipelines.remote, providers, batches.providers, egress, reserved, director, remote, rows_added, batch_events.message_id, batch_chat, tool_calls; 00017 M5a: model_jobs, host_settings, takes.import_key, legacy and import_wait, import_edges; 00018 model_jobs.heard_at; 00020 M6: runs.lost_host; 00022 judge_tests).
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
