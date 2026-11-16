package studio

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/helmstudio/helmgo/utility"

	"github.com/helmstudio/studio_service/api/studio/dto"
	"github.com/helmstudio/studio_service/common"
)

// Decisions (contracts/README.md "Decisions", plan 16.7): pipelines records each judge, write and decide visit and
// publishes it on evt.pipeline.step; the batch projector keeps it in decisions (storeDecision), on the batch row and,
// for a judge, on its candidate takes. Reads are the owner's only (row-level security): another user's batch, row or
// take is a 404.

// decisionRow is a decisions row as read.
type decisionRow struct {
	ID            string
	BatchID       string
	Row           int
	OwnerID       string
	Data          json.RawMessage
	WorkflowTitle *string
	UserPick      json.RawMessage
	Agreed        *bool
	CreatedAt     time.Time
}

// decisionCols are the columns decisionRow scans, in order.
const decisionCols = `id, batch_id::text, n, owner_id::text, data, workflow_title, user_pick, agreed, created_at`

// decisionIn is defs decision as pipelines sent it.
type decisionIn struct {
	ID           string          `json:"id"`
	Node         string          `json:"node"`
	Kind         string          `json:"kind"`
	Visit        int             `json:"visit"`
	Attempt      int             `json:"attempt"`
	Replayed     bool            `json:"replayed"`
	ReplayedFrom *string         `json:"replayed_from"`
	RunIDs       []string        `json:"run_ids"`
	Workflow     string          `json:"workflow"`
	Model        json.RawMessage `json:"model"`
	Template     string          `json:"template"`
	Prompt       string          `json:"prompt"`
	Seen         []struct {
		TakeID string `json:"take_id"`
		Frame  *int   `json:"frame"`
		Key    string `json:"key"`
		Sha256 string `json:"sha256"`
		Width  int    `json:"width"`
		Height int    `json:"height"`
	} `json:"seen"`
	Answers    []json.RawMessage `json:"answers"`
	TokensIn   int               `json:"tokens_in"`
	TokensOut  int               `json:"tokens_out"`
	Seconds    float64           `json:"seconds"`
	MemoryRead []string          `json:"memory_read"`
	Error      *string           `json:"error"`
	Judge      *judgeIn          `json:"judge"`
	Write      json.RawMessage   `json:"write"`
	Decide     json.RawMessage   `json:"decide"`
	// API 1.15: where the model ran, whether it may be replayed, transport failures, an agent visit's turns
	Source          json.RawMessage `json:"source"`
	Replayable      *bool           `json:"replayable"`
	NetworkFailures *int            `json:"network_failures"`
	Agent           json.RawMessage `json:"agent"`
}

// judgeIn is a judge decision's result as pipelines sent it: one check per criterion (must lines, then nice).
type judgeIn struct {
	Criteria dto.PipelineCriteria `json:"criteria"`
	Keep     int                  `json:"keep"`
	Mode     string               `json:"mode"`
	Takes    []struct {
		TakeID string `json:"take_id"`
		Checks []bool `json:"checks"`
		Reason string `json:"reason"`
		Passes bool   `json:"passes"`
		Rank   int    `json:"rank"`
	} `json:"takes"`
	Picked      []string `json:"picked"`
	Verdict     string   `json:"verdict"`
	FirstFailed *string  `json:"first_failed"`
}

// checks names a take's answers by criterion and level.
func (j judgeIn) checks(answers []bool) []dto.JudgeCheck {
	out := []dto.JudgeCheck{}
	for i, line := range j.Criteria.Lines() {
		if i < len(answers) {
			level := map[bool]string{true: "must", false: "nice"}[i < len(j.Criteria.Must)]
			out = append(out, dto.JudgeCheck{Criterion: line, Level: level, Pass: answers[i]})
		}
	}
	return out
}

// decisionsDTO maps stored decisions: the thumbnails the model saw signed (the owner's keys, of takes not deleted: a
// thumbnail is a copy of its take), each candidate's own thumbnail (null once deleted), the user's pick at a Suggest
// judge.
func (s *Service) decisionsDTO(ctx context.Context, tx pgx.Tx, rows []decisionRow) ([]dto.Decision, error) {
	parsed := make([]decisionIn, len(rows))
	var takeIDs []string
	for i, row := range rows {
		_ = json.Unmarshal(row.Data, &parsed[i])
		if parsed[i].Judge != nil {
			for _, t := range parsed[i].Judge.Takes {
				takeIDs = append(takeIDs, t.TakeID)
			}
		}
		for _, seen := range parsed[i].Seen {
			takeIDs = append(takeIDs, seen.TakeID)
		}
	}
	keys, err := takeKeys(ctx, tx, takeIDs)
	if err != nil {
		return nil, err
	}
	out := make([]dto.Decision, len(rows))
	for i, row := range rows {
		d := parsed[i]
		shown := dto.Decision{ID: row.ID, BatchID: row.BatchID, Row: row.Row, Node: d.Node, Kind: d.Kind, Visit: d.Visit,
			Attempt: d.Attempt, Replayed: d.Replayed, ReplayedFrom: d.ReplayedFrom, RunIDs: nonNil(d.RunIDs),
			Workflow: d.Workflow, WorkflowTitle: deref(row.WorkflowTitle), Model: rawOrNil(d.Model), Template: d.Template,
			Prompt: d.Prompt, Seen: []dto.DecisionSeen{}, Answers: d.Answers, TokensIn: d.TokensIn, TokensOut: d.TokensOut,
			Seconds: d.Seconds, MemoryRead: nonNil(d.MemoryRead), Error: d.Error, At: row.CreatedAt,
			Write: rawOrNil(d.Write), Decide: rawOrNil(d.Decide), Source: rawOrNil(d.Source), Agent: rawOrNil(d.Agent),
			NetworkFailures: d.NetworkFailures}
		// a remote answer is never reproducible, whatever was sent (plan 16.7)
		var source struct {
			Kind string `json:"kind"`
		}
		_ = json.Unmarshal(d.Source, &source)
		replayable := source.Kind != "remote" && (d.Replayable == nil || *d.Replayable)
		shown.Replayable = &replayable
		if shown.Model == nil {
			shown.Model = json.RawMessage("null")
		}
		if shown.Answers == nil {
			shown.Answers = []json.RawMessage{}
		}
		for _, seen := range d.Seen {
			if _, live := keys[seen.TakeID]; live && common.Owns(row.OwnerID, seen.Key) {
				shown.Seen = append(shown.Seen, dto.DecisionSeen{TakeID: seen.TakeID, Frame: seen.Frame, Sha256: seen.Sha256,
					Width: seen.Width, Height: seen.Height, ThumbURL: s.signer.Sign(seen.Key)})
			}
		}
		if j := d.Judge; j != nil {
			judge := &dto.DecisionJudge{Criteria: j.Criteria, Keep: j.Keep, Mode: j.Mode, Takes: []dto.DecisionJudgeTake{},
				Picked: nonNil(j.Picked), Verdict: j.Verdict, FirstFailed: j.FirstFailed, Agreed: row.Agreed}
			var pick []string
			if json.Unmarshal(row.UserPick, &pick) == nil && row.UserPick != nil && string(row.UserPick) != "null" {
				pick = nonNil(pick)
				judge.UserPick = &pick
			}
			for _, t := range j.Takes {
				take := dto.DecisionJudgeTake{TakeID: t.TakeID, Checks: j.checks(t.Checks), Reason: t.Reason, Passes: t.Passes,
					Rank: t.Rank}
				if key, ok := keys[t.TakeID]; ok {
					take.ThumbURL = s.signer.SignPtr(&key)
				}
				judge.Takes = append(judge.Takes, take)
			}
			shown.Judge = judge
		}
		out[i] = shown
	}
	return out, nil
}

// readDecisions reads decisions by id (the execution view's steps, a parked row's).
func (s *Service) readDecisions(ctx context.Context, tx pgx.Tx, ids []string) (map[string]dto.Decision, error) {
	out := map[string]dto.Decision{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := collect[decisionRow](ctx, tx, `SELECT `+decisionCols+` FROM decisions WHERE id = ANY($1)`, ids)
	if err != nil {
		return nil, err
	}
	shown, err := s.decisionsDTO(ctx, tx, rows)
	for _, d := range shown {
		out[d.ID] = d
	}
	return out, err
}

// RowDecisions lists every decision of a pipeline batch's row (?attempt= one attempt), oldest first; a workflow batch,
// another user's batch or an attempt the row never had is a 404.
func (s *Service) RowDecisions(ctx context.Context, caller common.Caller, batchID, rowText, attemptText string) (dto.DecisionList, error) {
	out := dto.DecisionList{Decisions: []dto.Decision{}}
	n, err := parseRow(batchID, rowText)
	if err != nil {
		return out, err
	}
	asked, err := parseAttempt(attemptText)
	if err != nil {
		return out, err
	}
	err = s.tx(ctx, caller, func(tx pgx.Tx) error {
		if _, _, _, _, _, err := s.rowAttempt(ctx, tx, batchID, n, asked); err != nil {
			return err
		}
		rows, err := collect[decisionRow](ctx, tx, `SELECT `+decisionCols+` FROM decisions WHERE batch_id = $1 AND n = $2
			AND ($3 = 0 OR attempt = $3) ORDER BY attempt, created_at, id LIMIT 500`, batchID, n, asked)
		if err != nil {
			return err
		}
		out.Decisions, err = s.decisionsDTO(ctx, tx, rows)
		return err
	})
	return out, err
}

// TakeDecisions lists the judge decisions that weighed one of the caller's takes, newest first.
func (s *Service) TakeDecisions(ctx context.Context, caller common.Caller, takeID string) (dto.DecisionList, error) {
	out := dto.DecisionList{Decisions: []dto.Decision{}}
	if !utility.IsUUID(takeID) {
		return out, errNotFound
	}
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		var id string
		if err := tx.QueryRow(ctx, `SELECT id::text FROM takes WHERE id = $1 AND deleted_at IS NULL`, takeID).Scan(&id); errors.Is(err, pgx.ErrNoRows) {
			return errNotFound
		} else if err != nil {
			return err
		}
		rows, err := collect[decisionRow](ctx, tx, `SELECT `+decisionCols+` FROM decisions WHERE take_ids @> ARRAY[$1::uuid]
			ORDER BY created_at DESC, id LIMIT 500`, takeID)
		if err != nil {
			return err
		}
		out.Decisions, err = s.decisionsDTO(ctx, tx, rows)
		return err
	})
	return out, err
}
