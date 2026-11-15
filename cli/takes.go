package main

import (
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
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
}

func (t take) line() string {
	size, star := "-", " "
	if t.Width != nil && t.Height != nil {
		size = fmt.Sprintf("%dx%d", *t.Width, *t.Height)
	}
	if t.Starred {
		star = "*"
	}
	return fmt.Sprintf("%s %s %-5s %-6s %-9s %s %s", t.ID, star, t.Kind, t.Source, size, t.CreatedAt, t.Title)
}

// takes lists one page of the caller's gallery, newest first.
func takesCmd(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("takes", flag.ContinueOnError)
	fs.SetOutput(stderr)
	q := url.Values{}
	for flagName, param := range map[string]string{"kind": "kind", "source": "source", "session": "session_id", "q": "q", "cursor": "cursor"} {
		fs.Func(flagName, "filter: "+param, func(v string) error { q.Set(param, v); return nil })
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
	t, err := c.upload(args[0])
	if err != nil {
		return err
	}
	fmt.Fprintln(stdout, t.line())
	return nil
}

// upload stores a png, jpeg or webp file as a take: start the upload, PUT the bytes to the signed link, complete it.
func (c *client) upload(file string) (take, error) {
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
	if err := c.call(http.MethodPost, "/v1/studio/uploads", body, http.StatusCreated, &up); err != nil {
		return t, err
	}
	if err := c.put(up.UploadURL, f, st.Size(), ct); err != nil {
		return t, err
	}
	err = c.call(http.MethodPost, "/v1/studio/uploads/"+url.PathEscape(up.TakeID)+"/complete", nil, http.StatusOK, &t)
	return t, err
}

// put sends a file's bytes to a signed files_service link (the signature is the auth: no key, no token).
func (c *client) put(uploadURL string, body io.Reader, size int64, ct string) error {
	base, err := url.Parse(c.cfg.URL + "/")
	if err != nil {
		return err
	}
	link, err := base.Parse(uploadURL)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPut, link.String(), body)
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
	line := "helm run " + reuse.WorkflowID
	for key, v := range reuse.Inputs { // one key: the image input
		line += fmt.Sprintf(" -p %s=%s", key, strings.Join(takeIDs(v), ","))
	}
	fmt.Fprintln(stdout, line)
	return nil
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
