package studio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/helmstudio/helmgo/network"
	"github.com/helmstudio/helmgo/utility"

	"github.com/helmstudio/studio_service/api/studio/dto"
	"github.com/helmstudio/studio_service/api/studio/message"
	"github.com/helmstudio/studio_service/common"
	"github.com/helmstudio/studio_service/registry"
)

// Judge tests (contracts/README.md "Judge test", API 1.28): the Judge panel's Test on takes. Pipelines owns the judge
// template and its checks, so studio asks it for the filled template (pipelines.judge.test prompt), makes the ask run
// itself (interactive, in the first take's session) and, once the run succeeded, asks it for the verdict (verdict),
// which it keeps on the test.

// JudgeTestAsk asks pipelines_service about a judge test (pipelines.judge.test); an error is no answer.
type JudgeTestAsk func(ctx context.Context, req message.JudgeTestRequest) (message.JudgeTestReply, error)

// SetJudgeTest sets how the service reaches pipelines for judge tests; without one they answer 503.
func (s *Service) SetJudgeTest(fn JudgeTestAsk) { s.judgeTest = fn }

// askJudgeTest sends req: no answer or internal is 503, invalid 422; an `answer` code is returned to the caller.
func (s *Service) askJudgeTest(ctx context.Context, req message.JudgeTestRequest) (message.JudgeTestReply, error) {
	unavailable := network.NewServiceUnavailableError("the pipeline service did not answer; try again", nil)
	if s.judgeTest == nil {
		return message.JudgeTestReply{}, unavailable
	}
	reply, err := s.judgeTest(ctx, req)
	switch {
	case err != nil || reply.Code == "internal":
		return reply, unavailable
	case reply.Code == "invalid":
		return reply, network.NewUnprocessableError(reply.Error, nil)
	}
	return reply, nil
}

// criteriaJSON is defs judgeCriteria: both lists, never null.
func criteriaJSON(c dto.PipelineCriteria) json.RawMessage {
	out, _ := json.Marshal(map[string][]string{"must": append([]string{}, c.Must...), "nice": append([]string{}, c.Nice...)})
	return out
}

// askNode is a workflow's ask node: the first by id whose kind ends in /ask, else "ask" (as pipelines finds it).
func askNode(workflow registry.Workflow) string {
	for _, id := range slices.Sorted(maps.Keys(workflow.Nodes)) {
		if strings.HasSuffix(workflow.Nodes[id].Kind, "/ask") {
			return id
		}
	}
	return "ask"
}

type judgeTestRow struct {
	ID         string
	PipelineID string
	Node       string
	TakeIDs    []string
	Criteria   json.RawMessage
	Keep       int
	RunID      *string
	Result     json.RawMessage
	CreatedAt  time.Time
}

const judgeTestCols = `id::text, pipeline_id::text, node, take_ids::text[], criteria, keep, run_id::text, result, created_at`

// CreateJudgeTest is POST /pipelines/{id}/judge-tests: the saved judge's ask run on the caller's takes (202).
func (s *Service) CreateJudgeTest(ctx context.Context, caller common.Caller, pipelineID string, body *dto.JudgeTestCreate) (dto.JudgeTest, error) {
	if err := s.ready(); err != nil {
		return dto.JudgeTest{}, err
	}
	unprocessable := func(format string, args ...any) error {
		return network.NewUnprocessableError(fmt.Sprintf(format, args...), nil)
	}
	var out dto.JudgeTest
	var run dto.Run
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		row, err := readPipeline(ctx, tx, pipelineID, false)
		if err != nil {
			return err
		}
		doc := pipelineDoc(row)
		node, ok := doc.Nodes[body.Node]
		if !ok || node.Kind != "judge" {
			return unprocessable("%s is not a judge of this pipeline", body.Node)
		}
		// the takes: the caller's (else 404), image or video, in the order given; the session and brief of the first
		type takeRow struct {
			ID        string
			Kind      string
			SessionID *string
			Prompt    *string
		}
		takes, err := collect[takeRow](ctx, tx, `SELECT id::text, kind, session_id::text, made_with->>'prompt' FROM takes
			WHERE id = ANY($1::uuid[]) AND owner_id = $2 AND deleted_at IS NULL AND ready`, body.TakeIDs, caller.ID)
		if err != nil {
			return err
		} else if len(takes) != len(body.TakeIDs) {
			return errNotFound
		}
		slices.SortFunc(takes, func(a, b takeRow) int {
			return slices.Index(body.TakeIDs, a.ID) - slices.Index(body.TakeIDs, b.ID)
		})
		for _, t := range takes {
			if t.Kind != "image" && t.Kind != "video" {
				return unprocessable("take %s is %s; a judge looks at images and videos", t.ID, t.Kind)
			}
		}
		// the model as at Submit: only a local ask workflow (a judge sends thumbnails)
		_, _, info, err := s.pipelineProblemsNow(ctx, tx, caller.ID, doc)
		if err != nil {
			return err
		}
		wid := info.Models[body.Node]
		if wid == "" || strings.Contains(wid, ":") {
			return unprocessable("%s: model %s is not an ask workflow you can run here", body.Node, node.ModelOr())
		}
		workflow, _, err := s.runnable(ctx, tx, wid)
		if err != nil {
			return err
		}
		stored := storedPipeline{Doc: doc, Workflows: map[string]registry.Workflow{wid: workflow}, Models: info.Models, Near: info.Near}
		if step, ok := info.Near[body.Node]; ok {
			if stored.Workflows[doc.Nodes[step].Workflow], _, err = s.runnable(ctx, tx, doc.Nodes[step].Workflow); err != nil {
				return err
			}
		}
		// the session: the first take's, else the caller's newest
		sessionID := takes[0].SessionID
		if sessionID == nil {
			if err := tx.QueryRow(ctx, `SELECT id::text FROM sessions WHERE owner_id = $1 ORDER BY updated_at DESC LIMIT 1`,
				caller.ID).Scan(&sessionID); errors.Is(err, pgx.ErrNoRows) {
				return unprocessable("open Create once first: a test run needs a session")
			} else if err != nil {
				return err
			}
		}
		criteria, keep := dto.PipelineCriteria{}, 1
		if node.Criteria != nil {
			criteria = *node.Criteria
		}
		if node.Keep != nil {
			keep = *node.Keep
		}
		req := message.JudgeTestRequest{Op: "prompt", OwnerID: caller.ID, PipelineID: row.ID, Node: body.Node,
			Criteria: criteriaJSON(criteria), N: len(takes), Brief: takes[0].Prompt}
		if req.Brief == nil {
			req.Brief = new(string)
		}
		if m, ok := doc.Nodes[deref(node.Memory)]; ok && m.Kind == "memory" && m.Op == "read" {
			req.Memory = &message.JudgeTestMemory{Key: m.Key, Limit: 8}
			if m.Limit != nil {
				req.Memory.Limit = *m.Limit
			}
		}
		prompted, err := s.askJudgeTest(ctx, req)
		if err != nil {
			return err
		} else if prompted.Code != "" {
			return unprocessable("%s", prompted.Error)
		}
		ask := askNode(workflow)
		inputs := map[string]any{ask + ".system": prompted.System, ask + ".prompt": prompted.Prompt,
			ask + ".schema": prompted.Schema, ask + ".max_tokens": float64(prompted.MaxTokens)} // numbers as JSON decodes them
		media, thumbs := make([]any, len(takes)), 0
		for i, t := range takes {
			media[i] = map[string]any{"take": map[string]any{"take_id": t.ID}}
			thumbs++ // a video is one grid // as a batch's ask run counts them
		}
		inputs[ask+".media"] = media
		for k, v := range stored.askModel(body.Node) { // a family ask at the judged step's variant and precision
			inputs[k] = v
		}
		if run, err = s.createRun(ctx, tx, caller, *sessionID, &dto.RunRequest{WorkflowID: wid, Inputs: inputs, Ask: true}); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE runs SET thumbs = $2 WHERE id = $1`, run.ID, thumbs); err != nil {
			return err
		}
		test, err := one[judgeTestRow](ctx, tx, `INSERT INTO judge_tests (id, owner_id, pipeline_id, node, take_ids, criteria, keep, run_id)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING `+judgeTestCols, utility.NewID(), caller.ID, row.ID, body.Node,
			body.TakeIDs, criteriaJSON(criteria), keep, run.ID)
		if err != nil {
			return err
		}
		out = judgeTestDTO(test, run.Status, run.Error)
		return nil
	})
	if err == nil {
		err = s.placeQueued(ctx, &run)
	}
	return out, err
}

// GetJudgeTest is GET /judge-tests/{id}: the run's status and, once it succeeded, the verdict (asked once, then kept).
func (s *Service) GetJudgeTest(ctx context.Context, caller common.Caller, id string) (dto.JudgeTest, error) {
	if !utility.IsUUID(id) {
		return dto.JudgeTest{}, errNotFound
	}
	var test judgeTestRow
	var status string
	var failure *string
	var answer json.RawMessage
	err := s.tx(ctx, caller, func(tx pgx.Tx) (err error) {
		if test, err = one[judgeTestRow](ctx, tx, `SELECT `+judgeTestCols+` FROM judge_tests WHERE id = $1`, id); err != nil {
			return err
		}
		status = "failed"
		if test.RunID == nil {
			failure = new(string)
			*failure = "its run was deleted"
			return nil
		}
		return tx.QueryRow(ctx, `SELECT status, error, answer FROM runs WHERE id = $1`, *test.RunID).Scan(&status, &failure, &answer)
	})
	if err != nil || status != "succeeded" || test.Result != nil {
		return judgeTestDTO(test, status, failure), err
	}
	var parsed message.Answer
	text := ""
	if json.Unmarshal(answer, &parsed) == nil && len(parsed.Tries) > 0 {
		text = parsed.Tries[len(parsed.Tries)-1].Text
	}
	reply, err := s.askJudgeTest(ctx, message.JudgeTestRequest{Op: "verdict", Criteria: test.Criteria, Keep: test.Keep,
		TakeIDs: test.TakeIDs, Text: &text})
	if err != nil {
		return dto.JudgeTest{}, err
	}
	result, _ := json.Marshal(reply) // {error, code: answer} when the answer failed the checks
	if reply.Code == "" {
		result, _ = json.Marshal(map[string]any{"verdict": reply.Verdict, "first_failed": reply.FirstFailed,
			"picked": reply.Picked, "takes": reply.Takes})
	}
	err = s.tx(ctx, caller, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE judge_tests SET result = $2 WHERE id = $1`, id, result)
		return err
	})
	test.Result = result
	return judgeTestDTO(test, status, failure), err
}

// judgeTestDTO maps a stored test with its run's status: the verdict once kept; an answer that failed the checks
// makes the test failed with that error.
func judgeTestDTO(t judgeTestRow, status string, failure *string) dto.JudgeTest {
	out := dto.JudgeTest{ID: t.ID, PipelineID: t.PipelineID, Node: t.Node, TakeIDs: t.TakeIDs, Status: status,
		RunID: t.RunID, Error: failure, Takes: []dto.JudgeTestTake{}, CreatedAt: t.CreatedAt}
	if t.Result == nil {
		return out
	}
	var r struct {
		judgeIn
		Error string `json:"error"`
	}
	_ = json.Unmarshal(t.Result, &r)
	if r.Error != "" {
		out.Status, out.Error = "failed", &r.Error
		return out
	}
	_ = json.Unmarshal(t.Criteria, &r.Criteria)
	out.Verdict, out.FirstFailed = &r.Verdict, r.FirstFailed
	for _, take := range r.Takes {
		out.Takes = append(out.Takes, dto.JudgeTestTake{TakeID: take.TakeID, Passes: take.Passes, Rank: take.Rank,
			Picked: slices.Contains(r.Picked, take.TakeID), Reason: take.Reason, Checks: r.checks(take.Checks)})
	}
	return out
}
