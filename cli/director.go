package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
)

type chatMessage struct {
	ID        string `json:"id"`
	Role      string `json:"role"`
	Text      string `json:"text"`
	At        string `json:"at"`
	Wake      *int   `json:"wake"`
	ToolCalls []struct {
		Tool   string          `json:"tool"`
		Args   json.RawMessage `json:"args"`
		Result json.RawMessage `json:"result"`
		Error  *string         `json:"error"`
	} `json:"tool_calls"`
	Rows []int `json:"rows"`
}

// batchChat reads the director's chat of a batch, or with a text says it to the director (API 1.15).
func batchChat(args []string, stdout io.Writer) error {
	if len(args) < 1 {
		return errors.New("usage: helm batch chat <batch_id> [text ...]")
	}
	c, err := loggedIn()
	if err != nil {
		return err
	}
	path := "/v1/studio/batches/" + url.PathEscape(args[0]) + "/chat"
	if len(args) > 1 {
		var m chatMessage
		if err := c.call(http.MethodPost, path, map[string]string{"text": strings.Join(args[1:], " ")}, http.StatusAccepted, &m); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "sent %s; the director answers in the chat (helm batch chat %s)\n", m.ID, args[0])
		return nil
	}
	var page struct {
		Director struct {
			Node     string `json:"node"`
			State    string `json:"state"`
			Wakes    int    `json:"wakes"`
			MaxWakes int    `json:"max_wakes"`
		} `json:"director"`
		Messages []chatMessage `json:"messages"`
	}
	if err := c.call(http.MethodGet, path, nil, http.StatusOK, &page); err != nil {
		return err
	}
	d := page.Director
	fmt.Fprintf(stdout, "director %s: %s, %d of %d wakes\n", d.Node, d.State, d.Wakes, d.MaxWakes)
	for _, m := range page.Messages {
		fmt.Fprintf(stdout, "%s %s: %s\n", m.At, m.Role, m.Text)
		for _, tc := range m.ToolCalls {
			out := string(tc.Result)
			if tc.Error != nil {
				out = "error: " + *tc.Error
			}
			fmt.Fprintf(stdout, "  %s %s -> %s\n", tc.Tool, compactJSON(tc.Args), compactJSON(json.RawMessage(out)))
		}
		if len(m.Rows) > 0 {
			fmt.Fprintf(stdout, "  rows %s\n", strings.Trim(fmt.Sprint(m.Rows), "[]"))
		}
	}
	return nil
}

type providerItem struct {
	Name         string   `json:"name"`
	Kind         string   `json:"kind"`
	BaseURL      string   `json:"base_url"`
	Models       []string `json:"models"`
	DefaultModel string   `json:"default_model"`
	Enabled      bool     `json:"enabled"`
	Host         string   `json:"host"`
	MemoryGB     float64  `json:"memory_gb"`
	KeySecret    string   `json:"key_secret"`
}

// providersCmd manages the local servers and remote providers pipelines may name (admin only, API 1.15). A remote
// provider's key is never sent: key_secret names a file the admin put in pipelines_service's secrets directory.
func providersCmd(args []string, stdout, stderr io.Writer) error {
	use := errors.New("usage: helm providers [set <name> <file.json> | rm <name> | test <name> [--model m]]")
	c, err := loggedIn()
	if err != nil {
		return err
	}
	base := "/v1/studio/admin/providers"
	if len(args) == 0 {
		var list struct {
			RemoteEnabled bool           `json:"remote_enabled"`
			Providers     []providerItem `json:"providers"`
		}
		if err := c.call(http.MethodGet, base, nil, http.StatusOK, &list); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "remote models on this deployment: %t\n", list.RemoteEnabled)
		for _, p := range list.Providers {
			where := "key " + p.KeySecret
			if p.Kind == "server" {
				where = fmt.Sprintf("%s %.1f GB", p.Host, p.MemoryGB)
			}
			state := "on"
			if !p.Enabled {
				state = "off"
			}
			fmt.Fprintf(stdout, "%-16s %-6s %-3s %s %s (default %s) %s\n", p.Name, p.Kind, state, p.BaseURL,
				strings.Join(p.Models, ","), p.DefaultModel, where)
		}
		return nil
	}
	fs := flag.NewFlagSet("providers "+args[0], flag.ContinueOnError)
	fs.SetOutput(stderr)
	model := fs.String("model", "", "test: also ask this model a tiny prompt")
	if len(args) < 2 {
		return use
	}
	name := args[1]
	if err := fs.Parse(args[2:]); err != nil {
		return err
	}
	switch {
	case args[0] == "set" && fs.NArg() == 1:
		body, err := os.ReadFile(fs.Arg(0))
		if err != nil {
			return err
		}
		var put map[string]any
		if err := json.Unmarshal(body, &put); err != nil {
			return fmt.Errorf("%s: %w", fs.Arg(0), err)
		}
		resp, err := c.do(context.Background(), http.MethodPut, base+"/"+url.PathEscape(name), put, nil)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		switch resp.StatusCode {
		case http.StatusCreated:
			fmt.Fprintf(stdout, "created %s\n", name)
		case http.StatusOK:
			fmt.Fprintf(stdout, "replaced %s\n", name)
		default:
			return readError(resp)
		}
	case args[0] == "rm" && fs.NArg() == 0:
		if err := c.call(http.MethodDelete, base+"/"+url.PathEscape(name), nil, http.StatusNoContent, nil); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "deleted %s\n", name)
	case args[0] == "test" && fs.NArg() == 0:
		path := base + "/" + url.PathEscape(name) + "/test"
		if *model != "" {
			path += "?model=" + url.QueryEscape(*model)
		}
		var t struct {
			OK      bool     `json:"ok"`
			Models  []string `json:"models"`
			Answer  *string  `json:"answer"`
			Error   *string  `json:"error"`
			Failure *string  `json:"failure"`
			Seconds float64  `json:"seconds"`
		}
		if err := c.call(http.MethodPost, path, nil, http.StatusOK, &t); err != nil {
			return err
		}
		if !t.OK {
			return fmt.Errorf("%s failed (%s, %.1f s): %s", name, deref(t.Failure), t.Seconds, deref(t.Error))
		}
		fmt.Fprintf(stdout, "%s ok in %.1f s; models %s\n", name, t.Seconds, strings.Join(t.Models, ","))
		if t.Answer != nil {
			fmt.Fprintf(stdout, "answer: %s\n", *t.Answer)
		}
	default:
		return use
	}
	return nil
}

// compactJSON: raw JSON on one line (text that is not JSON as it is).
func compactJSON(raw json.RawMessage) string {
	var b bytes.Buffer
	if json.Compact(&b, raw) != nil {
		return string(raw)
	}
	return b.String()
}
