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
	"strings"
)

// docKeys are the WorkflowDoc fields; a workflow file is a WorkflowDoc plus `id` and `version` (one's own, from
// helm workflow get) or `based_on` (a template: saving it creates a copy).
var docKeys = []string{"title", "family", "env", "nodes", "studio"}

type problem struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Blocks  string `json:"blocks"`
	Node    string `json:"node"`
	Port    string `json:"port"`
	Param   string `json:"param"`
}

type savedWorkflow struct {
	ID       string    `json:"id"`
	Version  int       `json:"version"`
	Problems []problem `json:"problems"`
}

func workflows(stdout io.Writer) error {
	c, err := loggedIn()
	if err != nil {
		return err
	}
	var list struct {
		Workflows []struct {
			ID      string `json:"id"`
			Title   string `json:"title"`
			Env     string `json:"env"`
			Source  string `json:"source"`
			Version int    `json:"version"`
		} `json:"workflows"`
	}
	if err := c.call(http.MethodGet, "/v1/studio/workflows", nil, http.StatusOK, &list); err != nil {
		return err
	}
	for _, w := range list.Workflows {
		v := ""
		if w.Source == "mine" {
			v = fmt.Sprintf("v%d", w.Version)
		}
		fmt.Fprintf(stdout, "%-36s %-8s %-4s %-8s %s\n", w.ID, w.Source, v, w.Env, w.Title)
	}
	return nil
}

func workflowCmd(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("workflow", flag.ContinueOnError)
	fs.SetOutput(stderr)
	title := fs.String("title", "", "copy: the new title (default: <title> copy)")
	if len(args) < 2 {
		fmt.Fprint(stderr, usage)
		return flag.ErrHelp
	}
	sub, arg := args[0], args[1]
	if err := fs.Parse(args[2:]); err != nil {
		return err
	}
	c, err := loggedIn()
	if err != nil {
		return err
	}
	path := "/v1/studio/workflows/" + url.PathEscape(arg)
	switch sub {
	case "get":
		return c.getWorkflow(path, stdout)
	case "save":
		return c.saveWorkflow(arg, stdout)
	case "validate":
		return c.validateWorkflow(arg, stdout)
	case "copy":
		body := map[string]string{"based_on": arg}
		if *title != "" {
			body["title"] = *title
		}
		var w savedWorkflow
		if err := c.call(http.MethodPost, "/v1/studio/workflows", body, http.StatusCreated, &w); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "%s\n", w.ID)
		return nil
	case "rm":
		if err := c.call(http.MethodDelete, path, nil, http.StatusNoContent, nil); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "deleted %s\n", arg)
		return nil
	}
	fmt.Fprintf(stderr, "unknown workflow command %q\n%s", sub, usage)
	return flag.ErrHelp
}

// getWorkflow prints the workflow as a file helm workflow save takes back.
func (c *client) getWorkflow(path string, stdout io.Writer) error {
	var w map[string]json.RawMessage
	if err := c.call(http.MethodGet, path, nil, http.StatusOK, &w); err != nil {
		return err
	}
	out := map[string]json.RawMessage{}
	for _, k := range docKeys {
		if v, ok := w[k]; ok {
			out[k] = v
		}
	}
	if string(w["source"]) == `"mine"` {
		out["id"], out["version"] = w["id"], w["version"]
	} else {
		out["based_on"] = w["id"]
	}
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "%s\n", b)
	return err
}

// readWorkflowFile splits a workflow file into its document and its id, version and based_on.
func readWorkflowFile(file string) (doc map[string]json.RawMessage, id string, version int, basedOn string, err error) {
	b, err := os.ReadFile(file)
	if err != nil {
		return nil, "", 0, "", err
	}
	var all map[string]json.RawMessage
	if err := json.Unmarshal(b, &all); err != nil {
		return nil, "", 0, "", fmt.Errorf("%s: %w", file, err)
	}
	doc = map[string]json.RawMessage{}
	for _, k := range docKeys {
		if v, ok := all[k]; ok {
			doc[k] = v
		}
	}
	for k, dst := range map[string]any{"id": &id, "version": &version, "based_on": &basedOn} {
		if v, ok := all[k]; ok {
			if err := json.Unmarshal(v, dst); err != nil {
				return nil, "", 0, "", fmt.Errorf("%s: %s: %w", file, k, err)
			}
		}
	}
	return doc, id, version, basedOn, nil
}

// saveWorkflow replaces one's own workflow (the file has id and version) or creates one (based_on kept).
func (c *client) saveWorkflow(file string, stdout io.Writer) error {
	doc, id, version, basedOn, err := readWorkflowFile(file)
	if err != nil {
		return err
	}
	method, path, want := http.MethodPost, "/v1/studio/workflows", http.StatusCreated
	switch {
	case id != "" && version > 0:
		method, path, want = http.MethodPut, path+"/"+url.PathEscape(id), http.StatusOK
		doc["version"] = mustJSON(version)
	case id != "":
		return errors.New(file + ": an id needs its version (helm workflow get it again)")
	case basedOn != "":
		doc["based_on"] = mustJSON(basedOn)
	}
	resp, err := c.do(context.Background(), method, path, doc, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnprocessableEntity {
		var e struct {
			Detail   string    `json:"detail"`
			Problems []problem `json:"problems"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&e)
		printProblems(e.Problems, stdout)
		return &apiError{status: resp.StatusCode, detail: e.Detail}
	}
	if resp.StatusCode != want {
		return readError(resp)
	}
	var w savedWorkflow
	if err := json.NewDecoder(resp.Body).Decode(&w); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "saved %s version %d\n", w.ID, w.Version)
	printProblems(w.Problems, stdout)
	if method == http.MethodPost {
		fmt.Fprintf(stdout, "to keep editing it: helm workflow get %s > %s\n", w.ID, file)
	}
	return nil
}

// validateWorkflow prints every problem; an error when there is any.
func (c *client) validateWorkflow(file string, stdout io.Writer) error {
	doc, _, _, _, err := readWorkflowFile(file)
	if err != nil {
		return err
	}
	var v struct {
		Problems []problem `json:"problems"`
		CanSave  bool      `json:"can_save"`
		CanRun   bool      `json:"can_run"`
	}
	if err := c.call(http.MethodPost, "/v1/studio/workflows/validate", doc, http.StatusOK, &v); err != nil {
		return err
	}
	printProblems(v.Problems, stdout)
	switch {
	case v.CanRun:
		fmt.Fprintln(stdout, "ok: it can be saved and run")
		return nil
	case v.CanSave:
		return fmt.Errorf("%d problems: it can be saved, not run", len(v.Problems))
	}
	return fmt.Errorf("%d problems: it cannot be saved", len(v.Problems))
}

// printProblems prints one line per problem: what it blocks, where, the code and the message.
func printProblems(ps []problem, stdout io.Writer) {
	for _, p := range ps {
		where := "-"
		if p.Node != "" {
			where = strings.Join(append([]string{p.Node}, nonEmpty(p.Port, p.Param)...), ".")
		}
		fmt.Fprintf(stdout, "%-4s %-20s %s: %s\n", p.Blocks, where, p.Code, p.Message)
	}
}

func nonEmpty(ss ...string) []string {
	var out []string
	for _, s := range ss {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}
