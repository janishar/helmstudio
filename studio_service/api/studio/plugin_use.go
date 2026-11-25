package studio

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/helmstudio/helmgo/network"
	"github.com/helmstudio/helmgo/utility"

	"github.com/helmstudio/studio_service/api/studio/dto"
	"github.com/helmstudio/studio_service/common"
)

// The Gallery's Edit (API 1.70; contracts/README.md "Plugins: takes", The Gallery's Edit): a takes plugin's tool run
// for the signed-in user, with a pass studio signs for them, as the assistant's calls are.

func nonEmpty(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}

// takesPlugin is a plugin that is on, usable, of kind assistant-tools and declares takes; else errNotFound.
func (s *Service) takesPlugin(ctx context.Context, name string) (manifest, error) {
	var rows []pluginRow
	err := common.System(ctx, s.db, func(tx pgx.Tx) (err error) {
		rows, err = readPlugins(ctx, tx, `WHERE name = $1 AND enabled AND blocked = '' AND manifest->>'kind' = 'assistant-tools'`, name)
		return err
	})
	switch {
	case err != nil:
		return manifest{}, err
	case len(rows) != 1 || !rows[0].Manifest.Takes:
		return manifest{}, errNotFound
	}
	return rows[0].Manifest, nil
}

// GetPluginUse answers GET /plugins/{plugin}.
func (s *Service) GetPluginUse(ctx context.Context, name string) (dto.PluginUse, error) {
	if !pluginNameRe.MatchString(name) {
		return dto.PluginUse{}, errNotFound
	}
	m, err := s.takesPlugin(ctx, name)
	if err != nil {
		return dto.PluginUse{}, err
	}
	out := dto.PluginUse{Name: name, Title: m.Title, Running: running(ctx, m), Tools: []string{}}
	for _, t := range m.Tools {
		if t.safe() {
			out.Tools = append(out.Tools, t.Name)
		}
	}
	return out, nil
}

// toolClient calls a plugin's tool: 30 s (the plugin's own budget), never a redirect.
var toolClient = &http.Client{Timeout: 30 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

// RunPluginTool answers POST /plugins/{plugin}/tools/{tool}: the caller's pass for a new call, the plugin's answer,
// and only the takes studio vouches for.
func (s *Service) RunPluginTool(ctx context.Context, caller common.Caller, name, tool string, body *dto.PluginToolRun) (dto.PluginToolDone, error) {
	if !pluginNameRe.MatchString(name) || !toolNameRe.MatchString(tool) {
		return dto.PluginToolDone{}, errNotFound
	}
	m, err := s.takesPlugin(ctx, name)
	if err != nil {
		return dto.PluginToolDone{}, err
	}
	if !slices.ContainsFunc(m.Tools, func(t manifestTool) bool { return t.Name == tool && t.safe() }) {
		return dto.PluginToolDone{}, errNotFound
	}
	call := utility.NewID()
	pass, _ := json.Marshal(passBody{Owner: caller.ID, Plugin: name, Call: call, Exp: time.Now().Add(PassTTL).Unix()})
	raw, _ := json.Marshal(map[string]any{"input": body.Input})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.URL+"/tools/"+tool, bytes.NewReader(raw))
	if err != nil {
		return dto.PluginToolDone{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.signer.Pass(pass))
	req.Header.Set("X-Helm-Owner", caller.ID)
	req.Header.Set("X-Helm-Call", call)
	req.Header.Set("X-Helm-Api", s.signer.BaseURL())
	failed := func(why string) error { return network.NewAPIError(http.StatusBadGateway, why, nil) }
	res, err := toolClient.Do(req)
	if err != nil {
		log.Printf("plugin %s tool %s: %v", name, tool, err)
		return dto.PluginToolDone{}, failed(m.Title + " did not answer")
	}
	defer res.Body.Close()
	var reply struct {
		Line    string   `json:"line"`
		TakeIDs []string `json:"take_ids"`
		Detail  any      `json:"detail"`
	}
	// ponytail: up to 32 MB (a frames reply carries pictures); only line, take_ids and detail are read
	_ = json.NewDecoder(io.LimitReader(res.Body, 32<<20)).Decode(&reply)
	if res.StatusCode < 200 || res.StatusCode > 299 {
		why, _ := reply.Detail.(string)
		why = strings.TrimSpace(why)
		if why == "" {
			why = m.Title + " did not answer"
		}
		if len([]rune(why)) > 200 {
			why = string([]rune(why)[:200])
		}
		if res.StatusCode == http.StatusUnprocessableEntity {
			return dto.PluginToolDone{}, network.NewUnprocessableError(why, nil)
		}
		return dto.PluginToolDone{}, failed(why)
	}
	out := dto.PluginToolDone{TakeIDs: []string{}, Line: strings.TrimSpace(reply.Line)}
	if out.Line == "" {
		out.Line = m.Title + " · " + tool
	}
	if len([]rune(out.Line)) > 300 {
		out.Line = string([]rune(out.Line)[:300])
	}
	if n := len(reply.TakeIDs); n > 0 && n <= 8 {
		made := s.madeBy(ctx, &PassRequest{OwnerID: caller.ID, Plugin: name, CallID: call, TakeIDs: reply.TakeIDs})
		if made.Error != "" {
			return dto.PluginToolDone{}, errors.New("plugin takes: " + made.Error)
		}
		out.TakeIDs = *made.TakeIDs
	}
	return out, nil
}
