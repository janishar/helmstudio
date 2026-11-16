package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const timelineUse = "usage: helm timeline show <id> | add <id> [--track A2] <take_id>... | export <id> [-o file] [--plan] [--host h]"

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
func timelineCmd(args []string, stdout, stderr io.Writer, stop <-chan os.Signal) error {
	if len(args) < 2 {
		return errors.New(timelineUse)
	}
	fs := flag.NewFlagSet("timeline "+args[0], flag.ContinueOnError)
	fs.SetOutput(stderr)
	track := fs.String("track", "V1", "add: the track (V1, or A2…A9 for a video take's sound)")
	out := fs.String("o", "", "export: save the MP4 here")
	plan := fs.Bool("plan", false, "export: only print the export plan")
	host := fs.String("host", "", "export: run it on this host")
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
		if err := c.call(http.MethodGet, base, nil, http.StatusOK, &t); err != nil {
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
			return c.saveExport(deref(j.TakeID), out, stdout)
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
func (c *client) saveExport(takeID, out string, stdout io.Writer) error {
	fmt.Fprintf(stdout, "take %s\n", takeID)
	if out == "" {
		return nil
	}
	var t struct {
		URL string `json:"url"`
	}
	if err := c.call(http.MethodGet, "/v1/studio/takes/"+url.PathEscape(takeID), nil, http.StatusOK, &t); err != nil {
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
	if err := fetch(c.http, link.String(), out); err != nil {
		return err
	}
	fmt.Fprintln(stdout, out)
	return nil
}
