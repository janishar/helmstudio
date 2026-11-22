package studio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"

	"github.com/jackc/pgx/v5"

	"github.com/helmstudio/helmgo/network"

	gallerydto "github.com/helmstudio/studio_service/api/gallery/dto"
	gallerymodel "github.com/helmstudio/studio_service/api/gallery/model"
	"github.com/helmstudio/studio_service/api/studio/dto"
	"github.com/helmstudio/studio_service/api/studio/message"
	"github.com/helmstudio/studio_service/api/studio/model"
	"github.com/helmstudio/studio_service/common"
)

// Gates and the execution view (contracts/README.md "Batches of a pipeline", "Execution view"): the review grid of
// parked rows, a pick (recorded with its cmd.pipeline.pick in one transaction), a row's steps from the stored
// evt.pipeline.step, and a step's checkpoint state from pipelines (request/reply, the owner only).

// StateGet asks pipelines_service for a row's state at one checkpoint (pipelines.state.get); an error is no answer.
type StateGet func(ctx context.Context, req message.StateGet) (message.StateReply, error)

// SetStateGet sets how the service reaches pipelines' state; without one the state endpoint answers 503.
func (s *Service) SetStateGet(fn StateGet) { s.stateGet = fn }

// rowGate is a waiting row as the review and a pick read it.
type rowGate struct {
	Row     int
	Attempt int
	Status  string
	Inputs  json.RawMessage
	Parked  json.RawMessage
	Cells   json.RawMessage
	Picks   json.RawMessage
}

// rejected counts a gate's rejected visits in a row's cells: the fail-edge turns taken so far.
func rejected(cells json.RawMessage, gate string) int {
	var list []cellIn
	_ = json.Unmarshal(cells, &list)
	n := 0
	for _, cell := range list {
		for _, a := range cell.Attempts {
			if cell.Node == gate && a.Status == "rejected" {
				n++
			}
		}
	}
	return n
}

// onReject says what rejecting every candidate does now, and how many fail-edge turns are left.
func onReject(p storedPipeline, gate string, cells json.RawMessage) (string, int) {
	for _, edge := range p.Doc.Edges {
		if edge.From != gate {
			continue
		}
		if edge.Branch == nil {
			return "fail", 0
		}
		left := edge.Branch.Max - min(rejected(cells, gate), edge.Branch.Max)
		if left > 0 {
			return "retry", left
		} else if edge.Branch.Then == "" { // API 1.28 Ask me: a reject at the cap ends the row
			return "fail", 0
		}
		return "then", 0
	}
	return "fail", 0
}

// BatchReview lists every waiting row with its signed candidates, by row number.
func (s *Service) BatchReview(ctx context.Context, caller common.Caller, batchID string) (dto.BatchReview, error) {
	out := dto.BatchReview{BatchID: batchID, Items: []dto.BatchReviewItem{}}
	if _, err := parseRow(batchID, "1"); err != nil {
		return out, err
	}
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		stored, isPipeline, err := readStoredPipeline(ctx, tx, batchID)
		if err != nil || !isPipeline {
			return err
		}
		rows, err := collect[rowGate](ctx, tx, `SELECT n, attempt, status, inputs, parked, cells, picks FROM batch_rows
			WHERE batch_id = $1 AND status = 'waiting' AND parked IS NOT NULL ORDER BY n`, batchID)
		if err != nil {
			return err
		}
		var ids []string
		parked := make([]dto.BatchRowParked, len(rows))
		for i, row := range rows {
			_ = json.Unmarshal(row.Parked, &parked[i])
			ids = append(ids, parked[i].Candidates...)
		}
		type candidate struct {
			ID     string
			Kind   string
			Key    string
			Thumb  string // a video's poster, else the image itself
			Video  []byte
			Width  *int
			Height *int
			Seed   *int64
		}
		found, err := collect[candidate](ctx, tx, `SELECT t.id::text, t.kind, t.key, `+gallerymodel.ThumbKey+`, t.video, t.width, t.height,
			(SELECT o.seed FROM run_outputs o WHERE o.take_id = t.id LIMIT 1)
			FROM takes t WHERE t.id = ANY($1::uuid[]) AND t.deleted_at IS NULL AND t.ready`, uuids(ids))
		if err != nil {
			return err
		}
		byID := map[string]candidate{}
		for _, c := range found {
			byID[c.ID] = c
		}
		var decisionIDs []string
		for _, p := range parked {
			if p.Kind == "judge" && p.DecisionID != "" {
				decisionIDs = append(decisionIDs, p.DecisionID)
			}
		}
		decisions, err := s.readDecisions(ctx, tx, decisionIDs)
		if err != nil {
			return err
		}
		for i, row := range rows {
			p := parked[i]
			item := dto.BatchReviewItem{Row: row.Row, Attempt: row.Attempt, Inputs: map[string]any{}, Gate: p.Gate,
				GateTitle: stored.Doc.Nodes[p.Gate].Title, Visit: p.Visit, Keep: p.Keep, Candidates: []dto.ReviewCandidate{}}
			_ = json.Unmarshal(row.Inputs, &item.Inputs)
			item.OnReject, item.RejectsLeft = onReject(stored, p.Gate, row.Cells)
			decision, judged := decisions[p.DecisionID]
			judged = judged && decision.Judge != nil
			if p.Kind == "judge" { // the judge's pick pre-selected, its verdict and the model that gave it
				item.Kind, item.Suggested, item.DecisionID = "judge", nonNil(p.Suggested), p.DecisionID
				item.Model = stored.Workflows[stored.Models[p.Gate]].Title
				if p.OnReject != "" {
					item.OnReject = p.OnReject
				}
				if judged {
					item.Verdict = decision.Judge.Verdict
				}
			}
			for _, id := range p.Candidates {
				if c, ok := byID[id]; ok {
					candidate := dto.ReviewCandidate{TakeID: id, Kind: c.Kind, URL: s.signer.Sign(c.Key),
						ThumbURL: s.signer.Sign(c.Thumb), Video: gallerydto.Info(c.Video, s.signer.Sign),
						Width: c.Width, Height: c.Height, Seed: c.Seed}
					for i := 0; judged && i < len(decision.Judge.Takes); i++ {
						if t := decision.Judge.Takes[i]; t.TakeID == id {
							candidate.Judge = &dto.ReviewJudge{Passes: t.Passes, Rank: t.Rank, Checks: t.Checks, Reason: t.Reason}
						}
					}
					item.Candidates = append(item.Candidates, candidate)
				}
			}
			if len(item.Candidates) > 0 {
				out.Items = append(out.Items, item)
			}
		}
		return nil
	})
	return out, err
}

// pickRecord is one gate visit's answer as batch_rows.picks keeps it.
type pickRecord struct {
	TakeIDs []string `json:"take_ids,omitempty"`
	Reject  bool     `json:"reject,omitempty"`
}

// PickBatchRow answers a row's gate: it records the pick on the gate cell, sets the row running and writes
// cmd.pipeline.pick. The same pick again for that gate visit answers the row and sends nothing.
func (s *Service) PickBatchRow(ctx context.Context, caller common.Caller, batchID, rowText string, body *dto.BatchPick) (dto.BatchRow, error) {
	n, err := parseRow(batchID, rowText)
	if err != nil {
		return dto.BatchRow{}, err
	}
	want := pickRecord{TakeIDs: body.TakeIDs, Reject: body.Reject != nil}
	notWaiting := network.NewConflictError(fmt.Sprintf("row %d is not waiting at %s", n, body.Gate), nil)
	var out dto.BatchRow
	err = s.tx(ctx, caller, func(tx pgx.Tx) error {
		batch, err := getBatch(ctx, tx, batchID, true)
		if err != nil {
			return err
		}
		row, err := one[rowGate](ctx, tx, `SELECT n, attempt, status, inputs, parked, cells, picks FROM batch_rows
			WHERE batch_id = $1 AND n = $2 FOR UPDATE`, batchID, n)
		if err != nil {
			return err
		}
		picks := map[string]pickRecord{}
		_ = json.Unmarshal(row.Picks, &picks)
		var parked *dto.BatchRowParked
		_ = json.Unmarshal(row.Parked, &parked)
		if parked == nil { // moved on already: the same pick again for its latest visit of that gate is a no-op
			if visit := latestPick(picks, row.Attempt, body.Gate); visit > 0 && samePick(picks[pickKey(row.Attempt, body.Gate, visit)], want) {
				out, err = s.readRow(ctx, tx, batchID, n)
				return err
			}
			return notWaiting
		}
		key := pickKey(row.Attempt, parked.Gate, parked.Visit)
		switch earlier, done := picks[key]; {
		case parked.Gate != body.Gate || row.Status != "waiting":
			return notWaiting
		case done && samePick(earlier, want):
			out, err = s.readRow(ctx, tx, batchID, n)
			return err
		case done:
			return network.NewConflictError(fmt.Sprintf("row %d already has another pick at %s", n, body.Gate), nil)
		case ended(batch.Status) || batch.Control == "stop":
			return network.NewConflictError("This batch has ended or is stopping.", nil)
		case body.Note != nil && parked.Kind != "judge":
			return network.NewUnprocessableError("a note is kept for judge picks only", nil)
		}
		seen := map[string]bool{}
		for _, id := range want.TakeIDs {
			if seen[id] || !slices.Contains(parked.Candidates, id) || len(want.TakeIDs) > parked.Keep {
				return network.NewUnprocessableError(fmt.Sprintf("pick 1 to %d of the candidates", parked.Keep), nil)
			}
			seen[id] = true
		}
		picks[key] = want
		var agreed *bool // a judge: the pick equals its suggestion (reject all with none suggested: true)
		if parked.Kind == "judge" {
			same := sameSet(want.TakeIDs, parked.Suggested)
			agreed = &same
		}
		cells := markGateCell(row.Cells, parked.Gate, parked.Visit, want, agreed)
		if _, err := tx.Exec(ctx, `UPDATE batch_rows SET status = 'running', parked = NULL, picks = $3, cells = $4
			WHERE batch_id = $1 AND n = $2`, batchID, n, picks, cells); err != nil {
			return err
		}
		command := message.Pick{BatchID: batchID, Row: n, Attempt: row.Attempt, Gate: parked.Gate, Visit: parked.Visit,
			TakeIDs: want.TakeIDs, Agreed: agreed, Note: body.Note}
		if want.Reject {
			command.Reject = &want.Reject
		}
		if agreed != nil && parked.DecisionID != "" {
			if _, err := tx.Exec(ctx, `UPDATE decisions SET user_pick = $2, agreed = $3 WHERE id = $1`, parked.DecisionID,
				nonNil(want.TakeIDs), *agreed); err != nil {
				return err
			}
		}
		if err := s.command(ctx, tx, batchID, caller.ID, command); err != nil {
			return err
		}
		if err := batchChanged(ctx, tx, batchID, n); err != nil {
			return err
		}
		out, err = s.readRow(ctx, tx, batchID, n)
		return err
	})
	return out, err
}

// samePick reports whether two answers are one pick.
func samePick(a, b pickRecord) bool {
	return a.Reject == b.Reject && slices.Equal(a.TakeIDs, b.TakeIDs)
}

// latestPick is the highest visit of gate picked in attempt, 0 for none.
func latestPick(picks map[string]pickRecord, attempt int, gate string) int {
	latest := 0
	for visit := 1; visit <= MaxPipelineRuns; visit++ {
		if _, ok := picks[pickKey(attempt, gate, visit)]; ok {
			latest = visit
		}
	}
	return latest
}

// sameSet reports whether two lists hold the same ids.
func sameSet(a, b []string) bool {
	x, y := slices.Clone(a), slices.Clone(b)
	slices.Sort(x)
	slices.Sort(y)
	return slices.Equal(slices.Compact(x), slices.Compact(y))
}

// markGateCell records a pick on the gate's cell attempt of that visit (picked or rejected, the picked takes); at a
// judge (agreed set) also on its judged summary: how many were kept and whether the user agreed.
func markGateCell(cells json.RawMessage, gate string, visit int, pick pickRecord, agreed *bool) json.RawMessage {
	var list []map[string]any
	if json.Unmarshal(cells, &list) != nil {
		return cells
	}
	for _, cell := range list {
		attempts, _ := cell["attempts"].([]any)
		for _, raw := range attempts {
			attempt, _ := raw.(map[string]any)
			if cell["node"] == gate && attempt["visit"] == float64(visit) {
				attempt["status"], attempt["take_ids"] = "picked", pick.TakeIDs
				if pick.Reject {
					attempt["status"], attempt["take_ids"] = "rejected", []string{}
				}
				if judged, ok := attempt["judged"].(map[string]any); ok && agreed != nil {
					judged["agreed"], judged["picked"] = *agreed, len(pick.TakeIDs)
				}
			}
		}
	}
	out, _ := json.Marshal(list)
	return out
}

// ------------------------------------------------------------------ the execution view

// parseAttempt reads ?attempt= (absent: 0, the current one); anything but a whole number ≥ 1 is a 422.
func parseAttempt(text string) (int, error) {
	if text == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(text)
	if err != nil || n < 1 {
		return 0, network.NewUnprocessableError("attempt must be a whole number from 1", nil)
	}
	return n, nil
}

// rowAttempt reads a pipeline batch's row and the attempt asked for (0: the current one); a workflow batch, or an
// attempt the row never had, is errNotFound.
func (s *Service) rowAttempt(ctx context.Context, tx pgx.Tx, batchID string, n, attempt int) (storedPipeline, dto.Batch, model.BatchRow, map[string]dto.RetriedFrom, int, error) {
	var row model.BatchRow
	var retried map[string]dto.RetriedFrom
	batch, err := getBatch(ctx, tx, batchID, false)
	if err != nil {
		return storedPipeline{}, batch, row, nil, 0, err
	}
	stored, isPipeline, err := readStoredPipeline(ctx, tx, batchID)
	if err != nil || !isPipeline {
		return stored, batch, row, nil, 0, cmpErr(err, errNotFound)
	}
	row, err = one[model.BatchRow](ctx, tx, model.BatchRowFrom+`WHERE br.batch_id = $1 AND br.n = $2`, batchID, n)
	if err != nil {
		return stored, batch, row, nil, 0, err
	}
	var raw []byte
	if err := tx.QueryRow(ctx, `SELECT retried_from FROM batch_rows WHERE batch_id = $1 AND n = $2`, batchID, n).Scan(&raw); err != nil {
		return stored, batch, row, nil, 0, err
	}
	_ = json.Unmarshal(raw, &retried)
	if attempt == 0 {
		attempt = row.Attempt
	}
	if attempt > row.Attempt {
		return stored, batch, row, nil, 0, errNotFound
	}
	return stored, batch, row, retried, attempt, nil
}

// cmpErr is err, or fallback when err is nil.
func cmpErr(err, fallback error) error {
	if err != nil {
		return err
	}
	return fallback
}

// stepStatus is a node's status after one of its steps, as the cells name it.
func stepStatus(step model.BatchStep) string {
	var edge dto.ExecutionEdge
	_ = json.Unmarshal(step.Edge, &edge)
	switch {
	case step.Error != nil:
		return "failed"
	case step.Part == "gate" && step.Status == "current":
		return "waiting"
	case step.Part == "route" && edge.Via == "pass":
		return "picked"
	case step.Part == "route" && edge.Via == "next": // a decide's route
		return "succeeded"
	case step.Part == "route":
		return "rejected"
	case step.Part == "submit" || (step.Part == "wait" && step.Status == "current"):
		return "running"
	}
	return "succeeded"
}

// RowExecution is the execution view of one row attempt, built from its stored steps.
func (s *Service) RowExecution(ctx context.Context, caller common.Caller, batchID, rowText, attemptText string) (dto.RowExecution, error) {
	n, err := parseRow(batchID, rowText)
	if err != nil {
		return dto.RowExecution{}, err
	}
	asked, err := parseAttempt(attemptText)
	if err != nil {
		return dto.RowExecution{}, err
	}
	var out dto.RowExecution
	err = s.tx(ctx, caller, func(tx pgx.Tx) error {
		stored, batch, row, retried, attempt, err := s.rowAttempt(ctx, tx, batchID, n, asked)
		if err != nil {
			return err
		}
		steps, err := collect[model.BatchStep](ctx, tx, `SELECT `+model.BatchStepCols+` FROM batch_steps
			WHERE batch_id = $1 AND n = $2 AND attempt = $3 ORDER BY step`, batchID, n, attempt)
		if err != nil {
			return err
		}
		out = dto.RowExecution{BatchID: batchID, Row: n, Attempt: attempt, Attempts: row.Attempt, Status: row.Status,
			Pipeline: stored.Doc, Steps: []dto.ExecutionStep{}, Nodes: map[string]dto.ExecutionNode{},
			Edges: []dto.ExecutionEdgeCount{}, Loops: map[string]int{}}
		if from, ok := retried[strconv.Itoa(attempt)]; ok {
			out.RetriedFrom = &from
		}
		// the thread holds this attempt's checkpoints while the batch has not ended and every later attempt forked
		// from a step (a plain retry deletes the thread)
		threads := !ended(batch.Status)
		for later := attempt + 1; later <= row.Attempt; later++ {
			if _, forked := retried[strconv.Itoa(later)]; !forked {
				threads = false
			}
		}
		var decisionIDs []string
		for _, step := range steps {
			if step.DecisionID != nil {
				decisionIDs = append(decisionIDs, *step.DecisionID)
			}
		}
		decisions, err := s.readDecisions(ctx, tx, decisionIDs)
		if err != nil {
			return err
		}
		counts := map[dto.ExecutionEdgeCount]int{}
		var order []dto.ExecutionEdgeCount
		for _, step := range steps {
			shown := dto.ExecutionStep{Step: step.Step, Node: step.Node, Part: step.Part, Visit: step.Visit, What: step.What,
				Changed: []string{}, Status: step.Status, Loops: map[string]int{}, RunIDs: nonNil(step.RunIDs),
				TakeIDs: nonNil(step.TakeIDs), Error: step.Error, At: step.At, HasState: threads && step.CheckpointID != nil,
				Branch: step.Branch, Turn: step.Turn, ToolCalls: rawOrNil(step.ToolCalls)}
			_ = json.Unmarshal(step.Changed, &shown.Changed)
			_ = json.Unmarshal(step.Loops, &shown.Loops)
			_ = json.Unmarshal(step.Memory, &shown.Memory) // null leaves it out
			if step.DecisionID != nil {
				if d, ok := decisions[*step.DecisionID]; ok {
					shown.Decision = &d
				}
			}
			if json.Unmarshal(step.Edge, &shown.Edge) == nil && shown.Edge != nil {
				key := dto.ExecutionEdgeCount{From: step.Node, To: shown.Edge.To, Via: shown.Edge.Via}
				if counts[key]++; counts[key] == 1 {
					order = append(order, key)
				}
			}
			out.Steps = append(out.Steps, shown)
			node := out.Nodes[step.Node]
			out.Nodes[step.Node] = dto.ExecutionNode{Visits: max(node.Visits, step.Visit), Status: stepStatus(step)}
			out.Loops = shown.Loops
		}
		for _, key := range order {
			key.Count = counts[key]
			out.Edges = append(out.Edges, key)
		}
		if attempt == row.Attempt { // the current attempt: the cells know queued and the row's own status
			shown, err := s.rowsDTO(ctx, tx, []model.BatchRow{row})
			if err != nil {
				return err
			}
			cells, _ := shown[0].Cells.([]dto.BatchCell)
			for _, cell := range cells {
				if last := len(cell.Attempts) - 1; last >= 0 {
					node := out.Nodes[cell.Node]
					out.Nodes[cell.Node] = dto.ExecutionNode{Visits: max(node.Visits, cell.Attempts[last].Visit),
						Status: cell.Attempts[last].Status}
				}
			}
			if row.Status != "succeeded" && row.Status != "failed" && row.Status != "cancelled" {
				out.Current = row.Node
			}
		} else if last := len(steps) - 1; last >= 0 {
			out.Status = map[bool]string{true: "failed", false: "cancelled"}[steps[last].Error != nil]
			if steps[last].Part == "output" {
				out.Status = "succeeded"
			}
		} else {
			out.Status = "cancelled"
		}
		for _, id := range slices.Sorted(maps.Keys(out.Nodes)) {
			if _, known := stored.Doc.Nodes[id]; !known {
				delete(out.Nodes, id)
			}
		}
		return nil
	})
	return out, err
}

// errStateGone is the state endpoint's 404 once pipelines no longer has the row's threads.
var errStateGone = network.NewNotFoundError("this row's state is gone; the batch has finished", nil)

// RowStepState is a row's state at the checkpoint after one step, read from pipelines for the owner.
func (s *Service) RowStepState(ctx context.Context, caller common.Caller, batchID, rowText, stepText, attemptText string) (dto.RowState, error) {
	n, err := parseRow(batchID, rowText)
	if err != nil {
		return dto.RowState{}, err
	}
	step, err := strconv.Atoi(stepText)
	if err != nil || step < 1 || strconv.Itoa(step) != stepText {
		return dto.RowState{}, errNotFound
	}
	asked, err := parseAttempt(attemptText)
	if err != nil {
		return dto.RowState{}, err
	}
	out := dto.RowState{Row: n, Step: step}
	var checkpoint *string
	err = s.tx(ctx, caller, func(tx pgx.Tx) error {
		_, batch, _, _, attempt, err := s.rowAttempt(ctx, tx, batchID, n, asked)
		if err != nil {
			return err
		}
		out.Attempt = attempt
		if err := tx.QueryRow(ctx, `SELECT node, checkpoint_id FROM batch_steps WHERE batch_id = $1 AND n = $2
			AND attempt = $3 AND step = $4`, batchID, n, attempt, step).Scan(&out.Node, &checkpoint); errors.Is(err, pgx.ErrNoRows) {
			return errNotFound
		} else if err != nil {
			return err
		}
		switch {
		case checkpoint == nil:
			return errNotFound
		case ended(batch.Status):
			return errStateGone
		}
		return nil
	})
	if err != nil {
		return out, err
	}
	unavailable := network.NewServiceUnavailableError("the pipeline service did not answer; try again", nil)
	if s.stateGet == nil {
		return out, unavailable
	}
	reply, err := s.stateGet(ctx, message.StateGet{OwnerID: caller.ID, BatchID: batchID, Row: n, CheckpointID: *checkpoint})
	switch {
	case err != nil || reply.Code == "internal" || (reply.Code == "" && reply.State == nil):
		return out, unavailable
	case reply.Code != "":
		return out, errStateGone
	}
	out.State = reply.State
	return out, nil
}
