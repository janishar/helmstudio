package main

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// helm mcp (plan 16): the catalog's mcp tools over stdio, each calling the API through the CLI's client as the
// signed-in user. Inputs are take ids, never bytes or URLs; long work returns ids, and wait_run is bounded.

//go:embed gen/catalog.json
var toolCatalogJSON []byte // make gen: a copy of contracts/tools/catalog.json

type catalogTool struct {
	Description string          `json:"description"`
	MCPDesc     string          `json:"mcp_description"` // plan 16 §20.8: in place of description (no card over MCP)
	Surfaces    []string        `json:"surfaces"`
	ReadOnly    bool            `json:"read_only"`
	Destructive bool            `json:"destructive"`
	Input       json.RawMessage `json:"input"`
}

func mcpCatalog() map[string]catalogTool {
	var doc struct {
		Tools map[string]catalogTool `json:"tools"`
	}
	if err := json.Unmarshal(toolCatalogJSON, &doc); err != nil {
		panic("gen/catalog.json: " + err.Error())
	}
	return doc.Tools
}

// maxQueued caps the runs one helm mcp process has waiting or running, so an agent loop cannot fill the GPU queue.
const maxQueued = 4

// waitMax bounds wait_run and export_timeline's wait.
var waitMax = 10 * time.Minute

type mcpServer struct {
	c       *client
	mu      sync.Mutex
	runs    map[string]bool // runs this process started that may still be queued or running
	startMu sync.Mutex      // one run_workflow at a time: calls run concurrently, and the cap is check-then-start
}

type toolFunc func(ctx context.Context, req *mcp.CallToolRequest, in args) (any, []mcp.Content, error)

// args is one call's input, already checked against the tool's catalog schema.
type args map[string]any

func (a args) str(k string) string { s, _ := a[k].(string); return s }
func (a args) esc(k string) string { return url.PathEscape(a.str(k)) }

// localPath is a path the agent names: ~ is the home folder, and it must be absolute, since helm mcp's working
// folder is the MCP client's (Claude Desktop starts it in /), not the agent's. Unset is "".
func (a args) localPath(k string) (string, error) {
	p := a.str(k)
	if p == "" {
		return "", nil
	}
	if rest, ok := strings.CutPrefix(p, "~"); ok && (rest == "" || rest[0] == '/') {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		p = home + rest
	}
	if !filepath.IsAbs(p) {
		return "", fmt.Errorf("%s: give an absolute path (or ~/...), not %q", k, a.str(k))
	}
	return filepath.Clean(p), nil
}

// pick copies the given keys that are set.
func (a args) pick(keys ...string) map[string]any {
	out := map[string]any{}
	for _, k := range keys {
		if v, ok := a[k]; ok {
			out[k] = v
		}
	}
	return out
}

func mcpCmd(args []string, stdin io.Reader, stdout io.Writer) error {
	if len(args) != 0 {
		return errors.New("usage: helm mcp   (an MCP server over stdio; add it to your MCP client)")
	}
	c, err := loggedIn()
	if err != nil {
		return err
	}
	srv := newMCPServer(c)
	return srv.Run(context.Background(), &mcp.IOTransport{Reader: io.NopCloser(stdin), Writer: nopWriteCloser{stdout}})
}

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }

// newMCPServer registers every catalog tool marked mcp and the two resources.
func newMCPServer(c *client) *mcp.Server {
	c.app = "mcp" // its timeline edits are labelled via mcp
	s := &mcpServer{c: c, runs: map[string]bool{}}
	srv := mcp.NewServer(&mcp.Implementation{Name: "helmstudio", Version: "1"}, &mcp.ServerOptions{
		Instructions: "helmstudio: find a workflow (list_workflows, get_workflow), run_workflow, wait_run, then get_take to see a take. Inputs are take ids; upload turns a local image into one. To edit a sequence: show_timeline (clip ids, revision), try_timeline_edit, then edit_timeline with the same ops and base_revision; timeline_frame to look at a time; restore_timeline goes back. Magic boards: list_boards or create_board, show_board (block ids, revision), try_board_edit then edit_board; plan_board shows the cost before run_blocks (no card here: plan first); board_frame looks at a time; send_to_timeline makes a sequence.",
	})
	handlers := s.handlers()
	for name, t := range mcpCatalog() {
		if !slices.Contains(t.Surfaces, "mcp") {
			continue
		}
		h, ok := handlers[name]
		if !ok {
			panic("helm mcp: no handler for catalog tool " + name)
		}
		ann := &mcp.ToolAnnotations{ReadOnlyHint: t.ReadOnly}
		if !t.ReadOnly {
			destructive := t.Destructive
			ann.DestructiveHint = &destructive // the spec's default is true: every other write says false
		}
		desc := t.Description
		if t.MCPDesc != "" {
			desc = t.MCPDesc
		}
		mcp.AddTool(srv, &mcp.Tool{Name: name, Description: desc, InputSchema: t.Input, Annotations: ann},
			func(ctx context.Context, req *mcp.CallToolRequest, in map[string]any) (*mcp.CallToolResult, any, error) {
				out, content, err := h(ctx, req, in)
				if err != nil {
					return nil, nil, err // an API error is its {"detail"} text, as a tool error
				}
				if content == nil {
					return nil, out, nil
				}
				b, err := json.Marshal(out)
				if err != nil {
					return nil, nil, err
				}
				return &mcp.CallToolResult{Content: append([]mcp.Content{&mcp.TextContent{Text: string(b)}}, content...)}, out, nil
			})
	}
	srv.AddResourceTemplate(&mcp.ResourceTemplate{Name: "take", URITemplate: "helm://take/{id}", Description: "a take: its metadata and thumbnail"}, s.readResource)
	srv.AddResourceTemplate(&mcp.ResourceTemplate{Name: "workflow", URITemplate: "helm://workflow/{id}", Description: "a workflow and its params", MIMEType: "application/json"}, s.readResource)
	return srv
}

// get answers one GET as raw JSON.
func (s *mcpServer) get(ctx context.Context, path string) (json.RawMessage, error) {
	var out json.RawMessage
	err := s.c.callCtx(ctx, http.MethodGet, "/v1/studio"+path, nil, http.StatusOK, &out)
	return out, err
}

func (s *mcpServer) send(ctx context.Context, method, path string, body any, want int) (json.RawMessage, error) {
	var out json.RawMessage
	err := s.c.callCtx(ctx, method, "/v1/studio"+path, body, want, &out)
	return out, err
}

// gets is several GETs into one object, by key.
func (s *mcpServer) gets(ctx context.Context, paths ...string) (map[string]json.RawMessage, error) {
	out := map[string]json.RawMessage{}
	for i := 0; i < len(paths); i += 2 {
		b, err := s.get(ctx, paths[i+1])
		if err != nil {
			return nil, err
		}
		out[paths[i]] = b
	}
	return out, nil
}

// plain is a tool that answers JSON only.
func plain(f func(context.Context, args) (any, error)) toolFunc {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in args) (any, []mcp.Content, error) {
		out, err := f(ctx, in)
		return out, nil, err
	}
}

func (s *mcpServer) handlers() map[string]toolFunc {
	del := func(path, key string) toolFunc {
		return plain(func(ctx context.Context, in args) (any, error) {
			if err := s.c.callCtx(ctx, http.MethodDelete, "/v1/studio"+path+in.esc(key), nil, http.StatusNoContent, nil); err != nil {
				return nil, err
			}
			return map[string]string{"deleted": in.str(key)}, nil
		})
	}
	h := map[string]toolFunc{
		"list_workflows": plain(func(ctx context.Context, _ args) (any, error) { return s.get(ctx, "/workflows") }),
		"get_workflow":   plain(s.getWorkflow),
		"plan_run": plain(func(ctx context.Context, in args) (any, error) {
			body := in.pick("workflow_id", "inputs", "host")
			if body["inputs"] == nil {
				body["inputs"] = map[string]any{}
			}
			return s.send(ctx, http.MethodPost, "/runs/plan", body, http.StatusOK)
		}),
		"search_takes": plain(func(ctx context.Context, in args) (any, error) {
			q := url.Values{"limit": {"10"}}
			for k, v := range in {
				q.Set(k, fmt.Sprint(v))
			}
			return s.get(ctx, "/takes?"+q.Encode())
		}),
		"get_take": s.getTake,
		"list_library": plain(func(ctx context.Context, in args) (any, error) {
			switch k := in.str("kind"); {
			case in.str("pipeline_id") != "":
				return s.gets(ctx, "memory", "/memory?pipeline_id="+in.esc("pipeline_id"))
			case k == "lora":
				return s.gets(ctx, "loras", "/loras")
			case k != "":
				return s.gets(ctx, "items", "/library?kind="+url.QueryEscape(k))
			}
			return s.gets(ctx, "items", "/library", "loras", "/loras")
		}),
		"batch_status": plain(func(ctx context.Context, in args) (any, error) {
			id, limit := in.esc("batch_id"), 10
			if l, ok := in["limit"].(float64); ok {
				limit = int(l)
			}
			switch row, hasRow := in["row"].(float64); {
			case id == "":
				return s.gets(ctx, "batches", fmt.Sprintf("/batches?limit=%d", limit))
			case hasRow:
				return s.gets(ctx, "execution", fmt.Sprintf("/batches/%s/rows/%d/execution", id, int(row)),
					"decisions", fmt.Sprintf("/batches/%s/rows/%d/decisions", id, int(row)))
			}
			out, err := s.gets(ctx, "batch", "/batches/"+id, "rows", fmt.Sprintf("/batches/%s/rows?limit=%d", id, limit),
				"review", "/batches/"+id+"/review")
			if err != nil {
				return nil, err
			}
			// the chat is 404 for a batch without a director (a plain workflow batch, a pipeline with none)
			var b struct{ Director json.RawMessage }
			if json.Unmarshal(out["batch"], &b) == nil && len(b.Director) > 0 && string(b.Director) != "null" {
				if out["chat"], err = s.get(ctx, "/batches/"+id+"/chat"); err != nil {
					return nil, err
				}
			}
			return out, nil
		}),
		"list_timelines": plain(func(ctx context.Context, _ args) (any, error) { return s.get(ctx, "/timelines") }),
		"show_timeline": plain(func(ctx context.Context, in args) (any, error) { // API 1.49: the agent view
			return s.gets(ctx, "timeline", "/timelines/"+in.esc("timeline_id")+"?view=agent", "exports", "/timelines/"+in.esc("timeline_id")+"/exports")
		}),
		"timeline_revisions": plain(func(ctx context.Context, in args) (any, error) {
			base := "/timelines/" + in.esc("timeline_id") + "/revisions"
			if r, ok := in["revision"].(float64); ok {
				return s.gets(ctx, "revision", fmt.Sprintf("%s/%d", base, int(r)))
			}
			q := url.Values{"limit": {"10"}}
			for _, k := range []string{"before", "limit"} {
				if v, ok := in[k].(float64); ok {
					q.Set(k, fmt.Sprint(int(v)))
				}
			}
			return s.gets(ctx, "revisions", base+"?"+q.Encode())
		}),
		"try_timeline_edit": plain(func(ctx context.Context, in args) (any, error) { return s.editTimeline(ctx, in, true) }),
		"edit_timeline":     plain(func(ctx context.Context, in args) (any, error) { return s.editTimeline(ctx, in, false) }),
		"restore_timeline": plain(func(ctx context.Context, in args) (any, error) {
			return s.send(ctx, http.MethodPost, "/timelines/"+in.esc("timeline_id")+"/restore", in.pick("revision", "to"), http.StatusOK)
		}),
		"timeline_frame": s.timelineFrame,
		"run_workflow":   plain(s.runWorkflow),
		"wait_run":       s.waitRun,
		"cancel_run": plain(func(ctx context.Context, in args) (any, error) {
			return s.send(ctx, http.MethodPost, "/runs/"+in.esc("run_id")+"/cancel", nil, http.StatusAccepted)
		}),
		"upload": plain(func(ctx context.Context, in args) (any, error) {
			p, err := in.localPath("path")
			if err != nil {
				return nil, err
			}
			return s.c.upload(ctx, p)
		}),
		"start_batch": plain(func(ctx context.Context, in args) (any, error) {
			return s.send(ctx, http.MethodPost, "/batches", map[string]any(in), http.StatusAccepted)
		}),
		"review_batch": plain(func(ctx context.Context, in args) (any, error) {
			return s.get(ctx, "/batches/"+in.esc("batch_id")+"/review")
		}),
		"pick": plain(s.pick),
		"batch_chat": plain(func(ctx context.Context, in args) (any, error) {
			return s.send(ctx, http.MethodPost, "/batches/"+in.esc("batch_id")+"/chat", in.pick("text"), http.StatusAccepted)
		}),
		"add_to_timeline": plain(func(ctx context.Context, in args) (any, error) {
			if in.str("timeline_id") == "" {
				if in.str("track") != "" && in.str("track") != "V1" {
					return nil, errors.New("a new sequence starts on V1: add sound to it after")
				}
				return s.send(ctx, http.MethodPost, "/timelines", in.pick("take_ids", "name"), http.StatusCreated)
			}
			return s.send(ctx, http.MethodPost, "/timelines/"+in.esc("timeline_id")+"/clips", in.pick("take_ids", "track"), http.StatusOK)
		}),
		"export_timeline": s.exportTimeline,
		"delete_take":     del("/takes/", "take_id"),
		"delete_run":      del("/runs/", "run_id"),
		"delete_session":  del("/sessions/", "session_id"),
	}
	maps.Copy(h, s.magicHandlers())
	return h
}

// editTimeline sends the ops to /edits (dry run: try_timeline_edit). An op that cannot apply is the API's 422
// detail (`ops[3]: split_edge`), as a tool error.
func (s *mcpServer) editTimeline(ctx context.Context, in args, dry bool) (any, error) {
	body := in.pick("base_revision", "ops")
	body["dry_run"] = dry
	return s.send(ctx, http.MethodPost, "/timelines/"+in.esc("timeline_id")+"/edits", body, http.StatusOK)
}

// timelineFrame is what V1 shows at a time, with its stored image as image content.
func (s *mcpServer) timelineFrame(ctx context.Context, _ *mcp.CallToolRequest, in args) (any, []mcp.Content, error) {
	raw, err := s.send(ctx, http.MethodPost, "/timelines/"+in.esc("timeline_id")+"/frame", in.pick("at"), http.StatusOK)
	if err != nil {
		return nil, nil, err
	}
	var f struct {
		URL string `json:"url"`
	}
	_ = json.Unmarshal(raw, &f)
	if img, mime := s.image(ctx, f.URL, 16<<20); img != nil {
		return raw, []mcp.Content{&mcp.ImageContent{Data: img, MIMEType: mime}}, nil
	}
	return raw, nil, nil
}

// getWorkflow is the workflow and the node schemas of the kinds it uses (as the assistant's get_workflow).
func (s *mcpServer) getWorkflow(ctx context.Context, in args) (any, error) {
	wf, err := s.get(ctx, "/workflows/"+in.esc("workflow_id"))
	if err != nil {
		return nil, err
	}
	var w struct {
		Env   string `json:"env"`
		Nodes map[string]struct {
			Kind string `json:"kind"`
		} `json:"nodes"`
	}
	var palette struct {
		Groups []struct {
			Nodes []map[string]any `json:"nodes"`
		} `json:"groups"`
	}
	nodes := []map[string]any{}
	if json.Unmarshal(wf, &w) == nil {
		raw, err := s.get(ctx, "/nodes?env="+url.QueryEscape(w.Env))
		if err != nil {
			return nil, err
		}
		_ = json.Unmarshal(raw, &palette)
		for _, g := range palette.Groups {
			for _, n := range g.Nodes {
				kind, _ := n["kind"].(string)
				for _, wn := range w.Nodes {
					if wn.Kind == kind {
						delete(n, "envs")
						nodes = append(nodes, n)
						break
					}
				}
			}
		}
	}
	return map[string]any{"workflow": wf, "nodes": nodes}, nil
}

// getTake is the take, its uses and decisions, and its small thumbnail as image content (not an audio take's).
func (s *mcpServer) getTake(ctx context.Context, _ *mcp.CallToolRequest, in args) (any, []mcp.Content, error) {
	out, err := s.gets(ctx, "take", "/takes/"+in.esc("take_id"), "uses", "/takes/"+in.esc("take_id")+"/uses",
		"decisions", "/takes/"+in.esc("take_id")+"/decisions")
	if err != nil {
		return nil, nil, err
	}
	img, mime := s.thumb(ctx, out["take"])
	if img == nil {
		return out, nil, nil
	}
	return out, []mcp.Content{&mcp.ImageContent{Data: img, MIMEType: mime}}, nil
}

// thumb fetches a take's thumb_url (plan 22: a WebP, short side 512 px); nil for audio or on any failure.
func (s *mcpServer) thumb(ctx context.Context, take json.RawMessage) ([]byte, string) {
	var t struct {
		Kind     string `json:"kind"`
		ThumbURL string `json:"thumb_url"`
	}
	if json.Unmarshal(take, &t) != nil || t.Kind == "audio" {
		return nil, ""
	}
	return s.image(ctx, t.ThumbURL, 4<<20)
}

// image fetches a signed image link (relative to the API) of at most limit bytes; nil when empty or on any failure.
func (s *mcpServer) image(ctx context.Context, link string, limit int64) ([]byte, string) {
	if link == "" {
		return nil, ""
	}
	base, err := url.Parse(s.c.cfg.URL + "/")
	if err != nil {
		return nil, ""
	}
	abs, err := base.Parse(link)
	if err != nil {
		return nil, ""
	}
	ctx, cancel := context.WithTimeout(ctx, apiTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, abs.String(), nil)
	if err != nil {
		return nil, ""
	}
	resp, err := s.c.http.Do(req)
	if err != nil {
		return nil, ""
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	mime := resp.Header.Get("Content-Type")
	if err != nil || resp.StatusCode != http.StatusOK || !strings.HasPrefix(mime, "image/") {
		return nil, ""
	}
	return b, mime
}

// runWorkflow starts a run (in a new session unless one is given), at most maxQueued of this process's at once.
func (s *mcpServer) runWorkflow(ctx context.Context, in args) (any, error) {
	s.startMu.Lock()
	defer s.startMu.Unlock()
	if err := s.roomForRun(ctx); err != nil {
		return nil, err
	}
	sid := in.str("session_id")
	if sid == "" {
		var sess struct {
			ID string `json:"id"`
		}
		if err := s.c.callCtx(ctx, http.MethodPost, "/v1/studio/sessions", map[string]string{"title": in.str("workflow_id")}, http.StatusCreated, &sess); err != nil {
			return nil, err
		}
		sid = sess.ID
	}
	body := in.pick("workflow_id", "inputs", "looks", "host")
	raw, err := s.send(ctx, http.MethodPost, "/sessions/"+url.PathEscape(sid)+"/runs", body, http.StatusAccepted)
	if err != nil {
		return nil, err
	}
	var r run
	if json.Unmarshal(raw, &r) == nil && !ended(r.Status) {
		s.mu.Lock()
		s.runs[r.ID] = true
		s.mu.Unlock()
	}
	return raw, nil
}

func ended(status string) bool {
	return status == "succeeded" || status == "failed" || status == "cancelled"
}

// roomForRun drops the tracked runs that ended, then refuses when maxQueued are still waiting or running.
func (s *mcpServer) roomForRun(ctx context.Context) error {
	s.mu.Lock()
	ids := make([]string, 0, len(s.runs))
	for id := range s.runs {
		ids = append(ids, id)
	}
	s.mu.Unlock()
	if len(ids) < maxQueued {
		return nil
	}
	live := 0
	for _, id := range ids {
		var r run
		err := s.c.callCtx(ctx, http.MethodGet, "/v1/studio/runs/"+url.PathEscape(id), nil, http.StatusOK, &r)
		var apiErr *apiError
		if (err == nil && ended(r.Status)) || (errors.As(err, &apiErr) && apiErr.status == http.StatusNotFound) {
			s.mu.Lock()
			delete(s.runs, id)
			s.mu.Unlock()
			continue
		}
		live++
	}
	if live >= maxQueued {
		return fmt.Errorf("%d runs from this MCP session are still queued or running (at most %d): wait_run or cancel_run one first", live, maxQueued)
	}
	return nil
}

// progress turns the lines printEvent writes into MCP progress notifications (when the client asked for them).
type progress struct {
	ctx   context.Context
	req   *mcp.CallToolRequest
	n     float64
	total float64
	buf   bytes.Buffer
}

func (p *progress) Write(b []byte) (int, error) {
	p.buf.Write(b)
	for {
		line, err := p.buf.ReadString('\n')
		if err != nil {
			p.buf.WriteString(line) // not a whole line yet
			return len(b), nil
		}
		p.say(strings.TrimSpace(line))
	}
}

func (p *progress) say(msg string) {
	tok := p.req.Params.GetProgressToken()
	if tok == nil || p.req.Session == nil {
		return
	}
	p.n++
	_ = p.req.Session.NotifyProgress(p.ctx, &mcp.ProgressNotificationParams{ProgressToken: tok, Message: msg, Progress: p.n, Total: p.total})
}

// waitRun follows the run on its session's event stream (sending progress) until it ends or the time is up.
func (s *mcpServer) waitRun(ctx context.Context, req *mcp.CallToolRequest, in args) (any, []mcp.Content, error) {
	dir, err := in.localPath("out_dir")
	if err != nil {
		return nil, nil, err
	}
	limit := waitMax
	if t, ok := in["timeout_s"].(float64); ok {
		limit = min(limit, time.Duration(t*float64(time.Second)))
	}
	tool := ctx // the download after the wait: the tool call's, not cut by the wait's limit
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	id := in.str("run_id")
	var r struct {
		run
		SessionID string `json:"session_id"`
	}
	getRun := func() (json.RawMessage, error) {
		raw, err := s.get(tool, "/runs/"+url.PathEscape(id)) // also after the wait ran out
		if err == nil {
			err = json.Unmarshal(raw, &r)
		}
		return raw, err
	}
	raw, err := getRun()
	if err != nil {
		return nil, nil, err
	}
	p := &progress{ctx: ctx, req: req}
	since := 0 // the session's replay from the start: the run's own events, however far it got
	for !ended(r.Status) {
		status, seq, err := s.c.stream(ctx, r.SessionID, id, since, p)
		since = max(since, seq)
		var apiErr *apiError
		if status == "" && ctx.Err() == nil && errors.As(err, &apiErr) {
			return nil, nil, err
		}
		if status == "" && ctx.Err() == nil {
			select { // the stream dropped: resume after the last seq seen
			case <-ctx.Done():
			case <-time.After(time.Second):
			}
		}
		if raw, err = getRun(); err != nil {
			return nil, nil, err
		}
		if ctx.Err() != nil {
			break
		}
	}
	out := map[string]any{"run": raw, "still_running": !ended(r.Status)}
	if dir != "" && r.Status == "succeeded" {
		var log bytes.Buffer
		if err := s.c.download(tool, r.run, dir, &log); err != nil {
			return nil, nil, err
		}
		saved := []string{}
		for _, l := range strings.Split(strings.TrimSpace(log.String()), "\n") {
			if l != "" && !strings.HasPrefix(l, "take ") {
				saved = append(saved, l)
			}
		}
		out["saved"] = saved
	}
	if ended(r.Status) {
		s.mu.Lock()
		delete(s.runs, id)
		s.mu.Unlock()
	}
	return out, nil, nil
}

// pick answers the gate the row waits at (read from the review when not given).
func (s *mcpServer) pick(ctx context.Context, in args) (any, error) {
	row, _ := in["row"].(float64)
	reject, _ := in["reject"].(bool)
	if reject == (in["take_ids"] != nil) {
		return nil, errors.New("give take_ids to keep, or reject: true")
	}
	items, err := s.c.review(ctx, in.str("batch_id"))
	if err != nil {
		return nil, err
	}
	gate := ""
	for _, it := range items {
		if it.Row == int(row) {
			gate = it.Gate
		}
	}
	if gate == "" {
		return nil, fmt.Errorf("row %d is not waiting for a pick (review_batch lists the rows that are)", int(row))
	}
	body := in.pick("take_ids", "note")
	body["gate"] = gate
	if reject {
		body["reject"] = true // the API's reject is const true: never send false next to take_ids
	}
	return s.send(ctx, http.MethodPost, fmt.Sprintf("/batches/%s/rows/%d/pick", in.esc("batch_id"), int(row)), body, http.StatusAccepted)
}

// exportTimeline plans, then exports the current revision; with out it waits (bounded) and saves the MP4.
func (s *mcpServer) exportTimeline(ctx context.Context, req *mcp.CallToolRequest, in args) (any, []mcp.Content, error) {
	out, err := in.localPath("out")
	if err != nil {
		return nil, nil, err
	}
	base := "/timelines/" + in.esc("timeline_id")
	body := in.pick("host")
	plan, err := s.send(ctx, http.MethodPost, base+"/export/plan", body, http.StatusOK)
	if err != nil {
		return nil, nil, err
	}
	if in["plan_only"] == true {
		return map[string]any{"plan": plan}, nil, nil
	}
	var pl struct {
		Revision int `json:"revision"`
	}
	_ = json.Unmarshal(plan, &pl)
	body["revision"] = pl.Revision
	raw, err := s.send(ctx, http.MethodPost, base+"/export", body, http.StatusAccepted)
	if err != nil {
		return nil, nil, err
	}
	if out == "" {
		return map[string]any{"plan": plan, "job": raw}, nil, nil
	}
	tool := ctx // the job reads and the download: the tool call's, not cut by the wait's limit
	ctx, cancel := context.WithTimeout(ctx, waitMax)
	defer cancel()
	p := &progress{ctx: ctx, req: req}
	var j mediaJob
	for {
		if err := json.Unmarshal(raw, &j); err != nil {
			return nil, nil, err
		}
		if j.Progress != nil {
			p.total = float64(j.Progress.Total)
			p.n = float64(j.Progress.Frames) - 1
			p.say(fmt.Sprintf("%s %d/%d frames", j.Status, j.Progress.Frames, j.Progress.Total))
		}
		switch j.Status {
		case "succeeded":
			if err := s.c.saveExport(tool, deref(j.TakeID), out, io.Discard); err != nil {
				return nil, nil, err
			}
			return map[string]any{"plan": plan, "job": raw, "saved": out}, nil, nil
		case "failed", "cancelled":
			return map[string]any{"plan": plan, "job": raw}, nil, nil
		}
		select {
		case <-ctx.Done():
			return map[string]any{"plan": plan, "job": raw}, nil, nil // still running: show_timeline lists it
		case <-time.After(pollEvery):
		}
		if raw, err = s.get(tool, "/media-jobs/"+url.PathEscape(j.ID)); err != nil { // also after the wait ran out
			return nil, nil, err
		}
	}
}

// readResource answers helm://take/{id} (metadata and thumbnail) and helm://workflow/{id}.
func (s *mcpServer) readResource(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	uri := req.Params.URI
	path := ""
	if id, ok := strings.CutPrefix(uri, "helm://take/"); ok {
		path = "/takes/" + url.PathEscape(id)
	} else if id, ok := strings.CutPrefix(uri, "helm://workflow/"); ok {
		path = "/workflows/" + url.PathEscape(id)
	}
	if path == "" {
		return nil, mcp.ResourceNotFoundError(uri)
	}
	raw, err := s.get(ctx, path)
	var apiErr *apiError
	if errors.As(err, &apiErr) && apiErr.status == http.StatusNotFound {
		return nil, mcp.ResourceNotFoundError(uri)
	}
	if err != nil {
		return nil, err
	}
	res := &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: uri, MIMEType: "application/json", Text: string(raw)}}}
	if strings.HasPrefix(path, "/takes/") {
		if img, mime := s.thumb(ctx, raw); img != nil {
			res.Contents = append(res.Contents, &mcp.ResourceContents{URI: uri, MIMEType: mime, Blob: img})
		}
	}
	return res, nil
}
