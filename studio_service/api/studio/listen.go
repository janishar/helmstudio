package studio

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"log"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/helmstudio/helmgo/network"
	"github.com/helmstudio/helmgo/utility"
	"github.com/helmstudio/studio_service/api/studio/dto"
	"github.com/helmstudio/studio_service/api/studio/message"
	"github.com/helmstudio/studio_service/api/studio/model"
	"github.com/helmstudio/studio_service/common"
	"github.com/helmstudio/studio_service/registry"
)

// listenNode is the node a listen workflow's run is given its take and question on (API 1.107, contracts/README.md
// The listen tool): its kind ends in /listen (omni/listen, the fake echo/listen).
func listenNode(workflow registry.Workflow) string {
	for _, id := range slices.Sorted(maps.Keys(workflow.Nodes)) {
		if strings.HasSuffix(workflow.Nodes[id].Kind, "/listen") {
			return id
		}
	}
	return ""
}

// listenWorkflow is the listen workflow that can run now: Qwen3-Omni's (omni-listen) first, else any other (the fake).
func (s *Service) listenWorkflow() (registry.Workflow, bool) {
	templates, now := s.registry.Workflows(), time.Now()
	rank := func(w registry.Workflow) int { return map[bool]int{true: 0, false: 1}[w.ID == "omni-listen"] }
	slices.SortFunc(templates, func(a, b registry.Workflow) int {
		return cmp.Or(cmp.Compare(rank(a), rank(b)), strings.Compare(a.ID, b.ID))
	})
	for _, workflow := range templates {
		if listenNode(workflow) == "" || !registry.IsText(workflow.Nodes) {
			continue
		}
		graph := map[string]registry.Node{}
		for id, node := range workflow.Nodes {
			node.Params = merged(workflow.NodeSchemas[node.Kind], node.Params)
			graph[id] = node
		}
		nodes, _ := json.Marshal(graph)
		if _, _, reason := eligible(s.registry, s.registry.Hosts(), model.Run{WorkflowID: workflow.ID, Env: workflow.Env, Nodes: nodes}, now); reason == "" {
			return workflow, true
		}
	}
	return registry.Workflow{}, false
}

// Listen is POST /takes/{id}/listen (API 1.107) and the listen tool: one interactive run of the listen workflow on the
// caller's video or audio take, in the take's session (else the caller's newest); 202 with the run. Its answer is the
// run's `answer`, the transcript its resolved.texts `<node>.transcript`.
func (s *Service) Listen(ctx context.Context, caller common.Caller, takeID string, body *dto.ListenRequest) (dto.Run, error) {
	if !utility.IsUUID(takeID) {
		return dto.Run{}, errNotFound
	}
	if !s.registry.Ready() {
		return dto.Run{}, network.NewServiceUnavailableError("studio is starting; try again", nil)
	}
	var out dto.Run
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		var kind string
		var sessionID *string
		if err := tx.QueryRow(ctx, `SELECT kind, session_id::text FROM takes WHERE id = $1 AND owner_id = $2
			AND deleted_at IS NULL AND ready`, takeID, caller.ID).Scan(&kind, &sessionID); errors.Is(err, pgx.ErrNoRows) {
			return errNotFound
		} else if err != nil {
			return err
		}
		if kind != "video" && kind != "audio" {
			return network.NewUnprocessableError("take "+takeID+" is "+kind+"; listen hears video and audio takes (look_at sees images)", nil)
		}
		workflow, ok := s.listenWorkflow()
		if !ok {
			return network.NewConflictError("no listen workflow can run now: download Qwen3-Omni or start its machine", nil)
		}
		if sessionID == nil {
			if err := tx.QueryRow(ctx, `SELECT id::text FROM sessions WHERE owner_id = $1 ORDER BY updated_at DESC LIMIT 1`,
				caller.ID).Scan(&sessionID); errors.Is(err, pgx.ErrNoRows) {
				return network.NewConflictError("open Create once first: listening needs a session", nil)
			} else if err != nil {
				return err
			}
		}
		node := listenNode(workflow)
		transcript := body.Transcript == nil || *body.Transcript
		inputs := map[string]any{node + ".source": map[string]any{"take_id": takeID},
			node + ".question": strings.TrimSpace(body.Question), node + ".transcript": transcript}
		var err error
		out, err = s.createRun(ctx, tx, caller, *sessionID, &dto.RunRequest{WorkflowID: workflow.ID, Inputs: inputs, Ask: true})
		return err
	})
	if err == nil {
		err = s.placeQueued(ctx, &out)
	}
	return out, err
}

// listenTool is the Assistant's listen: Listen as the thread's owner, answered as look_at answers (the run to await).
func (s *Service) listenTool(ctx context.Context, caller common.Caller, raw json.RawMessage) message.ToolsReply {
	var args struct {
		TakeID     string `json:"take_id"`
		Question   string `json:"question"`
		Transcript *bool  `json:"transcript"`
	}
	if err := strictArgs(raw, &args); err != nil {
		return toolErr("invalid", "args: %v", err)
	}
	body := &dto.ListenRequest{Question: args.Question, Transcript: args.Transcript}
	if detail := body.Validate(); detail != "" {
		return toolErr("invalid", "args: %s", detail)
	} else if !utility.IsUUID(args.TakeID) {
		return toolErr("invalid", "args: take_id must be a uuid")
	}
	run, err := s.Listen(ctx, caller, args.TakeID, body)
	var refused *network.APIError
	switch {
	case errors.As(err, &refused) && refused.Code < 500:
		code := map[int]string{404: "not_found", 409: "conflict"}[refused.Code]
		return toolErr(cmp.Or(code, "invalid"), "%s", refused.Message)
	case err != nil:
		log.Printf("studio.tools listen: %v", err)
		return toolErr("internal", "internal error")
	}
	return message.ToolsReply{Tool: "listen", Result: map[string]any{"run_id": run.ID, "session_id": run.SessionID,
		"workflow_id": run.WorkflowID, "host": run.Host, "position": run.Position}}
}
