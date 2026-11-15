package main

import (
	"bufio"
	"encoding/csv"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type batchCounts struct {
	Total     int `json:"total"`
	Waiting   int `json:"waiting"`
	Succeeded int `json:"succeeded"`
	Failed    int `json:"failed"`
	Cancelled int `json:"cancelled"`
}

type batch struct {
	ID        string      `json:"id"`
	Title     string      `json:"title"`
	SessionID *string     `json:"session_id"`
	Status    string      `json:"status"`
	Reason    *string     `json:"reason"`
	Counts    batchCounts `json:"counts"`
	CreatedAt string      `json:"created_at"`
}

type batchRow struct {
	Row     int      `json:"row"`
	Attempt int      `json:"attempt"`
	Status  string   `json:"status"`
	TakeIDs []string `json:"take_ids"`
	Error   *string  `json:"error"`
}

// batchCmd submits a batch of one workflow or of a pipeline (rows from --lines, --csv or --grid) and prints each
// row as it ends. Exit 1 when any row failed or the batch did not end done. helm batch review|pick answer gates.
func batchCmd(args []string, stdin io.Reader, stdout, stderr io.Writer, stop <-chan os.Signal) error {
	if len(args) > 0 && args[0] == "review" {
		return batchReview(args[1:], stdout)
	}
	if len(args) > 0 && args[0] == "pick" {
		return batchPick(args[1:], stdout)
	}
	fs := flag.NewFlagSet("batch", flag.ContinueOnError)
	pipelineID := fs.String("pipeline", "", "run one of your pipelines instead of a workflow; its inputs are the columns (inputs.<name>)")
	fs.SetOutput(stderr)
	linesFile := fs.String("lines", "", "file with one value per line (- = stdin); blank lines are dropped")
	param := fs.String("param", "prompt.text", "the node.param --lines sets")
	csvFile := fs.String("csv", "", "CSV file whose header names node.param columns; an empty cell leaves it unset")
	var grid, ps params
	fs.Var(&grid, "grid", "node.param=a,b,c: one axis of a grid (at most 2)")
	fs.Var(&ps, "p", "node.param=value base setting for every row (repeatable)")
	seeds := fs.Int("seeds", 0, "repeat each row N times with consecutive seeds")
	sessionID := fs.String("session", "", "session id (default: a new one)")
	title := fs.String("title", "", "batch title")
	if err := fs.Parse(args); err != nil {
		return err
	}
	wfID := ""
	if fs.NArg() > 0 {
		wfID = fs.Arg(0)
		if err := fs.Parse(fs.Args()[1:]); err != nil { // flags may follow the workflow id
			return err
		}
	}
	if (wfID == "") == (*pipelineID == "") {
		fs.Usage()
		return flag.ErrHelp
	}
	if n := btoi(*linesFile != "") + btoi(*csvFile != "") + btoi(len(grid) > 0); n != 1 {
		return errors.New("give exactly one of --lines, --csv, --grid")
	}
	c, err := loggedIn()
	if err != nil {
		return err
	}
	var wf workflow
	body := map[string]any{"workflow_id": wfID}
	if *pipelineID != "" {
		col := ""
		if wf, col, err = c.pipelineColumns(*pipelineID); err != nil {
			return fmt.Errorf("pipeline %s: %w", *pipelineID, err)
		}
		paramSet := false
		fs.Visit(func(f *flag.Flag) { paramSet = paramSet || f.Name == "param" })
		if !paramSet {
			*param = col
		}
		body = map[string]any{"pipeline_id": *pipelineID}
	} else if err := c.call(http.MethodGet, "/v1/studio/workflows/"+url.PathEscape(wfID), nil, http.StatusOK, &wf); err != nil {
		return fmt.Errorf("workflow %s: %w", wfID, err)
	}
	inputs, err := wf.inputs(ps)
	if err != nil {
		return err
	}
	var rows map[string]any
	switch {
	case *linesFile != "":
		rows, err = linesRows(*linesFile, *param, stdin)
	case *csvFile != "":
		rows, err = wf.csvRows(*csvFile)
	default:
		rows, err = wf.gridRows(grid)
	}
	if err != nil {
		return err
	}
	body["inputs"], body["rows"] = inputs, rows
	if *seeds > 0 {
		body["seeds"] = map[string]int{"count": *seeds}
	}
	if *sessionID != "" {
		body["session_id"] = *sessionID
	}
	if *title != "" {
		body["title"] = *title
	}
	var b batch
	if err := c.call(http.MethodPost, "/v1/studio/batches", body, http.StatusAccepted, &b); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "batch %s: %s, %d rows\n", b.ID, b.Title, b.Counts.Total)
	return c.followBatch(b.ID, stdout, stop)
}

// pipelineColumns describes a pipeline's inputs as a workflow of one node (the inputs node), so the batch columns
// `<inputs node>.<name>` are typed like workflow params; col is the first Text or Image input (the --lines default).
func (c *client) pipelineColumns(id string) (wf workflow, col string, err error) {
	var p struct {
		Title string `json:"title"`
		Nodes map[string]struct {
			Kind   string `json:"kind"`
			Inputs []struct {
				Name string `json:"name"`
				Type string `json:"type"`
			} `json:"inputs"`
		} `json:"nodes"`
	}
	if err := c.call(http.MethodGet, pipelineKind.path+"/"+url.PathEscape(id), nil, http.StatusOK, &p); err != nil {
		return wf, "", err
	}
	nodes, schemas := map[string]any{}, map[string]any{}
	for nid, n := range p.Nodes {
		if n.Kind != "inputs" {
			continue
		}
		ps := []map[string]any{}
		for _, in := range n.Inputs {
			typ := strings.ToLower(in.Type) // Text Int Float Seed Image -> the param types that parse them
			if col == "" && (typ == "text" || typ == "image") {
				col = nid + "." + in.Name
			}
			ps = append(ps, map[string]any{"name": in.Name, "type": typ})
		}
		nodes[nid], schemas["inputs:"+nid] = map[string]string{"kind": "inputs:" + nid}, map[string]any{"params": ps}
	}
	err = json.Unmarshal(mustJSON(map[string]any{"id": id, "title": p.Title, "nodes": nodes, "node_schemas": schemas}), &wf)
	return wf, col, err
}

type reviewItem struct {
	Row         int    `json:"row"`
	Gate        string `json:"gate"`
	Visit       int    `json:"visit"`
	Keep        int    `json:"keep"`
	OnReject    string `json:"on_reject"`
	RejectsLeft int    `json:"rejects_left"`
	Candidates  []struct {
		TakeID string `json:"take_id"`
		Seed   *int64 `json:"seed"`
	} `json:"candidates"`
}

func (c *client) review(id string) ([]reviewItem, error) {
	var r struct {
		Items []reviewItem `json:"items"`
	}
	err := c.call(http.MethodGet, "/v1/studio/batches/"+url.PathEscape(id)+"/review", nil, http.StatusOK, &r)
	return r.Items, err
}

// batchReview prints every row parked on a gate with its candidates.
func batchReview(args []string, stdout io.Writer) error {
	if len(args) != 1 {
		return errors.New("usage: helm batch review <batch_id>")
	}
	c, err := loggedIn()
	if err != nil {
		return err
	}
	items, err := c.review(args[0])
	if err != nil {
		return err
	}
	if len(items) == 0 {
		fmt.Fprintln(stdout, "no row is waiting for a pick")
	}
	for _, it := range items {
		left := ""
		if it.OnReject == "retry" {
			left = fmt.Sprintf(", %d left", it.RejectsLeft)
		}
		fmt.Fprintf(stdout, "row %d at %s (visit %d): pick 1 to %d; reject = %s%s\n", it.Row, it.Gate, it.Visit, it.Keep, it.OnReject, left)
		for _, cand := range it.Candidates {
			seed := "-"
			if cand.Seed != nil {
				seed = strconv.FormatInt(*cand.Seed, 10)
			}
			fmt.Fprintf(stdout, "  %s seed %s\n", cand.TakeID, seed)
		}
	}
	return nil
}

// batchPick answers the gate a row is parked on: the picked takes, or --reject.
func batchPick(args []string, stdout io.Writer) error {
	reject, rest := false, []string{}
	for _, a := range args {
		if a == "--reject" || a == "-reject" {
			reject = true
		} else {
			rest = append(rest, a)
		}
	}
	if len(rest) < 2 || reject == (len(rest) > 2) {
		return errors.New("usage: helm batch pick <batch_id> <row> (<take_id>... | --reject)")
	}
	row, err := strconv.Atoi(rest[1])
	if err != nil {
		return fmt.Errorf("row %q: not a number", rest[1])
	}
	c, err := loggedIn()
	if err != nil {
		return err
	}
	items, err := c.review(rest[0])
	if err != nil {
		return err
	}
	gate := ""
	for _, it := range items {
		if it.Row == row {
			gate = it.Gate
		}
	}
	if gate == "" {
		return fmt.Errorf("row %d is not waiting for a pick", row)
	}
	body := map[string]any{"gate": gate, "take_ids": rest[2:]}
	if reject {
		body = map[string]any{"gate": gate, "reject": true}
	}
	var r batchRow
	path := fmt.Sprintf("/v1/studio/batches/%s/rows/%d/pick", url.PathEscape(rest[0]), row)
	if err := c.call(http.MethodPost, path, body, http.StatusAccepted, &r); err != nil {
		return err
	}
	what := "picked " + strings.Join(rest[2:], ",")
	if reject {
		what = "rejected all"
	}
	fmt.Fprintf(stdout, "row %d at %s: %s; row %s\n", row, gate, what, r.Status)
	return nil
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}

func linesRows(file, param string, stdin io.Reader) (map[string]any, error) {
	r := stdin
	if file != "-" {
		f, err := os.Open(file)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		r = f
	}
	values := []string{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		if line := strings.TrimRight(sc.Text(), "\r"); strings.TrimSpace(line) != "" {
			values = append(values, line)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return map[string]any{"lines": map[string]any{"param": param, "values": values}}, nil
}

func (w workflow) csvRows(file string) (map[string]any, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	records, err := csv.NewReader(f).ReadAll()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	if len(records) < 2 {
		return nil, fmt.Errorf("%s: want a header of node.param names and at least one row", file)
	}
	columns, rows := records[0], [][]any{}
	for i, rec := range records[1:] {
		row := make([]any, len(rec))
		for j, cell := range rec {
			if cell == "" {
				continue // null: the input is left unset for this row
			}
			if row[j], err = w.value(columns[j], cell); err != nil {
				return nil, fmt.Errorf("%s row %d: %w", file, i+1, err)
			}
		}
		rows = append(rows, row)
	}
	return map[string]any{"table": map[string]any{"columns": columns, "rows": rows}}, nil
}

func (w workflow) gridRows(axes []string) (map[string]any, error) {
	out := []map[string]any{}
	for _, a := range axes {
		key, vals, ok := strings.Cut(a, "=")
		if !ok || !strings.Contains(key, ".") {
			return nil, fmt.Errorf("--grid %q: want node.param=a,b,c", a)
		}
		values := []any{}
		for _, v := range strings.Split(vals, ",") {
			typed, err := w.value(key, v)
			if err != nil {
				return nil, fmt.Errorf("--grid %w", err)
			}
			values = append(values, typed)
		}
		out = append(out, map[string]any{"param": key, "values": values})
	}
	return map[string]any{"grid": map[string]any{"axes": out}}, nil
}

// followBatch polls the batch's events, prints each row as it ends and the batch's status changes, and returns
// an error unless the batch ended done with no failed row. An interrupt stops following; the batch keeps running.
func (c *client) followBatch(id string, stdout io.Writer, stop <-chan os.Signal) error {
	since, status, printed := 0, "queued", map[int]int{} // row -> the attempt whose end was printed
	for {
		var page struct {
			Events []struct {
				Type string          `json:"type"`
				Data json.RawMessage `json:"data"`
			} `json:"events"`
			LastSeq int `json:"last_seq"`
		}
		path := fmt.Sprintf("/v1/studio/batches/%s/events?since=%d", url.PathEscape(id), since)
		if err := c.call(http.MethodGet, path, nil, http.StatusOK, &page); err != nil {
			return err
		}
		since = page.LastSeq
		for _, e := range page.Events {
			if e.Type == "row" {
				var r batchRow
				_ = json.Unmarshal(e.Data, &r)
				if (r.Status == "succeeded" || r.Status == "failed" || r.Status == "cancelled") && printed[r.Row] != r.Attempt {
					printed[r.Row] = r.Attempt
					fmt.Fprintln(stdout, r.line())
				}
				continue
			}
			var b batch
			_ = json.Unmarshal(e.Data, &b)
			if b.Status == status {
				continue
			}
			status = b.Status
			switch status {
			case "done", "failed", "stopped":
				fmt.Fprintf(stdout, "%s: %d succeeded, %d failed, %d cancelled of %d\n", status,
					b.Counts.Succeeded, b.Counts.Failed, b.Counts.Cancelled, b.Counts.Total)
				if status != "done" {
					return fmt.Errorf("batch %s %s", status, deref(b.Reason))
				}
				if b.Counts.Failed > 0 {
					return fmt.Errorf("%d of %d rows failed", b.Counts.Failed, b.Counts.Total)
				}
				return nil
			case "paused":
				fmt.Fprintf(stdout, "paused: %s\n", deref(b.Reason))
			case "waiting":
				fmt.Fprintf(stdout, "waiting: %d rows wait for a pick (helm batch review %s)\n", b.Counts.Waiting, id)
			default:
				fmt.Fprintln(stdout, status)
			}
		}
		if len(page.Events) > 0 {
			continue // there may be more than one page
		}
		select {
		case <-stop:
			return fmt.Errorf("stopped following; batch %s keeps running (helm batch-stop %[1]s cancels it)", id)
		case <-time.After(pollEvery):
		}
	}
}

func (r batchRow) line() string {
	switch {
	case r.Status == "failed":
		return fmt.Sprintf("row %d failed: %s", r.Row, deref(r.Error))
	case len(r.TakeIDs) > 0:
		return fmt.Sprintf("row %d %s take %s", r.Row, r.Status, strings.Join(r.TakeIDs, ","))
	default:
		return fmt.Sprintf("row %d %s", r.Row, r.Status)
	}
}

// batchesCmd lists one page of the caller's batches, newest first.
func batchesCmd(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("batches", flag.ContinueOnError)
	fs.SetOutput(stderr)
	limit := fs.Int("limit", 60, "batches per page (1-200)")
	cursor := fs.String("cursor", "", "next page")
	if err := fs.Parse(args); err != nil {
		return err
	}
	q := url.Values{"limit": {strconv.Itoa(*limit)}}
	if *cursor != "" {
		q.Set("cursor", *cursor)
	}
	c, err := loggedIn()
	if err != nil {
		return err
	}
	var page struct {
		Batches    []batch `json:"batches"`
		NextCursor *string `json:"next_cursor"`
	}
	if err := c.call(http.MethodGet, "/v1/studio/batches?"+q.Encode(), nil, http.StatusOK, &page); err != nil {
		return err
	}
	for _, b := range page.Batches {
		fmt.Fprintf(stdout, "%s %-7s %3d/%-3d failed %-3d %s %s\n", b.ID, b.Status, b.Counts.Succeeded, b.Counts.Total,
			b.Counts.Failed, b.CreatedAt, b.Title)
	}
	if page.NextCursor != nil {
		fmt.Fprintf(stdout, "more: --cursor %s\n", *page.NextCursor)
	}
	return nil
}

// batchStop cancels what the batch has not finished.
func batchStop(args []string, stdout io.Writer) error {
	if len(args) != 1 {
		return errors.New("usage: helm batch-stop <batch_id>")
	}
	c, err := loggedIn()
	if err != nil {
		return err
	}
	var b batch
	if err := c.call(http.MethodPost, "/v1/studio/batches/"+url.PathEscape(args[0])+"/stop", nil, http.StatusAccepted, &b); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "batch %s stopping: %d succeeded, %d failed of %d\n", b.ID, b.Counts.Succeeded, b.Counts.Failed, b.Counts.Total)
	return nil
}
