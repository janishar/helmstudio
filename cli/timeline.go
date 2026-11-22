package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"
)

const timelineUse = "usage: helm timeline show <id> [--agent] | add <id> [--track A2] <take_id>... | edit <id> -f ops.json [--revision n] [--dry-run] | revisions <id> [--before n] [--limit k] | restore <id> --to n --revision n | export <id> [-o file] [--plan] [--host h]"

type tlTarget struct {
	Width      int     `json:"width"`
	Height     int     `json:"height"`
	FPS        float64 `json:"fps"`
	SampleRate int     `json:"sample_rate"`
}

func (t tlTarget) String() string {
	return fmt.Sprintf("%dx%d %sfps %dHz", t.Width, t.Height, trimFloat(t.FPS), t.SampleRate)
}

func trimFloat(f float64) string {
	return strings.TrimSuffix(strings.TrimRight(fmt.Sprintf("%.3f", f), "0"), ".")
}

type tlClip struct {
	ID      string   `json:"id"`
	TakeID  *string  `json:"take_id"`
	In      *float64 `json:"in"`
	Out     *float64 `json:"out"`
	Hold    *float64 `json:"hold"`
	At      float64  `json:"at"`
	GainDB  float64  `json:"gain_db"`
	Mute    bool     `json:"mute"`
	Offline bool     `json:"offline"`
	Trans   *struct {
		Frames int `json:"frames"`
	} `json:"transition_in"`
	Alternatives []string `json:"alternatives"`
	Shot         *struct {
		Status string  `json:"status"`
		Error  *string `json:"error"`
	} `json:"shot"`
}

type timelineDoc struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Revision  int      `json:"revision"`
	Target    tlTarget `json:"target"`
	DurationS float64  `json:"duration_s"`
	Frames    int      `json:"frames"`
	CanUndo   bool     `json:"can_undo"`
	CanRedo   bool     `json:"can_redo"`
	Tracks    []struct {
		Name   string   `json:"name"`
		GainDB float64  `json:"gain_db"`
		Mute   bool     `json:"mute"`
		Clips  []tlClip `json:"clips"`
	} `json:"tracks"`
	Takes map[string]struct {
		Title string `json:"title"`
	} `json:"takes"`
}

// timelinesCmd lists the caller's sequences.
func timelinesCmd(stdout io.Writer) error {
	c, err := loggedIn()
	if err != nil {
		return err
	}
	var list struct {
		Timelines []struct {
			ID        string   `json:"id"`
			Name      string   `json:"name"`
			Target    tlTarget `json:"target"`
			Revision  int      `json:"revision"`
			DurationS float64  `json:"duration_s"`
			Clips     int      `json:"clips"`
			UpdatedAt string   `json:"updated_at"`
		} `json:"timelines"`
	}
	if err := c.call(http.MethodGet, "/v1/studio/timelines", nil, http.StatusOK, &list); err != nil {
		return err
	}
	for _, t := range list.Timelines {
		fmt.Fprintf(stdout, "%s r%-3d %3d clips %7.2fs  %s  %s  %s\n", t.ID, t.Revision, t.Clips, t.DurationS, t.Target, t.UpdatedAt, t.Name)
	}
	return nil
}

// timelineCmd: helm timeline show|add|export.
func timelineCmd(args []string, stdin io.Reader, stdout, stderr io.Writer, stop <-chan os.Signal) error {
	if len(args) < 2 {
		return errors.New(timelineUse)
	}
	fs := flag.NewFlagSet("timeline "+args[0], flag.ContinueOnError)
	fs.SetOutput(stderr)
	track := fs.String("track", "V1", "add: the track (V1, or A2…A9 for a video take's sound)")
	out := fs.String("o", "", "export: save the MP4 here")
	plan := fs.Bool("plan", false, "export: only print the export plan")
	host := fs.String("host", "", "export: run it on this host")
	agent := fs.Bool("agent", false, "show: the agent view as JSON (every clip flat, seconds and frames)")
	file := fs.String("f", "", "edit: the ops, a JSON list or {base_revision, ops} (- = stdin)")
	dry := fs.Bool("dry-run", false, "edit: try the ops, store nothing")
	revision := fs.Int("revision", 0, "edit, restore: the revision the change was planned on (helm timeline show prints it)")
	to := fs.Int("to", 0, "restore: the kept revision to bring back")
	before := fs.Int("before", 0, "revisions: only those below this one")
	limit := fs.Int("limit", 20, "revisions: at most this many")
	pos, err := parseMixed(fs, args[1:])
	if err != nil {
		return err
	}
	if len(pos) == 0 || (args[0] != "add" && len(pos) != 1) || (args[0] == "add" && len(pos) < 2) {
		return errors.New(timelineUse)
	}
	c, err := loggedIn()
	if err != nil {
		return err
	}
	base := "/v1/studio/timelines/" + url.PathEscape(pos[0])
	var t timelineDoc
	switch args[0] {
	case "show":
		if *agent { // API 1.49
			var view json.RawMessage
			if err := c.call(http.MethodGet, base+"?view=agent", nil, http.StatusOK, &view); err != nil {
				return err
			}
			return printJSON(stdout, view)
		}
		if err := c.call(http.MethodGet, base, nil, http.StatusOK, &t); err != nil {
			return err
		}
	case "edit":
		return c.editTimeline(base, *file, *revision, *dry, stdin, stdout)
	case "revisions":
		return c.timelineRevisions(base, *before, *limit, stdout)
	case "restore":
		if *revision < 1 || *to < 1 {
			return errors.New("restore needs --to and --revision (the current one: helm timeline show prints it)")
		}
		if err := c.call(http.MethodPost, base+"/restore", map[string]int{"revision": *revision, "to": *to}, http.StatusOK, &t); err != nil {
			return err
		}
	case "add":
		body := map[string]any{"take_ids": pos[1:], "track": *track}
		if err := c.call(http.MethodPost, base+"/clips", body, http.StatusOK, &t); err != nil {
			return err
		}
	case "export":
		return c.exportTimeline(base, *out, *host, *plan, stdout, stop)
	default:
		return errors.New(timelineUse)
	}
	t.print(stdout)
	return nil
}

func (t timelineDoc) print(w io.Writer) {
	fmt.Fprintf(w, "%s  %s  revision %d  %s  %.2fs (%d frames)\n", t.ID, t.Name, t.Revision, t.Target, t.DurationS, t.Frames)
	for _, tr := range t.Tracks {
		head := tr.Name
		if tr.GainDB != 0 {
			head += fmt.Sprintf(" %+gdB", tr.GainDB)
		}
		if tr.Mute {
			head += " muted"
		}
		fmt.Fprintln(w, head)
		for _, cl := range tr.Clips {
			fmt.Fprintf(w, "  %-6s %7.2fs  %s\n", cl.ID, cl.At, cl.describe(t))
		}
	}
}

func (cl tlClip) describe(t timelineDoc) string {
	var s string
	switch {
	case cl.TakeID == nil:
		s = fmt.Sprintf("empty slot %.2fs", deref0f(cl.Hold))
	case cl.Hold != nil:
		s = fmt.Sprintf("image %s hold %.2fs", *cl.TakeID, *cl.Hold)
	default:
		s = fmt.Sprintf("video %s %.2f-%.2fs", *cl.TakeID, deref0f(cl.In), deref0f(cl.Out))
	}
	if cl.TakeID != nil {
		if tk, ok := t.Takes[*cl.TakeID]; ok {
			s += " " + tk.Title
		}
	}
	var tags []string
	if cl.Offline {
		tags = append(tags, "OFFLINE")
	}
	if cl.Trans != nil {
		tags = append(tags, fmt.Sprintf("dissolve %d", cl.Trans.Frames))
	}
	if cl.GainDB != 0 {
		tags = append(tags, fmt.Sprintf("%+gdB", cl.GainDB))
	}
	if cl.Mute {
		tags = append(tags, "muted")
	}
	if len(cl.Alternatives) > 1 {
		tags = append(tags, fmt.Sprintf("%d alternatives", len(cl.Alternatives)))
	}
	if cl.Shot != nil {
		shot := "shot " + cl.Shot.Status
		if cl.Shot.Error != nil {
			shot += ": " + *cl.Shot.Error
		}
		tags = append(tags, shot)
	}
	if len(tags) > 0 {
		s += "  [" + strings.Join(tags, ", ") + "]"
	}
	return s
}

// printJSON writes raw indented.
func printJSON(w io.Writer, raw json.RawMessage) error {
	b, err := json.MarshalIndent(raw, "", "  ")
	if err == nil {
		_, err = fmt.Fprintln(w, string(b))
	}
	return err
}

// editTimeline sends the ops of file (- = stdin; a JSON list, or {base_revision, ops}) to /edits at revision
// (--revision, else the file's base_revision) and prints the diff, the warnings and the sequence.
func (c *client) editTimeline(base, file string, revision int, dry bool, stdin io.Reader, stdout io.Writer) error {
	if file == "" {
		return errors.New("edit needs -f ops.json")
	}
	var raw []byte
	var err error
	if file == "-" {
		raw, err = io.ReadAll(stdin)
	} else {
		raw, err = os.ReadFile(file)
	}
	if err != nil {
		return err
	}
	var body struct {
		BaseRevision int               `json:"base_revision"`
		Ops          []json.RawMessage `json:"ops"`
	}
	if err := json.Unmarshal(raw, &body.Ops); err != nil {
		if err := json.Unmarshal(raw, &body); err != nil {
			return fmt.Errorf("%s: a JSON list of ops or {base_revision, ops}: %w", file, err)
		}
	}
	if revision > 0 {
		body.BaseRevision = revision
	}
	if body.BaseRevision < 1 {
		return errors.New("edit needs the revision the ops were planned on: --revision n (helm timeline show prints it)")
	}
	var out struct {
		Revision int `json:"revision"`
		Diff     struct {
			Added   []string `json:"added"`
			Removed []string `json:"removed"`
			Changed []struct {
				Clip   string                     `json:"clip"`
				Fields map[string]json.RawMessage `json:"fields"`
			} `json:"changed"`
			Tracks struct {
				Added   []string `json:"added"`
				Removed []string `json:"removed"`
			} `json:"tracks"`
			Duration [2]float64 `json:"duration"`
		} `json:"diff"`
		Warnings []string    `json:"warnings"`
		Timeline timelineDoc `json:"timeline"`
	}
	req := map[string]any{"base_revision": body.BaseRevision, "ops": body.Ops, "dry_run": dry}
	if err := c.call(http.MethodPost, base+"/edits", req, http.StatusOK, &out); err != nil {
		return err
	}
	head := fmt.Sprintf("revision %d", out.Revision)
	if dry {
		head += " (dry run: nothing stored)"
	}
	fmt.Fprintln(stdout, head)
	d := out.Diff
	var changed []string
	for _, ch := range d.Changed {
		var fields []string
		for f := range ch.Fields {
			fields = append(fields, f)
		}
		slices.Sort(fields)
		changed = append(changed, ch.Clip+" ("+strings.Join(fields, ", ")+")")
	}
	for _, part := range []struct {
		name string
		list []string
	}{{"added", d.Added}, {"removed", d.Removed}, {"changed", changed}, {"tracks added", d.Tracks.Added},
		{"tracks removed", d.Tracks.Removed}} {
		if len(part.list) > 0 {
			fmt.Fprintf(stdout, "  %s: %s\n", part.name, strings.Join(part.list, ", "))
		}
	}
	fmt.Fprintf(stdout, "  duration %.2fs -> %.2fs\n", d.Duration[0], d.Duration[1])
	for _, w := range out.Warnings {
		fmt.Fprintln(stdout, "  warning: "+w)
	}
	out.Timeline.print(stdout)
	return nil
}

// timelineRevisions prints the kept revisions, newest first.
func (c *client) timelineRevisions(base string, before, limit int, stdout io.Writer) error {
	q := url.Values{"limit": {fmt.Sprint(limit)}}
	if before > 0 {
		q.Set("before", fmt.Sprint(before))
	}
	var list struct {
		Revisions []struct {
			Revision   int     `json:"revision"`
			By         string  `json:"by"`
			Via        *string `json:"via"`
			SavedAt    string  `json:"saved_at"`
			OpsSummary *string `json:"ops_summary"`
			Clips      int     `json:"clips"`
			DurationS  float64 `json:"duration_s"`
		} `json:"revisions"`
	}
	if err := c.call(http.MethodGet, base+"/revisions?"+q.Encode(), nil, http.StatusOK, &list); err != nil {
		return err
	}
	for _, r := range list.Revisions {
		via := r.By
		if r.Via != nil {
			via += " " + *r.Via
		}
		fmt.Fprintf(stdout, "r%-4d %-11s %s %3d clips %7.2fs  %s\n", r.Revision, via, r.SavedAt, r.Clips, r.DurationS, deref(r.OpsSummary))
	}
	return nil
}

type exportReason struct {
	Code    string  `json:"code"`
	Clip    *string `json:"clip"`
	Message string  `json:"message"`
}

type mediaJob struct {
	ID       string `json:"id"`
	Status   string `json:"status"`
	Host     string `json:"host"`
	Env      string `json:"env"`
	Progress *struct {
		Frames int `json:"frames"`
		Total  int `json:"total"`
	} `json:"progress"`
	TakeID  *string `json:"take_id"`
	Failure *struct {
		Class  string `json:"class"`
		Detail string `json:"detail"`
	} `json:"failure"`
}

// exportTimeline prints the plan (--plan), or exports the current revision: it follows the job (an interrupt
// cancels it) and saves the MP4 to out when given.
func (c *client) exportTimeline(base, out, host string, planOnly bool, stdout io.Writer, stop <-chan os.Signal) error {
	body := map[string]any{}
	if host != "" {
		body["host"] = host
	}
	var p struct {
		Revision   int            `json:"revision"`
		StreamCopy bool           `json:"stream_copy"`
		Reasons    []exportReason `json:"reasons"`
		Blocked    []exportReason `json:"blocked"`
		Frames     int            `json:"frames"`
		DurationS  float64        `json:"duration_s"`
		Bytes      int64          `json:"estimated_bytes"`
		Host       *string        `json:"host"`
		Env        *string        `json:"env"`
		Hardware   *bool          `json:"hardware"`
	}
	if err := c.call(http.MethodPost, base+"/export/plan", body, http.StatusOK, &p); err != nil {
		return err
	}
	if planOnly {
		mode := "re-encode"
		if p.StreamCopy {
			mode = "stream copy (sound re-encoded)"
		}
		where := "no host"
		if p.Host != nil {
			enc := "software"
			if p.Hardware != nil && *p.Hardware {
				enc = "hardware"
			}
			where = fmt.Sprintf("%s.%s, %s", *p.Host, deref(p.Env), enc)
		}
		fmt.Fprintf(stdout, "revision %d: %s, %d frames %.2fs, about %d bytes, on %s\n", p.Revision, mode, p.Frames, p.DurationS, p.Bytes, where)
		for _, r := range p.Reasons {
			fmt.Fprintf(stdout, "  re-encode %s: %s\n", r.Code, r.Message)
		}
		for _, r := range p.Blocked {
			fmt.Fprintf(stdout, "  blocked %s: %s\n", r.Code, r.Message)
		}
		if len(p.Blocked) > 0 {
			return &exitErr{code: 1}
		}
		return nil
	}
	body["revision"] = p.Revision
	var j mediaJob
	if err := c.call(http.MethodPost, base+"/export", body, http.StatusAccepted, &j); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "export %s queued on %s.%s\n", j.ID, j.Host, j.Env)
	jobPath := "/v1/studio/media-jobs/" + url.PathEscape(j.ID)
	last := ""
	for {
		line := j.Status
		if j.Progress != nil {
			line = fmt.Sprintf("%s %d/%d frames", j.Status, j.Progress.Frames, j.Progress.Total)
		}
		if line != last {
			fmt.Fprintln(stdout, line)
			last = line
		}
		switch j.Status {
		case "succeeded":
			return c.saveExport(context.Background(), deref(j.TakeID), out, stdout)
		case "failed", "cancelled":
			if j.Failure != nil {
				return fmt.Errorf("export %s: %s: %s", j.Status, j.Failure.Class, j.Failure.Detail)
			}
			return fmt.Errorf("export %s", j.Status)
		}
		select {
		case <-stop:
			fmt.Fprintln(stdout, "cancelling")
			if err := c.call(http.MethodPost, jobPath+"/cancel", nil, http.StatusOK, &j); err != nil {
				fmt.Fprintln(stdout, "cancel:", err) // 409: it already ended; the next read shows how
			}
			stop = nil // one cancel; keep following until it ends
		case <-time.After(pollEvery):
		}
		if err := c.call(http.MethodGet, jobPath, nil, http.StatusOK, &j); err != nil {
			return err
		}
	}
}

// saveExport prints the export's take and, with out, saves its MP4 through the signed link.
func (c *client) saveExport(ctx context.Context, takeID, out string, stdout io.Writer) error {
	fmt.Fprintf(stdout, "take %s\n", takeID)
	if out == "" {
		return nil
	}
	var t struct {
		URL string `json:"url"`
	}
	if err := c.callCtx(ctx, http.MethodGet, "/v1/studio/takes/"+url.PathEscape(takeID), nil, http.StatusOK, &t); err != nil {
		return err
	}
	base, err := url.Parse(c.cfg.URL + "/")
	if err != nil {
		return err
	}
	link, err := base.Parse(t.URL)
	if err != nil {
		return err
	}
	if err := fetch(ctx, c.http, link.String(), out); err != nil {
		return err
	}
	fmt.Fprintln(stdout, out)
	return nil
}
