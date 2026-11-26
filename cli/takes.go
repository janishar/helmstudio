package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

type take struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Source    string `json:"source"`
	Title     string `json:"title"`
	Width     *int   `json:"width"`
	Height    *int   `json:"height"`
	CreatedAt string `json:"created_at"`
	Starred   bool   `json:"starred"`
	Video     *struct {
		DurationS float64 `json:"duration_s"`
	} `json:"video"` // a video take only
	Audio *struct {
		DurationS float64 `json:"duration_s"`
	} `json:"audio"` // an audio take only
	Sound string `json:"sound"` // API 1.79, an audio take's class: music, sound or voice
}

func (t take) line() string {
	size, star, length := "-", " ", "-"
	if t.Width != nil && t.Height != nil {
		size = fmt.Sprintf("%dx%d", *t.Width, *t.Height)
	}
	if t.Starred {
		star = "*"
	}
	if t.Video != nil {
		length = fmt.Sprintf("%.1fs", t.Video.DurationS)
	} else if t.Audio != nil {
		length = fmt.Sprintf("%.1fs", t.Audio.DurationS)
	}
	kind := t.Kind
	if t.Sound != "" { // API 1.79: an audio take shows its class (music, sound, voice)
		kind = t.Sound
	}
	return fmt.Sprintf("%s %s %-5s %-6s %-9s %-6s %s %s", t.ID, star, kind, t.Source, size, length, t.CreatedAt, t.Title)
}

// takes lists one page of the caller's gallery, newest first.
func takesCmd(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("takes", flag.ContinueOnError)
	fs.SetOutput(stderr)
	q := url.Values{}
	for flagName, param := range map[string]string{"kind": "kind", "source": "source", "session": "session_id", "q": "q", "cursor": "cursor"} {
		usage := "filter: " + param
		if param == "kind" {
			usage = "filter: image, video, audio, or (API 1.79) music, sound or voice"
		}
		fs.Func(flagName, usage, func(v string) error { q.Set(param, v); return nil })
	}
	starred := fs.Bool("starred", false, "only starred takes")
	limit := fs.Int("limit", 60, "takes per page (1-200)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *starred {
		q.Set("starred", "true")
	}
	q.Set("limit", strconv.Itoa(*limit))
	c, err := loggedIn()
	if err != nil {
		return err
	}
	var page struct {
		Takes      []take  `json:"takes"`
		NextCursor *string `json:"next_cursor"`
	}
	if err := c.call(http.MethodGet, "/v1/studio/takes?"+q.Encode(), nil, http.StatusOK, &page); err != nil {
		return err
	}
	for _, t := range page.Takes {
		fmt.Fprintln(stdout, t.line())
	}
	if page.NextCursor != nil {
		fmt.Fprintf(stdout, "more: --cursor %s\n", *page.NextCursor)
	}
	return nil
}

// upload sends an image to the gallery.
func uploadCmd(args []string, stdout io.Writer) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: helm upload <file>")
	}
	c, err := loggedIn()
	if err != nil {
		return err
	}
	t, err := c.upload(context.Background(), args[0])
	if err != nil {
		return err
	}
	fmt.Fprintln(stdout, t.line())
	return nil
}

// upload stores a png, jpeg or webp file as a take: start the upload, PUT the bytes to the signed link, complete it.
func (c *client) upload(ctx context.Context, file string) (take, error) {
	var t take
	f, err := os.Open(file)
	if err != nil {
		return t, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return t, err
	}
	head := make([]byte, 512)
	n, _ := io.ReadFull(f, head)
	ct := http.DetectContentType(head[:n])
	if ct != "image/png" && ct != "image/jpeg" && ct != "image/webp" {
		return t, fmt.Errorf("%s is %s: only png, jpeg and webp images can be uploaded", file, ct)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return t, err
	}
	var up struct {
		TakeID    string `json:"take_id"`
		UploadURL string `json:"upload_url"`
	}
	body := map[string]any{"filename": filepath.Base(file), "content_type": ct, "bytes": st.Size()}
	if err := c.callCtx(ctx, http.MethodPost, "/v1/studio/uploads", body, http.StatusCreated, &up); err != nil {
		return t, err
	}
	if err := c.put(ctx, up.UploadURL, f, st.Size(), ct); err != nil {
		return t, err
	}
	err = c.callCtx(ctx, http.MethodPost, "/v1/studio/uploads/"+url.PathEscape(up.TakeID)+"/complete", nil, http.StatusOK, &t)
	return t, err
}

// put sends a file's bytes to a signed files_service link (the signature is the auth: no key, no token).
func (c *client) put(ctx context.Context, uploadURL string, body io.Reader, size int64, ct string) error {
	base, err := url.Parse(c.cfg.URL + "/")
	if err != nil {
		return err
	}
	link, err := base.Parse(uploadURL)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, link.String(), body)
	if err != nil {
		return err
	}
	req.ContentLength = size
	req.Header.Set("Content-Type", ct)
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("upload: %w", readError(resp))
	}
	return nil
}

// use prints the helm run command that puts a take into a workflow's image input ("Use in…").
func useCmd(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("use", flag.ContinueOnError)
	fs.SetOutput(stderr)
	param := fs.String("param", "", "which image input (node.param), when the workflow has several")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 2 {
		return fmt.Errorf("usage: helm use [--param node.param] <take_id> <workflow_id>")
	}
	c, err := loggedIn()
	if err != nil {
		return err
	}
	body := map[string]string{"workflow_id": fs.Arg(1)}
	if *param != "" {
		body["param"] = *param
	}
	var reuse struct {
		WorkflowID string         `json:"workflow_id"`
		Inputs     map[string]any `json:"inputs"`
	}
	if err := c.call(http.MethodPost, "/v1/studio/takes/"+url.PathEscape(fs.Arg(0))+"/use", body, http.StatusOK, &reuse); err != nil {
		return err
	}
	fmt.Fprintln(stdout, runLine(reuse.WorkflowID, reuse.Inputs))
	return nil
}

// takeCmd: helm take uses <take_id> lists the workflows the take can go into ("Use in…"), each as its label and the
// helm run command with the take and the pre-filled inputs.
func takeCmd(args []string, stdout io.Writer) error {
	if len(args) != 2 || args[0] != "uses" {
		return fmt.Errorf("usage: helm take uses <take_id>")
	}
	c, err := loggedIn()
	if err != nil {
		return err
	}
	var ans struct {
		Uses []struct {
			WorkflowID string         `json:"workflow_id"`
			Title      string         `json:"title"`
			Action     string         `json:"action"`
			Inputs     map[string]any `json:"inputs"`
		} `json:"uses"`
	}
	if err := c.call(http.MethodGet, "/v1/studio/takes/"+url.PathEscape(args[1])+"/uses", nil, http.StatusOK, &ans); err != nil {
		return err
	}
	if len(ans.Uses) == 0 {
		fmt.Fprintln(stdout, "no workflow takes it")
	}
	for _, u := range ans.Uses {
		label := u.Title
		if u.Action != "" {
			label = u.Action + " · " + u.Title
		}
		fmt.Fprintf(stdout, "%s\n  %s\n", label, runLine(u.WorkflowID, u.Inputs))
	}
	return nil
}

// runLine is the helm run command for a workflow and RunInputs, each as -p: an image or rows value as its take ids,
// a media value as <take>[:in:out], a span as start:end.
func runLine(workflowID string, inputs map[string]any) string {
	line := "helm run " + workflowID
	for _, key := range slices.Sorted(maps.Keys(inputs)) {
		switch v := inputs[key].(type) {
		case map[string]any:
			switch id, ok := v["take_id"].(string); {
			case ok && v["in"] != nil: // a trimmed media value
				line += fmt.Sprintf(" -p %s=%s:%s:%s", key, id, num(v["in"]), num(v["out"]))
			case v["start"] != nil: // a span
				line += fmt.Sprintf(" -p %s=%s:%s", key, num(v["start"]), num(v["end"]))
			default: // an image or media value
				line += fmt.Sprintf(" -p %s=%s", key, strings.Join(takeIDs(v), ","))
			}
		case []any:
			line += fmt.Sprintf(" -p %s=%s", key, strings.Join(takeIDs(v), ","))
		default:
			line += fmt.Sprintf(" -p %s=%s", key, num(v))
		}
	}
	return line
}

// num prints a JSON scalar as helm run reads it back (a number without exponent or trailing zeros).
func num(v any) string {
	if f, ok := v.(float64); ok {
		return strconv.FormatFloat(f, 'f', -1, 64)
	}
	return fmt.Sprint(v)
}

// takeIDs lists the take ids of an image value ({take_id}) or a rows value (items holding one).
func takeIDs(v any) []string {
	switch v := v.(type) {
	case map[string]any:
		if id, ok := v["take_id"].(string); ok {
			return []string{id}
		}
		var ids []string
		for _, f := range v {
			ids = append(ids, takeIDs(f)...)
		}
		return ids
	case []any:
		var ids []string
		for _, item := range v {
			ids = append(ids, takeIDs(item)...)
		}
		return ids
	}
	return nil
}
