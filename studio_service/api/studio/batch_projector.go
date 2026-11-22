package studio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/helmstudio/helmgo/micro"
	"github.com/helmstudio/helmgo/utility"

	"github.com/helmstudio/studio_service/api/studio/dto"
	"github.com/helmstudio/studio_service/api/studio/message"
	"github.com/helmstudio/studio_service/common"
	"github.com/helmstudio/studio_service/gen"
	"github.com/helmstudio/studio_service/messaging"
)

// MountPipeline registers the handlers of pipelines_service's evt.pipeline.<batch_id>.batch, .row and .step (durable
// studio-pipelines): they build the batch record the UI reads.
func (p *Projector) MountPipeline(group *micro.NatsJetStreamGroup) {
	group.AddHandler("evt.pipeline.*.batch", pipelineHandler(p, applyPipelineBatch))
	group.AddHandler("evt.pipeline.*.row", pipelineHandler(p, applyPipelineRow))
	group.AddHandler("evt.pipeline.*.step", pipelineHandler(p, applyPipelineStep))
	group.AddHandler("evt.pipeline.*.chat", pipelineHandler(p, applyPipelineChat))
	group.AddHandler("evt.pipeline.*.named", p.namedHandler) // API 1.43: a session's title
}

// pipelineApply applies one event to its batch, locked and checked; it returns the row it changed (0: none) and
// whether anything changed.
type pipelineApply[D any] func(ctx context.Context, tx pgx.Tx, batchID, ownerID string, data D) (int, bool, error)

// pipelineHandler decodes the envelope (an undecodable one is dropped) and applies it once per (batch, seq) when the
// batch belongs to the envelope's owner and the subject names it; a database failure naks it.
func pipelineHandler[D interface{ PipelineSeq() int64 }](p *Projector, apply pipelineApply[D]) micro.NatsJetStreamHandlerFunc {
	return func(ctx context.Context, msg *micro.NatsJetStreamMessage) {
		event, err := micro.JsonToMsg[gen.Message[D]](msg.Data())
		if err != nil {
			micro.TermNatsJetStreamMessage(msg, err)
			return
		}
		parts := strings.Split(msg.Subject(), ".")
		seq := event.Data.PipelineSeq()
		if event.JobID == nil || !utility.IsUUID(*event.JobID) || len(parts) != 4 || parts[2] != *event.JobID || seq < 1 {
			log.Printf("batch projector: dropping %s on %s without a matching job_id or a seq", event.Type, msg.Subject())
			micro.AckNatsJetStreamMessage(msg)
			return
		}
		batchID := *event.JobID
		err = common.System(ctx, p.pool, func(tx pgx.Tx) error {
			var owner string
			err := tx.QueryRow(ctx, `SELECT owner_id::text FROM batches WHERE id = $1 FOR UPDATE`, batchID).Scan(&owner)
			if errors.Is(err, pgx.ErrNoRows) || (err == nil && owner != event.OwnerID) {
				log.Printf("batch projector: %s for unknown batch %s (owner %s)", event.Type, batchID, event.OwnerID)
				return nil
			} else if err != nil {
				return err
			}
			// attempt 0: a batch's seq space, apart from any run's (job_id differs anyway)
			if first, err := messaging.FirstSeen(ctx, tx, batchID, 0, int(seq)); err != nil || !first {
				return err
			}
			row, changed, err := apply(ctx, tx, batchID, owner, event.Data)
			if err != nil || !changed {
				return err
			}
			return batchChanged(ctx, tx, batchID, row)
		})
		if err != nil {
			micro.NakNatsJetStreamMessage(ctx, msg, err)
			return
		}
		micro.AckNatsJetStreamMessage(msg)
	}
}

// batchStatuses are the statuses pipelines reports for a batch.
var batchStatuses = map[string]bool{"running": true, "paused": true, "done": true, "failed": true, "stopped": true}

// applyPipelineBatch sets the batch's status when the event is newer than the last one applied: reason with paused or
// failed, started_at at the first running, finished_at while done, failed or stopped; done is stopped once the
// user stopped the batch.
func applyPipelineBatch(ctx context.Context, tx pgx.Tx, batchID, ownerID string, data message.PipelineBatch) (int, bool, error) {
	if !batchStatuses[data.Status] {
		log.Printf("batch projector: batch %s: unknown status %q", batchID, data.Status)
		return 0, false, nil
	}
	if data.Status != "paused" && data.Status != "failed" {
		data.Reason = nil
	}
	var memory any // the memory snapshot pipelines took (a pipeline batch), kept once sent
	if snap := (struct {
		TakenAt *string `json:"taken_at"`
	}{}); json.Unmarshal(data.Memory, &snap) == nil && snap.TakenAt != nil {
		memory = data.Memory
	}
	// done after the user's Stop is stopped: pipelines learns of the stop through cmd.pipeline.stop (CMD), and the runs
	// studio cancelled for it can end the last rows first (EVT), so it may end the batch done before it reads the stop
	tag, err := tx.Exec(ctx, `UPDATE batches SET status = CASE WHEN $2 = 'done' AND control = 'stop' THEN 'stopped' ELSE $2 END,
		reason = $3, pipeline_seq = $4,
		started_at = CASE WHEN $2 = 'running' THEN coalesce(started_at, clock_timestamp()) ELSE started_at END,
		finished_at = CASE WHEN $2 IN ('done', 'failed', 'stopped') THEN coalesce(finished_at, clock_timestamp()) END,
		memory = coalesce($5::jsonb, memory),
		director = CASE WHEN director IS NULL THEN NULL ELSE coalesce($6::jsonb, director) END,
		remote = CASE WHEN remote IS NULL THEN NULL ELSE coalesce($7::jsonb, remote) END
		WHERE id = $1 AND pipeline_seq < $4`, batchID, data.Status, data.Reason, data.Seq, memory,
		directorState(data.Director), remoteCounts(data.Remote))
	changed := err == nil && tag.RowsAffected() == 1
	if changed && (data.Status == "done" || data.Status == "failed" || data.Status == "stopped") {
		err = batchShotsEnded(ctx, tx, batchID, ownerID) // API 1.17: a re-render's steps that never ran leave their slots
	}
	return 0, changed, err
}

// directorState is an evt.pipeline.batch's director when it is the contract's, else nil (the stored one stays).
func directorState(raw json.RawMessage) any {
	var d dto.BatchDirector
	if json.Unmarshal(raw, &d) != nil || d.Node == "" || !slices.Contains([]string{"idle", "waiting", "thinking", "off"}, d.State) ||
		d.Wakes < 0 || d.MaxWakes < 1 {
		return nil
	}
	return d
}

// remoteCounts is an evt.pipeline.batch's remote counters when they are the contract's, else nil.
func remoteCounts(raw json.RawMessage) any {
	var r dto.BatchRemote
	if json.Unmarshal(raw, &r) != nil || r.MaxCalls < 1 || r.MaxTokens < 1 || r.Calls < 0 || r.Tokens < 0 || r.NetworkFailures < 0 {
		return nil
	}
	return r
}

// rowStatuses are the statuses pipelines reports for a row.
var rowStatuses = map[string]bool{"queued": true, "running": true, "waiting": true, "succeeded": true, "failed": true,
	"cancelled": true}

// applyPipelineRow sets the row's whole state when the event is newer than the last one applied to it and not of an
// older attempt. The run must be a run of this batch, and only the owner's takes are kept. A pipeline row also takes
// node, cells and, while waiting, parked; a waiting for a gate visit already picked (pipelines republished it before it
// read the pick) shows running.
func applyPipelineRow(ctx context.Context, tx pgx.Tx, batchID, ownerID string, data message.PipelineRow) (int, bool, error) {
	if !rowStatuses[data.Status] {
		log.Printf("batch projector: batch %s row %d: unknown status %q", batchID, data.Row, data.Status)
		return 0, false, nil
	}
	if data.Status != "failed" {
		data.Error = nil
	}
	if data.TakeIDs == nil {
		data.TakeIDs = []string{}
	}
	data.TakeIDs = slices.DeleteFunc(data.TakeIDs, func(id string) bool { return !utility.IsUUID(id) })
	if data.RunID != nil && !utility.IsUUID(*data.RunID) {
		data.RunID = nil
	}
	var parked *dto.BatchRowParked
	if data.Status == "waiting" {
		if json.Unmarshal(data.Parked, &parked) != nil || parked == nil || parked.Gate == "" || len(parked.Candidates) == 0 {
			log.Printf("batch projector: batch %s row %d: waiting without parked", batchID, data.Row)
			return 0, false, nil
		}
		var picked bool
		if err := tx.QueryRow(ctx, `SELECT picks ? $3 FROM batch_rows WHERE batch_id = $1 AND n = $2`, batchID, data.Row,
			pickKey(data.Attempt, parked.Gate, parked.Visit)).Scan(&picked); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return 0, false, err
		} else if picked {
			data.Status, parked = "running", nil
		}
	}
	var cells any // absent: the row keeps its cells
	if len(data.Cells) > 0 && string(data.Cells) != "null" {
		cells = data.Cells
	}
	tag, err := tx.Exec(ctx, `UPDATE batch_rows br SET status = $3, attempt = $4, pipeline_seq = $5, error = $6,
		run_id = (SELECT id FROM runs WHERE id = $7::uuid AND batch_id = $1 AND batch_row = $2),
		take_ids = coalesce((SELECT array_agg(t.id ORDER BY u.ord) FROM unnest($8::uuid[]) WITH ORDINALITY u(id, ord)
			JOIN takes t ON t.id = u.id AND t.owner_id = $9), '{}'),
		finished_at = CASE WHEN $3 IN ('succeeded', 'failed', 'cancelled') THEN coalesce(br.finished_at, clock_timestamp()) END,
		node = $10, parked = $11, cells = CASE WHEN br.cells IS NULL THEN NULL ELSE coalesce($12::jsonb, br.cells) END
		WHERE batch_id = $1 AND n = $2 AND pipeline_seq < $5 AND attempt <= $4`,
		batchID, data.Row, data.Status, data.Attempt, data.Seq, data.Error, data.RunID, data.TakeIDs, ownerID, data.Node,
		parked, cells)
	return data.Row, err == nil && tag.RowsAffected() == 1, err
}

// stepParts and stepStatuses are what evt.pipeline.step may carry.
var (
	stepParts = map[string]bool{"inputs": true, "submit": true, "wait": true, "gate": true, "route": true, "output": true,
		"verdict": true, "apply": true, "memory": true, "tools": true, "custom": true}
	stepStatuses = map[string]bool{"finished": true, "resumed": true, "current": true}
)

// applyPipelineStep stores one graph step of a pipeline row when it is newer than the one stored for (row, attempt,
// step). Only runs of this batch row and the owner's takes are kept.
func applyPipelineStep(ctx context.Context, tx pgx.Tx, batchID, ownerID string, data message.PipelineStep) (int, bool, error) {
	if !stepParts[data.Part] || !stepStatuses[data.Status] || data.Step < 1 || data.Attempt < 1 || data.Node == "" {
		log.Printf("batch projector: batch %s row %d: dropping step %d (%s %s)", batchID, data.Row, data.Step, data.Part, data.Status)
		return 0, false, nil
	}
	keep := func(ids []string) []string {
		return slices.DeleteFunc(slices.Clone(ids), func(id string) bool { return !utility.IsUUID(id) })
	}
	edge := any(nil)
	if len(data.Edge) > 0 && string(data.Edge) != "null" {
		edge = data.Edge
	}
	if data.Changed == nil {
		data.Changed = []string{}
	}
	if data.Loops == nil {
		data.Loops = map[string]int{}
	}
	decisionID, err := storeDecision(ctx, tx, batchID, ownerID, data)
	if err != nil {
		return 0, false, err
	}
	var memory any
	if stepMemory := (dto.StepMemory{}); json.Unmarshal(data.Memory, &stepMemory) == nil && stepMemory.Read != nil &&
		stepMemory.Wrote != nil {
		memory = dto.StepMemory{Read: keep(stepMemory.Read), Wrote: keep(stepMemory.Wrote)}
	}
	branch, turn, calls := data.Branch, data.Turn, toolCalls(data.ToolCalls, 4)
	if branch != nil && (*branch < 0 || *branch > 23) {
		branch = nil
	}
	if turn != nil && (*turn < 1 || *turn > 16) {
		turn = nil
	}
	tag, err := tx.Exec(ctx, `INSERT INTO batch_steps (batch_id, n, attempt, step, owner_id, seq, node, part, visit, status,
		what, changed, edge, loops, run_ids, take_ids, error, checkpoint_id, decision_id, memory, branch, turn, tool_calls)
		SELECT $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13::jsonb, $14,
		coalesce((SELECT array_agg(r.id ORDER BY u.ord) FROM unnest($15::uuid[]) WITH ORDINALITY u(id, ord)
			JOIN runs r ON r.id = u.id AND r.batch_id = $1 AND r.batch_row = $2), '{}'),
		coalesce((SELECT array_agg(t.id ORDER BY u.ord) FROM unnest($16::uuid[]) WITH ORDINALITY u(id, ord)
			JOIN takes t ON t.id = u.id AND t.owner_id = $5), '{}'), $17, $18, $19, $20::jsonb, $21, $22, $23::jsonb
		FROM batch_rows WHERE batch_id = $1 AND n = $2
		ON CONFLICT (batch_id, n, attempt, step) DO UPDATE SET seq = excluded.seq, node = excluded.node, part = excluded.part,
		visit = excluded.visit, status = excluded.status, what = excluded.what, changed = excluded.changed,
		edge = excluded.edge, loops = excluded.loops, run_ids = excluded.run_ids, take_ids = excluded.take_ids,
		error = excluded.error, checkpoint_id = excluded.checkpoint_id, at = excluded.at,
		decision_id = coalesce(excluded.decision_id, batch_steps.decision_id), memory = coalesce(excluded.memory, batch_steps.memory),
		branch = excluded.branch, turn = excluded.turn, tool_calls = coalesce(excluded.tool_calls, batch_steps.tool_calls)
		WHERE batch_steps.seq < excluded.seq`,
		batchID, data.Row, data.Attempt, data.Step, ownerID, data.Seq, data.Node, data.Part, max(data.Visit, 1), data.Status,
		data.What, data.Changed, edge, data.Loops, keep(data.RunIDs), keep(data.TakeIDs), data.Error, data.CheckpointID,
		decisionID, memory, branch, turn, calls)
	return data.Row, err == nil && tag.RowsAffected() == 1, err
}

// toolCalls keeps at most max tool calls (tools.schema.json toolCall) that are the contract's shape, each result cut to
// 8000 characters of JSON (null when longer, as pipelines would have cut it); nil when absent.
func toolCalls(raw json.RawMessage, max int) any {
	var calls []struct {
		ID     string          `json:"id"`
		Tool   string          `json:"tool"`
		Args   json.RawMessage `json:"args"`
		Result json.RawMessage `json:"result"`
		Error  *string         `json:"error"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &calls) != nil {
		return nil
	}
	out := []map[string]any{}
	for _, c := range calls {
		if len(out) == max || !wireTools[c.Tool] || c.ID == "" || len(c.ID) > 200 || len(c.Args) == 0 || c.Args[0] != '{' {
			continue
		}
		result := any(nil)
		if len(c.Result) > 0 && c.Result[0] == '{' && len(c.Result) <= 8000 {
			result = c.Result
		}
		out = append(out, map[string]any{"id": c.ID, "tool": c.Tool, "args": c.Args, "result": result, "error": c.Error})
	}
	return out
}

// wireTools are the tool names a model sees (tools.schema.json wireName).
var wireTools = map[string]bool{"gallery_search": true, "memory_read": true, "memory_write": true, "library_read": true,
	"rows_add": true, "rows_edit": true, "route": true, "set_items": true}

// storeDecision keeps a step event's decision (the newest seq per id) when its id names this batch, row, attempt and
// node; a judge's candidates (the owner's takes) are the takes it affected. It returns the id, nil without one.
func storeDecision(ctx context.Context, tx pgx.Tx, batchID, ownerID string, data message.PipelineStep) (*string, error) {
	var d struct {
		ID       string `json:"id"`
		Node     string `json:"node"`
		Kind     string `json:"kind"`
		Visit    int    `json:"visit"`
		Attempt  int    `json:"attempt"`
		Workflow string `json:"workflow"`
		Judge    *struct {
			Takes []struct {
				TakeID string `json:"take_id"`
			} `json:"takes"`
		} `json:"judge"`
	}
	if len(data.Decision) == 0 || string(data.Decision) == "null" {
		return nil, nil
	}
	if json.Unmarshal(data.Decision, &d) != nil || !dto.HasModel(d.Kind) || d.Node != data.Node || d.Attempt != data.Attempt ||
		d.ID != fmt.Sprintf("%s:%d:%d:%s:%d", batchID, data.Row, d.Attempt, d.Node, d.Visit) {
		log.Printf("batch projector: batch %s row %d: dropping a decision that is not this row's", batchID, data.Row)
		return nil, nil
	}
	var takes []string
	if d.Judge != nil {
		for _, t := range d.Judge.Takes {
			takes = append(takes, t.TakeID)
		}
	}
	_, err := tx.Exec(ctx, `INSERT INTO decisions (id, batch_id, n, attempt, owner_id, seq, kind, data, workflow_title, take_ids)
		SELECT $1, $2, $3, $4, $5, $6, $7, $8, b.pipeline->'workflows'->$9->>'title',
		coalesce((SELECT array_agg(t.id ORDER BY u.ord) FROM unnest($10::uuid[]) WITH ORDINALITY u(id, ord)
			JOIN takes t ON t.id = u.id AND t.owner_id = $5), '{}')
		FROM batches b WHERE b.id = $2
		ON CONFLICT (id) DO UPDATE SET seq = excluded.seq, data = excluded.data, workflow_title = excluded.workflow_title,
		take_ids = excluded.take_ids WHERE decisions.seq < excluded.seq`,
		d.ID, batchID, data.Row, d.Attempt, ownerID, data.Seq, d.Kind, data.Decision, d.Workflow, uuids(takes))
	return &d.ID, err
}

// pickKey names one gate visit of a row attempt in batch_rows.picks.
func pickKey(attempt int, gate string, visit int) string {
	return fmt.Sprintf("%d:%s:%d", attempt, gate, visit)
}
