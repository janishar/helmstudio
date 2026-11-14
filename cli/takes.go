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

// upload sends an image to the gallery: start the upload, PUT the bytes to the signed link, complete it.
func uploadCmd(args []string, stdout io.Writer) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: helm upload <file>")
	}
	f, err := os.Open(args[0])
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	head := make([]byte, 512)
	n, _ := io.ReadFull(f, head)
	ct := http.DetectContentType(head[:n])
	if ct != "image/png" && ct != "image/jpeg" && ct != "image/webp" {
		return fmt.Errorf("%s is %s: only png, jpeg and webp images can be uploaded", args[0], ct)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	c, err := loggedIn()
	if err != nil {
		return err
	}
	var up struct {
		TakeID    string `json:"take_id"`
		UploadURL string `json:"upload_url"`
	}
	body := map[string]any{"filename": filepath.Base(args[0]), "content_type": ct, "bytes": st.Size()}
	if err := c.call(http.MethodPost, "/v1/studio/uploads", body, http.StatusCreated, &up); err != nil {
		return err
	}
	base, err := url.Parse(c.cfg.URL + "/")
	if err != nil {
		return err
	}
	link, err := base.Parse(up.UploadURL)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPut, link.String(), f) // the signature is the auth: no key, no token
	if err != nil {
		return err
	}
	req.ContentLength = st.Size()
	req.Header.Set("Content-Type", ct)
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("upload: %w", readError(resp))
	}
	var t take
	if err := c.call(http.MethodPost, "/v1/studio/uploads/"+url.PathEscape(up.TakeID)+"/complete", nil, http.StatusOK, &t); err != nil {
		return err
	}
	fmt.Fprintln(stdout, t.line())
	return nil
}
