package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type workflow struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Env   string `json:"env"`
	Nodes map[string]struct {
		Kind string `json:"kind"`
	} `json:"nodes"`
	NodeSchemas map[string]struct {
		Params []struct {
			Name string `json:"name"`
			Type string `json:"type"`
		} `json:"params"`
	} `json:"node_schemas"`
}

type runOutput struct {
	Index int    `json:"index"`
	Key   string `json:"key"`
	Kind  string `json:"kind"`
	URL   string `json:"url"`
}

type run struct {
	ID      string      `json:"id"`
	Status  string      `json:"status"`
	Error   *string     `json:"error"`
	Host    *string     `json:"host"`
	Outputs []runOutput `json:"outputs"`
}

type event struct {
	Seq   int             `json:"seq"`
	Type  string          `json:"type"`
	RunID string          `json:"run_id"`
	Data  json.RawMessage `json:"data"`
}

type params []string

func (p *params) String() string     { return strings.Join(*p, " ") }
func (p *params) Set(v string) error { *p = append(*p, v); return nil }

// inputs turns node.param=value pairs into RunRequest.inputs, typed by the workflow's node schemas. A key the
// workflow does not have is sent as text: the server answers 422 naming it.
func (w workflow) inputs(ps []string) (map[string]any, error) {
	out := map[string]any{}
	for _, p := range ps {
		key, val, ok := strings.Cut(p, "=")
		node, name, ok2 := strings.Cut(key, ".")
		if !ok || !ok2 {
			return nil, fmt.Errorf("-p %q: want node.param=value", p)
		}
		typ := ""
		for _, ps := range w.NodeSchemas[w.Nodes[node].Kind].Params {
			if ps.Name == name {
				typ = ps.Type
			}
		}
		var v any = val
		var err error
		switch typ {
		case "int":
			v, err = strconv.ParseInt(val, 10, 64)
		case "float":
			v, err = strconv.ParseFloat(val, 64)
		case "bool":
			v, err = strconv.ParseBool(val)
		}
		if err != nil {
			return nil, fmt.Errorf("-p %s: not a valid %s", key, typ)
		}
		out[key] = v
	}
	return out, nil
}

func runCmd(args []string, stdout, stderr io.Writer, stop <-chan os.Signal) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	sessionID := fs.String("session", "new", "session id, or new")
	out := fs.String("out", ".", "directory for the outputs")
	var ps params
	fs.Var(&ps, "p", "node.param=value (repeatable)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		fs.Usage()
		return flag.ErrHelp
	}
	wfID := fs.Arg(0)
	if err := fs.Parse(fs.Args()[1:]); err != nil { // flags may follow the workflow id
		return err
	}
	c, err := loggedIn()
	if err != nil {
		return err
	}
	var wf workflow
	if err := c.call(http.MethodGet, "/v1/studio/workflows/"+url.PathEscape(wfID), nil, http.StatusOK, &wf); err != nil {
		return fmt.Errorf("workflow %s: %w", wfID, err)
	}
	inputs, err := wf.inputs(ps)
	if err != nil {
		return err
	}

	sid, since := *sessionID, 0
	if sid == "new" {
		var s struct {
			ID string `json:"id"`
		}
		if err := c.call(http.MethodPost, "/v1/studio/sessions", map[string]string{"title": wf.Title}, http.StatusCreated, &s); err != nil {
			return err
		}
		sid = s.ID
	} else if since, err = c.lastSeq(sid); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "session %s\n", sid)

	var r run
	body := map[string]any{"workflow_id": wfID, "inputs": inputs}
	if err := c.call(http.MethodPost, "/v1/studio/sessions/"+url.PathEscape(sid)+"/runs", body, http.StatusAccepted, &r); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "run %s\n", r.ID)
	if r.Status == "failed" {
		return fmt.Errorf("run failed: %s", deref(r.Error))
	}

	status, err := c.follow(sid, r.ID, since, stdout, stop)
	if err != nil {
		return err
	}
	if status != "succeeded" {
		return fmt.Errorf("run %s", status)
	}
	if err := c.call(http.MethodGet, "/v1/studio/runs/"+r.ID, nil, http.StatusOK, &r); err != nil {
		return err
	}
	return c.download(r, *out, stdout)
}

// lastSeq is the session's newest event seq, so following starts after it.
func (c *client) lastSeq(sid string) (int, error) {
	since := 0
	for {
		var page struct {
			Events  []json.RawMessage `json:"events"`
			LastSeq int               `json:"last_seq"`
		}
		path := fmt.Sprintf("/v1/studio/sessions/%s/events?since=%d", url.PathEscape(sid), since)
		if err := c.call(http.MethodGet, path, nil, http.StatusOK, &page); err != nil {
			return 0, err
		}
		if len(page.Events) == 0 {
			return since, nil
		}
		since = page.LastSeq
	}
}

// follow streams the session's events after `since`, prints the run's, and returns its terminal status. The first
// interrupt cancels the run and keeps following until it ends; the second stops following.
func (c *client) follow(sid, runID string, since int, stdout io.Writer, stop <-chan os.Signal) (string, error) {
	last, failures, cancelling := since, 0, false
	for {
		ctx, quit := context.WithCancel(context.Background())
		interrupted, done := make(chan struct{}), make(chan struct{})
		go func() { // only this goroutine touches stop; it ends the stream on an interrupt
			select {
			case <-stop:
				close(interrupted)
				quit()
			case <-done:
			}
		}()
		status, seq, err := c.stream(ctx, sid, runID, last, stdout)
		close(done)
		quit()
		if status != "" {
			return status, nil
		}
		if seq > last {
			last, failures = seq, 0
		}
		select {
		case <-interrupted:
			if cancelling {
				return "", errors.New("interrupted; the run may still be going")
			}
			cancelling = true
			fmt.Fprintln(stdout, "cancelling (Ctrl-C again to stop waiting)")
			if err := c.call(http.MethodPost, "/v1/studio/runs/"+url.PathEscape(runID)+"/cancel", nil, http.StatusAccepted, nil); err != nil {
				fmt.Fprintln(stdout, "cancel:", err) // 409: it already ended, and the stream shows how
			}
			continue
		default:
		}
		var apiErr *apiError
		if failures++; failures > 5 || errors.As(err, &apiErr) {
			return "", fmt.Errorf("event stream: %w", err)
		}
		time.Sleep(time.Second) // the stream dropped: resume from the last seq seen
	}
}

// stream reads one SSE connection. It returns the run's terminal status when seen, else the last seq read and why
// the stream ended.
func (c *client) stream(ctx context.Context, sid, runID string, since int, stdout io.Writer) (string, int, error) {
	hdr := http.Header{"Accept": {"text/event-stream"}, "Last-Event-Id": {strconv.Itoa(since)}}
	resp, err := c.do(ctx, http.MethodGet, "/v1/studio/sessions/"+url.PathEscape(sid)+"/events", nil, hdr)
	if err != nil {
		return "", since, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", since, readError(resp)
	}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	last := since
	for sc.Scan() {
		data, ok := strings.CutPrefix(sc.Text(), "data:")
		if !ok {
			continue // id:, event:, comments and blank lines: the data line carries everything
		}
		var e event
		if err := json.Unmarshal([]byte(strings.TrimSpace(data)), &e); err != nil {
			return "", last, fmt.Errorf("bad event: %w", err)
		}
		last = max(last, e.Seq)
		if e.RunID != runID {
			continue
		}
		if status := printEvent(e, stdout); status != "" {
			return status, last, nil
		}
	}
	if err := sc.Err(); err != nil {
		return "", last, err
	}
	return "", last, io.ErrUnexpectedEOF
}

// printEvent prints one event of the run and returns its status when it is terminal.
func printEvent(e event, stdout io.Writer) string {
	switch e.Type {
	case "status":
		var d struct {
			Status string  `json:"status"`
			Error  *string `json:"error"`
			Host   *string `json:"host"`
		}
		_ = json.Unmarshal(e.Data, &d)
		switch d.Status {
		case "running":
			fmt.Fprintf(stdout, "running on %s\n", deref(d.Host))
		case "failed":
			fmt.Fprintf(stdout, "failed: %s\n", deref(d.Error))
		default:
			fmt.Fprintln(stdout, d.Status)
		}
		if d.Status == "succeeded" || d.Status == "failed" || d.Status == "cancelled" {
			return d.Status
		}
	case "progress":
		var d struct {
			Node string `json:"node"`
			Step int    `json:"step"`
			Of   int    `json:"of"`
		}
		_ = json.Unmarshal(e.Data, &d)
		fmt.Fprintf(stdout, "%s %d/%d\n", d.Node, d.Step, d.Of)
	case "output":
		var d runOutput
		_ = json.Unmarshal(e.Data, &d)
		fmt.Fprintf(stdout, "output %d %s\n", d.Index, d.Kind)
	}
	return ""
}

// download saves each output through its signed link (no credentials) as <dir>/<run_id>_<file name>.
func (c *client) download(r run, dir string, stdout io.Writer) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	base, err := url.Parse(c.cfg.URL + "/")
	if err != nil {
		return err
	}
	for _, o := range r.Outputs {
		link, err := base.Parse(o.URL) // a relative link resolves against the API base
		if err != nil {
			return err
		}
		dest := filepath.Join(dir, r.ID+"_"+path.Base(o.Key))
		if err := fetch(c.http, link.String(), dest); err != nil {
			return fmt.Errorf("output %d: %w", o.Index, err)
		}
		fmt.Fprintln(stdout, dest)
	}
	return nil
}

func fetch(hc *http.Client, link, dest string) error {
	resp, err := hc.Get(link)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return readError(resp)
	}
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
