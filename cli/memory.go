package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

type memoryItem struct {
	ID     string  `json:"id"`
	Kind   string  `json:"kind"`
	Key    *string `json:"key"`
	Text   string  `json:"text"`
	Agreed *bool   `json:"agreed"`
	Source struct {
		Kind    string  `json:"kind"`
		BatchID *string `json:"batch_id"`
		Row     *int    `json:"row"`
	} `json:"source"`
	CreatedAt string `json:"created_at"`
}

// memoryCmd lists, adds to and deletes from a pipeline's memory: judge examples (picks with a note) and notes.
// Batches already started keep the snapshot they took; a change is seen from the next batch.
func memoryCmd(args []string, stdout, stderr io.Writer) error {
	use := errors.New("usage: helm memory list --pipeline <id> [--kind judge_example|note] | add --pipeline <id> [--key k] <text> | rm <item_id>")
	if len(args) == 0 {
		return use
	}
	fs := flag.NewFlagSet("memory "+args[0], flag.ContinueOnError)
	fs.SetOutput(stderr)
	pipeline := fs.String("pipeline", "", "one of your pipelines")
	kind := fs.String("kind", "", "list only judge_example or note items")
	key := fs.String("key", "", "a note's key (a memory read node with that key reads it)")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	c, err := loggedIn()
	if err != nil {
		return err
	}
	switch {
	case args[0] == "list" && *pipeline != "" && fs.NArg() == 0:
		q := url.Values{"pipeline_id": {*pipeline}}
		if *kind != "" {
			q.Set("kind", *kind)
		}
		var list struct {
			Items  []memoryItem   `json:"items"`
			Counts map[string]int `json:"counts"`
		}
		if err := c.call(http.MethodGet, "/v1/studio/memory?"+q.Encode(), nil, http.StatusOK, &list); err != nil {
			return err
		}
		for _, it := range list.Items {
			fmt.Fprintln(stdout, it.line())
		}
		fmt.Fprintf(stdout, "%d judge examples, %d notes\n", list.Counts["judge_example"], list.Counts["note"])
	case args[0] == "add" && *pipeline != "" && fs.NArg() > 0:
		body := map[string]any{"pipeline_id": *pipeline, "text": strings.Join(fs.Args(), " ")}
		if *key != "" {
			body["key"] = *key
		}
		var it memoryItem
		if err := c.call(http.MethodPost, "/v1/studio/memory", body, http.StatusCreated, &it); err != nil {
			return err
		}
		fmt.Fprintln(stdout, it.ID)
	case args[0] == "rm" && fs.NArg() == 1:
		if err := c.call(http.MethodDelete, "/v1/studio/memory/"+url.PathEscape(fs.Arg(0)), nil, http.StatusNoContent, nil); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "deleted %s\n", fs.Arg(0))
	default:
		return use
	}
	return nil
}

// line: id, kind, key, where it came from, the text.
func (it memoryItem) line() string {
	from := it.Source.Kind
	if it.Source.BatchID != nil && it.Source.Row != nil {
		from = fmt.Sprintf("%s batch %s row %d", from, *it.Source.BatchID, *it.Source.Row)
	}
	if it.Agreed != nil {
		from += fmt.Sprintf(" agreed %t", *it.Agreed)
	}
	key := "-"
	if it.Key != nil {
		key = *it.Key
	}
	return fmt.Sprintf("%s %-13s %-10s %s: %s", it.ID, it.Kind, key, from, it.Text)
}
