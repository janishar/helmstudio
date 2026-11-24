package studio

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/helmstudio/helmgo/network"
	"github.com/helmstudio/helmgo/utility"

	"github.com/helmstudio/studio_service/api/studio/dto"
	"github.com/helmstudio/studio_service/api/studio/message"
	"github.com/helmstudio/studio_service/api/studio/model"
	"github.com/helmstudio/studio_service/common"
	"github.com/helmstudio/studio_service/gen"
	"github.com/helmstudio/studio_service/registry"
)

// The assistant's studio tools (contracts/README.md "Assistant: tools", API 1.44): tools/catalog.json's tools served
// by studio, each one or more of the API routes it names, asked in process (common.LocalEngine) as the thread's owner
// with the roles of its last input; their answers are the API's own shapes. An admin tool is checked against those
// roles first (forbidden). Idempotent on call_id (assistant_tool_calls): asking again returns the first final reply.

// catalogTool is one tool of tools/catalog.json, the fields studio reads.
type catalogTool struct {
	Surfaces []string `json:"surfaces"`
	ServedBy string   `json:"served_by"`
	Roles    []string `json:"roles"`
}

var toolCatalog = sync.OnceValue(func() map[string]catalogTool {
	var doc struct {
		Tools map[string]catalogTool `json:"tools"`
	}
	if err := json.Unmarshal(gen.ToolCatalog, &doc); err != nil {
		panic("gen/tools/catalog.json: " + err.Error())
	}
	return doc.Tools
})

// offeredTools are the tools a turn offers (contracts check.py offered_tools): surface assistant, every role it needs
// held, web_search only when on, look_at only when an ask workflow can run now; sorted.
func offeredTools(roles []string, webSearch, lookAt bool, skills ...bool) []string {
	skillOn := len(skills) > 0 && skills[0] // API 1.64: use_skill and read_skill_file only while a skill is on
	out := []string{}
	for name, t := range toolCatalog() {
		if slices.Contains(t.Surfaces, "assistant") && !slices.ContainsFunc(t.Roles, func(r string) bool { return !slices.Contains(roles, r) }) &&
			(name != "web_search" || webSearch) && (name != "look_at" || lookAt) &&
			(name != "use_skill" && name != "read_skill_file" || skillOn) {
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return out
}

// OfferedTools is offeredTools, for the contract's offer fixtures.
func OfferedTools(roles []string, webSearch, lookAt, skills bool) []string {
	return offeredTools(roles, webSearch, lookAt, skills)
}

// askWorkflow is the ask workflow look_at runs now: a template answering text whose ask node takes media (Qwen Image's
// first), on a machine that can run it now (the judge test's runnable check, as Create's list counts it).
func (s *Service) askWorkflow() (registry.Workflow, bool) {
	templates, now := s.registry.Workflows(), time.Now()
	rank := func(w registry.Workflow) int { return map[bool]int{true: 0, false: 1}[w.ID == "qwen-image-ask"] }
	slices.SortFunc(templates, func(a, b registry.Workflow) int {
		return cmp.Or(cmp.Compare(rank(a), rank(b)), strings.Compare(a.ID, b.ID))
	})
	for _, workflow := range templates {
		ask, ok := workflow.Nodes[askNode(workflow)]
		if !ok || !registry.IsText(workflow.Nodes) ||
			!slices.ContainsFunc(workflow.NodeSchemas[ask.Kind].Params, func(p registry.Param) bool { return p.Name == "media" }) {
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

// localGet asks the in-process API as caller: the 2xx body, or the tool's refusal.
func (s *Service) localCall(ctx context.Context, caller common.Caller, method, path string, body any) (json.RawMessage, *message.ToolsReply) {
	if s.local == nil {
		out := toolErr("internal", "studio is starting; try again")
		return nil, &out
	}
	var reader *bytes.Reader = bytes.NewReader(nil)
	if body != nil {
		reader = bytes.NewReader(toJSON(body))
	}
	request := httptest.NewRequestWithContext(common.WithLocalCaller(ctx, caller), method, path, reader)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	s.local.ServeHTTP(recorder, request)
	if recorder.Code < 300 {
		return recorder.Body.Bytes(), nil
	}
	var failure struct {
		Detail string `json:"detail"`
	}
	_ = json.Unmarshal(recorder.Body.Bytes(), &failure)
	code := map[int]string{http.StatusNotFound: "not_found", http.StatusUnprocessableEntity: "invalid", http.StatusForbidden: "forbidden",
		http.StatusConflict: "conflict", http.StatusBadRequest: "invalid"}[recorder.Code]
	if code == "" {
		code, failure.Detail = "internal", "internal error"
	}
	out := toolErr(code, "%s", failure.Detail)
	if out.Error == "" {
		out.Error = code
	}
	return nil, &out
}

// assistantTool answers one assistant call (req.ThreadID set).
func (s *Service) assistantTool(ctx context.Context, req *message.ToolsRequest) message.ToolsReply {
	tool, known := toolCatalog()[req.Tool]
	switch {
	case !utility.IsUUID(req.OwnerID) || !utility.IsUUID(req.ThreadID) || !assistantCallID.MatchString(req.CallID) ||
		!strings.HasPrefix(req.CallID, req.ThreadID+":") || req.BatchID != "" || req.Node != "":
		return toolErr("invalid", "owner_id, thread_id and call_id (<thread_id>:<turn>:<n>) are required")
	case !known || tool.ServedBy != "studio" || !slices.Contains(tool.Surfaces, "assistant"):
		return toolErr("invalid", "studio does not serve %s", req.Tool)
	case !s.registry.Ready():
		return toolErr("internal", "studio is starting; try again")
	}
	var owner string
	var roles []string
	var first []byte
	err := common.System(ctx, s.db, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT owner_id::text, roles FROM assistant_threads WHERE id = $1`, req.ThreadID).Scan(&owner,
			&roles); err != nil {
			return err
		}
		err := tx.QueryRow(ctx, `SELECT reply FROM assistant_tool_calls WHERE call_id = $1 AND thread_id = $2`, req.CallID,
			req.ThreadID).Scan(&first)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows) || (err == nil && owner != req.OwnerID):
		return toolErr("not_found", "not found")
	case err != nil:
		log.Printf("studio.tools %s: %v", req.CallID, err)
		return toolErr("internal", "internal error")
	}
	var out message.ToolsReply
	if first != nil && json.Unmarshal(first, &out) == nil {
		return out // asked again: the first reply, nothing done twice
	}
	caller := common.Caller{ID: owner, Roles: roles}
	switch {
	case slices.ContainsFunc(tool.Roles, func(r string) bool { return !slices.Contains(roles, r) }):
		out = toolErr("forbidden", "%s needs the %s role", req.Tool, strings.Join(tool.Roles, " and "))
	case req.Tool == "look_at":
		return s.lookAt(ctx, caller, req) // it keeps its reply with its run
	default:
		out = s.readTool(ctx, caller, req.Tool, req.Args)
	}
	if out.Code == "internal" {
		return out
	}
	if out.Code == "" {
		out.Tool = req.Tool
	}
	{
		if err := common.System(ctx, s.db, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `INSERT INTO assistant_tool_calls (call_id, thread_id, owner_id, tool, reply)
				VALUES ($1, $2, $3, $4, $5) ON CONFLICT (call_id) DO NOTHING`, req.CallID, req.ThreadID, owner, req.Tool, toJSON(out))
			return err
		}); err != nil {
			log.Printf("studio.tools %s: %v", req.CallID, err)
		}
	}
	return out
}

// toolArgs are every read tool's args (tools/catalog.json input); each tool takes only its own.
type toolArgs struct {
	SessionID  *string `json:"session_id"`
	Limit      *int    `json:"limit"`
	WorkflowID *string `json:"workflow_id"`
	RunID      *string `json:"run_id"`
	Inputs     any     `json:"inputs"`
	Host       *string `json:"host"`
	Q          *string `json:"q"`
	Kind       *string `json:"kind"`
	Starred    *bool   `json:"starred"`
	BatchID    *string `json:"batch_id"`
	Order      *string `json:"order"`
	TakeID     *string `json:"take_id"`
	PipelineID *string `json:"pipeline_id"`
	Row        *int    `json:"row"`
	TimelineID *string `json:"timeline_id"`
	Revision   *int    `json:"revision"` // timeline_revisions (API 1.49)
	Before     *int    `json:"before"`
	Skill      *string `json:"skill"` // use_skill, read_skill_file (API 1.64)
	File       *string `json:"file"`
}

// toolInputs are the args each read tool takes, and which it needs.
var toolInputs = map[string][2][]string{
	"sessions":           {{"session_id", "limit"}},
	"list_workflows":     {},
	"get_workflow":       {{"workflow_id"}, {"workflow_id"}},
	"get_run":            {{"run_id"}, {"run_id"}},
	"plan_run":           {{"workflow_id", "inputs", "host"}, {"workflow_id"}},
	"models":             {},
	"hosts":              {},
	"search_takes":       {{"q", "kind", "starred", "session_id", "batch_id", "order", "limit"}},
	"get_take":           {{"take_id"}, {"take_id"}},
	"list_library":       {{"kind", "pipeline_id"}},
	"pipelines":          {{"pipeline_id"}},
	"batch_status":       {{"batch_id", "row", "limit"}},
	"list_timelines":     {},
	"show_timeline":      {{"timeline_id"}, {"timeline_id"}},
	"timeline_revisions": {{"timeline_id", "revision", "before", "limit"}, {"timeline_id"}},
	"custom_nodes":       {},
	"model_sources":      {},
	"model_jobs":         {},
	"use_skill":          {{"skill"}, {"skill"}},                 // API 1.64
	"read_skill_file":    {{"skill", "file"}, {"skill", "file"}}, // API 1.64
}

// readTool runs one read tool through its routes.
func (s *Service) readTool(ctx context.Context, caller common.Caller, name string, raw json.RawMessage) message.ToolsReply {
	var top map[string]json.RawMessage
	var args toolArgs
	if len(raw) == 0 {
		raw = []byte(`{}`)
	}
	if err := json.Unmarshal(raw, &top); err != nil || top == nil || strictArgs(raw, &args) != nil {
		return toolErr("invalid", "args must be an object of the tool's input")
	}
	takes := toolInputs[name]
	for key := range top {
		if !slices.Contains(takes[0], key) {
			return toolErr("invalid", "args: %s does not take %s", name, key)
		}
	}
	for _, key := range takes[1] {
		if _, ok := top[key]; !ok {
			return toolErr("invalid", "args: %s is required", key)
		}
	}
	uuids := map[string]*string{"session_id": args.SessionID, "run_id": args.RunID, "batch_id": args.BatchID,
		"take_id": args.TakeID, "pipeline_id": args.PipelineID, "timeline_id": args.TimelineID}
	for key, value := range uuids {
		if value != nil && !utility.IsUUID(*value) {
			return toolErr("invalid", "args: %s must be a uuid", key)
		}
	}
	limit := 10
	switch {
	case args.Limit != nil && (*args.Limit < 1 || *args.Limit > MaxToolResults):
		return toolErr("invalid", "args: limit must be 1 to 20")
	case args.Limit != nil:
		limit = *args.Limit
	case args.Row != nil && (*args.Row < 1 || *args.Row > MaxRows || args.BatchID == nil):
		return toolErr("invalid", "args: row must be 1 to %d, with batch_id", MaxRows)
	case args.WorkflowID != nil && (len(*args.WorkflowID) < 1 || len(*args.WorkflowID) > 80):
		return toolErr("invalid", "args: workflow_id must be 1-80 characters")
	}
	result := map[string]any{}
	var refused *message.ToolsReply
	get := func(key, path string) json.RawMessage {
		if refused != nil {
			return nil
		}
		var body json.RawMessage
		body, refused = s.localCall(ctx, caller, http.MethodGet, "/v1/studio"+path, nil)
		if refused == nil && key != "" {
			result[key] = body
		}
		return body
	}
	esc := func(p *string) string { return url.PathEscape(deref(p)) }
	switch name {
	case "sessions":
		if args.SessionID == nil {
			get("sessions", "/sessions")
			cut(result, "sessions", "sessions", limit)
		} else {
			get("runs", "/sessions/"+esc(args.SessionID)+"/runs")
			cut(result, "runs", "runs", limit)
			get("takes", fmt.Sprintf("/sessions/%s/takes?limit=%d", esc(args.SessionID), limit))
		}
	case "list_workflows":
		return single(get("", "/workflows"), refused)
	case "get_workflow":
		body := get("workflow", "/workflows/"+esc(args.WorkflowID))
		var wf struct {
			Env   string                   `json:"env"`
			Nodes map[string]registry.Node `json:"nodes"`
		}
		if refused == nil && json.Unmarshal(body, &wf) == nil {
			var palette struct {
				Groups []struct {
					Nodes []map[string]any `json:"nodes"`
				} `json:"groups"`
			}
			used := map[string]bool{}
			for _, n := range wf.Nodes {
				used[n.Kind] = true
			}
			if json.Unmarshal(get("", "/nodes?env="+url.QueryEscape(wf.Env)), &palette) == nil {
				nodes := []map[string]any{}
				for _, g := range palette.Groups {
					for _, n := range g.Nodes {
						if kind, _ := n["kind"].(string); used[kind] {
							delete(n, "envs") // PaletteNode is a NodeSchema plus envs
							nodes = append(nodes, n)
						}
					}
				}
				result["nodes"] = nodes
			}
		}
	case "get_run":
		return single(get("", "/runs/"+esc(args.RunID)), refused)
	case "plan_run":
		body, failed := s.localCall(ctx, caller, http.MethodPost, "/v1/studio/runs/plan",
			map[string]any{"workflow_id": *args.WorkflowID, "inputs": cmpAny(args.Inputs, map[string]any{}), "host": args.Host})
		return single(body, failed)
	case "models":
		return single(get("", "/models"), refused)
	case "hosts":
		hosts := get("", "/hosts")
		var list map[string][]map[string]any
		if refused == nil && json.Unmarshal(hosts, &list) == nil {
			for _, h := range list["hosts"] {
				delete(h, "admin") // redacted: no pins or settings
			}
			result["hosts"] = list
		}
		get("queue", "/queue")
	case "search_takes":
		query := url.Values{"limit": {fmt.Sprint(limit)}}
		for key, value := range map[string]*string{"q": args.Q, "kind": args.Kind, "session_id": args.SessionID,
			"batch_id": args.BatchID, "order": args.Order} {
			if value != nil {
				query.Set(key, *value)
			}
		}
		if args.Starred != nil {
			query.Set("starred", fmt.Sprint(*args.Starred))
		}
		return single(get("", "/takes?"+query.Encode()), refused)
	case "get_take":
		get("take", "/takes/"+esc(args.TakeID))
		get("uses", "/takes/"+esc(args.TakeID)+"/uses")
		get("decisions", "/takes/"+esc(args.TakeID)+"/decisions")
	case "list_library":
		switch {
		case args.PipelineID != nil:
			get("memory", "/memory?pipeline_id="+esc(args.PipelineID))
		case args.Kind != nil && *args.Kind == "lora":
			get("loras", "/loras")
		case args.Kind != nil && slices.Contains([]string{"element", "look", "voice"}, *args.Kind):
			get("items", "/library?kind="+*args.Kind)
		case args.Kind != nil:
			return toolErr("invalid", "args: kind must be element, look, voice or lora")
		default:
			get("items", "/library")
			get("loras", "/loras")
		}
	case "pipelines":
		if args.PipelineID == nil {
			get("pipelines", "/pipelines")
		} else {
			get("pipeline", "/pipelines/"+esc(args.PipelineID))
		}
	case "batch_status":
		switch id := esc(args.BatchID); {
		case args.BatchID == nil:
			get("batches", fmt.Sprintf("/batches?limit=%d", limit))
		case args.Row != nil:
			get("execution", fmt.Sprintf("/batches/%s/rows/%d/execution", id, *args.Row))
			get("decisions", fmt.Sprintf("/batches/%s/rows/%d/decisions", id, *args.Row))
		default:
			get("batch", "/batches/"+id)
			get("rows", fmt.Sprintf("/batches/%s/rows?limit=%d", id, limit))
			get("review", "/batches/"+id+"/review")
			get("chat", "/batches/"+id+"/chat")
		}
	case "list_timelines":
		return single(get("", "/timelines"), refused)
	case "show_timeline": // API 1.49: the agent view
		get("timeline", "/timelines/"+esc(args.TimelineID)+"?view=agent")
		get("exports", "/timelines/"+esc(args.TimelineID)+"/exports")
	case "timeline_revisions":
		switch base := "/timelines/" + esc(args.TimelineID) + "/revisions"; {
		case args.Revision != nil && (*args.Revision < 1 || args.Before != nil):
			return toolErr("invalid", "args: revision must be 1 or more, without before")
		case args.Revision != nil:
			get("revision", fmt.Sprintf("%s/%d", base, *args.Revision))
		case args.Before != nil && *args.Before < 1:
			return toolErr("invalid", "args: before must be 1 or more")
		case args.Before != nil:
			get("revisions", fmt.Sprintf("%s?limit=%d&before=%d", base, limit, *args.Before))
		default:
			get("revisions", fmt.Sprintf("%s?limit=%d", base, limit))
		}
	case "use_skill": // API 1.64
		return single(get("", "/skills/"+esc(args.Skill)), refused)
	case "read_skill_file":
		return single(get("", "/skills/"+esc(args.Skill)+"/files/"+esc(args.File)), refused)
	case "custom_nodes":
		return single(get("", "/custom-nodes"), refused)
	case "model_sources":
		return single(get("", "/model-sources"), refused)
	case "model_jobs":
		return single(get("", "/admin/model-jobs"), refused)
	}
	if refused != nil {
		return *refused
	}
	return message.ToolsReply{Result: result}
}

// single is a tool whose output is one route's answer.
func single(body json.RawMessage, refused *message.ToolsReply) message.ToolsReply {
	if refused != nil {
		return *refused
	}
	return message.ToolsReply{Result: body}
}

// cut keeps the first n items of result[key][field] (a list the route does not page).
func cut(result map[string]any, key, field string, n int) {
	raw, ok := result[key].(json.RawMessage)
	var doc map[string]json.RawMessage
	var items []json.RawMessage
	if !ok || json.Unmarshal(raw, &doc) != nil || json.Unmarshal(doc[field], &items) != nil {
		return
	}
	doc[field] = toJSON(items[:min(n, len(items))])
	result[key] = doc
}

// cmpAny is v, or fallback when v is nil.
func cmpAny(v, fallback any) any {
	if v == nil {
		return fallback
	}
	return v
}

// lookAt is look_at after the user's Yes: one interactive ask run of the takes (the owner's image and video takes, in
// order) in the first take's session, else the owner's newest; the reply is kept with the run in one transaction.
func (s *Service) lookAt(ctx context.Context, caller common.Caller, req *message.ToolsRequest) message.ToolsReply {
	var args struct {
		TakeIDs  []string `json:"take_ids"`
		Question string   `json:"question"`
	}
	switch err := strictArgs(req.Args, &args); {
	case err != nil:
		return toolErr("invalid", "args: %v", err)
	case len(args.TakeIDs) < 1 || len(args.TakeIDs) > 8 || len(slices.Compact(slices.Sorted(slices.Values(args.TakeIDs)))) < len(args.TakeIDs):
		return toolErr("invalid", "args: take_ids: 1 to 8 takes, none twice")
	case strings.TrimSpace(args.Question) == "" || len([]rune(args.Question)) > 500:
		return toolErr("invalid", "args: question must be 1-500 characters")
	}
	for _, id := range args.TakeIDs {
		if !utility.IsUUID(id) {
			return toolErr("invalid", "args: take_ids: each a uuid")
		}
	}
	workflow, ok := s.askWorkflow()
	if !ok {
		return toolErr("conflict", "no ask workflow can run now: install Qwen Image or start its machine")
	}
	var out message.ToolsReply
	var run dto.Run
	err := s.tx(ctx, caller, func(tx pgx.Tx) error {
		type takeRow struct {
			ID        string
			Kind      string
			SessionID *string
		}
		takes, err := collect[takeRow](ctx, tx, `SELECT id::text, kind, session_id::text FROM takes
			WHERE id = ANY($1::uuid[]) AND owner_id = $2 AND deleted_at IS NULL AND ready`, args.TakeIDs, caller.ID)
		if err != nil {
			return err
		} else if len(takes) != len(args.TakeIDs) {
			out = toolErr("not_found", "not found")
			return nil
		}
		slices.SortFunc(takes, func(a, b takeRow) int {
			return slices.Index(args.TakeIDs, a.ID) - slices.Index(args.TakeIDs, b.ID)
		})
		media, thumbs := make([]any, len(takes)), 0
		for i, t := range takes {
			if t.Kind != "image" && t.Kind != "video" {
				out = toolErr("invalid", "take %s is %s; look_at looks at images and videos", t.ID, t.Kind)
				return nil
			}
			media[i] = map[string]any{"take": map[string]any{"take_id": t.ID}}
			thumbs += map[bool]int{true: 3, false: 1}[t.Kind == "video"]
		}
		sessionID := takes[0].SessionID
		if sessionID == nil {
			if err := tx.QueryRow(ctx, `SELECT id::text FROM sessions WHERE owner_id = $1 ORDER BY updated_at DESC LIMIT 1`,
				caller.ID).Scan(&sessionID); errors.Is(err, pgx.ErrNoRows) {
				out = toolErr("invalid", "open Create once first: a look needs a session")
				return nil
			} else if err != nil {
				return err
			}
		}
		ask := askNode(workflow)
		inputs := map[string]any{ask + ".system": "Answer the user's question about the takes shown, in plain words.",
			ask + ".prompt": strings.TrimSpace(args.Question), ask + ".media": media}
		if run, err = s.createRun(ctx, tx, caller, *sessionID, &dto.RunRequest{WorkflowID: workflow.ID, Inputs: inputs, Ask: true}); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE runs SET thumbs = $2 WHERE id = $1`, run.ID, thumbs); err != nil {
			return err
		}
		out = message.ToolsReply{Tool: "look_at", Result: map[string]any{"run_id": run.ID, "session_id": run.SessionID,
			"workflow_id": run.WorkflowID, "host": nil, "position": nil}}
		_, err = tx.Exec(ctx, `INSERT INTO assistant_tool_calls (call_id, thread_id, owner_id, tool, reply) VALUES ($1, $2, $3, $4, $5)`,
			req.CallID, req.ThreadID, caller.ID, req.Tool, toJSON(out))
		return err
	})
	var refused *network.APIError
	switch {
	case errors.As(err, &refused) && refused.Code < 500:
		return toolErr(map[bool]string{true: "not_found", false: "invalid"}[refused.Code == 404], "%s", refused.Message)
	case err != nil:
		log.Printf("studio.tools %s: %v", req.CallID, err)
		return toolErr("internal", "internal error")
	case out.Code != "":
		return out
	}
	if err := s.placeQueued(ctx, &run); err == nil {
		result := map[string]any{"run_id": run.ID, "session_id": run.SessionID, "workflow_id": run.WorkflowID, "host": run.Host,
			"position": run.Position}
		out = message.ToolsReply{Tool: "look_at", Result: result}
		_ = common.System(ctx, s.db, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `UPDATE assistant_tool_calls SET reply = $2 WHERE call_id = $1`, req.CallID, toJSON(out))
			return err
		})
	}
	return out
}
