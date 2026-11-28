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

// listenWorkflow is the listen workflow that can run now: Qwen3-Omni's (omni-listen) first, then the smaller
// Qwen2.5-Omni-3B's (omni-listen-3b, API 1.109), else any other (the fake); with the 3B, note says so and why (listenNote).
// None: why not, in plain words the Assistant and helm mcp pass on (listenWhy; API 1.108).
func (s *Service) listenWorkflow() (registry.Workflow, string, string) {
	templates, now := s.registry.Workflows(), time.Now()
	rank := func(w registry.Workflow) int { // omni-listen, omni-listen-3b, then the rest
		if i := slices.Index([]string{"omni-listen", smallListen}, w.ID); i >= 0 {
			return i
		}
		return 2
	}
	slices.SortFunc(templates, func(a, b registry.Workflow) int {
		return cmp.Or(cmp.Compare(rank(a), rank(b)), strings.Compare(a.ID, b.ID))
	})
	first, reasons := "", map[string]string{}
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
		_, _, reason := eligible(s.registry, s.registry.Hosts(), model.Run{WorkflowID: workflow.ID, Env: workflow.Env, Nodes: nodes}, now)
		if reason == "" {
			note := ""
			if workflow.ID == smallListen {
				note = s.listenNote(reasons["omni-listen"])
			}
			return workflow, note, ""
		}
		if first == "" {
			first = workflow.ID
		}
		reasons[workflow.ID] = reason
	}
	return registry.Workflow{}, "", s.listenWhy(first, reasons)
}

// smallListen is the smaller listen model's workflow (API 1.109): Qwen2.5-Omni-3B, variant 3b of the omni family.
const smallListen = "omni-listen-3b"

// omniUnplugged: some host lists Qwen3-Omni (omni variant q8) as on a drive that is not connected.
func (s *Service) omniUnplugged() bool {
	for _, h := range s.registry.Hosts() {
		found, _ := s.registry.Model(h.Host+"."+h.Env, "omni")
		if slices.ContainsFunc(found.Variants, func(v registry.Variant) bool { return v.ID == "q8" && v.Presence == "unplugged" }) {
			return true
		}
	}
	return false
}

// listenNote is the reply's note when the 3B answers (API 1.109): which model, and why Qwen3-Omni did not (its
// dispatch reason), for the Assistant to tell the owner.
func (s *Service) listenNote(reason string) string {
	why := "Qwen3-Omni can't run now (" + reason + ")."
	switch {
	case s.omniUnplugged():
		why = "the T9 with Qwen3-Omni is not connected."
	case strings.Contains(reason, " has model "):
		why = "Qwen3-Omni is not on this Mac."
	}
	return "Answered by the smaller listen model (Qwen2.5-Omni-3B): " + why
}

// listenWhy says in plain words why no listen workflow can run (`first` the first one's id, reasons each one's
// dispatch reason), with what to do: no runner on, neither model present (Qwen3-Omni on a drive that is not
// connected), else the reason.
func (s *Service) listenWhy(first string, reasons map[string]string) string {
	reason := reasons[first]
	switch {
	case first == "" || (strings.HasPrefix(reason, "no live host for env") && !strings.Contains(reason, " has ")):
		return "Can't listen: no runner that can listen is on; start the Mac's runner and ask again."
	case first == "omni-listen" && reasons[smallListen] != "" && !strings.Contains(reasons[smallListen], " has model "):
		return "Can't listen now: " + reasons[smallListen] + "." // the 3B is there but cannot run either (no room, …)
	case first == "omni-listen" && strings.Contains(reason, " has model "):
		if s.omniUnplugged() {
			return "Can't listen: Qwen3-Omni is on the T9 drive, which is not connected, and the smaller Qwen2.5-Omni-3B is not on this Mac; connect the T9 or download the 3B in Models, and ask again."
		}
		return "Can't listen: neither Qwen3-Omni nor the smaller Qwen2.5-Omni-3B is on this Mac; download one in Models (or connect the drive that holds it) and ask again."
	}
	return "Can't listen now: " + reason + "."
}

// Listen is POST /takes/{id}/listen (API 1.107) and the listen tool: one interactive run of the listen workflow on the
// caller's video or audio take, in the take's session (else the caller's newest); 202 with the run. Its answer is the
// run's `answer`, the transcript its resolved.texts `<node>.transcript`.
func (s *Service) Listen(ctx context.Context, caller common.Caller, takeID string, body *dto.ListenRequest) (dto.ListenRun, error) {
	if !utility.IsUUID(takeID) {
		return dto.ListenRun{}, errNotFound
	}
	if !s.registry.Ready() {
		return dto.ListenRun{}, network.NewServiceUnavailableError("studio is starting; try again", nil)
	}
	var out dto.ListenRun
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
		workflow, note, why := s.listenWorkflow()
		out.Note = note
		if why != "" {
			return network.NewConflictError(why, nil)
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
		out.Run, err = s.createRun(ctx, tx, caller, *sessionID, &dto.RunRequest{WorkflowID: workflow.ID, Inputs: inputs, Ask: true})
		return err
	})
	if err == nil {
		err = s.placeQueued(ctx, &out.Run)
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
	result := map[string]any{"run_id": run.ID, "session_id": run.SessionID, "workflow_id": run.WorkflowID,
		"host": run.Host, "position": run.Position}
	if run.Note != "" {
		result["note"] = run.Note // API 1.109: the smaller model answers
	}
	return message.ToolsReply{Tool: "listen", Result: result}
}
