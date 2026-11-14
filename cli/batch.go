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

// batchCmd submits a batch of one workflow (rows from --lines, --csv or --grid) and prints each row as it ends.
// Exit 1 when any row failed or the batch did not end done.
func batchCmd(args []string, stdin io.Reader, stdout, stderr io.Writer, stop <-chan os.Signal) error {
	fs := flag.NewFlagSet("batch", flag.ContinueOnError)
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
	if fs.NArg() == 0 {
		fs.Usage()
		return flag.ErrHelp
	}
	wfID := fs.Arg(0)
	if err := fs.Parse(fs.Args()[1:]); err != nil { // flags may follow the workflow id
		return err
	}
	if n := btoi(*linesFile != "") + btoi(*csvFile != "") + btoi(len(grid) > 0); n != 1 {
		return errors.New("give exactly one of --lines, --csv, --grid")
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
	body := map[string]any{"workflow_id": wfID, "inputs": inputs, "rows": rows}
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
