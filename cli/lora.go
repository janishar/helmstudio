package main

import (
	"context"
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

type lora struct {
	ID        string  `json:"id"`
	Source    string  `json:"source"`
	Name      string  `json:"name"`
	Family    string  `json:"family"`
	Rank      *int    `json:"rank"`
	SizeBytes int64   `json:"size_bytes"`
	KeyStyle  *string `json:"key_style"`
	State     string  `json:"state"`
	Reason    *string `json:"reason"`
}

func (l lora) line() string {
	rank := "-"
	if l.Rank != nil {
		rank = "r" + strconv.Itoa(*l.Rank)
	}
	s := fmt.Sprintf("%-64s %-6s %-8s %-12s %-5s %8.1f MB %s", l.ID, l.Source, l.State, l.Family, rank, float64(l.SizeBytes)/1e6, l.Name)
	if l.Reason != nil {
		s += " (" + *l.Reason + ")"
	}
	return s
}

// lorasCmd lists the LoRAs the caller can use: their own first, then the shared ones of the live hosts.
func lorasCmd(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("loras", flag.ContinueOnError)
	fs.SetOutput(stderr)
	q := url.Values{}
	for _, name := range []string{"source", "family", "q"} {
		fs.Func(name, "filter: "+name, func(v string) error { q.Set(name, v); return nil })
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	c, err := loggedIn()
	if err != nil {
		return err
	}
	var list struct {
		Loras []lora `json:"loras"`
	}
	if err := c.call(http.MethodGet, "/v1/studio/loras?"+q.Encode(), nil, http.StatusOK, &list); err != nil {
		return err
	}
	for _, l := range list.Loras {
		fmt.Fprintln(stdout, l.line())
	}
	return nil
}

// loraCmd: helm lora upload <file> | helm lora rm <id>.
func loraCmd(args []string, stdout io.Writer) error {
	if len(args) != 2 || (args[0] != "upload" && args[0] != "rm") {
		return fmt.Errorf("usage: helm lora upload <file.safetensors> | helm lora rm <id>")
	}
	c, err := loggedIn()
	if err != nil {
		return err
	}
	if args[0] == "rm" {
		if err := c.call(http.MethodDelete, "/v1/studio/loras/"+url.PathEscape(args[1]), nil, http.StatusNoContent, nil); err != nil {
			return err
		}
		fmt.Fprintln(stdout, "deleted", args[1])
		return nil
	}
	l, err := c.uploadLora(args[1])
	if err != nil {
		return err
	}
	fmt.Fprintln(stdout, l.line())
	return nil
}

// uploadLora stores a .safetensors file as the caller's LoRA: start, PUT to the signed link, complete (the server
// checks it there; a failed check is the error).
func (c *client) uploadLora(file string) (lora, error) {
	var l lora
	if !strings.HasSuffix(strings.ToLower(file), ".safetensors") {
		return l, fmt.Errorf("%s: only .safetensors files can be uploaded", file)
	}
	f, err := os.Open(file)
	if err != nil {
		return l, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return l, err
	}
	var up struct {
		Lora        lora   `json:"lora"`
		UploadURL   string `json:"upload_url"`
		ContentType string `json:"content_type"`
	}
	body := map[string]any{"filename": filepath.Base(file), "bytes": st.Size()}
	if err := c.call(http.MethodPost, "/v1/studio/loras", body, http.StatusCreated, &up); err != nil {
		return l, err
	}
	if err := c.put(context.Background(), up.UploadURL, f, st.Size(), up.ContentType); err != nil {
		return l, err
	}
	err = c.call(http.MethodPost, "/v1/studio/loras/"+url.PathEscape(up.Lora.ID)+"/complete", nil, http.StatusOK, &l)
	return l, err
}

// loraRows turns --lora <id>[:<strength>] flags into the rows of the workflow's one LoRA stack (the node whose
// schema has a rows param with a Lora field): {"<node>.<param>": [{<field>: {lora_id}, strength}]}.
func (w workflow) loraRows(flags []string) (map[string]any, error) {
	if len(flags) == 0 {
		return nil, nil
	}
	key, field := "", ""
	for nid, n := range w.Nodes {
		for _, p := range w.NodeSchemas[n.Kind].Params {
			for f, t := range p.Item {
				if p.Type == "rows" && t == "Lora" {
					if key != "" {
						return nil, fmt.Errorf("--lora: the workflow has several LoRA stacks; use -p <node>.rows")
					}
					key, field = nid+"."+p.Name, f
				}
			}
		}
	}
	if key == "" {
		return nil, fmt.Errorf("--lora: workflow %s has no LoRA stack", w.ID)
	}
	rows := []any{}
	for _, v := range flags {
		id, s, hasStrength := strings.Cut(v, ":")
		row := map[string]any{field: map[string]any{"lora_id": id}}
		if hasStrength {
			strength, err := strconv.ParseFloat(s, 64)
			if err != nil {
				return nil, fmt.Errorf("--lora %q: want <id>:<strength>", v)
			}
			row["strength"] = strength
		}
		if id == "" {
			return nil, fmt.Errorf("--lora %q: want <id>:<strength>", v)
		}
		rows = append(rows, row)
	}
	return map[string]any{key: rows}, nil
}
