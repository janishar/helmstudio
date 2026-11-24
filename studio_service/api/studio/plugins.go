package studio

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/helmstudio/helmgo/network"

	"github.com/helmstudio/studio_service/api/studio/dto"
	"github.com/helmstudio/studio_service/api/studio/message"
	"github.com/helmstudio/studio_service/common"
)

// Plugins (API 1.60; contracts/README.md "Plugins"): small separate HTTP services in plugins/<name>/ with a manifest
// (contracts/plugins/manifest.schema.json). Studio reads only */plugin.json from its read-only mounts, on start and at
// Rescan, keeps them in the plugins table (off until an admin turns one on), checks a running one's health path, makes
// a provider from a model-provider plugin (Add as provider) and sends the assistant-tools plugins that are on with
// every assistant input. It never runs a plugin.

// SetPluginDirs sets the folders plugins are read from (STUDIO_PLUGINS_DIRS).
func (s *Service) SetPluginDirs(dirs []string) { s.pluginDirs = dirs }

// pluginOn is the SQL condition a provider row needs to be usable: no plugin, or its plugin on.
const pluginOn = `(plugin IS NULL OR EXISTS (SELECT 1 FROM plugins g WHERE g.name = providers.plugin AND g.enabled))`

// manifest is plugins/manifest.schema.json.
type manifest struct {
	Name        string                `json:"name"`
	Title       string                `json:"title"`
	Version     string                `json:"version"`
	Kind        string                `json:"kind"`
	Description string                `json:"description"`
	Author      string                `json:"author"`
	Source      *string               `json:"source,omitempty"`
	MayDo       []string              `json:"may_do"`
	Personal    bool                  `json:"personal,omitempty"`
	Disclaimer  *dto.PluginDisclaimer `json:"disclaimer,omitempty"`
	URL         string                `json:"url"`
	Health      string                `json:"health"`
	Start       *string               `json:"start,omitempty"`
	Provider    *manifestProvider     `json:"provider,omitempty"`
	Tools       []manifestTool        `json:"tools,omitempty"`
}

type manifestProvider struct {
	Path     string   `json:"path"`
	Models   []string `json:"models"`
	KeyHint  string   `json:"key_hint"`
	MemoryGB *float64 `json:"memory_gb,omitempty"`
	TimeoutS *int     `json:"timeout_s,omitempty"`
}

type manifestTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Input       json.RawMessage `json:"input"`
	Risk        *manifestRisk   `json:"risk"`
}

// manifestRisk is a tool's flags, each required (nil: missing).
type manifestRisk struct {
	UsesGPU         *bool `json:"uses_gpu"`
	Deletes         *bool `json:"deletes"`
	SendsOffMachine *bool `json:"sends_off_machine"`
}

var (
	pluginNameRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,22}$`)
	versionRe    = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$`)
	pluginURLRe  = regexp.MustCompile(`^https?://[^\s/?#@]+$`)
	pluginPathRe = regexp.MustCompile(`^/[A-Za-z0-9/_.-]{0,99}$`)
	toolNameRe   = regexp.MustCompile(`^[a-z][a-z0-9_]{0,23}$`)
	modelNameRe  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]{0,127}$`)
)

// problem is why a manifest is skipped (the schema's rules a scan can trip on), or "".
func (s *Service) problem(ctx context.Context, m *manifest, folder string) string {
	text := func(v string, max int) bool { return v != "" && len([]rune(v)) <= max }
	switch {
	case !pluginNameRe.MatchString(m.Name) || m.Name != folder:
		return "name must be its folder's (a-z, 0-9, -; at most 23)"
	case !text(m.Title, 60) || !versionRe.MatchString(m.Version) || !text(m.Description, 300) || !text(m.Author, 60):
		return "title, version, description and author are required"
	case len(m.MayDo) < 1 || len(m.MayDo) > 8 || slices.ContainsFunc(m.MayDo, func(v string) bool { return !text(v, 120) }):
		return "may_do needs 1-8 lines"
	case !pluginURLRe.MatchString(m.URL) || !pluginPathRe.MatchString(m.Health):
		return "url (no path) and health (a path) are required"
	case m.Disclaimer != nil && !text(m.Disclaimer.Text, 400):
		return "disclaimer.text is required"
	}
	switch m.Kind {
	case "model-provider":
		p := m.Provider
		if p == nil || m.Tools != nil || !pluginPathRe.MatchString(p.Path) || !text(p.KeyHint, 120) || len(p.Models) < 1 ||
			len(p.Models) > 16 || slices.ContainsFunc(p.Models, func(v string) bool { return !modelNameRe.MatchString(v) }) {
			return "a model-provider needs provider {path, models, key_hint} and no tools"
		}
	case "assistant-tools":
		if m.Provider != nil || len(m.Tools) < 1 || len(m.Tools) > 8 {
			return "assistant-tools needs 1-8 tools and no provider"
		}
		for _, t := range m.Tools {
			var input struct {
				Type string `json:"type"`
			}
			r := t.Risk
			if !toolNameRe.MatchString(t.Name) || !text(t.Description, 500) || r == nil || r.UsesGPU == nil ||
				r.Deletes == nil || r.SendsOffMachine == nil ||
				json.Unmarshal(t.Input, &input) != nil || input.Type != "object" {
				return "each tool needs name, description, an object input and risk"
			}
		}
	default:
		return "kind must be model-provider or assistant-tools"
	}
	return s.hostRule(ctx, "server", m.URL)
}

// readManifests reads */plugin.json in every plugin folder (the first of a name wins); a bad one is logged and skipped.
func (s *Service) readManifests(ctx context.Context) map[string]manifest {
	out := map[string]manifest{}
	for _, dir := range s.pluginDirs {
		files, _ := filepath.Glob(filepath.Join(dir, "*", "plugin.json"))
		for _, f := range files {
			folder := filepath.Base(filepath.Dir(f))
			raw, err := readLimited(f, 64<<10)
			if err != nil || len(raw) > 64<<10 {
				log.Printf("plugin %s skipped: unreadable or over 64 KB", folder)
				continue
			}
			var m manifest
			dec := json.NewDecoder(bytes.NewReader(raw))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&m); err != nil {
				log.Printf("plugin %s skipped: %v", folder, err)
				continue
			}
			if why := s.problem(ctx, &m, folder); why != "" {
				log.Printf("plugin %s skipped: %s", folder, why)
				continue
			}
			if _, seen := out[m.Name]; !seen {
				out[m.Name] = m
			}
		}
	}
	return out
}

// readLimited reads at most max+1 bytes of a file (a longer one is refused by its caller, never read whole).
func readLimited(path string, max int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, max+1))
}

// ScanPlugins reads the plugin folders into the table: a new one is added off, a known one's manifest replaced, one
// gone is dropped. It returns the names added.
func (s *Service) ScanPlugins(ctx context.Context) ([]string, error) {
	found := s.readManifests(ctx)
	added := []string{}
	err := common.System(ctx, s.db, func(tx pgx.Tx) error {
		names := []string{}
		for _, name := range slices.Sorted(maps.Keys(found)) {
			names = append(names, name)
			var inserted bool
			if err := tx.QueryRow(ctx, `INSERT INTO plugins (name, manifest) VALUES ($1, $2)
				ON CONFLICT (name) DO UPDATE SET manifest = excluded.manifest,
				updated_at = CASE WHEN plugins.manifest = excluded.manifest THEN plugins.updated_at ELSE clock_timestamp() END
				RETURNING xmax = 0`, name, toJSON(found[name])).Scan(&inserted); err != nil {
				return err
			}
			if inserted {
				added = append(added, name)
			}
		}
		_, err := tx.Exec(ctx, `DELETE FROM plugins WHERE NOT (name = ANY($1))`, names)
		return err
	})
	return added, err
}

// pluginRow is a plugins row.
type pluginRow struct {
	Name      string
	Manifest  manifest
	Enabled   bool
	UpdatedAt time.Time
}

func readPlugins(ctx context.Context, tx pgx.Tx, where string, args ...any) ([]pluginRow, error) {
	return collect[pluginRow](ctx, tx, `SELECT name, manifest, enabled, updated_at FROM plugins `+where+` ORDER BY name`, args...)
}

// healthClient checks a plugin's health path: 2 s, never a redirect.
var healthClient = &http.Client{Timeout: 2 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

// running reports whether a plugin's health path answers 2xx now.
func running(ctx context.Context, m manifest) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.URL+m.Health, nil)
	if err != nil {
		return false
	}
	res, err := healthClient.Do(req)
	if err != nil {
		return false
	}
	res.Body.Close()
	return res.StatusCode >= 200 && res.StatusCode < 300
}

// pluginDTOs is rows as the caller sees them: the on ones' health checked at once, the caller's own provider of each.
func (s *Service) pluginDTOs(ctx context.Context, caller common.Caller, rows []pluginRow) ([]dto.Plugin, error) {
	out := make([]dto.Plugin, len(rows))
	var wg sync.WaitGroup
	for i, r := range rows {
		m := r.Manifest
		p := dto.Plugin{Name: r.Name, Title: m.Title, Version: m.Version, Kind: m.Kind, Description: m.Description,
			Author: m.Author, Source: m.Source, MayDo: m.MayDo, Personal: m.Personal, Disclaimer: m.Disclaimer, URL: m.URL,
			Start: m.Start, Enabled: r.Enabled, Status: "off", UpdatedAt: r.UpdatedAt}
		if m.Provider != nil {
			p.Models, p.KeyHint = m.Provider.Models, &m.Provider.KeyHint
		}
		for _, t := range m.Tools {
			p.Tools = append(p.Tools, dto.PluginTool{Name: t.Name, Description: t.Description, Risk: dto.PluginRisk{
				UsesGPU: *t.Risk.UsesGPU, Deletes: *t.Risk.Deletes, SendsOffMachine: *t.Risk.SendsOffMachine}})
		}
		out[i] = p
		if r.Enabled {
			wg.Go(func() {
				out[i].Status = map[bool]string{true: "running", false: "not_running"}[running(ctx, m)]
			})
		}
	}
	err := s.tx(ctx, caller, func(tx pgx.Tx) error { // the providers policy: the caller's own or everyone's
		for i := range out {
			name, err := one[struct{ Name string }](ctx, tx, `SELECT name FROM providers WHERE plugin = $1
				ORDER BY owner_id NULLS LAST, name LIMIT 1`, out[i].Name)
			if err == nil {
				out[i].Provider = &name.Name
			} else if !errors.Is(err, errNotFound) {
				return err
			}
		}
		return nil
	})
	wg.Wait()
	return out, err
}

// ListPlugins answers GET /admin/plugins.
func (s *Service) ListPlugins(ctx context.Context, caller common.Caller) (dto.PluginList, error) {
	if !caller.IsAdmin() {
		return dto.PluginList{}, errAdmin
	}
	var rows []pluginRow
	err := common.System(ctx, s.db, func(tx pgx.Tx) (err error) {
		rows, err = readPlugins(ctx, tx, "")
		return err
	})
	if err != nil {
		return dto.PluginList{}, err
	}
	list, err := s.pluginDTOs(ctx, caller, rows)
	return dto.PluginList{Plugins: list}, err
}

// RescanPlugins answers POST /admin/plugins/rescan.
func (s *Service) RescanPlugins(ctx context.Context, caller common.Caller) (dto.PluginRescan, error) {
	if !caller.IsAdmin() {
		return dto.PluginRescan{}, errAdmin
	}
	added, err := s.ScanPlugins(ctx)
	if err != nil {
		return dto.PluginRescan{}, err
	}
	list, err := s.ListPlugins(ctx, caller)
	return dto.PluginRescan{Plugins: list.Plugins, New: added}, err
}

// PutPlugin answers PUT /admin/plugins/{plugin}: on or off.
func (s *Service) PutPlugin(ctx context.Context, caller common.Caller, name string, body *dto.PluginPut) (dto.Plugin, error) {
	if !caller.IsAdmin() {
		return dto.Plugin{}, errAdmin
	}
	var rows []pluginRow
	err := common.System(ctx, s.db, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE plugins SET enabled = $2, updated_at = clock_timestamp() WHERE name = $1`,
			name, *body.Enabled); err != nil {
			return err
		}
		var err error
		rows, err = readPlugins(ctx, tx, "WHERE name = $1", name)
		return err
	})
	if err != nil {
		return dto.Plugin{}, err
	} else if len(rows) == 0 {
		return dto.Plugin{}, errNotFound
	}
	log.Printf("plugin %s turned %s by %s", name, map[bool]string{true: "on", false: "off"}[*body.Enabled], caller.ID)
	list, err := s.pluginDTOs(ctx, caller, rows)
	return list[0], err
}

// RemovePlugin answers DELETE /admin/plugins/{plugin}: the plugin and every provider made from it (409 while an
// unfinished batch names one); the folder stays.
func (s *Service) RemovePlugin(ctx context.Context, caller common.Caller, name string) error {
	if !caller.IsAdmin() {
		return errAdmin
	}
	return common.System(ctx, s.db, func(tx pgx.Tx) error {
		if _, err := one[struct{ Name string }](ctx, tx, `SELECT name FROM plugins WHERE name = $1 FOR UPDATE`, name); err != nil {
			return err
		}
		var used *string
		if err := tx.QueryRow(ctx, `SELECT min(p.name) FROM providers p JOIN batches b ON p.name = ANY(b.providers)
			WHERE p.plugin = $1 AND b.status IN ('queued', 'running', 'paused')`, name).Scan(&used); err != nil {
			return err
		} else if used != nil {
			return network.NewConflictError(*used+" is in use by an unfinished batch", nil)
		}
		if _, err := tx.Exec(ctx, `DELETE FROM providers WHERE plugin = $1`, name); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `DELETE FROM plugins WHERE name = $1`, name)
		log.Printf("plugin %s removed by %s", name, caller.ID)
		return err
	})
}

// nameFree is 409 when a provider of that name exists that the caller cannot see or that belongs to another plugin
// (plugin "" = the admin form): the providers policy hides another admin's Only-you provider.
func (s *Service) nameFree(ctx context.Context, caller common.Caller, name, plugin string) error {
	return common.System(ctx, s.db, func(tx pgx.Tx) error {
		var owner, from *string
		err := tx.QueryRow(ctx, `SELECT owner_id::text, plugin FROM providers WHERE name = $1`, name).Scan(&owner, &from)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return nil
		case err != nil:
			return err
		case owner != nil && *owner != caller.ID, plugin != "" && deref(from) != plugin:
			return network.NewConflictError(name+" is taken; pick another name", nil)
		}
		return nil
	})
}

// AddPluginProvider answers POST /admin/plugins/{plugin}/provider (Add as provider): pipelines checks the key on the
// plugin's /models and writes it, then the provider `<plugin>-<8 hex of the caller's id>` is created or replaced; a
// personal plugin's is Only you. The key is never logged.
func (s *Service) AddPluginProvider(ctx context.Context, caller common.Caller, name string, body *dto.PluginProviderCreate) (dto.Provider, error) {
	if !caller.IsAdmin() {
		return dto.Provider{}, errAdmin
	}
	var rows []pluginRow
	if err := common.System(ctx, s.db, func(tx pgx.Tx) (err error) {
		rows, err = readPlugins(ctx, tx, "WHERE name = $1", name)
		return err
	}); err != nil {
		return dto.Provider{}, err
	} else if len(rows) == 0 {
		return dto.Provider{}, errNotFound
	}
	m := rows[0].Manifest
	switch {
	case m.Kind != "model-provider" || m.Provider == nil:
		return dto.Provider{}, network.NewConflictError(m.Title+" is not a model provider", nil)
	case !rows[0].Enabled:
		return dto.Provider{}, network.NewConflictError("turn "+m.Title+" on first", nil)
	}
	model := cmp.Or(deref(body.DefaultModel), m.Provider.Models[0])
	if !slices.Contains(m.Provider.Models, model) {
		return dto.Provider{}, network.NewUnprocessableError(fmt.Sprintf("%s is not one of %s's models", model, m.Title), nil)
	}
	hosts := []string{}
	for _, h := range s.registry.Hosts() {
		if !slices.Contains(hosts, h.Host) {
			hosts = append(hosts, h.Host)
		}
	}
	slices.Sort(hosts)
	host := deref(body.Host)
	if host == "" && len(hosts) > 0 {
		host = hosts[0]
	}
	if !slices.Contains(hosts, host) {
		return dto.Provider{}, network.NewUnprocessableError(cmp.Or(host, "a host")+" is not a known host", nil)
	}
	provider := name + "-" + strings.ReplaceAll(caller.ID, "-", "")[:8]
	if err := s.nameFree(ctx, caller, provider, name); err != nil {
		return dto.Provider{}, err
	}
	baseURL, secret := m.URL+m.Provider.Path, "plugin-"+provider
	reply, err := s.askKeyWrite(ctx, message.ProviderKeyWriteRequest{Op: "save", KeySecret: secret, Key: body.Key,
		Provider: map[string]any{"kind": "server", "api": "openai", "base_url": baseURL, "timeout_s": 15}})
	if err != nil {
		return dto.Provider{}, err
	}
	switch {
	case reply.Check == "refused":
		log.Printf("plugin %s: key refused by the plugin, not saved, by %s", name, caller.ID)
		return dto.Provider{}, network.NewUnprocessableError("The key doesn't match: copy it again from the plugin's .env", nil)
	case !reply.Saved:
		return dto.Provider{}, network.NewUnprocessableError(m.Title+" did not answer: start it, then Try again", nil)
	}
	log.Printf("plugin %s: key %s saved by %s", name, secret, caller.ID)
	title := cmp.Or(deref(body.Title), m.Title)
	if body.Title == nil && m.Personal {
		title = m.Title + " (personal)"
	}
	var owner, preset *string
	if m.Personal {
		owner = &caller.ID
	}
	if i := slices.IndexFunc(presets(), func(p dto.ProviderPreset) bool { return deref(p.Plugin) == name }); i >= 0 {
		preset = &presets()[i].ID // its thinking rules (presets.json `plugin`)
	}
	var out dto.Provider
	err = s.tx(ctx, caller, func(tx pgx.Tx) error {
		row, err := one[providerRow](ctx, tx, `INSERT INTO providers (name, kind, title, base_url, models, default_model,
			enabled, timeout_s, host, memory_gb, key_secret, owner_id, plugin, preset) VALUES ($1, 'server', $2, $3, $4, $5, true,
			$6, $7, $8, $9, $10, $11, $12)
			ON CONFLICT (name) DO UPDATE SET title = excluded.title, base_url = excluded.base_url, models = excluded.models,
			default_model = excluded.default_model, enabled = true, timeout_s = excluded.timeout_s, host = excluded.host,
			memory_gb = excluded.memory_gb, key_secret = excluded.key_secret, preset = excluded.preset,
			updated_at = clock_timestamp()
			RETURNING `+providerCols, provider, title, baseURL, m.Provider.Models, model,
			cmp.Or(deref(m.Provider.TimeoutS), 180), host, cmp.Or(deref(m.Provider.MemoryGB), 0.5), secret, owner, name, preset)
		out = row.dto()
		return err
	})
	return out, err
}

// assistantPlugins is a turn's `plugins`: the assistant-tools plugins that are on, as studio read them.
func assistantPlugins(ctx context.Context, tx pgx.Tx) ([]message.TurnPlugin, error) {
	rows, err := readPlugins(ctx, tx, `WHERE enabled AND manifest->>'kind' = 'assistant-tools'`)
	out := []message.TurnPlugin{}
	for _, r := range rows {
		if len(out) < 16 {
			out = append(out, message.TurnPlugin{Name: r.Name, Title: r.Manifest.Title, URL: r.Manifest.URL,
				Tools: toJSON(r.Manifest.Tools)})
		}
	}
	return out, err
}
